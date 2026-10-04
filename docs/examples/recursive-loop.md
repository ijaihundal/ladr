# C. Recursive Multi-Loop Refinement

Combines loops and step-level recursion for iterative quality improvement across rounds and nested depth.

## C.1 What This Ladder Does

This ladder layers two control flow mechanisms: loops run the entire step sequence multiple times, and recursion lets a single step spawn a child execution of the same ladder to refine its own output. The result is a ladder that produces progressively refined drafts across rounds, with each round's output going through multiple levels of recursive polishing.

The first step (`draft`) generates an initial response from the caller's message. It also reads outputs from previous loops via a `multi_ingest` field with `loopRef: accumulate`; on loop 2, it reads what the final step produced in loops 0 and 1.

The second step (`final`) takes the draft and recursively refines it. With `maxDepth` driven by a knob, the caller controls how many refinement passes happen inside each loop. At each recursion depth, the child ladder runs the same two steps; `draft` from the parent's output, then `final` refines again, possibly recursing further. When the deepest level finishes, the refined output bubbles back up through every level.

Loops and recursion are independent axes. Each loop iteration gets its own independent child execution. The child always runs with `maxLoops: 1`; it does one clean pass. The `multi_ingest` with `loopRef: accumulate` collects across loops, not across recursion depths.

## C.2 Step-by-Step Execution

With 2 rounds (loops) and maxDepth 2:

**Loop 0.**

The `draft` step runs. Its `Context` field reads `input.context`; the caller's message. Its `Earlier Drafts` field is a `multi_ingest` with `loopRef: accumulate` targeting the `final` step. Since this is loop 0, there are no previous loops, so the `Earlier Drafts` field resolves to nothing; it's omitted from the prompt. The draft step produces an initial draft, labeled **D0**.

The `final` step runs. Its `Draft` field ingests D0 from the draft step. Its system prompt says "Refine draft and return final response." It produces output **F0**. Recursion check: depth 0 < maxDepth 2, so a child is spawned with `input.context` = F0. The parent pauses.

**Child at depth 1 (loop 0).** The child starts from step 0. `draft` reads F0 as `input.context`. No earlier loops (`maxLoops: 1`), so `Earlier Drafts` is empty. It produces **D1**. `final` ingests D1, produces **F1**. Recursion check: depth 1 < maxDepth 2, spawn another child with `input.context` = F1.

**Child at depth 2 (loop 0).** `draft` reads F1, produces **D2**. `final` ingests D2, produces **F2**. Recursion check: depth 2 = maxDepth 2; no more recursion. Since `exit: final`, **F2** is this level's exit output.

**Depth 1 receives F2.** This replaces the `final` step's stored output (was F1, now F2). Since `exit: final`, the exit output is F2. This bubbles up.

**Depth 0 receives F2.** This replaces the `final` step's stored output (was F0, now F2). Loop 0 is complete. The `final` step's output for loop 0 is stored as **F2**.

**Loop 1.**

The `draft` step runs again. `Context` is still the caller's original message. But now `Earlier Drafts` resolves; it reads the `final` step's output from loop 0, which is F2. The prompt includes both the original context and the refined output from loop 0. The draft step produces **D3**.

The `final` step ingests D3, produces **F3**. Recursion fires again; same depth chain as loop 0. Depth 1 spawns, depth 2 is the base case, refined output bubbles back up, labeled **F5**.

Loop 1 is complete. The `final` step's output for loop 1 is **F5**.

**Exit.** Since `exit: final`, the last loop's exit output is the ladder's response. The caller receives **F5**; a response that went through two rounds of drafting (with accumulated context) and two levels of recursive refinement in each round.

## C.3 How Loops and Recursion Interact

Loops and recursion are independent axes. The loop counter doesn't advance during child execution. When the parent is on loop 1 and hits the recursion step, the entire child execution happens within loop 1; the child runs its single pass, finishes, and returns. Only then does the parent move on.

Each loop iteration gets its own child. The child always runs with `maxLoops: 1`, which means `loopRef: accumulate` inside the child collects nothing; there are no previous loops in the child. The `multi_ingest` field on the `draft` step only accumulates across the parent's loops.

The recursion step's refined output (after the child returns) is what gets stored for that loop iteration. A `multi_ingest` with `loopRef: accumulate` targeting the recursion step collects the refined outputs; one per loop. Loop 2 sees the refined outputs from loops 0 and 1.

The `multi_ingest` on the `draft` step targets the `final` step, not the `draft` step itself. Each loop's draft sees the *final, polished* output from previous loops; not the raw draft. The draft step receives the best version of each previous round to build on.

## C.4 Full Config

```yaml
name: Recursive Multi-Loop Refinement

allowedTargets:
  strategy: universal

exit: final

knobs:
  rounds:
    name: Rounds
    type: loops
    input: numerical
    default: 2
    min: 1
    max: 4
  iterations:
    name: Iterations
    type: recursion
    input: numerical
    default: 2
    min: 1
    max: 3

steps:
  - id: draft
    name: Draft
    type: normal
    fields:
      - name: Context
        type: text
        from: input.context
      - name: Earlier Drafts
        type: multi_ingest
        from:
          - stepId: final
            loopRef: accumulate
    systemPrompt: "Draft based on context and prior rounds."

  - id: final
    name: Final
    type: normal
    recursion:
      maxDepth: "iterations"
    fields:
      - name: Draft
        type: ingest
        from:
          stepId: draft
          loopRef: current
    systemPrompt: "Refine draft and return final response."
```

Two knobs: `rounds` (type `loops`) controls how many times the step sequence repeats, and `iterations` (type `recursion`) controls the recursion depth on the `final` step. Both are exposed to the caller as numerical inputs. The `recursion` block on the `final` step only has `maxDepth`; it references the `iterations` knob. The child inherits the same ladder config with `maxLoops: 1` and `input.context` replaced by the step's output.

## C.5 What the Caller Sees

The ladder is addressed by the author's username and the ladder's slug name in the URL path. The request body follows the standard OpenAI chat completions format, with an optional `knobs` object to override the ladder's default knob values:

```bash
curl http://localhost:8788/v1/local/recursive-multi-loop/chat/completions \
  -H "Authorization: Bearer $LADR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Write a technical analysis of WebAssembly."}],
    "knobs": { "rounds": 3, "iterations": 2 }
  }'
```

The response is a standard chat completion containing the final output after 3 rounds of drafting and 2 levels of recursive refinement in each round. The response format is identical to a direct model call; intermediate drafts, recursion nesting, and accumulated context are not exposed.

In Foundry, the timeline renders the full execution. Each loop is a distinct pass through the steps. The `draft` step shows how the output evolved across rounds; the first round is a basic draft, the second incorporates the first round's refined output, the third builds on both. The `final` step can be drilled into to see each recursion level and how the output was progressively refined at each depth before bubbling back up.

