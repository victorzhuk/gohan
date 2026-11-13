# Proposal

## Why

M3 is the backend and workflow milestone behind the M2 acceptance floor: nested sub-flows, MCP interop, shadow release checks, the adk-go backend, PostgreSQL schema ownership, the Redis stores, constrained decoding and the S3 load test. The stub also carried scope that belongs to M4 and offline example legs that M0.5 already delivered, so the scope is corrected here and split into the eight children below.

## What Changes

- `stores` gains the PostgreSQL migration cycle, the expand/contract skew rule, time partitioning, the schema lint and the storetest migration and bloat suites.
- A new `adapter/redis` module serves quota pools, admission control and one shared EventLog.
- A new `adapter/adkgo` module serves the adk-go runtime, its bridges and workflow flows, and becomes the third conformance backend beside native and eino.
- `subflows` gains nested suspension, nested limits and the fan-out policy.
- `interop` gains the MCP client and server on the 2026-07-28 binding.
- `release` gains shadow-run orchestration and manifest-driven release checks.
- `structured-output` gains the `Constrained` decoding surface.
- `performance` gains the S3 load test: the disconnect storm and the noisy-neighbor bounds, beside the live-engine legs of the three workflow processes.

## Children

| Child | Scope | Governing ADRs |
|---|---|---|
| `m3-postgres-schema-ownership` | `Migrate`/`Maintain` cycle, skew rule, time partitioning, schema lint, `storetest.Migrations` and `storetest.Bloat` | ADR-0169, ADR-0073 |
| `m3-redis-stores` | `adapter/redis`: quota pools, admission, shared EventLog | ADR-0055 |
| `m3-adkgo-runtime` | `adapter/adkgo`: runtime, bridges, workflow flows, third conformance backend | ADR-0061 |
| `m3-subflows` | Nested suspension, limits, fan-out policy | ADR-0091 |
| `m3-interop-mcp` | `adapter/mcp` client and server on 2026-07-28 | ADR-0074, ADR-0092 |
| `m3-release-shadow` | Shadow-run orchestration, manifest-driven release checks | ADR-0096 |
| `m3-constrained-output` | `Constrained` decoding surface | — |
| `m3-s3-loadtest` | S3 disconnect storm, noisy-neighbor, live-engine legs | — |

Each child is an independently reviewed change with its own tasks, deltas and scenario ids. This umbrella tracks only their M3 outcomes and the integration acceptance.

## Capabilities

### New Capabilities

None. M3 extends existing capabilities; its adapter modules implement frozen ports.

### Modified Capabilities

- `stores`: PostgreSQL schema ownership, the migration cycle, partitioning and maintenance, and the Redis-backed stores.
- `subflows`: nested suspension, limits and fan-out policy.
- `interop`: MCP client and server on the 2026-07-28 binding.
- `release`: shadow runs and manifest-driven release checks.
- `structured-output`: the `Constrained` decoding surface.
- `performance`: the S3 disconnect storm and noisy-neighbor bounds.
- `engines`: live-engine legs for the three workflow processes.

## Impact

Implementation touches `core`, `std`, new adapter modules, PostgreSQL and Redis, the performance gate, the Taskfile and the workspace manifests. No frozen port method changes; additions arrive as optional interfaces with their own ADR and a regenerated types index.

`support-triage`, `ops-agent` and `pr-review` move to M4. `kafka-refunds`, `temporal-travel` and `camunda-invoice` keep only their live-engine legs, inside `m3-s3-loadtest`, because their offline acceptance legs shipped with M0.5.
