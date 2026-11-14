# ADR-0181: The adk-go runtime backend targets adk v2 and falls back to Replay for backend gaps

Status: Proposed (m3-adkgo-runtime)

## Decision

`adapter/adkgo` implements the frozen runtime surface over `google.golang.org/adk/v2` (`agent.Context`, graph engine, native pause/resume, per ADR-0061) and becomes the third backend of the shared conformance suite beside `native` and `eino`. Where adk-go v2 carries no mechanism for a gohan capability, the adapter falls back to `Replay`: it records the step result and re-executes `Step` over recorded results, and it declares the fallback per capability rather than falling back silently. Q3 is settled accordingly: target v2, `Replay` fallback, verified against the v2 tag pinned in `adapter/adkgo/go.mod`.

## Context and evidence

ADR-0002 fixes three runtimes from the start. The conformance clause in the `runtime` capability names all three backends, and the m2-eino-bridges leg table records `adkgo` as pending. Q3 in `docs/overview.md` 13 notes that source fetches disagree on v1 while the v2 module path and feature set are consistent; the frozen port surface (`runtime.Runtime`, `AgentRun`, `State.Backend`, the two `StepGranularity` values) leaves no room for a per-backend state field or a third granularity, so the backend must map its behavior onto the frozen seam. The `Replay`-as-step-re-execution contract (`engines.replay-is-step-re-execution`) already defines the fallback semantics for any runtime.

## Consequences

The shared suites stay free of an adk-go import: codecs enter them as function values, as in the eino change. The `Runtime.Granularity` reach into `Explain` must show the adkgo backend's `Turn` value. A tag bump in `adapter/adkgo/go.mod` re-runs the conformance suite as its arbiter. Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
