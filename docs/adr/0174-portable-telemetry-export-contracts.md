# 0174. Portable telemetry export contracts

Status: accepted

## Context

The frozen `Telemetry` port in `core/types/telemetry.go` instruments spans and metrics without delivery acknowledgement. `adapter/otel/otel.go` uses caller-provided tracer and meter providers. It creates counters and histograms on first use and drops their creation errors and the corresponding observations. `std/telemetry` supplies conventions and metric label filtering.

ADR-0171 orders `telemetry-export-contracts` after `repair-release-verification`, whose tasks are complete. `telemetry-local-wal` and `production-online-evaluation` remain later in the chain.

## Decision

Implement ADR-0171's export and lifecycle clause as separate contracts. Place dependency-free records and ports in `core/types`, policy in `std/telemetry`, and SDK translation in `adapter/otel`. Preserve the frozen `Telemetry` methods through optional interfaces. Records carry versioned signal data and stable IDs.

Export reports accepted, retryable, permanent or ambiguous outcomes per record. Cancellation leaves unacknowledged records unresolved with their IDs. Stable IDs permit deduplication, not exactly-once delivery. Remote acceptance does not imply local durability.

Borrowed providers remain under caller ownership. Owned pipelines expose context-bounded flush and shutdown, report unresolved-work counts, and do not leak workers at deadline. Each destination has an independent bounded queue. Bound instruments, dimension values and total series. Redact and filter metrics before serialization or queue admission. Export diagnostics through an independent path, never recursively through the failing pipeline.

Completed spans cross supported SDK export boundaries, not replayed `StartSpan` calls. Preserve ancestry, timestamps and schema identity. Aggregate metric observations once per destination with declared temporality and histogram boundaries. Preserve instrument kind and unit. Reject unsupported signals and incompatible replay semantics at configuration.

## Consequences

Ambiguous remote acknowledgement can duplicate metric aggregation and must be reported. Content capture stays off by default. Direct in-memory delivery makes no crash-durability promise. Proposed records and interfaces require compatibility review before code lands; concrete Go signatures remain implementation artifacts.

## Alternatives

Direct OpenTelemetry instrumentation remains available where in-memory delivery risk is acceptable. Collector persistent queues can provide durability after Collector receipt. Neither replaces explicit local durable acknowledgement or offline replay, which belong to the later local WAL change.
