# Spec Delta

## Context

This delta ADDS requirements to the `sandbox` capability. It does not restate or replace any existing requirement. The `Lifecycle and resume` and `Limits and outputs` requirements, and the `sandbox.output-to-store` scenario they own, stay with their current packages; the new ids below are proposed and unused by another package.

## ADDED Requirements

### Requirement: Sanitization precedes persistence

Tool output bytes MUST be sanitized for every injected secret value before any byte is persisted to the `OutputStore` or rendered as an inline excerpt. The stored document and the head excerpt MUST both derive from the sanitized bytes, so a `Ref` served by `read_output` cannot carry an injected value. Sanitization MUST be idempotent, and MUST NOT alter bytes when the policy injects no value.

#### Scenario: stored output is redacted
ID: `sandbox.output-stored-redacted` (proposed)
- WHEN `run_code` prints an injected value and stdout exceeds `MaxOutput`
- THEN the `OutputStore` holds the redacted bytes and the result's head excerpt carries the redaction marker

#### Scenario: read_output cannot re-enter the value
ID: `sandbox.output-ref-sanitized` (proposed)
- WHEN the `Ref` produced by a redacted result is read back through `read_output`
- THEN the returned content contains no injected value

#### Scenario: redaction is deterministic
ID: `sandbox.output-redaction-deterministic` (proposed)
- WHEN two calls emit equal stdout containing the same injected value
- THEN both results carry the same content-addressed `Ref` and the same redacted excerpt

### Requirement: Route-independent leak counting

Every occurrence of an injected value in a stream MUST increment `gohan.sandbox.secret_leak` exactly once, whether the bytes stay inline or spill to the store, and a second sanitization pass over an already-redacted result MUST NOT count again. The inline text MUST be bounded by `ToolSpec.MaxOutput` measured after sanitization, plus the truncation marker.

#### Scenario: count is route independent
ID: `sandbox.leak-count-route-independent` (proposed)
- WHEN a call prints an injected value twice with stdout under the cap and a second call prints it over the cap
- THEN `gohan.sandbox.secret_leak` counts every occurrence once for both routes

#### Scenario: excerpt bounded after redaction
ID: `sandbox.excerpt-cap-boundary` (proposed)
- WHEN sanitized stdout reaches exactly `MaxOutput` bytes and one byte beyond it
- THEN the first result is returned inline with no `Ref` and the second carries a head excerpt of at most `MaxOutput` bytes plus the marker

### Requirement: Overflow without a store

When a stream exceeds `MaxOutput` and no `OutputStore` is wired, the tool MUST return the sanitized head excerpt with a marker that names the dropped byte count and states that no store is configured. It MUST NOT return the whole body inline, and it MUST NOT persist bytes.

#### Scenario: no-store overflow is explicit
ID: `sandbox.no-store-overflow` (proposed)
- WHEN stdout exceeds `MaxOutput` and no `OutputStore` is wired
- THEN the result carries a bounded head excerpt and a marker naming the dropped bytes and the missing store, and no `Ref` is produced
