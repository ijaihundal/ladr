# Build a Tree-of-Thoughts Ladder

A worked tutorial: from a one-shot answer to a multi-branch Tree-of-Thoughts with gating and synthesis. Walks through the canonical LADR pattern step by step. By the end you will understand nodes, fields, gates, and the exit step.

## What we are building

A Tree-of-Thoughts (ToT) ladder that:

1. **Drafts** N independent answers in parallel (one per node).
2. **Evaluates** each draft; emits a per-branch verdict (1 = strong, 0 = weak).
3. **Prunes** weak branches via an `if` gate (the surviving nodes pass through).
4. **Synthesizes** the survivors into a single final answer.

By the end you'll understand: `nodes`, fields (`text`, `ingest`, `multi_ingest`, `nodeInfo`, `knobInfo`), gates (`if`), and the exit step.

This is the canonical ToT pattern. The same shape appears in the `gated-tot` example ladder and is the foundation for the more complex Crucible reference ladder.

## Prerequisites

Complete [Hello World](../tutorials/hello-world.md) first. You should have a running LADR instance, a configured provider (OpenAI is fine), and have called at least one ladder via the API.

You should also understand how LADR assembles prompts from declared fields. Quick refresher: every step declares a list of fields. At runtime, each field resolves to a value and renders as a line in the user-role message:

```
FieldName: resolved value
```

After all field lines, the runtime appends the system instruction:

```
[System Instruction]
<systemPrompt value>
```

There is **no string interpolation** in systemPrompt text. The LLM sees both the field lines and the system instruction; it does not see `{FieldName}` substituted into the prompt. Keep this in mind as you read the examples below.

## Step 1: The draft step

Create a new ladder in Foundry. Set `name: "Gated Tree of Thought"`, `exit: answer`, `allowedTargets: { strategy: universal }`.

Add the first step:

```yaml
- id: draft
  name: Draft
  type: normal
  nodes: "branches"
  fields:
    - { name: Context, type: text, from: input.context }
    - { name: Branch Number, type: nodeInfo }
    - { name: Total Branches, type: knobInfo, from: branches }
  systemPrompt: |
    You are branch {Branch Number} of {Total Branches}.
    Produce an independent first-pass answer to the user's Context.
    Vary your angle based on your branch number.
```

Notes:

- `nodes: "branches"` fans out into N parallel LLM calls, one per node. The value comes from the `branches` knob, declared later.
- `Context` reads `input.context`; the caller's last message.
- `Branch Number` is `type: nodeInfo`; the runtime injects the current node's 1-indexed number. Node 1 sees `Branch Number: 1`; node 5 sees `Branch Number: 5`. No `from` needed.
- `Total Branches` is `type: knobInfo` with `from: branches`; the runtime injects the knob's current value. If the caller set `branches: 5`, every node sees `Total Branches: 5`.
- The systemPrompt text `{Branch Number}` and `{Total Branches}` is **literal text** in the prompt; the LLM is expected to read the corresponding field lines (`Branch Number: 3`, `Total Branches: 5`) and interpret. Most LLMs handle this naturally; you can also write the prompt more explicitly (`"Look at the 'Branch Number' and 'Total Branches' fields above."`).

The prompt the LLM actually sees:

```
Context: What is the best approach to quantum error correction?

Branch Number: 3

Total Branches: 5

[System Instruction]
You are branch {Branch Number} of {Total Branches}.
Produce an independent first-pass answer to the user's Context.
Vary your angle based on your branch number.
```

Most models read the field lines above and correctly interpret `{Branch Number}` as referring to the value `3`. This is a common LADR pattern.

## Step 2: The evaluate step

Now add a second step that runs N independent evaluators, one per draft node. Each evaluator scores the corresponding draft.

```yaml
- id: evaluate
  name: Evaluate
  type: normal
  nodes: "branches"
  fields:
    - { name: Draft, type: ingest, from: { stepId: draft, loopRef: current } }
    - { name: Branch Number, type: nodeInfo }
  systemPrompt: |
    You are evaluating the draft in the Draft field.
    Output a single integer: 1 if the draft is strong, 0 if it is weak.
    Output the integer alone, nothing else.
  if:
    integerEquals: 1
    then: continue
    else: abort
```

Key things:

- `nodes: "branches"`; same fanout as draft. Each evaluate node corresponds to one draft node.
- `Draft` is `type: ingest` with `from: { stepId: draft, loopRef: current }`. The default `nodeRef` is the matching node number — node 1 of evaluate reads node 1 of draft, node 2 reads node 2, and so on. This is per-node routing.
- `Branch Number` injects the current node number, useful for the prompt.
- `if: { integerEquals: 1 }` is the gate. Each evaluate node's output is parsed as an integer; nodes that don't emit `1` are pruned.
- Default actions: `then: continue` and `else: abort`. After per-node pruning, if any survivors remain the `then` fires (advance to the next step); if no survivors, the `else` fires (terminate the ladder).

What this gives you: of N evaluate nodes, only the ones that scored their draft as `1` survive. The other drafts are silently dropped. Downstream consumers see only survivors.

## Step 3: A richer gate via JSON

The integer gate above is brittle (a single word from the LLM can break it). The more robust pattern is to ask for structured JSON and gate on a schema.

```yaml
- id: evaluate
  name: Evaluate
  type: normal
  nodes: "branches"
  fields:
    - { name: Draft, type: ingest, from: { stepId: draft, loopRef: current } }
  systemPrompt: |
    Evaluate the draft in the Draft field.
    Output JSON: {"score": <1-5>, "verdict": "strong"|"weak", "issues": [...]}
  if:
    jsonMatches:
      type: object
      required: [score, verdict]
      properties:
        score: { type: integer, minimum: 4 }
        verdict: { type: string, enum: [strong] }
      additionalProperties: false
    then: continue
    else: abort
```

The runtime extracts the first JSON block from each evaluate node's output, validates against the schema. Only nodes that emitted `{"score": >= 4, "verdict": "strong"}` survive.

This pattern is the most common gate in production LADR ladders. The schema is strict; the LLM has to emit well-formed JSON to pass. Anything malformed or off-spec is silently pruned.

## Step 4: The synthesis step

Add the step that merges the surviving drafts:

```yaml
- id: synthesize
  name: Synthesize
  type: normal
  fields:
    - name: Surviving Drafts
      type: multi_ingest
      from:
        - { stepId: draft, loopRef: current, nodeRef: accumulate }
  systemPrompt: |
    Several candidate drafts are listed under "Surviving Drafts" above.
    Synthesize them into one coherent final answer.
    Pull the strongest points from each; resolve contradictions in favor
    of the majority view; output only the final answer.
```

`type: multi_ingest` with `nodeRef: accumulate` pulls **all draft nodes** into the prompt. Because the evaluate gate pruned the weak drafts, the synthesize step sees only the survivors — but it reads from the `draft` step, not from `evaluate`.

Wait, that's a subtle point worth being explicit about. `multi_ingest` with `nodeRef: accumulate` reads **all** nodes of the source step, regardless of whether downstream gates pruned anything. To get only the surviving drafts, you have two options:

**Option A: Read from the gated step.** `evaluate` is the gated step; its outputs are the verdicts. Read those:

```yaml
- name: Surviving Evaluations
  type: multi_ingest
  from:
    - { stepId: evaluate, loopRef: current, nodeRef: accumulate }
```

This pulls all surviving evaluate outputs (the verdicts). You can then look up which drafts they correspond to via the matching node numbers.

**Option B: Make the evaluate step ingest the draft and emit the draft text in its output.** Then the synthesize step reads from evaluate.

```yaml
- id: evaluate
  nodes: "branches"
  fields:
    - { name: Draft, type: ingest, from: { stepId: draft, loopRef: current } }
  systemPrompt: |
    If the draft is strong, output the draft text verbatim.
    If the draft is weak, output "REJECT".
  if:
    contains: REJECT
    then: abort
    else: continue
```

Now evaluate's outputs are either the (strong) draft text or pruned. `multi_ingest` from `evaluate` gives synthesize only the strong drafts.

This is the more common pattern. We'll use it going forward.

## Step 5: The answer step + exit

One final step to format the synthesized answer:

```yaml
- id: answer
  name: Answer
  type: normal
  fields:
    - { name: Synthesized, type: ingest, from: { stepId: synthesize, loopRef: current } }
  systemPrompt: "Return the synthesized answer in the response format."
```

Set the ladder's `exit: answer`. The output of this step is what the API returns to the caller in the OpenAI-compatible response.

Why a separate `answer` step instead of just exiting on `synthesize`? Two reasons:

- **Format normalization.** If you want to strip the model's preamble, enforce a specific output shape, or apply a final formatting pass, the `answer` step is the place.
- **Future-proofing.** Adding a step after `synthesize` lets you add gates, writes, or jumps without re-architecting the exit.

For a minimal ladder, you can skip this and just exit on `synthesize`. The pattern is the same.

## Step 6: Declare the knob

Both `draft` and `evaluate` reference the `branches` knob via `nodes: "branches"`. Declare it at the top level:

```yaml
knobs:
  branches:
    name: Branches
    type: nodes                              # semantic category; controls fan-out
    input: slider                            # UI: 3-5 discrete ticks
    steps:
      - { title: Low,  value: 3, default: true }
      - { title: Mid,  value: 5 }
      - { title: High, value: 7 }
```

The knob has three ticks: 3 (default), 5, 7. Callers pick at request time via the API:

```bash
curl http://localhost:8788/v1/local/gated-tot/chat/completions \
  -H "Authorization: Bearer $LADR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "..."}],
    "knobs": { "branches": 7 }
  }'
```

A caller who picks 7 gets a wider search (more candidates, more cost). A caller who picks 3 gets a faster, cheaper call.

## Step 7: Set the execution budget

Before saving, set a budget so an aggressive caller cannot blow your spend:

```yaml
executionBudget:
  maxSpend: 0.50          # dollars; primary safety net
  maxHops: 50
  maxLlmCalls: 30
```

Plenty for a 7-branch ToT (7 draft calls + 7 evaluate calls + 1 synthesize + 1 answer = 16 calls worst-case), but bounded. The runtime hard-kills the execution if any ceiling is hit, returning the best-so-far output.

Budget is non-negotiable for published ladders. The default $5 is generous; tighten it for public free-tier listings to set caller expectations.

## Step 8: The full config

```yaml
name: Gated Tree of Thought
allowedTargets: { strategy: universal }
exit: answer
knobs:
  branches:
    name: Branches
    type: nodes
    input: slider
    steps:
      - { title: Low,  value: 3, default: true }
      - { title: Mid,  value: 5 }
      - { title: High, value: 7 }
executionBudget:
  maxSpend: 0.50
  maxHops: 50
  maxLlmCalls: 30

steps:
  - id: draft
    name: Draft
    type: normal
    nodes: "branches"
    fields:
      - { name: Context, type: text, from: input.context }
      - { name: Branch Number, type: nodeInfo }
      - { name: Total Branches, type: knobInfo, from: branches }
    systemPrompt: |
      You are branch {Branch Number} of {Total Branches}.
      Produce an independent first-pass answer to the user's Context.

  - id: evaluate
    name: Evaluate
    type: normal
    nodes: "branches"
    fields:
      - { name: Draft, type: ingest, from: { stepId: draft, loopRef: current } }
    systemPrompt: |
      If the draft in Draft is strong, output the draft text verbatim.
      If it is weak, output "REJECT".
    if:
      contains: REJECT
      then: abort
      else: continue

  - id: synthesize
    name: Synthesize
    type: normal
    fields:
      - name: Surviving Drafts
        type: multi_ingest
        from:
          - { stepId: evaluate, loopRef: current, nodeRef: accumulate }
    systemPrompt: |
      Several candidate drafts are listed under "Surviving Drafts".
      Synthesize them into one coherent final answer.

  - id: answer
    name: Answer
    type: normal
    fields:
      - { name: Synthesized, type: ingest, from: { stepId: synthesize, loopRef: current } }
    systemPrompt: "Return the synthesized answer as-is."
```

Save, validate, call. That's a working Tree-of-Thoughts ladder in roughly 40 lines of YAML.

## Step 9: What to try next

Once you have this working, extensions to try:

- **Add a reflexion loop.** Make `evaluate`'s `else` jump backward to `draft` instead of aborting, so the ladder retries on rejection. Persist the rejection reason to a relik so the next draft attempt reads it.

- **Replace the integer gate with `jsonMatches`.** Have `evaluate` emit `{"score": 1-5, "verdict": "strong"|"weak"}` and gate on `score: >= 4`. More robust than integer parsing.

- **Vary the draft prompts per branch.** Use the `Branch Number` injection more aggressively: branch 1 is asked to be thorough, branch 2 to be creative, branch 3 to be skeptical.

- **Add recursion on `refine`.** After `synthesize`, add a refine step that recursively improves the output. See the Recursion article for the pattern.

- **Persist survivors to a relik.** Use a `relikWrite:` block on `synthesize` to extract structured findings and append them to a memory relik. See Reads and Writes for the schema-matched write pattern.

Each of these exercises one of the power axes (gates, jumps, reliks, recursion). The canonical reference for all of them composed together is the Crucible ladder in Examples.

