# ADR-0096: Release safety — shadow runs, release identity, evals contract

Status: accepted · Origin: grill round 22 (2026-09-29)

## Decision

Core gains `RunMode{Primary, Shadow}`: in `Shadow`, `ReadOnly` tools run, `Idempotent`/`SideEffect` calls are answered from the primary `Journal` by `CallKey` or suppressed, writes go to a shadow session, cost is charged separately, suspensions are terminal. `Build` computes a `ReleaseManifest` (model versions, prompt/tool/skill/chain/definition hashes) with a deterministic `ID`; `RunInfo` carries `ReleaseID`, `Variant` (frozen rollout flag) and `Mode`, propagated to spans, audit, `Done` and all metrics. The `evals` module is specified: SHA-pinned datasets, delta gating bands, judge ×3 median with spread flag, pairwise shadow runner, session import through the `Redactor`, online sampler as a `Done` hook. Traffic splitting and rollback stay with the flags/observability platform.

## Context and evidence

Shadow deployment is the recommended practice for prompt/model changes, yet published guides do not solve challenger side effects — the harness owns the journal and the effect classes, so it can. Without a release identity on runs, traces and metrics cannot be sliced by change, which every canary and rollback rule needs.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

New capability `release`; `identity.RunInfo` gains `ReleaseID`, `Variant`, `Mode`; `runtime.AgentRun` and `stores.Run` gain `Mode`; `build` computes the manifest; `telemetry` gains eval/shadow metrics and release/variant attributes. Resolves Q15 and the shadow half of Q16; extends ADR-0032.
