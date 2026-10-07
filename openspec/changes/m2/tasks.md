# Tasks: m2

The milestone plan. `proposal.md` is the scope, `design.md` the handoff, the acceptance matrix (61 rows) and the three milestone conflicts with their resolutions. This file holds two lists: what M2 authoring does before implementation, and which change implements which rows.

A task is done when its `verify:` command is green and `task lint` and `task test` are green for what it touched. Implementation tasks repeat the milestone definition of done: every listed scenario ID has a passing subtest named exactly by its ID, and `task lint`, `task spec` and `task test` are green.

## Authoring

- [x] 1. `openspec`: record the acceptance matrix and the milestone conflicts.
  - files: `openspec/changes/m2/design.md`
  - verify: `openspec validate m2 --strict --no-interactive` → `is valid`; every row in §2.1–§2.7 names one owner; the three conflicts in §3 carry a resolution.

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

- [ ] 3. `m2-sandbox-governance` — the `std/sandbox` governed tool set: effect derivation, `PerCall` lifecycle with `Close` exactly once, `SetEnv` injection at `Open`, output spill to `OutputStore`, sandbox-seconds charging, secret-leak scanning and redaction.
  - verify: `go test -short -timeout 2m ./core/... ./std/...` with the seven `sandbox.*` and `identity.credential-not-in-sandbox` subtests passing.

- [x] 4. `m2-eino-runtime` — the `adapter/eino` module: the turn-granularity runtime behind `runtime.Runtime`, the model/tool/message bridges, and the acceptance suite whose ids land in `adapter/eino` output.
  - verify: `go -C adapter/eino test -short -timeout 2m ./...` with the eino acceptance rows and `flow.backend-swap`, `limits.limits-under-foreign-backend` passing; `runtime.message-round-trip` proven for the eino leg only.
  - observed: delivered and archived; the conformance, lifecycle, preemption, health, parity, limits, mixing and bridge rows all green in `adapter/eino`, the six native scheduling rows stay green in `core`, and `runtime.message-round-trip` stays open for the adk-go leg.

- [ ] 5. Second-wave lanes per `design.md` §2 — `m2-eino-bridges`, `m2-eino-graph-native-resume`, `m2-context-persisted-compaction`, `m2-provider-compaction`, `m2-context-calibration-overflow`, `m2-sandbox-adapter-conformance`, `m2-sandbox-lifecycle-resume`.
  - verify: each package authored, reviewed and admitted before its implementation starts.

- [ ] 6. `m2-docker-sandbox`, `m2-e2b-sandbox` after the sandbox port freezes; `m2-research-report-example`, `m2-data-analyst-example` after their composed paths exist.
  - verify: the adapter suite passes unchanged against both adapters; each example runs offline against its documented paths.

- [ ] 7. `m2-s4-acceptance`, `m2-api-freeze` last: the S4 graph acceptance and conformance suites green on eino, the port-method-set compatibility check, then `v1.0.0` at the milestone exit.
  - verify: `task api:check` clean against the frozen ports; the M2 gate reports `missing_in_scope=0`.

## Milestone definition of done

- Every row in `design.md` §2 passes: `task spec:gate GATE=M2` reports `missing_in_scope=0`, and each row's evidence names the subtest that carries its id.
- `task lint`, `task spec`, `task test`, `task api:check` and `task examples:run` are green, each reported with its observed output.
- The three milestone conflicts in `design.md` §3 are reconciled in the artifacts that land, and `docs/overview.md`'s M2 row and exit line match what shipped.
- The port shapes recorded in the three first-wave designs are settled before the freeze: `runtime.Runtime` unchanged, `runtime.AgentRun` grown only when two shipped backends need it, `State.Backend` the only backend state carrier, `StepGranularity` two values, the sandbox port and `SecretResolver` as declared.
