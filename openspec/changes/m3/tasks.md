# Tasks

## 1. Child changes

Each child is an independently reviewed OpenSpec change in this directory and carries its own `tasks.md`, deltas and scenario ids. This umbrella tracks only the M3 outcomes below.

- [ ] 1.1 `m3-postgres-schema-ownership` — `Migrate`/`Maintain` cycle, skew rule, time partitioning, schema lint, `storetest.Migrations` and `storetest.Bloat`.
- [ ] 1.2 `m3-redis-stores` — `adapter/redis` quota pools, admission and the shared EventLog.
- [ ] 1.3 `m3-adkgo-runtime` — `adapter/adkgo` runtime, bridges and workflow flows; the third conformance backend.
- [ ] 1.4 `m3-subflows` — nested suspension, limits and fan-out policy.
- [ ] 1.5 `m3-interop-mcp` — `adapter/mcp` client and server on the 2026-07-28 binding.
- [ ] 1.6 `m3-release-shadow` — shadow-run orchestration and manifest-driven release checks.
- [ ] 1.7 `m3-constrained-output` — the `Constrained` decoding surface.
- [ ] 1.8 `m3-s3-loadtest` — S3 disconnect storm, noisy-neighbor and the live-engine legs of the three workflow processes.

## 2. Frozen contracts for the children

These were fixed before authoring and do not change without a new decision.

- Every scenario id follows `<capability>.<name>` and stays unregistered until the child's own task 1.1 registers it as proposed (ADR-0171).
- ADR numbers, in authoring order: 0179 `m3-postgres-schema-ownership`, 0180 `m3-redis-stores`, 0181 `m3-adkgo-runtime`, 0182 `m3-subflows`, 0183 `m3-interop-mcp`, 0184 `m3-release-shadow`, 0185 `m3-constrained-output`, 0186 `m3-s3-loadtest`.
- Capability ownership for the identifiers the milestone extends: `Migrate`/`Maintain`, the expand/contract skew rule, `storetest.Migrations` and `storetest.Bloat` belong to `stores` (ADR-0169, `docs/design/adapters.md`); `Constrained` belongs to `structured-output`, whose spec declares the strategy set.
- Shared files serialize during implementation: `openspec/specs/stores/spec.md`, `openspec/scenarios.json`, `docs/design/types.md`, `Taskfile.yml` and `CHANGELOG.md`. Authoring runs in parallel because each child owns a disjoint change directory.

## 3. M3 integration acceptance

- [ ] 3.1 Run `task spec:gate GATE=M3` across all modules.
  Acceptance: The gate reports `missing_in_scope=0` for the M3 scope, and every remaining `missing` entry carries a named `deferred_to`.
- [ ] 3.2 Verify `conformance.*` green on all three backends — native, eino and adkgo — through each adapter's public path.
  Acceptance: The conformance subtests pass with `adapter/adkgo` present in the workspace and no scenario skipped.
- [ ] 3.3 Verify S3 green: the disconnect storm and the noisy-neighbor run under the performance gate.
  Acceptance: `task bench:gate` evidence for the S3 scenarios; a leg whose infrastructure is still absent carries a blocked-admission marker naming its external prerequisite instead of an unevidenced pass.
- [ ] 3.4 Run `task spec:coverage` and reconcile every M3 scenario.
  Acceptance: Each M3 scenario is covered by a subtest of its exact id or carries a named `deferred_to`.
- [ ] 3.5 Run `task spec`, `task lint`, `task test` and `task api:check` on the merged tree.
  Acceptance: All four exit 0.
