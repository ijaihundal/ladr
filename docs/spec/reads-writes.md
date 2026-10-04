# Reads and Writes

Reliks become useful when steps can read from and write to them. LADR has one declarative read primitive (relikRead, a field type) and one declarative write mechanism (schema-matched relikWrite on a step). For adaptive multi-turn access, see the [Adaptive keys](tools.md) (`relikRead:` for adaptive reads, `relikEdit:` for adaptive edits). Reading is parameterized — same field, different targets. Writing is structured — schema-matched writes only persist what conforms.

## Why reads and writes are separate primitives

A relik declared in the config is just a schema — folders, files, metatags, all empty. To make it memory, the ladder has to actually move data in and out. That's what reads and writes do.

Without them, a relik is dead structure. With them, it becomes:

- **Accumulated state.** Lessons learned across calls. Entities discovered. Verifications passed.
- **Selective persistence.** Only schema-conforming data hits the relik. Prose, hallucinations, and malformed JSON get filtered out.
- **Context-shaping inputs.** A step that reads the lessons file at start sees what failed last time; its prompt adapts.

Two design choices make LADR's memory programmable rather than magical:

1. **Reads and writes are explicit.** No implicit context-stuffing. The author declares exactly which fields read from which relik, and which steps write to which target. Memory is observable; debugging is possible.
2. **Writes are typed.** Schema-matched writes (`relikWrite.match`) extract JSON from the LLM's output, validate against a declared schema, and write only what conforms. The relik's schema is enforced.

This article covers the declarative read primitive (`relikRead` field type) and the declarative write mechanism (`relikWrite:`). The adaptive read/edit loops (`relikRead:` / `relikEdit:` step keys) are covered in [Adaptive keys](tools.md).

## relikRead: the read primitive

`relikRead` is one of the seven field types. It resolves at runtime by querying the relik store.

```yaml
fields:
  # File contents (default; no metatag specified).
  - name: Core
    type: relikRead
    from: { relik: "Project Memory", folder: facts, file: core.md }

  # A user metatag on one file.
  - name: CoreSummary
    type: relikRead
    from: { relik: "Project Memory", folder: facts, file: core.md, metatag: summary }

  # A user metatag across all files in the folder (returns array).
  - name: AllTags
    type: relikRead
    from: { relik: "Project Memory", folder: facts, metatag: tags }

  # System metatag: folder-level index.
  - name: FactsIndex
    type: relikRead
    from: { relik: "Project Memory", folder: facts, metatag: index }

  # System metatag: recursive tree.
  - name: FactsTree
    type: relikRead
    from: { relik: "Project Memory", folder: facts, metatag: tree, depth: 2 }

  # System metatag: file timestamps.
  - name: CoreModified
    type: relikRead
    from: { relik: "Project Memory", folder: facts, file: core.md, metatag: modified }
```

The `from` object has four fields:

| Field | Required | Description |
|---|---|---|
| `relik` | yes | Relik title. Must be declared in the config. |
| `folder` | yes | Folder name within the relik. Must be declared. |
| `file` | no | File name within the folder. If omitted, the read targets the folder level. |
| `metatag` | no | Metatag name. If omitted, the read returns file contents. |
| `depth` | no | Recursion depth for `metatag: tree` only. |

The combination of these fields determines what the read returns:

- **File + no metatag:** returns the file's contents as a string.
- **File + metatag:** returns the metatag's value (a typed value).
- **No file + metatag across folder:** returns an array of `{file, value}` pairs for every file in the folder that has the metatag. Empty array if none.
- **No file + system metatag (`index`, `tree`):** returns the auto-maintained folder index or recursive tree structure.

**Reading across a folder where some files lack the metatag.** Returns only files that have it. No error. Empty array if no file has the metatag.

## relikRead validation

At config load time, every `relikRead` is validated.

- Relik title must be declared in the config.
- Folder name must exist within that relik.
- If a file is specified, it must exist within that folder.
- If a metatag is specified on a file, it must be in that file's effective schema (folder-level ∪ file-level), or be a system metatag (`index`, `tree`, `created`, `modified`, `size`).
- If a metatag is specified without a file, the metatag must be in at least one file's effective schema (or be a system metatag).

Validation errors look like:

```text
Step "generate": relikRead field "PriorArt" references non-existent relik "Project Memory"
Step "generate": relikRead field "Lessons" references non-existent folder "lesson" in relik "Project Memory"
Step "generate": relikRead field "Confidence" references undeclared metatag "confidence" on file "core.md"
```

Every error includes the step's `id`, the field's name, and the offending reference so the author can locate the problem in the config.

## relikWrite: schema-matched writes

`relikWrite` is an optional block on a step. After the step runs, the runtime inspects the LLM's output, extracts the first JSON block, validates it against a declared schema, and if valid, writes the result to the declared target. **If no match, no-op (not an error)** — the write is opportunistic.

```yaml
- id: extract_facts
  systemPrompt: 'Read input. Emit JSON {"fact": "...", "confidence": 0.0-1.0} if a durable fact is found.'
  relikWrite:
    to: { relik: "Project Memory", folder: facts, file: core.md }
    field: fact                         # extract just the fact string, write to contents
    on: append                          # append | overwrite | mergeByKey
    match:
      type: object
      required: [fact, confidence]
      properties:
        fact: { type: string, minLength: 1 }
        confidence: { type: number, minimum: 0, maximum: 1 }
      additionalProperties: false
```

Properties:

| Property | Required | Default | Description |
|---|---|---|---|
| `to` | yes | — | Target: `{ relik, folder, file?, metatag? }`. |
| `match` | yes | — | JSON Schema. Extracted JSON must validate to be written. |
| `field` | no | (whole object) | Extract one field from the validated JSON before writing. |
| `on` | no | `append` | Write policy: `append`, `overwrite`, or `mergeByKey`. |
| `key` | no | — | Required when `on: mergeByKey`. Field name inside the JSON objects. |
| `atomic` | no | `false` | For array writes: all-or-nothing. Without it, per-item best-effort. |

**Why this design.** The LLM doesn't need to know about filesystems. It just emits structured output when it has something to say. The runtime handles extraction, validation, and persistence. If the LLM hallucinates or emits malformed JSON, the write silently no-ops — no error, no corruption, just a missed opportunity (no event is published; the relik file is unchanged).

## JSON extraction rules

`relikWrite.match` and `if.jsonMatches` use the same JSON extraction engine. The rules:

1. **Strict JSON parse first.** If the entire output is valid JSON (`{"a": 1}`), use it.
2. **Try code fence extraction.** If strict parse fails, look for a ```json ... ``` code fence and extract its contents.
3. **Try balanced-block extraction.** If no code fence, look for the first balanced `{...}` or `[...}` block. This handles prose-embedded JSON like `"The answer is {\\"score\\": 4}."`.
4. **No JSON found → no-op.** The gate fails (for `if.jsonMatches`) or the write is skipped (for `relikWrite.match`). No error.

Once JSON is extracted, it's parsed and validated against the schema. Strict JSON Schema validation: required fields must be present, types must match, ranges must hold, `additionalProperties: false` rejects unknown fields by default.

**Why first-block-wins.** Some LLMs emit multiple JSON blocks (thinking out loud before the final answer). The first block is the most likely to be the LLM's first structured emission, which is what you usually want. If your use case needs the last block, restructure the prompt to suppress intermediate JSON.

## Write policies

Three policies control how writes interact with existing content.

### `append` (default)

Add the new content to the end of the existing content. For text files, joined with a blank line. For JSON-typed metatags, the value is appended to the existing array (or wraps both values in a new array if the existing value is a scalar).

```yaml
relikWrite:
  to: { relik: "Memory", folder: trace, file: lessons.md }
  on: append
  match: { ... }
```

### `overwrite`

Replace the existing content entirely. Useful when the new content is the canonical version (e.g. "current best answer") and prior content should not accumulate.

```yaml
relikWrite:
  to: { relik: "Memory", folder: solution, file: best.md }
  on: overwrite
  match: { ... }
```

### `mergeByKey`

For JSON array content. Load the existing JSON array, merge new items by matching a key field **inside the JSON objects**, write the merged array back. This is what turns a relik file into a maintained structured store.

```yaml
relikWrite:
  to: { relik: "Memory", folder: registry, file: entities.md }
  on: mergeByKey
  key: id           # field name INSIDE the JSON objects, not a metatag
  match: { type: array, items: { ... } }
```

If the existing file contains `[{id: "a", ...}, {id: "b", ...}]` and the new content is `[{id: "b", ...updated...}, {id: "c", ...new...}]`, the merged result is `[{id: "a", ...}, {id: "b", ...updated...}, {id: "c", ...new...}]`. Existing entries with matching keys are replaced; new entries are appended.

**`key` is NOT a metatag.** It's the name of a field inside the JSON content stored in the file. Two distinct layers:

- **Metatags** describe the file itself (declared at config time, attached to the file, writable via `relikWrite` with `metatag:` target). Examples: `summary`, `tags`, `confidence`.
- **JSON content fields** live inside the JSON object/array stored as the file's contents. The `key:` in `mergeByKey` is one of these.

Without `mergeByKey`, you have only `append` (duplicates on every write) or `overwrite` (wipes prior content). `mergeByKey` is what lets a relik act as a maintained structured store — registries, indexes, scoreboards, knowledge graphs — where entries persist and update by key across calls.

## Conditional persistence

Gates are control-flow only (continue, jump, abort) — they can't write to reliks directly. To persist conditionally, jump to a step that declares `relikWrite:` or `relikEdit:`. See [Control Flow](control-flow.md) for the pattern.

## The from: source vocabulary

Dynamic step loading uses `from:` to specify what config to load. The same vocabulary is used by field fallbacks and adaptive-key result injection. One way to specify a source, everywhere.

```yaml
# All survivors of the immediately preceding step.
# Equivalent to from: { stepId: ..., nodeRef: accumulate }.
from: previous

# The current step's own output.
from: output

# A literal string.
from: "Literal text to write."

# A specific prior step.
from: { stepId: generate }

# A specific node of a prior step.
from: { stepId: generate, nodeRef: current }       # per-node routing
from: { stepId: generate, nodeRef: previous }      # sequential flow
from: { stepId: generate, nodeRef: accumulate }    # all nodes joined
from: { stepId: generate, node: 3 }                # absolute index

# A relik file's contents.
from: { relik: "Memory", folder: trace, file: verified.md }

# A relik metatag's value.
from: { relik: "Memory", folder: trace, file: verified.md, metatag: score }
```

**Default behavior when no `nodeRef` is declared.** The write targets all surviving nodes of the source step. The runtime picks the format based on the target:

- For `to.file` (file contents): survivors are joined with `\\n\\n` and written as text.
- For `to.metatag` of a JSON-typed metatag: survivors are written as a JSON array.

This means "no `nodeRef`" is the same as "`nodeRef: accumulate`" — there is no parallel taxonomy.

**Edge cases:**

- Previous step had 1 node: the default is just that output. Simple case.
- Previous step had 0 survivors (all pruned): the step doesn't run (existing NodeDependencyError path).
- Previous step was a **group**: `previous` is ambiguous — disallowed. Must specify `stepId` explicitly.
- Current step is multi-node + `from: previous` (no `nodeRef`): each current node writes the same set of survivors. Usually not intended; use `nodeRef: current` for per-node routing.

## Validation rules

Write-related rules checked at config load time.

**`relikWrite` rules:**

- `to.relik` must reference a declared relik.
- `to.folder` must reference a declared folder in that relik.
- `on: mergeByKey` requires `key`.
- `key` is only valid when `on: mergeByKey`. Using it with other policies fails.
- `match` must be non-empty valid JSON Schema.

**Gated write rules:**

- `to.relik` and `to.folder` must be declared.
- `from` must be a valid source spec (`previous`, `output`, literal, step ref, or relik ref).

**`from:` resolution rules** (checked at runtime, not config time):

- `previous` requires a prior step to have executed.
- `{ stepId: X }` requires step `X` to have executed in the current loop.
- `{ relik: ... }` requires the relik store to be available.

Every error includes the step's `id` and the branch (`then` or `else`) where the write was declared.

## Common patterns

**Schema-matched extraction.** Step emits JSON when it has a durable fact; runtime validates and appends to a relik file.

```yaml
- id: extract
  systemPrompt: 'Emit JSON {"fact": "...", "confidence": 0.0-1.0} if a durable fact is found.'
  relikWrite:
    to: { relik: "Memory", folder: facts, file: core.md }
    field: fact
    on: append
    match:
      type: object
      required: [fact, confidence]
      properties:
        fact: { type: string, minLength: 1 }
        confidence: { type: number, minimum: 0.7 }
```

Only facts with confidence ≥ 0.7 are persisted. Prose, missing fields, or low-confidence claims silently no-op.

**Gated persistence of survivors.** Use a gate to decide what's worth saving.

```yaml
- id: verify
  nodes: 5
  systemPrompt: 'Score 1-5. Output JSON {"score": N, "verdict": "accept"|"reject"}.'
  if:
    jsonMatches:
      type: object
      properties:
        verdict: { type: string, enum: [accept] }
        score: { type: number, minimum: 4 }
    then:
      write:
        to: { relik: "Memory", folder: trace, file: verified.md }
        from: output
        on: append
    else:
      write:
        to: { relik: "Memory", folder: lessons, file: failures.md }
        from: output
        on: append
```

Survivors write their verdicts to the trace; rejected verifiers write their analyses to lessons. Both writes happen automatically based on gate outcome.

**Maintained entity store via `mergeByKey`.** Treat a relik file as a structured store.

```yaml
relikWrite:
  to: { relik: "Registry", folder: entities, file: index.md }
  on: mergeByKey
  key: id
  match:
    type: array
    items:
      type: object
      required: [id, name]
      properties:
        id: { type: string }
        name: { type: string }
        lastSeen: { type: string }
```

The file starts as `[]`. Each write adds new entities or updates existing ones by `id`. Over many calls, the file accumulates a maintained registry.

**Verify-then-persist in one step.** Combine a verifier gate with a schema-matched write so only verified outputs hit the relik.

```yaml
- id: verify_and_store
  systemPrompt: 'Output JSON {"answer": "...", "confidence": 0.0-1.0, "verified": true|false}.'
  if:
    jsonMatches:
      type: object
      required: [answer, confidence, verified]
      properties:
        verified: { type: boolean, enum: [true] }
        confidence: { type: number, minimum: 0.9 }
    then: continue
    else: abort
  relikWrite:
    to: { relik: "Memory", folder: verified, file: answers.md }
    field: answer
    on: append
    match:
      type: object
      required: [answer, confidence, verified]
      properties:
        answer: { type: string, minLength: 1 }
```

The gate ensures the answer is verified and high-confidence. The `relikWrite` then persists the answer text to the relik. Both checks run on the same output; the write only fires if the step doesn't abort.

