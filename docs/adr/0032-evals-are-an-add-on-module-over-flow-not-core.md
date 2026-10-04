# ADR-0032: Evals are an add-on module over `Flow`; not core

Status: accepted · Amended by ADR-0096: the evals module contract is specified in `release`. · Origin: gohan-spec v0.13 decision D32

## Decision

Evals are an add-on module over `Flow`; not core.

## Context and evidence

Keeps core small; evals reuse `Decider` and record/replay.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
