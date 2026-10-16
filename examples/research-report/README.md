# research-report — M2 market-brief demo

An offline research report over gohan's governed native conversation: a
scripted model calls `search_tools`, discovers a subset of a catalog of
40 deferred source tools, fans out over two discovered sources, writes a
progress note through the session notes pair, and the context policy
clears spent tool results and folds the covered prefix into a summary
before the run finishes its report. The persisted history is loaded back
through `stores.SessionLog.Load` at the end, so every assertion about the
run reads the same store the harness wrote.

The example persists nothing by hand and computes no projection,
compaction or accounting logic of its own: it composes `gohan.Build`, one
`gohan.NativeSpec`, one `conv.Send` and one `Load`.

Nothing reaches the network. No credential, no API key, no daemon and no
container is involved: every model turn, every tool result and every store
is an in-process fixture.

## Run it

```
go -C examples run ./research-report
```

## Test it

```
go -C examples test -json -short -timeout 2m ./research-report/... -run '^TestResearchReportOffline$'
```

Both commands run offline: the run needs no credential, no network and no
daemon.

## Requirements → scenario ids

Every row cites the subtest that owns it. A renamed or removed upstream
subtest turns into a failing citation rather than a stale claim.

| Requirement | Scenario id | Owning subtest | Example reaches it |
|---|---|---|---|
| context spec — deferred discovery over a fixed catalog, composed | `context.research-report-compaction` | `TestResearchReportOffline/context.research-report-compaction` (this package; registry entry in `openspec/scenarios.json`, requirement in `openspec/changes/m2-research-report-example/specs/context/spec.md`) | the one composed guarantee: discovery, fan-out, note, compaction, suspended resume and reattach in a single run |
| context spec — projections never write | `context.projection-does-not-write` | `TestResearchReportOffline/context.projection-does-not-write`; upstream `TestProjectionPublicPath/context.projection-does-not-write` (`core/projection_scenarios_test.go`) | an assembled request carries a `[tool result cleared]` replacement while `SessionLog.Load` still returns the original retrieved payload |
| context spec — compaction is appended to the log | `context.compaction-persisted` | `TestResearchReportOffline/context.compaction-persisted`; upstream `TestCompactionPublicPath/context.compaction-persisted` (`core/compaction_public_path_test.go:195`) | the seeded backlog pushes the working history over the fixture window and the run's compaction comes back through `Load` |
| working-state spec — notes survive compaction | `working-state.notes-survive-compaction` | `TestResearchReportOffline/working-state.notes-survive-compaction`; upstream `TestTruncateKeepsNotes` (`std/context/truncate_notes_test.go:24`) | the progress note written before the compaction is still in the next assembled request, delivered by the `SlotSession` provider rather than by history |
| limits spec — tree cost accumulates | `limits.cost-accumulates` | `TestResearchReportOffline/limits.cost-accumulates`; upstream `TestChainLimits/limits.cost-accumulates` (`core/chains/limits_test.go:88`) | the per-source child costs accumulate into the parent run total under the declared `types.RunLimits` ceiling |
| tools spec — a deferred tool is not assembled until discovered | `tools.not-assembled-until-discovered` | `TestResearchReportOffline/tools.not-assembled-until-discovered`; upstream `TestDeferredTools` (`std/toolsearch/search_test.go:68`) | the first request carries the always-loaded fixtures plus `search_tools` only; `std.NarrowTools` with `toolsearch.DeferredFilter` widens the set on the next turn |
| composition shape and fixture determinism | (no id of its own) | `TestResearchReportOffline/composition` | the run produces tools, events and assembled requests, calls `search_tools`, and the deferred catalog reports exactly 40 entries |

### D6 rows this package does not claim

These ids stay owned by their upstream tests. The package's acceptance test
runs no subtest for them, so no README row here can read as a claim this
example proves.

| Scenario id | Owning subtest | State |
|---|---|---|
| `tools.activation-persists-across-resume` | `TestDeferredTools` (`std/toolsearch/search_test.go:107`) | skipped by the acceptance test with a named reason: no shipped native resume path restores a per-run deferred-tool selection. `toolsearch.Save` replaces `stores.Checkpoint.Data` wholesale (`std/toolsearch/activation.go:38-46`), while native suspension persists a versioned checkpoint envelope whose decoder refuses an activation-only payload (`core/lifecycle_suspend.go:60-70`, `core/checkpoint_envelope.go:200-220`); `runtime.State.ActiveTools` (`core/runtime/runtime.go:32`) has no production reader |
| `tools.governed-while-deferred` | `TestDeferredToolsGoverned` (`std/toolsearch/activation_test.go:14`) | skipped by the acceptance test with a named reason: the example installs no visibility-scoped permission lookup. `AssembleInput.Filter` is unset on the native path (`core/native_effects.go:249-263`), so governed execution resolves over the full registered set (`core/native_effects.go:25-55`, `core/permission/gate.go:126-130`); the only scope-scoped gate lookup in the repository is the upstream test itself (`std/toolsearch/activation_test.go:32-36`) |
| `streams.detached-reconnect` | `TestStreamLifetime/streams.detached-reconnect` (`core/stream_lifetime_test.go:144`) | not reached: the example obtains its conversation without `gohan.Detached()` and asserts no client reattachment. The row stays with the upstream lifetime suite |
| `streams.detached-requires-log` | `TestStreamLifetime/streams.detached-requires-log` (`core/stream_lifetime_test.go:229`) | not reached: the example builds no detached conversation, so `errDetachedNoLog` is never reached from here |
| `context.projection-deterministic` | `TestProjectionPublicPath/context.projection-deterministic` (`core/projection_scenarios_test.go`) | not reached as a subtest: the example assembles its requests once per turn through its own `Assemble` callback and does not assert byte-equality across two assemblies of the same input |
| `context.notes-excluded` | `TestProjectionPublicPath/context.notes-excluded`; unit `TestClearToolResults/context.notes-excluded` (`std/context/clear_tool_results_test.go`) | not reached as a subtest: the example asserts the note survives compaction, not that a cleared window excludes `notes_*` results |
| `context.replay-after-compaction` | `TestCompactionPublicPathReconciled/context.replay-after-compaction` (`core/compaction_public_path_test.go:1305`) | not reached as a subtest: the example runs `Replay` against no recorded run; its resumed-turn leg is the composition id's own row |
| `tools.notes-survive-reset` | `TestNotesSurviveReset` (`std/notes/reset_test.go:97`) | not reached: the example runs a single send and never resets the session notes slot |

## Public paths the example calls

| Path | Use |
|---|---|
| `gohan.Build`, `gohan.Defaults` via `std.Defaults()` | the stack: memory stores, scripted model, estimator, compaction guard and fallback, limits middleware |
| `gohan.NativeSpec` (`Request`, `Profile`, `ContextPolicy`, `Assemble`, `Tools`, `Decider`) | the single `research_report` native definition |
| `gohan.NewNativeConversation` with `WithConversationRuns` and `WithConversationEventLog` | the governed conversation over the `research` session |
| `conv.Send` | one send, ranged for its events |
| `stores.SessionLog.Append` / `stores.SessionLog.Load` | the seeded pre-run backlog and the persisted history read back at the end |
| `stores.ContextPolicy` with `std/context.ClearToolResults` and `std/context.NewSummarize` | the per-flow projection and compactor |
| `std/context.NewTruncate` | the compaction fallback behind `stdguard.NewContextGuard()` |
| `std.NarrowTools` + `toolsearch.DeferredFilter` + `toolsearch.NewSearchTools` | deferred discovery and the per-turn assembled set |
| `std/notes` `New` and `Provide` | the `notes_write` tool pair and the `SlotSession` provider |
| `gohan.NewTool`, `permission.Allow` | the fixture decider over the five allowed tool names |
| `testkit/gohantest.NewFakeTool` | the fixture tool bodies |
| `std.ExplainHandler` contract | the `explain` subcommand, JSON on a non-TTY stdout |
| `limit.Limits` with `types.RunLimits` | the run ceiling the fan-out spends against |

## Fixtures

| Fixture | Content |
|---|---|
| scripted model (`scriptedTurns`) | four fixed turns: the always-loaded batch (`search_archive`, `take_notes`), the `search_tools` query `{"query":"corpus"}`, the two discovered corpus sources (`source_corpus_batch`, `source_corpus_stream`), then the report text |
| `deferredCatalog` | exactly 40 `types.ToolSpec` entries, every one `ReadOnly` and `Deferred`, generated from one table; the deferred count is asserted so the catalog cannot silently shrink |
| always-loaded tools | `search_archive` (archived retrieval notes), `take_notes` (the notes pair), `search_tools` (the reserved meta-tool over the registry closure) |
| discovered source bodies | `sourceTools` materializes the named catalog entries as fake tools, so a discovered definition carries an executable body over the same spec the registry reports |
| `backlogResult()` | the seeded pre-run archive backlog: a long enough digest that the first assembled request crosses the fixture window and the compaction folds a store-minted prefix |
| `reportPrincipal()` | tenant `local`, subject `analyst`, session read/write scopes, no approve scope |
| stores | `NewMemorySessionLog`, `NewMemoryRuns`, `NewMemoryEventLog`, `NewMemoryAuditLog`, `NewMemoryNotes` |

No sandbox is declared: the sources are read-only retrievals, so no
`Sandbox` or `Workspace` port is touched.

## Versions it pins

| Pin | Value |
|---|---|
| gohan module | `github.com/victorzhuk/gohan v0.1.0` in `examples/go.mod`, replaced by the repository root for the workspace |
| Go toolchain | `go 1.27.1` |
| provider model id (live run only) | the scripted profile `scripted`; a live run pins the model id its provider profile carries and the fixtures stand in for that profile's answers |

## `explain`

```
go -C examples run ./research-report explain
```

Prints the JSON `std.ExplainHandler` serves from `Stack.Explain`, with no
provider call and no run budget spent: `Flow` (`research_report`),
`Profile`, `Steps`, `ToolSteps`, `Tools`, `Strategies`, `Fallback`,
`Limits`, `Granularity`, `Sample`, `Prompts`, `Skills`, `Release` and
`Warnings`. Nothing beyond that surface is emitted.

## Unmet prerequisites

These are prerequisites the example records and never claims as evidence.

| Prerequisite | Owner | State |
|---|---|---|
| `MapReduce`, `RAG`, `Judge` recipes in `std/flow` | M4 (`docs/overview.md:116`); catalog row `docs/design/scenarios.md:186` | absent from `std/flow`; the fan-out uses the shipped subflow surfaces instead and claims no recipe leg |
| `subflows.collect-partials` | M3 (registry `deferred_to: "M3"`, `openspec/scenarios.json`) | partial collection is recorded as a prerequisite, never as evidence |
| native resume that restores a per-run deferred-tool selection | no milestone owns it | unmet: `core/drive.go:92-100`, `core/drive_turn.go:776-795`, `core/resume.go:511-522`; `runtime.State.ActiveTools` has no production reader |
| permission lookup scoped to the assembled set | no milestone owns it | unmet: `core/native_effects.go:25-55`, `:249-263`, `core/permission/gate.go:126-130` |

Until those land, the requirement rows above cannot be read as claims the
harness backs: the two activation ids stay skipped with their named
reasons.