# Tasks

## 1. Authoring

- [ ] 1.1 Assign the five owned scenario IDs and confirm the deferred split. Record that persisted compaction, summaries, calibration and provider compaction stay with their later owners.
  - evidence: `openspec/scenarios.json:939-968` (the five `context.*` rows carry `deferred_to: "M2"`); `openspec/changes/m2/design.md:53-73` (owner rows); package delta lists exactly `context.projection-does-not-write`, `context.projection-deterministic`, `context.keep-last-n`, `context.notes-excluded`, `context.clear-at-least`.
- [ ] 1.2 Map the assembly seam and confirm the stage position: projections before the caller assembler, blob resolution, redaction and the operator-turn fence after it.
  - evidence: `core/native_effects.go:202-243` (`assemblyInput`, `nativeAssemble` stage order); design D1.
- [ ] 1.3 Record the counting seam and reuse the shipped estimator port instead of a second token counter.
  - evidence: `core/types/context_budget.go:16-33` (`ContextBudget`), `core/types/tokens.go:19` (`TokenCounter`), `std/context/truncate.go:20-42` (`WithTruncateEstimator`, `WithTruncateRatio`).
- [ ] 1.4 Define the public-path test seeding: a session log with six completed tool calls, an intercepting `SessionLog` that records appends, and a declared estimator so a firing is reproducible.
  - evidence: design D3, D5; `std/context/truncate_test.go:12-40` (`textEstimator`, `toolTurn`) supplies the existing estimator test shape.

## 2. Projection contract

- [ ] 2.1 Declare `Projection` with `Project(ctx context.Context, h History, p types.ModelProfile) (History, error)` and `ContextPolicy{Projections []Projection; Compactor Compactor}` in `core/stores`, beside `History`. It cannot be declared in `core/types`: `core/stores` already imports `core/types` (`core/stores/session.go:8`), so a method set naming `stores.History` from `core/types` would close an import cycle, and moving `History` down to the floor is a wider migration this change does not need. Add `core/stores/projection_test.go` with `TestProjectionSatisfiesTruncate` and `TestContextPolicyFields`, so `Truncate` satisfies the interface with no signature change and the frozen struct gains no field in the later compaction change.
  - verify: `go test -timeout 2m ./core/stores -run 'TestProjectionSatisfiesTruncate|TestContextPolicyFields'`
  - evidence: `core/stores/projection.go` (`Projection`, `ContextPolicy`, `Compactor`), `core/stores/projection_test.go`; `std/context/truncate.go:55` (`Truncate.Project`); design D2 and the pre-freeze shape list.
- [ ] 2.2 Bind the resolved policy to the native flow through the existing option surface, leaving a flow with no policy on today's code path. Add `TestWithContextPolicy` and `TestNoContextPolicyUnchanged` in `core`.
  - verify: `go test -timeout 10m ./core -run 'TestWithContextPolicy|TestNoContextPolicyUnchanged'`
  - evidence: `core/build_options.go` (`WithContextPolicy`); `core/native_options.go` (`NativeSpec`, `MemoryPolicy` as the neighbouring declared-policy shape).

## 3. Stage placement in the native wrapper

- [ ] 3.1 Apply the declared projections in order inside `nativeAssemble`, before the caller assembler, over a copy of the loaded messages carrying the current history version. Keep blob resolution, redaction and the operator-turn fence after the assembler, unchanged. Add `TestProjectionAssemblyOrder` in `core/projection_assembly_test.go`.
  - verify: `go test -timeout 10m ./core -run 'TestProjectionAssemblyOrder'`
  - evidence: `core/native_effects.go:215-243` (`nativeAssemble`); binds `context.projection-deterministic` for the order assertion.
- [ ] 3.2 Prove determinism over an unchanged history version: two assemblies of the same `History.Version` for the same profile produce identical projected request bytes. Register `TestProjectionAssembly` with the subtest named exactly `context.projection-deterministic`.
  - verify: `go test -timeout 10m ./core -run 'TestProjectionAssembly/context\.projection-deterministic'`
  - evidence: `core/projection_assembly_test.go` (`context.projection-deterministic`).
- [ ] 3.3 Prove the projection is a view: after a firing, `SessionLog.Load` returns the full results and `History.Version` is unchanged, and no append reached the intercepting log. Register the subtest named exactly `context.projection-does-not-write` under `TestProjectionAssembly`.
  - verify: `go test -timeout 10m ./core -run 'TestProjectionAssembly/context\.projection-does-not-write'`
  - evidence: `core/projection_assembly_test.go` (`context.projection-does-not-write`); design D1 pass-by-value note.
- [ ] 3.4 Cover `Build`, `Send` and `Resume` on the public path with an intercepting session log; unit-only tests do not satisfy acceptance. Add `TestProjectionPublicPath` in `core/projection_scenarios_test.go`.
  - verify: `go test -timeout 10m ./core -run 'TestProjectionPublicPath'`
  - evidence: `core/projection_scenarios_test.go` (`context.projection-does-not-write`, `context.projection-deterministic`).

## 4. `ClearToolResults` retention and exclusion

- [ ] 4.1 Implement the identification and count: resolve each `ToolResult`'s tool through its matching `ToolUse.ID`, record every `ToolUse` in history order so a pending use still occupies a window slot, take the last `Keep` tool uses as the retained window, and leave results outside the window as candidates only. Preserve every `ToolUse` block and every `ToolResult` position, and leave results inside the retained window intact. Register the subtest named exactly `context.keep-last-n` under `TestClearToolResults`.
  - verify: `go test -timeout 2m ./std/context -run 'TestClearToolResults/context\.keep-last-n'`
  - evidence: `std/context/clear_tool_results.go` (`Project`); `std/context/clear_tool_results_test.go` (`context.keep-last-n`); design D3.
- [ ] 4.2 Never clear a result whose tool is in `Exclude`, and never clear `notes_read` or `notes_write` whatever the caller's `Exclude` lists. Register the subtest named exactly `context.notes-excluded` under `TestClearToolResults`.
  - verify: `go test -timeout 2m ./std/context -run 'TestClearToolResults/context\.notes-excluded'`
  - evidence: `std/context/clear_tool_results.go` (`excluded`); `std/context/clear_tool_results_test.go` (`context.notes-excluded`); `core/tool_names.go:17-28` (`reservedToolNames`).
- [ ] 4.3 Replace eligible content with the fixed deterministic placeholder while preserving `ID`, `Outcome`, `Error`, `Ref` and block origin. Add `TestClearToolResultsPlaceholderKeepsIdentity` in `std/context`.
  - verify: `go test -timeout 2m ./std/context -run 'TestClearToolResultsPlaceholderKeepsIdentity'`
  - evidence: `std/context/clear_tool_results.go` (`placeholder`, `clearResult`); design D4.
- [ ] 4.4 Bind the estimator and ratio options so the counting rule matches `Truncate`, and return the history unchanged when no estimator is declared. Add `TestClearToolResultsEstimatorBinding` and `TestClearToolResultsNoEstimator` in `std/context`.
  - verify: `go test -timeout 2m ./std/context -run 'TestClearToolResultsEstimatorBinding|TestClearToolResultsNoEstimator'`
  - evidence: `std/context/clear_tool_results.go` (`WithClearEstimator`, `WithClearRatio`); `std/context/truncate.go:20-42`.

## 5. Batched clearing

- [ ] 5.1 Decide `ClearAtLeast` through `ContextBudget` with the declared estimator. Cleared tokens are the positive difference between the original estimate and the candidate estimate, placeholder cost included. Register the subtest named exactly `context.clear-at-least` under `TestClearToolResults`.
  - verify: `go test -timeout 2m ./std/context -run 'TestClearToolResults/context\.clear-at-least'`
  - evidence: `std/context/clear_tool_results.go` (`clearedTokens`); `std/context/clear_tool_results_test.go` (`context.clear-at-least`).
- [ ] 5.2 Make a firing all-or-nothing: below `ClearAtLeast`, return the history unchanged, with no partial clearing. Add `TestClearToolResultsBelowThresholdNoPartial` in `std/context`.
  - verify: `go test -timeout 2m ./std/context -run 'TestClearToolResultsBelowThresholdNoPartial'`
  - evidence: `std/context/clear_tool_results.go` (`Project` early return); design D5.
- [ ] 5.3 Cover `Keep: 3` over six completed tool calls end to end on the public path: the three most recent results intact, the three older ones placeholders, all six `ToolUse` blocks present, and the notes result still intact. Register the subtests named exactly `context.keep-last-n` and `context.notes-excluded` under `TestProjectionPublicPath`.
  - verify: `go test -timeout 10m ./core -run 'TestProjectionPublicPath/(context\.keep-last-n|context\.notes-excluded)'`
  - evidence: `core/projection_scenarios_test.go` (`context.keep-last-n`, `context.notes-excluded`).

## 6. Invariants and package gates

- [ ] 6.1 Assert the four invariants this stage must not break, so the later persisted-compaction change inherits green checks: pending calls untouched, tool-use/result adjacency intact, taint window computed over the full log, and journal fingerprints unchanged by a clearing. Add `TestProjectionPendingCallUntouched`, `TestProjectionAdjacencyIntact`, `TestProjectionTaintOverFullLog` and `TestProjectionJournalFingerprintUnchanged` in `core/projection_invariants_test.go`.
  - verify: `go test -timeout 10m ./core -run 'TestProjectionPendingCallUntouched|TestProjectionAdjacencyIntact|TestProjectionTaintOverFullLog|TestProjectionJournalFingerprintUnchanged'`
  - evidence: `core/projection_invariants_test.go`; design §"Invariants the later persisted-compaction change must not break".
- [ ] 6.2 Add the five public-path tests named exactly `context.projection-does-not-write`, `context.projection-deterministic`, `context.keep-last-n`, `context.notes-excluded`, `context.clear-at-least`, and record `gohan.context.tokens_cleared` as owned by the later budget stage rather than emitting it here.
  - verify: `go test -timeout 10m ./core ./std/context -run 'TestProjectionAssembly|TestProjectionPublicPath|TestClearToolResults'`
  - evidence: `core/projection_scenarios_test.go`, `std/context/clear_tool_results_test.go`; `openspec/specs/context/spec.md` Metrics.
- [ ] 6.3 Run the repository gates after implementation and resolve failures.
  - verify: `task lint && task test && task spec && task api:check`
  - evidence: `task lint` 0 issues; `task test` green; `task spec` reports no missing scenario id for `context`; `task api:check` lists the new `Projection`/`ContextPolicy` surface before the v1 freeze.
- [ ] 6.4 Validate the authored change package.
  - verify: `openspec validate m2-context-projections --strict --no-interactive`
  - evidence: `Change 'm2-context-projections' is valid`.