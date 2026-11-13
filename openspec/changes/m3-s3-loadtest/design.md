# Design

## Context

The `performance` capability owns the benchmark gate: frozen budgets on a reference machine, base/head comparison, 5% tolerance, and the rule that a missing measurement is never a pass. The S3 load test extends this capability with resilience acceptance the benchmark gate does not cover: sustained concurrency, mid-run infrastructure failure, and fairness between concurrent runs. The `engines` capability already states exactly-once semantics for external engines (`engines.redelivery-returns-the-same-result`, `engines.duplicate-workers`, `engines.crash-after-consume`); what is missing is their execution against real engines, since only offline acceptance legs shipped with M0.5.

The `m2-e2b-sandbox` change established the blocked-admission pattern: a task is authored and reviewed with its evidence line and unblock condition stated, marked blocked, and reported blocked rather than passed while its external prerequisite is absent. This change applies the same pattern to the three live-engine legs.

## Goals / Non-Goals

Goals:

- One harness, `testkit/loadtest`, serving the storm suite, the noisy-neighbor suite and the live-leg driver.
- The disconnect storm: connection loss mid-run, every run completes exactly once.
- Noisy neighbor: one saturating run does not push an unloaded neighbor beyond a stated bound.
- The three live legs, gated behind their infrastructure with blocked-admission markers.

Non-Goals:

- No new engines requirements: the legs execute existing normative scenarios.
- No re-authoring of the offline legs of the three processes; they shipped with M0.5 and stay green via `task examples:test`.
- No production-grade chaos tooling; the fault-injecting wrapper is deterministic and test-local.
- No new store port methods; the wrapper sits behind the existing store interfaces.

## Decisions

### D1: Harness in `testkit/loadtest`

A root-module package with two pieces: a fault-injecting store wrapper implementing the existing store ports, which fails the next N operations or drops the connection for a bounded window on command, and a run driver that starts K concurrent runs against one stack, observes completion states, and reports per-run wall time and dedup outcomes. Both suites and the live-leg driver reuse it. The package carries no scenario id; suites and legs own the ids.

### D2: Disconnect storm semantics

The storm starts K runs sharing one stack, injects a connection drop on the backing store mid-run (during writes for some runs, during reads for others), then drives every run to completion. Exactly-once is asserted through the existing contracts: dedup by `OperationID` returns `ErrOperationExists` before re-execution, journal fingerprints identify replayed work, and `Recover` re-drives interrupted runs exactly once (`engines.crash-after-consume` semantics apply to the run level even without an external engine). No run may execute a component twice, and no run may be reported complete twice.

### D3: Noisy-neighbor bound

K-1 runs saturate the stack while one unloaded neighbor run executes at steady state. The requirement: the neighbor's per-step latency stays within a stated multiple of its isolated latency, measured on the reference machine and gated like the benchmark table. The bound is a ratio over the same run's isolated measurement, not an absolute number, so the gate travels with the machine class the way `bench:gate` does.

### D4: Live legs are gated acceptance runs, not new requirements

Each leg runs its example process against the real engine while the storm and neighbor suites generate background load on the same stack, and asserts the existing `engines` scenario ids as subtests named exactly their id. Because no new normative text is needed, this change ships no `engines` delta; the legs are scenarios under `performance` that bind the existing engines ids to load conditions.

### D5: Blocked admission for the live legs

No Kafka broker, Temporal server or Camunda/Zeebe broker exists in this repository or CI. Each live-leg task carries a blocked-admission marker naming its exact prerequisite, stays unchecked until a live run is observed, and is reported blocked, never passed — the `m2-e2b-sandbox` pattern. The storm and neighbor suites have no external prerequisite and are unblocked.

## Risks / Trade-offs

- **Flaky storm suite.** A deterministic fault schedule (fail next N ops, fixed drop window) keeps the suite reproducible; randomized schedules are rejected.
- **Neighbor bound too tight or too loose.** The ratio is baselined on the reference machine in the first apply round and then frozen, mirroring how ADR-0135 froze the benchmark budgets; the first run records, later runs gate.
- **Live legs drift from engine versions.** Each leg pins the engine version it was evidenced against; the unblock condition includes the version, so a later bump re-runs the leg.
- **Background load perturbing live-leg evidence.** The legs assert dedup and completion semantics, not latency budgets, so load changes wall time but not the pass criteria.

## Migration Plan

No migration: the harness, suites and legs are additive. `task bench` and `task bench:gate` keep their current meaning; the load suites join the gate wiring in their own targets beside `task examples:run`. Nothing existing moves.

## Acceptance strategy

The storm and neighbor suites run in CI as bounded, deterministic `go test` runs and gate like the benchmark paths. The live legs are validated by `openspec validate` and reported blocked; each becomes green only when its infrastructure exists and a live run is observed.
