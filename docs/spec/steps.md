# Steps

A step is the unit of LLM execution. It resolves its declared fields into concrete values, assembles a prompt, dispatches one or more LLM calls, stores the outputs as numbered nodes, optionally runs adaptive tool loops (code, read, edit), optionally writes to memory, evaluates a gate that prunes nodes, and chooses the next action. The runtime walks the steps array sequentially; jumps reposition the cursor; loops repeat the entire walk.

## Why steps exist

A ladder without steps is just a prompt template. The step is the smallest unit where the runtime gets to act between LLM calls: resolve fields, store output, apply gates, choose what to do next. Every power axis in LADR attaches to a step.

A step is where the author declares:

- **What data flows in** (`fields:`)
- **What instruction the model gets** (`systemPrompt:`)
- **How many parallel calls to make** (`nodes:`)
- **What gate to evaluate against the output** (`if:`)
- **What to do next** (the gate's `then:` / `else:` actions)
- **What to persist** (`relikWrite:`)
- **What to fetch on demand** (`relikRead:` — adaptive read loop)
- **Whether to recurse** (`recursion:`)
- **Whether to load its own config at runtime** (`dynamic:`)

Every one of those is optional except `id` and `name`. The minimum step is just an id, a name, and a system prompt.

## Step anatomy

A step in YAML, every optional field shown for reference:

```yaml
- id: draft                    # required - unique step ID used by jumps and ingest references
  name: Draft                  # required - human-readable label shown in the timeline
  type: normal                 # normal | sequential | group (default: normal)
  nodes: 5                     # fan-out count (number | knob name | { from: knob name })
  fields:                      # prompt inputs (see Fields)
    - { name: Question, type: text, from: input.context }
  systemPrompt: "..."          # system-role instruction
  if:                          # typed gate (see Control Flow)
    integerRange: [4, 5]
    then: continue
    else: { jump: { stepId: refine } }
  continueIf: "READY"          # deprecated shorthand for if: { equals: "READY" }
  timeline: circle             # UI hint: circle (default for steps with nodes) | init
  recursion:                   # spawn a child ladder (see Recursion)
    maxDepth: 3
  checkpoint:                  # trace section labels
    heading: "Drafting"
    nodeHeading: "Candidate"
  relikWrite: { ... }          # schema-matched write (see Reads and Writes)
  codeExecution: { ... }       # adaptive code loop (see Adaptive keys)
  relikRead: { ... }           # adaptive read loop (see Adaptive keys)
  relikEdit: { ... }           # adaptive edit loop (see Adaptive keys)
  dynamic: false               # load config at runtime (see Dynamic Steps)
  from: previous               # source for dynamic config (when dynamic: true)
  onInvalid: abort             # fallback action when dynamic config fails validation
```

The runtime never silently ignores an unknown key. If you mistype a field name, validation fails with the offending key in the error message.

## The three step types

Every step has a `type` that controls how its nodes (the LLM calls inside the step) are dispatched.

| Type | How nodes run | When to use |
|---|---|---|
| `normal` (default) | All N nodes dispatched in parallel. Each node receives the same assembled prompt, except for `nodeInfo` which injects the node's own number. | Fan-out for Tree-of-Thoughts, multi-perspective debate, self-consistency voting, multi-verifier gates. The default for any step that benefits from independent attempts. |
| `sequential` | Nodes dispatched in order, one after the other. Node K can read outputs of nodes 1 through K-1 via `nodeRef: previous`. | Chain-of-thought within one step, iterative self-refinement where each call refines the previous one's output, any flow where later calls in the same step should build on earlier calls. |
| `group` | Has no `nodes:`, `fields:`, or `systemPrompt:` of its own. Instead has a `steps:` array of child steps that run as parallel siblings. | Independent sub-strategies that feed a shared downstream step. Three reviewers running side by side, then a synthesis step merging their outputs. |

When in doubt, use `normal`. Use `sequential` only when later nodes in the same step must consume earlier nodes' outputs. Use `group` only when two or more independent sub-strategies must run side by side and feed a shared downstream step.

```yaml
# normal: parallel fan-out
- id: generate
  type: normal
  nodes: 5
  systemPrompt: "Generate an independent candidate."

# sequential: in-order chain
- id: refine
  type: sequential
  nodes: 3
  fields:
    - name: Previous
      type: ingest
      from: { stepId: refine, loopRef: current, nodeRef: previous }
      skipFirstNode: true
  systemPrompt: "Refine the previous output."

# group: parallel siblings under one parent
- id: multi_review
  type: group
  steps:
    - { id: security, name: Security, systemPrompt: "Review for security issues." }
    - { id: style,     name: Style,     systemPrompt: "Review for style issues." }
    - { id: correctness, name: Correctness, systemPrompt: "Review for correctness." }

- id: synthesize
  fields:
    - { name: Security,    type: ingest, from: { stepId: security, loopRef: current } }
    - { name: Style,       type: ingest, from: { stepId: style, loopRef: current } }
    - { name: Correctness, type: ingest, from: { stepId: correctness, loopRef: current } }
  systemPrompt: "Merge the three reviews into one report."
```

Hard rules enforced at validation:

- A `group` must have at least 2 child steps.
- Groups cannot be nested.
- Group children cannot reference each other with `loopRef: current` (parallel steps cannot read from each other).

## Nodes: parallel fan-out

The `nodes:` field controls how many LLM calls the step makes. Four forms are
accepted:

```yaml
nodes: 5                            # literal number
nodes: branches                     # bare knob name (resolved at call time)
nodes: "{{generate.output}}"        # step output parsed as integer
nodes: { from: { stepId: gated_step, pruned: true } }  # object form
```

**Literal.** A plain integer. The simplest form.

**Knob reference.** A bare string matching a knob name. At call time, the
knob's resolved value is used. The knob must be declared in the `knobs:`
section. The legacy `{{knobs.X}}` interpolation form also works but is
deprecated — prefer the bare name.

**Step output reference.** The `{{stepId.output}}` form reads a prior step's
output, parses it as a strict integer, and uses that count. The output must
be a clean integer (trailing `.0` tolerated; prose is not). If parsing fails,
the node count resolves to 0 and the step is a no-op. A `node_count_resolved`
event is published with the raw output, normalized output, and accept/reject
reason.

**Object form.** The `{ from: NodesFrom }` object resolves the node count
from a prior step's state. Supports `pruned: true` to inherit survivor count
after gate pruning.

When the step runs, the runtime resolves `nodes` to an integer, clamps it to
the platform's `MaxNodesPerStep` limit (default 20), then dispatches that many
LLM calls. If the requested count exceeds the limit, the runtime clamps
silently and publishes a `node_count_resolved` event with `clamped: true`.

**Parallel batching.** For `normal` steps with more than 20 nodes, the engine
dispatches in batches of 20 to avoid overwhelming the provider. Each batch
completes before the next starts. For `sequential` steps, nodes run strictly
in order regardless of count.

Each call's output is stored as a **numbered node** belonging to that step.
Node 1 is the first call's output, node 2 the second's, and so on. Downstream
steps address specific nodes via the `nodeRef` vocabulary.

| `nodeRef` | Resolves to |
|---|---|
| `current` | The node whose index matches the consuming step's node index. Enables per-node routing: each node in the consumer reads the corresponding node in the producer. |
| `previous` | The node at index N-1 of the source step. Used in sequential flows where node K reads node K-1. |
| `accumulate` | All nodes of the source step, joined by `\\n\\n` for text content or as a JSON array for JSON-typed metatags. Only valid on `multi_ingest`. |

When `nodes:` is omitted entirely, the step runs as a single-node call. `nodeInfo` fields still inject node number 1.

```yaml
# A 5-node generate step. Each node produces an independent candidate.
- id: generate
  nodes: 5
  fields:
    - { name: Question, type: text, from: input.context }
    - { name: NodeNumber, type: nodeInfo }
    systemPrompt: "Generate an independent candidate. Use the Node Number field above to vary your approach."

# A downstream step that picks up node 3 specifically
- id: tiebreaker
  fields:
    - { name: Specific, type: ingest, from: { stepId: generate, loopRef: current, nodeRef: { node: 3 } } }
```

**Inheriting survivor counts.** When a prior step used a gate to prune nodes, a downstream step can inherit its node count from the survivors:

```yaml
- id: process_survivors
  nodes: { from: { stepId: gated_step, pruned: true } }
```

The downstream step's node count dynamically matches however many nodes survived the gate. Useful for "process each surviving candidate individually."

## Field resolution and prompt assembly

Before any LLM call is dispatched, every declared field is resolved to a concrete value. Fields of different types resolve from different sources:

| Field type | Resolution source |
|---|---|
| `text` | Reads the named key from the runtime input map (typically `input.context` for the last user message). |
| `ingest` | Reads one node's output from a prior step, addressed by `stepId` + `loopRef` + `nodeRef`. |
| `multi_ingest` | Reads multiple node outputs and numbers them sequentially. |
| `relikRead` | Reads file contents or a metatag value from a declared relik. |
| `knobInfo` | Reads the resolved value of a named knob. |
| `nodeInfo` | Injects the current node's number (1-indexed). |
| `manifest` | Returns a JSON snapshot of every source available at this step (used by dynamic-step planners). |

Once all fields are resolved, the runtime assembles the user-role message. Each field renders as one line:

```
FieldName: resolved value
```

For fields that resolve to multiple values (`multi_ingest`, or `relikRead` of a folder-level metatag), the lines are auto-numbered:

```
FieldName 1: first value
FieldName 2: second value
FieldName 3: third value
```

After all field lines, the runtime appends the system instruction:

```
<field lines>

[System Instruction]
<systemPrompt value>
```

The field lines go in the user role. The system prompt goes in the system role. Both are sent in a single `chat/completions` call to the provider.

For multi-node steps, each node receives an identical assembled prompt except for the `nodeInfo` value. The LLM has no way to know which node it is unless the prompt instructs it to use the injected `Node Number`.

## What happens when a step runs

Per step, the runtime executes this sequence in order. Every phase is observable via lifecycle events (see Observability).

1. **Hop +1.** The runtime increments its hop counter and checks it against the ceiling. If exceeded, the ladder exits gracefully with the best-so-far output. Every step costs exactly 1 hop regardless of node count.

2. **Resolve fields.** All declared fields are resolved concurrently. Each field type has its own resolution path (see Fields). If any field fails to resolve, the step fails.

3. **Dispatch LLM calls.**
   - `normal`: all nodes dispatched in parallel.
   - `sequential`: nodes dispatched in order, node K can read nodes 1..K-1.
   - `group`: not applicable (groups have no nodes of their own; their children are steps).
   
   Each call increments the LLM-call counter and adds its token cost to the spend counter. Each call's output is stored as a numbered node.

4. **Apply schema-matched relik write** (if `relikWrite:` is declared). The runtime extracts the first JSON block from the combined output, validates against the declared schema, and writes valid matches to the target relik. No-match is a no-op, not an error.

5. **Run adaptive loop** (if any adaptive key is declared: `codeExecution:`, `relikRead:`, `relikEdit:`). Up to `maxRounds` of call-inject-rerun. Guaranteed to terminate.

6. **Evaluate the gate** (if `if:` or `continueIf:` is declared).
   - For multi-node steps, the condition is checked **per node**. Non-matching nodes are pruned; their outputs are no longer visible to downstream consumers.
   - After pruning: if any survivors remain, the `then:` action fires. If zero survivors, the `else:` action fires.

7. **Apply the action.**
   - `continue` advances the cursor to the next step.
   - `jump` within the ladder repositions the cursor to the named step.
   - `jump` cross-ladder terminates this ladder and hands off to another.
   - `abort` terminates the ladder.

8. **Recurse** (if `recursion:` is declared and depth limit not reached). The runtime spawns a child instance of the same ladder. The child's `input.context` is this step's output. The parent pauses at this step; when the child completes, the child's exit output replaces this step's stored output.

9. **Step complete.** The runtime publishes a `step_complete` event and moves to the next step (or the jump target).

Per step, cost scales as you would expect: hop cost is 1; LLM-call cost is `nodes`; spend is the sum of token costs across all calls.

## Loops: multi-pass execution

The runtime's main loop iterates the steps array up to `maxLoops` times. Each iteration is called a **loop** and is identified by a 0-indexed `loopIndex`.

```
Loop 0
  Step draft       <- reads input.context, produces draft output
  Step evaluate    <- reads draft output, scores it
  Step refine      <- reads draft + evaluation, produces refined output
Loop 1
  Step draft       <- can read loop 0's refine output via loopRef: previous
  Step evaluate
  Step refine
...
```

Steps read outputs from prior loops via the `loopRef` field on `ingest` and `multi_ingest`:

| `loopRef` | Resolves to |
|---|---|
| `current` | The same loop iteration that's currently executing. The default for cross-step references within one loop. |
| `previous` | The loop immediately before the current one. At loop 0 this resolves to nothing; only meaningful from loop 1 onward. |
| `accumulate` | Collects outputs from all previous loops. At loop 2, this reads loops 0 and 1. Only valid on `multi_ingest`. |
| A number (e.g. `0`) | A specific loop index. Always reads that exact iteration regardless of which loop is currently running. |

Loops compose with jumps. A backward jump inside the steps array creates an inner loop bounded by the hop ceiling. The outer `maxLoops` counter limits the total number of full passes; the hop ceiling limits the total number of step executions across all loops and jumps.

`maxLoops` is bounded by the runtime's resource budget (default 50). Callers can request fewer loops via the API request body. If the caller asks for more than the budget allows, the runtime clamps silently and publishes a `node_count_resolved` event with `clamped: true`.

## Recursion: spawning child instances

A step with a `recursion:` block can spawn a child execution of the same ladder. After the step's own execution completes, the runtime checks whether the current depth is below `maxDepth`. If so, a child instance is created with the step's output replacing `input.context`. The parent pauses at this step; the child runs the full ladder from step 0; when the child completes, its exit output replaces the parent's recursion-step output.

```yaml
- id: divide
  fields:
    - { name: Problem, type: text, from: input.context }
  systemPrompt: "Decompose the problem into a smaller sub-problem."
  recursion:
    maxDepth: 3              # number or knob name of type: recursion
```

Key semantics:

- Only **one** step per ladder can have a `recursion:` block. Two recursion steps fail validation.
- `maxDepth` accepts a literal number or a knob name (which must be `type: recursion`).
- The child always runs with `maxLoops: 1`. No looping in the child.
- Knob values are inherited from the parent.
- Each child level counts as 1 hop in the parent's hop budget, plus whatever hops the child itself uses.
- A recursive ladder that hits the recursion ceiling exits gracefully with the best-so-far output.

See Language > Recursion for the full reference, including how recursion composes with loops, what the child sees, and worked depth-by-depth walkthroughs.

## The exit step

The ladder's `exit:` field names the step whose output is returned to the caller. After the main loop completes, the runtime reads the exit step's output from the final loop iteration and returns it as the `content` field of the OpenAI-compatible response.

Validation rules:

- The named step must exist in `steps:`.
- If the exit step has multiple `nodes:`, it must be `type: sequential`. A `normal` step with multiple parallel nodes has no deterministic "last" node, so it cannot be the exit.
- The exit step can be the same step that is also the target of a jump.

For `sequential` exit steps with multiple nodes, the runtime returns the output of the last surviving node (after gate pruning). For single-node exit steps (the common case), the single output is returned directly.

```yaml
exit: answer

steps:
  - id: draft
    # ...
  - id: answer
    # single-node exit step: its output is returned to the caller
    fields:
      - { name: Refined, type: ingest, from: { stepId: draft, loopRef: current } }
    systemPrompt: "Return the final answer in the response format."
```

## Timeline annotations

The optional `timeline:` field is a UI hint for Foundry. Two values are accepted:

| Value | Effect |
|---|---|
| `circle` (default for steps with `nodes:`) | Renders the step as a circle in the timeline. One circle per node. |
| `init` | Renders as the timeline's leftmost node. At most one step per ladder can carry this. Cannot have `nodes:`. Cannot be the same as the exit step. |

The `init` marker is purely visual; it does not change execution order. The runtime still walks the steps array in declaration order.

```yaml
steps:
  - id: setup
    name: Setup
    timeline: init
    systemPrompt: "Initialize context."
  - id: generate
    name: Generate
    nodes: 5
    timeline: circle
    systemPrompt: "Generate candidate."
```

In this example, Foundry renders a square `Setup` node on the left, followed by five circle nodes for `Generate`. The setup step still runs first because it appears first in the array, regardless of the timeline hint.

## Checkpoint labels

The optional `checkpoint:` block adds text labels to the trace output. Two fields:

```yaml
- id: draft
  checkpoint:
    heading: "Drafting phase"
    nodeHeading: "Candidate"
```

- `heading` labels the step's section in the trace.
- `nodeHeading` labels each individual node within the step.

These are purely display hints. They do not affect execution. Useful for long ladders where the trace would otherwise be a wall of step IDs.

## continueIf: the legacy gate

`continueIf:` is the older form of step gate. It takes a bare string and tests for exact equality against the step output:

```yaml
- id: classify
  continueIf: "READY"
  systemPrompt: "Output READY if done, otherwise output anything else."
```

This desugars at parse time to:

```yaml
if: { equals: "READY", then: continue, else: abort }
```

Hard rules:

- `continueIf` and `if` are **mutually exclusive**. Declaring both fails validation.
- `continueIf` has no effect on a single-node step (a step with no `nodes:`). Validation warns.
- Existing published ladders using `continueIf` continue to work unchanged. New ladders should prefer the typed `if:` form; it covers every case `continueIf` does, plus richer conditions and actions.

## Dynamic steps

A step with `dynamic: true` does not declare its own `systemPrompt`, `fields`, or `nodes`. Instead, it loads them at runtime from a declared source (usually the previous step's output). Combined with a `type: manifest` field on the planner step, this lets the ladder rewrite parts of itself based on observed state.

```yaml
- id: plan_next
  fields:
    - { name: Available, type: manifest }
    - { name: Context, type: text, from: input.context }
  systemPrompt: |
    Based on Available sources, generate the next step.
    Emit JSON: {"systemPrompt": "...", "fields": [...], "nodes": N}

- id: run_planned
  dynamic: true
  from: previous              # uses plan_next's output directly as config
```

The loaded config is validated at runtime: must be a well-formed step config, every field reference must resolve against the current manifest, no nested `dynamic: true`, node count within platform cap. On validation failure, the `onInvalid:` action fires (default `abort`).

See Language > Dynamic Steps for the full reference.

## Step validation rules

Step-level rules checked at config load time:

- Every step must have a unique `id`.
- Every step must have a `name`.
- `type: group` cannot be nested inside another group.
- `continueIf` and `if` cannot both be declared on the same step.
- `if.integerRange` and `if.numberRange` must have `min <= max`.
- `if.in` must have at least one value.
- `if.jsonMatches` must be a valid JSON Schema.
- A step's `fields` cannot reference forward-declared steps with `loopRef: current` (referenced step must have already executed).
- A non-sequential step cannot self-reference with `loopRef: current`.
- Group children cannot reference each other with `loopRef: current` (parallel steps cannot read from each other).
- `nodes:` resolving to zero or negative fails.
- `recursion.maxDepth` must be a positive integer or a knob of `type: recursion`.
- Only one step per ladder can have a `recursion:` block.
- The exit step must exist and (if it has nodes) must be `type: sequential`.
- At most one step can have `timeline: init`. It cannot have `nodes:` and cannot be the exit step.

Validation is non-fatal in aggregate: the validator collects every error and returns them all at once. The config is rejected only if at least one error is found. Every error includes the step's `id` so the author can locate the problem in the config.

