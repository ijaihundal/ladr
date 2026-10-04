# Adaptive reads (relikRead:)

> **This capability has moved.** The standalone `retrieve:` key has been removed. Adaptive
> multi-turn reads are now the flat `relikRead:` step key, documented in
> [Adaptive keys](tools.md).

The `relikRead:` step key enables a bounded on-demand fetch loop: inside a single step's
execution, the LLM sees a folder index, reasons about what is relevant, requests specific files,
gets them injected as a field, and continues. Termination is guaranteed by `maxRounds`.

See [Adaptive keys](tools.md) for the full `relikRead:` reference, and
[Reads and Writes](reads-writes.md) for how adaptive reads relate to the declarative `relikRead`
field type.
