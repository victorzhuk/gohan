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