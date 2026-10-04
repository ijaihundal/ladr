# Adaptive keys: codeExecution / relikRead / relikEdit / files

Adaptive keys are LADR's answer to function calling. A step that declares any of the four flat
adaptive keys enters a bounded loop where the LLM can call enabled tools (run code, read reliks,
edit reliks, work with files), receive results, and continue reasoning — until it produces a final
answer or hits the round limit.

This is the function-calling pattern done LADR's way: text + JSON-Schema matching,
provider-agnostic, fully visible in the trace. No proprietary API needed.

---

## The four keys

Each adaptive key is a flat step-level property. A step can declare any mix of them.

### `codeExecution:`

Runs LLM-emitted code in a persistent interpreter session (python3/node/bash — direct runtimes,
no container). The process is kept alive across the step's turns so REPL state (imports,
variables, files in the session workspace) persists.

```yaml
codeExecution:
  maxRounds: 6
  runtime: python             # python | node | bash (default: python)
  as: ComputedResult           # field name the result is injected under
  requestMatch:                # JSON Schema — what a code call looks like
    type: object
    properties:
      code: { type: string }
  autoPrompt: true             # default: inject tool spec into prompt
```

The LLM emits `{"code": "...", "language": "python"}` to call it. `language` is optional and
overrides the declared runtime.

### `relikRead:`

A bounded read loop against a relik folder. The LLM sees a folder index, requests specific
files/subfolders/metatags/globs, and the engine injects their contents. This is the adaptive
counterpart to the declarative `relikRead` field type — same capability, two surfaces.

```yaml
relikRead:
  maxRounds: 5
  from: { relik: Knowledge, folder: docs }   # READ source (note: from, not to)
  as: RetrievedDocuments
  requestMatch:
    type: object
    properties:
      request:
        type: object
        properties:
          files: { type: array, items: { type: string } }
```

The LLM emits `{"request": {"files": ["path.md"]}}` or other retrieval modes (glob, metatag,
subfolders). See [Reads and Writes](reads-writes.md) for the read vocabulary.

### `relikEdit:`

Surgical editing of relik files mid-reasoning. The LLM emits edit operations against files in
the declared folder; each turn it receives an `EditReceipt` showing what applied, what didn't
and why, and a numbered view of the changed file.

```yaml
relikEdit:
  maxRounds: 4
  to: { relik: Memory, folder: drafts }      # WRITE target (note: to, not from)
  as: EditReceipt
  diff: false                 # default: per-op status only, no unified diff
  requestMatch:
    type: object
    properties:
      edit:
        type: object
        properties:
          file: { type: string }
          ops: { type: array }
```

The LLM emits `{"edit": {"file": "name.md", "ops": [...]}}` to edit. Operations:

| Op | Description |
|---|---|
| `replace` | Replace anchor text with content. |
| `insertBefore` | Insert content before the anchor. |
| `insertAfter` | Insert content after the anchor. |
| `delete` | Delete the anchor text. |
| `lineReplace` | Replace lines [start, end] (1-indexed, inclusive) with content. |
| `lineDelete` | Delete lines [start, end]. |
| `lineInsert` | Insert content before line `start`. |

Anchor ops require the anchor text to exist and be unique. Line ops address 1-indexed inclusive
ranges. Edits are atomic — all ops in a batch apply or none do.

### `files:`

Read, write, and list files relative to the run's file scope — without choosing the scope. The
grammar carries no path-root field: on a local run, paths resolve against the step's session
workspace (the same directory `codeExecution` uses as cwd, so the two tools compose — write a
file with `files:`, read it from python, or vice versa); on a hosted run, the device client
resolves paths against the folder the user granted that ladder, with per-ladder consent enforced
entirely device-side before anything touches disk.

```yaml
files:
  maxRounds: 8
  as: FileResult
  requestMatch:
    type: object
    properties:
      read:  { type: object, properties: { path: { type: string } } }
      write: { type: object, properties: { path: { type: string }, content: { type: string } } }
      list:  { type: object, properties: { glob: { type: string } } }
```

The LLM emits one of:

| Call | Effect | Injected as |
|---|---|---|
| `{"read": {"path": "report.md"}}` | file contents (1 MiB cap, `…(truncated)` flag) | the content |
| `{"write": {"path": "out/x.md", "content": "..."}}` | create/overwrite (`"append": true` to append) | `(wrote N bytes to path)` |
| `{"list": {"glob": "**/*.py"}}` | matching relative paths | newline-joined entries |

Paths are relative. Absolute paths and `..` escapes are rejected with a clear error (which the
LLM sees as tool feedback). A device-side denial ("denied by user") is an ordinary tool error —
the loop continues, it does not kill the run.

---

## The shared loop model

When a step declares **more than one** adaptive key, they share **ONE bounded loop** with **ONE
`maxRounds`** and **ONE `requestMatch`**. The engine dispatches each turn by inspecting which
property of the LLM's JSON output is populated:

1. **`code`** → codeExecution (if declared and `code` is non-empty)
2. **`request`** → relikRead (if declared and `request` is non-empty)
3. **`edit`** → relikEdit (if declared and `edit` is non-empty)
4. **`read`/`write`/`list`** → files (if declared and one is non-empty)

This means `{"code": "print(1)", "request": {"files": ["x"]}}` will execute code, not read.

### Shared field agreement

When multiple keys coexist, `maxRounds` and `requestMatch` must agree across all declared keys
(or be present on only one). Validation rejects conflicting values:

```
Step "X": relikRead.maxRounds (5) conflicts with codeExecution.maxRounds (3) —
adaptive keys share one loop; declare maxRounds on one key only.
```

In practice, most steps declare exactly one adaptive key, so the shared fields live naturally
on that one key.

---

## How the loop works

Each turn:

1. The engine assembles a prompt from the step's fields + system prompt + auto-injected tool spec.
2. The LLM produces output.
3. The engine tries to match the output against `requestMatch` (a JSON Schema).
4. **If it matches** → the output is a tool call. The engine dispatches to the matching tool, injects the result as a field, and continues the loop.
5. **If it doesn't match** → the output is the final answer. The step ends.

`maxRounds` guarantees termination. Each round is one LLM call. When exhausted, the engine
forces a final answer.

---

## Field reference

### `maxRounds` (required)

The hard cap on adaptive-loop turns. Must be > 0. Each turn is one LLM call — this is the cost
bound. When exhausted, the engine forces a final answer.

### `requestMatch` (required)

A JSON Schema the engine uses to decide whether the LLM's output is a tool call (match) or the
final answer (no match). It's also used to route the call by property name
(`code`/`request`/`edit`/`read`/`write`/`list`).

The engine extracts the first JSON block from the output (direct JSON, code-fenced, or
prose-embedded), validates it against the schema, and dispatches.

### `as` (required)

The field name the tool's result is injected under on subsequent turns. For `codeExecution`,
this is the computed result. For `relikRead`, the retrieved documents. For `relikEdit`, the
`EditReceipt` JSON. For `files`, the op result (content / write receipt / entries).

### `autoPrompt` (optional, default: true)

When true (or nil), the engine auto-injects a tool-usage spec into the system prompt describing
the available tools and their call formats. Set to `false` to write your own usage instructions
in the step's `systemPrompt`.

### `prompt` (optional)

When set, replaces the engine's default usage instructions for this specific tool. Allows
author-owned wording without disabling autoPrompt entirely.

### `runtime` (codeExecution only, default: python)

Selects the interpreter: `python`, `node`, or `bash`. Direct runtimes — the session runs on the
executing host with the user's own privileges (no container).

### `from` (relikRead only, required)

`{ relik, folder }` — the source folder to read from.

### `to` (relikEdit only, required)

`{ relik, folder }` — the target folder to edit. Must be `access: readWrite`.

### `diff` (relikEdit only, default: false)

When true, includes a unified diff in the receipt. Default is token-lean: per-op status + changed
line numbers only.

---

## Dispatch precedence

When an output matches the schema with multiple tool-call keys present, dispatch order is:

1. **codeExecution** (if `code` is present and non-empty)
2. **relikRead** (if `request` is present and non-empty)
3. **relikEdit** (if `edit` is present and non-empty)
4. **files** (if `read`, `write`, or `list` is present and non-empty; read > write > list)

---

## Per-key prompt overrides

Each key accepts an optional `prompt` field for author-owned wording:

```yaml
codeExecution:
  maxRounds: 6
  runtime: python
  as: Result
  requestMatch: { type: object, properties: { script: { type: string } } }
  prompt: "Emit {\"script\": \"...\"} to run Python code."
```

When set, `prompt` replaces the engine-rendered usage text for that key in the auto-prompt.

---

## Composition with other primitives

Adaptive keys compose with every other LADR primitive.

**With `if/then/else`:** the adaptive loop runs first (rounds complete), then the gate fires on
the final output. A step can read → compute → gate in one place.

**With `relikWrite`:** the adaptive loop and declarative write coexist. A step can read/compute
adaptively, then persist structured findings via schema-matched `relikWrite:`.

**With multi-node fanout (`nodes: N`):** each node runs its own adaptive loop independently.
Useful for parallel work; expensive (cost scales with `nodes × maxRounds`).

**With recursion:** a recursive child can have its own adaptive keys against the same shared
relik. The child's loop is independent of the parent's; the relik state is shared.

---

## Validation rules

- **`maxRounds` positive.** Zero or negative fails validation.
- **`as` non-empty.** Required on all four keys.
- **`requestMatch` non-empty valid JSON.** Must be a parseable JSON Schema.
- **`runtime` valid.** Must be `python`, `node`, or `bash` (codeExecution only).
- **`from.relik`/`from.folder` exist** (relikRead).
- **`to.relik`/`to.folder` exist and are writable** (relikEdit — read-only folders rejected).
- **Shared fields agree** when multiple keys coexist on one step.
