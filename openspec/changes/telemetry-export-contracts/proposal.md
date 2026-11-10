# Proposal: Portable telemetry export

## Why

Instrumentation exists, but exporter acknowledgement, failure reporting and lifecycle behavior have no portable contract.

## What Changes

- Preserve the frozen Telemetry interface and add separate export and lifecycle contracts.
- Export completed spans and metric observations with explicit signal semantics.
- Bound queues, instruments and series. Diagnose failures independently.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `telemetry`: portable export, lifecycle, signal fidelity and bounded admission.

## Impact

Dependency-free records and ports in core/types, policies in std/telemetry and SDK translation in adapter/otel. No publication or tag changes.
