# Control Flow: if / then / else

The if/then/else block is LADR's typed conditional. After a step's nodes complete, the gate evaluates a condition against each node's output, prunes the non-matching nodes, then fires a control-flow action (continue, jump, or abort) based on whether any survivors remain. This is the primitive that makes ladders adaptive: same config, different behavior per call based on what the model actually produced.

**Gates are control-flow only.** They decide *where execution goes* — continue to the next step, jump to a different step, or abort. Data operations (reading from reliks, writing to reliks) are **step-level capabilities**, not gate actions. A step that needs to read declares ingest fields or `relikRead:`. A step that needs to write declares `relikWrite:` or `relikEdit:`. The gate just routes execution to (or away from) steps that have those capabilities.

## Why gates exist

Without gates, every step's output is used unconditionally. A 5-node generate step produces 5 candidates, and all 5 flow into the next step. There's no way to prune the bad ones, no way to branch based on what was generated, no way to abort early on a clearly-wrong answer.

That covers simple cases (one-shot answer, refine, return) but misses the entire class of adaptive reasoning:

- **Tree-of-Thoughts.** Generate many candidates; keep only the ones that pass a verifier; drop the rest before they waste downstream cost.
- **Reflexion.** Generate → verify → if verification failed, jump back and try again with feedback.
- **Early exit.** If the verifier is highly confident, skip the rest of the pipeline and synthesize immediately.
- **Conditional persistence.** If the output is worth saving, jump to a step that declares `relikWrite:`; otherwise skip that step. The write lives on the target step as a capability, not on the gate as an action.

Gates enable all of these. The gate is a typed predicate evaluated against a step's output, paired with actions for what to do on match and on miss. The runtime prunes non-matching nodes per-node first, then fires the step-level action based on whether any survivors remain.

The typed form (`if: { integerRange: [4,5] }`) is more powerful than the legacy string form (`continueIf: "READY"`) because it handles numeric ranges, substrings, set membership, and JSON Schema validation. Existing ladders using `continueIf` continue to work unchanged; new ladders should prefer `if:`.

## Anatomy of an if block

Every `if:` block has three parts:

1. **A condition.** One of twelve typed predicates (nine leaf + three compound), declared as a single-key object.
2. **A `then` action.** What to do when the condition passes (or when any survivors remain). Default: `continue`.
3. **An `else` action.** What to do when the condition fails (or when no survivors remain). Default: `abort`.

```yaml
- id: classify
  systemPrompt: "Output a single integer rating from 1 to 5."
  if:
    integerEquals: 5
    then: continue                              # default; may be omitted
    else: { jump: { stepId: refine } }          # default is abort; override here
```

Both `then` and `else` accept any of three control-flow actions (continue, abort, jump). Defaults: `then: continue` and `else: abort`. Omit `then:` to accept the default; omit `else:` to accept abort-on-failure.

The shorthand form is a bare string, equivalent to `equals` with default actions:

```yaml
if: "READY"
# desugars to:
# if: { equals: "READY", then: continue, else: abort }
```

This is exactly the legacy `continueIf` behavior. Existing ladders using `continueIf` work unchanged; the desugaring happens at parse time.

## Condition keys

Twelve typed predicates are supported. Each is a single-key object; the key names the predicate, the value is its argument.

### Leaf conditions

| Key | Argument | What it checks |
|---|---|---|
| `equals` | string | Exact string match against the trimmed output. |
| `integerEquals` | integer | Output parsed as integer, compared equal. Tolerates trailing `.0`. |
| `integerRange` | `[a, b]` | Output parsed as integer, must fall within inclusive `[a, b]`. |
| `numberEquals` | number | Output parsed as float, compared equal (epsilon tolerance). |
| `numberRange` | `[a, b]` | Output parsed as float, within inclusive range. |
| `contains` | string | Substring match. More forgiving than `equals` — handles prose around the marker. |
| `in` | `[a, b, c, ...]` | Output is one of the declared values. Handles LLM phrasing variance (`"yes"`, `"Y"`, `"accept"`). |
| `jsonMatches` | JSON Schema | First JSON block extracted from output, validated against the schema. The structured gate. |
| `matches` | string (RE2 regex) | Regex search (partial match, not full-match). The output is searched for the pattern. |

### Compound conditions

| Key | Argument | What it checks |
|---|---|---|
| `and` | `[condition, condition, ...]` | Every sub-condition must pass. |
| `or` | `[condition, condition, ...]` | At least one sub-condition must pass. |
| `not` | condition | Inverts the single sub-condition. |

Compound conditions compose recursively — `and`, `or`, and `not` can contain other compound conditions, making `Condition` a tree. This lets you express complex gates:

```yaml
# String equality
if: { equals: "READY" }

# Integer comparison (parsing-tolerant)
if: { integerEquals: 5 }
if: { integerRange: [4, 5] }

# Floating-point comparison
if: { numberEquals: 0.95 }
if: { numberRange: [0.7, 1.0] }

# Substring (handles prose around the marker)
if: { contains: "VERDICT: ACCEPT" }

# Regex search (partial match)
if: { matches: "\\d{3}-\\d{4}" }

# Set membership (handles phrasing variance)
if: { in: ["yes", "Y", "accept", "ACCEPT"] }

# Structured (extract JSON, validate schema)
if:
  jsonMatches:
    type: object
    required: [verdict, score]
    properties:
      verdict: { type: string, enum: [accept] }
      score: { type: number, minimum: 4 }

# Compound: all must pass
if:
  and:
    - { contains: "READY" }
    - { or: [{ equals: "READY" }, { contains: "GO" }] }
    - { not: { contains: "BLOCKED" } }
```

**Parsing tolerance.** The integer and number predicates parse the output
leniently. The output is whitespace-trimmed, then parsed. An output of
`"5"`, `"5.0"`, or `"  5  "` all parse to the integer 5. However, prose
like `"Score: 5"` or `"The answer is 5"` will **not** parse — the entire
trimmed output must be the number (optionally with a trailing `.0`). This
is deliberately strict: if you need to extract a number from prose, use
`jsonMatches` with a schema, or instruct the LLM to output the number alone.

**Use the most specific predicate that works.** `equals` is brittle (one whitespace difference fails). `contains` is more forgiving. `integerRange` / `numberRange` are best for scored outputs. `jsonMatches` is best for structured outputs. `in` is best when the LLM might phrase the same answer multiple ways.

## jsonMatches: the structured gate

`jsonMatches` is the most powerful condition. It extracts the first JSON block from the output, parses it, and validates against a declared JSON Schema.

**JSON extraction rules** (shared with `relikWrite.match`):

1. Strict JSON parse first. If the entire output is valid JSON, use it.
2. If strict parse fails, look for a JSON code fence (`\`\`\`json ... \`\`\``) and extract its contents.
3. If no code fence, look for the first balanced `{...}` or `[...]` block (handles prose-embedded JSON).
4. If no JSON found, the gate fails (no error; just `false`).

**Validation.** The extracted JSON is validated against the schema. Common schema constraints:

- `required: [field1, field2]` — listed fields must be present.
- `properties: { ... }` — per-field type and value constraints.
- `enum: [a, b, c]` — field must be one of the listed values.
- `minimum` / `maximum` — numeric bounds.
- `minLength` / `maxLength` — string length bounds.
- `additionalProperties: false` — reject unknown fields (default behavior).

```yaml
if:
  jsonMatches:
    type: object
    required: [verdict, score]
    properties:
      verdict: { type: string, enum: [accept] }
      score: { type: number, minimum: 4, maximum: 5 }
    additionalProperties: false
  then: continue
  else: { jump: { stepId: reflect } }
```

This gate accepts only outputs where the JSON has `verdict: "accept"` and `score` between 4 and 5, with no other fields. Anything else (missing fields, wrong enum value, extra fields) fails the gate.

**Why this matters.** Without `jsonMatches`, structured gating requires fragile string parsing. With it, the runtime handles JSON extraction and validation; the author declares the schema. The same JSON Schema engine powers `relikWrite.match` — one implementation, two surfaces (verify-and-branch via `if.jsonMatches`; verify-and-persist via `relikWrite.match`).

## The three control-flow actions

Under `then:` and `else:`, three actions are accepted. These are **control-flow only** — they decide where the cursor goes next, nothing else. Data operations (relik reads/writes) are step-level capabilities, declared on the target step, not inside the gate.

### `continue`

Advance the cursor to the next step. The default for `then:`.

```yaml
then: continue
```

### `abort`

Terminate the ladder. The exit step's output from the most recent completed loop is returned to the caller. The default for `else:`.

```yaml
else: abort
```

### `{ jump: ... }`

Reposition the cursor. Within-ladder jumps take a `stepId`; cross-ladder jumps take a `ladderId` (and optionally a `stepId`). See Jumps for the full reference.

```yaml
then: { jump: { stepId: refine } }                                   # within-ladder
else: { jump: { ladderId: "@alice/helper" } }                       # cross-ladder
else: { jump: { ladderId: "@alice/helper", stepId: "step3" } }      # cross-ladder mid-entry
```

### Conditional persistence pattern

Gates are control-flow only — they can't write to reliks directly. To persist conditionally,
jump to a step that declares a write capability (`relikWrite:` or `relikEdit:`):

```yaml
# Gate controls flow; the target step owns the data operation.
then: { jump: { stepId: persist } }

# ...elsewhere in the ladder:
- id: persist
  relikWrite:
    to: { relik: "Memory", folder: trace, file: verified.md }
    field: fact
    on: append
    match: { type: object, required: [fact], properties: { fact: { type: string } } }
```

This separation keeps gates purely about routing. The target step can declare schema-matched
`relikWrite:` or `relikEdit:`. The gate doesn't need to know about reliks.

## How the gate evaluates

The evaluation has two phases, implemented in `DispatchGate`.

### Phase 1: Per-node pruning

For multi-node steps, the gate condition is checked against **every node's
output individually**. Non-matching nodes are pruned — their outputs are
removed from the pruned node map and are no longer visible to downstream
`ingest` or `multi_ingest` references.

```
5 nodes execute in parallel
  → Node 1: {"score": 5, "verdict": "accept"}   ✓ survives
  → Node 2: {"score": 2, "verdict": "reject"}   ✗ pruned
  → Node 3: {"score": 4, "verdict": "accept"}   ✓ survives
  → Node 4: {"score": 1, "verdict": "reject"}   ✗ pruned
  → Node 5: {"score": 5, "verdict": "accept"}   ✓ survives

Survivors: [1, 3, 5]
Pruned node map updated: step "verify" → survivors [1, 3, 5]
```

For single-node steps, the same logic applies with one node: if it passes,
it survives; if it fails, it's pruned.

### Phase 2: Step-level action

After pruning, the engine checks whether any survivors remain:

- **Survivors exist** → fire the `then:` action.
- **No survivors** → fire the `else:` action.

The `else:` action carries a `BestOutput` — the first node's output from the
pruned set. This is used for graceful degradation: if a ceiling is hit during
the `else` branch, the caller receives the best output rather than nothing.

### Interaction with the pruned node map

After gate evaluation, the pruned node map is updated for the current step.
Downstream steps that reference this step via `{ stepId: verify, loopRef:
current }` will see only the survivors, not the original node set. The
`nodeRef: accumulate` modifier reads all surviving nodes across the
specified loop iterations.

```yaml
- id: verify
  nodes: 3
  systemPrompt: 'Score 1-5. Output JSON {"score": N, "verdict": "accept|reject"}.'
  if:
    jsonMatches:
      type: object
      properties:
        score: { type: number, minimum: 4 }
        verdict: { type: string, enum: [accept] }
    then: continue
    else: { jump: { stepId: persist_failure } }
```

In this example:

- Each of the 3 verifier nodes produces JSON.
- Per-node pruning keeps verifiers that emitted `{score: >=4, verdict: "accept"}`;
  prunes the rest.
- If any survivors remain, `then: continue` fires — the step advances.
- If all nodes are pruned, `else:` fires — the gate jumps to `persist_failure`,
  a step that declares `relikWrite:` to record the failing verifiers' analysis
  as a lesson.

The composed behavior: verifiers that pass survive; verifiers that fail route
to a persist step. Pruning becomes productive signal — every pruned node teaches
the ladder something for next time.

## Shorthand and continueIf

Two shorthand forms exist for backward compatibility.

**`if: "X"`** desugars to `if: { equals: "X", then: continue, else: abort }`:

```yaml
if: "READY"
# identical to:
if: { equals: "READY" }
```

**`continueIf: "X"`** is the legacy form. It desugars to the same thing:

```yaml
continueIf: "READY"
# identical to:
if: { equals: "READY", then: continue, else: abort }
```

Hard rules:

- `continueIf` and `if` are **mutually exclusive**. Declaring both fails validation.
- `continueIf` has no effect on a single-node step (a step with no `nodes:`). Validation warns.
- Existing published ladders using `continueIf` continue to work unmodified.

New ladders should always prefer the typed `if:` form. It covers every case `continueIf` does, plus richer conditions (ranges, substrings, sets, JSON Schemas) and richer actions (jumps and writes, not just continue/abort).

## Validation rules

Gate-related rules checked at config load time.

- **`if:` and `continueIf:` mutual exclusion.** Declaring both fails: `Step "X": cannot declare both "continueIf" (deprecated) and "if" — use only "if"`.
- **`integerRange` well-formed.** `min <= max` required. Inverted ranges fail: `Step "X": if.integerRange has min (5) > max (3)`.
- **`numberRange` well-formed.** Same rule.
- **`in` non-empty.** `if: { in: [] }` fails: `Step "X": if.in has empty array (must have at least 1 value)`.
- **`jsonMatches` valid JSON.** Malformed JSON fails: `Step "X": if.jsonMatches is not valid JSON: ...`.
- **`jsonMatches` non-empty.** Empty schema fails.
- **Jump targets.** `then: { jump: { stepId: X } }` requires step `X` to exist. Cross-ladder `ladderId` values must parse as valid identifiers.

Every error includes the step's `id` so the author can locate the problem in the config.

## Common patterns

**Single-threshold accept/reject.** The simplest gate: accept outputs scoring above a threshold, reject (and jump back) otherwise.

```yaml
- id: verify
  systemPrompt: 'Score 1-5. Output the integer alone.'
  if:
    integerRange: [4, 5]
    then: continue
    else: { jump: { stepId: refine } }
```

**Confidence-conditional early exit.** Use `jsonMatches` to extract a confidence field and skip ahead when it's high.

```yaml
- id: classify
  systemPrompt: 'Output JSON {"confidence": "high"|"medium"|"low"}.'
  if:
    jsonMatches:
      type: object
      properties:
        confidence: { type: string, enum: [high] }
    then: { jump: { stepId: synthesize } }
    else: continue                              # fall through to gather more
```

**Multi-verifier majority vote.** Run multiple verifiers; the gate naturally prunes dissenters.

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
    then: continue                              # only accepting verifiers survive
    else: abort
```

Downstream `multi_ingest: { nodeRef: accumulate }` reads all surviving (accepting) verifiers. Majority is implicit in the count of survivors.

**Phrasing-tolerant acceptance.** Use `in` to handle LLM phrasing variance.

```yaml
- id: classify
  systemPrompt: 'Output "yes" or "no".'
  if:
    in: ["yes", "Y", "YES", "accept", "ACCEPT", "true", "True"]
    then: continue
    else: abort
```

The gate accepts any reasonable affirmative phrasing without needing `jsonMatches`.

