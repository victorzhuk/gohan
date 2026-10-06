# Proposal: m1-redaction-pipeline

## Why

The normative redaction and guard contracts require stable pseudonyms, redacted persistence, output rehydration, and residency enforcement. The matrix records all six assigned scenarios as missing (`openspec/changes/m1/design.md:115-124`). `std/redact` does not exist, and `std/guard` has no redaction stage. This change connects those requirements to the public run path without taking subject erasure, which belongs to `m1-subject-erasure-residency`.

## What Changes

- Add the layered detector, token, redactor, key, and redaction-map implementation under `std/redact`.
- Apply the flow's redaction policy before prompt assembly and before persisted or captured content can expose raw entities.
- Join rehydration to the existing windowed output guard so released windows never split a pseudonym and unknown tokens never reach users.
- Enforce tenant residency at `Build` across reachable profiles and configured stores. Report store implementations without region evidence rather than treating them as compliant.
- Preserve the separate `m1-subject-erasure-residency` ownership of `EraseSubject` and its four scenarios.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `redaction`: implement pseudonym stability, storage protection, streaming rehydration, unknown-token removal, and residency enforcement.
- `guards`: implement the existing `guards.redaction` input rewrite behavior.

## Impact

This change adds `std/redact` and modifies the guard, assembly, Build, persistence, output, and telemetry paths. It adds no provider service dependency. It does not implement `EraseSubject`, add an HTTP server, or cut release tags. Residency enforcement cannot admit current store ports as region-aware until each configured implementation supplies verifiable region metadata.