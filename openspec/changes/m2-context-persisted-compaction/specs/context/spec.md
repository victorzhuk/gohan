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

## ADDED Requirements

### Requirement: Compaction covers a safe prefix selected before summarization

The harness MUST select the covered prefix and its `CoversUpTo` boundary before it invokes any summary source. The boundary MUST end on a whole-turn boundary. The summary source MUST see only the history the boundary keeps and MUST NOT see an excluded message. The harness MUST refuse a returned `CoversUpTo` that differs from the selected boundary and MUST persist nothing.

#### Scenario: safe prefix precedes summary
ID: `context.compaction-safe-prefix` (proposed)
- WHEN compaction runs over a history whose newest turn holds a completed tool pair and whose earlier turn holds a pending `ToolUse`
- THEN the boundary is chosen first, ends after whole turns, and the summary source sees only the prefix the boundary keeps

### Requirement: A summary source folds only newly covered messages

A summary source MUST receive only newly covered ordinary messages. Previously covered raw messages and prior `Compaction` blocks MUST NOT enter the source. The source's history version MUST carry the selected absolute `CoversUpTo`. The composed summary MUST carry prior summary blocks unchanged. The harness MUST NOT re-summarize text a superseded `Compaction` block holds.

#### Scenario: prior summary carried, not refolded
ID: `context.compaction-summary-not-refolded` (proposed)
- WHEN a second compaction covers messages after an earlier summary
- THEN the source sees only the newly covered messages and the composed summary keeps the earlier summary blocks unchanged

### Requirement: A completed tool pair is covered whole

The harness MUST cover a `ToolUse` with its `ToolResult` together or not at all. `CoversUpTo` MUST NOT fall inside a completed pair. The harness MUST decide pair coverage by matching `ToolUse.ID` with `ToolResult.ID` and MUST NOT decide it by counting uses and results. The harness MUST NOT cover a `ToolUse` whose `ToolResult` is absent from history.

#### Scenario: pair not split
ID: `context.compaction-pair-whole` (proposed)
- WHEN a compaction boundary would end between a `ToolUse` and its matching `ToolResult`
- THEN the boundary moves to keep the pair together and the pair is covered or left whole

#### Scenario: counts disagree with matching ids
ID: `context.compaction-pair-by-id` (proposed)
- WHEN a prefix holds two tool uses and one result that matches the first use
- THEN the boundary keeps the second use whole rather than closing the pair by count

### Requirement: Assembly renders one current summary at the view head

Assembly MUST render at most one `Compaction` block: the newest by append position. Assembly MUST remove the selected block from its original position and insert it at the head of the view. Assembly MUST drop every other `Compaction` block and every message the selected block covers. Assembly MUST keep unrelated blocks of a retained message. The rendered view MUST NOT carry summary text twice.

#### Scenario: superseded summary dropped
ID: `context.compaction-single-summary` (proposed)
- WHEN a history holds two `Compaction` blocks and a third compaction runs
- THEN assembly renders only the newest block at the head and neither the older summaries nor the newest summary appear twice

### Requirement: The compaction view runs before the declared projections

The collapse MUST run before the declared projections, including when no projection is declared. A history without a `Compaction` block MUST render unchanged.

#### Scenario: collapse runs without declared projections
ID: `context.compaction-collapse-without-projections` (proposed)
- WHEN a flow declares no projection and its log holds a `Compaction` block
- THEN the covered versions are still hidden and one summary is rendered at the head

### Requirement: A compaction publication is durable before the append completes

The harness MUST persist the publication payload with the compaction message and MUST decode it after loading. The payload MUST carry the publishing run, the covered versions, the token counts, the policy name and the summary checksum. The harness MUST validate the payload against the session, the message identity and the summary checksum. A reconciling run MUST NOT change the publishing run.

#### Scenario: publication facts survive a crash
ID: `context.compaction-publication-persisted` (proposed)
- WHEN the process stops after the compaction message is appended
- THEN loading the session yields the payload and the publishing run the appender recorded, not the reconciling run

### Requirement: The publication identity is known before the append

The publication identity MUST be derivable from the session, the publishing run and the loaded version before the append. The append MUST assign that identity to the stored message. An ordinary history append MUST keep its current identity behaviour.

#### Scenario: identity is stable across runs
ID: `context.compaction-publication-identity` (proposed)
- WHEN two runs compute the identity of the same session, publishing run and loaded version
- THEN both derive the same identity and the append stores that identity on the message

### Requirement: Reconciliation publishes under the original run

The session append MUST be the commit point. The harness MUST publish to the original run partition under the durable publishing run, and a reconciling run MUST NOT substitute its own identity. An absent sink MUST be skipped and every configured sink MUST reconcile. A decode, lookup, authorization or append error MUST prevent the provider call.

#### Scenario: publication reconciled after interruption
ID: `context.compaction-publication-reconciled` (proposed)
- WHEN a process appends a `Compaction` block and stops before the audit record or the event lands
- THEN the next run completes only the missing sink under the publishing run and appends no second block

### Requirement: Every durable publication is reconciled at every provider invocation

Before every provider invocation the harness MUST inspect every durable compaction publication, not only the newest rendered block, even when the current policy carries no compactor. Reconciliation MUST hold on the native path and on the foreign path, including a foreign runtime that calls the model directly without harness assembly.

#### Scenario: foreign invocation reconciles without a compactor
ID: `context.compaction-publication-reconciled-foreign` (proposed)
- WHEN a registered foreign flow whose runtime calls the model directly runs with no active compactor over a session holding an unreconciled publication
- THEN the invocation completes the missing sinks under the publishing run and the run's own identity never replaces it

### Requirement: A duplicate publication returns its original version

After validating and authorizing the request, `AppendCompaction` MUST look up the publication identity before it compares `expectedVersion`. An existing identical identity MUST return the version its original append committed, MUST append no message and MUST NOT advance the version, even when `expectedVersion` is stale.

#### Scenario: identical duplicate returns the original version
ID: `context.compaction-append-duplicate-noop` (proposed)
- WHEN a publication identity is appended again with an identical payload and a stale `expectedVersion`
- THEN the original committed version is returned, no message is appended and the version does not advance

### Requirement: A conflicting or unauthorized publication is refused without mutation

An existing identity whose payload differs MUST be an error and MUST change nothing, and that refusal MUST stay distinct from `types.ErrVersionConflict`. An invalid payload or an unauthorized request MUST be refused before the identity lookup.

#### Scenario: conflicting duplicate is refused
ID: `context.compaction-append-conflict-refused` (proposed)
- WHEN an existing publication identity is appended with a different payload
- THEN the append returns an error and neither the log nor the version changes

### Requirement: An absent publication identity appends once or refuses

An absent identity with a stale `expectedVersion` MUST return `types.ErrVersionConflict` and change nothing. An absent identity with a current `expectedVersion` MUST append one message carrying the deterministic identity and advance the version by exactly one. The memory store and the PostgreSQL store MUST implement this order identically and atomically.

#### Scenario: stale absent identity refuses
ID: `context.compaction-append-stale-refused` (proposed)
- WHEN an absent publication identity is appended with a stale `expectedVersion`
- THEN the append returns `types.ErrVersionConflict` and no message is appended

### Requirement: A publication sink records each publication once

The harness MUST append through an atomic identity-keyed operation and MUST NOT read a sink to prove absence before it appends. A sink MUST record the publication at most once. An identical existing payload MUST NOT advance the event sequence. A conflicting existing payload MUST be an error.

#### Scenario: concurrent reconciliation inserts once
ID: `context.compaction-publication-once` (proposed)
- WHEN two runs reconcile the same publication at the same time
- THEN each sink holds one audit record and one event, and the event sequence advances once

### Requirement: Compaction audit records hash the payload and verify backward

The audit hash MUST cover the additive compaction payload deterministically and identically in the memory and Postgres implementations, through one shared encoding helper. The payload MUST NOT enter the fixed hash field list, so a record sealed before the payload existed MUST still verify. A reader MUST hash a deep copy of the payload so no caller mutation moves the chain.

#### Scenario: payload covered and old records verify
ID: `context.compaction-audit-hash` (proposed)
- WHEN the memory and Postgres audit logs seal a `Compacted` record carrying the compaction payload
- THEN both compute the same hash over the same payload bytes and a record sealed before the payload existed still verifies

### Requirement: An active compaction requires a bound context guard

`Build` MUST refuse a flow whose context policy carries a compactor when no context guard is bound through `WithCompactionGuard`, because a summary is guarded before it is persisted and an unguarded compaction would persist an unguarded summary. The refusal MUST name the flow and the profile and MUST name the option. The core module MUST NOT construct a guard of its own, and a flow without a compactor MUST be unaffected.

#### Scenario: unguarded compaction refuses the build
ID: `context.compaction-guard-required` (proposed)
- WHEN a flow declares a compactor with a resolvable count and a validated fallback, but no guard is bound
- THEN `Build` returns an error naming the flow, the profile and `WithCompactionGuard`, and no compaction runs

### Requirement: Active compaction requires a resolvable count

`Build` MUST refuse a flow whose context policy carries a compactor when no token count is resolvable for the profile, meaning neither a declared estimator nor a counting model. The refusal MUST name the flow and the profile. The harness MUST NOT invent an estimator, a ratio, or a default fallback.

#### Scenario: missing counting refuses the build
ID: `context.compaction-counting-required` (proposed)
- WHEN a flow declares a compactor with no declared estimator and a model without a token counter
- THEN `Build` returns an error naming the flow and the profile and no compaction runs

### Requirement: The guard-rejection fallback is injected and validated

The core module MUST NOT construct a fallback projection from a policy package. The fallback MUST be supplied by the caller and MUST validate against the profile. `Build` MUST refuse an active compaction whose fallback does not validate. A counting model MUST NOT require a declared estimator when the injected fallback is valid.

#### Scenario: unvalidated fallback refuses the build
ID: `context.compaction-fallback-required` (proposed)
- WHEN a flow declares a compactor with a fallback that cannot reduce the request for the profile
- THEN `Build` returns an error naming the flow and the profile

### Requirement: A guard rejection applies the fallback once and fails closed

A summary the guard rejects MUST NOT be persisted. The harness MUST apply the fallback once, reassemble once and count the rejection once. The harness MUST refuse the provider call when the request still exceeds its budget after the fallback.

#### Scenario: fallback still over budget refuses the call
ID: `context.compaction-fallback-fails-closed` (proposed)
- WHEN the guard rejects the summary and the fallback leaves the request above its budget
- THEN no provider invocation happens and the rejected count increments once

### Requirement: One trigger, two measurements

The compaction trigger MUST be the overflow of the assembled request, measured after the declared projections and the collapse step. `Compaction.Tokens` MUST mean the token count of the rendered summary block at decision time. The publication MUST separately measure the assembled request's size immediately before and after compaction as `TokensBefore` and `TokensAfter`. The trigger MUST NOT use provider-reported usage.

#### Scenario: tokens measured separately from the summary
ID: `context.compaction-tokens-measured` (proposed)
- WHEN a compaction fires on a request estimated at 90 000 tokens and the summary renders at 4 000 tokens
- THEN `Compaction.Tokens` is the summary's count and the publication's `TokensBefore` and `TokensAfter` are the request's sizes

### Requirement: The same turn measures the request it sends

The harness MUST assemble a prospective history from the predetermined message identity and version and MUST record `TokensBefore` and `TokensAfter` with the publication. After the append the harness MUST reload the history, replace the working history and version, and reassemble through the shipped assembly composition with the same request options and provider declaration transformations. The measured size MUST equal the durable value before the harness publishes or invokes the provider.

#### Scenario: next request is measured after the append
ID: `context.compaction-same-turn-measurement` (proposed)
- WHEN the compaction block is appended and the turn continues
- THEN the harness reloads the history, reassembles the request it actually sends, and refuses the call when the measured size differs from the persisted `TokensAfter`

### Requirement: A measurement mismatch fails closed

A measured size that differs from the durable `TokensAfter` MUST prevent the provider invocation. The harness MUST NOT emit the differing measurement as the recorded fact.

#### Scenario: nondeterministic assembly fails closed
ID: `context.compaction-measurement-fails-closed` (proposed)
- WHEN assembly produces a different size for the same durable history than the prospective measurement
- THEN no provider call happens and the published record carries no corrected value