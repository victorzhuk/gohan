# Change: m2

Stub: scope only; tasks are written when the previous milestone's M-review closes.

## Scope

adapter/eino (runtime, bridges, graph flow, Native resume), `context` (projections, persisted compaction, `ProviderCompact` caps), `sandbox` (port, `std/sandbox`, adapter/docker), `research-report` and `data-analyst` examples.

`adapter/e2b` was in this scope and is deferred past M2: no E2B credential, API URL or sandbox domain exists in the repository or the environment, so its lane and the two E2B legs that depend on it move past the milestone. `design.md` §3 conflict 1 carries the resolution and `design.md` §4.1 the durable-enumeration contract that replaces the E2B-side dependency inside M2.

## Exit

See the milestone table in `docs/overview.md`.
