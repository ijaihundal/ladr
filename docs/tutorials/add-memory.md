# Add Persistent Memory with Reliks

Give a ladder long-term memory across calls. A worked tutorial using real LADR relik grammar: declare, read (relikRead), write (relikWrite, gated writes), and retrieve (on-demand fetch).

## Why reliks (and not "just stuff context")

Most LLM apps that want "memory" do it by stuffing prior conversation into the prompt. This works until it doesn't:

- The prompt grows linearly with usage. After 50 calls, your prompt is 50k tokens.
- The model attends to old context unevenly. Relevant facts get lost in the noise.
- The author has no control over what's actually surfaced.

LADR reliks are the **explicit** alternative. The author declares:

- The **schema** of memory (folders, files, metatags) at config time.
- **What gets written**; `relikWrite` step directive, optionally gated.
- **What gets retrieved**; `relikRead` field source, or `retrieve:` for on-demand fetch.

The runtime handles storage and retrieval. The author never loses control of what's in the prompt.

## Step 1: Declare a relik

Add a top-level `reliks` array. Each relik has folders; each folder has a schema, token budget, and access policy.

```yaml
reliks:
  - title: "Lessons"
    folders:
      - name: wins
        tokenLimit: 800
        access: readWrite
        evictionPolicy: FIFO
        metatags:
          - { name: summary, type: string }
          - { name: tags,    type: "string[]" }
        files:
          - name: index.md
            init: blank
      - name: failures
        tokenLimit: 400
        access: readWrite
        metatags:
          - { name: summary, type: string }
          - { name: severity, type: number }
        files:
          - name: log.md
            init: blank
```

Two folders: `wins` (high-quality past results) and `failures` (postmortem log). Each has user metatags (`summary`, `tags`, `severity`) for retrieval and ranking.

The engine also maintains **system metatags** on every folder/file: `index` (folder-level file listing), `tree` (recursive structure), `created` / `modified` / `size` (file-level).

## Step 2: Read from a relik via relikRead

Add a field to your draft step that pulls the wins folder's index, so the LLM can decide what to retrieve:

```yaml
- id: draft
  fields:
    - { name: Context, type: text, from: "input.context" }
    - name: WinsIndex
      type: relikRead
      from: { relik: "Lessons", folder: wins, metatag: index }
  systemPrompt: |
    Answer the question.
    The "Wins Index" field above lists prior successes you can reference.
    If you want a specific file's contents, emit a retrieval request (see Step 5).
```

`relikRead` is just "read these fields." The shape is:

```yaml
from: { relik, folder, file?, metatag?, depth? }
```

- **Omit `metatag`** → read the file contents (default).
- **Set `metatag: <user-metatag>`** → read that metatag's value (one file's value, or all files' values if `file` is omitted).
- **Set `metatag: index`** → read the folder's auto-generated file listing.
- **Set `metatag: tree`** → read the recursive folder+subfolder structure (`depth: N` controls recursion).

On the first call, the relik is empty; the field renders blank or `"[]"`. On later calls, prior wins surface automatically.

**Note:** `relikRead` does NOT support server-side filtering (`op`, `value`, `orderBy`, `limit`). It's "read these fields," period. For dynamic filtering at runtime, use `retrieve:` (Step 5).

## Step 3: Write via schema-matched relikWrite

Add a `relikWrite` directive to your final step. The engine extracts JSON from the LLM output, validates against a schema, and writes if valid. **No match = no-op (not error).**

```yaml
- id: refine
  ...
  relikWrite:
    to: { relik: "Lessons", folder: wins, file: latest.md }
    on: overwrite                         # append | overwrite | mergeByKey
    match:                                # JSON Schema; what valid output looks like
      type: object
      required: [summary, tags]
      properties:
        summary: { type: string, minLength: 1 }
        tags: { type: array, items: { type: string } }
      additionalProperties: false
  systemPrompt: |
    After producing the final answer, emit JSON summarizing what was learned:
    {"summary": "<one line>", "tags": ["keyword1", "keyword2"]}
```

What happens:
1. The step runs and produces its normal output (the answer).
2. The engine inspects the output, extracts the first JSON block, validates against `match`.
3. **On success:** writes the JSON to `wins/latest.md` (overwriting prior content).
4. **On failure or no JSON:** no-op. silently no-ops (no error, no event). The relik file is unchanged.

`field: <name>` optionally extracts one field from the validated JSON before writing. `on: mergeByKey` + `key: <field>` maintains a structured store (registry/knowledge-graph) keyed by a JSON field.

## Step 4: Write via gated write (if/then)

The alternative write mechanism: gated writes under `if/then`. Use this when you want to write **unconditionally on gate match** (no schema extraction):

```yaml
- id: record
  systemPrompt: "Output RECORD if worth saving, else SKIP."
  if:
    equals: "RECORD"
    then:
      write:
        to: { relik: "Lessons", folder: wins, file: log.md }
        from: previous              # writes all survivors of preceding step, formatted by target type
        on: append                  # default
    else: continue
```

`from:` accepts the unified source vocabulary (also used by dynamic steps):

| Form | Meaning |
      |------|---------|
      | `previous` | All survivors of the immediately preceding step. Engine formats based on target: file contents get `\n\n`-joined; JSON-typed metatag gets a JSON array. |
      | `output` | The current step's own output. |
      | `"literal text"` | A literal string. |
      | `{ stepId: "X" }` | All survivors of step X. |
      | `{ stepId: "X", nodeRef: "current" }` | Per-node routing: writer node N ← source node N. |
      | `{ stepId: "X", node: 3 }` | One specific node by index. |
      | `{ relik, folder, file?, metatag? }` | Relik reference (cross-relik writes). |

## Step 5: On-demand fetch via retrieve:

`relikRead` requires you to know at config time which file/metatag to read. `retrieve:` lets the LLM decide at runtime; it sees the index, requests specific files, and the engine fetches them within a bounded loop.

```yaml
- id: research
  type: normal
  fields:
    - { name: Question, type: text, from: "input.context" }
    - { name: WinsIndex, type: relikRead, from: { relik: "Lessons", folder: wins, metatag: index } }
  retrieve:
    to: { relik: "Lessons", folder: wins }
    as: RetrievedDocuments             # injected field name on subsequent rounds
    maxRounds: 5                       # hard cap → guarantees termination
    requestMatch:                      # JSON Schema; if output matches → retrieval request
      type: object
      required: [request]
      properties:
        request:
          type: object
          properties:
            files:      { type: array, items: { type: string } }
            subfolders: { type: array, items: { type: string } }
            metatag:
              type: object
              properties:
                name: { type: string }
                contains: { type: string }
            glob: { type: string }
          additionalProperties: false
      additionalProperties: false
  systemPrompt: |
    You can see WinsIndex. To retrieve, emit exactly:
    {"request": {"files": ["path.md"]}}
    {"request": {"metatag": {"name": "tags", "contains": "calculus"}}}
    When you have enough, output your final answer in prose.
```

Execution model:
1. Round 1: engine assembles fields (including `WinsIndex`), LLM call #1 produces output.
2. If output matches `requestMatch` → engine fetches per the request, **adds the `RetrievedDocuments` field**, runs LLM call #2 with enriched context. Round increments.
3. If output does NOT match → step completes (the output is the final answer).
4. Repeat until final answer OR `maxRounds: 5` hit. On budget exhaustion, the engine injects: *"Retrieval budget exhausted. Produce your final answer now."* Next call must produce final answer.

Termination is guaranteed by `maxRounds`. A ladder author cannot cause infinite retrieval loops.

See [Retrieve](../spec/retrieve.md) for the four retrieval modes (files / subfolders / metatag query / glob) and dedup behavior.

## Relik lifecycle and scope

Reliks are **per-user** by default. If user A calls the ladder, they read and write to their own relik. User B has a separate relik. Cross-user sharing is a planned feature.

Reliks **persist across calls**; that's the whole point. A user who calls the ladder today and again next week sees the same memory.

Reliks are **scoped to the ladder**. Two different ladders by the same author have separate reliks. To share memory across ladders, use recursion or cross-ladder jumps (advanced; see [Jumps](../spec/jumps.md)).

## Token cost and eviction

Every folder has a `tokenLimit`. When a write would push the folder over budget, the `evictionPolicy` kicks in:

- `FIFO` (default); drop the oldest content. Cheap, predictable.
- `LRU`; drop the least-recently-read content. Better for hot/cold patterns.
- `reject`; fail the write, preserve existing content. Use for write-once folders.

For a `wins` folder with `tokenLimit: 800` and `FIFO` eviction, the ladder always remembers roughly the 800 most recent tokens of wins; older wins fall off the front as new ones come in. Bump `tokenLimit` to remember more, but watch total prompt size: relik content gets rendered into the prompt.

## Where to go next

- **[Reliks Reference](../spec/reliks.md)**; full folder/file/metatag schema.
- **[Reads & Writes](../spec/reads-writes.md)**; complete `relikRead`/`relikWrite` reference with all options.
- **[Retrieve](../spec/retrieve.md)**; on-demand bounded fetch for pulling specific records by metatag at runtime.
- **[Crucible Reference Ladder](../examples/gated-tot.md)**; a complete ladder that uses reliks for persistent memory across all its phases.

