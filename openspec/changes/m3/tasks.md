# Tasks

## 1. Child changes

Each child is an independently reviewed OpenSpec change in this directory and carries its own `tasks.md`, deltas and scenario ids. This umbrella tracks only the M3 outcomes below.

- [x] 1.1 `m3-postgres-schema-ownership` — `Migrate`/`Maintain` cycle, skew rule, time partitioning, schema lint, `storetest.Migrations` and `storetest.Bloat`.
  - observed: delivered in `c4a82e9`, archived as `2026-11-15-m3-postgres-schema-ownership`; the `stores` spec gained the ownership, partitioning and expand-before-contract rules.
- [x] 1.2 `m3-redis-stores` — `adapter/redis` quota pools, admission and the shared EventLog.
  - observed: delivered in `d78db04`, archived as `2026-11-15-m3-redis-stores` (`7a9d294`); the `stores` spec gained the shared quota pool and the Redis event log. Its nine scenario ids assert behind `//go:build full`, which the gate's redis leg now loads (see 3.1).
- [x] 1.3 `m3-adkgo-runtime` — `adapter/adkgo` runtime, bridges and workflow flows; the third conformance backend.
  - observed: archived as `2026-11-14-m3-adkgo-runtime`; `adapter/adkgo` binds `testkit/conformance` through `TestConformance`, green in 3.2.
- [x] 1.4 `m3-subflows` — nested suspension, limits and fan-out policy.
  - observed: delivered in `b68970e`, archived as `2026-11-15-m3-subflows`. Seven of its ids were deferred to "a later M3 slice"; that slice is now `m3-deferred-assertions` (see 3.1).
- [x] 1.5 `m3-interop-mcp` — `adapter/mcp` client and server on the 2026-07-28 binding.
  - observed: delivered in `9e2e7bc`, archived as `2026-11-15-m3-interop-mcp` (`6c31d7b`); `interop.awaiting-input-invalid` stays unasserted and belongs to `m3-deferred-assertions`.
- [x] 1.6 `m3-release-shadow` — shadow-run orchestration and manifest-driven release checks.
  - observed: delivered in `08efd05`, archived as `2026-11-15-m3-release-shadow` (`ab660db`); the `release` spec gained the shadow replay and the manifest-driven check enumeration, and the eight `ADR-0184` ids left the deferred list.
- [x] 1.7 `m3-constrained-output` — the `Constrained` decoding surface.
  - observed: delivered in `789f19d`, archived as `2026-11-15-m3-constrained-output` (`5ad8024`) with `--skip-specs`, because its `structured-output` delta was already synced into the live spec and a plain archive would apply it twice.
- [x] 1.8 `m3-s3-loadtest` — S3 disconnect storm, noisy-neighbor and the live-engine legs of the three workflow processes.
  - applied: 9 of 12 tasks green and seven of its twelve scenarios covered by passing subtests; the change stays active with blocked admission. Three live legs (`performance.live-kafka-refunds-under-load`, `performance.live-temporal-travel-under-load`, `performance.live-camunda-invoice-under-load`) are blocked on external Kafka, Temporal and Camunda/Zeebe infrastructure that exists neither in the repository nor in CI; each unblock condition, pinned-version slot and planned assertion is recorded in `openspec/changes/m3-s3-loadtest/lane-report.md` under "Live-engine legs — blocked admission". Task 3.2 stays open for the same reason it did in the change: `task bench:gate` needs `git worktree add`, which the local sandbox refuses on a read-only `.git` (see 3.3).
- [x] 1.9 `m3-shadow-core-enforcement` — core enforcement of the frozen shadow-run contract and release identity on native flows.
  - observed: delivered in `09ec65d`, archived as `2026-11-15-m3-shadow-core-enforcement` (`3176197`); it carries no spec delta, so archiving moved the package without touching `openspec/specs`.

## 2. Frozen contracts for the children

These were fixed before authoring and do not change without a new decision.

- Every scenario id follows `<capability>.<name>` and stays unregistered until the child's own task 1.1 registers it as proposed (ADR-0171).
- ADR numbers, in authoring order: 0179 `m3-postgres-schema-ownership`, 0180 `m3-redis-stores`, 0181 `m3-adkgo-runtime`, 0182 `m3-subflows`, 0183 `m3-interop-mcp`, 0184 `m3-release-shadow`, 0185 `m3-constrained-output`, 0186 `m3-s3-loadtest`.
- Capability ownership for the identifiers the milestone extends: `Migrate`/`Maintain`, the expand/contract skew rule, `storetest.Migrations` and `storetest.Bloat` belong to `stores` (ADR-0169, `docs/design/adapters.md`); `Constrained` belongs to `structured-output`, whose spec declares the strategy set.
- Shared files serialize during implementation: `openspec/specs/stores/spec.md`, `openspec/scenarios.json`, `docs/design/types.md`, `Taskfile.yml` and `CHANGELOG.md`. Authoring runs in parallel because each child owns a disjoint change directory.

## 3. M3 integration acceptance

- [ ] 3.1 Run `task spec:gate GATE=M3` across all modules.
  Acceptance: The gate reports `missing_in_scope=0` for the M3 scope, and every remaining `missing` entry carries a named `deferred_to`.
  - observed: blocked, not passed. On this tree the gate reports `summary registered=820 subtests=2913 covered=753 missing=67 missing_in_scope=22 unregistered=0 failed_legs=0`. Nine further ids it named before this round were invisible only because the redis leg skipped the `full` lane; the leg now passes `-tags full` beside the postgres leg, and those nine pass by their exact ids. The remaining twenty-two are nineteen registered and spec-named ids with no subtest anywhere — `subflows.*` (7), `release.*` (6), `interop.awaiting-input-invalid`, `model.version-drift`, `stores.pg-auto-migrate-refused-in-prod`, `structured-output.constrained`, `context.research-report-claim-boundary`, `taint.subflow-carries-origin` — plus the three blocked `performance.live-*` legs recorded at 1.8.
  - next: `openspec/changes/m3-deferred-assertions` owns the nineteen assertions and its task 5.1 requires this gate at `missing_in_scope=3`, the three blocked legs. No id was re-deferred to a later milestone to pass the gate.
  - evidence: `task spec:gate GATE=M3` output on HEAD of this round; `openspec/changes/m3-deferred-assertions/proposal.md` ("Why").
- [x] 3.2 Verify `conformance.*` green on all three backends — native, eino and adkgo — through each adapter's public path.
  Acceptance: The conformance subtests pass with `adapter/adkgo` present in the workspace and no scenario skipped.
  - observed: `go test -timeout 5m ./testkit/conformance/` ok (34.8s), `go -C adapter/eino test -timeout 5m ./...` ok, `go -C adapter/adkgo test -timeout 5m ./...` ok; both adapters bind the shared suite through `TestConformance`, and no scenario reported `skip`.
  - evidence: the three command results on HEAD; `adapter/eino/conformance_test.go:9`, `adapter/adkgo/conformance_test.go:9`.
- [x] 3.3 Verify S3 green: the disconnect storm and the noisy-neighbor run under the performance gate.
  Acceptance: `task bench:gate` evidence for the S3 scenarios; a leg whose infrastructure is still absent carries a blocked-admission marker naming its external prerequisite instead of an unevidenced pass.
  - observed: `task bench:gate` exits 0 with `verdict: PASS` — `BenchmarkModelCall_Raw` −2.38%, `BenchmarkModelChain` +0.79%, `BenchmarkToolCall_Raw` −4.03%, `BenchmarkToolChain_ReadOnly` −9.02% against the frozen baselines, then the advisory load half. The strict load half, `go run ./tools/loadgate --latency`, exits 0 on the same tree when it runs alone. In place the gate cannot run at all: it calls `git worktree add`, which the local sandbox refuses with `Read-only file system` on `.git/worktrees`; both halves were therefore measured on a full writable copy of the tree (`scratchpad/apicheck-copy`, byte-identical source, same commits), and `.github/workflows/performance.yml` runs both on the reference runner. The three live legs carry the blocked-admission markers at 1.8 instead of a pass.
  - evidence: `task bench:gate` output (`verdict: PASS`) and `go run ./tools/loadgate --latency` exit 0; `openspec/changes/m3-s3-loadtest/lane-report.md`.
- [x] 3.4 Run `task spec:coverage` and reconcile every M3 scenario.
  Acceptance: Each M3 scenario is covered by a subtest of its exact id or carries a named `deferred_to`.
  - observed: `summary registered=820 subtests=2830 covered=713 missing=107 missing_in_scope=0 unregistered=0 failed_legs=0` — no subtest is named by an unregistered id, and every uncovered id carries a named `deferred_to`.
  - evidence: `task spec:coverage` output; the M3-scope reconciliation is the 3.1 gate line.
- [x] 3.5 Run `task spec`, `task lint`, `task test` and `task api:check` on the merged tree.
  Acceptance: All four exit 0.
  - observed: `task spec` exits 0 with `missing_in_scope=0` at the default M0.5 gate and an unchanged types index; `task lint` reports 0 issues on all four legs (golangci-lint v2.14.0, the version CI pins); `task test` is green across every module including `examples`; `task api:check` reports the root compatible with `v0.3.0` and `adapter/otel` compatible with `adapter/otel/v0.1.0`. `api:check` shares the `.git` limitation of 3.3 and was run on the same writable copy; no incompatible delta exists, so nothing was forced.
  - evidence: the four command results; `Taskfile.yml` `api:check` and `spec:gate` targets.
