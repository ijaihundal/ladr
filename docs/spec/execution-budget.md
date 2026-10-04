# Execution Budget

LADR is Turing-complete: ladders can loop, recurse, jump, and rewrite themselves at runtime. Termination cannot be statically guaranteed (halting problem). Instead, every execution runs under four hard runtime ceilings — spend, hops, LLM calls, recursion depth — declared per-ladder via executionBudget. The spend cap is the primary safety net that makes untrusted published ladders safe to invoke.

## Why the budget exists

Most languages don't need a runtime budget because most languages run on infrastructure the operator controls. A Python script that infinite-loops burns CPU on your machine; you kill it and move on.

LADR is different. Published ladders get invoked by callers who didn't write them and don't trust them. A malicious or buggy ladder could:

- Loop forever, burning tokens.
- Recurse infinitely, blowing stack and budget.
- Fan out 100 nodes per step, multiplying cost.
- Run for hours on a hard problem that never converges.

Grammar restrictions are the obvious answer: cap loops at N, ban backward jumps, forbid recursion. But that fights the domain. Real reasoning is unbounded: a reflexion loop on a hard problem might need 50 iterations; a recursive decomposition might need 8 levels. Static caps make ladders weaker for the cases where they're most useful.

LADR's design: the grammar is Turing-complete; safety is enforced at runtime via hard ceilings. Every execution hits four caps. The runtime hard-kills on breach and returns the best-so-far output. Ladder authors can lower any cap (always safe) or raise it up to the platform max.

The **spend cap is the real safety net.** Every LLM call costs real money. No matter how cleverly a malicious ladder loops or recurses, it cannot avoid paying for each call. The spend cap is the kill switch that bounds total cost regardless of what the ladder does. This is what makes untrusted ladders safe to run — not grammar restrictions, but economics.

## The four ceilings

Every execution runs under four hard ceilings.

| Ceiling | Default | Configurable via | Platform max |
|---|---|---|---|
| **Spend** (dollars) | $5.00 | `executionBudget.maxSpend` | $50.00 |
| **Hops** (loops + jumps + recursion) | 200 | `executionBudget.maxHops` | 10,000 |
| **LLM calls** | 500 | `executionBudget.maxLlmCalls` | 5,000 |
| **Recursion depth** | 5 | `executionBudget.maxRecursion` | 20 |

A ladder can declare its own `executionBudget` to override any of these. The runtime takes the lower of the declared value and the platform max.

```yaml
executionBudget:
  maxSpend: 10.0          # raise the spend cap to $10
  maxHops: 1000           # raise hops to 1000
  maxLlmCalls: 1500       # raise call cap to 1500
  maxRecursion: 10        # raise recursion to 10
```

All four fields are optional. Omit the whole object to use platform defaults for all four. Omit individual fields to use defaults for those while overriding others.

```yaml
# Just override spend; rest stay at default
executionBudget:
  maxSpend: 3.00
```

Platform maximums are hard caps. Declaring `maxSpend: 100` against a platform max of 50 fails validation: `executionBudget.maxSpend (100) exceeds platform max (50)`.

## maxSpend: the spend cap

`maxSpend` is the dollar-cost ceiling per execution. Default: $5.00. Platform max: $50.00.

Every LLM call's token cost is added to a running total in real time. The runtime checks the total after each call. If the next call would push cumulative spend past the cap, the call is never started; the runtime publishes an `error` event with `ceiling_exceeded: true` and exits gracefully with the best-so-far output.

```yaml
executionBudget:
  maxSpend: 8.00      # cap this ladder at $8 per call
```

**Per-token cost.** The platform tracks cost per call based on the provider's pricing for the model used. A call to GPT-4o costs more per token than a call to GPT-4o-mini. The spend cap automatically scales with the model's real cost.

**Best-so-far output.** On cap breach, the runtime returns whatever output was most recently completed — the exit step's output from the most recent completed loop, or for a multi-step pipeline, the best output produced before the cap was hit. The caller sees a partial result, not a crash. The response includes a flag indicating the cap was hit.

**Why the spend cap is the real safety net.** Every other ceiling (hops, calls, recursion) is a structural bound. The spend cap is the economic bound. A malicious ladder could try to use very large node counts or very long prompts to make each call expensive; the spend cap catches that too, because expensive calls hit the cap faster.

**Default $5 is generous.** Most useful ladders cost well under $1 per call. The $5 default covers reflexion loops with several iterations, multi-verify with 5+ verifiers, and modest recursion. Reserve higher caps for ladders you know need them.

## maxHops: the hop ceiling

`maxHops` is the structural bound on total step executions. Default: 200. Platform max: 10,000.

**One hop = one step execution.** Every time the runtime advances the cursor to a step and runs it (resolves fields, dispatches LLM calls, evaluates the gate, applies the action), that's one hop. Hops cost is independent of node count: a 10-node step is 1 hop, just like a 1-node step.

Hops accumulate across:

- **Loops.** Each loop iteration walks the steps array once. A 5-step ladder run for 4 loops = 20 hops.
- **Jumps.** Each jump is itself 1 hop, plus the cost of the target step's execution. A reflexion loop that goes `generate → verify → reflect → generate` 10 times costs 30+ hops.
- **Recursion.** Each spawned child counts as 1 hop, plus the child's own internal hops. A recursion depth of 3 with 4 steps per level = 12 hops.

The default of 200 supports most useful ladder patterns:

- ~40 loop iterations of a 5-step pipeline.
- ~30 reflexion cycles of a 3-step loop.
- Recursion depth 4 with ~10 steps per level.

For ladders that need more, raise the ceiling up to the platform max.

```yaml
executionBudget:
  maxHops: 1000      # supports long reflexion or deep recursion
```

When the hop ceiling is hit, the runtime publishes `error` with `ceiling_exceeded: true` and exits with the best-so-far output.

## maxLlmCalls: the LLM call ceiling

`maxLlmCalls` is the total number of LLM API calls per execution. Default: 500. Platform max: 5,000.

Every LLM call counts: generate nodes, verify nodes, refine nodes, retrieve rounds, recursion levels, dynamic step executions. The runtime increments the counter before each call and checks against the cap.

```yaml
executionBudget:
  maxLlmCalls: 100
```

This ceiling is mostly redundant with `maxSpend` (every call costs money, so spend catches runaway calls too). But it's useful when:

- You want a hard structural cap independent of model pricing.
- You're testing against a free or flat-rate provider where spend doesn't bound calls.
- You want to publish a ladder that's guaranteed to make at most N calls, regardless of which model the caller picks.

When the call ceiling is hit, the runtime publishes `error` with `ceiling_exceeded: true` and exits with the best-so-far output.

## maxRecursion: the recursion depth ceiling

`maxRecursion` is the maximum recursion depth per execution. Default: 5. Platform max: 20.

This ceiling caps the effective depth regardless of what a step's `recursion.maxDepth` declares. If a step declares `maxDepth: 10` but the ceiling is 5, recursion stops at 5.

```yaml
executionBudget:
  maxRecursion: 8      # allow up to 8 levels of recursion
```

The recursion depth counter is per-execution, not per-loop. A looping ladder that recurses to depth 5 on loop 0 cannot recurse further on loop 1.

When the recursion ceiling is hit, the deepest recursion level's exit output bubbles up normally; the parent does not spawn another child. The runtime publishes `error` with `ceiling_exceeded: true` only if the ceiling prevents the ladder from completing its intended recursion. Otherwise, the ladder completes with the recursion truncated at the ceiling.

**Distinct from hop ceiling.** A recursion ceiling of 5 means at most 5 levels deep. Each level still costs its own hops against the hop ceiling. The two ceilings are independent; both apply.

## What happens when a ceiling is hit

When any ceiling is exceeded mid-execution:

1. **The current operation fails fast.** If a hop check fails before a step starts, the step doesn't run. If a call-count check fails before an LLM call, the call doesn't fire. If a spend check fails after a call, subsequent calls don't fire.
2. **The runtime publishes an `error` event** with `ceiling_exceeded: true` and a message identifying which ceiling was hit and the current usage.
3. **The runtime exits gracefully**, returning the best-so-far output. Best-so-far means: the exit step's output from the most recent completed loop, or for a multi-step pipeline that hasn't reached the exit, the best output produced before the cap was hit.
4. **The response includes a flag** indicating the cap was hit, so the caller can distinguish "the ladder finished normally" from "the ladder was truncated by the ceiling."

This is **informational, not an error**. The caller gets a usable response. The cap is doing exactly what it's supposed to do: bounding cost. If the result is unsatisfactory, the caller can either:

- Re-run with a higher budget (via `executionBudget`).
- Restructure the ladder to use fewer calls (lower `nodes`, fewer reflexion iterations, less recursion).

The trace shows exactly where the cap was hit and what the ladder was doing at the time.

## Validation

`executionBudget` is validated at config load time.

- All four fields, when present, must be positive (or non-negative for `maxRecursion`).
- All four fields, when present, must be at or below the platform max:
  - `maxSpend` ≤ 50.0
  - `maxHops` ≤ 10,000
  - `maxLlmCalls` ≤ 5,000
  - `maxRecursion` ≤ 20

Errors look like:

```text
executionBudget.maxSpend must be > 0
executionBudget.maxSpend (100) exceeds platform max (50)
executionBudget.maxRecursion must be >= 0
```

The validation is sanity-only. The runtime re-checks at execution time and clamps any value that somehow exceeds the platform max.

## executionBudget vs the runtime resource budget

Two distinct concepts: the per-ladder `executionBudget` and the per-deployment runtime resource budget.

**`executionBudget`** is what the ladder author declares. It's a YAML field. It overrides platform defaults per-ladder, up to the platform max. Authors control this.

**Runtime resource budget** is what the platform operator configures. It's set via environment variables on the engine process:

| Env var | Default | Controls |
|---|---|---|
| `ENGINE_MAX_LOOPS` | 50 | Hard cap on `maxLoops` regardless of caller request. |
| `ENGINE_MAX_NODES` | 20 | Hard cap on nodes per step. Knobs of `type: nodes` cannot exceed this. |
| `ENGINE_MAX_RECURSION` | 5 | Hard cap on recursion depth. `executionBudget.maxRecursion` cannot exceed this. |
| `ENGINE_MAX_TOTAL_LLM_CALLS` | 500 | Hard cap on total LLM calls per execution. |

Authors do not see or control the runtime resource budget. Operators do. The runtime takes the minimum of (caller request, ladder's declared budget, runtime resource budget) for every ceiling.

## Common patterns

**Tight budget for public directory ladders.** Default ceilings are generous; tighten them for ladders strangers will invoke.

```yaml
executionBudget:
  maxSpend: 1.00       # cheap calls only
  maxHops: 50          # limited reflexion
  maxLlmCalls: 30      # bounded fan-out
```

This ladder cannot cost a caller more than $1 per invocation. Good for shared ladders strangers will invoke.

**High budget for research ladders.** Long-running ladders that need to converge.

```yaml
executionBudget:
  maxSpend: 25.00
  maxHops: 2000
  maxLlmCalls: 2000
  maxRecursion: 10
```

For serious research or hard-problem ladders where the caller is paying a premium for quality.

**Asymmetric budget: tight spend, loose hops.** When you want many small calls but bounded total cost.

```yaml
executionBudget:
  maxSpend: 2.00       # tight spend
  maxHops: 1000        # many hops allowed
  maxLlmCalls: 500
```

Useful for reflexion-heavy ladders using a cheap model (GPT-4o-mini). Many iterations, low per-call cost.

**Override per-call via the API.** Callers can control `maxLoops` per request
via the `maxLoops` field in the request body (clamped to the runtime's
`ENGINE_MAX_LOOPS`). Knobs of `type: nodes` can also be adjusted per-call,
indirectly controlling fan-out cost.

```bash
POST /v1/{author}/{ladder}/chat/completions
{
  "model": "gpt-4o",
  "messages": [...],
  "knobs": { "branches": 3 },
  "maxLoops": 2
}
```

This lets callers bound their own cost by limiting iterations and fan-out.
The spend ceiling declared in `executionBudget` still applies as the hard
cap.

## Common pitfalls

**Forgetting to declare a budget for directory-published ladders.** The default $5 might be more than callers expect. Declare a tight budget for public ladders to set caller expectations.

**Declaring a budget above the runtime max.** Fails validation. The max is fixed per deployment; you can declare up to it, not above.

**Expecting the runtime to warn before breach.** It doesn't. The runtime fails fast on ceiling check; it doesn't pre-warn. If you want to monitor spend during execution, watch the trace events (`error` with `ceiling_exceeded: true` is the indicator).

**Setting recursion ceiling too low for divide-and-conquer.** A divide-and-conquer ladder with 4-step decomposition at depth 5 = 20 hops just for the recursion. Add in the parent's hops and you're at 30+. Make sure both `maxHops` and `maxRecursion` are high enough for the ladder's intended depth.

**Confusing the ladder's budget with the runtime's resource budget.** The ladder's `executionBudget` is what the author declares; the runtime's resource budget is what the operator sets via env vars. A ladder declared with `maxSpend: 10` might still be capped at $5 on a deployment where the operator set a stricter limit. Check the deployment config if your ladder's budget doesn't seem to apply.

**Relying on callers to set the budget.** Callers can tighten the budget; they cannot loosen it. A ladder with no budget declared uses defaults; callers cannot raise those defaults per-call. If your ladder needs more headroom, declare it in the config.

