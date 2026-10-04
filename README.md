# LADR — reasoning, written like code

LADR (Ladder) is a language and engine for **inference-time reasoning**: you
author multi-step LLM workflows as versioned, gate-checked programs instead of
chaining prompts in application code.

A ladder is a strict YAML document — steps with typed fields, gates that route
control flow, jumps for retry, knobs for tunable parameters, reliks for
persistent memory, and budgets that cap what a run can spend. The engine
executes it: resolves fields, renders prompts, calls providers (with
fallbacks), evaluates gates, runs mid-reasoning code in persistent interpreter
sessions, and writes schema-matched output to memory.

```yaml
name: verify-claim
description: Verify a factual claim by running code, not by vibes.
allowedTargets: { strategy: universal }
exit: answer
knobs: {}
steps:
  - id: verify
    type: normal
    fields:
      - { name: Claim, type: text, from: input.context }
    codeExecution:
      maxRounds: 3
      runtime: python
      as: ComputedResult
    systemPrompt: |
      Verify the Claim. Run code to check it numerically.
      Then state VERIFIED or REFUTED in prose.
  - id: answer
    type: normal
    fields:
      - { name: Verdict, type: ingest, from: { stepId: verify } }
    systemPrompt: "Return the verdict as-is: {Verdict}"
```

## Why

Chain-of-thought happens inside one forward pass and evaporates. LADR moves
deliberation *out* of the context window and into a program you can read,
diff, fork, and re-run:

- **Gates, not hopes** — every step's output can be checked (JSON schema,
  integer ranges, contains, enum) before control flows onward; failures route
  to retry jumps instead of silently polluting downstream context.
- **Budgets are law** — `executionBudget` caps dollars, hops, LLM calls, and
  recursion depth per run. A ladder cannot spend more than its author allowed.
- **Memory as data** — `reliks` are per-instance structured memory files with
  optimistic concurrency; ladders read them as fields and write them through
  schema-matched, auditable writes.
- **Code where it belongs** — `codeExecution` gives the model a persistent
  python/node/bash session mid-reasoning. Verification runs on real
  interpreters; the ladder just reads what came out.
- **Provider-agnostic** — one provider chain (primary + fallbacks) per run;
  OpenRouter, OpenAI, Anthropic, Google, xAI, DeepSeek, Mistral, Groq, or any
  OpenAI-compatible endpoint.

## Install & quickstart

Requires Go 1.25+:

```sh
git clone https://github.com/ijaihundal/ladr
cd ladr
go build -o ladr ./cmd/ladr
```

Check a ladder (parse + full grammar validation):

```sh
./ladr check examples/code-exec-demo.ladr
# ✓ examples/code-exec-demo.ladr: valid — 2 steps, exit "answer"
```

Run one (providers resolve from your environment):

```sh
export OPENROUTER_API_KEY=sk-or-...
./ladr run examples/code-exec-demo.ladr \
  --input "context=The claim: 2+2=5. Verify arithmetically." \
  --model z-ai/glm-5.3-flash -v
```

Standalone runs need no Redis and no Postgres: step outputs live in memory,
reliks live in memory, code executes on your machine (`--sessions-dir` keeps
interpreter sessions persistent across runs), and `files:` tools are scoped to
the run's workspace directory.

Providers are picked up automatically from `OPENROUTER_API_KEY`,
`OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GOOGLE_API_KEY`, `XAI_API_KEY`,
`DEEPSEEK_API_KEY`, `MISTRAL_API_KEY`, or `GROQ_API_KEY` (first match wins),
or point at any OpenAI-compatible endpoint:

```sh
LADR_PROVIDER=custom LADR_BASE_URL=https://your-host/v1 LADR_API_KEY=... ./ladr run ...
```

## The language

The grammar reference lives in [`docs/spec/`](docs/spec/) — control flow,
steps, fields, gates, reliks, knobs, budgets, dynamic steps, tools. Start
with [`docs/spec/README.md`](docs/spec/README.md), then read the examples:

- [`examples/code-exec-demo.ladr`](examples/code-exec-demo.ladr) — mid-reasoning code execution
- [`examples/claim-verifier.ladr`](examples/claim-verifier.ladr) — force verification through real command output
- [`examples/memory-edit-demo.ladr`](examples/memory-edit-demo.ladr) — reliks: structured persistent memory
- [`examples/project-context-keeper.ladr`](examples/project-context-keeper.ladr) — a working project-memory ladder

## What's in this repo

The engine core (`internal/engine`), the grammar + validator
(`internal/ladr`), provider adapters (`internal/provider`), the relik store
(`internal/relik`), persistent interpreter sessions (`internal/device`), and
the `ladr` CLI (`cmd/ladr`). The test suite is the spec's shadow: gates
matrices, control flow, budgets, relik version conflicts, session protocols.

Not in this repo (kept in the hosted platform): the visual builder, the
ladder marketplace/registry, multi-instance run state, the OpenAI-compatible
serving gateway, and the postgres-backed indexer store (`indexSearch` fields
report "not configured" in standalone mode rather than pretending).

## Status

v0.1.0 — the engine core and grammar are production-tested; the standalone
CLI is new. Expect sharp edges in the CLI, report them.

## License

AGPL-3.0-or-later — see [LICENSE](LICENSE). Run it, fork it, build on it; if
you serve a modified version, share the source.
