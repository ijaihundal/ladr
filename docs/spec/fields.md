# Fields

Fields define what data flows into each step's prompt. You never write a prompt template; you declare fields, and the assembled prompt is built from them.

## What Fields Do

Every step declares a list of fields. When the step is about to run, each field resolves to a concrete value; a user input, another step's output, a knob value, or a runtime number. These resolved values are then assembled into the prompt the LLM receives.

The assembled prompt has two parts. First, the resolved fields render as labeled lines:

```
Query: What is the meaning of life?
Analysis: The data shows a clear upward trend in existential inquiry...
Draft 1: First attempt at answering
Draft 2: Second attempt at answering
Node Number: 3
Branch Count: 5
```

Each field produces one line; `FieldName: value`. For fields that resolve to multiple values (`multi_ingest`), the lines are auto-numbered: `FieldName 1: value`, `FieldName 2: value`, and so on.

After all field lines, the step's `systemPrompt` is appended under `[System Instruction]`. Fields are rendered in declaration order, separated by blank lines.

## Field Types

Seven field types exist in LADR. Five carry data into the prompt from inputs,
prior step outputs, and runtime state. The other two (`relikRead` and
`manifest`) have their own articles: `relikRead` is documented in Memory >
Reads and Writes; `manifest` is documented below and in Dynamic Steps.

**The seven field types:**

| Field type | What it provides |
|---|---|
| `text` | A value from the ladder's runtime input map (typically the user's message). |
| `ingest` | One node's output from a prior step. |
| `multi_ingest` | Multiple node outputs joined into a numbered block. |
| `nodeInfo` | The current node's number (1-indexed). |
| `knobInfo` | A knob's resolved value at call time. |
| `relikRead` | Contents read from a persistent memory store (relik). See [Reads and Writes](./reads-writes.md). |
| `manifest` | A runtime snapshot of everything readable at this step. See below. |

Each type has a distinct `from:` shape and resolution behavior, documented below.

**`text`** reads a value from the ladder's runtime inputs. The `from` property is a dot-path string; most commonly `input.context`. Via the API, `input.context` is the last user message extracted from the `messages` array. During recursion, `input.context` is replaced with the output of the step that triggered the child execution. During cross-ladder handoffs, `input.context` is replaced with the firing step's output.

```yaml
- name: Query
  type: text
  from: input.context
```

Renders as `Query: Should we use retrieval augmentation?`

**`ingest`** pulls a single output from another step. The `from` object specifies which step (`stepId`), which loop iteration (`loopRef`), and optionally which node (`nodeRef`).

```yaml
- name: Analysis
  type: ingest
  from:
    stepId: analyze
    loopRef: current
```

Renders as `Analysis: The data shows a clear upward trend...`

**`multi_ingest`** pulls multiple outputs and merges them into one auto-numbered block. The `from` property is an array of source objects; each with `stepId`, `loopRef`, and optional `nodeRef`. All resolved values are concatenated and numbered sequentially, regardless of which source they came from. This is the only field type that can use `loopRef: "accumulate"` or `nodeRef: "accumulate"`.

```yaml
- name: Previous Drafts
  type: multi_ingest
  from:
    - stepId: step0
      loopRef: 0
    - stepId: final
      loopRef: accumulate
```

Renders as multiple numbered lines: `Previous Drafts 1: ...`, `Previous Drafts 2: ...`, etc. The numbering is continuous; it doesn't reset per source.

**`nodeInfo`** injects the current node number (1-based). No `from` property is needed; it automatically resolves to which parallel branch is executing.

```yaml
- name: Node Number
  type: nodeInfo
```

Renders as `Node Number: 3`

**`knobInfo`** injects a knob's resolved value. The `from` property is the knob's key name (not a `{{}}` reference; just the bare name).

```yaml
- name: Branch Count
  type: knobInfo
  from: coverage
```

If the `coverage` knob is set to 5, this renders as `Branch Count: 5`.

## ingest and multi_ingest References

The `ingest` and `multi_ingest` field types share the same reference system for pointing at step outputs. Every source object has three properties:

**`stepId`**; the id of the step whose output to read. Must reference a step that exists and has already executed.

**`loopRef`**; which loop iteration to read from. Four options:

- `current`; the same loop that's currently executing.
- `previous`; the loop immediately before the current one. At loop 0 this resolves to nothing (there is no previous loop), so it's only meaningful from loop 1 onward.
- A **number** (e.g. `0`); a specific loop index. Always reads from that exact iteration, regardless of which loop is currently running.
- `accumulate`; collects outputs from **all** previous loops. At loop 2, this reads loops 0 and 1. At loop 3, it reads loops 0, 1, and 2. Only valid on `multi_ingest` because it produces multiple values.

**`nodeRef`**; optional. Which node within the referenced step to read from. Three options:

- `current`; the same node number that's currently executing.
- `previous`; one less than the current node number. In a sequential step with 5 nodes, node 3 reads node 2's output. Node 1 has no previous, so pair with `skipFirstNode: true`.
- `accumulate`; collects all node outputs from the referenced step. Only valid on `multi_ingest`.

When `nodeRef` is omitted, the reference reads the step's output without targeting a specific node; this works for single-node steps.

**`skipFirstNode`**; a boolean property available on `ingest` fields. When true, the field returns an empty string for node 1 instead of trying to read a previous output that doesn't exist. Standard pattern for sequential self-referencing steps:

```yaml
- id: refine
  name: Refine
  type: sequential
  nodes: 3
  fields:
    - name: Previous
      type: ingest
      from:
        stepId: refine
        loopRef: current
        nodeRef: previous
      skipFirstNode: true
  systemPrompt: "Improve the previous output."
```

Node 1 gets an empty `Previous:` line and generates from scratch. Nodes 2 and 3 each read the previous node's output and refine it.

## How accumulate Grows Across Loops

The `accumulate` loop reference collects outputs from all previous loops. Each time the ladder completes a loop, the accumulated list of outputs grows by one entry.

A single-source accumulate, collecting the `final` step from every previous loop:

```yaml
- name: Draft
  type: multi_ingest
  from:
    - stepId: final
      loopRef: accumulate
```

During **loop 1** (the second pass), the ladder has completed loop 0, so the field renders:

```text
Draft 1: <final step output from loop 0>
```

During **loop 2** (the third pass):

```text
Draft 1: <final step output from loop 0>
Draft 2: <final step output from loop 1>
```

During **loop 3**:

```text
Draft 1: <final step output from loop 0>
Draft 2: <final step output from loop 1>
Draft 3: <final step output from loop 2>
```

Each loop, the accumulated list grows by one. The field name stays the same and the numbering is appended.

A multi-source accumulate mixes fixed references with accumulated ones:

```yaml
- name: Drafts
  type: multi_ingest
  from:
    - stepId: step0
      loopRef: 0
    - stepId: step11
      loopRef: accumulate
```

The first source always reads loop 0 (the initial draft). The second accumulates across all previous loops. Numbering is continuous across both sources.

During **loop 1**:

```text
Drafts 1: <step0 loop 0 output>
Drafts 2: <step11 loop 0 output>
```

During **loop 2**:

```text
Drafts 1: <step0 loop 0 output>
Drafts 2: <step11 loop 0 output>
Drafts 3: <step11 loop 1 output>
```

The fixed source always produces the same entries. The accumulated source adds one more entry each loop.

## Prompt Assembly Across Loops

When a ladder has loops, the assembled prompt evolves with each pass. Fields using `loopRef: accumulate` grow, fixed references stay the same, and the LLM receives progressively more context each round.

Consider this step inside a looping ladder:

```yaml
fields:
  - name: Context
    type: text
    from: input.context
  - name: Drafts
    type: multi_ingest
    from:
      - stepId: step0
        loopRef: 0
      - stepId: final
        loopRef: accumulate
  - name: Node Number
    type: nodeInfo
systemPrompt: "Produce the final refined draft."
```

**During loop 0**, the accumulate is empty (there are no previous loops yet). The step receives only the initial draft:

```text
Context: Write an essay on vector databases

Drafts 1: <step0 loop 0 output; the initial draft>

Node Number: 1

[System Instruction]
Produce the final refined draft.
```

**During loop 1**, the accumulate collected loop 0's `final` output:

```text
Context: Write an essay on vector databases

Drafts 1: <step0 loop 0 output; the initial draft>
Drafts 2: <final step loop 0 output; first refinement>

Node Number: 1

[System Instruction]
Produce the final refined draft.
```

**During loop 2**, one more entry appears:

```text
Context: Write an essay on vector databases

Drafts 1: <step0 loop 0 output; the initial draft>
Drafts 2: <final step loop 0 output; first refinement>
Drafts 3: <final step loop 1 output; second refinement>

Node Number: 1

[System Instruction]
Produce the final refined draft.
```

Each loop, the `text` field stays the same (input.context doesn't change). The `multi_ingest` field grows by one entry. The `nodeInfo` field shows the current node. The system prompt stays fixed.

## Full Example

A step using all five data-carrying field types together (the seven total include `relikRead` and `manifest`, documented in Memory and Dynamic Steps):

```yaml
fields:
  - name: Query
    type: text
    from: input.context
  - name: Analysis
    type: ingest
    from:
      stepId: analyze
      loopRef: current
  - name: Previous Drafts
    type: multi_ingest
    from:
      - stepId: step0
        loopRef: 0
      - stepId: final
        loopRef: accumulate
  - name: Node Number
    type: nodeInfo
  - name: Branch Count
    type: knobInfo
    from: coverage
```

This produces the following prompt (assuming the ladder is on loop 2, node 3 of 5, with coverage set to 5):

```text
Query: What is the best approach to quantum error correction?

Analysis: Surface codes and color codes are the two dominant families...

Previous Drafts 1: <step0 loop 0 output; the initial draft>
Previous Drafts 2: <final step loop 0 output>
Previous Drafts 3: <final step loop 1 output>

Node Number: 3

Branch Count: 5

[System Instruction]
Evaluate and improve the previous drafts.
```

Each field type contributes its line. The `multi_ingest` field produces three numbered entries because loop 0 is fixed and the accumulate covers loops 0 and 1. The system prompt closes it out.

## The manifest field type

The `manifest` field injects a **runtime snapshot** of everything readable at
the current step — all prior step outputs (with node counts and survivor
counts), all declared reliks (with file indexes), all available input keys,
all resolved knob values, and the current time.

```yaml
- name: Available
  type: manifest
```

No `from:` property is needed. The engine builds the manifest automatically
at field resolution time. The resolved value is a JSON object that the LLM
sees rendered as:

```text
Available: {"inputs":["input.context","input.systemTime"],"knobs":{"branches":5},"steps":[{"id":"draft","loop":0,"nodes":1,"survivors":1}],"reliks":[]}
```

The manifest is primarily used by **dynamic step planners** — a step whose
job is to generate a config for the next step. The planner reads the manifest
to know what sources it can reference in the generated config. See
[Dynamic Steps](./dynamic-steps.md) for the full pattern.

**Internal keys filtered out.** The manifest excludes reserved engine-internal
keys from the inputs list: anything starting with `__`, plus `messages`,
`model`, `author`, `template`, `debugMode`, and `input.handoff*` lineage
metadata. Only author-relevant keys surface to the planner.

## Clone references

A step can clone another step's field list instead of re-declaring it:

```yaml
- id: draft
  fields:
    - { name: Question, type: text, from: input.context }
  systemPrompt: "Write a draft."

- id: revise
  fields: "clone:draft"          # inherits draft's field list
  systemPrompt: "Revise the draft."
```

The clone target must exist in the config. This is shorthand for copy-paste;
the cloned step gets its own field resolution at runtime (the values differ
because step outputs differ), but the field declarations are identical.

## Common Validation Failures

Using `loopRef: "accumulate"` or `nodeRef: "accumulate"` on an `ingest` field; these produce arrays and only work on `multi_ingest`. Giving a `text` field an object `from` instead of a string path. Giving `nodeInfo` a `from` property; it doesn't take one. Referencing a `stepId` in `ingest` or `multi_ingest` that doesn't exist in the config. Referencing a knob name in `knobInfo.from` that isn't defined in the `knobs` section.

## Fallback sources

Three field types (`text`, `ingest`, `relikRead`) accept an optional `fallback:` declaration. The fallback is consulted when the primary source *misses* — meaning it returns an empty value, or it raises a source-missing error (the referenced step didn't run, the input key isn't set, the relik file is empty). Other errors propagate; `fallback:` is not a catch-all.

```yaml
fields:
  - name: Candidate
    type: ingest
    from: { stepId: generate }
    fallback: input.context        # used if generate hasn't run yet (e.g. mid-ladder entry)
  - name: PriorArt
    type: relikRead
    from: { relik: "Memory", folder: trace, file: verified.md }
    fallback: "no prior art yet"   # used before any verified entries are written
```

The fallback uses the unified `from:` vocabulary — the same shapes accepted by `relikWrite.from:`, gated write `from:`, and dynamic step `from:`:

| Form | Meaning |
|---|---|
| `previous` | All survivors of the immediately preceding step. |
| `output` | The current step's own output. |
| `"input.X"` | A key from the runtime inputs map (e.g. `input.context`, `input.systemTime`, `input.localTime`). |
| `"literal text"` | A literal string. |
| `{ stepId: "X" }` | Output of a specific prior step. |
| `{ relik: "...", folder: "...", file: "..." }` | Relik file contents or metatag. |

**Resolution priority** (lowest to highest):

1. `fallback:` — last resort, only consulted when primary misses.
2. `from:` — declared primary source.
3. `passField` override — caller-supplied value via `passFields` on a jump.

Fallback is purely additive. Fields without a declared `fallback:` behave exactly as before — empty result stays empty, source-missing errors surface as before. Fallback is opt-in.

**Auto-injected input keys** every ladder can rely on as primary or fallback sources:

| Key | Meaning |
|---|---|
| `input.context` | The firing step's combined output on a cross-ladder jump; the user's query on a direct API call. Always present. |
| `input.systemTime` | Current time at instance creation, ISO 8601 UTC. Always present. |
| `input.localTime` | Current time in the caller's declared timezone, ISO 8601 with offset. Present only if the API caller passed a `timezone` field in the request body. |
| `input.timezone` | The IANA timezone name the caller declared (e.g. `"America/New_York"`). Present only if the API caller passed a `timezone` field. |

The system/local time pair lets authors build time-aware ladders without any grammar additions:

```yaml
- id: pace_check
  fields:
    - { name: Now, type: text, from: input.systemTime }
  systemPrompt: "Current time: {{Now}}. If we've been working more than 30 minutes, output STOP. Else output CONTINUE."
  if: { equals: "STOP", then: { jump: { stepId: finalize } }, else: continue }
```

The LLM does the time arithmetic. The engine just exposes the data.

