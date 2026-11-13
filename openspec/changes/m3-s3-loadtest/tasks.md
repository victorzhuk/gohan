# Tasks: m3-s3-loadtest

A task is done when its listed scenario ids pass and `task lint`, `task spec` and `task test` are green. The storm and noisy-neighbor scenarios have no external prerequisite. The three live-engine legs are blocked on external infrastructure: a task marked **blocked** stays unchecked until a live run is observed and is reported blocked, never passed (`m2-e2b-sandbox` pattern, `.spego/artifacts/decision/defer-e2b-sandbox-admission.md`).

## 1. Harness and registration

- [ ] 1.1 Register the scenario ids of this change in `openspec/scenarios.json` as proposed until admission, following the ADR-0171 pattern. Verify `timeout 60s task spec:types` is clean after the delta lands.
  - Acceptance: `openspec/scenarios.json` carries the five proposed ids; `task spec:types` exits zero.
  - evidence: the registry diff; the types index run output
- [ ] 1.2 Create `testkit/loadtest` (D1): the fault-injecting store wrapper over the existing store ports (fail next N operations, drop the connection for a bounded window, deterministic schedule) and the concurrent run driver reporting per-run completion and wall time. Verify `timeout 300s go build ./...` and `timeout 120s go test -timeout 2m ./testkit/loadtest/` are green.
  - Acceptance: the wrapper implements the store ports without a port change; the driver starts K concurrent runs against one stack.
  - evidence: `testkit/loadtest/fault.go`, `testkit/loadtest/driver.go`; the test run output

## 2. Disconnect storm

- [ ] 2.1 Author the storm suite: K concurrent runs over one stack, a deterministic mid-run connection drop on the backing store (drop during writes for some runs, during reads for others), then every run driven to completion. Assert exactly-once per D2: dedup by `OperationID` returns `ErrOperationExists` before re-execution, journal fingerprints identify replayed work, no component executes twice, no run reports completion twice. Verify `timeout 600s go test -timeout 10m ./core/ -run TestLoadDisconnectStorm` is green.
  - Acceptance: `performance.disconnect-storm-exactly-once` passes as a subtest named exactly its id.
  - evidence: the suite file; the observed green run naming the scenario id
- [ ] 2.2 Assert recovery within the storm: an interrupted run is found `Resuming` by `Recover`, its `PendingInput` is re-driven exactly once, and suspended runs are never reclaimed mid-storm. Verify `timeout 600s go test -timeout 10m ./core/ -run TestLoadDisconnectStorm/recovery` is green.
  - Acceptance: `performance.disconnect-storm-recovery` passes.
  - evidence: the suite subtest; the observed green run

## 3. Noisy neighbor

- [ ] 3.1 Author the neighbor suite (D3): K-1 runs saturate the stack while one unloaded neighbor runs at steady state; record the neighbor's per-step latency against its isolated baseline on the reference machine and assert the frozen ratio. Verify `timeout 600s go test -timeout 10m ./core/ -run TestLoadNoisyNeighbor` is green locally (advisory) and the ratio is recorded for the gate.
  - Acceptance: `performance.noisy-neighbor-isolation` passes with the baseline recorded.
  - evidence: the suite file; the recorded baseline; the observed run
- [ ] 3.2 Gate the neighbor bound beside the benchmark gate: add the ratio to the load-test gate wiring invoked by `task bench:gate` so a regression beyond the frozen ratio fails the gate naming the run and the delta, with a missing measurement never a pass. Verify `timeout 10m task bench:gate` exits zero on an unchanged tree.
  - Acceptance: the neighbor bound gates like `performance.regression-gate` does for benchmarks.
  - evidence: the gate wiring; the `bench:gate` output on an unchanged tree

## 4. Live-engine legs — blocked admission

- [ ] 4.1 **(blocked: no Kafka broker exists in this repository or CI)** Author and run the `kafka-refunds` live leg (D4): the process runs against a real Kafka broker while the storm suite generates background load, asserting `engines.redelivery-returns-the-same-result` and `engines.crash-after-consume` as subtests named exactly their ids, pinning the broker version in the evidence. Verify `timeout 10m go test -timeout 10m ./examples/ -run TestLoadLiveKafka` is green against a live broker.
  - Acceptance: `performance.live-kafka-refunds-under-load` passes; the offline leg stays green via `task examples:test`.
  - evidence: the observed run naming the engine scenario ids and the broker version
- [ ] 4.2 **(blocked: no Temporal server exists in this repository or CI)** Author and run the `temporal-travel` live leg: the process runs against a real Temporal server under background load, asserting `engines.redelivery-returns-the-same-result` and `engines.duplicate-workers`, pinning the server version. Verify `timeout 10m go test -timeout 10m ./examples/ -run TestLoadLiveTemporal` is green against a live server.
  - Acceptance: `performance.live-temporal-travel-under-load` passes.
  - evidence: the observed run naming the engine scenario ids and the server version
- [ ] 4.3 **(blocked: no Camunda/Zeebe broker exists in this repository or CI)** Author and run the `camunda-invoice` live leg: the process runs against a real Zeebe broker under background load, asserting `engines.redelivery-returns-the-same-result` and `engines.duplicate-workers`, pinning the broker version. Verify `timeout 10m go test -timeout 10m ./examples/ -run TestLoadLiveCamunda` is green against a live broker.
  - Acceptance: `performance.live-camunda-invoice-under-load` passes.
  - evidence: the observed run naming the engine scenario ids and the broker version
- [ ] 4.4 Report the three legs' admission state in the lane report: each blocked leg names its exact prerequisite and pinned engine version slot; none is reported passed. Verify the lane report lists the three blocked legs and their unblock conditions.
  - Acceptance: the report carries no unevidenced pass for any live leg.
  - evidence: the lane acceptance report

## 5. Lane acceptance

- [ ] 5.1 Run the unblocked floor: `timeout 600s task lint`, `timeout 300s task test`, `timeout 10m task bench`. Resolve every failure in the code this change touched. Verify all three exit zero.
  - Acceptance: the unblocked suites are green; only the live-leg tests skip or stay blocked.
  - evidence: the three command outputs
- [ ] 5.2 Validate the change package. Verify `timeout 30s openspec validate m3-s3-loadtest --strict --no-interactive` reports `is valid`.
  - Acceptance: strict validation passes.
  - evidence: the validate output
