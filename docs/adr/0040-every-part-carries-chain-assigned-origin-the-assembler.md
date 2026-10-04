# ADR-0040: Every `Part` carries chain-assigned `Origin`; the assembler fences non-user/system origins; a `StageContext` guard runs on `ContextProvider` output and `notes_write` input

Status: accepted · Amended by ADR-0097: provenance is enforced at the tool boundary (`taint`). · Origin: gohan-spec v0.13 decision D40

## Decision

Every `Part` carries chain-assigned `Origin`; the assembler fences non-user/system origins; a `StageContext` guard runs on `ContextProvider` output and `notes_write` input.

## Context and evidence

Notes and memory providers are otherwise a persistent injection channel (OWASP ASI06/ASI01).

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
