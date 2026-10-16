# Spec Delta

## ADDED Requirements

### Requirement: Example composition: research report

The `research-report` example MUST compose deferred discovery over exactly forty sources, a bounded retrieval fan-out, a progress-note write, a persisted compaction, a suspended resume and a detached client reattachment into one offline run. It MUST add no behaviour to `context`: every rule it exercises is declared by another requirement, and this requirement binds only their composition. It MUST NOT claim that the harness restores a deferred-tool selection across a resume: the shipped native resume restores the checkpoint position without replaying discovery (`core/drive.go:92-100`, `core/drive_turn.go:776-795`) and resolves governed execution over the full registered toolset (`core/native_effects.go:25-55`), so the example's own assembler recomputes the active set from the reloaded history and that recomputation is recorded as a caller-side bridge, not a harness guarantee.

#### Scenario: research report compacts and stays coherent across reattachment
ID: `context.research-report-compaction`
- WHEN the offline example discovers sources through `search_tools`, fans out over the discovered sources, writes a progress note, compacts the persisted history, runs detached, suspends and resumes, and a client reattaches from its last `Seq`
- THEN the resumed run assembles the same request bytes the original assembled after compaction, the discovered sources are the definitions the resumed request carries — recomputed by the flow's assembler from the discovery results in the reloaded history through `std.NarrowTools` and `toolsearch.DeferredFilter` — the progress note is still in the next request, and the reattached client receives every event after its cursor without a gap or a duplicate; the projection that cleared the older retrieved results left `stores.SessionLog.Load` returning the full results and the history version unchanged