# Cookbook

Copy-paste recipes for common ladder patterns. Each recipe is a minimal
working config with notes on when to use it and how to extend it.

## Simple Q&A (one-shot wrapper)

The minimal ladder. No benefit over calling the model directly, but
establishes the pattern.

```yaml
name: QnA
allowedTargets: { strategy: universal }
exit: answer
knobs: {}
steps:
  - id: answer
    fields:
      - { name: Question, type: text, from: input.context }
    systemPrompt: "Answer the question concisely."
```

**When to use:** You don't — call the model directly. This is the starting
point to build on.

## Draft and Refine (two-step)

Two LLM calls. The second sees the first's output.

```yaml
name: Draft-Refine
allowedTargets: { strategy: universal }
exit: refine
knobs: {}
steps:
  - id: draft
    fields:
      - { name: Question, type: text, from: input.context }
    systemPrompt: "Write a first draft answering the question."

  - id: refine
    fields:
      - { name: Question, type: text, from: input.context }
      - { name: Draft, type: ingest, from: { stepId: draft, loopRef: current } }
    systemPrompt: "Improve the draft. Fix errors, tighten prose. Output the final answer."
```

**When to use:** Simple tasks where a second pass improves quality. Latency:
~2x one-shot. The most common useful ladder.

## Tree-of-Thoughts (fan-out + gate + synthesize)

N parallel candidates, gate prunes the weak ones, synthesis merges survivors.

```yaml
name: ToT
allowedTargets: { strategy: universal }
exit: synthesize
knobs:
  branches:
    name: Branches
    type: nodes
    input: slider
    steps:
      - { title: Fast, value: 3, default: true }
      - { title: Wide, value: 5 }
steps:
  - id: generate
    nodes: branches
    fields:
      - { name: Question, type: text, from: input.context }
    systemPrompt: "Generate an independent solution."

  - id: score
    nodes: branches
    fields:
      - { name: Candidate, type: ingest, from: { stepId: generate, loopRef: current } }
    systemPrompt: 'Score 1-5. Output JSON {"score": N}.'
    if:
      integerRange: [4, 5]
      then: continue
      else: abort

  - id: synthesize
    fields:
      - { name: Survivors, type: multi_ingest, from: [{ stepId: score, loopRef: current, nodeRef: accumulate }] }
    systemPrompt: "Merge the surviving candidates into one answer."
```

**When to use:** Problems with multiple solution paths where bad paths can
be scored and pruned. Cost: 2N + 1 LLM calls.

## Reflexion (generate → critique → retry)

Draft, critique, and loop back if the critique is negative.

```yaml
name: Reflexion
allowedTargets: { strategy: universal }
exit: answer
knobs:
  max_iterations:
    name: Iterations
    type: loops
    input: numerical
    default: 3
    min: 1
    max: 5
steps:
  - id: draft
    fields:
      - { name: Question, type: text, from: input.context }
      - { name: Critique, type: ingest, from: { stepId: critique, loopRef: previous }, fallback: "" }
    systemPrompt: |
      Draft an answer. If Critique is provided, address its concerns.

  - id: critique
    fields:
      - { name: Answer, type: ingest, from: { stepId: draft, loopRef: current } }
    systemPrompt: 'Critique the answer. Output JSON {"verdict": "good"|"bad", "issues": [...]}'
    if:
      jsonMatches:
        type: object
        properties:
          verdict: { type: string, enum: [good] }
      then: continue
      else: { jump: { stepId: draft } }

  - id: answer
    fields:
      - { name: Final, type: ingest, from: { stepId: draft, loopRef: current } }
    systemPrompt: "Return the answer as-is."
```

**When to use:** Tasks where verbal critique improves the next attempt.
Cost: 2 calls per iteration.

## Self-Consistency (parallel + majority)

N independent attempts, no gating, synthesis picks the consensus.

```yaml
name: SelfConsistency
allowedTargets: { strategy: universal }
exit: synthesize
knobs:
  samples:
    name: Samples
    type: nodes
    input: numerical
    default: 5
    min: 3
    max: 10
steps:
  - id: generate
    nodes: samples
    fields:
      - { name: Question, type: text, from: input.context }
    systemPrompt: "Answer the question independently."

  - id: synthesize
    fields:
      - { name: Attempts, type: multi_ingest, from: [{ stepId: generate, loopRef: current, nodeRef: accumulate }] }
    systemPrompt: |
      Below are N independent answers to the same question.
      Identify the consensus answer and return it.
```

**When to use:** High-variance tasks (math, logic) where majority vote
reduces error rate. No gate needed — all N attempts are synthesized.

## Persistent Memory (relik read + write)

Read prior context at start, write new findings at end.

```yaml
name: MemEx
allowedTargets: { strategy: universal }
exit: answer
knobs: {}
reliks:
  - title: Memory
    folders:
      - name: facts
        tokenLimit: 500
        files:
          - { name: core.md, init: blank }
steps:
  - id: answer
    fields:
      - { name: Question, type: text, from: input.context }
      - { name: KnownFacts, type: relikRead, from: { relik: Memory, folder: facts, file: core.md }, fallback: "No prior facts." }
    systemPrompt: "Answer the question. Use KnownFacts if relevant."
    relikWrite:
      to: { relik: Memory, folder: facts, file: core.md }
      on: append
      field: fact
      match:
        type: object
        required: [fact]
        properties:
          fact: { type: string, minLength: 5 }
```

**When to use:** Any task where accumulated knowledge improves future calls.
The relik persists across invocations.

## Parallel Review (group step)

Multiple independent reviewers, then synthesis.

```yaml
name: Review
allowedTargets: { strategy: universal }
exit: synthesize
knobs: {}
steps:
  - id: reviewers
    type: group
    steps:
      - id: security
        fields: [{ name: Input, type: text, from: input.context }]
        systemPrompt: "Review for security issues."
      - id: style
        fields: [{ name: Input, type: text, from: input.context }]
        systemPrompt: "Review for style issues."
      - id: correctness
        fields: [{ name: Input, type: text, from: input.context }]
        systemPrompt: "Review for correctness."

  - id: synthesize
    fields:
      - { name: Security, type: ingest, from: { stepId: security, loopRef: current } }
      - { name: Style, type: ingest, from: { stepId: style, loopRef: current } }
      - { name: Correctness, type: ingest, from: { stepId: correctness, loopRef: current } }
    systemPrompt: "Merge the three reviews into one report."
```

**When to use:** Multi-perspective analysis where each reviewer has a
different focus. All three run in parallel.

## Sequential Self-Refine

Chain of N refinements, each building on the last.

```yaml
name: SelfRefine
allowedTargets: { strategy: universal }
exit: final
knobs:
  rounds:
    name: Rounds
    type: nodes
    input: numerical
    default: 3
    min: 2
    max: 5
steps:
  - id: refine
    type: sequential
    nodes: rounds
    fields:
      - { name: Question, type: text, from: input.context }
      - { name: Previous, type: ingest, from: { stepId: refine, loopRef: current, nodeRef: previous }, skipFirstNode: true }
    systemPrompt: |
      Improve the answer. If Previous is available, build on it.

  - id: final
    fields:
      - { name: Best, type: ingest, from: { stepId: refine, loopRef: current } }
    systemPrompt: "Return the final answer."
```

**When to use:** When each refinement pass genuinely improves the output.
Cost: N LLM calls.

## JSON Extraction with Validation

Extract structured data from prose, validate, and persist.

```yaml
name: Extract
allowedTargets: { strategy: universal }
exit: done
knobs: {}
reliks:
  - title: Data
    folders:
      - name: records
        tokenLimit: 1000
        files: [{ name: index.md, init: "[]" }]
steps:
  - id: done
    fields:
      - { name: Input, type: text, from: input.context }
    systemPrompt: |
      Extract structured records from the input.
      Emit JSON array: [{"id": "...", "name": "...", "category": "..."}]
    relikWrite:
      to: { relik: Data, folder: records, file: index.md }
      on: mergeByKey
      key: id
      match:
        type: array
        items:
          type: object
          required: [id, name]
          properties:
            id: { type: string }
            name: { type: string }
            category: { type: string }
```

**When to use:** Building a structured store from unstructured input. The
`mergeByKey` ensures records update by ID rather than duplicating.

## Confidence-Gated Early Exit

Skip remaining steps if confidence is high.

```yaml
name: EarlyExit
allowedTargets: { strategy: universal }
exit: answer
knobs: {}
steps:
  - id: draft
    fields: [{ name: Question, type: text, from: input.context }]
    systemPrompt: 'Answer. Emit JSON {"answer": "...", "confidence": 0.0-1.0}.'
    if:
      jsonMatches:
        type: object
        properties:
          confidence: { type: number, minimum: 0.9 }
      then: { jump: { stepId: answer } }
      else: continue

  - id: expand
    fields:
      - { name: Question, type: text, from: input.context }
      - { name: Draft, type: ingest, from: { stepId: draft, loopRef: current } }
    systemPrompt: "The draft had low confidence. Research deeper and improve."

  - id: answer
    fields:
      - { name: Result, type: ingest, from: { stepId: draft, loopRef: current }, fallback: "" }
    systemPrompt: "Return the answer."
```

**When to use:** When easy inputs don't need the full pipeline. Saves cost
on high-confidence cases.

## Combining Recipes

Every recipe above is a phase. They compose by connecting field references:

```yaml
# ToT + Reflexion + Memory
steps:
  - id: generate          # from ToT recipe
    nodes: 5
    # ... reads Lessons from relik
  - id: verify            # from ToT recipe (gate)
    # ... prunes to score >= 4
  - id: reflect           # from Reflexion recipe
    # ... writes lesson to relik, jumps back to generate
  - id: synthesize        # from ToT recipe
    # ... merges survivors
```

See [Composing Patterns](../patterns/patterns.md) for the full composition
guide and [Crucible](../examples/crucible.md) for the reference composition
ladder.
