# Proposal: Durable local telemetry

## Why

Offline operation and exporter outages need durable local acceptance with explicit loss and replay behavior.

## What Changes

- Add opt-in bounded WAL storage, synced acknowledgement and restart replay.
- Track independent destination progress and explicit discard decisions.
- Enforce privacy, erasure, exclusive ownership and corruption recovery.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `telemetry`: durable local export delivery.

## Impact

Depends on telemetry-export-contracts. Standard-library storage implementation in std/telemetry. No new storage dependency or frozen method change.
