# Spec Delta

## MODIFIED Requirements

### Requirement: Compaction is persisted

The harness MUST persist a compaction as a `Compaction` block that records `CoversUpTo`, and assembly MUST hide the covered versions. `CoversUpTo` MUST stay below any pending `ToolUse`. A replayed run MUST assemble the same request bytes as the original run did after compaction. A summary the guard rejects MUST NOT be persisted, and the turn MUST use `Truncate` instead. A `CompactionOpaque` block MUST bind to the model it was created for. Every persisted compaction MUST write the `Compacted` audit record and event.

#### Scenario: compaction appended
ID: `context.compaction-persisted`
- WHEN `Summarize` compacts a history at version 40
- THEN `SessionLog` holds a `Compaction` block with `CoversUpTo: 40` and the next assembly hides versions ≤ 40

#### Scenario: replay after compaction
ID: `context.replay-after-compaction`
- WHEN a run compacts, suspends and is resumed with `Replay` on another pod
- THEN the resumed run assembles the same request bytes as the original run did after compaction

#### Scenario: pending call survives
ID: `context.pending-call-survives`
- WHEN compaction is triggered while a `ToolUse` awaits `HumanApproval`
- THEN `CoversUpTo` is below that `ToolUse` and the resumed run still sees it

#### Scenario: summary is guarded
ID: `context.summary-guarded`
- WHEN a harness-side summary contains an instruction the `StageContext` guard rejects
- THEN nothing is persisted, `Truncate` is used for the turn and `gohan.guard.context_rejected{provider="compaction"}` increments

#### Scenario: opaque compaction bound to model
ID: `context.opaque-bound-to-model`
- WHEN a `CompactionOpaque` block exists and the router selects a different endpoint
- THEN the block is dropped from assembly and the request is re-fitted with projections and `Summarize`

#### Scenario: opaque under cross-provider fallback rejected
ID: `context.opaque-fallback-rejected`
- WHEN a flow uses `ProviderCompact` on a profile with `Caps.Compaction == CompactionOpaqueCap` and its `Fallback` lists another provider
- THEN `Build` returns an error naming flow, profile and strategy

#### Scenario: compaction audited
ID: `context.compaction-audited`
- WHEN any compaction is persisted
- THEN `AuditLog` holds a `Compacted` record with versions, token counts, policy and summary checksum, and `EventLog` holds the `Compacted` event

