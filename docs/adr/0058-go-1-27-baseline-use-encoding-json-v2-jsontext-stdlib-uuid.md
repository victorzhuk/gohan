# ADR-0058: Go 1.27 baseline; use `encoding/json/v2` + `jsontext`, stdlib `uuid`, `errors.AsType`, `testing/synctest`, `httptest.NewTestServer`, goroutine-leak profile; fall back to 1.26 only for a feature with no 1.27 benefit

Status: accepted · Origin: gohan-spec v0.13 decision D58

## Decision

Go 1.27 baseline; use `encoding/json/v2` + `jsontext`, stdlib `uuid`, `errors.AsType`, `testing/synctest`, `httptest.NewTestServer`, goroutine-leak profile; fall back to 1.26 only for a feature with no 1.27 benefit.

## Context and evidence

Greenfield library shipping in 2027; two supported Go versions at any time.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
