# Hello World: Your First Ladder

Build, save, and call a ladder end-to-end in 15 minutes. No prior LADR knowledge assumed.

## Prerequisites

You need:

- A running LADR instance (see [Getting Started](../intro/getting-started.md) for Docker Compose setup).
- At least one LLM provider API key configured (OpenAI is the easiest first provider). Add it under **Foundry → Settings → Providers** at `http://localhost:3006`.
- Comfort with a terminal and curl (or any HTTP client).

That's it. You author in Foundry (browser-based visual editor) and call via curl.

## Step 1: Open Foundry

Go to **`http://localhost:3006`** in your browser. The left sidebar shows your existing ladders (empty if this is your first). Click **New ladder**.

Name it `hello`. (Visibility defaults to private; only you can call it.)

You're now in the visual editor. The empty canvas represents one ladder with zero steps.

## Step 2: Add a step

Click **Add step**. Set:
- **ID:** `answer`
- **Name:** `Answer`
- **Type:** `normal`
- **System prompt:** `You answer questions clearly and directly. No hedging, no preamble.`

Add one field:
- **Name:** `Context`
- **Type:** `text`
- **From:** `input.context`

This field reads the user's last message and renders it in the prompt as `Context: <the user's text>`.

## Step 3: Set the exit step

In the top-level ladder config, set **Exit step** to `answer`. This tells the runtime: "when this step finishes, the ladder is done; return its output as the response."

The YAML preview (top-right) should look like:

```yaml
name: Hello Ladder
allowedTargets: { strategy: universal }
exit: answer
knobs: {}
steps:
  - id: answer
    name: Answer
    type: normal
    fields:
      - { name: Context, type: text, from: "input.context" }
    systemPrompt: "You answer questions clearly and directly. No hedging, no preamble."
```

Note what's NOT in the YAML:
- **`id`, `author`, `version`, `visibility`**; set by the platform at publish time, not in the author YAML.
- **`loops`**; not a top-level field. Iteration is via jumps (`then: { jump: ... }`) or recursion per step.

## Step 4: Save

Click **Save**. Foundry validates the config (every field reference must resolve, the exit step must exist, JSON Schemas well-formed). If validation passes, you see a green **Saved** indicator and the ladder appears in your list with version 1.

If validation fails, Foundry highlights the offending line and explains the issue. Fix and re-save.

## Step 5: Call the ladder

Your ladder is now callable. The URL path encodes the ladder identity
(`local/hello`); the `model` field is the actual LLM model:

```bash
curl http://localhost:8788/v1/local/hello/chat/completions \
  -H "Authorization: Bearer $LADR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "What is the meaning of life?"}]
  }'
```

You'll get back a standard OpenAI chat completion:

```json
{
  "id": "ladr-abc123",
  "object": "chat.completion",
  "model": "gpt-4o",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "The meaning of life is what you make of it..."
      },
      "finish_reason": "stop"
    }
  ]
}
```

That's a working ladder. One LLM call, one field, no knobs. Not yet more powerful than a direct OpenAI call; but you now have the full LADR runtime available to extend.

## Step 6: Add a knob

Let's add a knob so the caller can control how concise or expansive the answer is.

Edit the ladder. Add a knob to the `knobs:` map:

```yaml
knobs:
  verbosity:
    name: Verbosity
    type: generic                       # semantic category; "generic" for non-fanout/non-loop knobs
    input: slider                       # UI input kind: slider | numerical
    steps:                              # slider input: discrete ticks
      - { title: Terse,  value: 1 }
      - { title: Medium, value: 3, default: true }
      - { title: Verbose, value: 5 }
```

Now add a second field to the step that injects the knob value:

```yaml
fields:
  - { name: Context, type: text, from: "input.context" }
  - { name: Verbosity, type: knobInfo, from: verbosity }    # 'from' is just the knob name
```

Update the system prompt:

```
You answer questions clearly and directly. Verbosity is set on a 1-5 scale
where 1 is telegram-style and 5 is a long essay. Match the "Verbosity" field
above exactly.
```

Save. Now callers can tune verbosity per-call:

```bash
curl http://localhost:8788/v1/local/hello/chat/completions \
  -H "Authorization: Bearer $LADR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Explain gravity."}],
    "knobs": { "verbosity": 1 }
  }'
```

Same ladder. Different runtime behavior, controlled by the caller.

## Step 7: Cap the cost

Before publishing, set an execution budget so callers can't accidentally run up your spend:

```yaml
executionBudget:
  maxSpend: 0.10          # dollars; hard ceiling per call
  maxHops: 5              # this ladder is 1 hop, so 5 is plenty of headroom
  maxLlmCalls: 3          # this ladder is 1 call, so 3 is plenty
```

The engine hard-kills the execution if any ceiling is hit and returns the best-so-far output (not an error). For a one-step ladder these caps are never reached, but it's hygiene; get into the habit.

## Where to go next

You now know the core LADR loop: define a step, wire up fields, set the exit, call the endpoint.

Next:

- **[Build a Tree-of-Thoughts](../tutorials/build-tot.md)**; multiple nodes drafting in parallel, gates pruning the bad ones, a synthesis step merging the survivors.
- **[Language Overview](../spec/overview.md)**; the full concept tour (steps, fields, knobs, loops, gates, jumps, reliks, recursion, dynamic steps).
- **[Examples](../examples/minimal-two-step.md)**; copy-paste starting points.

