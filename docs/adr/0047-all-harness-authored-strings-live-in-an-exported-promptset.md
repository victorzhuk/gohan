# ADR-0047: All harness-authored strings live in an exported `PromptSet`, passed to `Build`, hashed into the manifest

Status: accepted · Origin: gohan-spec v0.13 decision D47

## Decision

All harness-authored strings live in an exported `PromptSet`, passed to `Build`, hashed into the manifest.

## Context and evidence

Hidden prompts are the LangChain failure mode.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
