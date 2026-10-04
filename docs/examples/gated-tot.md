# B. Gated Tree-of-Thought Ladder

Draft multiple reasoning branches in one shot, evaluate each branch in parallel, prune the weak ones with a gate, then synthesize the survivors.

## B.1 What This Ladder Does

This ladder implements a tree-of-thought search pattern in four steps.

The first step is a single LLM call that drafts multiple reasoning branches at once. It receives the user's question and a `knobInfo` field specifying the number of branches to write. A single LLM call produces all branches, which naturally encourages diversity since the model generates them with awareness of each other.

The second step fans out into multiple parallel evaluator nodes; one per branch. Each node receives the full draft plus its branch number (`nodeInfo`) and total branch count (`knobInfo`). Each node evaluates its assigned branch and outputs "1" (strong) or "0" (weak). A gate (`if: { equals: "1" }`) prunes the nodes that voted "0".

The third step synthesizes the surviving branches. It receives two fields: the full draft (via `ingest` from the draft step) and the evaluation results (via `multi_ingest` with `nodeRef: "accumulate"` from the evaluate step). The LLM receives the complete draft plus which branches survived, and produces a single coherent answer.

The fourth step is the exit step; it passes through the synthesis as the final result.

The config demonstrates four mechanisms: `knobs` for caller-controlled branch count, `knobInfo` and `nodeInfo` for injecting runtime values into prompts, typed gates (`if:`) for pruning, and multi-source ingestion combining outputs from two different upstream steps.

## B.2 Step-by-Step Execution

With the branches knob set to 5:

**Step 1; Draft (single node).** The `draft` step has no `nodes` field, so it makes one LLM call. Its prompt includes the user's context and a `Branch Count: 5` field from `knobInfo`. The LLM drafts 5 distinct reasoning branches in a single response; each exploring a different angle or approach to the question.

**Step 2; Evaluate (5 nodes).** The `evaluate` step has `nodes: "branches"`, which resolves to 5. The engine makes 5 parallel LLM calls. Each call receives:

```
Draft: <the full draft output with all 5 branches>
Branch Number: 3
Total Branches: 5

[System Instruction]
Evaluate branch 3 of 5. Output 1 if strong, 0 if weak.
```

Each evaluator node reads the full draft but focuses on its assigned branch. For example, if nodes 1, 3, and 5 output "1" (strong), while nodes 2 and 4 output "0" (weak):

**Gate check.** The `if: { equals: "1" }` gate on the evaluate step fires. The runtime checks each node's output for an **exact match** against "1". Nodes 1, 3, and 5 pass. Nodes 2 and 4 are pruned; they're marked as eliminated in the pruned node map.

**Step 3; Synthesize (single node).** The synthesize step has no `nodes` field, so it makes one LLM call. It receives two fields:

```
Draft: <the full draft with all 5 branches>

Evaluations 1: 1

Evaluations 2: 1

Evaluations 3: 1
```

The `Draft` field pulls the complete draft from step 1. The `Evaluations` field is a `multi_ingest` with `nodeRef: "accumulate"` targeting the evaluate step; it gathers only the surviving node outputs (3 of them, all "1"). The LLM receives the full draft plus confirmation of which branches survived, and synthesizes the best reasoning into one answer.

**Step 4; Exit.** Since `exit: answer`, the `answer` step ingests the synthesize step's output and returns it as the final result.

## B.3 Gates, KnobInfo, and Pruning in Detail

This example combines three mechanisms.

**`knobInfo` and `nodeInfo`** inject runtime values into a step's prompt. The `draft` step uses `knobInfo` to inject the number of branches to write; the resolved numeric value of the `branches` knob appears as `Branch Count: 5` in the prompt. The `evaluate` step uses both: `knobInfo` for the total branch count and `nodeInfo` for each node's position (1-based). Each evaluator receives its assigned branch number; node 3 sees `Branch Number: 3` and `Total Branches: 5`.

**`if:` gate on a step** instructs the runtime to check each node's output after the step completes. The check is an **exact string match**; the node's entire output must equal the value declared in the condition. With `if: { equals: "1" }`, a node's output must be exactly `"1"` to survive. An output like `"Yes, 1"` or `"1. Strong"` will not match.

On a multi-node step, non-matching nodes are pruned individually. Downstream steps using `pruned: true` on their `nodes.from` will only see the survivors. If ALL nodes are pruned, the ladder aborts with an error.

**Multi-source ingestion** in the synthesize step pulls from two upstream steps simultaneously. It has two fields: an `ingest` field that reads the draft step's output, and a `multi_ingest` field with `nodeRef: "accumulate"` that reads all surviving evaluate outputs. The assembled prompt contains both the draft content and the evaluation results.

## B.4 Full Config

```yaml
name: Gated Tree of Thought

allowedTargets:
  strategy: universal

exit: answer

knobs:
  branches:
    name: Branches
    type: nodes
    input: slider
    steps:
      - title: Low
        value: 3
        default: true
      - title: Mid
        value: 5
      - title: High
        value: 7

steps:
  - id: draft
    name: Draft
    type: normal
    fields:
      - name: Context
        type: text
        from: input.context
      - name: Branch Count
        type: knobInfo
        from: branches
    systemPrompt: "Draft multiple reasoning branches. The Branch Count field above tells you how many to produce. Label each branch clearly."

  - id: evaluate
    name: Evaluate
    type: normal
    nodes: branches
    fields:
      - name: Draft
        type: ingest
        from:
          stepId: draft
          loopRef: current
      - name: Branch Number
        type: nodeInfo
      - name: Total Branches
        type: knobInfo
        from: branches
    systemPrompt: "Evaluate the draft. The Branch Number and Total Branches fields above tell you which branch this is. Output 1 if strong, 0 if weak."
    if:
      equals: "1"
      then: continue
      else: abort

  - id: synthesize
    name: Synthesize
    type: normal
    fields:
      - name: Draft
        type: ingest
        from:
          stepId: draft
          loopRef: current
      - name: Evaluations
        type: multi_ingest
        from:
          - stepId: evaluate
            loopRef: current
            nodeRef: accumulate
    systemPrompt: "Synthesize the surviving branches from the draft into one coherent answer."

  - id: answer
    name: Answer
    type: normal
    fields:
      - name: Draft
        type: ingest
        from:
          stepId: synthesize
          loopRef: current
    systemPrompt: "Return the synthesized answer as-is."
```

The `draft` step uses `knobInfo` to inject the branch count into the prompt. The `evaluate` step fans out into N parallel nodes; each receives the full draft plus its branch number (`nodeInfo`) and the total count (`knobInfo`). The gate (`if: { equals: "1" }`) prunes weak branches — nodes that output "0" are removed from the downstream view. The `synthesize` step pulls from two sources: the full draft (via `ingest`) and the surviving evaluations (via `multi_ingest` with `nodeRef: "accumulate"`). The `answer` step is a pass-through exit step.

## B.5 What the Caller Sees

The ladder is addressed by the author's username and the ladder's slug name in the URL path. The request body follows the standard OpenAI chat completions format, with an optional `knobs` object to override the ladder's default knob values:

```bash
curl http://localhost:8788/v1/local/gated-tot/chat/completions \
  -H "Authorization: Bearer $LADR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "What is the best programming language for systems programming?"}],
    "knobs": { "branches": 7 }
  }'
```

The response is a standard chat completion containing the answer step's output. The response format is identical to a direct model call; branch count, pruning results, and intermediate outputs are not exposed.

In Foundry, the timeline renders the full execution: the draft step with its single output, the evaluate step with 7 node dots (pruned nodes are visually marked), the synthesize step combining the survivors, and the answer step. Each evaluator node is inspectable to see the branch evaluation and pruning result.

