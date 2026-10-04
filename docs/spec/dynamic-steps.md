# Dynamic Steps

A dynamic step is a step that loads its own configuration at runtime, usually from the previous step's output. Combined with the manifest field (a runtime snapshot of everything readable), dynamic steps let a ladder rewrite parts of itself based on observed state. This is the primitive that makes a ladder a self-improving organism rather than a fixed program.

## Why dynamic steps exist

A ladder without dynamic steps is static. Same system prompt, same fields, same node count, same gates every invocation. Every "iteration" is just running the same fixed program again with different inputs. The reasoning strategy is locked at author time.

That covers most of what published reasoning strategies do. Tree-of-Thoughts, reflexion, self-consistency, self-refine — all of them are fixed programs. They can't change their own behavior based on what they observe.

But real reasoning does change strategy mid-flow. A human solving a hard math problem tries one approach, notices it's not working, and switches to a different approach. A research agent reading documents realizes it needs to look at a different folder than it initially planned. A code reviewer notices a specific class of bug and pivots to check for more instances of it.

Dynamic steps give a ladder that capability. A planner step reads what's available, decides what to do next, and emits a step config (system prompt, fields, node count, optional gates and recursion) as its output. The dynamic step loads that config and executes it. The ladder has rewritten its own behavior mid-execution.

This is the load-bearing piece for genuine self-improvement. Without dynamic steps, a ladder can refine its output (via gates, recursion, reflexion) but cannot refine its strategy. With dynamic steps, the strategy itself adapts to the situation.

## How a dynamic step is declared

A dynamic step does **not** declare its own `systemPrompt`, `fields`, or `nodes`. Instead it declares a source via `from:`, and the runtime loads the config from that source at execution time.

```yaml
- id: run_planned
  type: normal
  dynamic: true
  from: previous              # uses the previous step's output directly as config
```

The previous step (the **planner**) is responsible for emitting a valid step config as its output:

```yaml
- id: plan_next
  type: normal
  fields:
    - { name: Available, type: manifest }
    - { name: Context,   type: text, from: input.context }
  systemPrompt: |
    Based on Available sources and the current state, generate the next step.
    Emit JSON: {"systemPrompt": "...", "fields": [...], "nodes": N}
    Reference only sources listed in Available.
  if:
    jsonMatches:                              # self-validate the emitted config
      type: object
      required: [systemPrompt, fields]
      properties:
        systemPrompt: { type: string, minLength: 1 }
        fields: { type: array, items: { type: object }, minItems: 1 }
        nodes: { type: number, minimum: 1, maximum: 5 }
      additionalProperties: false
```

The planner reads the manifest to know what's available. It reasons about what to do next. It emits a JSON config as its output. The dynamic step loads that config and executes it as if it had been declared statically.

Two steps, one plan-and-execute cycle. The plan is data; the execution is mechanical.

## The from: source vocabulary

The `from:` field on a dynamic step uses the same source vocabulary used everywhere else in LADR (writes, adaptive-key result injection). One way to specify a source, everywhere.

```yaml
# Default and most common: direct step output (fast, no relik roundtrip)
- id: run_planned
  dynamic: true
  from: previous

# Reference a specific prior step
- id: run_planned
  dynamic: true
  from: { stepId: plan_next }

# Reference a specific node of a prior step
- id: run_planned
  dynamic: true
  from: { stepId: plan_next, nodeRef: current }

# Reference a relik file (when you want the config persisted or inspectable)
- id: run_planned
  dynamic: true
  from: { relik: "Dynamic", folder: planned, file: next_step.md }

# Reference the current step's own output (rare; recursion-like)
- id: run_planned
  dynamic: true
  from: output

# A literal string (rare; for testing or fixed config injection)
- id: run_planned
  dynamic: true
  from: '{"systemPrompt": "Hello", "fields": [...]}'
```

The default and overwhelmingly most common case is `from: previous`. The planner step emits the config as its output; the dynamic step consumes it immediately. No relik roundtrip; the config exists only in the runtime's memory between the two steps.

Use `from: { relik: ... }` when you want the config to persist across calls. For example: a long-running planner that learns from past plans, where each plan is stored in a relik file for inspection and reuse.

## What the loaded config can contain

The planner emits a JSON object that conforms to the shape of a step config. Because LADR's grammar is Turing-complete, the loaded config **can include control flow** — `if`, `jump`, `recursion` — and adaptive keys (`codeExecution`, `relikRead`, `relikEdit`). The only restriction is no nested `dynamic: true`.

```json
{
  "systemPrompt": "Verify the candidate. Output JSON {score: 1-5, verdict: 'accept'|'reject'}.",
  "fields": [
    { "name": "Candidate", "type": "ingest", "from": {"stepId": "generate", "loopRef": "current"} },
    { "name": "Threshold", "type": "knobInfo", "from": "threshold" }
  ],
  "nodes": 3,
  "if": {
    "integerRange": [4, 5],
    "then": "continue",
    "else": {"jump": {"stepId": "reflect"}}
  },
  "recursion": {"maxDepth": 2},
  "relikWrite": {
    "to": {"relik": "Memory", "folder": "trace", "file": "verified.md"},
    "on": "append",
    "match": {"type": "object", "required": ["score"]}
  },
  "relikRead": {
    "from": {"relik": "Memory", "folder": "docs"},
    "as": "RetrievedDocuments",
    "maxRounds": 3,
    "requestMatch": {...}
  }
}
```

The dynamic step executes as if this config had been written into the YAML directly. Gates, jumps, recursion, writes, adaptive keys — all work identically to static steps.

Hard restriction: the loaded config **cannot** itself contain `dynamic: true`. A dynamic step cannot spawn another dynamic step. This prevents direct infinite self-modification loops. (Indirect loops via jumps are still possible and bounded by the hop and spend ceilings.)

## The manifest field

The planner step needs to know what's available before it can decide what to do next. That's the role of the `type: manifest` field.

```yaml
fields:
  - name: Available
    type: manifest
```

At runtime, this resolves to a JSON snapshot of everything readable at the current step:

```json
{
  "inputs": ["context", "question"],
  "knobs": {"branches": 5, "verifiers": 3, "iterations": 2},
  "steps": [
    {"id": "generate", "loop": 0, "nodes": 5, "survivors": 3},
    {"id": "verify",   "loop": 0, "nodes": 3, "survivors": 3}
  ],
  "reliks": [
    {
      "title": "Crucible Memory",
      "folders": [
        {"name": "trace",   "files": [{"name": "verified.md", "size": 412}]},
        {"name": "lessons", "files": [{"name": "failures.md", "size": 188}]},
        {"name": "solution","files": [{"name": "best.md", "size": 250}]}
      ]
    }
  ],
  "time": {
    "now": "2026-07-21T20:47:26Z",
    "localNow": "2026-07-21T16:47:26-04:00",
    "timezone": "America/New_York"
  }
}
```

The planner LLM reads this snapshot as part of its prompt. It can see:

- What inputs are available (typically `context`).
- What knobs the caller set, and to what values.
- What steps have run, how many nodes they had, and how many survived gates.
- What reliks exist, their folders, and the current sizes of their files.
- The current time (UTC always; local time + timezone if the caller declared one).

The `time` key lets the planner reason about elapsed time, deadlines, and pacing — see [Fields](./fields.md) for the full list of auto-injected time inputs.

**Internal key filtering.** The manifest excludes reserved engine-internal
keys from the inputs list: anything starting with `__` (such as
`__passfields`), plus `messages`, `model`, `author`, `template`, `debugMode`,
and `input.handoff*` lineage metadata. Only author-relevant input keys surface
to the planner — `input.context`, `input.systemTime`, `input.localTime`,
`input.timezone`, and any author-declared inputs.

The manifest currently ships **unfiltered**: every step, every relik, every input is included. Optional filtering (`manifest: { steps: last_3 }` or `manifest: { reliks: ["Crucible Memory"] }`) is planned, and will be non-breaking.

The manifest is the runtime telling the planner "here's what you have to work with." The planner's job is to decide what to do with it.

## Runtime validation of loaded config

When the runtime loads a dynamic step's config from `from:`, it validates against four criteria. Failures fire the step's `onInvalid:` action (default: `abort`).

1. **Schema conformance.** The loaded JSON must be a well-formed step config. Required fields: `systemPrompt` (non-empty), `fields` (non-empty array). Optional fields within the loaded config: `nodes`, `if`, `relikWrite`, `codeExecution`, `relikRead`, `relikEdit`, `recursion`. No other fields. No nested `dynamic: true`.

2. **Field reference resolution.** Every `ingest`, `multi_ingest`, `relikRead`, `knobInfo` reference in the loaded config must resolve to something in the current manifest. If the planner emits `{"from": {"stepId": "nonexistent_step"}}`, validation fails.

3. **No nested dynamic.** The loaded config cannot itself be `dynamic: true`. Direct self-modification loops are disallowed.

4. **Node count within cap.** The `nodes` value (if specified) must be between
   1 and 5 for dynamic step configs. This is stricter than the static step
   limit (20) — dynamic steps are capped at 5 nodes to prevent runaway
   fan-out from planner-generated configs. Values outside `[1, 5]` fail
   validation.

If any check fails, the step's `onInvalid:` action fires (default `abort`):

```yaml
- id: run_planned
  dynamic: true
  from: previous
  onInvalid: abort              # default; also: continue | { jump: { stepId: fallback } }
```

- `abort` (default): terminate the ladder.
- `continue`: skip the dynamic step and proceed to the next one. Useful when the dynamic step is an optimization, not a requirement.
- `{ jump: { stepId: ... } }`: jump to a fallback step that handles the failure explicitly.

`onInvalid` gives the ladder author a controlled way to handle planner failures. Without it, a single bad planner output would terminate the whole ladder.

## The fromFallback declaration

When `from:` misses (resolves to an empty string or errors), the engine can
consult a `fromFallback:` source before giving up. This uses the same
`from:` vocabulary:

```yaml
- id: run_planned
  dynamic: true
  from: previous
  fromFallback:
    relik: "Dynamic"
    folder: planned
    file: default_step.json
```

Resolution order: try `from:` first. If empty/error, try `fromFallback:`. If
both miss, fire `onInvalid:`.

This is useful when the planner step might not emit valid JSON on every call
(a common failure mode for LLMs). The fallback provides a known-good config
so the ladder degrades gracefully instead of aborting.

## Execution lifecycle

When the runtime reaches a dynamic step in the cursor, this sequence runs:

1. **Hop +1.** The dynamic step itself costs 1 hop, just like any step.

2. **Resolve `from:`.** The runtime reads the source (typically the previous step's output) and gets a string. If empty and `fromFallback:` is declared, consult it.

3. **Parse the source as JSON.** If parsing fails, fire `onInvalid:`.

4. **Validate the parsed config** against the four criteria above. If any check fails, same path: `onInvalid:`.

5. **Execute the loaded config as if it were the step's own.** Field resolution, LLM calls, gates, writes, adaptive keys, recursion — all work identically to a static step.

6. **Apply the runtime ceilings to any control flow inside the loaded config.** Gates evaluate per node. Jumps reposition the cursor. Recursion spawns children. All of it counts against the ladder's hop, LLM-call, and spend ceilings.

7. **Publish `step_complete` when done.** The step that executed was the dynamic step; the loaded config is not separately identified in the trace. (Foundry shows the loaded config in the step inspector for debugging.)

The total resource consumption of a dynamic step is: 1 hop (the dynamic step itself) + 1 hop per gate evaluation inside the loaded config + 1 hop per recursion level + tokens for every LLM call. The hop ceiling bounds everything at runtime.

## Safety constraints

Dynamic steps are LLM-generated code-as-data. Under LADR's Turing-complete grammar, generated configs can include full control flow: gates, jumps, recursion, adaptive keys. Safety comes from runtime ceilings, not grammar restrictions.

The complete safety stack:

- **Spend cap.** The ladder's `executionBudget.maxSpend` bounds total dollar cost. A dynamic step that loops or recurses still pays for every LLM call. The cap hard-kills execution on breach.
- **Hop ceiling.** Every step (dynamic or not) costs 1 hop. Every gate, every recursion inside the loaded config also costs hops. The ceiling bounds total step transitions.
- **LLM call ceiling.** Every call (in the dynamic step or in any control flow it spawns) counts against the ladder's call cap.
- **No nested dynamic.** A dynamic step's loaded config cannot itself be `dynamic: true`. Direct infinite self-modification is disallowed.
- **Single step per generation.** The loaded config is one step's config, not a sub-ladder. The planner cannot emit a whole new ladder inline; it can only emit one step.
- **Field reference validation at load time.** Every reference in the loaded config must resolve against the current manifest. The planner cannot emit a step that reads from non-existent sources.
- **Node count cap.** `nodes` in the loaded config is bounded by the platform maximum.

Together these constraints mean: a dynamic step can do anything a static step can do, but its resource consumption is bounded by the same ceilings. A malicious or buggy planner cannot escape the budget.

## Patterns

**Adaptive refinement.** After verification, generate a step that targets a specific weakness in the candidate.

```yaml
- id: diagnose
  fields:
    - { name: Available, type: manifest }
    - { name: Candidate, type: ingest, from: { stepId: verify, loopRef: current } }
  systemPrompt: |
    Read the candidate and verifier feedback.
    If there's a specific weakness, emit JSON for a step that addresses it:
    {"systemPrompt": "...", "fields": [...], "nodes": 1}
    If not, emit {"systemPrompt": "Output DONE.", "fields": [], "nodes": 1}.

- id: targeted_fix
  dynamic: true
  from: previous
  onInvalid: continue
```

The ladder adaptively decides what to do next based on what the verifier found.

**Memory-informed next step.** After reading relik lessons from past calls, generate a step that addresses the most recent failure pattern.

```yaml
- id: read_lessons
  fields:
    - { name: Lessons, type: relikRead, from: { relik: "Memory", folder: lessons, file: failures.md } }
    - { name: Context, type: text, from: input.context }
  systemPrompt: |
    Based on Lessons from prior calls, generate a step that avoids past mistakes.
    Emit JSON config.

- id: apply_lesson
  dynamic: true
  from: previous
```

The ladder uses accumulated memory to shape its current strategy.

**Plan, validate, execute.** The planner self-validates its output via `if: { jsonMatches: ... }` so it can react to its own failure.

```yaml
- id: plan
  fields:
    - { name: Available, type: manifest }
  systemPrompt: "Generate the next step config as JSON."
  if:
    jsonMatches:
      type: object
      required: [systemPrompt, fields]
      properties: { ... }
    then: continue
    else: { jump: { stepId: fallback_plan } }     # self-recovery on bad emission

- id: execute_plan
  dynamic: true
  from: previous
```

If the planner emits malformed JSON or missing fields, it jumps back to itself (or to a fallback planner) for another attempt, bounded by the hop ceiling.

**Persistent plans (relik-stored).** When you want the plan to be inspectable or reusable, route it through a relik.

```yaml
- id: plan_to_relik
  fields:
    - { name: Available, type: manifest }
  systemPrompt: "Generate the next step config as JSON."
  relikWrite:
    to: { relik: "Plans", folder: current, file: next.md }
    on: overwrite
    match: { type: object, required: [systemPrompt, fields] }

- id: execute_from_relik
  dynamic: true
  from: { relik: "Plans", folder: current, file: next.md }
```

The plan persists in the relik. A subsequent call can read it via `relikRead` to inspect what was planned, or to reuse it.

## Common pitfalls

**Planner emits overly-generic configs.** A planner that always emits `{"systemPrompt": "Try again.", "fields": [], "nodes": 1}` adds an LLM call without adding reasoning. Constrain the planner with a strict `jsonMatches` schema that requires meaningful fields and a non-trivial system prompt.

**Planner references the wrong step ids.** The planner must emit `from: { stepId: X }` references that exist in the current manifest. If the planner hallucinates a step id, validation fails and `onInvalid:` fires. Give the planner the manifest as a field so it can see what's actually available.

**Dynamic steps in tight loops.** A reflexion loop that includes a dynamic step will re-plan on every iteration. Each plan is an LLM call; the spend adds up fast. Either bound the loop via `executionBudget.maxHops` or hoist the planner outside the loop.

**Forgetting `onInvalid`.** If the planner ever emits invalid JSON, the dynamic step aborts the whole ladder by default. Always declare `onInvalid:` explicitly — `continue` to skip, or `{ jump: ... }` to recover — unless abort is genuinely the right behavior.

**Expecting nested dynamic.** A dynamic step's loaded config cannot itself be dynamic. If you want two-layer meta-reasoning, use a cross-ladder jump instead: the dynamic step's loaded config jumps to another ladder that has its own planner + dynamic step pair.

**Treating dynamic steps as a silver bullet.** Most ladders do not need dynamic steps. The four other power axes (knobs, gates+jumps, reliks, adaptive keys) cover the vast majority of reasoning strategies. Dynamic steps add cost and complexity; reach for them only when the strategy itself needs to adapt to observed state, not just the output.

