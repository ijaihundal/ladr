# Reference Ladder: Crucible (Walkthrough)

> For the complete reference config with design rationale and failure modes,
> see [Spec > Crucible](../spec/crucible.md).

Crucible is the reference composition ladder. It combines Tree-of-Thoughts,
per-node gating, reflexion, sequential self-refine with recursion,
self-consistency synthesis, and persistent memory into one ladder. Every
power axis in LADR is exercised.

This article is a complete annotated walkthrough. The config is ~100 lines
of YAML; the execution trace on a hard problem is 30+ LLM calls across 5+
phases.

## What Crucible does

Given a hard question, Crucible:

1. **Generates** N independent candidate solutions in parallel (ToT fan-out).
2. **Verifies** each candidate with M independent verifiers (per-node gate).
3. Candidates scoring 4+ **survive**; candidates scoring 1-3 are pruned.
4. If no survivors, **reflects** on failures, extracts a lesson, persists
   it, and **jumps back** to generate (reflexion loop).
5. Surviving candidates enter a **sequential refine** phase that recursively
   deepens (self-refine + recursion).
6. The best refined candidate is **synthesized** into a single answer
   (self-consistency).
7. The answer is **persisted** to the solution relik for future calls.

Across calls, Crucible reads accumulated lessons and prior solutions from
reliks. It starts call 2 already educated.

## The config

```yaml
name: Crucible
allowedTargets: { strategy: universal }
exit: answer
knobs:
  branches:
    name: Branches
    type: nodes
    input: slider
    steps:
      - { title: Compact, value: 3, default: true }
      - { title: Balanced, value: 5 }
      - { title: Wide, value: 8 }
  verifiers:
    name: Verifiers
    type: nodes
    input: slider
    steps:
      - { title: Quick, value: 2, default: true }
      - { title: Thorough, value: 4 }
  iterations:
    name: Iterations
    type: recursion
    input: numerical
    default: 2
    min: 1
    max: 4

reliks:
  - title: Crucible Memory
    folders:
      - name: trace
        tokenLimit: 800
        files:
          - { name: verified.md, init: blank }
      - name: lessons
        tokenLimit: 400
        files:
          - { name: failures.md, init: blank }
      - name: solution
        tokenLimit: 1000
        files:
          - { name: best.md, init: blank }

executionBudget:
  maxSpend: 10.0
  maxHops: 300
  maxLlmCalls: 300

steps:
  # --- Phase 1: Generate (Tree-of-Thoughts fan-out) ---
  - id: generate
    name: Generate
    type: normal
    nodes: branches
    fields:
      - { name: Question, type: text, from: input.context }
      - { name: PriorArt, type: relikRead, from: { relik: Crucible Memory, folder: solution, file: best.md } }
      - { name: Lessons, type: relikRead, from: { relik: Crucible Memory, folder: lessons, file: failures.md } }
    systemPrompt: |
      Generate an independent solution to the Question.
      Build on PriorArt if available. Heed Lessons from prior failures.
      Be thorough and original.

  # --- Phase 2: Verify (per-node gate) ---
  - id: verify
    name: Verify
    type: normal
    nodes: verifiers
    fields:
      - { name: Candidate, type: ingest, from: { stepId: generate, loopRef: current } }
    systemPrompt: 'Score the Candidate 1-5. Emit JSON {"score": N, "verdict": "accept"|"reject", "reason": "..."}.'
    if:
      jsonMatches:
        type: object
        required: [score, verdict]
        properties:
          verdict: { type: string, enum: [accept] }
          score: { type: number, minimum: 4 }
      then: continue
      else:
        write:
          to: { relik: Crucible Memory, folder: lessons, file: failures.md }
          from: output
          on: append

  # --- Phase 3: Reflect (reflexion loop entry) ---
  # Fires only if ALL verify nodes were pruned (no survivors).
  # This step is reached via the else branch when verify has zero survivors.
  - id: reflect
    name: Reflect
    fields:
      - { name: Question, type: text, from: input.context }
    systemPrompt: |
      All candidates failed verification. Analyze why.
      Emit JSON {"root_cause": "...", "lesson": "..."}.
      The lesson should help the next generation avoid this failure.
    relikWrite:
      to: { relik: Crucible Memory, folder: lessons, file: failures.md }
      on: append
      match:
        type: object
        required: [lesson]
        properties:
          lesson: { type: string, minLength: 10 }
    # Jump back to generate — reflexion loop
    # (implicit via then on the relikWrite step)
    timeline: circle

  # --- Phase 4: Refine (sequential self-refine with recursion) ---
  - id: refine
    name: Refine
    type: sequential
    nodes: 2
    fields:
      - { name: Candidate, type: ingest, from: { stepId: verify, loopRef: current } }
      - { name: Previous, type: ingest, from: { stepId: refine, loopRef: current, nodeRef: previous }, skipFirstNode: true }
    systemPrompt: |
      Improve the Candidate. Fix errors, tighten reasoning, add depth.
      If Previous is available, build on it rather than starting fresh.
    recursion:
      maxDepth: iterations

  # --- Phase 5: Synthesize (self-consistency) ---
  - id: synthesize
    name: Synthesize
    fields:
      - { name: Refined, type: multi_ingest, from: [{ stepId: refine, loopRef: current, nodeRef: accumulate }] }
    systemPrompt: |
      Merge the refined candidates into a single, definitive answer.
      Resolve contradictions. Preserve the strongest insights from each.
    relikWrite:
      to: { relik: Crucible Memory, folder: solution, file: best.md }
      on: overwrite
      field: answer
      match:
        type: object
        required: [answer]
        properties:
          answer: { type: string, minLength: 1 }

  # --- Phase 6: Answer (exit step) ---
  - id: answer
    name: Answer
    fields:
      - { name: Solution, type: relikRead, from: { relik: Crucible Memory, folder: solution, file: best.md } }
    systemPrompt: "Return the Solution as-is. No commentary."
```

## Phase-by-phase walkthrough

### Phase 1: Generate

```yaml
nodes: branches    # 3/5/8 candidates based on caller's knob choice
```

The generate step fans out into N parallel LLM calls. Each call receives:

- `Question`: the user's input.
- `PriorArt`: the best solution from the previous call (empty on first call).
- `Lessons`: accumulated failure lessons from the lessons relik.

On call 1, `PriorArt` and `Lessons` are blank. On call 2+, the ladder
starts with everything it learned last time.

### Phase 2: Verify

```yaml
nodes: verifiers    # 2/4 verifiers based on caller's knob choice
```

Each verify node scores a candidate. The `jsonMatches` gate accepts only
outputs with `verdict: "accept"` and `score >= 4`. Non-matching nodes are
pruned.

If any survivors remain, `then: continue` fires and execution advances to
`refine`. The pruned verifiers' outputs are written to the lessons relik
via the `else: write` action — but only if ALL nodes are pruned (the else
fires at step level when zero survivors remain).

### Phase 3: Reflect

Reached only when all verify nodes failed the gate. The reflect step:

1. Reads the question.
2. Asks the LLM to analyze why all candidates failed.
3. Extracts a structured lesson via `relikWrite` with schema matching.
4. The lesson is appended to `failures.md`.

After reflect, the pipeline continues to `refine` (or the author could add
a backward jump to `generate` for a full reflexion loop).

### Phase 4: Refine

```yaml
type: sequential
nodes: 2
recursion: { maxDepth: iterations }
```

Sequential step with 2 nodes. Node 1 refines the candidate from scratch.
Node 2 reads node 1's output via `nodeRef: previous` and refines further.

The `recursion` block spawns a child instance of the same ladder. The
child's `input.context` is the refine step's output. The child runs the
full pipeline from `generate` with the refined output as its question.
Recursion depth is controlled by the `iterations` knob (default 2, max 4).

### Phase 5: Synthesize

```yaml
fields:
  - { name: Refined, type: multi_ingest, from: [{ stepId: refine, loopRef: current, nodeRef: accumulate }] }
```

Reads all surviving refine nodes' outputs. The synthesis step merges them
into one answer. The result is persisted to the solution relik via
`relikWrite` with `on: overwrite` (each call replaces the previous best).

### Phase 6: Answer

The exit step reads the persisted solution from the relik and returns it
verbatim. This indirection (write to relik, then read from relik) ensures
the solution is durable — even if the exit step fails, the solution is
already saved.

## Cost analysis

| Phase | LLM calls (balanced knobs) | Notes |
|-------|---------------------------|-------|
| Generate | 5 | `branches: 5` |
| Verify | 5 | `verifiers: 5`, paired 1:1 with generate nodes |
| Reflect | 0-1 | Only fires if all verify nodes fail |
| Refine | 2 × depth | `nodes: 2`, recursion depth 2 = 4 calls |
| Synthesize | 1 | Single synthesis call |
| Answer | 0 | No LLM call — reads from relik |
| **Total** | **~13-14** | Without reflexion; +5 per reflexion iteration |

At GPT-4o pricing (~$5/1M input, ~$15/1M output), a typical Crucible call
costs $0.10-$0.30. The `$10` spend ceiling provides ample headroom.

## Knobs: caller-tunable parameters

```yaml
knobs:
  branches:   { ..., input: slider, steps: [3, 5, 8] }
  verifiers:  { ..., input: slider, steps: [2, 4] }
  iterations: { ..., input: numerical, default: 2, min: 1, max: 4 }
```

The caller controls:

- **Branches**: how many parallel candidates to generate. Compact (3) for
  speed; Wide (8) for coverage.
- **Verifiers**: how many verifiers per candidate. Quick (2) for fast
  gating; Thorough (4) for rigorous filtering.
- **Iterations**: recursion depth for the refine phase. 1 for shallow;
  4 for deep self-improvement.

Example call with wide exploration and deep refinement:

```bash
curl http://localhost:8788/v1/local/crucible/chat/completions \
  -H "Authorization: Bearer $LADR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Prove that the sum of two odd numbers is even."}],
    "knobs": { "branches": 8, "verifiers": 4, "iterations": 3 }
  }'
```

## Memory across calls

Crucible's reliks persist between invocations:

| Relik | Folder | File | What accumulates |
|-------|--------|------|------------------|
| Crucible Memory | solution | best.md | Latest best solution (overwritten each call) |
| Crucible Memory | lessons | failures.md | Growing list of failure lessons (appended) |
| Crucible Memory | trace | verified.md | Audit trail of verified candidates |

On call 1: empty reliks, generate starts from scratch.
On call 2: generate reads `best.md` as `PriorArt` and `failures.md` as
`Lessons`. The ladder starts educated.

## Variations

**Minimal Crucible (3 steps):** Remove reflect and refine. Just
generate → verify → answer. Covers ToT + gating. ~7 LLM calls.

**Reflexion-only Crucible (4 steps):** Remove refine. generate → verify →
reflect → generate (loop). Covers ToT + reflexion. ~11 LLM calls per
iteration.

**Deep recursion Crucible:** Set `iterations: 4` and remove the verify
gate. generate → refine (deep recursion) → answer. Covers ToT + recursion.
~15 LLM calls at depth 4.
