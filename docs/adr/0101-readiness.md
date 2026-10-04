# ADR-0101: Spec-driven development readiness

Status: accepted · Origin: review round 27 (2026-09-29)

## Decision

`AGENTS.md` at the repository root and `openspec/project.md` are the entry points for implementing agents: reading order, core budget rule, contract tiers, scenario binding, commands and definition of done. Scenario binding is by subtest name equal to the scenario ID; `task spec:coverage` matches `openspec/scenarios.json` against `go test -list`. `m0-core/tasks.md` is rewritten as 33 tasks of at most one day, scoped to the 21 M0 capabilities, each listing the scenario IDs it turns green, with an explicit deferred list; `design.md` records the single-`core`-package plan, ordering rationale, memory-implementation policy and M0 test strategy. Q11 is resolved: `github.com/victorzhuk/gohan`, Apache-2.0. The performance reference machine is the CI runner class; local runs are advisory. M1–M4 exist as scope stubs under `openspec/changes/`.

## Context and evidence

The previous task list contradicted the M0 scope, carried no acceptance criteria and no binding between scenarios and tests; an agent starting M0 would have had to invent process before writing code.

## Consequences

Every task is now checkable mechanically; `docs/overview.md` non-goals and totals updated.
