# Design: Portable telemetry export

## Context

core/types/telemetry.go defines void instrumentation methods. adapter/otel/otel.go accepts borrowed providers, creates counters and histograms lazily and discards creation errors. std/telemetry owns conventions and label filtering.

## Goals / Non-Goals

Support direct OTel instrumentation and portable completed-record export. Logs retain their existing slog contract. Audit persistence retains its existing failure semantics.

## Decisions

Use dependency-free records and ports in core/types, policy in std/telemetry and SDK translation in adapter/otel. Records carry format version, stable ID, resource identity, signal kind and typed payload. Spans preserve trace/parent IDs, start/end timestamps, outcome and schema identity. Metric records preserve instrument kind, unit, observation time and dimensions.

Export batches return per-record accepted, retryable, permanent or ambiguous outcomes. Remote acceptance does not imply local durability. Destinations declare supported signals and replay semantics. Reject incompatible configurations before admission. Stable IDs permit deduplication but cannot guarantee exactly-once remote aggregation.

Preserve Telemetry methods. Add optional gauge support and instrument descriptors. Bound instrument names, dimension values and total series. Diagnose absent gauge support and instrument creation failures through independent slog/health reporting. Never send exporter diagnostics recursively through the failing pipeline.

Borrowed providers retain caller ownership. Owned pipelines expose context-bounded flush and shutdown with unresolved-work counts. Each destination has an independent bounded queue. Direct mode reports drops and makes no crash-durability promise.

Export completed spans through supported SDK export boundaries, not replayed StartSpan calls. Aggregate metric observations once per destination with declared temporality and histogram boundaries. Ambiguous metric delivery can duplicate remote aggregation and must be reported. Verify exact SDK APIs against adapter/otel/go.mod and official documentation before implementation. Pin semantic conventions independently.

Apply redaction and metric filtering before serialization or queue admission. Content capture stays off by default. Collector persistent queues remain an alternative when durability after Collector receipt suffices: https://opentelemetry.io/docs/collector/resiliency/.

## Risks / Trade-offs

Risk depth: existing-service-strict. Metric duplication and queue saturation require observable acceptance. Optional interfaces preserve frozen methods. Exact Go declarations are implementation design artifacts subject to compatibility review before code lands.

## Migration Plan

Depends on repair-release-verification. Opt in at the composition root. Disable admission and drain owned queues to roll back. Update existing architecture, testing and compatibility documents with implementation.
