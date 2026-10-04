# Composing Patterns

How the canonical patterns stack into larger ladders. A composed ladder
chains multiple reasoning strategies — each phase is a self-contained
pattern, connected by field references, gates, and relik writes.

## The composition principle

Every canonical pattern (ToT, reflexion, self-refine, self-consistency,
verify-and-persist) is a **phase**: a group of steps that accomplish one
reasoning task. Phases compose by connecting their inputs and outputs:

- **Forward data flow.** Each phase reads the previous phase's output via
  `ingest` or `multi_ingest` fields.
- **Control flow.** Gates between phases decide whether to advance, retry
  (jump backward), or abort. This is where reflexion and early-exit live.
- **State.** Relik writes persist intermediate results, lessons, and the
  final answer. The next call reads them and starts educated.

The key insight: **patterns don't know about each other.** A ToT generate
phase doesn't know it's feeding into a self-refine phase. It just produces
N candidates and lets the gate prune. The next phase reads whatever
survived. This decoupling is what makes composition safe — you can swap
phases without rewriting the others.

## Phases of a composed ladder

| Phase | Pattern | Purpose | Typical steps |
|-------|---------|---------|---------------|
| Generate | Tree-of-Thoughts | Multi-branch draft | 1 step, N nodes |
| Verify | Per-node gate | Prune weak candidates | 1 step, M verifiers |
| Reflect | Reflexion | Analyze failures, persist lessons | 1 step + backward jump |
| Refine | Self-Refine | Iterative improvement on survivors | 1 sequential step or recursion |
| Aggregate | Self-Consistency | Reduce variance across candidates | 1 synthesis step |
| Persist | Verify-then-Persist | Write result + lessons to reliks | relikWrite on verify/refine |

Not every ladder needs all phases. A simple draft-and-refine ladder uses
only Generate + Refine. A reflexion ladder adds Verify + Reflect. The
Crucible reference ladder uses all six.

## Connecting phases

### Via field references

Each phase reads the previous phase's output. The connection is just a
field declaration:

```yaml
# Generate phase produces N candidates
- id: generate
  nodes: 5
  systemPrompt: "Generate an independent candidate."

# Verify phase reads each candidate
- id: verify
  nodes: 3
  fields:
    - name: Candidate
      type: ingest
      from: { stepId: generate, loopRef: current }
  systemPrompt: "Score the candidate."

# Synthesis phase reads all surviving verified candidates
- id: synthesize
  fields:
    - name: Verified
      type: multi_ingest
      from: [{ stepId: verify, loopRef: current, nodeRef: accumulate }]
  systemPrompt: "Merge the verified candidates into one answer."
```

The `ingest` with `nodeRef: current` pairs each verify node with the
corresponding generate node. After the gate prunes non-matching verify
nodes, only survivors are visible to `synthesize` via `nodeRef: accumulate`.

### Via gates

Gates control whether the composition advances or loops back:

```yaml
- id: verify
  if:
    integerRange: [4, 5]
    then: continue                    # advance to synthesis
    else: { jump: { stepId: reflect } }  # loop back through reflection
```

If any verify node passes, `then: continue` fires and the composition
advances to `synthesize`. If all fail, `else: { jump: { stepId: reflect } }`
fires and the composition enters the reflection phase, which eventually
jumps back to `generate` for another attempt.

### Via relik writes

The final phase persists the result and any lessons learned:

```yaml
- id: synthesize
  relikWrite:
    to: { relik: "Memory", folder: solution, file: best.md }
    on: overwrite
    match: { type: object, required: [answer] }
    field: answer
```

On the next call, the generate phase reads the persisted solution:

```yaml
- id: generate
  fields:
    - name: PriorArt
      type: relikRead
      from: { relik: "Memory", folder: solution, file: best.md }
```

The ladder starts call 2 already educated.

## Budgeting a composed ladder

Each phase costs LLM calls. A composed ladder's total cost is the sum:

| Phase | LLM calls (typical) |
|-------|---------------------|
| Generate (5 nodes) | 5 |
| Verify (3 nodes) | 3 |
| Reflect (1 node) | 1 |
| Refine (3 sequential nodes) | 3 |
| Synthesize (1 node) | 1 |
| **Total per iteration** | **13** |

With 3 reflexion iterations: 39 calls. Plus recursion: ×depth. The spend
ceiling catches runaway compositions. Declare a budget that matches your
expected cost:

```yaml
executionBudget:
  maxSpend: 5.00       # generous for a 5-node ToT + reflexion
  maxHops: 100         # ~7 iterations of the 5-phase pipeline
  maxLlmCalls: 200     # hard structural cap
```

## When NOT to compose

**Start simple.** A two-step draft-and-refine ladder covers most use cases.
Add phases only when the simple version isn't good enough:

- **Add verify** when the draft quality is inconsistent and you need to
  filter bad candidates.
- **Add reflexion** when the ladder makes the same mistakes across calls
  and you want it to learn.
- **Add self-consistency** when variance is high and you need stable
  outputs.
- **Add recursion** when the problem decomposes into sub-problems.

Each phase adds cost and complexity. The best ladder is the simplest one
that solves the problem.

## Worked example: ToT + Reflexion

```yaml
name: ToT with Reflexion
allowedTargets: { strategy: universal }
exit: answer
knobs:
  branches: { name: Branches, type: nodes, input: slider, steps: [...] }
reliks:
  - title: Memory
    folders:
      - { name: lessons, tokenLimit: 400, files: [{ name: failures.md, init: blank }] }
      - { name: solution, tokenLimit: 500, files: [{ name: best.md, init: blank }] }
steps:
  - id: generate
    nodes: branches
    fields:
      - { name: Question, type: text, from: input.context }
      - { name: Lessons, type: relikRead, from: { relik: Memory, folder: lessons, file: failures.md } }
    systemPrompt: "Generate an independent solution. Heed Lessons."

  - id: verify
    nodes: 3
    fields:
      - { name: Candidate, type: ingest, from: { stepId: generate, loopRef: current } }
    systemPrompt: 'Score 1-5. Output JSON {"score": N, "verdict": "accept"|"reject"}.'
    if:
      jsonMatches:
        type: object
        properties:
          verdict: { type: string, enum: [accept] }
          score: { type: number, minimum: 4 }
      then: continue
      else: { jump: { stepId: reflect } }

  - id: reflect
    fields:
      - { name: Failures, type: multi_ingest, from: [{ stepId: verify, loopRef: current, nodeRef: accumulate }] }
    systemPrompt: 'Analyze failures. Output JSON {"lesson": "..."}.'
    relikWrite:
      to: { relik: Memory, folder: lessons, file: failures.md }
      on: append
      match: { type: object, required: [lesson] }
    if:
      jsonMatches: { type: object }       # lesson emitted → jump back
      then: { jump: { stepId: generate } }

  - id: answer
    fields:
      - { name: Solution, type: ingest, from: { stepId: verify, loopRef: current } }
    systemPrompt: "Return the solution as-is."
```

This composes ToT (generate with N branches), per-node gating (verify prunes
to score ≥ 4), reflexion (reflect writes lessons and jumps back), and
persistent memory (reliks survive across calls). Each phase is a
self-contained pattern; the composition is just field references and jump
targets connecting them.

Read the [Patterns Catalog](./patterns-catalog.md) for the full set of
canonical shapes, and the [Gated ToT example](../examples/gated-tot.md)
for a complete walkthrough.

