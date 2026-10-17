# research-report — M2 market-brief demo

An offline research report over gohan's governed native conversation: a
scripted model calls `search_tools`, discovers a subset of a catalog of
40 deferred source tools, fans out over two discovered sources, writes a
progress note through the session notes pair, and the context policy
clears spent tool results and folds the covered prefix into a summary
before the run finishes its report. The persisted history is loaded back
through `stores.SessionLog.Load` at the end, so every assertion about the
run reads the same store the harness wrote.

The example reconstructs no run: it assembles no session log by hand,
writes no run records of its own and appends no compaction. Its backlog
digest is fixture input the scripted model turns carry, appended once per
composition before the run through the store's own append path. Every run
message, the checkpoint and the compaction itself are persisted by the
harness, not by the example. It computes no
projection, compaction or accounting logic of its own: it composes
`gohan.Build`, one `gohan.NativeSpec`, one `conv.Send`, one
`conv.Continue`, one `conv.Resume` and one `Load`. Three compositions share
that stack. The plain `Compose` run is a completed `conv.Send` followed by
a `conv.Continue`. `ComposeDetached` completes a `conv.Send`, then a
`conv.Continue` that ends in a `types.Suspended` with reason
`types.AwaitingControl`, then a `conv.Resume` over that suspension token on
the same conversation: `ComposeDetached` reads the checkpoint the
suspension minted through `stores.Checkpoints.Peek` and the harness owns
its persistence. `ComposeWiderFanout` declares a cost ceiling and observes
its typed refusal.

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
| context spec — deferred discovery over a fixed catalog, composed | `context.research-report-compaction` | `TestResearchReportOffline/context.research-report-compaction` (this package; registry entry in `openspec/scenarios.json`, requirement in `openspec/changes/m2-research-report-example/specs/context/spec.md`) | implemented: discovery through `search_tools`, the two-source fan-out, the persisted compaction and the discovered source definitions plus the progress note in the final assembled request; the detached composition in `ComposeDetached` builds with `gohan.Detached()`, reattaches from a `Seq` cursor and resumes the suspended run through `conv.Resume` over the suspension token. The detached leg asserts a durable compaction in the persisted history before the Resume, four original provider captures equal at index 3 to the two resumed captures' index 0 by canonical bytes, the discovered source definitions and the retained note text inside that first resumed request, a contiguous resumed event suffix belonging to the suspended run and ending in one `Done` with reason `types.StopCompleted`, and a consumed resume token. Not asserted: cross-pod `Replay` and activation restoration |
| context spec — projections never write | `context.projection-does-not-write` | `TestResearchReportOffline/context.projection-does-not-write`; upstream `TestProjectionPublicPath/context.projection-does-not-write` (`core/projection_scenarios_test.go`) | an assembled request carries a `[tool result cleared]` replacement while `SessionLog.Load` still returns the original retrieved payload |
| context spec — compaction is appended to the log | `context.compaction-persisted` | `TestResearchReportOffline/context.compaction-persisted`; upstream `TestCompactionPublicPath/context.compaction-persisted` (`core/compaction_public_path_test.go:195`) | the fixture backlog digest pushes the working history over the fixture window and the run's compaction comes back through `Load` |
| working-state spec — notes survive compaction | `working-state.notes-survive-compaction` | `TestResearchReportOffline/working-state.notes-survive-compaction`; upstream `TestTruncateKeepsNotes` (`std/context/truncate_notes_test.go:24`) | the progress note is written before the fold and is still in the first request assembled after it, delivered by the `SlotSession` provider rather than by history. The subtest checks both ends: the note turn sits inside the folded prefix and the post-compaction request carries the note text |
| limits spec — tree cost accumulates | `limits.cost-accumulates` | `TestResearchReportOffline/limits.cost-accumulates`; upstream `TestChainLimits/limits.cost-accumulates` (`core/chains/limits_test.go:88`) | `Done.Cost` is positive at the end of the composed run, and the two runs the fixture produces are compared against independently observed harness totals: `Done.Cost` 0.71 and 0.62 from separate runs, summing to 1.33. Weak leg: the scripted model spends usage on every turn, so the assertion does not isolate the per-source fan-out costs inside the run. There are no child runs here, so no per-child cost is observed. The ceiling and its typed refusal are covered separately by `TestResearchReportOffline/limits-ceiling-refuses-wider-fanout`, where the wider run's harness-observed cost is 1.08 (the charged sequence 0.24+0.20+0.32+0.32) above the declared 1.0 ceiling; complete parent-plus-two-children accounting is an upstream requirement this example does not prove |
| tools spec — a deferred tool is not assembled until discovered | `tools.not-assembled-until-discovered` | `TestResearchReportOffline/tools.not-assembled-until-discovered`; upstream `TestDeferredTools` (`std/toolsearch/search_test.go:68`) | the first request carries the always-loaded fixtures plus `search_tools` only; `std.NarrowTools` with `toolsearch.DeferredFilter` widens the set on the next turn |
| composition shape and fixture determinism | (no id of its own) | `TestResearchReportOffline/composition` | the run produces tools, events and assembled requests, calls `search_tools`, and the deferred catalog reports exactly 40 entries |
| limits spec — a declared cost ceiling refuses an over-budget run | (no id of its own) | `TestResearchReportOffline/limits-ceiling-refuses-wider-fanout` | `ComposeWiderFanout` declares `types.RunLimits` with a cost ceiling over the wider five-call retrieval fan-out (a `search_tools` query plus four source calls over two source names, on top of the always-loaded batch) and the run ends in a `*types.LimitExceededError` whose `Limit` is `MaxCost`, after the harness charged 1.08 against the 1.0 ceiling. The typed refusal proves the ceiling at that value only, not soft-warning cardinality or complete parent-plus-two-children accounting |
| projection leaves the history version untouched | (no id of its own) | `TestResearchReportOffline/context-projection-preserves-version` | the assembled request carries the projection's replacements while the history version is unchanged around the assembly. It checks the version half only; repeated-assembly determinism is asserted by `context.projection-deterministic` upstream |
| streams spec — a detached run reattaches from a cursor | (no id of its own) | `TestResearchReportOffline/streams-detached-reattach` | `ComposeDetached` builds with `gohan.Detached()`, drops at a known `Seq` and compares the tail `Attach` delivered from that cursor against the logged events after it. This is a retained-tail comparison, not a complete live reconnect during an active detached run; `TestStreamLifetime/streams.detached-reconnect` stays the scenario id's owner |

### D6 rows this package does not claim

These ids stay owned by their upstream tests. The package's acceptance test
runs no subtest for them, so no README row here can read as a claim this
example proves.

| Scenario id | Owning subtest | State |
|---|---|---|
| `tools.activation-persists-across-resume` | `TestDeferredTools` (`std/toolsearch/search_test.go:107`) | skipped by the acceptance test with a named reason: no shipped native resume path restores a per-run deferred-tool selection. `toolsearch.Save` replaces `stores.Checkpoint.Data` wholesale (`std/toolsearch/activation.go:38-46`), while native suspension persists a versioned checkpoint envelope whose decoder refuses an activation-only payload (`core/lifecycle_suspend.go:60-70`, `core/checkpoint_envelope.go:200-220`); `runtime.State.ActiveTools` (`core/runtime/runtime.go:32`) has no production reader |
| `tools.governed-while-deferred` | `TestDeferredToolsGoverned` (`std/toolsearch/activation_test.go:14`) | skipped by the acceptance test with a named reason: this example installs no visibility-scoped permission bridge. `AssembleInput.Filter` is unset on the native path (`core/native_effects.go:249-263`), so governed execution resolves over the full registered set (`core/native_effects.go:25-55`, `core/permission/gate.go:126-130`); the only scope-scoped gate lookup in the repository is the upstream test itself (`std/toolsearch/activation_test.go:32-36`) |
| `streams.detached-reconnect` | `TestStreamLifetime/streams.detached-reconnect` (`core/stream_lifetime_test.go:144`) | reached only in part by the example's own reattachment assertions in `TestResearchReportOffline/streams-detached-reattach`: the composed run builds with `gohan.Detached()` and the client reattaches from the last `Seq` and compares the retained tail against the logged events, while the upstream lifetime suite stays the row's owner |
| `streams.detached-requires-log` | `TestStreamLifetime/streams.detached-requires-log` (`core/stream_lifetime_test.go:229`) | not reached: `ComposeDetached` always installs a session log, so `errDetachedNoLog` is never reached from here |
| `context.projection-deterministic` | `TestProjectionPublicPath/context.projection-deterministic` (`core/projection_scenarios_test.go`) | not reached as a subtest: the example assembles its requests once per turn through its own `Assemble` callback and does not assert byte-equality across two assemblies of the same persisted history. `TestResearchReportOffline/context-projection-preserves-version` covers the history-version half only |
| `context.notes-excluded` | `TestProjectionPublicPath/context.notes-excluded`; unit `TestClearToolResults/context.notes-excluded` (`std/context/clear_tool_results_test.go`) | not reached as a subtest: the example asserts the note survives compaction, not that a cleared window excludes `notes_*` results |
| `context.replay-after-compaction` | `TestCompactionPublicPathReconciled/context.replay-after-compaction` (`core/compaction_public_path_test.go:1305`) | not reached as a subtest: neither composition calls `Replay`. `ComposeDetached` suspends on its fourth provider entry and resumes the same run through `conv.Resume`, so the example records no replayed turn; the composition id's row above carries the same gap and claims none of it |
| `tools.notes-survive-reset` | `TestNotesSurviveReset` (`std/notes/reset_test.go:97`) | not reached: the example writes its note once and never resets the session notes slot |

## Public paths the example calls

| Path | Use |
|---|---|
| `gohan.Build`, `gohan.Defaults` via `std.Defaults()` | the stack: memory stores, scripted model, estimator, compaction guard and fallback, limits middleware |
| `gohan.NativeSpec` (`Request`, `Profile`, `ContextPolicy`, `Assemble`, `Tools`, `Decider`) | the single `research_report` native definition |
| `gohan.NewNativeConversation` with `WithConversationRuns`, `WithConversationEventLog`, `WithConversationCheckpoints` and `gohan.Detached()` | the governed conversation over the `research` session; `ComposeDetached` builds it detached so a client can drop and reattach, and installs the checkpoint store the suspension token resolves against |
| `conv.Send` | the opening send of both compositions, ranged for its events (`ComposeDetached` reads only the minted run id from it) |
| `conv.Continue` | the second half of the plain run: it reloads the persisted history, publishes the compaction and finishes the report. In `ComposeDetached` it runs on the original conversation and ends in a `types.Suspended` with reason `types.AwaitingControl`, not in a completion |
| `conv.Resume` | the resume call: `ComposeDetached` passes the single-use suspension token to `conv.Resume` on the same conversation, and the run completes from the same durable history |
| `stores.NewMemoryCheckpoints` and `Peek` | the checkpoint read around the suspension: `ComposeDetached` peeks the token before the Resume to check the run, session, flow and reason the harness persisted, and peeks again afterwards to confirm the token is consumed |
| `conv.Attach` | the dropped client's reattach from a `Seq` cursor in `ComposeDetached` |
| `stores.SessionLog.Append` / `stores.SessionLog.Load` | the one fixture-backlog append before the run starts, and the persisted history read back at the end; the harness owns every run message, the compaction and the checkpoint |
| `stores.ContextPolicy` with `std/context.ClearToolResults` and `std/context.NewSummarize` | the per-flow projection and compactor |
| `std/context.NewTruncate` | the compaction fallback installed beside `stdguard.NewContextGuard()`, the compaction guard |
| `std.NarrowTools` + `toolsearch.DeferredFilter` + `toolsearch.NewSearchTools` | deferred discovery and the per-turn assembled set |
| `std/notes` `New`, `Call` and `Provide` | the `take_notes` tool, whose body calls the pair's `Call`, and the `SlotSession` provider behind `Provide` |
| `gohan.NewTool`, `permission.Allow` | the fixture decider over the five allowed tool names |
| `testkit/gohantest.NewFakeTool` | the fixture tool bodies |
| `std.ExplainHandler` contract | the `explain` subcommand, JSON on a non-TTY stdout |
| `limit.Limits` with `types.RunLimits` | the stack's limit middleware, installed by `std.Defaults()`. `ComposeWiderFanout` adds a `types.RunLimits` cost ceiling over the wider fan-out and observes the typed `*types.LimitExceededError` refusal; the composed run declares no ceiling and claims nothing beyond its positive run total |

## Fixtures

| Fixture | Content |
|---|---|
| scripted model (`scriptedTurns`) | four fixed turns for the composed run: the always-loaded batch (`search_archive`, `take_notes`), the `search_tools` query `{"query":"corpus"}`, the two discovered corpus sources (`source_corpus_batch`, `source_corpus_stream`), then the report text |
| detached model (`detachedModel`) | the `suspendingModel` wrapper around the same scripted fixture `Compose` uses. It records every provider-entry request, yields `&types.SuspendError{Reason: types.AwaitingControl}` at the fourth entry before the embedded model produces anything, and lets `conv.Resume` replay the same scripted turns on the same run |
| wide-fanout turns for `ComposeWiderFanout` | a `scriptedTurns` value of seven turns over the wider five-call retrieval fan-out; each turn's usage is scripted, and the cost the assertion reads comes from the harness ledger rather than from the fixture |
| `deferredCatalog` | exactly 40 `types.ToolSpec` entries, every one `ReadOnly` and `Deferred`, generated from one table; the deferred count is asserted so the catalog cannot silently shrink |
| always-loaded tools | `search_archive` (archived retrieval notes), `take_notes` (the notes pair), `search_tools` (the reserved meta-tool over the registry closure) |
| discovered source bodies | `sourceTools` materializes the named catalog entries as fake tools, so a discovered definition carries an executable body over the same spec the registry reports |
| `backlogResult()` | the fixture backlog digest the scripted turns carry: a long enough archive digest that the working history crosses the fixture window and the run's own compaction folds a prefix |
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
| `MapReduce`, `RAG`, `Judge` recipes in `std/flow` | M4 (`docs/overview.md:116`); catalog row `docs/design/scenarios.md:186` | absent from `std/flow`; the fan-out runs as sequential model turns inside one run, not as a shipped subflow, and claims no recipe leg |
| `subflows.collect-partials` | M3 (registry `deferred_to: "M3"`, `openspec/scenarios.json`) | partial collection is recorded as a prerequisite, never as evidence |
| native resume that restores a per-run deferred-tool selection | no milestone owns it | unmet: `core/drive.go:92-100`, `core/drive_turn.go:776-795`, `core/resume.go:511-522`; `runtime.State.ActiveTools` has no production reader |
| permission lookup scoped to the assembled set | no milestone owns it | unmet: `core/native_effects.go:25-55`, `:249-263`, `core/permission/gate.go:126-130` |
| detached run with a suspended `Replay` resume | `m2-research-report-example` | partial: `ComposeDetached` suspends with reason `types.AwaitingControl` and resumes the same run through `conv.Resume` over the token, but it never calls `Replay`, so no replayed turn is recorded and no cross-pod leg is claimed |
| harness-side accumulation of per-source child costs into a parent total | `m2-research-report-example` | partial: this example has no child runs — every source retrieval is a sequential model turn inside one run — so the ceiling refusal is observed as a typed `*types.LimitExceededError` at the wider run's harness-observed cost of 1.08. Complete parent-plus-two-children accounting is an upstream requirement this example does not prove |

Until those land, the requirement rows above cannot be read as claims the
harness backs: the two activation ids stay skipped with their named
reasons.