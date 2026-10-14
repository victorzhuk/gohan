# Spec Delta

## ADDED Requirements

### Requirement: Example composition: research report

The `research-report` example MUST compose deferred discovery over exactly forty sources, a bounded retrieval fan-out, a progress-note write, a persisted compaction, a suspended resume and a detached client reattachment into one offline run. It MUST add no behaviour to `context`: every rule it exercises is declared by another requirement, and this requirement binds only their composition.

#### Scenario: research report compacts and stays coherent across reattachment
ID: `context.research-report-compaction`
- WHEN the offline example discovers sources through `search_tools`, fans out over the discovered sources, writes a progress note, compacts the persisted history, runs detached and resumes through `Replay`, and a client reattaches from its last `Seq`
- THEN the resumed run assembles the same request bytes the original assembled after compaction, the discovered sources are still active, the progress note is still in the next request, and the reattached client receives every event after its cursor without a gap or a duplicate; the projection that cleared the older retrieved results left `stores.SessionLog.Load` returning the full results and the history version unchanged