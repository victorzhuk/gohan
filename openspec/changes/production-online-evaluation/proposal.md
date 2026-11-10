# Proposal: Bounded production evaluation

## Why

The release spec describes online judges but lacks production failure, evidence and calibration semantics.

## What Changes

- Pull the minimum online evaluation slice forward from M4.
- Keep independent quality, safety and execution results with explicit unscored states.
- Bound asynchronous work and durably export provenance-rich results.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `release`: bounded evaluation, calibration and result provenance.

## Impact

Depends on telemetry-local-wal. Use the existing evals module boundary. Full shadow orchestration, datasets and Langfuse integration retain their milestone owners.
