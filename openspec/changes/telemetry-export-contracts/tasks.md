# Tasks

Risk depth: existing-service-strict. No skipped, flaky or infrastructure-failed scenario passes. Allow one fixture repair and two diagnostic reruns per boundary. Stop on changed contracts or exhausted recovery limits.

## 1. Contracts

- [x] 1.1 Add the next available ADR, sync the approved delta, register its scenario IDs and regenerate docs/design/types.md. Verify `task spec:types` and frozen method compatibility.
- [x] 1.2 Finalize additive Go declarations and serialized shapes from design.md before implementation. Verify review against existing telemetry, redaction and compatibility contracts.

## 2. Behavior

- [x] 2.1 Implement and prove `telemetry.export-acceptance` with an exact-ID public acceptance subtest and every delta assertion. Update existing architecture/testing guidance for the behavior.
- [x] 2.2 Implement and prove `telemetry.export-lifecycle` with an exact-ID public acceptance subtest and every delta assertion. Update existing architecture/testing guidance for the behavior.
- [x] 2.3 Implement and prove `telemetry.export-signal-fidelity` with an exact-ID public acceptance subtest and every delta assertion. Update existing architecture/testing guidance for the behavior.
- [x] 2.4 Implement and prove `telemetry.export-bounds-privacy` with an exact-ID public acceptance subtest and every delta assertion. Update existing architecture/testing guidance for the behavior.

## 3. Integration

- [x] 3.1 Run assigned scenarios, process crash tests where applicable and targeted race checks. Retain assertion-level evidence, process deadlines and worker bounds. Unit tests require named residual risks beyond acceptance coverage.
- [x] 3.2 Verify `task lint`, `task spec`, `task test`, `task api:check` and affected standalone adapter checks after inspecting wrappers. Bound command duration and concurrency. Update existing `[Unreleased]` entries for shipped behavior.
