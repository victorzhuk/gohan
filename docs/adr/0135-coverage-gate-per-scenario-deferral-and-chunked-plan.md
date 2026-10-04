# Coverage gate: per-scenario deferral, plan cut into chunks

Status: accepted · Origin: review round, 2026-10-04

## Decision

`openspec/scenarios.json` carries a third traceability field per scenario, `deferred_to: <milestone>`, set only where a scenario is deliberately not turned green in the milestone being gated. `task spec:coverage` fails when a registered scenario has no matching subtest and no `deferred_to` later than the gated milestone, and fails on a subtest named like a scenario ID that the registry does not hold. `openspec/changes/m0-core/tasks.md` keeps its reviewed task rows as the scope record and is cut underneath them into chunk rows: one package or type-cluster, one to four scenario IDs, the files the chunk touches, and one literal verify command.

## Context and evidence

The registry held `capability` and `origin` and nothing else, so the deferred list existed only as prose in `tasks.md`: coverage could not separate a deliberately deferred scenario from a missing test, and eight `model.*` scenarios (ADR-0105) belonged to no task and to no deferred list. Ten of the 34 task rows carried 163 of 285 scenarios while each claimed at most one working day — task 23 named 24 — so a row was not an executable unit.

## Considered options

- A frozen baseline file of expected-missing IDs. Encodes intent only as a diff, and must be regenerated every milestone.
- A separate `openspec/deferrals.yaml`. A second source of truth that drifts from the registry.
- Leaving coverage advisory. No task's definition of done would be mechanically enforceable.

## Consequences

Every milestone gate becomes one command. Adding a scenario to a later milestone means setting `deferred_to`; closing it means clearing the field. Task rows stay the reviewed scope, so `tasks.md` no longer claims one-day granularity.
