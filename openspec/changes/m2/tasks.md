# Tasks: m2

The milestone plan. `proposal.md` is the scope, `design.md` the handoff, the acceptance matrix (63 rows) and the three milestone conflicts with their resolutions. This file holds two lists: what M2 authoring does before implementation, and which change implements which rows.

A task is done when its `verify:` command is green and `task lint` and `task test` are green for what it touched. Implementation tasks repeat the milestone definition of done: every listed scenario ID has a passing subtest named exactly by its ID, and `task lint`, `task spec` and `task test` are green.

## Authoring

- [x] 1. `openspec`: record the acceptance matrix and the milestone conflicts.
  - files: `openspec/changes/m2/design.md`
  - verify: `openspec validate m2 --strict --no-interactive` → `is valid`; every row in §2.1–§2.8 names one owner; the three conflicts in §3 carry a resolution.

- [x] 2. `openspec`: author the first-wave change packages — the three seams the rest of the matrix builds on.
  - files: `openspec/changes/{m2-context-projections,m2-sandbox-governance,m2-eino-runtime}/{proposal.md,design.md,tasks.md,specs/<cap>/spec.md}`
  - verify: each `openspec validate <name> --strict --no-interactive` → `is valid`; every scenario block copies the capability spec byte for byte.

- [x] 3. `openspec`: review the three packages before implementation admission.
  - observed: twelve findings — four blockers (the projection import cycle, the missing named-secret seam, the shutdown/cancellation contradiction, graph acceptance depending on excluded work), seven majors and one minor. Every one resolved in the packages: the seam moved beside `History`, `types.SecretResolver` plus `SetEnv` became the declared injection path, cancellation is separated from safe-point publication, and the graph row stayed `m2-eino-graph-native-resume`'s. A re-check confirms all three packages valid and their scenario blocks byte-identical.

## Implementation order

Each change is implemented only after its package is admitted (task 3). Shared driver files — `core/build.go`, `core/build_options.go`, `core/native_effects.go` — are edited in dependency order, never concurrently merely because two packages exist.

- [x] 1. First vertical seam of the three first-wave changes.
  - observed: `feat(context)` declares `stores.Projection`/`ContextPolicy`/`Compactor` and `ClearToolResults`; `feat(sandbox)` declares the sandbox port, `types.SecretResolver`, `WorkspaceLister` and the `Build` host-exec warning; `feat(runtime)` registers `ForeignSpec` and constructs governed runs on the send, resume and recovery paths. Floor green: `task lint` 0 issues, `task test`, `task spec` (`missing_in_scope=0`, `covered=391`).

- [x] 2. `m2-context-projections` — wire the stage into the native assembler (`WithContextPolicy`, ordered before the caller assembler, redaction, blob resolution and the operator fence untouched), then the public-path and invariant tests.
  - verify: `go test -short -timeout 2m ./core/... ./std/...` with the five `context.*` subtests passing.
  - observed: delivered and archived as `2026-10-07-m2-context-projections`; all five `context.*` ids covered with the invariant guard file inherited by the later compaction change.

- [x] 3. `m2-sandbox-governance` — the `std/sandbox` governed tool set: effect derivation, `PerCall` lifecycle with `Close` exactly once, `SetEnv` injection at `Open`, output spill to `OutputStore`, sandbox-seconds charging, secret-leak scanning and redaction.
  - verify: `go test -short -timeout 2m ./core/... ./std/...` with the seven `sandbox.*` and `identity.credential-not-in-sandbox` subtests passing.
  - observed: delivered; all eight owned scenario IDs green on the public path. The sandbox-seconds charge landed as the change's Stage B after the eino limit work, and the change archives with 23/23 tasks evidenced.

- [x] 4. `m2-eino-runtime` — the `adapter/eino` module: the turn-granularity runtime behind `runtime.Runtime`, the model/tool/message bridges, and the acceptance suite whose ids land in `adapter/eino` output.
  - verify: `go -C adapter/eino test -short -timeout 2m ./...` with the eino acceptance rows and `flow.backend-swap`, `limits.limits-under-foreign-backend` passing; `runtime.message-round-trip` proven for the eino leg only.
  - observed: delivered and archived; the conformance, lifecycle, preemption, health, parity, limits, mixing and bridge rows all green in `adapter/eino`, the six native scheduling rows stay green in `core`, and `runtime.message-round-trip` stays open for the adk-go leg.

- [x] 5. Second-wave lanes per `design.md` §2 — `m2-eino-bridges`, `m2-eino-graph-native-resume`, `m2-context-persisted-compaction`, `m2-provider-compaction`, `m2-context-calibration-overflow`, `m2-sandbox-adapter-conformance`, `m2-sandbox-lifecycle-resume`. Ticked for authoring, review and admission only: the two E2B implementation legs named below are deferred past M2 with the lane (`design.md` §3 conflict 1), not pending in this milestone.
  - verify: each package authored, reviewed and admitted before its implementation starts.
  - deferred: `archive/2026-10-12-m2-sandbox-adapter-conformance/tasks.md:49` (16/17, task 5.4) and `archive/2026-10-12-m2-sandbox-lifecycle-resume/tasks.md:80` (36/37, task 6.5) — the two legs that need an E2B credential, deferred-external past M2 with the `adapter/e2b` lane.
  - observed: all seven packages are authored, admitted and archived, each with its own `openspec validate … --strict` task ticked: `2026-10-11-m2-eino-bridges` 25/25, `2026-10-11-m2-context-persisted-compaction` 33/33, `2026-10-12-m2-eino-graph-native-resume` 12/12, `2026-10-12-m2-provider-compaction` 13/13, `2026-10-12-m2-context-calibration-overflow` 13/13, `2026-10-12-m2-sandbox-adapter-conformance` 16/17, `2026-10-12-m2-sandbox-lifecycle-resume` 36/37. The two incomplete tasks are the E2B legs (`m2-sandbox-adapter-conformance` task 5.4, `m2-sandbox-lifecycle-resume` task 6.5, the durable-enumeration admission), not admission failures: the verify covers authoring, review and admission for the seven named packages, and those two legs are deferred past M2 with the `adapter/e2b` lane.
  - evidence: `openspec/changes/archive/2026-10-11-m2-eino-bridges/tasks.md`, `…-m2-context-persisted-compaction/tasks.md`, `2026-10-12-m2-eino-graph-native-resume/tasks.md`, `…-m2-provider-compaction/tasks.md`, `…-m2-context-calibration-overflow/tasks.md`, `…-m2-sandbox-adapter-conformance/tasks.md:49`, `…-m2-sandbox-lifecycle-resume/tasks.md:80`; `openspec/changes/m2/design.md` §5.

- [ ] 6. The two example lanes — `m2-research-report-example` and `m2-data-analyst-example` — after their composed paths exist. The `m2-e2b-sandbox` lane is dropped from this item: `adapter/e2b` is deferred past M2 (`design.md` §3 conflict 1).
  - verify: each example package passes `openspec validate <name> --strict --no-interactive`; each example runs offline against its documented composed public paths; both `examples/research-report` and `examples/data-analyst` are listed in the `examples:run` task target.
  - open: neither example change has a package yet — `openspec/changes/` holds no `m2-research-report-example` or `m2-data-analyst-example` directory. Authoring both packages (proposal, design, tasks, spec deltas) is the first sub-task; each package names the composed public-path dependencies it accepts, the offline verify commands, and its scenario ids (existing capability ids keep their names; new composition ids use the owning capability's prefix plus an example qualifier).
  - done: `m2-docker-sandbox` is archived 24/24 with the adapter suite green against the live daemon; it supplied the durable-enumeration contract through `types.SandboxResources` (`design.md` §4.1), so no sandbox-port work remains in this item.
  - deferred: `m2-e2b-sandbox` and its two legs (`m2-sandbox-adapter-conformance` task 5.4, `m2-sandbox-lifecycle-resume` task 6.5) — blocked-external, not failed: no E2B credential, API URL or sandbox domain exists in the repository or the environment and no E2B dependency exists in any `go.mod`, so no in-repo work can turn those legs green.

- [x] 7. `m2-s4-acceptance`, `m2-api-freeze` last: the S4 graph acceptance and conformance suites green on eino, the port-method-set compatibility check, then `v1.0.0` at the milestone exit.
  - verify: `task api:check` clean against the frozen ports; the M2 gate reports `missing_in_scope=0`.
  - observed: `task api:check` diffs all seven modules against their own `v1.0.0` tags and prints `exported API compatible` for each (`.`, `adapter/anthropic`, `adapter/docker`, `adapter/eino`, `adapter/openai`, `adapter/otel`, `adapter/postgres`), exit 0, no `no tag yet, skipped` line. `task spec:gate GATE=M2` → `summary registered=663 subtests=2286 covered=568 missing=95 missing_in_scope=0 unregistered=0` at the freeze, every `missing` row carrying `deferred_to M3`/`M4`; re-run 2026-10-12 after `runtime.no-goroutine-leak` joined the registry: `registered=664 subtests=2289 covered=569 missing=95 missing_in_scope=0 unregistered=0`. `m2-s4-acceptance` is archived 11/11; `m2-api-freeze` is 15/15 with seven local tags cut at `00075cd` in release order (root first, then the six adapters).
  - evidence: `openspec/changes/archive/2026-10-12-m2-s4-acceptance/tasks.md`; `openspec/changes/m2-api-freeze/tasks.md` tasks 3.2, 3.3, 4.1; `docs/design/compatibility.md` *M2 freeze*; `git tag --list`.

## Milestone definition of done

- Every row in `design.md` §2 passes: `task spec:gate GATE=M2` reports `missing_in_scope=0`, and each row's evidence names the subtest that carries its id.
- `task lint`, `task spec`, `task test`, `task api:check` and `task examples:run` are green, each reported with its observed output.
- The three milestone conflicts in `design.md` §3 are reconciled in the artifacts that land, and `docs/overview.md`'s M2 row and exit line match what shipped.
- The port shapes recorded in the three first-wave designs are settled before the freeze: `runtime.Runtime` unchanged, `runtime.AgentRun` grown only when two shipped backends need it, `State.Backend` the only backend state carrier, `StepGranularity` two values, the sandbox port and `SecretResolver` as declared.
