# ADR-0069: One repository, many modules: root module (`core`, `std`, `testkit`), one module per `adapter/<name>` with prefixed tags; `go.work` for development only; released adapter `go.mod` files require a published core version, never `replace`. Promotion rule to a separate repo: external maintainer, CI needing secrets/infrastructure core should not own, or dependency churn dominating the repo

Status: accepted · Origin: gohan-spec v0.13 decision D69

## Decision

One repository, many modules: root module (`core`, `std`, `testkit`), one module per `adapter/<name>` with prefixed tags; `go.work` for development only; released adapter `go.mod` files require a published core version, never `replace`. Promotion rule to a separate repo: external maintainer, CI needing secrets/infrastructure core should not own, or dependency churn dominating the repo.

## Context and evidence

aws-sdk-go-v2/testcontainers/grpc pattern; OTel's core+contrib split pays a two-PR tax on every cross-cutting change, which is our M1–M3 phase.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
