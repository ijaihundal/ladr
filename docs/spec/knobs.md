# Knobs

A knob is a caller-tunable parameter declared in the ladder config and resolved at call time. Knobs let one ladder serve multiple use cases (fast draft, high-stakes final, verbose explanation) without code changes. The author declares the knob, its allowed range, and a default; the caller supplies a value (or accepts the default) when invoking the ladder.

## Why knobs exist

Without knobs, every ladder is one strategy frozen at author time. You want a fast draft, you write a fast ladder. You want a high-stakes final, you write a separate ladder. Two configs to maintain, two endpoints to publish, two prices to set.

Knobs collapse that. One ladder, declared once, exposes knobs the caller picks at request time:

```json
{
  "model": "gpt-4o",
  "messages": [{"role": "user", "content": "Explain RAG."}],
  "knobs": { "branches": 9, "depth": 4 }
}
```

Same ladder, different behavior per call. The caller picks the trade-off (more branches = more cost, better coverage). The author declares the safe range once.

Knobs also surface in the ladder's public UI. Foundry render slider knobs as named presets ("Compact / Balanced / Wide"); numerical knobs as min/max inputs. A published ladder's directory page shows its knobs as part of the API contract. Callers know what they can tune without reading the YAML.

## Knob anatomy

A knob declaration has three parts: a YAML key (the machine identifier), a small metadata block (`name` and `type`), and an input mode (`slider` or `numerical`).

```yaml
knobs:
  branches:                       # YAML key (machine identifier)
    name: Branches                # display label shown in Foundry
    type: nodes                   # semantic type
    input: slider                 # input mode
    steps:                        # required when input: slider
      - { title: Compact, value: 3, default: true }
      - { title: Balanced, value: 5 }
      - { title: Wide,     value: 7 }
```

The YAML key is the identifier used in references (`nodes: branches`, `knobInfo.from: branches`) and in API requests (`{ "knobs": { "branches": 5 } }`). The `name` is shown to humans in UIs.

The same pattern in numerical form:

```yaml
knobs:
  iterations:
    name: Iterations
    type: recursion
    input: numerical
    default: 2
    min: 1
    max: 5
```

A ladder with no knobs just declares `knobs: {}`. The field is required; an empty map is valid.

## Knob types

Every knob has a semantic type that tells the platform what the knob controls. Four types are supported.

| Type | What it controls | Multiplicity |
|---|---|---|
| `loops` | How many times the entire steps sequence repeats per execution. | At most one per ladder. |
| `recursion` | Maximum recursion depth for any step that has `recursion:` enabled. | At most one per ladder. |
| `nodes` | Fan-out: how many parallel LLM calls a step makes. | Multiple per ladder (one for branch count, another for evaluator panel, etc.). |
| `generic` | Any other caller-tunable value (tone, audience, strictness, style). Consumed only via `knobInfo`. | Multiple per ladder. |

`loops` and `recursion` are global quantities: there is one loop count for the whole ladder, one recursion depth cap. `nodes` and `generic` are per-step quantities: each step that needs fan-out picks its own knob.

The multiplicity rules are enforced at validation. More than one `loops` knob or more than one `recursion` knob is a validation error. `nodes` and `generic` knobs are unrestricted.

```yaml
knobs:
  branches:                       # type: nodes (multiple allowed)
    name: Branches
    type: nodes
    input: slider
    steps: [...]
  verifiers:                      # another type: nodes
    name: Verifiers
    type: nodes
    input: slider
    steps: [...]
  iterations:                     # type: recursion (only one allowed)
    name: Iterations
    type: recursion
    input: numerical
    default: 2
    min: 1
    max: 5
  tone:                           # type: generic (multiple allowed)
    name: Tone
    type: generic
    input: slider
    steps:
      - { title: Concise, value: 1 }
      - { title: Medium,  value: 2, default: true }
      - { title: Verbose, value: 3 }
```

## Input modes

Every knob has an input mode that controls how Foundry presents it to the caller. The two modes are independent of the semantic type: any knob type can use either input mode.

| Mode | UI element | Required fields |
|---|---|---|
| `slider` | 3 to 5 named positions, each with a numeric value. | `steps: [{ title, value, default? }, ...]` |
| `numerical` | Number input with a continuous range. | `min`, `max`, optional `default` |

`slider` is the right choice when the caller benefits from named presets. `numerical` is the right choice when the caller needs fine-grained control over a continuous quantity.

**Slider validation rules:**

- Exactly 3 to 5 steps. Fewer or more fails validation.
- Exactly one step has `default: true`. Zero defaults or multiple defaults both fail validation.
- Step titles should be unique (not enforced, but Foundry shows them as labels).

**Numerical validation rules:**

- `min` and `max` are both required. The parser rejects numerical inputs missing either.
- `default` is optional. If omitted, defaults to `min`.
- `min <= default <= max` is enforced.
- Non-integer values are supported. Useful for confidence thresholds (`min: 0.0`, `max: 1.0`, `default: 0.7`).

```yaml
# Slider: caller picks from named presets
knobs:
  branches:
    name: Branches
    type: nodes
    input: slider
    steps:
      - { title: Few,    value: 3, default: true }
      - { title: Medium, value: 5 }
      - { title: Many,   value: 9 }

# Numerical: caller picks any value in range
knobs:
  confidence:
    name: Confidence Threshold
    type: generic
    input: numerical
    default: 0.85
    min: 0.0
    max: 1.0
```

## YAML key versus display name

Each knob has two identifiers that serve different purposes.

**YAML key.** The key under `knobs:` is the machine identifier. It is used in code references: `nodes: branches` in step configs, `knobInfo.from: branches` in fields, and `{ "knobs": { "branches": 5 } }` in API requests.

**`name` field.** The display label shown in Foundry UIs. It appears on sliders, numerical inputs, and knob summaries.

The two can differ:

```yaml
knobs:
  coverage:                       # YAML key
    name: Exploration Breadth     # display label
    type: nodes
    input: slider
    steps:
      - { title: Compact, value: 3, default: true }
      - { title: Balanced, value: 6 }
```

Here `coverage` is the key (used in code references) and `Exploration Breadth` is the label shown in Foundry. In an API request:

```json
{
  "model": "gpt-4o",
  "messages": [{"role": "user", "content": "..."}],
  "knobs": { "coverage": 6 }
}
```

The caller sees `Exploration Breadth` as the slider label and picks `Balanced`. The runtime receives `coverage: 6` internally.

By convention, YAML keys are lowercase singular nouns (`branches`, `iterations`, `tone`); display names are title-case noun phrases (`Branches`, `Max Iterations`, `Output Tone`).

## Referencing knobs

Knob values are referenced in three places in a ladder.

**In a step's `nodes:` count.** The knob value determines how many parallel LLM calls the step makes:

```yaml
nodes: branches                   # bare knob name (preferred)
nodes: { from: branches }         # explicit object form (equivalent)
nodes: "{{knobs.branches}}"       # legacy interpolation form (still accepted)
```

The runtime resolves the knob value at call time and uses it as the node count. The step's per-node cost scales linearly with the resolved value.

The knob must be of `type: nodes`. Referencing a knob of the wrong type fails validation.

**In a step's `recursion.maxDepth`:**

```yaml
recursion:
  maxDepth: iterations            # bare knob name
recursion:
  maxDepth: "{{knobs.iterations}}"  # legacy form
```

The knob must be of `type: recursion`. The resolved value becomes the maximum recursion depth for this step.

**In a field via `knobInfo`.** To inject the knob's resolved value into the prompt:

```yaml
fields:
  - name: Branch Count
    type: knobInfo
    from: branches                # bare knob name (NOT "knobs.branches")
```

The `from:` value is just the knob's YAML key. The runtime resolves it to the current value and renders it into the prompt as `Branch Count: 6`.

Knobs of `type: generic` are typically consumed only via `knobInfo`. They have no automatic effect on ladder execution; they are values the prompt can use to shape behavior.

## Runtime resolution

When a ladder is invoked, the runtime resolves every knob's value before any step runs. The resolution order:

1. **Start with the declared default.** For sliders, the value of whichever step has `default: true`. For numericals, the explicit `default` field (or `min` if no default is declared).

2. **Override with any caller-supplied value** from the API request's `knobs` map. If the caller sent `{ "branches": 7 }`, that overrides the default.

3. **Clamp to the declared range.** For numericals, the value is clamped to `[min, max]`. For sliders, the value must match one of the declared step values exactly; otherwise it is silently dropped and the default is used.

4. **Make the resolved value available** to every `nodes:`, `recursion.maxDepth`, and `knobInfo` reference in the config.

Caller-supplied values that fall outside the declared range are clamped silently. A request for `{ "branches": 100 }` against a slider with max 7 resolves to 7. A request for `{ "iterations": 99 }` against a numerical with max 5 resolves to 5.

Unrecognized knob keys in the API request are silently dropped. If the caller sends `{ "branches": 5, "unknown_knob": 3 }`, only `branches` takes effect. This lets you add new knobs without breaking older clients.

The resolved knob values are reflected in the runtime manifest (see Language > Dynamic Steps), so dynamic-step planners can reason about the caller's choices.

## knobInfo and nodeInfo fields

Two field types inject runtime values into a step's prompt: `knobInfo` and `nodeInfo`. They are declared alongside other fields on a step, but work differently because they do not read from user input or other steps' outputs.

**`knobInfo`** injects a knob's resolved value. The `from` field is the knob's YAML key:

```yaml
fields:
  - name: Branch Count
    type: knobInfo
    from: branches
```

If `branches` is set to 6, this renders as `Branch Count: 6` in the prompt.

**`nodeInfo`** injects the current node's 1-indexed number. No `from` is needed; the runtime automatically resolves it based on which parallel branch is executing:

```yaml
fields:
  - name: Node Number
    type: nodeInfo
```

If the step has 5 nodes running in parallel, node 3 receives `Node Number: 3` as a field line in its prompt. Useful for prompting each node to vary its approach:

```yaml
systemPrompt: "Draft an independent branch. Use the Node Number field above to vary your angle."
```

The model reads the rendered field line and the systemPrompt together. LADR does not interpolate `{FieldName}` tokens in systemPrompt text; the field line is the mechanism for getting the value into the prompt.

Neither `knobInfo` nor `nodeInfo` is required for the knob or node to function. They are prompt-injection helpers. `nodes: branches` works without any `knobInfo` field; the knob value is used directly to determine fan-out. The `knobInfo` field is only needed when the prompt itself should reference the value.

## Validation summary

Knob validation aggregates every error before returning. The full rule set:

| Rule | Error message |
|---|---|
| Invalid `type` value | `Knob "X": invalid type "Y"` |
| More than one `type: loops` knob | `At most one knob with type "loops" allowed (found N)` |
| More than one `type: recursion` knob | `At most one knob with type "recursion" allowed (found N)` |
| Slider with fewer than 3 or more than 5 steps | `Knob "X": slider must have 3-5 steps (found N)` |
| Slider with zero defaults | `Knob "X": slider must have exactly one step with default: true (found none)` |
| Slider with multiple defaults | `Knob "X": slider must have exactly one step with default: true (found N)` |
| Numerical with `min > default` | `Knob "X": min (A) > default (B)` |
| Numerical with `default > max` | `Knob "X": default (A) > max (B)` |
| Numerical with `min > max` | rejected at parse time |
| Numerical missing `min` or `max` | rejected at parse time |
| Loop knob with value above platform max (50) | `Knob "X": loop count N exceeds maximum (50)` |
| Nodes knob with value above platform max (20) | `Knob "X": node count N exceeds maximum (20)` |
| Recursion knob with value above platform max (5) | `Knob "X": recursion depth N exceeds maximum (5)` |

Cross-reference validation runs separately and catches:

- `knobInfo.from` references a non-existent knob.
- `nodes:` references a non-existent knob.
- `nodes:` references a knob not of `type: nodes`.
- `recursion.maxDepth` references a knob not of `type: recursion`.

Every error includes the knob's YAML key so the author can locate the problem in the config.

## Common patterns

**Bounded fan-out.** A single slider knob controls branch count, defaulting to a moderate value with options for fast or wide execution:

```yaml
knobs:
  branches:
    name: Branches
    type: nodes
    input: slider
    steps:
      - { title: Few,    value: 3 }
      - { title: Medium, value: 5, default: true }
      - { title: Many,   value: 9 }
```

The caller picks the trade-off: 3 for fast drafts, 9 for high-stakes calls.

**Bounded recursion.** A numerical knob caps recursion depth, defaulting to 1 (no recursion):

```yaml
knobs:
  depth:
    name: Recursion Depth
    type: recursion
    input: numerical
    default: 1
    min: 1
    max: 4
```

**Caller-selected strategy.** A generic knob that switches the prompt's behavior. The ladder reads it via `knobInfo`:

```yaml
knobs:
  style:
    name: Output Style
    type: generic
    input: slider
    steps:
      - { title: Technical,    value: 1, default: true }
      - { title: Approachable, value: 2 }
      - { title: Executive,    value: 3 }

steps:
  - id: draft
    fields:
      - { name: Context, type: text, from: input.context }
      - { name: Style, type: knobInfo, from: style }
    systemPrompt: |
      Draft the answer in the style indicated by {Style}.
      1 = technical (precise, jargon ok).
      2 = approachable (clear, accessible).
      3 = executive (concise, decision-focused).
```

The caller selects a style without changing the prompt; the prompt adapts at runtime.

**Caller-tunable strictness.** A numerical knob controls a verifier gate's threshold:

```yaml
knobs:
  threshold:
    name: Verifier Threshold
    type: generic
    input: numerical
    default: 4
    min: 1
    max: 5

steps:
  - id: verify
    fields:
      - { name: Candidate, type: ingest, from: { stepId: draft, loopRef: current } }
      - { name: Threshold, type: knobInfo, from: threshold }
    systemPrompt: 'Score 1-5. Output the integer alone.'
    # Note: the gate's integerRange is statically declared, not knob-driven.
    # For knob-driven thresholds, use a verifier pattern that emits JSON
    # and gate via jsonMatches with properties: { score: { minimum: N } }.
```

These patterns compose. A reference ladder like Crucible exposes four knobs at once (branches, verifiers, iterations, threshold) for the caller to dial in cost versus quality.

