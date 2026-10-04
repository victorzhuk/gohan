# ADR-0086: `Redactor` port: layered detection (rules in std, NER/LLM via `Decider`), reversible tenant-keyed pseudonymisation (`KeySource`; HMAC default, FPE/vault implementations), fixed placement — before the prompt, before every store write, before telemetry content capture — and streaming-safe rehydration at egress inside the output stage; unknown or hallucinated tokens are dropped and counted. `EraseSubject` spans `SessionLog`, `EventLog`, `OutputStore`, `Checkpoints` and grants; `AuditLog` keeps checksums and records the erasure. Residency: `Region` on profiles and stores, `Residency` on the principal's tenant; `Build` refuses any router path or store that would leave a bound tenant's region

Status: accepted · Origin: gohan-spec v0.13 decision D86

## Decision

`Redactor` port: layered detection (rules in std, NER/LLM via `Decider`), reversible tenant-keyed pseudonymisation (`KeySource`; HMAC default, FPE/vault implementations), fixed placement — before the prompt, before every store write, before telemetry content capture — and streaming-safe rehydration at egress inside the output stage; unknown or hallucinated tokens are dropped and counted. `EraseSubject` spans `SessionLog`, `EventLog`, `OutputStore`, `Checkpoints` and grants; `AuditLog` keeps checksums and records the erasure. Residency: `Region` on profiles and stores, `Residency` on the principal's tenant; `Build` refuses any router path or store that would leave a bound tenant's region.

## Context and evidence

Session history was an unredacted PII store; irreversible redaction destroys entities the model needs; erasure and residency are enterprise requirements no external proxy can satisfy for gohan's own stores.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
