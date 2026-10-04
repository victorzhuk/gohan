# Project: gohan

## Purpose

A Go library that fixes the structure, governance, identity, durability and telemetry of AI agent features in services, and delegates loop execution to adapters (native, eino, adk-go). See `docs/overview.md`.

## Tech stack

- Go 1.27 (`docs/design/go-baseline.md`): `encoding/json/v2`, `iter`, `log/slog`, `synctest`, `tool` directives.
- Root module `github.com/victorzhuk/gohan` (core + std + testkit); each `adapter/<name>` its own module; `examples/` one module. Apache-2.0.
- Tooling: go-task, golangci-lint with depguard, govulncheck, testcontainers (Postgres, Redis), OpenTelemetry API.

## Conventions

- Layering and the core budget rule: `docs/design/architecture.md` §4.1–§4.2a.
- Contract tiers and requirement format: every `openspec/specs/<cap>/spec.md` has *Purpose*, *Contract* (normative Go code and prose unless marked `(illustrative)`), *Requirements* with `### Requirement:` groups and `#### Scenario:` entries carrying a stable `ID: \`<cap>.<slug>\``.
- Scenario ↔ test binding: subtest named exactly by the ID. `openspec/scenarios.json` is the registry; `tools/gen_types_index.py` and `task spec:coverage` are the checks.
- Decisions: `docs/adr/NNNN-*.md`, one per decision, with `Superseded by` / `Amended by` pointers; specs cite ADRs, never the reverse.
- Style: `AGENTS.md` (zero comments unless why, short error wraps, functional options, slog only, Conventional Commits, no tooling references in code).

## Change workflow

1. Pick the change under `openspec/changes/` for the current milestone; take the next unchecked task.
2. Read the capability spec(s) the task names and the scenario IDs it must turn green.
3. Implement in the package the core budget rule dictates; write the subtests first.
4. Definition of done: listed scenarios pass; `task lint`, `task spec:types`, `task spec:coverage`, `task test` green.
5. If the spec is wrong or incomplete: write the ADR, edit the spec, regenerate `types.md`, then implement.

## Milestones

M0 core → M0.5 API review → M1 (`taint`, `Verify`, redaction, cache, flags, lifecycle, first examples) → M2 (eino, `context`, `sandbox`) → M3 (adk-go, `subflows`, `interop`, `release`, engines, postgres/redis hardening) → M4 (`skills`, `agui`, evals, jev, langfuse, openfeature, `languages`). Scope lines live in `openspec/changes/<milestone>/proposal.md`.
