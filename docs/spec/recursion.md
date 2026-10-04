# Recursion

A step with a recursion block can spawn a child execution of the same ladder from its own output. The child runs the full ladder from step 0 with the parent step's output as its input. When the child completes, its exit output replaces the parent step's output. Recursion enables divide-and-conquer decomposition, deep self-refinement, and any pattern where the same reasoning strategy applies at multiple levels of abstraction.

## Why recursion exists

Reasoning has natural recursive structure. To solve a hard problem, decompose it into sub-problems. Each sub-problem gets the same reasoning strategy applied to it. The sub-problem's solution becomes input to the next level up.

Without recursion, you have two bad options:

- **One fixed-depth ladder.** Hardcode the decomposition depth at author time. Too shallow for hard problems; too expensive for easy ones. No adaptation to problem difficulty.
- **Multiple separate ladders.** Author one ladder per depth. Combinatorial explosion. No way for the runtime to manage the budget across levels.

Recursion collapses both. One ladder, declared once, can apply itself at multiple levels. The depth is controlled by `maxDepth` (which can be a knob the caller sets). The runtime manages the parent-child relationship, propagates outputs, and enforces a global recursion ceiling.

What recursion enables:

- **Divide-and-conquer.** A step that decomposes a problem, recurses on the sub-problem, then composes the result.
- **Deep refinement.** A step that improves its own output recursively, each level polishing the previous level's result.
- **Adaptive depth.** A caller-tunable recursion knob lets the caller trade cost for depth per call.
- **Bounded cost.** The recursion ceiling caps total depth per execution. Combined with the spend cap, total cost is bounded.

Recursion is one of LADR's five power axes. Most ladders do not need it. The ones that do — divide-and-conquer ladders, recursive refinement ladders, anything where the same strategy applies at multiple levels — cannot be expressed without it.

## Recursion anatomy

The recursion block has one required property: `maxDepth`. It accepts a literal integer or a knob name.

```yaml
steps:
  - id: divide
    name: Divide
    fields:
      - { name: Problem, type: text, from: input.context }
    systemPrompt: "Decompose the problem into a smaller sub-problem."
    recursion:
      maxDepth: 3              # literal integer

  # Or with a knob reference
  - id: refine
    name: Refine
    fields:
      - { name: Context, type: text, from: input.context }
    systemPrompt: "Improve depth and coherence."
    recursion:
      maxDepth: "iterations"   # knob name (must be type: recursion)
```

Hard rules:

- Only **one** step per ladder can have a `recursion:` block. Two recursion steps fail validation.
- If `maxDepth` is a string, it must reference an existing knob of `type: recursion`.
- The recursion ceiling (`executionBudget.maxRecursion`, default 5, platform max 20) caps the effective depth. If the ladder declares `maxDepth: 10` but the ceiling is 5, recursion stops at 5.

## How recursion works

When the runtime reaches a step with `recursion:`, this sequence runs:

1. **The step executes normally first.** Fields resolve, the prompt is assembled, the LLM call is made (or multiple calls if `nodes:` is set), the output is stored as numbered nodes. Gates, relik writes, and retrieve loops all run as usual.

2. **The recursion check fires.** If the current depth is below `maxDepth`, the runtime spawns a child instance of the **same ladder config**.

3. **The child's `input.context` is set to the parent step's output.** Every `text` field with `from: input.context` in the child reads this refined output instead of the original user input.

4. **The parent pauses at this step.** The parent does not advance to the next step yet; it waits for the child to complete.

5. **The child runs the full ladder from step 0.** If the child also has the recursion step (it does, because it's the same config), and the child's depth is still below `maxDepth`, the child spawns its own child. The nesting continues until a level hits `maxDepth`.

6. **At `maxDepth`, no more recursion.** The deepest level's recursion step produces its output; no child is spawned. The exit step (whatever the ladder declares) runs and produces that level's final output.

7. **The deepest level's exit output bubbles up.** It replaces the recursion step's stored output at the level above. The level above then runs any remaining steps after the recursion step, produces its own exit output, and bubbles up again.

8. **This continues until the original parent receives the final refined output.** The parent's recursion step output is replaced; any downstream steps in the parent run with the refined output; the parent's exit step produces the final answer returned to the caller.

The net effect: the ladder's reasoning is applied at multiple levels of abstraction, each level building on the result of the level below.

## A worked walkthrough

Consider a three-step ladder where `refine` has `maxDepth: 2`:

```yaml
name: Recursive Polish
allowedTargets: { strategy: universal }
exit: polish
knobs: {}
steps:
  - id: analyze
    name: Analyze
    fields:
      - { name: Context, type: text, from: input.context }
    systemPrompt: "Identify key themes and weaknesses."

  - id: refine
    name: Refine
    fields:
      - { name: Context, type: text, from: input.context }
    systemPrompt: "Improve depth and coherence."
    recursion:
      maxDepth: 2

  - id: polish
    name: Polish
    fields:
      - { name: Refined, type: ingest, from: { stepId: refine, loopRef: current } }
    systemPrompt: "Final polish for clarity and tone."
```

The user calls the ladder with "Write an essay on vector databases."

**Level 0 (the original call, depth = 0):**

- `analyze` reads "Write an essay on vector databases" and identifies themes.
- `refine` reads the same input, produces a draft; call it **R0**.
- Recursion check: depth 0 < `maxDepth` 2, so a child is spawned with `input.context = R0`. The parent **pauses here**.

**Level 1 (first child, depth = 1):**

- The child starts from step 0 with `input.context = R0`.
- `analyze` reads R0 and identifies what to improve.
- `refine` reads R0, produces **R1**.
- Recursion check: depth 1 < 2, so another child is spawned with `input.context = R1`. This child pauses.

**Level 2 (grandchild, depth = 2):**

- The grandchild starts from step 0 with `input.context = R1`.
- `analyze` reads R1 and identifies further improvements.
- `refine` reads R1, produces **R2**.
- Recursion check: depth 2 = `maxDepth` 2. **No more recursion.** R2 is the raw output.
- `polish` runs: it ingests R2 via the `Refined` field and produces **P2**.
- The exit step is `polish`, so the grandchild returns P2 as its final output.

**Back to Level 1:**

- The grandchild returned P2. This replaces the `refine` step's stored output (was R1, now P2).
- `polish` runs: it ingests P2 and produces **P1**.
- The child returns P1.

**Back to Level 0:**

- The child returned P1. This replaces the `refine` step's stored output (was R0, now P1).
- `polish` runs: it ingests P1 and produces **P0**.
- The exit step is `polish`, so P0 is the ladder's final answer returned to the caller.

The output went through **three rounds of analyze + refine** (one per depth level) and **three rounds of polish** (one per depth level). Each level builds on the previous level's result. The deepest refinement propagates all the way back up through every level's exit processing.

## What the child sees

Every child instance runs the same ladder config: same steps, same fields, same system prompts, same exit step. Three things differ from the parent.

**`input.context` is replaced.** The child's `input.context` is the parent recursion step's output, not the caller's original message. Every `text` field with `from: input.context` in the child reads the refined output. This is the mechanism that lets each level build on the previous one.

**`maxLoops` is always 1.** Regardless of the parent's loop count, the child runs a single pass through the steps. No looping in the child. This means:

- `loopRef: accumulate` in the child collects nothing; there are no previous loops.
- `loopRef: 0` works; loop 0 is the only loop that exists.
- A `multi_ingest` field on the child's recursion step with `loopRef: accumulate` will be empty on the child's only pass.

**Knob values are inherited.** The child gets the same knob values as the parent. If the parent had `branches: 8`, the child also has `branches: 8`. Node counts driven by knobs match the parent's. The recursion knob itself is also inherited, but since the child checks `depth >= maxDepth`, the knob value still controls when recursion stops.

Nothing else changes. The system prompts are identical. The relik declarations are identical (and the relik state is shared — writes in the child are visible in the parent after the child completes). The exit step is the same.

## Loops and recursion together

Loops and recursion are independent dimensions of control flow. The loop counter does not advance during child execution.

When a looping ladder has a recursion step:

1. The parent hits the recursion step on loop 0, spawns a child, waits for it, gets the refined result, stores it.
2. The parent moves to loop 1. The recursion step runs again, spawns **another** child, waits, gets the result, stores it.
3. Each loop iteration gets its own independent child. The child always runs with `maxLoops: 1`; it does one clean pass.

The recursion step's refined output (after the child returns) is what gets stored for that loop iteration. A `multi_ingest` field with `loopRef: accumulate` on the recursion step collects the refined outputs, one per loop.

**Example.** A ladder with `maxLoops: 3` and `maxDepth: 2`. The recursion step runs 3 times (once per loop). Each time, it spawns a child that itself spawns a grandchild. The grandchild's polished output bubbles up to the child, then to the parent. The parent stores that polished output for that loop iteration.

Loop 2's `multi_ingest` field sees: refined output from loop 0 + refined output from loop 1. Both have gone through two levels of recursion. The accumulated list grows by one per loop, just like in a non-recursive looping ladder.

**Edge case: recursion step is also the exit step.** If the recursion step is also the exit step — meaning there are no steps after it — the behavior is simpler. The deepest level's exit output bubbles straight up through each level without any further processing at intermediate levels, because there are no steps to run after the recursion step. The original parent's recursion step output is replaced; the parent's exit output is that replaced value; the caller gets it directly.

## Depth control

`maxDepth` controls how many levels of recursion can happen.

- `maxDepth: 0` means the recursion step produces its output but no child ever spawns. The `recursion:` block is effectively a no-op. Validation does not reject this, but it's pointless.
- `maxDepth: 1` means one level of recursion. The parent spawns a child; the child does not spawn a grandchild.
- `maxDepth: 2` means up to two levels deep (parent → child → grandchild).
- Higher values trade cost for depth.

Using a knob reference lets the caller control depth at call time:

```yaml
knobs:
  iterations:
    name: Iterations
    type: recursion
    input: numerical
    default: 2
    min: 1
    max: 5

steps:
  - id: refine
    recursion:
      maxDepth: "iterations"
    # ...
```

The knob must be `type: recursion`. The resolved value becomes the maximum recursion depth for this step.

The recursion ceiling caps the effective depth. If `maxDepth` is 8 but `executionBudget.maxRecursion` is 5 (the default), recursion stops at 5. A ladder author who needs more depth must raise the ceiling up to the platform max (20).

The recursion depth counter is **per-execution**, not per-loop. A looping ladder with recursion does not reset the counter on each loop. If the ceiling is 5 and the ladder recurses to depth 5 on loop 0, loop 1's recursion step will not spawn a child (depth is already at the ceiling). This is rarely the desired behavior; raise the ceiling if you need recursion on every loop.

## Cost and resource accounting

Recursion counts against three runtime ceilings:

- **Hop ceiling.** Each spawned child counts as 1 hop in the parent's hop budget. The child's own internal hops (one per step it runs) also count against the parent's hop budget. A ladder at depth 5 with 4 steps per level burns 5 × 4 = 20 hops just for the recursion.
- **LLM call ceiling.** Every LLM call in every child counts against the parent's call cap. A 3-node recursion step at depth 4 burns 3 × 4 = 12 calls.
- **Spend ceiling.** Every call's token cost accumulates against the parent's spend cap. This is what bounds the worst case.

A recursive ladder that hits any ceiling exits gracefully with the best-so-far output. The runtime publishes an `error` event with `ceiling_exceeded: true` and the specific ceiling name in the message.

The recursion ceiling itself is a separate hard cap on depth. It's enforced before any LLM call runs at the new level, so a recursion-bound ladder does not pay for the call that would have exceeded the depth.

**Budgeting recursive ladders.** A safe pattern: set `maxRecursion` to your actual maximum useful depth (typically 2-4), and set `maxSpend` to your dollar budget. The runtime will exit gracefully if either is hit. Don't try to compute the exact call count in advance; let the ceilings do their job.

## A recursive refinement ladder

```yaml
name: Recursive Draft Refinement
allowedTargets: { strategy: universal }
exit: final
knobs:
  iterations:
    name: Iterations
    type: recursion
    input: numerical
    default: 2
    min: 1
    max: 5
  rounds:
    name: Rounds
    type: loops
    input: numerical
    default: 3
    min: 1
    max: 5
executionBudget:
  maxSpend: 5.0
  maxRecursion: 5

steps:
  - id: draft
    name: Draft
    fields:
      - { name: Context, type: text, from: input.context }
    systemPrompt: "Write an initial draft."

  - id: final
    name: Final Draft
    recursion:
      maxDepth: "iterations"
    fields:
      - { name: Context, type: text, from: input.context }
      - { name: Earlier, type: multi_ingest, from: [{ stepId: final, loopRef: accumulate }] }
    systemPrompt: "Review and improve. Build on earlier drafts."
```

**Walkthrough with default knobs (3 rounds, maxDepth 2):**

The ladder runs 3 loops. On each loop:

1. `draft` produces an initial draft from the context.
2. `final` reads the context plus all previous loops' final outputs (accumulated).
3. `final` produces its output, then spawns a child (depth 1).
4. The child's `input.context` is `final`'s output; it runs the same two steps.
5. `final` in the child spawns a grandchild (depth 2).
6. The grandchild runs but does not recurse further (depth = `maxDepth`).
7. The grandchild's exit output bubbles up through both levels, replacing the parent `final` step's output.
8. That refined output is stored for this loop iteration.

On the next loop, the `Earlier` field accumulates one more entry. The accumulation is of refined outputs, not raw ones.

## Validation rules

Recursion-specific rules checked at config load time:

- **At most one recursion step.** Only one step per ladder can have a `recursion:` block. Two recursion steps fail with: `At most one step can have recursion (found N)`.
- **`maxDepth` type.** Must be a positive integer or a string. A negative number, zero (technically allowed but pointless), or a non-knob string fails.
- **`maxDepth` knob reference.** If `maxDepth` is a string, it must reference an existing knob of `type: recursion`. Referencing a non-existent knob fails: `Step "X": recursion.maxDepth references non-existent knob "Y"`. Referencing a knob of the wrong type fails similarly.
- **`recursion:` block placement.** Recursion is only valid on `normal` and `sequential` steps. A `group` step cannot have a `recursion:` block.

The recursion ceiling (`executionBudget.maxRecursion`) is checked separately as part of execution budget validation. Values above the platform max (20) fail.

## Common patterns

**Deep self-refinement.** A ladder that recursively improves its own output, each level polishing the previous level's result.

```yaml
- id: refine
  recursion: { maxDepth: "iterations" }
  fields:
    - { name: Context, type: text, from: input.context }
  systemPrompt: "Improve clarity and depth. Output only the refined version."

exit: refine   # exit step = recursion step (no further processing)
```

The output goes through N rounds of refinement, each building on the last. Total cost: N × tokens-per-call.

**Divide-and-conquer.** A ladder that decomposes a hard problem into a smaller sub-problem, solves the sub-problem recursively, then assembles the result.

```yaml
- id: decompose
  fields:
    - { name: Problem, type: text, from: input.context }
  systemPrompt: "If the problem is small enough to solve directly, output the solution. Otherwise, output a smaller sub-problem to solve first."
  recursion: { maxDepth: 4 }

- id: compose
  fields:
    - { name: SubSolution, type: ingest, from: { stepId: decompose, loopRef: current } }
    - { name: Original, type: text, from: input.context }
  systemPrompt: "Given the original problem and a sub-problem's solution, compose the final answer."

exit: compose
```

The decompose step recurses until the problem is small enough; the compose step assembles the answer from the sub-solution. Works for any problem with natural recursive structure (parsing, theorem proving, hierarchical planning).

**Adaptive depth via knob.** A recursion depth exposed as a caller-tunable knob.

```yaml
knobs:
  depth:
    name: Recursion Depth
    type: recursion
    input: slider
    steps:
      - { title: None,   value: 1, default: true }
      - { title: Medium, value: 3 }
      - { title: Deep,   value: 5 }

steps:
  - id: refine
    recursion: { maxDepth: "depth" }
    # ...
```

The caller picks the trade-off. `None` is no recursion (one pass); `Deep` is 5 levels deep. Same ladder, different cost/quality per call.

## Common pitfalls

**Expecting loop count to reset per recursion level.** It does not. The child runs with `maxLoops: 1` always. A looping ladder that recurses spawns one child per loop iteration, not a looping child.

**Forgetting that recursion depth is per-execution.** A looping ladder that recurses to depth 5 on loop 0 cannot recurse further on loop 1. The depth counter is per-execution, not per-loop. Raise the ceiling if you need recursion on every loop.

**Assuming the child inherits the parent's accumulated state.** The child does not see the parent's accumulated `multi_ingest` data; it sees only its own `input.context` (which is the parent recursion step's output). Pass context via the recursion step's output, not via accumulated fields.

**Setting `maxDepth` above the recursion ceiling.** The ceiling caps effective depth. If you want `maxDepth: 10`, you must also raise `executionBudget.maxRecursion` to at least 10. Otherwise the ceiling silently caps you at 5 (the default).

**Forgetting that the recursion step is the parent of its own recursion.** When the runtime spawns a child, the child runs the whole ladder — including the recursion step at the same position. The recursion check at the child uses the incremented depth, not the original parent's depth. This is how nesting works.

**Treating recursion as a magic solution.** Most ladders do not need recursion. Reflexion (backward jumps) handles iterative refinement more cheaply. Sequential steps handle chain-of-thought within one step. Reach for recursion only when the same reasoning strategy genuinely applies at multiple levels of abstraction.

