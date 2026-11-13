# ADR-0186: S3 load test — disconnect storm, noisy neighbor and live-engine legs

Date: 2026-11-11
Status: Proposed (M3, `m3-s3-loadtest`)
Supersedes: none

## Context

The `performance` capability owns the benchmark gate: frozen budgets on a reference machine, base/head comparison and the rule that a missing measurement is never a pass. That gate proves overhead bounds, not resilience. M3's exit criterion includes the S3 load test: a backend losing its connection mid-run with every run surviving exactly-once semantics, and a noisy-neighbor bound between concurrent runs sharing one stack. The three workflow processes (`kafka-refunds`, `temporal-travel`, `camunda-invoice`) shipped their offline acceptance legs with M0.5 and are green as examples on master; only their live-engine execution remains.

## Decision

- The load harness lives in `testkit/loadtest` in the root module: a fault-injecting store wrapper over the existing store ports (deterministic schedule: fail next N operations, drop the connection for a bounded window) and a concurrent run driver reporting per-run completion and wall time. One harness serves the storm suite, the neighbor suite and the live-leg driver. The wrapper adds no store port method.
- The disconnect storm is a deterministic suite, not chaos tooling: a fixed fault schedule keeps runs reproducible. Exactly-once is asserted through the existing contracts — `OperationID` dedup returning `ErrOperationExists` before re-execution, journal fingerprints identifying replayed work, and `Recover` re-driving an interrupted run exactly once.
- The noisy-neighbor bound is a ratio over the same run's isolated baseline, measured on the reference machine, baselined in the first apply round and then frozen, gating beside `task bench:gate` the way the benchmark table does. A ratio travels with the machine class; an absolute number would not.
- The live-engine legs are gated acceptance runs of existing normative `engines` scenarios under background load; no new `engines` requirement is authored, so the change ships a `performance`-only delta. Each leg binds the existing ids (`engines.redelivery-returns-the-same-result`, `engines.duplicate-workers`, `engines.crash-after-consume`) as subtests named exactly their id and pins the engine version it was evidenced against.
- A live leg whose infrastructure is absent carries a blocked-admission marker naming its exact prerequisite — Kafka broker, Temporal server, Zeebe broker — and is reported blocked, never passed, following the `m2-e2b-sandbox` pattern.

## Consequences

- The storm and neighbor suites are unblocked, deterministic and CI-gated from their first apply round; the live legs stay blocked until their infrastructure is provisioned, at which point the authored tasks run as written.
- The neighbor ratio must be baselined before it gates; the first run records, later runs enforce.
- A live engine version bump re-runs that leg, since the evidence is pinned to a version.

## References

- `openspec/specs/performance/spec.md` — the benchmark gate this change extends.
- `openspec/specs/engines/spec.md` — the exactly-once scenarios the live legs assert.
- `openspec/changes/m2-e2b-sandbox/proposal.md` — the blocked-admission pattern.
