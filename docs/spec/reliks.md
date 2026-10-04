# Reliks

A relik is a persistent memory store declared at config time. It is composed of folders of plain-text files, each file carrying typed metatags. A relik survives across ladder invocations: a ladder writes to a relik on call N, reads from it on call N+1. The author declares what gets written and what gets retrieved; there is no implicit context-stuffing. Memory in LADR is programmable, not magical.

## Why reliks exist

Every published reasoning strategy in the literature is stateless. Tree-of-Thoughts, reflexion, self-consistency, self-refine — they all start each invocation from scratch. Whatever the strategy learned on call 1 is gone by call 2. The model has no memory of having reasoned about this user, this task, this domain before.

That's not for lack of trying. It's because no reasoning strategy has had a memory primitive to use. LangChain has vector stores, but those are embeddings soup — opaque, unstructured, hard to debug. Agent frameworks have "memory" but it's just chat history. None of it is the structured, schema'd, declared-at-config-time, programmatically-writable memory that a reasoning strategy needs to compound across calls.

Reliks are that primitive. They give the ladder author:

- **Declaration over magic.** The author declares what folders exist, what files exist, what types the metatags carry. The runtime enforces the schema. No more "what did the LLM decide to remember?" surprises.
- **Structured writes.** A step can declare "after I run, extract the JSON score from my output and append it to the trace folder." The runtime does the extraction, validates against the schema, writes only what conforms.
- **Structured reads.** A step can declare "read the lessons file at the start so I see what failed last time." The runtime reads and injects it as a field.
- **Persistence across calls.** The same ladder invoked twice by the same user sees the same relik. Learning accumulates.
- **Cross-ladder sharing.** A relik declared in one ladder can be referenced by another via cross-ladder jumps. One ladder's lessons can inform another.

Without reliks, every ladder is a one-shot program. With reliks, a ladder becomes a long-running reasoning agent that compounds knowledge over time.

## Relik anatomy

A relik declaration in YAML:

```yaml
reliks:
  - title: Project Memory                # required - unique within the config
    folders:                             # required - at least one
      - name: facts                      # required - unique within the relik
        tokenLimit: 500                  # required - per-file token cap
        access: readWrite                # readWrite (default) | read
        evictionPolicy: FIFO             # FIFO (default) | LRU | reject
        metatags:                        # optional - folder-level schema
          - { name: summary, type: string }
          - { name: tags,    type: 'string[]' }
        files:                           # optional - declared files
          - name: core.md                # file name; unique within folder
            init: blank                  # blank or literal content
            metatags:                    # file-level extension to folder schema
              - { name: confidence, type: number }
              - { name: source, type: string }
          - name: scratch.md
            init: blank
```

The relik hierarchy:

- A **relik** has a title and at least one folder.
- A **folder** has a name, a per-file token limit, an access policy, an eviction policy, optional metatags (the folder's base schema), and optional pre-declared files.
- A **file** has a name, an initial content (`blank` or literal), and optional metatags (extending the folder's schema for this file specifically).

The strict parser rejects unknown keys at every level. Adding a new field is a schema change, not a silent extension.

## Effective schema

Every file has an effective schema computed as the union of its folder's metatags and its own metatags. If a metatag with the same name exists at both levels, the file-level declaration wins.

```yaml
reliks:
  - title: Project Memory
    folders:
      - name: facts
        tokenLimit: 500
        metatags:                          # applies to ALL files in this folder
          - { name: summary, type: string }
          - { name: tags,    type: 'string[]' }
        files:
          - name: core.md
            init: blank
            metatags:                      # extends folder schema for THIS file only
              - { name: confidence, type: number }
              - { name: source, type: string }
          - name: scratch.md
            init: blank
            # no file-level metatags; effective schema = folder schema only
```

For `core.md`, the effective schema is `{ summary, tags, confidence, source }` (folder + file).

For `scratch.md`, the effective schema is `{ summary, tags }` (folder only).

`relikRead` and `relikWrite` operations validate against the effective schema. Reading a metatag that isn't in the file's effective schema (and isn't a system metatag) fails validation. Writing one is also caught.

## Metatag types

Five metatag value types are supported:

| Type | What it holds | Example |
|---|---|---|
| `string` | A single string. | `"The cache invalidation strategy worked."` |
| `string[]` | A list of strings. | `["calculus", "integration", "u-substitution"]` |
| `number` | A single number (int or float). | `4.5` |
| `boolean` | True or false. | `true` |
| `object` | Arbitrary JSON object, validated against an inline schema. | `{"verdict": "accept", "score": 4}` |

The `object` type requires an inline `schema` field with a JSON Schema:

```yaml
metatags:
  - name: evaluation
    type: object
    schema:
      type: object
      required: [verdict, score]
      properties:
        verdict: { type: string, enum: [accept, reject] }
        score: { type: number, minimum: 0, maximum: 5 }
      additionalProperties: false
```

Writes to an `object` metatag are validated against the schema. No-match is a no-op (see Reads and Writes).

## System metatags

Five metatags are auto-maintained by the runtime. Authors cannot write to them; they're updated automatically as files are read and written.

| Metatag | Scope | What it provides |
|---|---|---|
| `index` | Folder-level | Auto-generated list of files in the folder with their sizes and timestamps. Returns a structured array. |
| `tree` | Folder-level, recursive | Recursive folder + subfolder structure with token sizes. The `depth: N` parameter on `relikRead` controls how deep to recurse. |
| `created` | File-level | Timestamp when the file was created. |
| `modified` | File-level | Timestamp when the file was last written. |
| `size` | File-level | Current token count of the file's contents. |

`index` and `tree` are the foundation of the retrieve primitive (see Retrieve). A step that needs to know "what files exist" reads `metatag: index`; a step that needs to know "what's the folder structure" reads `metatag: tree, depth: 2`. These reads are always valid; they don't need to be declared in any folder's schema.

```yaml
fields:
  - name: Available Files
    type: relikRead
    from: { relik: "Project Memory", folder: facts, metatag: index }

  - name: Folder Structure
    type: relikRead
    from: { relik: "Project Memory", folder: facts, metatag: tree, depth: 2 }

  - name: Core Last Modified
    type: relikRead
    from: { relik: "Project Memory", folder: facts, file: core.md, metatag: modified }
```

## Folder access policies

Every folder has an access policy controlling whether the ladder can write to it.

| Policy | Read | Write |
|---|---|---|
| `readWrite` (default) | Yes | Yes |
| `read` | Yes | No |

There is no write-only policy. It was considered and rejected as a footgun: a folder the ladder can write to but cannot read provides no observable state, which makes debugging impossible.

A ladder that tries to write to a `read` folder gets a runtime error: `folder "X" is read-only`. The step fails; the rest of the ladder may still run depending on its error-handling config.

Typical pattern: declare two parallel folders, one read-only for stable reference data (canonical facts, embeddings) and one read-write for accumulated state (lessons, scratch, in-progress work).

```yaml
reliks:
  - title: Knowledge Base
    folders:
      - name: canonical        # read-only; set up out-of-band
        tokenLimit: 2000
        access: read
        files:
          - { name: facts.md, init: "" }
      - name: working          # read-write; ladder appends here
        tokenLimit: 500
        access: readWrite
        evictionPolicy: FIFO
        files:
          - { name: notes.md, init: blank }
```

## Eviction policies

Every folder declares a `tokenLimit` per file. When a write would push a file past its limit, the runtime applies the folder's eviction policy.

| Policy | Behavior on overflow |
|---|---|
| `FIFO` (default) | Oldest content (from the top of the file) is removed until the new content fits. The most recent content survives. |
| `LRU` | Least-recently-read content is removed. The runtime tracks read counts per file. Useful for folders where some entries are accessed often and others never. |
| `reject` | The write fails. The file is unchanged. Useful when you never want silent data loss; pair with explicit cleanup logic in the ladder. |

Eviction is per-file, not per-folder. A folder with three files at 80% capacity each is fine; the limit applies to each file individually.

Eviction preserves content boundaries when possible. FIFO eviction starts at a newline character so partial lines aren't left dangling. The runtime does not split JSON structures mid-object; if eviction would corrupt a JSON array, it removes whole entries.

Token counting currently uses a rough approximation (chars / 4). A real tokenizer is planned. The approximation is conservative; a file that fits under the limit today will continue to fit under the real tokenizer.

## Two-tier storage

Reliks use a write-behind cache pattern with two tiers.

**Hot tier (during execution).** When a ladder instance starts, the runtime loads the relevant reliks into the hot tier. Every `relikRead`, `relikWrite`, and `retrieve` operation hits the hot tier — sub-millisecond latency. The runtime never touches the persistent store mid-execution.

**Cold tier (persistent).** After execution (or on step boundaries), the runtime flushes hot-tier state back to the persistent store. This is what users see when they browse the relik outside the ladder: durable, human-readable, diffable files.

| Deployment | Hot tier | Cold tier |
|---|---|---|
| Reference impl | In-process in-memory map | None (or optional filesystem for inspection) |
| Production cloud | Redis | Database / object storage |
| Local-first desktop (planned) | In-process in-memory map | Filesystem `.md` files (Obsidian vault) |

The flush point matters for crash recovery:

- **Flush on step boundary (recommended).** After each step completes, the runtime flushes relik state. Hot path during a step stays hot-only (fast). Crash recovery re-runs from the last completed step.
- **Flush on instance complete (alternative).** Simpler but worse crash recovery. If the ladder crashes mid-execution, all relik writes are lost.

The two-tier architecture is invisible to the ladder author. `relikRead` and `relikWrite` target the hot tier; the runtime handles the cold tier behind the scenes. The same YAML works identically across deployments.

## Relik scope and lifetime

Reliks persist across ladder invocations. That's the whole point. But they have a defined scope:

- **Per-user.** A relik belongs to one user. Different users invoking the same ladder see different reliks. Cross-user sharing via published reliks is planned.
- **Per-ladder.** Each ladder has its own relik namespace. Two different ladders cannot share a relik directly (they can exchange data via cross-ladder jumps and relik parameters).
- **Per-session (optional).** A user can open multiple sessions for the same ladder. Whether sessions share a relik or have isolated reliks is configurable per ladder. See [Stateful Sessions](../reference/sessions.md) for the session API and lifecycle.

The runtime guarantees:

- **Single-writer per call.** Within one execution, only one ladder instance writes to a given relik. No concurrent writers, no race conditions.
- **Cross-execution consistency.** Last-write-wins across separate executions. If two sessions write to the same file, the second write wins. A planned update adds vector clocks and a user-visible diff tool for finer-grained conflict resolution.
- **Atomicity per write.** A single `relikWrite` either fully succeeds or has no effect. The runtime does not leave partially-written state on a validation failure.

Reliks are explicitly declared. There is no global "memory" or implicit context-stuffing. If a ladder does not declare a relik, it has no memory across calls. This is by design: the author controls exactly what the ladder remembers.

## Validation rules

Relik declarations are validated at config load time. The full rule set:

**Relik-level:**

- `title` is non-empty.
- Relik titles are unique within the config.
- Every relik has at least 1 folder.

**Folder-level:**

- `name` is non-empty.
- Folder names are unique within the relik.
- `tokenLimit` is positive.
- Metatag types are valid (`string`, `string[]`, `number`, `boolean`, `object`).
- Metatag names are non-empty and unique within the folder.
- `type: object` metatags declare an inline `schema`.

**File-level:**

- `name` is non-empty.
- File names are unique within the folder.
- File-level metatag types are valid.
- File-level metatag names are unique within the file.

**Reference rules (checked across the whole config):**

- Every `relikRead.from.relik` references a declared relik.
- Every `relikRead.from.folder` references a declared folder in that relik.
- Every `relikRead.from.file` (if specified) references a declared file in that folder.
- Every `relikRead.from.metatag` (if specified) is in the file's effective schema or is a system metatag.
- Every `relikWrite.to.relik` and `retrieve.to.relik` reference declared reliks and folders.

Every error includes the relik title, folder name, and (if relevant) file name so the author can locate the problem in the config.

## Common patterns

**Accumulated lessons (reflexion memory).** A single-folder relik for structured lessons learned across calls.

```yaml
reliks:
  - title: Reflexion Memory
    folders:
      - name: lessons
        tokenLimit: 500
        access: readWrite
        evictionPolicy: FIFO
        metatags:
          - { name: severity, type: number }
          - { name: tags,     type: 'string[]' }
        files:
          - { name: failures.md, init: blank }
```

On every failed verification, the ladder extracts a structured lesson and appends it. On the next call, `generate` reads the lessons file and uses it to avoid repeating the same mistake.

**Maintained entity store (knowledge graph).** A relik file using `mergeByKey` to maintain a structured store of entities.

```yaml
reliks:
  - title: Entity Store
    folders:
      - name: registry
        tokenLimit: 2000
        access: readWrite
        files:
          - { name: entities.md, init: "[]" }    # JSON array literal
```

A `relikWrite` with `on: mergeByKey, key: id` adds or updates entities by their `id` field. The file remains a valid JSON array across writes; the runtime loads, merges, and writes back.

**Read-only reference data.** A folder the ladder reads from but never writes to, populated out-of-band.

```yaml
reliks:
  - title: Knowledge Base
    folders:
      - name: docs
        tokenLimit: 5000
        access: read
        files:
          # Populated via direct file upload, Obsidian sync, or another ladder's writes
          - { name: api_reference.md, init: blank }
          - { name: architecture.md, init: blank }
```

The ladder reads from this folder via `relikRead` or `retrieve` but cannot write to it. Use it for canonical knowledge that should not be modified at runtime.

**Two-tier working + canonical.** Combine read-only and read-write folders in one relik.

```yaml
reliks:
  - title: Project Memory
    folders:
      - name: canonical
        tokenLimit: 5000
        access: read
      - name: working
        tokenLimit: 500
        access: readWrite
        evictionPolicy: LRU
```

The ladder reasons against `canonical` (stable facts) and writes intermediate state to `working` (LRU-evicted to bound size). On the next call, `working` may have changed but `canonical` is still intact.

