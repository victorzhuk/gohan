# 0163. Persisted compaction owns its publication and its build gates

Status: accepted

## Context

`stores.Compactor` was declared and never resolved: `ContextPolicy.Compactor`
was carried, no implementation existed, and assembly never hid a covered
version. The delta that repairs this (`m2-context-persisted-compaction`) adds
four optional store interfaces, a typed publication payload, a shared canonical
audit-hash segment, and three `Build` refusals for a flow whose policy carries
a compactor. None of that had a normative home in a main capability spec, so the
declaration an implementer reads and the declaration the shipped code satisfies
were two different documents.

## Decision

The `stores` Contract owns the compaction persistence surface and the `context`
Contract owns the compaction behaviour. No port gains a method: the three append
seams (`SessionCompactionAppender`, `CompactionAuditAppender`,
`CompactionEventAppender`), the fallback port and the shared hash helpers are new
optional interfaces discovered by type assertion, and a store that implements
none of them keeps every other operation unchanged.

The `AppendCompaction` order is fixed and identical in the memory and
PostgreSQL stores: validate the payload, authorize the request, look up the
publication identity, then compare `expectedVersion`. The identity lookup
precedes the stale-version check deliberately. A retried publication is the same
fact under the same identity, so its recorded version is already correct; a
stale-version check first would refuse a publication that is already durable and
force the caller to re-plan a compaction that has already happened. An identical
existing identity is a no-op that returns its original committed version, a
conflicting one is an error that mutates nothing and stays distinct from
`types.ErrVersionConflict`, and an absent identity with a stale version is
`types.ErrVersionConflict`. Validation and authorization run before the lookup,
so a refused request never exposes stored publication state.

Reconciliation is a provider-invocation boundary, not an assembly hook: the
durable publication outlives the turn that published it, so every actual
invocation inspects every durable publication under the payload's own publishing
run, native and foreign, including a foreign runtime that calls the model
directly. The store is the deduplication barrier, never a read of a live or
bounded-retention stream.

An active compaction requires three things at `Build`, each named with the flow
and the profile: a resolvable token count (the model's `TokenCounter` or a
declared estimator — no invented default), a fallback that validates against the
profile, and a bound context guard. The guard requirement is part of the same
rule: a summary is guarded at `types.StageContext` before it is persisted, so an
unguarded compaction would persist an unguarded summary. The guard is supplied
by the caller through `WithCompactionGuard`, exactly as the fallback is supplied
through `WithCompactionFallback`; core constructs neither, because core does not
import `std`.

One trigger, two measurements: the trigger is the assembled request's overflow
measured after the projections and the collapse step, and the audit payload's
`TokensBefore`/`TokensAfter` are that request's size, while `Compaction.Tokens`
is the rendered summary's own count. A same-turn measurement that disagrees with
the durable value fails the provider call closed rather than publishing a
corrected number.

This record adds no capability and no identifier that the deltas did not already
introduce; it fixes where each declaration lives.

## Related records

**ADR-0090 — context management.** That record owns the split this one
persists: `Projections` (`Truncate`, `ClearToolResults{Keep, ClearAtLeast,
Exclude}`) are deterministic assembly-time views over `History` and are never
written, while `Compaction` appends a persisted `Compaction` block to
`SessionLog` with `CoversUpTo` and assembly hides everything it covers. Its
summary invariants are carried unchanged here and are not re-decided by this
record: pending or unknown-outcome tool calls are never covered,
`ToolUse`/`ToolResult` pairs are never split, `notes_*` results are never
cleared, a text summary carries `OriginModel` and passes `StageContext` guards,
and an opaque compaction is model-bound and dropped on endpoint change. What
ADR-0090 left open — the block's persistence being declared but never
implemented and never hidden by assembly — is what this record closes.

**ADR-0157 — capability ports, record envelopes and journal arguments.** That
record owns where the ports live and how a stored record carries its version:
every capability port is declared in `core` and implemented outside it, the
compaction seams and the fallback port follow that rule unchanged, and a
persisted record is wrapped in `stores.RecordEnvelope` with the `SchemaVersion`
it was written at and decoded through the one generic `DecodeRecord[T]`. The
additive `AuditRecord.Compaction` payload needs no upcaster and no registry
entry: the envelope carries the record's version regardless, and a record that
gains an optional field is not a shape change — the N→N+1 chain of ADR-0157
applies to a versioned shape, not to this payload.

The publication identity and the append ordering, the reconciliation boundary,
the two-measurement rule and the three `Build` refusals are decided by this
record alone; neither ADR-0090 nor ADR-0157 states them.