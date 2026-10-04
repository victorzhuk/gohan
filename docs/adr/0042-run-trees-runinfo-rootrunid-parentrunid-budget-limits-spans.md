# ADR-0042: Run trees: `RunInfo.RootRunID`/`ParentRunID`; budget, limits, spans and recovery apply to the tree

Status: accepted · Origin: gohan-spec v0.13 decision D42

## Decision

Run trees: `RunInfo.RootRunID`/`ParentRunID`; budget, limits, spans and recovery apply to the tree.

## Context and evidence

Hub-and-spoke orchestration costs ~15× tokens; per-leaf limits do not bound it.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
