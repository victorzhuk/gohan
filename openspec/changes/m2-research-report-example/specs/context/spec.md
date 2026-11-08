# Spec Delta

## ADDED Requirements

### Requirement: Example composition: research report

The `research-report` example MUST compose deferred discovery over exactly forty sources, a bounded retrieval fan-out, a progress-note write, a persisted compaction, a native `AwaitingControl` suspension, a token-based `Resume` of that same conversation and a client reattachment from its last event `Seq` into one offline run. It MUST add no behaviour to `context`: every rule it exercises is declared by another requirement, and this requirement binds only their composition.

#### Scenario: research report compacts and stays coherent across reattachment
ID: `context.research-report-compaction`
- WHEN the offline example discovers sources through `search_tools`, fans out over the discovered sources, writes a progress note through `take_notes`, starts the run detached via `gohan.Detached()` with a completed `conv.Send`, continues it over that run's durable history with `conv.Continue` until it publishes a durable compaction and suspends with a genuine `AwaitingControl` checkpoint carrying a nonempty token, resumes that same conversation with one `conv.Resume(ctx, token, stores.ResumeInput{Verdict: stores.VerdictApprove})`, and a client reattaches from its last event `Seq` through `conv.Attach` at the cursor it dropped on
- THEN the first provider request the resumed run assembles carries the same serializable request fields the original run assembled at the suspension boundary, the discovered sources are the definitions the resumed request carries — recomputed by the flow's assembler from the discovery results in the reloaded history through `std.NarrowTools` and `toolsearch.DeferredFilter` — the progress note is still in the next request, the resumed run's persisted records continue the suspended run's `Seq` space consecutively and end in one completed terminal record, the consumed token is no longer readable from the checkpoint store, and the reattached client receives every event after its cursor without a gap or a duplicate; the projection that cleared the older retrieved results left `stores.SessionLog.Load` returning the full results and the history version unchanged

### Requirement: Example claim boundary: research report

The resume is native and same-conversation: the example MUST NOT claim another-pod `Replay`, and MUST NOT reconstruct a run from a hand-assembled session log. It MUST NOT claim that the harness restores a deferred-tool selection across that resume: the example's own assembler recomputes the active set from the reloaded history, and that recomputation is recorded as a caller-side bridge, not a harness guarantee.

#### Scenario: claim boundary for native resume and deferred-tool selection
ID: `context.research-report-claim-boundary`
- WHEN the shipped native resume restores the checkpoint position without replaying discovery (`core/drive.go:92-100`, `core/drive_turn.go:776-795`) and resolves governed execution over the full registered toolset (`core/native_effects.go:25-55`)
- THEN the example claims neither another-pod `Replay` nor a run reconstructed from a hand-assembled session log, and it does not claim that the harness restores the deferred-tool selection across that resume: the recomputed active set is recorded as a caller-side bridge, not a harness guarantee
