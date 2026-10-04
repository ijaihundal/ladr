# Reference Ladder: Crucible (Design Spec)

> For a practical phase-by-phase walkthrough with cost analysis and
> variations, see [Examples > Crucible](../examples/crucible.md).

Crucible is the reference ladder: a worked example that composes five published reasoning mechanisms — Tree-of-Thoughts, multi-verify, reflexion, self-refine, and self-consistency — with persistent memory via reliks.

## Design rationale

Most published reasoning strategies focus on a single mechanism. Crucible demonstrates how the LADR primitives let an author compose several into one ladder: schema-verified persistence, cross-call memory, and conditional control flow combine so that each call's verified outputs and lessons are available to the next.

The name is a metaphor: a crucible melts material down, keeps only what survives, and pours that forward. In the ladder, only schema-verified claims persist; failed verifications produce lessons that later calls read back.

## Strategy

Generate → Multi-Verify → Survive-or-Reflect → Refine → Validate, with everything persisting.

**Five mechanisms, composed:**

1. **Tree of Thoughts (ToT)**; multi-node `generate` fans out into parallel candidate solutions.
2. **Self-consistency**; `verify` step with multiple verifiers prunes candidates via per-node gates.
3. **Reflexion**; backward jump from `verify` to `generate` on failure, with lessons written to a relik.
4. **Self-refine**; `refine` step sequential-chains through the best verified candidate.
5. **Verification**; `final_check` gate decides accept vs. loop-back-to-reflect.

**Plus persistent memory**; three relik folders (`trace`, `lessons`, `solution`) that survive across calls. Next invocation, `generate` reads `PriorArt` and `Lessons` from the relik. The ladder walks in already educated.

## Full Config

```yaml
name: Crucible
allowedTargets: { strategy: universal }
exit: answer
knobs:
  branches:    { name: Branches, type: nodes, input: slider, steps: [{title: Lean, value: 3}, {title: Balanced, value: 5, default: true}, {title: Deep, value: 8}] }
  verifiers:   { name: Verifiers, type: nodes, input: slider, steps: [{title: Quick, value: 2}, {title: Standard, value: 3, default: true}, {title: Rigorous, value: 5}] }
  iterations:  { name: Iterations, type: recursion, input: numerical, default: 2, min: 1, max: 4 }
  threshold:   { name: Threshold, type: generic, input: slider, steps: [{title: Permissive, value: 3}, {title: Balanced, value: 4, default: true}, {title: Strict, value: 5}] }

reliks:
  - title: Crucible Memory
    folders:
      - name: trace
        tokenLimit: 800
        access: readWrite
        metatags: [{name: score, type: number}, {name: tags, type: string[]}]
        files: [{name: verified.md, init: blank}]
      - name: lessons
        tokenLimit: 400
        access: readWrite
        metatags: [{name: severity, type: number}, {name: tags, type: string[]}]
        files: [{name: failures.md, init: blank}]
      - name: solution
        tokenLimit: 1000
        access: readWrite
        files: [{name: current.md, init: blank}, {name: best.md, init: blank}]

steps:
  - id: generate
    type: normal
    nodes: "branches"
    fields:
      - { name: Context, type: text, from: input.context }
      - { name: Branch, type: nodeInfo }
      - { name: PriorArt, type: relikRead, from: { relik: "Crucible Memory", folder: trace, file: verified.md } }
      - { name: Lessons, type: relikRead, from: { relik: "Crucible Memory", folder: lessons, file: failures.md } }
      - { name: Best, type: relikRead, from: { relik: "Crucible Memory", folder: solution, file: best.md } }
    systemPrompt: "You are the branch whose number is in the Branch field above. Generate an independent solution. Build on PriorArt and Best where useful, heed Lessons, but propose a complete answer."

  - id: verify
    type: normal
    nodes: "verifiers"
    fields:
      - { name: Candidate, type: ingest, from: { stepId: generate, loopRef: current } }
      - { name: Context, type: text, from: input.context }
    systemPrompt: 'Independently verify. Emit JSON {"score": 1-5, "issues": [...], "verdict": "accept|reject"}. 5=provably correct.'
    if:
      integerRange: [4, 5]
      then: continue
      else:
        write: { to: { relik: "Crucible Memory", folder: lessons, file: failures.md }, from: output, on: append }

  - id: tally
    type: normal
    fields:
      - { name: Survivors, type: multi_ingest, from: [{ stepId: verify, loopRef: current, nodeRef: accumulate }] }
    systemPrompt: 'Emit JSON {"count": <number of survivors>, "best_score": <int>}.'
    relikWrite:
      to: { relik: "Crucible Memory", folder: trace, file: verified.md }
      field: count
      on: append
      match: { type: object, required: [count, best_score], properties: { count: {type: number}, best_score: {type: number} } }
    if:
      integerRange: [1, 99]
      then: { jump: { stepId: refine } }
      else: { jump: { stepId: reflect } }

  - id: reflect
    type: normal
    fields:
      - { name: Failures, type: multi_ingest, from: [{ stepId: verify, loopRef: current, nodeRef: accumulate }] }
      - { name: PriorLessons, type: relikRead, from: { relik: "Crucible Memory", folder: lessons, file: failures.md } }
    systemPrompt: 'All candidates failed. Emit JSON {"root_cause": "...", "lesson": "...", "next_strategy": "..."}.'
    relikWrite:
      to: { relik: "Crucible Memory", folder: lessons, file: failures.md, metatag: tags }
      field: lesson
      on: append
      match: { type: object, required: [root_cause, lesson], properties: { root_cause: {type: string}, lesson: {type: string}, next_strategy: {type: string} } }
    if:
      jsonMatches: { type: object }           # lesson emitted → jump back
      then: { jump: { stepId: generate } }     # backward jump; bounded by hop + spend ceilings

  - id: refine
    type: sequential
    nodes: "iterations"
    fields:
      - { name: Best, type: ingest, from: { stepId: verify, loopRef: current } }
      - { name: Previous, type: ingest, from: { stepId: refine, loopRef: current, nodeRef: previous }, skipFirstNode: true }
      - { name: Context, type: text, from: input.context }
    systemPrompt: "Refine the best verified candidate. Address every issue the verifiers raised. Output only the refined solution."

  - id: final_check
    type: normal
    fields:
      - { name: Refined, type: ingest, from: { stepId: refine, loopRef: current } }
      - { name: Context, type: text, from: input.context }
    systemPrompt: 'Final validation. Emit JSON {"score": 1-5, "ready": true|false}.'
    if:
      integerEquals: 5
      then:
        write: { to: { relik: "Crucible Memory", folder: solution, file: best.md }, from: previous, on: overwrite }
      else: { jump: { stepId: reflect } }

  - id: answer
    type: normal
    fields:
      - { name: Solution, type: relikRead, from: { relik: "Crucible Memory", folder: solution, file: best.md } }
    systemPrompt: "Return the solution as-is."
```

## Which Features It Uses

Crucible uses **every load-bearing feature** in the spec, each doing work it couldn't do otherwise:

- **Typed integer gates**; clean numeric verifier signal, not brittle string matching.
- **Per-node `continue` + step-level `else: jump`**; verifiers that pass survive; verifiers that fail jump to a persist step that writes failure analysis as a lesson. Pruning becomes productive signal.
- **Backward jumps**; reflexion without backward jumps is "try again once." With backward jumps, iterate generate→verify→reflect until hop or spend ceiling runs out, compounding lessons.
- **Schema-matched writes**; only schema-conforming scores/lessons hit the relik. No prose noise; structured, queryable signal.
- **Metatags at folder + file level**; categorical retrieval ("lessons tagged 'calculus-error' with severity > 3") without semantic search.
- **Persistence across calls**; next invocation, `generate` reads `PriorArt` and `Lessons`. Ladder walks in already educated.
- **Recursion on refine**; refinement can spawn a child Crucible for sub-problems. Hard problems decompose.

## Execution Walkthrough

Call 1, with default knobs (5 branches, 3 verifiers, 2 iterations, threshold 4):

1. **`generate`** fans out 5 parallel nodes. Each reads the user's context + (empty on call 1) PriorArt/Lessons/Best, produces an independent candidate.
2. **`verify`** runs 3 verifier nodes against each generate survivor. Each verifier scores 1–5 and emits JSON. Per-node gate: scores in [4,5] survive; scores in [1,3] are pruned AND their output is written to `lessons/failures.md` (gated write on `else`).
3. **`tally`** counts survivors. If any survivors → jump to `refine`. If zero → jump to `reflect`.
4. **`refine`** runs sequentially through the best verified candidate. Each node refines the previous node's output. Node 1 starts from the verifier's pick; nodes 2+ each refine further.
5. **`final_check`** runs a final pass. If score = 5 → write to `solution/best.md` (overwrite). Else → jump back to `reflect`.
6. **`reflect`** (only reached on failure) reads all failures + prior lessons, emits a structured lesson, appends it to `lessons/failures.md`, then jumps back to `generate`. The next `generate` pass reads the new lessons; the ladder has learned.
7. **`answer`** returns `solution/best.md` verbatim.

The loop terminates when either (a) `final_check` accepts, (b) the hop ceiling is hit (default 200), or (c) the spend cap is hit (default $5). In all cases, the best-so-far answer is returned.

**Call 2:** the same user calls again with a new question. Now `generate` reads `PriorArt` (the verified trace from call 1) and `Lessons` (the accumulated failures). The ladder starts already educated.

## Honest Failure Modes

Crucible is not magic. It has documented failure modes:

- **Verifier drift**; miscalibrated verifier → wrong gate → wrong loop. Multi-verifier majority mitigates but doesn't eliminate.
- **Lesson pollution**; reflexion can amplify wrong lesson. Relik accumulates gist, but gist can be wrong.
- **Resource ceiling exhaustion on hard problems**; terminates before convergence; user sees score-3 output not knowing one more cycle would have gotten there.
- **Schema-match silence**; if LLM emits `{score: 4}` without required `verdict`, write silently no-ops. Observable only by checking the relik file contents (no event is published).
- **Cost**; branches × verifiers × loops × recursion can compound into many calls. Cost-adjusted framing matters for published numbers.

These are inherent to the approach. The resource ceilings bound the damage; the observable events surface the issues. But a published result needs to acknowledge them honestly.

