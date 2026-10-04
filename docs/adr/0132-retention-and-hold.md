# ADR-0132: Retention tiers, legal hold and zero retention

Status: accepted · Origin: grill round 58 (2026-09-30)

## Decision

One `RetentionPolicy{Conversation, Working, Audit}` per stack, overridable per tenant through `RetentionSource`. Conversation covers every session-keyed record from `LastActivity`; Working covers notes and subject memory from last write; Audit is never shorter than Conversation. `Stack.Maintain` purges tenant by tenant, skips pinned, archived and held sessions, and writes one `purge` audit record per tenant. `Conversation: 0` (`agent.Ephemeral()`) deletes the session's content at `Finish`, audit checksums remaining. `SessionMeta.Hold` (scope `session:hold`, audited) suspends purge, `DeleteSession` (`ErrSessionHeld`) and zero retention, and `EraseSubject` reports held sessions instead of erasing them.

## Context and evidence

Retention guidance for LLM products separates user-visible conversation, system context, immutable audit and derived artifacts into tiers with independent clocks, puts TTL on the row rather than the table, routes deletion through a hold check, logs every purge, and treats zero data retention as the enterprise default. gohan had scattered purge methods, no policy object, no hold and no way to express a zero-retention tenant.

## Consequences

`stores` v1.9 (`RetentionPolicy`, `RetentionSource`, `MaintainReport`, `Stack.Maintain`, `SessionMeta.Hold`, `SessionPatch.Hold`, `ErrSessionHeld`, rule block, four scenarios), `redaction` v1.3 (`EraseReport.Held`, one scenario), `working-state` v1.4 (one scenario), `identity` v1.5 (one scenario), `build` v1.4 (options), catalog row; task 11.
