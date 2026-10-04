# LADR Documentation

**LADR** — *Language for Adaptive Deliberation and Reasoning* — is the declarative DSL for
writing **ladders**: structured reasoning programs that run at inference time, above the
model and below the application. LADR is to inference-time reasoning what SQL is to data
queries: declarative, analyzable, optimizable by an engine underneath.

A ladder is a complete reasoning program saved as a `.ladr` file (YAML or JSON).
It is **data, not code** — it links against nothing and can be analyzed, validated,
rendered, and optimized without ever running.

---

## Documentation Map

| Document | What it covers |
|---|---|
| **[Language Spec](../spec/overview.md)** | Top-to-bottom tour of the LADR grammar. |
| **[Tutorials](../tutorials/hello-world.md)** | Step-by-step guides for building ladders. |
| **[Examples](../examples/minimal-two-step.md)** | Copy-paste starting points. |
| **[YAML Schema](../reference/yaml-schema.md)** | Complete field reference. |
| **[API Reference](../reference/api-reference.md)** | Full HTTP endpoint documentation. |
| **[Project README](../../README.md)** | Quick start, architecture overview, and service ports. |

---

## Core Concepts

| Concept | Definition |
|---|---|
| **Ladder** | A complete reasoning program. Atomic unit a user authors/publishes/runs/sells. Identified publicly as `@author/name`. |
| **Step** | Execution unit within a ladder. Type: `normal`, `sequential`, or `group`. Has fields, a system prompt, optional gates, jumps, recursion, and relik operations. |
| **Node** | A single LLM call within a step. `nodes: 5` fans out into 5 calls (parallel for `normal`, sequential for `sequential`). |
| **Loop** | A full iteration of the ladder. Ladders can run multiple loops. |
| **Recursion** | A step spawning a child instance of the same ladder; the child result bubbles up. |
| **Relik** | Persistent memory store declared at config time. Folders of plain-text files with structured metatags. Survives across invocations. |
| **Metatag** | Named, typed value attached to a file (or folder). The universal primitive for describing and retrieving file data. |

---

## Design Discipline

LADR is **declarative and Turing-complete** — structurally capable of unbounded loops,
recursion, conditional branching, and runtime self-modification via dynamic steps.
Turing-completeness is deliberate: real reasoning is unbounded, and forcing every ladder
into a fixed hop ceiling fights the domain.

What keeps LADR safe is not grammar restrictions but **platform-enforced runtime ceilings**.
Every execution hits hard resource caps:

- **Spend cap** (the primary safety net) — real-time dollar-cost tracking per execution.
  Hard-kill when exceeded. The trust model for untrusted published ladders.
- **Hop ceiling** — total `loops + jumps + recursion` steps; configurable per ladder.
- **LLM-call ceiling** — total API calls to any provider.
- **Recursion-depth ceiling** — max nesting depth of recursion.

A malicious or buggy published ladder can loop freely, but every LLM call costs money —
the spend cap hard-kills the execution the moment it exceeds the budget. This is what
makes untrusted ladders safe to run, not grammar restrictions.

Every primitive in LADR is:

- **First-class** — has a named place in the grammar.
- **Runtime-bounded** — the engine accounts for its resource consumption.
- **Statically analyzable** — types, references, schema well-formedness, and structure
  are all checkable without running. Termination is *not* statically guaranteed
  (undecidable for TC languages); resource ceilings enforce it at runtime instead.

---

## The SQL Analogy

LADR is to inference-time reasoning what SQL is to data queries:

| Property | SQL | LADR |
|---|---|---|
| Declarative | yes — you say *what* data you want | yes — you say *what* reasoning shape you want |
| Engine-executed | the DB optimizer picks the plan | the LADR engine runs loops/steps/nodes |
| Analyzable | query plans, schema validation | static validation, manifest analysis |
| Optimizable | indexes, caches | knobs (branches/iterations), gates, reliks |
| Bounded | transactions, timeouts | spend cap, hop/LLM-call ceilings |

---

## File Format

A ladder is authored as a YAML config in Foundry (or any text editor) and
stored as JSON in PostgreSQL. The schema is strict: unknown top-level keys
are rejected.

The minimal ladder is:

```yaml
name: Echo
allowedTargets: { strategy: universal }
exit: answer
knobs: {}
steps:
  - id: answer
    name: Answer
    type: normal
    systemPrompt: "Return the input verbatim."
```

See the language grammar in the [parser source](../../backend/internal/ladr/parse.go) and [types](../../backend/internal/ladr/types.go).

---

## What LADR Is Not

- **Not a general-purpose programming language.** LADR is declarative, analyzable, bounded.
- **Not a framework or library.** It is a *language* + *engine* + *memory* + *platform*.
- **Not a wrapper around frontier models.** It orchestrates calls to any model the
  caller supplies. Structure at inference time — multi-step search, verification,
  reflexion — is where the leverage comes from, not from a specific model.

---

## Source of Truth

The LADR grammar is defined in [`backend/internal/ladr/`](../../backend/internal/ladr/) (Go).
This `docs/` tree is the engineering reference for language concepts and engine semantics.
