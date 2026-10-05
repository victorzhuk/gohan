# gohan — working in this repository

gohan is a Go library for building AI agent harnesses: core contracts, `std` policies, adapters (eino, adk-go, OpenAI, Anthropic, Postgres, Redis, MCP, …). The specification is the source of truth; code implements it scenario by scenario.

## Read in this order

1. `docs/overview.md` — purpose, principles, capability map, milestones, open questions.
2. `docs/design/architecture.md` — layering, core budget rule (§4.2a), repository layout.
3. `docs/design/types.md` — every normative identifier and the capability that defines it (generated).
4. The capability spec for the task at hand: `openspec/specs/<capability>/spec.md`.
5. The ADRs the spec cites (`docs/adr/`), only when the *why* matters for the change.

The archived monolith `docs/archive/gohan-spec-v0.13.md` is history, not a source.

## Rules

- **Core budget** (`architecture.md` §4.2a): `core/` holds ports, types, events, errors and the driver; every default, matcher, preset or policy value lives in `std/`; anything with a third-party dependency lives in `adapter/<name>/` as its own module. `core` is a package tree — `core/` is the driver package `gohan`, `core/types` is the floor (ADR-0139).
- **Contract tiers**: code blocks in a spec's *Contract* section are normative unless marked `(illustrative)`; prose rules are normative; requirements are normative.
- **Scenario binding**: a scenario is covered by a subtest whose name is exactly its ID — `t.Run("flow.plain-invoke", …)`. No comments, no tags. `task spec:coverage` matches `openspec/scenarios.json` against `go test -json` subtests across all three modules (root, `adapter/otel`, `examples`). `task spec:gate` runs the same matching as a gate at milestone `M0.5` by default (`GATE=` overrides).
- **Types index**: after editing any spec run `task spec:types`; it fails on duplicate or undefined identifiers.
- **Changes**: work is organised as OpenSpec changes under `openspec/changes/<name>/` (`proposal.md`, `design.md`, `tasks.md`). A task is done when its listed scenario IDs pass, `task lint`, `task spec` (types index then `M0.5` gate) and `task test` (`-short`) are green.
- **Compatibility**: `docs/design/compatibility.md` — ports are frozen at v1 and grow only through optional interfaces; handles may grow; `task api:check` gates releases.
- **Spec edits** require an ADR under `docs/adr/` (next number) and a regenerated `docs/design/types.md`; capability additions are frozen until M0 ships (`docs/backlog.md`).
- **Code conventions**: Go 1.27 (`docs/design/go-baseline.md`), `log/slog` only, functional options, small interfaces, `errors.AsType`, no comments except *why* (and `// Deprecated:`), short error wraps (`"create user: %w"`), Conventional Commits. Never reference AI tooling, rule names or spec IDs in code or commit messages; scenario IDs appear only as subtest names.

## Commands

```
task spec:types      regenerate and check docs/design/types.md
task spec:coverage   spec coverage report: scenarios without tests / tests without scenarios
task spec:gate       spec gate across all modules (GATE, default M0.5)
task spec            types index followed by the spec gate
task api:check       apidiff of every module against its last tag
task lint            golangci-lint (depguard enforces the core budget)
task test            go test -short ./... across the workspace
task test:full       includes testcontainers and real-provider tests behind build tags
task bench           benchmark gate (openspec/specs/performance)
task examples:test   every example offline from cassettes
```

## Module

`github.com/victorzhuk/gohan`, Apache-2.0. Adapters: `github.com/victorzhuk/gohan/adapter/<name>`.
