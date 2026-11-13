## ADDED Requirements

### Requirement: Disconnect storm resilience

The load harness MUST drive concurrent runs through a deterministic mid-run store connection loss, and every affected run SHALL complete exactly once under the existing dedup and recovery contracts.

#### Scenario: disconnect storm exactly once
ID: `performance.disconnect-storm-exactly-once`
- WHEN the backing store drops its connection mid-run for concurrent runs sharing one stack
- THEN every run completes exactly once, dedup by `OperationID` returns `ErrOperationExists` before re-execution, and no component executes twice

#### Scenario: disconnect storm recovery
ID: `performance.disconnect-storm-recovery`
- WHEN a run is interrupted by the injected connection loss
- THEN `Recover` finds it `Resuming`, re-drives its `PendingInput` exactly once, and never reclaims a suspended run

### Requirement: Noisy neighbor isolation

A saturating run MUST NOT push an unloaded concurrent run beyond the frozen latency ratio over its isolated baseline on the reference machine, and a regression beyond the ratio MUST fail the load gate.

#### Scenario: noisy neighbor isolation
ID: `performance.noisy-neighbor-isolation`
- WHEN concurrent runs saturate the stack while one unloaded neighbor run executes at steady state
- THEN the neighbor's per-step latency stays within the frozen ratio over its isolated baseline and a regression beyond it fails the gate naming the run and the delta

### Requirement: Live-engine legs under load

Each workflow process leg MUST run against its real engine under background load and SHALL assert the existing `engines` exactly-once scenarios; a leg whose infrastructure is absent MUST be reported blocked, never passed.

#### Scenario: live kafka refunds under load
ID: `performance.live-kafka-refunds-under-load`
- WHEN `kafka-refunds` runs against a real Kafka broker while the storm suite generates background load
- THEN `engines.redelivery-returns-the-same-result` and `engines.crash-after-consume` pass as subtests named exactly their ids, with the broker version recorded

#### Scenario: live temporal travel under load
ID: `performance.live-temporal-travel-under-load`
- WHEN `temporal-travel` runs against a real Temporal server under background load
- THEN `engines.redelivery-returns-the-same-result` and `engines.duplicate-workers` pass as subtests named exactly their ids, with the server version recorded

#### Scenario: live camunda invoice under load
ID: `performance.live-camunda-invoice-under-load`
- WHEN `camunda-invoice` runs against a real Zeebe broker under background load
- THEN `engines.redelivery-returns-the-same-result` and `engines.duplicate-workers` pass as subtests named exactly their ids, with the broker version recorded
