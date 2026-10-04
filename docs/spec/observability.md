# Observability

Every ladder execution publishes a stream of lifecycle events that describe what the runtime is doing. Foundry consumes these events to render the live execution timeline. API clients can consume them via the streaming endpoint to show progress in their own UIs. This article documents the event types, the trace modes that control what shows in the response, and the timeline annotations that shape how steps render visually.

## Why every step publishes events

A ladder invocation is not a single LLM call; it is a multi-step deliberation. Without event publishing, the caller sees only the final output and has no visibility into how it was produced. That makes debugging impossible: did the verifier prune too aggressively? Did the reflexion loop fire? Did the dynamic step load a useful config?

Lifecycle events surface every transition:

- When the pipeline starts, loops, steps, recurses, and completes.
- When a cross-ladder handoff fires.
- When a step's nodes complete, fail, or get pruned by a gate.
- When a node count is clamped to the platform budget.
- When a resource ceiling is hit.
- When anything goes wrong.

The event stream is the canonical record of what happened during an execution. Foundry subscribes to it to render the timeline; the trace subsystem persists it for post-hoc debugging.

The author does not write events; they are emitted by the runtime. But the author does control two things that shape how events render: the trace mode (what shows in the OpenAI response) and the timeline annotations (how steps appear in the visual timeline).

## Lifecycle event types

The runtime publishes 16 distinct event types during a pipeline execution. Each event carries an instance id, a type, and a data map with type-specific fields.

| Event | When it fires | Key data fields |
|---|---|---|
| `pipeline_start` | Pipeline begins executing. | `maxLoops`, `ceilings` |
| `loop_start` | Each loop iteration begins. | `loopIndex` |
| `step_start` | Each step begins. | `loopIndex`, `stepId`, `groupId`, `midLadderEntry` |
| `step_complete` | Each step finishes successfully. | `loopIndex`, `stepId`, `groupId` |
| `step_paused` | Step paused for debug-mode inspection. | `loopIndex`, `stepIndex`, `stepId` |
| `loop_complete` | Each loop iteration finishes. | `loopIndex` |
| `recursion_start` | A child ladder spawns. | `parentInstanceID`, `stepId`, `depth` |
| `recursion_complete` | A child ladder finishes. | `parentInstanceID`, `stepId`, `depth` |
| `handoff_start` | A cross-ladder handoff fires. | `from`, `targetLadderId`, `jump` |
| `handoff_complete` | A cross-ladder handoff's child finishes. | `childInstanceId`, `targetLadderId`, `output` |
| `pipeline_complete` | Pipeline finishes successfully. | `output`, `usage`, optional `handoff`, `childInstanceId` |
| `pipeline_resumed` | Pipeline resumes from a paused position. | `loopIndex`, `stepId` |
| `node_count_resolved` | The runtime resolved a node count (clamped or parsed from output). | `stepId`, `requested`, `clamped`, `limit`, `accepted`, `reason` |
| `node_survivors` | After a gate pruned nodes, lists which survived. | `stepId`, `requestedNodes`, `survivingNodes`, `failedNodes`, `survivingNodeNumbers` |
| `node_failed` | One node's LLM call failed (others may still succeed). | `stepId`, `nodeNumber`, `message` |
| `error` | Anything went wrong: ceiling hit, relik write failed, all nodes failed, etc. | `message`, optional `ceiling_exceeded`, `nonFatal` |

Every event is published via the streaming endpoint (SSE for OpenAI-compatible streaming) and persisted to the trace subsystem for later replay.

## Trace modes

The ladder's optional `trace:` block controls how intermediate step outputs appear in the OpenAI-compatible response.

```yaml
trace:
  mode: reasoning          # reasoning | inline | off (default: reasoning)
  allSteps: false          # true emits every step; false emits only leaf steps
```

### `mode: reasoning` (default)

Each traced step's output appears under a separate `reasoning` field in the response, distinct from the final `content`. This matches how OpenAI's reasoning models expose their thinking. Drop-in clients that already handle reasoning model responses see the ladder's intermediate steps the same way.

```json
{
  "choices": [{
    "message": {
      "role": "assistant",
      "content": "The final answer...",
      "reasoning": {
        "draft": "First attempt...",
        "verify": "Score 4/5...",
        "refine": "Improved version..."
      }
    }
  }]
}
```

### `mode: inline`

Intermediate step outputs are interleaved with the final content in a single stream, separated by visual markers. Useful for clients that want one continuous text and don't distinguish reasoning from the answer.

### `mode: off`

No intermediate output. The response contains only the exit step's output. Useful for production calls where you want minimum payload and don't need to inspect intermediate state.

### `allSteps: true`

By default, only **leaf steps** (steps that produce user-visible output, typically those with `timeline: circle`) get trace blocks. Setting `allSteps: true` makes every step emit a trace block, including init steps and intermediate scaffolding. Useful for debugging; verbose for production.

## Timeline annotations

The `timeline:` field on a step is a UI hint for Foundry. It controls how the step renders in the visual execution timeline.

| Value | Effect |
|---|---|
| `circle` (default for steps with `nodes:`) | Renders as one circle per node. Each node's output appears in the timeline as a separate circle. |
| `init` | Renders as the timeline's leftmost node (a square marker). At most one step per ladder can have this. Cannot have `nodes:`. Cannot be the exit step. |

The `init` marker is purely visual; it does not change execution order. The runtime still walks the steps array in declaration order. Use `init` to mark a setup step whose output is consumed by everything downstream.

```yaml
steps:
  - id: setup
    name: Setup
    timeline: init
    systemPrompt: "Initialize context for the rest of the ladder."

  - id: generate
    name: Generate
    nodes: 5
    timeline: circle
    fields:
      - { name: Setup, type: ingest, from: { stepId: setup, loopRef: current } }
    systemPrompt: "Generate a candidate."
```

Foundry renders an `init` square on the far left followed by five circles for `generate`. Without `timeline: init`, the setup step would not appear in the timeline at all.

**When to hide a step from the timeline.** A step that does internal work the user shouldn't see (extracting JSON for a relik write, parsing an evaluator score) can be marked `timeline: circle` only if you want each node visible. To omit a step entirely from the visual timeline, omit `timeline:` and don't declare `nodes:` — single-node steps without an explicit hint don't render as circles.

## Checkpoint labels

The optional `checkpoint:` block on a step adds text labels to the trace output. Useful for long ladders where the trace would otherwise be a wall of step IDs.

```yaml
- id: draft
  checkpoint:
    heading: "Drafting phase"
    nodeHeading: "Candidate"
```

- `heading` labels the step's section in the trace.
- `nodeHeading` labels each individual node within the step.

These are purely display hints. They do not affect execution. They appear in the trace JSON and in Foundry's timeline as tooltips and section dividers.

## The trace subsystem

Every event published during execution is persisted to the trace subsystem for later replay. A **trace** is the complete event stream for one pipeline instance, keyed by instance id.

Traces are useful for:

- **Post-hoc debugging.** A failed execution can be replayed step by step in Foundry to inspect intermediate outputs and identify where reasoning diverged.
- **Benchmark reproduction.** A trace pins the exact sequence of calls and outputs that produced a benchmark number, so results can be reproduced by anyone with the trace id.
- **Cost analysis.** Token usage events in the trace let you decompose spend by step, by node, by provider.
- **Training data curation.** Traces of successful ladders are the structured measurements that feed the long-game corpus thesis.

Traces are queryable by instance id, by ladder id + version, by user, by time range. The exact query interface depends on deployment (in-memory reference impl, Redis-backed production, filesystem local-first).

## Token streaming

For streaming requests (`stream: true` in the OpenAI-compatible request), the runtime publishes token-level events as each LLM call progresses. Token streaming is separate from lifecycle events:

- **Lifecycle events** (the 14 types above) describe structural transitions: step started, node complete, pipeline done.
- **Token events** describe the LLM's output stream: each generated token, with its origin step, node, and loop index.

Token events stream over the same SSE connection as lifecycle events. Clients that consume the stream see both interleaved. Foundry use token events to render live token-by-token output for each node as it generates.

The token stream is bounded by the same spend cap and call ceiling as the rest of the execution. When a ceiling is hit, the stream ends with the appropriate error event and the runtime returns the best-so-far output.

Token streaming works for:

- The final exit step's output (always streamed when `stream: true`).
- Intermediate step outputs, when `trace.mode` is `reasoning` or `inline` (not `off`).

Clients that only want the final answer can ignore intermediate token events and consume only those tagged with the exit step id.

## Debug mode: pause and resume

The runtime supports a debug mode for step-through inspection. When enabled, the runtime checks before each step whether it should pause. If yes, it persists the current position and waits.

A paused pipeline can be resumed from the saved position. The runtime skips already-completed steps and continues from the paused step. Useful for:

- **Step-through debugging in Foundry.** Pause before a problematic step, inspect intermediate state in the visual editor, edit the relik by hand if needed, then resume.
- **Manual intervention.** A long-running pipeline can be paused while a human reviews a checkpoint, then resumed or aborted based on the review.
- **Cost-controlled execution.** Pause after a high-cost step to verify the output before continuing to even higher-cost steps.

Debug mode is controlled by the runtime, not by the ladder config. Authors do not declare it; operators enable it via the runtime options. The ladder's behavior is unchanged; only the timing changes.

## Cost and usage tracking

Every LLM call records its token usage. The runtime aggregates usage across the whole pipeline and exposes it in the response.

```json
{
  "usage": {
    "prompt_tokens": 2340,
    "completion_tokens": 850,
    "total_tokens": 3190
  }
}
```

## Observability patterns

**Always set `trace.mode` explicitly.** The default is `reasoning`, which works for most cases, but being explicit makes the ladder's intent clear. Use `off` for production hot paths where you don't need intermediate output; use `allSteps: true` temporarily during development to surface every step.

```yaml
trace:
  mode: reasoning
  allSteps: false   # production
```

**Use `checkpoint.heading` for any ladder with more than 3 steps.** The trace becomes unreadable without section labels. Headings like "Drafting phase", "Verifying", "Refining", "Finalizing" make the trace scannable.

**Mark setup steps with `timeline: init`.** A single `init` step on the left anchors the visual timeline and tells the reader "this runs first, everything else builds on it."

**Don't hide critical steps from the timeline.** If a step's output is important for understanding the result (a gate's pruning decision, a relik write's content), keep it visible. The temptation to hide "implementation detail" steps makes debugging impossible later.

**Watch `node_count_resolved` events in development.** They tell you when the runtime clamped your requested node count to the budget. If you see them often, either raise the budget (via `executionBudget.maxLlmCalls`) or lower your node counts.

**Watch `node_survivors` events to debug gates.** If a gate pruned all survivors unexpectedly, the event shows exactly which nodes survived (none) and which were considered. From there you can inspect each pruned node's output to see why the gate rejected it.

