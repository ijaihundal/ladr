# Patterns Catalog

A reference of canonical LADR patterns; Tree-of-Thoughts, Reflexion, Self-Consistency, Self-Refine, Debate, Verify-then-Persist. Each pattern has a config sketch, when-to-use guidance, and composition notes.

## How to use this catalog

Each pattern below is a reusable shape; a config sketch you can lift, combine with other patterns, and adapt to your domain. The patterns are **independent primitives**: any of them can be added to a ladder without breaking the others.

The catalog doesn't repeat the full tutorials; see [Build a ToT Ladder](../tutorials/build-tot.md) and [Add Reflexion](../tutorials/add-reflexion.md) for end-to-end walkthroughs. This page is a reference of canonical shapes, designed for skimming and copy-paste.

**Composition rules of thumb:**

- Patterns compose by stacking. ToT + reflexion = a ToT that retries the synthesis on low scores.
- Each pattern has a **cost** (LLM calls per execution) and a **latency** (LLM-rounds deep). Watch the execution budget.
- More patterns ≠ better. The smallest ladder that meaningfully beats one-shot is usually the right ladder.

## Tree-of-Thoughts (ToT)

**Shape:** N parallel draft nodes → per-node evaluation gate → synthesis of survivors.

**When to use:** hard reasoning, multi-angle analysis, any task where "draft many, pick the best, synthesize" beats "draft once."

**Cost:** N + N + 1 = 2N+1 LLM calls per loop (draft + evaluate + synthesize).

**Config sketch:**

```yaml
knobs:
  branches:
    name: Branches
    type: nodes
    input: slider
    steps: [{ title: Low, value: 3, default: true }, { title: High, value: 7 }]

steps:
  - id: draft
    type: normal
    nodes: "branches"
    fields:
      - { name: Context, type: text, from: "input.context" }
      - { name: Branch Number, type: nodeInfo }
    systemPrompt: "Draft an independent branch. Use the Branch Number field above to vary your angle."

  - id: evaluate
    type: normal
    nodes: "branches"
    fields:
      - { name: Draft, type: ingest, from: { stepId: draft, loopRef: current } }
    systemPrompt: 'Output JSON: {"score": 1-5, "verdict": "strong"|"weak"}.'
    if:
      jsonMatches:
        type: object
        required: [score]
        properties:
          score: { type: integer, minimum: 4 }
        additionalProperties: true
      then: continue
      else: abort

  - id: synthesize
    type: normal
    fields:
      - name: Drafts
        type: multi_ingest
        from:
          - { stepId: draft, loopRef: current, nodeRef: accumulate }
    systemPrompt: "Synthesize the surviving drafts into one final answer."
```

**Variants:**

- **Scored ToT.** Use `jsonMatches` gate on JSON-encoded scores (as above); accept only scores ≥ threshold.
- **Verbal ToT.** Use `equals: "1"` gate; simpler prompt, less nuanced.
- **ToT + Reflexion.** Add `else: { jump: { stepId: draft } }` to retry on no-survivors.

**Common mistake:** setting `branches` too high. Cost grows linearly; quality gains plateau around 5-7. Default to 5.

## Reflexion

**Shape:** draft → critique → gate (jump back to draft on low score) → refine.

**When to use:** tasks with a verifiable correctness signal (math, code, logic). Less useful for purely subjective tasks.

**Cost:** 3 LLM calls per loop × maxLoops (bounded by hop ceiling).

**Config sketch:** see [Add Reflexion tutorial](../tutorials/add-reflexion.md) for the full ladder.

**Key idea:** the gate's `else: { jump: { stepId: draft } }` creates the reflexion loop. The critique's output is available as a prior step output, so the next draft pass can ingest it.

```yaml
- id: critique
  if:
    jsonMatches:
      type: object
      required: [score]
      properties:
        score: { type: integer, minimum: 8 }
      additionalProperties: true
    then: continue
    else:
      jump: { stepId: draft }
```

**Common mistake:** reflexion on factual recall. "What's the capital of France?" doesn't improve from a critique step; the model either knows it or doesn't.

## Self-Consistency

**Shape:** N parallel draft nodes (no gating) → extract answers → take majority/median.

**When to use:** tasks with high variance and a clear answer type (math, multiple choice, classification). Multiple independent attempts + aggregation reduces variance.

**Cost:** N + 1 = N+1 LLM calls (draft + aggregate). Cheaper than ToT.

**Difference from ToT:** no gate, no pruning. ALL N nodes make it to aggregation. The aggregation step extracts the answer (not the reasoning) and takes a majority vote.

**Config sketch:**

```yaml
knobs:
  samples:
    name: Samples
    type: nodes
    input: slider
    steps: [{ title: Few, value: 3, default: true }, { title: Many, value: 9 }]

steps:
  - id: draft
    type: normal
    nodes: "samples"
    fields:
      - { name: Context, type: text, from: "input.context" }
      - { name: Sample Number, type: nodeInfo }
    systemPrompt: |
      Answer the question. Show your reasoning.
      Sample number: {Sample Number}.

  - id: aggregate
    type: normal
    fields:
      - name: Samples
        type: multi_ingest
        from:
          - { stepId: draft, loopRef: current, nodeRef: accumulate }
    systemPrompt: |
      Several independent attempts at the same question follow.
      Identify the answer (not the reasoning) in each, then return
      the majority answer. If no majority, return the most defensible.
      
      {Samples}
```

**Variant: median for numeric answers.** For numeric/categorical answers, you can have the aggregator emit JSON with the median value directly.

**Common mistake:** using self-consistency when answers are genuinely ambiguous. Majority vote converges to the most common phrasing, not the best answer.

## Self-Refine

**Shape:** draft → identify-issues → refine (single iteration; loop with jumps for multi-iteration).

**When to use:** writing tasks, code generation, structured output. Lighter-weight than reflexion when one refinement pass is enough.

**Cost:** 3 LLM calls (no loop). Reflexion is self-refine in a loop.

**Config sketch:**

```yaml
steps:
  - id: draft
    type: normal
    fields:
      - { name: Context, type: text, from: "input.context" }
    systemPrompt: "Draft an answer."

  - id: issues
    type: normal
    fields:
      - { name: Draft, type: ingest, from: { stepId: draft, loopRef: current } }
    systemPrompt: 'List concrete issues with the draft. Output JSON: {"issues": ["...", "..."]}'

  - id: refine
    type: normal
    fields:
      - { name: Draft,  type: ingest, from: { stepId: draft,  loopRef: current } }
      - { name: Issues, type: ingest, from: { stepId: issues, loopRef: current } }
    systemPrompt: "Address each issue. Output only the refined answer."
```

**Upgrade to reflexion:** wrap `draft → issues → refine` in a reflexion loop (jumps back to `draft` if `refine` still has issues).

## Debate / Multi-Agent

**Shape:** N nodes take opposing positions → each critiques the others → a judge picks the winner.

**When to use:** questions with legitimate opposing arguments (policy, ethics, design tradeoffs). Less useful for factual questions.

**Cost:** 2N + 1 = 2N+1 LLM calls (positions + rebuttals + judge).

**Config sketch:**

```yaml
steps:
  - id: positions
    type: normal
    nodes: 2                                # pro and con
    fields:
      - { name: Context, type: text, from: "input.context" }
      - { name: Side, type: nodeInfo }
    systemPrompt: |
      Argue position {Side} of 2. Node 1 = strongly in favor.
      Node 2 = strongly against. Make the strongest case for your side.

  - id: rebuttals
    type: normal
    nodes: 2
    fields:
      - { name: Own Position, type: ingest, from: { stepId: positions, loopRef: current } }
      - name: Opposing Views
        type: multi_ingest
        from:
          - { stepId: positions, loopRef: current, nodeRef: accumulate }
    systemPrompt: "Address the strongest points from the opposing views."

  - id: judge
    type: normal
    fields:
      - name: Debate
        type: multi_ingest
        from:
          - { stepId: positions,  loopRef: current, nodeRef: accumulate }
          - { stepId: rebuttals,  loopRef: current, nodeRef: accumulate }
    systemPrompt: |
      Weigh both sides of the debate. Synthesize a balanced verdict
      that acknowledges the strongest arguments on each side.
```

**Variant: N-way debate.** Set `nodes: 4` with a system prompt that assigns distinct stakeholder perspectives (e.g. user / business / legal / engineering).

## Verify-then-Persist

**Shape:** a step that emits structured JSON → `if: { jsonMatches: ... }` gate verifies → `then: { write: ... }` persists to relik.

**When to use:** durable learning across calls. Record what worked, what didn't, structured outcomes.

**Cost:** 1 LLM call (no extra cost; the gate and write run after the step).

**Config sketch:**

```yaml
- id: extract
  type: normal
  fields:
    - { name: Context, type: text, from: "input.context" }
  systemPrompt: |
    Output JSON summarizing the key finding:
    {"fact": "...", "confidence": 0.0-1.0}
  if:
    jsonMatches:
      type: object
      required: [fact, confidence]
      properties:
        fact: { type: string, minLength: 1 }
        confidence: { type: number, minimum: 0.6 }    # only persist confident facts
      additionalProperties: false
    then:
      write:
        to: { relik: "Memory", folder: facts, file: core.md }
        from: output
        on: append
    else: continue
```

**Why this matters:** the gate-and-write pattern is what turns a stateless ladder into a learning system. Every call contributes durable memory, but only for outputs that pass verification.

## Research Loop (with retrieve:)

**Shape:** a step that sees a relik index, requests files via JSON, gets them injected, reasons, repeats up to `maxRounds`.

**When to use:** cold-start retrieval; when you don't know at config time what specific memory is relevant.

**Cost:** `maxRounds` LLM calls per step (worst case).

**Config sketch:** see [Add Persistent Memory tutorial](../tutorials/add-memory.md for the full pattern.

**Key idea:** the LLM is the retriever. It sees the index, reasons about what to fetch, emits a structured request, and the engine injects the requested content. This is interpretable (the retrieval reasoning is in the trace) and composes with structured metatags for deterministic queries.

```yaml
retrieve:
  to: { relik: Knowledge, folder: docs }
  as: RetrievedDocuments
  maxRounds: 5
  requestMatch:
    type: object
    required: [request]
    properties:
      request:
        type: object
        properties:
          files:      { type: array, items: { type: string } }
          subfolders: { type: array, items: { type: string } }
          metatag:
            type: object
            properties:
              name: { type: string }
              contains: { type: string }
          glob: { type: string }
```

## Composing All of Them: Crucible

The [Crucible reference ladder](../examples/gated-tot.md) composes ToT + reflexion + self-refine + self-consistency + persistent memory into one ladder. It's a worked example of how the canonical patterns stack into a larger strategy.

The composition works because each pattern occupies a distinct phase of the ladder:

1. **Generate** (ToT); multi-branch draft.
2. **Verify** (gate + reflexion); prune, retry if no survivors.
3. **Refine** (self-refine); single-pass improvement on the winner.
4. **Reflect** (write to relik); persist lessons learned.

Each phase is one or two steps. The phases connect via field references and gates. The hop ceiling + spend cap bound the whole composition safely.

Read the [Crucible walkthrough](../examples/gated-tot.md) for the full annotated config.

