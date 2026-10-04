# Add Reflexion to a Ladder

Turn a one-pass ladder into a draft-critique-redraft loop using jumps. Uses real LADR grammar; typed if-blocks, jsonMatches, and the pending-jump cursor.

## What reflexion is

**Reflexion** is a simple loop: the model drafts an answer, critiques it, then drafts again using the critique as additional input. Repeat until the critique is positive or a max-iteration cap is hit.

Research basis: Shinn et al., *Reflexion: Language Agents with Verbal Reinforcement Learning* (2023). The pattern generalizes: any time you can describe what's wrong with an answer in words, you can feed that description back to the model on the next attempt.

In LADR, reflexion is implemented with **jumps** (backward jump to an earlier step) bounded by the hop ceiling and the spend cap. No new primitive; just `then: { jump: { stepId: ... } }` after a gate evaluation.

## The starting point

Start with a single-step draft ladder:

```yaml
name: Simple Draft
allowedTargets: { strategy: universal }
exit: draft
knobs: {}
steps:
  - id: draft
    name: Draft
    type: normal
    fields:
      - { name: Context, type: text, from: "input.context" }
    systemPrompt: "Answer the question."
```

We'll add: a critique step that scores the draft, a refine step that improves it, and a gate that jumps back to `draft` when the score is too low.

## Step 1: Add a critique step

Insert a step that scores the previous draft. The LLM emits JSON so we can gate on the score:

```yaml
- id: critique
  name: Critique
  type: normal
  fields:
    - { name: Context, type: text, from: "input.context" }
    - { name: Draft, type: ingest, from: { stepId: draft, loopRef: current } }
  systemPrompt: |
    Critique the previous draft. Output exactly:
    {"score": <1-10>, "issues": ["issue 1", "issue 2", ...]}
    Be specific. List concrete problems.
```

The output is JSON. We'll gate on the `score` field using `jsonMatches`.

## Step 2: Add the refine step

```yaml
- id: refine
  name: Refine
  type: normal
  fields:
    - { name: Context, type: text, from: "input.context" }
    - { name: Draft, type: ingest, from: { stepId: draft, loopRef: current } }
    - { name: Critique, type: ingest, from: { stepId: critique, loopRef: current } }
  systemPrompt: |
    Improve the previous draft using the critique. Address each issue
    explicitly. Output only the improved answer.
```

Wire the ladder's `exit` to `refine`. The final returned output is the refined answer.

## Step 3: Add the reflexion gate

Add a gate to `critique` that jumps back to `draft` when the score is low. The gate uses `jsonMatches` to extract the score from the critique's JSON output:

```yaml
- id: critique
  ...
  if:
    jsonMatches:
      type: object
      required: [score]
      properties:
        score: { type: integer, minimum: 8 }     # accept only scores >= 8
      additionalProperties: true                 # allow other fields (issues, etc.)
    then: continue                               # score is good; proceed to refine
    else:
      jump: { stepId: draft }                    # score too low; retry draft
```

What happens at runtime:
1. `draft` produces an initial answer.
2. `critique` evaluates it. The engine extracts the JSON, checks `score >= 8`.
3. **If pass:** gate's `then: continue` fires; `critique` completes, `refine` runs, ladder exits.
4. **If fail:** gate's `else: { jump: { stepId: draft } }` fires; the engine's mutable cursor repositions to `draft`. The critique's output is now available as a prior step output, so the next `draft` pass can ingest it.

To make the retry actually see the critique, update `draft`'s fields to ingest it when present:

```yaml
- id: draft
  name: Draft
  type: normal
  fields:
    - { name: Context, type: text, from: input.context }
    - { name: Prior Critique, type: ingest, from: { stepId: critique, loopRef: previous } }
  systemPrompt: |
    Answer the question in the Context field.
    If a "Prior Critique" field is present above, address each issue it lists.
```

How this renders for the LLM:

- **First pass** (no critique yet): the runtime can't resolve `Prior Critique` from a step that hasn't run, so the field renders as empty or omitted. The model sees only `Context: ...` and the system instruction. It drafts from scratch.
- **Retry passes**: `critique` has run on a prior loop. The runtime reads its output and renders `Prior Critique: {"score": 4, "issues": ["..."]}` above the system instruction. The model sees the critique and addresses it.

Note: the systemPrompt text does **not** use `{Prior Critique}` interpolation. The model reads the rendered field lines above the system instruction. LADR has no string-interpolation syntax in systemPrompts; the field lines are how values reach the prompt.

## Step 4: Cap the iterations

A reflexion loop without a ceiling is a runaway risk. The hop ceiling + spend cap bound it. Always set both:

```yaml
executionBudget:
  maxSpend: 1.00          # hard $ ceiling
  maxHops: 12             # hard cap on step executions (draft + critique + refine = 3 hops per loop → 4 loops worst case)
  maxLlmCalls: 20
```

The runtime hits whichever ceiling is reached first and exits with the best-so-far output (not an error). You'll see an `error` event with `ceiling_exceeded: true` in the trace.

## Step 5: Full reflexion ladder

```yaml
name: Reflexion Loop
allowedTargets: { strategy: universal }
exit: refine

executionBudget:
  maxSpend: 1.00
  maxHops: 12
  maxLlmCalls: 20

knobs:
  threshold:
    name: Threshold
    type: generic
    input: slider
    steps:
      - { title: Lenient,  value: 6, default: true }
      - { title: Standard, value: 8 }
      - { title: Strict,   value: 9 }

steps:
  - id: draft
    name: Draft
    type: normal
    fields:
      - { name: Context, type: text, from: input.context }
      - { name: Prior Critique, type: ingest, from: { stepId: critique, loopRef: previous } }
    systemPrompt: |
      Answer the question in the Context field.
      If a "Prior Critique" field is present above, address each issue it lists.

  - id: critique
    name: Critique
    type: normal
    fields:
      - { name: Context, type: text, from: "input.context" }
      - { name: Draft, type: ingest, from: { stepId: draft, loopRef: current } }
    systemPrompt: 'Output JSON: {"score": <1-10>, "issues": [...]}'
    if:
      jsonMatches:
        type: object
        required: [score]
        properties:
          score: { type: integer, minimum: 8 }     # static threshold; see note below
        additionalProperties: true
      then: continue
      else:
        jump: { stepId: draft }

  - id: refine
    name: Refine
    type: normal
    fields:
      - { name: Context, type: text, from: "input.context" }
      - { name: Draft, type: ingest, from: { stepId: draft, loopRef: current } }
      - { name: Critique, type: ingest, from: { stepId: critique, loopRef: current } }
    systemPrompt: "Address each critique issue. Output only the final answer."
```

**Note on the threshold knob.** JSON Schemas inside `jsonMatches` can't directly embed knob values (knobs resolve to runtime numbers; schemas are static). Two ways to make the threshold caller-tunable:

1. **Multiple schemas, picked at author time**; declare several ladders, one per threshold.
2. **Use `numberRange` on a stripped output**; have the LLM emit just the number on its own line, gate with `if: { numberRange: [6, 10] }`. Then the range bounds can't be knob-driven either, but you can ship variants.

For truly dynamic thresholds, the cleanest pattern is to gate via a separate `decide` step that reads the score and emits `CONTINUE` or `RETRY`, then gate on `equals`. Left as an exercise.

## When reflexion helps (and when it does not)

Reflexion pays off on:

- **Hard reasoning**; math, logic, multi-constraint puzzles. The critique step catches the failure mode and the next attempt avoids it.
- **Writing tasks**; prose, code, structured output. Critique surfaces issues the drafter missed.
- **Verification-friendly problems**; anything with a measurable correctness signal the critique can extract.

Reflexion is wasted on:

- **Factual recall.** "What is the capital of France?" does not improve from a critique step.
- **Open-ended preference.** "Write a poem about autumn"; the critique is just one opinion; iteration may converge to the critique's aesthetic, not the user's.
- **High-variance prompts.** If the model is uncertain, three loops may produce three different wrong answers; ToT with self-consistency is better than reflexion here.

