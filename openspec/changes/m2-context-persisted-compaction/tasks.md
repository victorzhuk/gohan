# Tasks

## 1. Authoring

- [ ] 1.1 Assign the five owned scenario IDs and confirm the deferred split. Record that provider compaction, calibration and the overflow retry stay with their later owners.
  - evidence: `openspec/changes/m2/design.md:53-73` (owner rows: `context.compaction-persisted`, `context.replay-after-compaction`, `context.pending-call-survives`, `context.summary-guarded`, `context.compaction-audited` carry `m2-context-persisted-compaction`; `context.opaque-bound-to-model` and `context.opaque-fallback-rejected` are `m2-provider-compaction`); the delta in `openspec/changes/m2-context-persisted-compaction/specs/context/spec.md` reproduces the whole *Compaction is persisted* requirement, all seven scenarios, and claims the five.
- [ ] 1.2 Reconcile the `Compactor` seam with the Contract: change `Compact` to return `(types.Compaction, error)` in `core/stores`, keeping `ContextPolicy` field-for-field. Do not touch the frozen `Projection`, `History` or `ContextPolicy` field sets.
  - verify: `go build ./...`
  - evidence: `core/stores/projection.go:20-33` (declaration today); `openspec/specs/context/spec.md` *Contract* (`Compact(ctx, h History, p ModelProfile) (Compaction, error)`); design D3.
- [ ] 1.3 Record the audit surface the record needs: `AuditCompacted AuditKind`, and the additive `AuditRecord.Compaction *CompactionAudit` payload. State in the design that both are `stores` Contract code and land with the main-spec edit at apply time.
  - evidence: `core/stores/audit.go:20-66` (`AuditKind` consts and `AuditRecord` fields today, no compaction kind); `openspec/specs/stores/spec.md:284-311` (Contract); design D6.

## 2. Assembly collapses covered versions

- [ ] 2.1 Add the intrinsic collapse step to the native assembly composition, before the declared projections and independent of `ContextPolicy.Projections`: scan the loaded messages for the newest `types.Compaction` block, drop the first `CoversUpTo` messages, and render that block at the head of the view. A log with no compaction block returns the messages unchanged. Compose it at both call sites.
  - verify: `go test -timeout 10m ./core -run 'TestCompactionView'`
  - evidence: `core/native_effects.go:31` and `core/foreign_conversation.go:77` (composition sites); `core/native_effects.go:273-299` (`withProjections`); `core/types/message.go:156-172` (`Compaction`); design D2.
- [ ] 2.2 Prove append-and-hide on the public path: `Summarize` (or the test compactor) persists a block at `CoversUpTo: 40`, `SessionLog.Load` returns it, and the next assembly hides versions ≤ 40. Register the subtest named exactly `context.compaction-persisted`.
  - verify: `go test -timeout 10m ./core -run 'TestCompactionPublicPath/context\.compaction-persisted'`
  - evidence: new `core/compaction_scenarios_test.go`; `core/projection_scenarios_test.go` (`projScenarioStack`, `projScenarioSend`, `projScenarioResume`) is the public-path shape to mirror; `core/stores/session.go:31` (`Append`).

## 3. `Summarize` and the guard

- [ ] 3.1 Add `std/context.Summarize` as a `stores.Compactor` whose `SummarySource` supplies the `Summary` blocks; the folded block is `types.Compaction{Kind: CompactionText, CoversUpTo, Summary, Model, Tokens}`. A nil `SummarySource` returns an error instead of persisting an empty summary. Add `TestSummarizeProducesText` in `std/context`.
  - verify: `go test -timeout 2m ./std/context -run 'TestSummarizeProducesText'`
  - evidence: new `std/context/summarize.go`; `std/context/truncate.go:55` and `std/context/clear_tool_results.go` (sibling projections); `openspec/changes/m2/design.md` §3 item 2; design D4.
- [ ] 3.2 Guard the produced summary through `ConsultGuard` at `StageContext` before the append. On a blocked decision persist nothing, apply the flow's `Truncate` for the turn, emit `types.GuardBlocked`, and count `gohan.guard.context_rejected` with `provider="compaction"`. Register the subtest named exactly `context.summary-guarded`.
  - verify: `go test -timeout 10m ./core -run 'TestCompactionPublicPath/context\.summary-guarded'`
  - evidence: `core/telemetry.go:75` (`ConsultGuard`); `core/guards/guard.go:33-44` (`GuardInput`); `std/guard/context.go:51-71` (`Decide`); `core/types/telemetry.go:9-20` (`Telemetry.Count`); `openspec/specs/telemetry/spec.md:96` (`gohan.guard.context_rejected` by provider); design D5.

## 4. The drive compaction stage

- [ ] 4.1 Add the stage to the drive turn: when `policy.Compactor != nil` and the budget/trigger fires (`types.ContextBudget`/`types.NeedsCompaction`), run the compactor, clamp the returned `CoversUpTo` below the earliest pending `ToolUse`, guard the summary (3.2), and append the block through the run's `HistoryAppender`. A flow with no `Compactor` keeps today's path.
  - verify: `go test -timeout 10m ./core -run 'TestCompactionStage'`
  - evidence: `core/drive_turn.go:152` (`driveTurns`), `:252-271` (`modelEffect`); `core/drive_lifecycle.go:95` (`HistoryAppender`); `core/drive_turn.go:585-588` (`historyAppenderFrom`); `core/types/context_budget.go:16-58`; design D1.
- [ ] 4.2 Clamp `CoversUpTo` below a `ToolUse` whose result is absent from history (pending, `HumanApproval`-suspended or `Outcome: Unknown`), reusing the history-order scan shape of `scanUses`. Register the subtest named exactly `context.pending-call-survives`.
  - verify: `go test -timeout 10m ./core -run 'TestCompactionPublicPath/context\.pending-call-survives'`
  - evidence: `std/context/clear_tool_results.go` (`scanUses`); `core/types/message.go:143-153` (`ToolUse`, `ToolResult`); `core/projection_invariants_test.go` (`TestProjectionPendingCallUntouched`); design D3.

## 5. Persistence: audit, event, replay

- [ ] 5.1 Write the `Compacted` audit record and emit the `types.Compacted` event in the same stage, and count `gohan.context.compactions` by kind with `gohan.context.tokens_before`/`tokens_after` recorded from the block. Register the subtest named exactly `context.compaction-audited`.
  - verify: `go test -timeout 10m ./core -run 'TestCompactionPublicPath/context\.compaction-audited'`
  - evidence: `core/stores/audit.go:71-76` (`AuditLog.Append`); `core/types/event.go:92-99` (`Compacted`); `core/stores/events.go:322` (`Compacted` encoding); `openspec/specs/context/spec.md` Metrics; design D6.
- [ ] 5.2 Prove replay equality: a run that compacts, suspends and resumes on another pod assembles the same request bytes as the original did after compaction. Register the subtest named exactly `context.replay-after-compaction`.
  - verify: `go test -timeout 10m ./core -run 'TestCompactionPublicPath/context\.replay-after-compaction'`
  - evidence: `core/stores/session_memory.go:308-330` (`Load`), `:331-385` (`Append`, version invariant); `core/projection_scenarios_test.go` (`projScenarioResume`); design D2.

## 6. Invariants and package gates

- [ ] 6.1 Keep `core/projection_invariants_test.go` green and extend it for the compaction view: pending calls survive, tool-use/result adjacency holds, the taint window stays the full log, and journal fingerprints are unchanged by a compaction. Add `TestCompactionViewPendingCall`, `TestCompactionViewAdjacency`, `TestCompactionViewTaintOverFullLog`, `TestCompactionViewJournalFingerprintUnchanged`.
  - verify: `go test -timeout 10m ./core -run 'TestProjectionPendingCallUntouched|TestProjectionAdjacencyIntact|TestProjectionTaintOverFullLog|TestProjectionJournalFingerprintUnchanged|TestCompactionView'`
  - evidence: `core/projection_invariants_test.go`; design §"Invariants" inherited from `2026-10-07-m2-context-projections`.
- [ ] 6.2 Register the five owned public-path subtests under one suite and confirm exactly those five ids are claimed, not the two `m2-provider-compaction` ids.
  - verify: `go test -timeout 10m ./core -run 'TestCompactionPublicPath'`
  - evidence: `core/compaction_scenarios_test.go` (`context.compaction-persisted`, `context.replay-after-compaction`, `context.pending-call-survives`, `context.summary-guarded`, `context.compaction-audited`).
- [ ] 6.3 Run the repository gates after implementation and resolve failures.
  - verify: `task spec:gate GATE=M2`; `task lint && task test && task api:check`
  - evidence: `task lint` 0 issues; `task test` green; `task spec:gate GATE=M2` reports the five owned `context` ids covered with no new `missing_in_scope`; `task api:check` lists the changed `Compactor` signature and the additive `AuditRecord` payload before the v1 freeze.
- [ ] 6.4 Validate the authored change package.
  - verify: `openspec validate m2-context-persisted-compaction --strict --no-interactive`
  - evidence: `Change 'm2-context-persisted-compaction' is valid`.
