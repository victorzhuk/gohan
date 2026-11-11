# 0175. Durable local telemetry WAL

Status: accepted

## Context

ADR-0174 shipped dependency-free export records, per-record acknowledgement outcomes and the `std/telemetry` delivery pipeline. That pipeline holds admitted work in memory only: a process crash loses locally accepted records, an offline process cannot queue exports, and remote acceptance is the only acknowledgement. ADR-0171 orders `telemetry-local-wal` after `telemetry-export-contracts`, whose tasks are complete.

## Decision

Implement ADR-0171's local durability clause as an opt-in spool owned by `std/telemetry`. One process owns each spool through an exclusive OS lock; a second opener fails. Segments carry versioned, length-framed, checksummed records. Recovery rejects interior corruption and unknown versions, and repairs only an incomplete final record, preserving preceding records and reporting the repair. Segment creation, rotation metadata and data are synced before durable acknowledgement; explicit durable append returns success only after sync. Batching amortizes syncs without acknowledging early.

Delivery stays at-least-once. Each destination persists its acknowledgement cursor before record reclamation; a crash between remote acceptance and cursor persistence can duplicate delivery, and stable IDs permit deduplication. Records are ordered per destination and delivered in bounded batches. Permanent rejection parks delivery until repair or explicit discard.

Construction requires a byte quota, segment size, retention limit, retry bounds, worker cap and request deadlines. A full disk rejects new appends without deleting pending records. Expired pending records stop delivery until explicit discard. Removing a destination requires explicit disposition of its backlog.

Captured content is redacted before append, and content persistence stays off by default. Spool files and directories use private permissions and rely on deployment filesystem encryption controls. Subject erasure blocks affected replay, compacts segments and cursor state crash-safely, and removes retired files before completion. Credentials are never persisted. Erasure does not promise physical media sanitization; network filesystems are out of scope.

## Consequences

The void instrumentation bridge keeps its bounded-admission-only promise; critical callers use explicit durable append. Queue acceptance stays distinct from synced acceptance. Health reports pending bytes, oldest age, destination lag, retries, rejected appends and corruption, and never routes health failures recursively through the spool. At-least-once remote metrics can duplicate aggregation after ambiguous acknowledgement, and process crash tests must prove the sync and cursor windows.

## Alternatives

Collector persistent queues provide durability after Collector receipt but not for offline processes or pre-acknowledgement crashes. Application-level retries over the in-memory pipeline lower loss probability without bounded, auditable local durability. Neither replaces explicit local durable acknowledgement, which this change owns.
