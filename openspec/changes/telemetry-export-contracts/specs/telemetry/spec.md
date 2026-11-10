## ADDED Requirements

### Requirement: Export acceptance is explicit
Exporters MUST distinguish accepted, retryable, permanent and ambiguous results per record. Cancellation MUST leave unacknowledged records unresolved.

#### Scenario: Partial export acknowledgement
ID: `telemetry.export-acceptance`
- **WHEN** a batch is partially accepted before its response is interrupted
- **THEN** only acknowledged records advance and unresolved records retain their IDs

### Requirement: Telemetry lifecycle has an owner
Pipelines MUST distinguish borrowed and owned resources. Flush and shutdown MUST honor context deadlines and report unresolved work.

#### Scenario: Bounded pipeline closure
ID: `telemetry.export-lifecycle`
- **WHEN** pipeline closure reaches its deadline with a blocked exporter and borrowed providers
- **THEN** it reports unresolved work without closing borrowed providers or leaking workers

### Requirement: Export preserves signal semantics
Export MUST preserve ancestry, timestamps, schema identity, metric kind, unit and temporality. Unsupported signals MUST fail configuration.

#### Scenario: Equivalent destinations
ID: `telemetry.export-signal-fidelity`
- **WHEN** completed spans and gauge, counter and histogram observations reach two supported destinations
- **THEN** original ancestry and timestamps match and instrument semantics remain equivalent

### Requirement: Export admission is bounded and private
Pipelines MUST bound queues, instruments, dimension values and total series. Redaction MUST precede serialization. Diagnostics MUST avoid recursive export.

#### Scenario: Admission rejects unsafe growth
ID: `telemetry.export-bounds-privacy`
- **WHEN** producers submit secret-bearing content, unbounded dimensions and a failing instrument registration
- **THEN** forbidden content reaches no serialized output and bounded independent diagnostics report rejection
