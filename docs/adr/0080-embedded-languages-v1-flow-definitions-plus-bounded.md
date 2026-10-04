# ADR-0080: Embedded languages v1 = flow *definitions* plus bounded expressions (generalized by D84–D85): definitions compile at startup into `std/flow` compositions; expressions do routing and transforms with step/time caps and no host calls. No definition or expression can call a tool, a model or `suspend`. Resumable programs are a later contract

Status: accepted · Origin: gohan-spec v0.13 decision D80

## Decision

Embedded languages v1 = flow *definitions* plus bounded expressions (generalized by D84–D85): definitions compile at startup into `std/flow` compositions; expressions do routing and transforms with step/time caps and no host calls. No definition or expression can call a tool, a model or `suspend`. Resumable programs are a later contract.

## Context and evidence

Durable continuation of arbitrary programs is unproven; `try/catch` in the interpreter could swallow a host suspension signal.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
