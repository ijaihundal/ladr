# Language Overview

LADR is a YAML dialect for writing reasoning programs. A LADR file declares the structure of a multi-step deliberation: which calls to make, in what order, what data flows between them, what conditions branch, what jumps where, and what the ladder should remember next time. A runtime engine walks the declaration and executes it. This article is the 30-second tour; the rest of the Language section documents each primitive in depth.

## What a ladder is

A **ladder** is the atomic unit a user authors, runs, and publishes. Internally it is a YAML document with a fixed shape: a name, a target policy, a set of caller-tunable knobs, an ordered list of steps, and an exit step whose output is returned to the caller. Optionally: persistent memory stores (reliks), a resource budget, trace settings.

The smallest possible ladder is one step with one field:

```yaml
name: Echo
allowedTargets: { strategy: universal }
exit: answer
knobs: {}
steps:
  - id: answer
    name: Answer
    fields:
      - { name: Question, type: text, from: input.context }
    systemPrompt: "Answer the question."
```

Run this through the gateway and you get back the model's response in an OpenAI-compatible shape. It is functionally identical to calling the model directly. The point of LADR is what becomes possible when the ladder stops being one step.

A two-step draft-and-refine ladder already does something a single call cannot:

```yaml
name: Draft and Refine
allowedTargets: { strategy: universal }
exit: refine
knobs: {}
steps:
  - id: draft
    name: Draft
    fields:
      - { name: Question, type: text, from: input.context }
    systemPrompt: "Write a first draft answering the question."

  - id: refine
    name: Refine
    fields:
      - { name: Question, type: text, from: input.context }
      - { name: Draft, type: ingest, from: { stepId: draft, loopRef: current } }
    systemPrompt: "Improve the draft. Address weaknesses. Output the final answer."
```

The runtime executes `draft`, stores its output, then executes `refine` with the draft injected as a field. Two LLM calls. The second sees the first. That is the foundation everything else builds on.

## Why a declarative language for reasoning

A reasoning strategy is easier to analyze, share, and bound when its structure is explicit data rather than embedded in application code. LADR makes the structure of a multi-step deliberation declarative: the author states the steps, their data dependencies, and the control flow; a runtime engine assembles prompts, dispatches calls, evaluates gates, and tracks resource use.

The closest analogy is SQL. SQL is declarative — you describe the query and the database decides how to execute it; you do not write the join algorithm. LADR applies the same shape to inference-time reasoning: you describe the strategy and the runtime executes it. Like SQL, LADR is purpose-built for its domain rather than a general-purpose language.

What this buys:

- **Inspectability.** Every ladder is a YAML file. You can read it, diff it, version it, render it visually, lint it.
- **Analyzability.** The runtime knows what fields a step will read, what steps a jump can target, what reliks a write will hit. Validation catches mistakes before a single LLM call runs.
- **Publishability.** A ladder is data, not code. It can be listed in a directory, forked, priced, embedded as JSON-LD on a crawlable page.
- **Composability.** Cross-ladder jumps turn published ladders into building blocks. One ladder can hand off to another mid-execution.
- **Bounded cost.** Every execution runs under a hard spend cap. A published ladder cannot run up an unbounded bill, no matter how complex its logic.

## The atomic pieces

LADR has a strict hierarchy. Every article in this section is about one piece in this list.

| Piece | What it is | Where you author it |
|---|---|---|
| **Ladder** | A complete reasoning program. The whole YAML file. | root |
| **Step** | One execution unit. Resolves fields, makes LLM calls, evaluates a gate, chooses the next action. | under `steps:` |
| **Field** | One prompt input. Renders as `Name: value` in the assembled prompt. | under a step's `fields:` |
| **Node** | One LLM call within a step. `nodes: 5` fans out into 5 calls. | the step's `nodes:` count |
| **Loop** | One full pass over the steps array. Ladders can run multiple loops. | runtime; bounded by `maxLoops` |
| **Recursion** | A step that spawns a child instance of the same ladder; child's exit output replaces the step's output. | a step's `recursion:` block |
| **Gate** | A typed predicate evaluated against step output. Decides which nodes survive and which action fires next. | a step's `if:` block |
| **Action** | What the runtime does after a gate evaluates: continue, jump, or abort. | under the gate's `then:` or `else:` |
| **Knob** | A caller-tunable parameter declared at config time. Same ladder, many use cases. | under `knobs:` |
| **Relik** | A persistent memory store declared at config time. Composed of folders of files plus typed metatags. Survives across calls. | under `reliks:` |
| **Metatag** | A named, typed value attached to a relik file. Plus five system metatags the engine auto-maintains. | under a folder or file's `metatags:` |
| **Manifest** | A runtime snapshot of everything readable at a given step. Used by dynamic-step planners. | a `type: manifest` field |

The hierarchy in one sentence: a **ladder** is a list of **steps**; each step makes some number of LLM **calls** (its nodes), each call assembled from declared **fields**; steps can branch via **gates** and **jumps**, recurse, persist to **reliks**, and rewrite themselves via **dynamic** loading.

## The five power axes

Five features separate LADR from a chained-prompt library. Each axis is what makes a specific class of reasoning strategy expressible.

**1. Knobs.** Caller-tunable parameters declared at config time. A single ladder can serve "fast draft" and "high-stakes final" by exposing knobs the caller sets at request time. The same YAML; different cost/quality trade-off per call. Without knobs, every ladder is one strategy frozen at author time.

**2. Gates and jumps.** A step can declare a typed condition (`if: { integerRange: [4,5] }`) and what to do on match vs miss (`then: continue, else: { jump: { stepId: refine } }`). Backward jumps enable reflexion. Forward jumps enable early exit. Cross-ladder jumps enable composition. Without gates and jumps, every ladder runs the same fixed sequence every time.

**3. Reliks and writes.** A persistent memory store declared at config time. Folders of plain-text files plus typed metatags. Schema-matched writes extract structured data from LLM output and persist only what conforms. Without reliks, every call starts from scratch; the ladder cannot learn across invocations.

**4. Adaptive keys.** Three flat step-level keys (`codeExecution:`, `relikRead:`, `relikEdit:`) that each enable a bounded multi-turn loop where the LLM can call tools (run code, read reliks, edit reliks), receive results, and continue reasoning. Termination is guaranteed by `maxRounds`. Without adaptive keys, the ladder must know at config time what to read; with them, the LLM picks at runtime.

**5. Dynamic steps.** A step can load its own configuration at runtime, usually from the previous step's output. Combined with the `manifest` field (a runtime snapshot of everything readable), a ladder can rewrite parts of itself based on observed state. This is what makes a ladder a self-improving organism rather than a fixed program.

Every ladder uses some of these. The reference ladder **Crucible** uses all five.

## What happens when you call a ladder

A caller hits the OpenAI-compatible endpoint. The URL path encodes the
ladder identity (`author/name`); the `model` field is the actual LLM model:

```bash
POST /v1/local/my-ladder/chat/completions
{
  "model": "gpt-4o",
  "messages": [{"role": "user", "content": "Explain the halting problem."}],
  "knobs": { "branches": 5 }
}
```

The gateway resolves the ladder from the URL path, loads the config from
the database, validates it, and dispatches the execution. The response shape
is a standard OpenAI chat completion. Because the ladder identity is in the
URL (not the model field), the caller can swap models per request without
changing the endpoint.

The runtime takes that request and runs:

1. **Load and parse** the ladder config. Reject unknown top-level keys. Reject missing required fields.
2. **Validate**. Twenty-five rule checks across structure, references, ranges, reliks, gates, jumps, dynamic config, and budget. All errors aggregate; nothing runs until the config is clean.
3. **Resolve ceilings**. Merge the ladder's declared `executionBudget` with platform defaults. The four ceilings: maxSpend (dollars), maxHops, maxLlmCalls, maxRecursion.
4. **Load reliks** into the hot tier (in-memory or Redis, depending on deployment). File contents, metatags, system indexes all become readable in sub-millisecond.
5. **Resolve knobs**. Start with declared defaults. Override with caller-supplied values. Clamp out-of-range values silently. Drop unrecognized keys silently.
6. **Enter the main loop**. For each loop iteration (up to `maxLoops`):
   - Walk steps in declaration order.
   - For each step: hop +1 (ceiling check), resolve fields, dispatch LLM calls (or adaptive loops if declared), store outputs as numbered nodes, optionally apply schema-matched writes, evaluate the gate per node and prune non-matching, fire the `then:` or `else:` action.
   - Jumps reposition the step cursor (forward skips steps; backward creates inner loops). The hop ceiling bounds the total across loops, jumps, and recursion.
7. **Resolve the exit step**. Read its output from the final loop iteration. Return as the response's `content`.

Every step publishes lifecycle events as it runs. Foundry render these as a live timeline so the caller can watch the ladder think.

## How untrusted ladders stay safe

LADR is Turing-complete. Ladders can loop, recurse, jump, and rewrite themselves at runtime. Termination is **not** statically guaranteed; it is enforced at runtime by four hard ceilings.

| Ceiling | Default | Per-ladder configurable via | Platform max |
|---|---|---|---|
| Spend (dollars) | $5 | `executionBudget.maxSpend` | $50 |
| Hops (loops + jumps + recursion) | 200 | `executionBudget.maxHops` | 10,000 |
| LLM calls | 500 | `executionBudget.maxLlmCalls` | 5,000 |
| Recursion depth | 5 | `executionBudget.maxRecursion` | 20 |

A ladder author can lower any ceiling (always safe) or raise it up to the platform max. A first-party ladder that needs more headroom declares a higher budget; a directory ladder that should be cheap to run declares a tighter one.

The **spend cap is the real safety net**. A malicious or buggy published ladder can loop freely, but every LLM call costs money. The runtime hard-kills the execution the moment cumulative spend crosses the cap and returns the best-so-far output. This is what makes untrusted ladders safe to invoke, not grammar restrictions.

When a ceiling is hit, the runtime publishes an `error` event with `ceiling_exceeded: true` and exits gracefully. The caller sees a partial result, not a crash.

## The shape of every ladder

Every ladder is a YAML object with this skeleton:

```yaml
# Required
name: <ladder name>
allowedTargets: { strategy: universal }   # or { strategy: constrained, providers: [...], models: [...] }
exit: <step id whose output is returned>
knobs: { ... }                             # may be empty
steps: [ ... ]                             # at least one

# Optional
id: <platform-assigned uuid>
description: <human-readable summary>
reliks: [ ... ]                            # persistent memory stores
executionBudget:
  maxSpend: 5.0
  maxHops: 200
  maxLlmCalls: 500
  maxRecursion: 5
trace: { mode: reasoning, allSteps: false }
```

The parser is **strict**: unknown top-level keys are rejected, not silently ignored. Adding a new top-level field is a grammar change, not a silent extension. The five required fields (`name`, `allowedTargets`, `exit`, `knobs`, `steps`) must all be present, even if `knobs` is `{}`.

Each entry in the `steps:` array is one of three step types. Every step shares a common base shape; the type adds specific behavior.

```yaml
steps:
  - id: draft                              # unique step id; used by jumps and ingest references
    name: Draft                            # human-readable label
    type: normal                           # normal | sequential | group (default: normal)
    nodes: 5                               # fan-out count (number | knob name | { from: knob name })
    fields:                                # prompt inputs
      - { name: Question, type: text, from: input.context }
    systemPrompt: "..."                    # system-role instruction
    if:                                    # typed gate
      integerRange: [4, 5]
      then: continue
      else: { jump: { stepId: refine } }
    timeline: circle                       # UI hint: circle | init
    recursion: { maxDepth: 3 }             # optional recursion
    relikWrite: { ... }                    # optional schema-matched write
    codeExecution: { ... }                 # optional adaptive code loop
    relikRead: { ... }                     # optional adaptive read loop
    relikEdit: { ... }                     # optional adaptive edit loop
    dynamic: false                         # optional: load config at runtime
```

The rest of this section walks each field of this skeleton in detail.

## The three step types

Every step has a `type`. The type controls how its nodes (LLM calls) are dispatched.

| Type | How nodes run | When to use |
|---|---|---|
| `normal` (default) | All N nodes dispatched in parallel. Each gets the same assembled prompt, except for `nodeInfo` which differs per node. | Fan-out for Tree-of-Thoughts, multi-perspective debate, self-consistency voting. The default for any step that benefits from independent attempts. |
| `sequential` | Nodes dispatched in order. Node K can read outputs of nodes 1 through K-1 via `nodeRef: previous`. | Chain-of-thought within one step, iterative self-refinement, any flow where later calls should build on earlier calls in the same step. |
| `group` | Has no `nodes:`, `fields:`, or `systemPrompt:` of its own. Instead has a `steps:` array of child steps that run as parallel siblings. | Independent analyses feeding a shared downstream synthesis. Three reviewers running side by side, then a synthesis step merging their outputs. |

When in doubt, use `normal`. Use `sequential` only when later nodes in the same step must consume earlier nodes' outputs. Use `group` only when two or more independent sub-strategies must run side by side and feed a shared downstream step.

Hard rules enforced at validation:

- A `group` must have at least 2 child steps.
- Groups cannot be nested.
- Group children cannot reference each other with `loopRef: current` (parallel steps cannot read from each other).

## A tour through a non-trivial ladder

The reference ladder **Crucible** uses every power axis. Reading this tour is the fastest way to see how the pieces compose. (The full config is in Examples > Reference Ladder: Crucible.)

```yaml
name: Crucible
allowedTargets: { strategy: universal }
exit: answer
knobs:
  branches:   { name: Branches,   type: nodes,     input: slider, steps: [...] }
  verifiers:  { name: Verifiers,  type: nodes,     input: slider, steps: [...] }
  iterations: { name: Iterations, type: recursion, input: numerical, default: 2, min: 1, max: 4 }
reliks:
  - title: Crucible Memory
    folders:
      - { name: trace,   tokenLimit: 800, files: [{name: verified.md, init: blank}] }
      - { name: lessons, tokenLimit: 400, files: [{name: failures.md, init: blank}] }
      - { name: solution, tokenLimit: 1000, files: [{name: best.md, init: blank}] }
steps:
  - id: generate
    nodes: "branches"             # fan out 3/5/8 candidates
    fields:
      - { name: Context, type: text, from: input.context }
      - { name: PriorArt, type: relikRead, from: { relik: Crucible Memory, folder: trace, file: verified.md } }
      - { name: Lessons, type: relikRead, from: { relik: Crucible Memory, folder: lessons, file: failures.md } }
    systemPrompt: "Generate an independent solution. Build on PriorArt; heed Lessons."

  - id: verify
    nodes: "verifiers"            # fan out verifiers
    fields:
      - { name: Candidate, type: ingest, from: { stepId: generate, loopRef: current } }
    systemPrompt: 'Emit JSON {"score": 1-5, "verdict": "accept|reject"}.'
    if:
      integerRange: [4, 5]                  # accept on 4 or 5
      then: continue                        # survivors continue
      else: { jump: { stepId: persist_failure } }  # rejects go persist lessons

  - id: reflect
    fields:
      - { name: Failures, type: multi_ingest, from: [{ stepId: verify, loopRef: current, nodeRef: accumulate }] }
    systemPrompt: 'Emit JSON {"root_cause": "...", "lesson": "..."}.'
    relikWrite:
      to: { relik: Crucible Memory, folder: lessons, file: failures.md }
      on: append
      match: { type: object, required: [root_cause, lesson] }
    if:
      jsonMatches: { type: object }         # lesson emitted → jump back to generate
      then: { jump: { stepId: generate } }  # backward jump = reflexion

  - id: refine
    type: sequential
    nodes: "iterations"
    recursion: { maxDepth: "iterations" }
    # ...

  - id: answer
    fields:
      - { name: Solution, type: relikRead, from: { relik: Crucible Memory, folder: solution, file: best.md } }
    systemPrompt: "Return the solution as-is."
```

What this ladder does, in plain English:

1. **`generate`** fans out N parallel candidates, each reading the user's question plus everything the ladder has learned so far (`PriorArt`, `Lessons`).
2. **`verify`** runs M independent verifiers against each candidate. Scores 4-5 survive; scores 1-3 are pruned and their analysis is written to the lessons relik on the way out.
3. If any survivors remain, control advances. If all were pruned, control jumps to `reflect`.
4. **`reflect`** reads all failures, extracts a structured lesson, persists it via schema-matched write, and jumps **backward** to `generate`. The next generate pass reads the new lesson; the ladder has learned within a single call.
5. **`refine`** runs a sequential chain that recursively deepens on the best candidate.
6. **`answer`** reads the verified solution from the relik and returns it verbatim.

Crucible on call 1 starts with empty reliks. On call 2, `generate` reads the verified trace and accumulated lessons from call 1. The ladder walks in already educated. That persistence is what no published reasoning strategy has, because none had the primitive.

## Where to go next

Read the Language section in order for the systematic treatment of each primitive.

1. **[Top-Level Config](./top-level.md)**. Every root-level field, every required/optional distinction, every validation rule.
2. **[Knobs](./knobs.md)**. Caller-tunable parameters. The first power axis.
3. **[Steps](./steps.md)**. The execution unit. Anatomy, types, lifecycle.
4. **[Fields](./fields.md)**. The seven field types. How prompt assembly works.
5. **[Control Flow](./control-flow.md)**. The typed gate and the four actions.
6. **[Jumps](./jumps.md)**. Cursor movement within and across ladders.
7. **[Recursion](./recursion.md)**. Spawning child instances of the same ladder.
8. **[Reliks](./reliks.md)**. Persistent memory: folders, files, metatags.
9. **[Reads and Writes](./reads-writes.md)**. `relikRead`, `relikWrite`, and gated writes.
10. **[Adaptive keys](./tools.md)**. Bounded multi-turn tool loops (code, read, edit).
11. **[Dynamic Steps](./dynamic-steps.md)**. The self-improvement primitive.
12. **[Execution Budget](./execution-budget.md)**. The four runtime ceilings.
13. **[Validation](./validation.md)**. The 25 rule checks and what each catches.
14. **[Observability](./observability.md)**. Lifecycle events, trace modes, timeline annotations.

If you learn better by example, start with Tutorials > Hello World and work through the Core Patterns. For a large composed example, see Examples > Reference Ladder: Crucible.

