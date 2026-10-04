# Top-Level Config

The root of every ladder is a PipelineConfig object. Five fields are required, four are optional. The parser is strict: unknown top-level keys are rejected, missing required fields are rejected, malformed values are rejected. This article documents every field, every value, every default, and every cross-field rule.

## Why the parser is strict

A LADR file is data, not code. It gets published to a directory, indexed by search engines, embedded as JSON-LD on crawlable pages, version-pinned for reproducibility, and loaded by callers who may not trust the author. All of this depends on the schema being closed.

Strict parsing means:

- Unknown top-level keys are rejected. Adding a new field is a grammar change, not a silent extension.
- Required fields are enforced. A ladder with no `exit` does not parse.
- Type mismatches surface at parse time. A ladder with `steps: "hello"` does not parse.

This is the opposite of a YAML config file that silently ignores typos. The discipline is what lets ladders be analyzed, validated, priced, and trusted as data.

When the parser rejects a config, it returns a single error message identifying the offending key. When validation rejects a config (after parse succeeds), it returns every error at once in an array, so the author can fix the whole batch before retrying.

## The skeleton

```yaml
# Required
name: <ladder name>
allowedTargets:
  strategy: universal              # or { strategy: constrained, providers: [...], models: [...] }
exit: <step id>
knobs: { ... }                     # may be empty
steps: [ ... ]                     # at least one

# Optional
id: <platform-assigned uuid>
description: <human-readable summary>
reliks: [ ... ]                    # persistent memory stores
executionBudget:
  maxSpend: 5.0
  maxHops: 200
  maxLlmCalls: 500
  maxRecursion: 5
trace:
  mode: reasoning                  # reasoning | inline | off
  allSteps: false
```

The ten accepted top-level keys. Anything else fails parse.

## Required fields

Five top-level fields must be present in every ladder.

### `name`

The ladder's display name. Shown in Foundry, the public directory page, and API responses. Free-form string.

```yaml
name: Gated Tree of Thought
```

### `allowedTargets`

Declares which LLM providers and models the ladder is allowed to run against. Two strategies:

```yaml
# Universal: any provider the platform supports
allowedTargets: { strategy: universal }

# Constrained: pinned to an allowlist
allowedTargets:
  strategy: constrained
  providers: [openai, anthropic]
  models: [gpt-4o, claude-sonnet-4-20250514]
```

Validation rules for `constrained`:

- `providers` must include at least one entry. `*` is accepted as a wildcard but cannot combine with explicit providers.
- `models` must include at least one entry. Same wildcard rule.
- Every entry must be non-empty.

`universal` is the right choice for published ladders that should run anywhere. `constrained` is the right choice for ladders that depend on a specific model's behavior (e.g. a ladder that requires a model with strong JSON-mode support).

See [Provider Strategy](../reference/provider-strategy.md) for the full deep dive on routing, fallback, and common patterns.

### `exit`

The id of the step whose output is returned to the caller. Must reference a step that exists in `steps:`. If the exit step has multiple `nodes:`, it must be `type: sequential` (a `normal` step with parallel nodes has no deterministic "last" node).

```yaml
exit: answer
```

### `knobs`

A map of caller-tunable parameters. May be empty (`knobs: {}`); the field itself is still required. See Language > Knobs for the full reference.

### `steps`

An ordered list of step declarations. Must contain at least one step. Each step must have `id` and `name`; everything else is optional. See Language > Steps for the full reference.

## Optional fields

Four top-level fields are optional.

### `id`

A platform-assigned UUID. Authors do not set this; the platform generates it when the ladder is first published. Present in API responses and used internally for referencing the ladder across versions.

```yaml
id: 4f6e8d2a-3b1c-4e5f-8a9b-7c3d2e1f0a9b
```

### `description`

A human-readable summary shown on the ladder's directory page and in Foundry. Free-form string. Not parsed or validated beyond being a string.

```yaml
description: Tree of Thoughts with verifier gate and synthesis. Best for hard multi-step reasoning tasks.
```

### `reliks`

A list of persistent memory stores. Each relik is composed of folders of plain-text files plus typed metatags. Survives across invocations. See Language > Reliks for the full reference.

### `executionBudget`

Per-ladder resource ceilings. All four fields are optional and use platform defaults when omitted. Authors can lower any ceiling (always safe) or raise it up to the platform max. See Runtime > Execution Budget for the full reference.

```yaml
executionBudget:
  maxSpend: 10.0          # dollars; default 5.0, platform max 50.0
  maxHops: 500            # default 200, platform max 10000
  maxLlmCalls: 1000       # default 500, platform max 5000
  maxRecursion: 10        # default 5, platform max 20
```

### `trace`

Controls what execution detail is emitted in the OpenAI-compatible response. Two fields:

```yaml
trace:
  mode: reasoning          # reasoning | inline | off (default: reasoning)
  allSteps: false          # true emits every step; false only leaf steps
```

See Runtime > Observability for how trace modes affect the response shape.

## Field reference

Every top-level field, its type, whether it's required, and its default.

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `name` | string | yes | — | Display name. |
| `allowedTargets` | object | yes | — | Provider/model strategy. |
| `exit` | string | yes | — | Step id whose output is returned. |
| `knobs` | map | yes | `{}` | Caller-tunable parameters. |
| `steps` | array | yes | — | Ordered step list. At least one. |
| `id` | string (uuid) | no | platform-assigned | Internal identifier. |
| `description` | string | no | empty | Human-readable summary. |
| `reliks` | array | no | `[]` | Persistent memory stores. |
| `executionBudget` | object | no | platform defaults | Per-ladder ceilings. |
| `trace` | object | no | `{ mode: reasoning, allSteps: false }` | Response trace settings. |

### `allowedTargets` shape

```yaml
allowedTargets: { strategy: universal }

allowedTargets:
  strategy: constrained
  providers: [...]    # required when constrained; "*" allowed alone
  models: [...]       # required when constrained; "*" allowed alone
```

### `executionBudget` shape

All four fields are optional within the object. Omitting the whole object uses platform defaults for all four.

```yaml
executionBudget:
  maxSpend: <number, dollars>       # default 5.0; platform max 50.0
  maxHops: <integer>                # default 200; platform max 10000
  maxLlmCalls: <integer>            # default 500; platform max 5000
  maxRecursion: <integer>           # default 5; platform max 20
```

A ladder can lower any ceiling (always safe) or raise it up to the platform max. Declaring a value above the platform max fails validation.

### `trace` shape

```yaml
trace:
  mode: reasoning      # "reasoning" | "inline" | "off"
  allSteps: false      # boolean; default false
```

`mode` controls how the runtime exposes intermediate step outputs in the OpenAI-compatible response. `allSteps` controls whether every step emits a trace block, or only "leaf" steps (steps that produce user-visible output).

## Cross-field rules

Some validation rules span multiple top-level fields.

**`exit` references `steps`.** The exit step id must exist in the `steps` array. If the exit step has multiple nodes, it must be `type: sequential`.

**`reliks` referenced by step fields must be declared.** A step with a `relikRead` field pointing at relik `"Project Memory"` fails validation if no relik titled `"Project Memory"` is declared at the top level.

**`reliks` referenced by `relikWrite` must be declared.** Same rule for writes. The folder and (if specified) file must also exist.

**`knobs` referenced by `nodes:`, `recursion.maxDepth`, or `knobInfo.from` must be declared.** Wrong-type references fail: `nodes: iterations` against a knob of `type: recursion` is invalid.

**`steps` cannot reference forward steps with `loopRef: current`.** A step can only ingest from a step that has already executed in the current loop. The forward-reference check enforces declaration order for `current` references.

**`steps` cannot have two steps with `recursion:` blocks.** Only one recursion step per ladder.

**`steps` cannot have nested `type: group` steps.** A group's children must be `normal` or `sequential`.

**At most one step can have `timeline: init`.** And that step cannot have `nodes:` and cannot be the exit step.

**`executionBudget` values cannot exceed platform maximums.** Setting `maxSpend: 100` against a platform max of 50 fails validation.

## A complete example

A ladder using every top-level field:

```yaml
name: Adaptive Critic
description: Tree of Thoughts with reflexion loop, persistent lessons, and dynamic refinement.
id: 7c3d2e1f-0a9b-4e5f-8a2c-3b1c4d5e6f7a   # normally platform-assigned
allowedTargets:
  strategy: constrained
  providers: [openai, anthropic]
  models: [gpt-4o, gpt-4o-mini, claude-sonnet-4-20250514]
exit: answer
knobs:
  branches:
    name: Branches
    type: nodes
    input: slider
    steps:
      - { title: Few, value: 3 }
      - { title: Many, value: 7, default: true }
  iterations:
    name: Iterations
    type: recursion
    input: numerical
    default: 2
    min: 1
    max: 4
reliks:
  - title: Critic Memory
    folders:
      - name: lessons
        tokenLimit: 400
        access: readWrite
        evictionPolicy: FIFO
        files:
          - { name: failures.md, init: blank }
      - name: solution
        tokenLimit: 1000
        access: readWrite
        files:
          - { name: best.md, init: blank }
executionBudget:
  maxSpend: 8.0
  maxHops: 500
  maxLlmCalls: 800
trace:
  mode: reasoning
  allSteps: true
steps:
  - id: generate
    # ...
  - id: verify
    # ...
  - id: answer
    # ...
```

This ladder:

- Pins to GPT-4o family and Claude Sonnet (no other models allowed).
- Exposes two knobs (caller picks branch count and recursion depth).
- Persists lessons and best solution across calls via two relik folders.
- Raises the spend ceiling to $8 (default is $5).
- Emits trace data for every step (not just leaves).

## Strict parser behavior

The strict parser enforces three rules at parse time, before any validation runs.

**Rejects unknown top-level keys.** Typos like `descripton:` or hypothetical fields like `version:` fail immediately:

```
parseConfig: unknown top-level key "descripton" (PipelineConfig is strict)
```

**Rejects missing required fields.** All five required fields (`name`, `exit`, `allowedTargets`, `knobs`, `steps`) must be present:

```
parseConfig: missing required top-level field "exit"
```

**Rejects malformed values.** A non-object `steps` value, a non-string `name`, or a structurally broken `allowedTargets` all fail at parse time.

Parse errors return immediately with a single message. They do not aggregate (the parser cannot continue past a malformed value). Validation errors, by contrast, aggregate; see Runtime > Validation for the full validation pipeline.

## Common questions

**Can I add custom metadata fields?** No. The strict parser rejects unknown keys. If you need to attach metadata, use `description` (free-form string) or embed it in the relik system (write to a relik on first call, read on subsequent calls).

**What happens if I omit `knobs`?** Parse fails. The field is required even when empty. Use `knobs: {}` for ladders with no caller-tunable parameters.

**Can I version a ladder via a top-level field?** Versioning is handled at the addressing layer (`@author/name@v3`), not in the config itself. The same YAML can be published under multiple versions.

**Can a ladder have zero steps?** No. `steps` must contain at least one entry.

**Are comments allowed in the YAML?** Yes. Standard YAML `# comment` syntax works. Comments are stripped during JSON conversion before parsing.

**Can I use JSON instead of YAML?** Yes. The parser accepts either format. YAML is the canonical authoring format; JSON is what gets stored and transmitted. Authoring tools that emit JSON work identically.

**What's the difference between `id` and `name`?** `id` is a platform-assigned UUID used internally for cross-version references. `name` is a human-readable display string set by the author. Two versions of the same ladder share `id` but may differ in `name`.

