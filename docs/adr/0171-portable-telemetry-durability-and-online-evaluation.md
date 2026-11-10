# ADR-0171: Portable telemetry delivery and bounded online evaluation

## Status

Accepted for change planning. Implementation and scenario evidence remain pending.

## Context

The frozen Telemetry port provides instrumentation without delivery acknowledgement. The OTel adapter borrows caller providers. The release spec describes online evaluation without complete failure or calibration semantics.

## Decision

Preserve frozen methods. Add separate export/lifecycle contracts for versioned completed records and explicit per-record outcomes. Keep dependency-free contracts in core/types, policy in std and SDK translation in adapters.

Implement an opt-in local WAL with synced durable acknowledgement, exclusive ownership, independent destination cursors and at-least-once replay. Distinguish queue acceptance, local durability and remote acceptance. Bound capacity and require explicit disposition of unresolved records. Redact before persistence and support subject erasure before replay.

Pull forward the minimum online evaluation slice. Bound asynchronous work and retain independent results, sampling provenance, evaluator versions and explicit unscored states. Calibrate judges against versioned human labels using configured criteria. No universal agreement threshold applies.

The ordered changes are telemetry-export-contracts, telemetry-local-wal and production-online-evaluation, after repair-release-verification. Their deltas define proposed acceptance behavior. Syncing them and registering scenarios belongs to implementation admission.

## Alternatives

Collector persistent queues suffice when durability after Collector receipt meets deployment requirements. Local WAL is needed for explicit local acceptance and offline replay. Direct OTel remains available for deployments accepting in-memory delivery risk.

## Consequences

Ambiguous remote acknowledgement can duplicate delivery and metric aggregation. Existing audit persistence remains independently authoritative. Proposed records and interfaces require compatibility review before implementation. No production-readiness claim follows from planning validation.
