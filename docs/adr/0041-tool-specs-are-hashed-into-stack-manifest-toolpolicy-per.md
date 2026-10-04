# ADR-0041: Tool specs are hashed into `Stack.Manifest()`; `ToolPolicy` per source (`Trusted`/`Untrusted`); untrusted descriptions are guarded at build and default to `ReadOnly`; `WithPinnedManifest` fails startup on drift

Status: accepted · Origin: gohan-spec v0.13 decision D41

## Decision

Tool specs are hashed into `Stack.Manifest()`; `ToolPolicy` per source (`Trusted`/`Untrusted`); untrusted descriptions are guarded at build and default to `ReadOnly`; `WithPinnedManifest` fails startup on drift.

## Context and evidence

Tool-description poisoning and post-approval rug pulls (ASI04, CVE-2025-54136).

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
