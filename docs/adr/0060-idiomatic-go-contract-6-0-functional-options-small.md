# ADR-0060: Idiomatic-Go contract (§6.0): functional options, small interfaces, callbacks over frameworks, `context` first, sentinel + typed errors, `iter.Seq2` for streams, no init-time registration, no globals, no reflection in hot paths

Status: accepted · Origin: gohan-spec v0.13 decision D60

## Decision

Idiomatic-Go contract (§6.0): functional options, small interfaces, callbacks over frameworks, `context` first, sentinel + typed errors, `iter.Seq2` for streams, no init-time registration, no globals, no reflection in hot paths.

## Context and evidence

The library must read like stdlib-adjacent Go, not like a port of a Python framework.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
