# Jumps

A jump is an action that repositions the runtime cursor to a different step. Within-ladder jumps enable iterative refinement, reflexion loops, and any pattern that needs to revisit a step. Cross-ladder jumps hand off to a different ladder entirely. Both are bounded by the hop ceiling, which is what makes loops safe without restricting the grammar.

## Why jumps exist

Without jumps, a ladder is a fixed sequence. Every call walks the steps in declaration order, then exits. There's no way to revisit a step, no way to skip ahead, no way to delegate part of the work to another ladder.

That covers a lot of cases. But three patterns are unreachable without jumps:

- **Reflexion.** Generate → verify → reflect on failure → generate again. The "generate again" requires going back to an earlier step. Without backward jumps, you can only do single-shot verify-and-accept-or-abort.
- **Early exit on confidence.** If a verifier is highly confident on the first candidate, skip the rest of the pipeline and synthesize immediately. Without forward jumps, you always pay the full pipeline cost.
- **Specialist composition.** A general-purpose ladder hits a hard sub-problem and hands off to a specialist. Without cross-ladder jumps, every ladder has to be self-contained.

Jumps are how LADR makes the steps array a **mutable cursor** instead of a fixed iteration. The runtime can move forward, backward, or to a different ladder entirely. Combined with the hop ceiling, this enables unbounded-feeling loops that are still safe to run on untrusted input.

Every jump counts as one hop against the ladder's hop ceiling. A reflexion loop that runs 50 times eats 50 hops. A ladder with a hop ceiling of 200 can support up to ~50 reflexion iterations before the ceiling kicks in. The ceiling is the safety net; the grammar places no restriction on how many times you jump.

## Declaring a jump

Jumps are declared under `then:` or `else:` in an `if:` block (see Control Flow). They fire when the gate evaluates and the matching branch is reached.

```yaml
- id: verify
  if:
    integerRange: [4, 5]
    then: continue                            # accept: advance normally
    else: { jump: { stepId: refine } }        # reject: jump back to refine

- id: classify
  if:
    integerEquals: 5
    then: { jump: { stepId: synthesize } }    # very confident: skip ahead
    else: { jump: { stepId: expand } }        # not confident: gather more
```

A jump action has two forms:

```yaml
# Within-ladder: target a step in the current ladder.
{ jump: { stepId: "refine" } }

# Cross-ladder: hand off to another ladder.
{ jump: { ladderId: "@alice/helper" } }
{ jump: { ladderId: "@alice/helper", stepId: "step3" } }
```

Jumps fire at **step level**, after per-node pruning has reduced the survivors. The gate first evaluates against every node's output; non-matching nodes are pruned; then the `then:` action fires if any survivors remain (or the `else:` action fires if none do). If that action is a jump, the cursor repositions before the next step executes.

## Within-ladder jumps

A within-ladder jump repositions the cursor to a step in the current ladder. The target `stepId` must exist in the same ladder; validation fails otherwise.

**Forward and backward both allowed.**

- **Forward jumps** skip steps conditionally. A verifier that's very confident can jump past the refinement step directly to the synthesis step, saving cost.
- **Backward jumps** create loops. The canonical use case is reflexion: generate → verify → reflect on failure → generate again. Each iteration of the loop costs one hop; the hop ceiling bounds total iterations.

**The target step re-executes from scratch.** Jumping back to `generate` does not preserve the previous run's nodes; the step re-runs with fresh LLM calls. State that needs to persist across iterations must flow through reliks (see Reliks).

```yaml
# Reflexion loop
steps:
  - id: generate
    fields:
      - { name: Question, type: text, from: input.context }
      - { name: Lessons, type: relikRead, from: { relik: "Memory", folder: lessons, file: failures.md } }
    systemPrompt: "Generate a candidate. Heed Lessons from prior failures."

  - id: verify
    fields:
      - { name: Candidate, type: ingest, from: { stepId: generate, loopRef: current } }
    systemPrompt: 'Score 1-5. Output JSON {"score": N, "verdict": "accept"|"reject"}.'
    if:
      integerRange: [4, 5]
      then: continue
      else: { jump: { stepId: reflect } }

  - id: reflect
    fields:
      - { name: Failure, type: ingest, from: { stepId: verify, loopRef: current } }
    systemPrompt: 'Analyze why the candidate failed. Output JSON {"lesson": "..."}.'
    relikWrite:
      to: { relik: "Memory", folder: lessons, file: failures.md }
      on: append
      match: { type: object, required: [lesson] }
    then: { jump: { stepId: generate } }   # backward jump; reflexion loop
```

Each cycle: generate, verify, reflect (writing a lesson), back to generate. The next `generate` reads the accumulated lessons via `relikRead` and avoids past mistakes. The hop ceiling bounds how many iterations run; the spend ceiling bounds total cost.

## Cross-ladder jumps (handoff)

A cross-ladder jump terminates the current execution and starts a new one in a different ladder. The current step's output becomes the target ladder's `input.context`. This is a **tail-call**, not a recursive call; the current ladder stops entirely.

```yaml
# Hand off to another ladder from its first step.
then: { jump: { ladderId: "@alice/helper" } }

# Hand off to a specific step in another ladder.
then: { jump: { ladderId: "@alice/helper", stepId: "step3" } }

# Local (unpublished) ladders use the local/ namespace.
then: { jump: { ladderId: "local/my-experiment" } }
```

When a cross-ladder jump fires:

1. **Current step's output becomes the target ladder's `input.context`.** The target ladder reads it via `text` fields with `from: input.context`.
2. **Current execution terminates.** No further steps in the current ladder run. This is a tail-call; control does not return.
3. **The target ladder inherits knobs and provider chain.** Budget ceilings also carry over (the combined execution still respects the original spend cap).
4. **Mid-ladder entry (when `stepId` is specified).** The engine skips ahead to the named step on the first loop iteration. Steps before the entry point are not executed. Subsequent loops (if any) run from step 0 normally. The engine publishes a `step_start` event with `midLadderEntry: true` to signal the skip.
5. **PassField validation.** If the jump declared `passFields`, the engine validates every `passField.to` key against the declared fields on the target step. Mismatch is fatal unless the firing step declares `onInvalid:`.

**Mid-ladder entry is powerful but risky.** Jumping into `step3` of another
ladder means steps 1 and 2 never ran — any setup, context gathering, or
relik initialization they would have done is skipped. Use mid-ladder entry
only when you control both ladders and understand what the skipped steps
would have done.

**Use cases:**

- **Specialist composition.** A general-purpose ladder hits a sub-problem and hands off to a specialist. `@alice/researcher` gathers context; `@bob/writer` synthesizes the final answer.
- **Routing.** A "router" ladder whose only job is to inspect the input and dispatch to the right specialist. One published endpoint, many possible behaviors.
- **Versioned upgrades.** Pin to `@alice/helper@v3` for reproducibility, or always use `@alice/helper` to get the latest.

The target ladder's `exit:` step output becomes the user-visible response. To the caller, it looks like the original ladder produced the output; the cross-ladder hop is invisible except in the trace.

## Ladder addressing

The canonical ladder identifier is `@author/name` (published) or `local/name` (self-hosted). **The identifier is the handle**; there is no separate permalink/ID split.

```yaml
# Public ladder, latest version.
{ ladderId: "@alice/helper" }

# Public ladder, pinned to a specific version.
{ ladderId: "@alice/helper@v3" }

# Org namespaced ladder (no leading @).
{ ladderId: "@alice/tot" }

# Local (unpublished, self-hosted only).
{ ladderId: "local/my-experiment" }
```

| Form | Meaning |
|------|---------|
| `@author/name` | Latest version of a user's ladder. |
| `@author/name@v3` | Pinned to version 3; reproducible. |
| `org/name` | Org-namespaced ladder (no leading `@`). |
| `local/name` | Unpublished, self-hosted only. Never on the directory. |

**Versioning.** `@author/name` is "latest"; `@author/name@v3` pins for reproducibility. Once published, a version is immutable; benchmarks and references stay valid forever.

**Username renames.** Permanent redirects (GitHub model). References must keep working forever; `@alice/old-name` still resolves after alice renames to `@aliceprime`.

**Forking.** GitHub-style with preserved lineage. `@bob/tot` can declare it was `forkedFrom: @alice/tot@v3`. The directory shows the lineage; the fork is a first-class ladder.

**Why URL-as-identifier.** Memorable and shareable (people can type and remember it). Indexable by search (every published ladder is a real public page). Crawlable by future training corpora — publishing measured reasoning strategies to a crawlable directory means future LLMs train on "which strategies work." The URL is the identifier because one thing is simpler than two.

## Validation

Jump-target validation runs at config load time.

**Within-ladder jumps.** Every `jump.stepId` in every `if.then`, `if.else`, and `onInvalid` field must reference an existing step in the current ladder. References to non-existent steps fail with: `Step "X": then jump references non-existent step "Y"`.

**Cross-ladder jumps.** Every `jump.ladderId` must parse as a valid ladder identifier. The validator checks the format (`@author/name[@v3]` or `local/name`); it does not check whether the ladder actually exists at validation time (that's a runtime check against the directory).

```text
Step "delegate": then jump has invalid ladderId "@alice": expected @author/name, got "@alice"
```

The validator also checks the addressing grammar:

- `@author/name` requires non-empty author and name.
- `local/name` requires non-empty name and no further slashes.
- Version suffix `@vN` requires `N` to be numeric.

Malformed identifiers fail validation before the ladder can run.

## Jumps and the hop ceiling

Every jump costs exactly **one hop** against the ladder's hop ceiling. This is what makes backward jumps safe.

A reflexion loop that runs 50 iterations eats 50 hops. If the ceiling is 200, that's fine. If the caller supplies `maxLoops: 500` via the API, the runtime clamps to 200 (the ceiling) and publishes a `node_count_resolved` event with `clamped: true`.

When the hop ceiling is hit mid-execution, the runtime publishes an `error` event with `ceiling_exceeded: true` and exits gracefully with the best-so-far output. The caller sees a partial result, not a crash.

The hop ceiling is per-execution, not per-loop. A ladder that does 100 hops on loop 0 has only 100 left for loops 1+. This is rarely a problem in practice; most ladders use far fewer hops than the ceiling allows.

**Budgeting jump-heavy ladders.** A safe pattern: estimate your worst-case hop count (number of steps × max reflexion iterations × max loops), declare an `executionBudget.maxHops` that comfortably exceeds it, and let the runtime enforce the ceiling as the safety net. Don't try to compute exact hop counts in the ladder logic; let the ceiling do its job.

## Common patterns

**Reflexion loop.** Generate → verify → reflect on failure → back to generate.

```yaml
- id: generate
  fields:
    - { name: Context, type: text, from: input.context }
    - { name: Lessons, type: relikRead, from: { relik: "Memory", folder: lessons, file: failures.md } }
  systemPrompt: "Generate a candidate. Heed Lessons from prior failures."

- id: verify
  fields:
    - { name: Candidate, type: ingest, from: { stepId: generate, loopRef: current } }
  systemPrompt: 'Output JSON {"score": 1-5, "verdict": "accept|reject"}.'
  if:
    integerRange: [4, 5]
    then: continue
    else: { jump: { stepId: reflect } }

- id: reflect
  fields:
    - { name: Failure, type: ingest, from: { stepId: verify, loopRef: current } }
  systemPrompt: 'Output JSON {"lesson": "...", "root_cause": "..."}.'
  relikWrite:
    to: { relik: "Memory", folder: lessons, file: failures.md }
    on: append
    match: { type: object, required: [lesson] }
  then: { jump: { stepId: generate } }
```

Each iteration persists a lesson; the next generate reads it. The ladder learns within a single call.

**Early exit on confidence.** Skip steps when the verifier is highly confident.

```yaml
- id: classify
  systemPrompt: 'Output JSON {"confidence": "high"|"medium"|"low"}.'
  if:
    jsonMatches:
      type: object
      properties:
        confidence: { type: string, enum: [high] }
    then: { jump: { stepId: synthesize } }     # skip expansion
    else: { jump: { stepId: expand } }          # gather more
```

Saves cost on easy inputs.

**Specialist handoff.** A router ladder dispatches to a specialist.

```yaml
- id: route
  systemPrompt: 'Output JSON {"specialist": "@alice/math"|"@alice/writing"|"@alice/code"}.'
  if:
    jsonMatches:
      type: object
      properties:
        specialist: { type: string, enum: ["@alice/math"] }
    then: { jump: { ladderId: "@alice/math" } }
    else: continue   # fall through to next-step router cases

- id: route_writing
  if: { ... }
  # ...
```

One published endpoint (`@alice/router`) dispatches to many specialists. To the caller, the API surface is one ladder; under the hood, the work happens elsewhere.

**Multi-stage pipeline via handoff.** `@alice/researcher` gathers context; `@bob/writer` synthesizes; `@carol/editor` refines. Each stage is a separate ladder, composed via cross-ladder jumps. Each ladder can be developed, versioned, and benchmarked independently.

## Common pitfalls

**Forgetting that backward jumps re-run the target step from scratch.** State that needs to persist across iterations must flow through reliks. `generate`'s second invocation does not see `generate`'s first output unless you explicitly ingest it via `multi_ingest` with `loopRef: accumulate` (within one loop) or persist it to a relik (across iterations via jumps).

**Setting the hop ceiling too low for reflexion.** A reflexion loop with 5 steps needs 5 hops per iteration. A ceiling of 20 only supports 4 iterations. Either raise `executionBudget.maxHops` or accept that reflexion will exit early on hard problems.

**Cross-ladder jumps inherit the budget.** If you hand off to `@alice/helper`, the helper runs against the **original** spend cap, not a fresh one. A chain of N ladders shares one ceiling. Plan the chain's total cost when picking the first ladder's `executionBudget`.

**Pinning to `@latest` for reproducibility.** `@alice/helper` resolves to the latest version at execution time. If alice publishes a breaking change, your ladder's behavior changes too. For reproducible results, pin: `@alice/helper@v3`.

**Using cross-ladder jumps as a function call.** Cross-ladder jumps are tail-calls; control does not return. If you want to "call" another ladder and continue, you need that ladder's exit output as your next step's input — which means using recursion or another composition pattern. Jumps are not function calls; they are handoffs.

**Jumping into the middle of another ladder.** `{ ladderId: "@alice/helper", stepId: "step3" }` works but skips steps 1 and 2 in the helper. The helper may not behave correctly without its setup steps. Use mid-ladder entry only when you control both ladders and understand the consequences.

**Relik state does not cross the handoff boundary.** Each ladder has its own declared reliks populated from their own `init:` state. The firing ladder's relik contents are NOT visible to the target. If you need to pass relik-derived data, the firing step must read it (via `relikRead` or a `from: { relik, ... }` reference) and either let it flow through the step's output (which becomes the child's `input.context`) or use `passFields` to ship it explicitly to a named field on the target. See [Fields](./fields.md) and the passFields section below.

**Local namespace without registration.** `local/my-experiment` resolves from the local database. In a fresh self-hosted environment with no local ladders saved, any `local/` handoff returns "ladder not found." Published `@author/name` ladders always resolve via the DB. See [Addressing](../reference/addressing.md) for the runtime semantics.

## Parameter passing (passFields)

By default, the only thing that crosses a jump boundary is `input.context` (the firing step's combined output). For richer composition — passing multiple distinct named values into the target's declared fields — declare `passFields:` on the jump.

```yaml
then:
  jump:
    ladderId: "@alice/helper"
    stepId: "step4"
    passFields:
      - to: Question                    # target field name
        from: { stepId: route }         # source, resolved in CALLER's context
      - to: Context
        from: "literal text"
```

**Mechanic.** For each `passField`, the engine resolves `from` in the caller's runtime context using the unified `from:` vocabulary (`previous`, `output`, `input.X`, `{stepId}`, `{relik,...}`, or literal). The resolved string is dropped directly into the target step's matching-named field, overriding whatever the field's declared `from:` would have produced.

**Resolution priority** (highest to lowest) when the target step runs:

1. **passField override** — if a caller's `passField.to` targets this field, the passed value wins outright.
2. **Normal `from:`** — declared primary source.
3. **`fallback:`** — declared secondary source, used when primary misses.

**Works for intra-ladder and cross-ladder jumps.** The grammar is identical; the only difference is whether `ladderId` is set. Intra-ladder: cursor repositions to the target step with the override map applied. Cross-ladder: tail-call handoff fires with the override map shipped to the child instance.

**Validation timing depends on jump type:**

- **Intra-ladder:** `passField.to` checked statically at config load time (target step exists in the same `stepMap` the validator already walks).
- **Cross-ladder:** `passField.to` checked at handoff-load-time — when the engine has just fetched the target config via the LadderResolver. Mismatch is fatal to the handoff unless the firing step declares an `onInvalid:` fallback action.

**The target is configured once, callable by anyone.** The target ladder's author declares their step's fields with normal `from:` (and optional `fallback:`); any caller can read the published field list and wire `passFields` to those names. A new caller can adopt a published target without the target being republished. The published field list IS the target's callable contract.

See [Fields](./fields.md) for the full `from:` vocabulary and the auto-injected input keys (`input.context`, `input.systemTime`, `input.localTime`, `input.timezone`).

