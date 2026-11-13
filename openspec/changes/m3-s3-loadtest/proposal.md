# Proposal

## Why

M2 proved correctness on the happy path; it never proved the harness survives a hostile environment. The S3 load test is the milestone's resilience acceptance: a backend losing its connection mid-run while every run must still complete under exactly-once semantics, and one run's load must not starve another run inside the same stack. Beside it sit the live-engine legs of the three workflow processes — `kafka-refunds`, `temporal-travel`, `camunda-invoice` — whose offline acceptance legs shipped with M0.5 and whose offline runs are green on master today (`task examples:test`, `task examples:run` cover them). Only live execution against real engines remains, and it needs a home: this change.

## What Changes

- `performance` gains the disconnect storm requirement: a fault-injecting store drops connections mid-run and every affected run completes exactly once, keyed by `OperationID` dedup and journal fingerprints, with the surviving-run budget stated as a load-test gate beside the existing benchmark gate.
- `performance` gains the noisy-neighbor requirement: concurrent runs at mixed load share one stack (stores, journal, EventLog, chain) and an unloaded run's latency stays within a stated bound while a neighbor saturates the stack.
- A load harness lives under `testkit/loadtest` in the root module: a fault-injecting store wrapper plus a bounded run driver, reused by both suites and by the live legs.
- The three live-engine legs are acceptance runs of the existing `engines` requirements under load: `engines.redelivery-returns-the-same-result`, `engines.duplicate-workers` and `engines.crash-after-consume` executed against real Kafka, Temporal and Camunda/Zeebe while the load suites run. A leg whose infrastructure is absent carries a blocked-admission marker naming its external prerequisite, per the `m2-e2b-sandbox` pattern (`openspec/changes/m2-e2b-sandbox/proposal.md`, `.spego/artifacts/decision/defer-e2b-sandbox-admission.md`).

No `engines` delta: every live leg asserts an existing normative `engines` scenario under load; no new requirement text or scenario id is introduced for the engines capability. The delta below is `performance`-only.

## Capabilities

### New Capabilities

- (none)

### Modified Capabilities

- `performance`: the disconnect storm and noisy-neighbor load requirements, with their scenarios gated like the existing benchmark scenarios; the three live-engine leg scenarios live here too because they are acceptance runs measured and reported by this capability's harness.

## Impact

Affected code: a new `testkit/loadtest` package (fault-injecting store wrapper, run driver), the load suites under the root module, and Taskfile wiring for `task bench`, `task bench:gate` and the live-leg runner beside `task examples:run`. No frozen port moves: the fault-injecting wrapper implements the existing store ports, and dedup relies on the existing `ErrOperationExists` and journal fingerprint contracts.

Dependencies: the `performance` benchmark gate (`task bench:gate`, `openspec/specs/performance/spec.md`) for the gate pattern; the `engines` spec for the semantics each live leg asserts; the three example processes for the live workload.

Unmet admission: no Kafka broker, Temporal server or Camunda/Zeebe broker is provisioned in this repository or its CI. The three live legs are authored, reviewed and gated, but each carries a blocked-admission marker and stays unchecked until its infrastructure exists; they are reported blocked, never passed. The storm and noisy-neighbor suites need no external infrastructure and are unblocked.
