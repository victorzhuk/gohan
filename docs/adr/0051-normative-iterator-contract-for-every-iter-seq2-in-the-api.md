# ADR-0051: Normative iterator contract for every `iter.Seq2` in the API, enforced by a leak test in conformance

Status: accepted · Origin: gohan-spec v0.13 decision D51

## Decision

Normative iterator contract for every `iter.Seq2` in the API, enforced by a leak test in conformance.

## Context and evidence

Iterators run on the caller's goroutine; early break and cancellation are the leak paths.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
