# Validation

Every ladder config goes through three validation phases before a single LLM call runs: strict parse, static validation, and per-rule aggregation. The point is to surface every mistake the author can make before execution, when mistakes are cheap to fix. This article documents the three-phase pipeline and every category of rule the validator checks.

## Why validation runs before execution

A LADR config can reference steps that don't exist, declare knobs with impossible ranges, point at reliks that aren't declared, embed malformed JSON Schemas, and nest groups inside groups. None of these are detectable by reading the YAML alone; all of them will cause failures mid-execution when they are much more expensive to debug.

Validation catches them upfront. A ladder that passes validation is guaranteed to be structurally sound: every cross-reference resolves, every range is satisfiable, every schema parses, every jump target exists.

What validation does **not** guarantee:

- That the ladder will produce good outputs. That's an authoring problem.
- That the ladder will terminate. LADR is Turing-complete; termination is enforced at runtime via the spend cap and hop ceiling, not statically.
- That the LLM will emit JSON matching your `relikWrite.match` schema. That's probabilistic; the runtime treats no-match as a no-op (see Reads and Writes).

What validation **does** guarantee:

- The config parses as a well-formed PipelineConfig.
- Every step id is unique.
- Every field reference points at a step that exists and has executed.
- Every relik reference points at a declared relik, folder, and (if specified) file.
- Every gate is well-formed.
- Every JSON Schema in the config parses as valid JSON.
- The execution budget does not exceed platform maximums.
- No mutually-exclusive fields are declared together.

## The three-phase pipeline

Every config goes through three phases in order.

### Phase 1: Parse

Raw YAML or JSON is deserialized into a typed PipelineConfig. The parser is **strict**: unknown top-level keys are rejected, missing required fields are rejected, malformed values are rejected. Parse errors return immediately with a single message; the parser cannot continue past a malformed value.

Common parse failures:

- Unknown top-level key (e.g. `descripton:` instead of `description:`).
- Missing required field (`name`, `exit`, `allowedTargets`, `knobs`, or `steps`).
- Type mismatch (`steps: "hello"` instead of `steps: [...]`).
- Invalid enum value (`allowedTargets.strategy: "universsal"`).
- Malformed JSON inside a JSON Schema field.

### Phase 2: Static validation

The parsed config is passed through 25 rule checks grouped into 8 categories. Every error is collected; nothing runs until the array is empty.

### Phase 3: Execution

Only configs with zero errors proceed to execution. The runtime assumes the config is sound; it does not re-check structural invariants mid-execution.

The rest of this article documents Phase 2 in detail.

## Rule categories

The 25 static-validation rules group into eight categories. Each category covers one aspect of the config.

| Category | What it checks |
|---|---|
| **Structure** | Step uniqueness, group nesting, exit step well-formedness, timeline uniqueness. |
| **References** | Every ingest, multi_ingest, knobInfo, relikRead, relikWrite, nodes, recursion.maxDepth, clone, and pruned-nodes reference points at something that exists. |
| **Forward references** | No step can ingest from a step later in the array with `loopRef: current`. Group children cannot reference each other. |
| **Ranges and counts** | Knob sliders have 3-5 steps; numericals have `min <= default <= max`; recursion depth within platform max; node count within platform max; loop count within platform max. |
| **Gates** | `if:` and `continueIf:` are not both declared; `integerRange` / `numberRange` have `min <= max`; `in` is non-empty; `jsonMatches` is valid JSON. |
| **Recursion** | At most one step per ladder has `recursion:`; `maxDepth` references a knob of `type: recursion` if it's a string. |
| **Reliks** | Relik titles unique; folder names unique within relik; file names unique within folder; metatag types valid; metatag names unique per scope; token limits positive; `type: object` metatags have inline schemas. |
| **Control flow + memory** | Jump targets exist (within-ladder) or parse (cross-ladder); dynamic steps declare `from:` and don't declare conflicting fields; adaptive key blocks have valid `requestMatch`; relikWrite targets declared reliks; execution budget within platform max. |

## Structural rules

Structural rules enforce the basic shape of the config.

- **Step uniqueness.** Every step `id` is unique across the whole ladder, including across group children. Duplicate ids fail.
- **No nested groups.** A `type: group` step's children must be `normal` or `sequential`. A group inside a group fails.
- **Exit step well-formedness.** The exit step id must exist. If it has multiple nodes, it must be `type: sequential`.
- **`timeline: init` uniqueness.** At most one step can have `timeline: init`. That step cannot have `nodes:` and cannot be the exit step.
- **`type: group` minimum children.** A group must have at least 2 child steps.

These are the simplest rules but catch the most typos. Every error includes the step's `id` so the author can find it.

## Reference rules

Reference rules ensure every cross-reference in the config points at something that exists.

- **`ingest` references.** Every `from.stepId` in an `ingest` field must exist as a step in the config.
- **`multi_ingest` references.** Every source object's `stepId` must exist.
- **`knobInfo` references.** Every `from` knob name must exist in the `knobs` map.
- **`relikRead` references.** Relik, folder, and (if specified) file must exist. If a metatag is specified, it must be in the file's effective schema (or be a system metatag).
- **`relikWrite` references.** Relik and folder must exist. If a metatag is specified as the target, the folder must permit writes (`access: readWrite`).
- **`nodes:` references.** If `nodes:` is a knob reference (`{{knobs.X}}` or bare knob name), the knob must exist.
- **`recursion.maxDepth` references.** If `maxDepth` is a string, it must reference an existing knob of `type: recursion`.
- **`clone:` references.** A step with `fields: clone:otherStep` requires `otherStep` to exist and not be a group.
- **`nodes.from.pruned` references.** A step with `nodes: { from: { stepId, pruned: true } }` requires the source step to have a gate declared (`continueIf` or `if:`).

## Forward reference and self-reference rules

These rules enforce execution order.

**No forward references with `loopRef: current`.** A step cannot ingest from a step later in the array using `loopRef: current`. The referenced step has not executed yet at the time the current step runs. Use `loopRef: previous` or a specific loop index instead.

```yaml
steps:
  - id: step_a
    fields:
      # INVALID: step_b hasn't run yet when step_a runs
      - { name: Future, type: ingest, from: { stepId: step_b, loopRef: current } }

  - id: step_b
    # ...
```

**No self-references with `loopRef: current` on non-sequential steps.** A `normal` step cannot read its own current-loop output because the output doesn't exist until the step completes. Only `sequential` steps can self-reference (a sequential step's node K can read its own nodes 1..K-1 via `nodeRef: previous`).

```yaml
steps:
  - id: chain
    type: sequential
    nodes: 3
    fields:
      # Valid on sequential: node K reads node K-1
      - name: Previous
        type: ingest
        from: { stepId: chain, loopRef: current, nodeRef: previous }
        skipFirstNode: true
```

**No cross-references between group children with `loopRef: current`.** Group children run as parallel siblings; they cannot read from each other because none has completed when the others run. Use explicit `stepId` references only after the group completes (in the step that follows the group).

**`skipFirstNode: true` only on sequential steps.** The flag tells the runtime to return an empty string for node 1 (which has no previous). It's only meaningful on `sequential` steps where node K reads node K-1. Declaring it on a `normal` step fails validation.

**`loopRef: accumulate` only on `multi_ingest`.** Single-value `ingest` cannot accumulate. Use `multi_ingest` instead.

**`nodeRef: accumulate` only on `multi_ingest`.** Same rule.

## Range and count rules

Range rules enforce sane bounds.

**Slider steps.** Every slider has 3-5 steps. Fewer or more fails.

**Slider defaults.** Every slider has exactly one step with `default: true`. Zero defaults or multiple defaults both fail.

**Numerical defaults.** Every numerical has `min <= default <= max`. `min > default` fails. `default > max` fails.

**Numerical required fields.** `min` and `max` are both required on numerical inputs. The parser rejects missing values.

**Loop knob limit.** A `type: loops` knob with default or max above 50 fails. (Platform max for loops.)

**Nodes knob limit.** A `type: nodes` knob with default or max above 20 fails. (Platform max for nodes per step.)

**Recursion knob limit.** A `type: recursion` knob with default or max above 5 fails. (Default platform max for recursion depth. Authors can raise per-ladder via `executionBudget.maxRecursion` up to 20.)

**Recursion step count.** At most one step per ladder can have a `recursion:` block. Multiple recursion steps fail.

**Numerical min/max well-formed.** `if.integerRange` and `if.numberRange` must have `min <= max`. `if.in` must have at least one value.

These rules surface as:

```text
Knob "branches": slider must have 3-5 steps (found 2)
Knob "iterations": default (3) > max (2)
Step "draft": if.integerRange has min (5) > max (3)
```

## Gate rules

Rules for the typed `if:` block.

- **`if:` and `continueIf:` are mutually exclusive.** Declaring both fails. Use only `if:` for new ladders.
- **`integerRange` has `min <= max`.** An inverted range fails.
- **`numberRange` has `min <= max`.** Same.
- **`in` has at least one value.** Empty `in: []` fails.
- **`jsonMatches` is valid JSON.** Malformed JSON fails.
- **`jsonMatches` is non-empty.** An empty schema fails.

Gate-rule errors look like:

```text
Step "verify": cannot declare both "continueIf" (deprecated) and "if" — use only "if"
Step "verify": if.integerRange has min (5) > max (3)
Step "verify": if.jsonMatches is not valid JSON: invalid character '}' looking for beginning of object key string
```

## Grammar rules in detail

Rules for the core grammar features: reliks, jumps, dynamic steps, adaptive keys, writes, budget.

**Relik declarations.**

- Relik titles are non-empty and unique within the config.
- Every relik has at least 1 folder.
- Folder names are non-empty and unique within the relik.
- `tokenLimit` is positive.
- Metatag types are one of `string`, `string[]`, `number`, `boolean`, `object`.
- Metatag names are non-empty and unique within their scope (folder or file).
- `type: object` metatags declare an inline `schema`.
- File names are non-empty and unique within the folder.

**Relik references.** Every `relikRead` field references a declared relik, folder, and (if specified) file. Metatag references must be in the file's effective schema (folder-level ∪ file-level) or be a system metatag (`index`, `tree`, `created`, `modified`, `size`).

**Jumps and actions.** Every `jump.stepId` in `if.then`, `if.else`, or `onInvalid` references an existing step. Cross-ladder `jump.ladderId` values must parse as valid ladder identifiers (`@author/name[@v3]` or `local/name`).

**`if` conditions.** `jsonMatches` is non-empty and parses as valid JSON.

**Dynamic steps.** A step with `dynamic: true` declares `from:`. It does not declare its own `systemPrompt`, `fields`, or `nodes` (those come from the loaded config).

**Retrieve blocks.** `maxRounds` is positive. `as` is non-empty. `requestMatch` is a non-empty valid JSON Schema. The target relik and folder exist.

**Relik writes.** The target relik and folder exist. `mergeByKey` requires `key`. `key` is only valid with `on: mergeByKey`. `match` is non-empty valid JSON.

**Execution budget.** All four fields, when present, are positive and at or below platform max: `maxSpend` ≤ 50, `maxHops` ≤ 10000, `maxLlmCalls` ≤ 5000, `maxRecursion` ≤ 20.

**`continueIf` conflict.** No step has both `continueIf:` and `if:` declared.

## Error format and aggregation

Parse errors return immediately with one message:

```text
parseConfig: missing required top-level field "exit"
```

Validation errors aggregate. The validator runs every rule, collects every error, and returns them all in one response:

```json
{
  "valid": false,
  "errors": [
    "Step "verify": field "Candidate" references non-existent step "draft"",
    "Step "verify": if.integerRange has min (5) > max (3)",
    "Knob "branches": slider must have 3-5 steps (found 2)"
  ]
}
```

`valid: true` means the config can be saved and executed. `valid: false` means at least one error was found; the `errors` array lists every problem (the validator does not stop at the first error).

Every error includes the offending element's identifier so the author can locate the problem in the config: the step's `id`, the knob's YAML key, the relik's title, the folder's name.

The error messages are stable across versions. They are part of the API contract; tooling that parses errors (linters, IDE extensions, Foundry's validator panel) can rely on the format.

## When validation runs

Validation runs at four moments in a ladder's lifecycle.

**At authoring time.** Foundry run validation on every save. The author sees errors immediately in the editor.

**At publish time.** The platform validates the config before listing it in the directory. Invalid configs cannot be published.

**At load time.** When the runtime loads a config to execute it, validation runs again. This catches config drift if the platform's validation rules have tightened since the ladder was published.

**At dynamic step load time.** A dynamic step's loaded config is validated at runtime against the current manifest. This is the only validation that runs mid-execution; see Language > Dynamic Steps.

Authors never invoke validation directly. It's always implicit. The error format is identical across all four moments, so an error caught at authoring time has the same message as one caught at execution time.

