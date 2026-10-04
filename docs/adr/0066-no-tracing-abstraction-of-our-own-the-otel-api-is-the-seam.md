# ADR-0066: No tracing abstraction of our own: the OTel API is the seam. A `Convention` mapping layer (pinned schema) translates `gohan.*` attributes to `gen_ai.*` and vendor namespaces (`langfuse.*`); renames are config changes

Status: accepted · Origin: gohan-spec v0.13 decision D66

## Decision

No tracing abstraction of our own: the OTel API is the seam. A `Convention` mapping layer (pinned schema) translates `gohan.*` attributes to `gen_ai.*` and vendor namespaces (`langfuse.*`); renames are config changes.

## Context and evidence

GenAI conventions are still "Development" with recent renames; Langfuse ingests OTLP and has no Go SDK.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
