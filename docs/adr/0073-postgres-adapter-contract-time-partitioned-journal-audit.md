# ADR-0073: Postgres adapter contract: time-partitioned journal/audit/event tables (retention = `DROP PARTITION`), small hot `runs` table with coalesced heartbeats, `SKIP LOCKED` batched reaping, session timeouts on every pool, and a lint that forbids holding a transaction across a model or tool call

Status: accepted · Origin: gohan-spec v0.13 decision D73

## Decision

Postgres adapter contract: time-partitioned journal/audit/event tables (retention = `DROP PARTITION`), small hot `runs` table with coalesced heartbeats, `SKIP LOCKED` batched reaping, session timeouts on every pool, and a lint that forbids holding a transaction across a model or tool call.

## Context and evidence

Update-heavy queue tables bloat under autovacuum; long transactions pin the MVCC horizon; bulk deletes are the worst pattern.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
