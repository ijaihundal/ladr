# A. Minimal Two-Step Ladder

The simplest useful ladder: one step drafts, the next improves, exit returns the polished result.

## A.1 What This Ladder Does

This ladder runs two steps in sequence. The first step takes the caller's message and drafts an answer. The second step reads that draft and improves its clarity and structure. The exit step is the second one, so the API response contains the improved version.

The config demonstrates two core mechanics. First, how fields wire steps together: the `text` field on step 0 reads `input.context` (the caller's message), and the `ingest` field on step 1 reads step 0's output using `loopRef: current`. Second, how `exit` selects the final answer: since `exit: step1`, whatever step 1 produces is what the API returns.

## A.2 Step-by-Step Execution

With the input "Explain why the sky is blue.":

**Step 0; Draft.** The ladder resolves step 0's fields. There's one field: a `text` field called `Context` with `from: input.context`. It resolves to the caller's message. The prompt assembled for the LLM:

```
Context: Explain why the sky is blue.

[System Instruction]
Draft answer.
```

The response is stored as step 0's output for loop 0.

**Step 1; Improve.** Step 1 has one field: an `ingest` field called `Draft` that reads from step 0 with `loopRef: current`. Since this is loop 0, that resolves to the raw draft just produced. The prompt:

```
Draft: <the raw draft from step 0>

[System Instruction]
Improve clarity and structure.
```

The response; the improved version; is stored as step 1's output.

**Exit.** Since `exit: step1`, step 1's output is the ladder's final answer. It's returned to the caller in standard OpenAI-compatible format.

## A.3 Full Config

```yaml
name: Mini Two-Step

allowedTargets:
  strategy: universal

exit: step1

knobs: {}

steps:
  - id: step0
    name: Draft
    type: normal
    fields:
      - name: Context
        type: text
        from: input.context
    systemPrompt: "Draft answer."

  - id: step1
    name: Improve
    type: normal
    fields:
      - name: Draft
        type: ingest
        from:
          stepId: step0
          loopRef: current
    systemPrompt: "Improve clarity and structure."
```

The `strategy: universal` in `allowedTargets` means any provider and model can serve this ladder. The platform auto-manages `id`, `author`, and `version` when the config is saved.

## A.4 What the Caller Sees

The ladder is addressed by the author's username and the ladder's slug name in the URL path. The request body follows the standard OpenAI chat completions format:

```bash
curl http://localhost:8788/v1/local/mini-two-step/chat/completions \
  -H "Authorization: Bearer $LADR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Explain why the sky is blue."}]
  }'
```

The response is a standard chat completion containing step 1's improved output. The response format is identical to a direct model call; no intermediate outputs are exposed.

In Foundry, the timeline renders both steps: step 0's output and step 1's output, with the exit checkpoint on step 1 marking it as the final answer.

