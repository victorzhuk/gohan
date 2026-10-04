# ADR-0045: Two modules with different contracts: `gohan` core (types, ports, chain-as-data, validation; zero default middleware, zero prompt text) and `gohan/std` (canonical chains, guards, notes, limits, prompts as readable functions)

Status: accepted · Origin: gohan-spec v0.13 decision D45

## Decision

Two modules with different contracts: `gohan` core (types, ports, chain-as-data, validation; zero default middleware, zero prompt text) and `gohan/std` (canonical chains, guards, notes, limits, prompts as readable functions).

## Context and evidence

Framework-exit retrospectives name hidden behavior and undebuggable layers as the reasons to leave.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
