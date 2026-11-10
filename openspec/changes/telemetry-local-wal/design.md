# Design: Durable local telemetry

## Context

The export-contract change supplies portable records and acknowledgement semantics. No local spool currently ships.

## Goals / Non-Goals

Provide local durability on Linux local filesystems. Network filesystems and physical media sanitization are outside this contract.

## Decisions

One process owns each spool through an exclusive OS lock. Versioned segment records use length framing and checksums. Reject interior corruption and unknown versions. Recover only an incomplete final record, preserving preceding records and reporting the repair. Sync segment creation and rotation metadata as well as data before durable acknowledgement.

Explicit durable append returns success only after sync. Batching amortizes sync without early acknowledgement. The void instrumentation bridge promises bounded admission only. Critical callers use explicit durable append. Queue acceptance is distinct from synced acceptance.

Delivery is at-least-once. Each destination keeps a durable cursor. Persist remote acknowledgements before reclamation. A crash between remote acceptance and cursor persistence can duplicate delivery. Preserve stable IDs. Order records per destination and use bounded batches. Permanent rejection parks delivery until repair or explicit discard.

Require byte quota, segment size, retention limit, retry bounds, worker cap and request deadlines at construction. Full disk rejects new append without deleting pending records. Expired pending records stop delivery and require explicit discard. Removing a destination requires explicit disposition of its backlog.

Redact before append. Content persistence is off by default. Use private file/directory permissions and deployment filesystem encryption controls. Subject erasure blocks affected replay, compacts segments and cursor state crash-safely, and removes retired files before completion. Never persist credentials. Erasure does not promise physical media sanitization.

Independent health reports pending bytes, oldest age, destination lag, retries, rejected appends and corruption. Flush distinguishes locally synced from remotely delivered work. Do not route health failures recursively through the WAL.

## Risks / Trade-offs

Risk depth: existing-service-strict. Process crash tests prove sync/cursor windows. Focused parser tests cover corrupt framing economically. At-least-once remote metrics can duplicate aggregation after ambiguous acceptance.

## Migration Plan

Land after telemetry-export-contracts. Opt in with an explicit spool location. Rollback stops admission and drains or retains pending segments. Never delete a spool as rollback. Update existing architecture, testing and compatibility guidance with implementation.
