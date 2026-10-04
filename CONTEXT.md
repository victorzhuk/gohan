# gohan

gohan fixes the structure, governance, identity, durability and telemetry of AI-agent features in Go services and delegates loop execution to adapters. This file fixes the words its spec corpus and change records use. It is a glossary, not a contract; the capability specs under `openspec/specs/` are normative.

## Language

**Capability spec**:
`openspec/specs/<capability>/spec.md` — the normative contract for one capability (`tools`, `stores`, `flow`, …).
_Avoid_: spec (bare), module, package

**Scenario ID**:
A stable `<capability>.<slug>` identifier registered in `openspec/scenarios.json`; exactly one subtest carries it as its name.
_Avoid_: requirement, test, case, tag

**Contract tier**:
*Normative* — code blocks in a spec's *Contract* section, its prose rules and its requirements. *Illustrative* — a block marked `(illustrative)`, whose names may change during M0 without a spec edit.
_Avoid_: draft, example (an `examples/` service is not an illustrative block)

**Core budget rule**:
`core` (package `gohan`) holds ports, types, events, error classes and the driver; every default, matcher, preset or policy value lives in `std`; anything with a third-party dependency lives in `adapter/<name>` as its own module (ADR-0098, `docs/design/architecture.md` §4.2a).
_Avoid_: layering (that is the service layering of §4.1), capability

**Acceptance source**:
The `origin` label of a registered scenario, naming the requirement round or decision it came from (`R1`–`R60`, `R5a`–`R5y`, `ADR-NNNN`, `RPERF`).
_Avoid_: origin (bare — `Origin` is a stored-block provenance type in `messages`)

**Change**:
`openspec/changes/<name>/` — `proposal.md`, `design.md`, `tasks.md`; the unit of work. `m0-core` is the only change carrying tasks.
_Avoid_: milestone, sprint, PR, epic

**Task row**:
One numbered item in a change's `tasks.md`: a scope line and the scenario IDs it turns green.
_Avoid_: chunk, step

**Chunk**:
The executable unit a task row is cut into: one package or type-cluster, one to four scenario IDs, its own files and one literal verify command.
_Avoid_: task, step, subtask

**Deferral**:
A scenario deliberately left red in the milestone being gated, recorded as `deferred_to: <milestone>` in `openspec/scenarios.json`.
_Avoid_: skip, waiver, xfail, ignored

**Milestone**:
A roadmap stage in `docs/overview.md` §12. M0 is `core` + `std` + `testkit` on the native runtime with memory stores; M0.5 is the core API review and the v1-candidate freeze; M1–M4 add capabilities and adapters.
_Avoid_: phase, release, version

**Acceptance process**:
One of the four production shapes the core API must survive before it freezes — Kafka refunds, Temporal travel, Camunda invoice, catalog enrichment (ADR-0083). Their offline paths run against memory stores and gate M0.5; their live engine runs gate M3.
_Avoid_: integration test, example (each is also shipped as an `examples/` service)

## Relationships

- A **capability spec** owns **scenario IDs**; a **change**'s **task rows** name the IDs they turn green.
- One **task row** contains several **chunks**; one **chunk** closes one to four **scenario IDs**.
- A **milestone** gate is the set of **scenario IDs** that are neither green nor **deferred** to a later milestone.
- The **core budget rule** decides which module a **chunk** lands in.

## Example dialogue

> **Dev:** "Task row 22 lists 17 scenario IDs — is that one chunk?"
> **Maintainer:** "No. A chunk is bounded by the package it lands in and by one verify command: `runtime.batch-*` is a chunk, and `runtime.max-turns` with `runtime.cancellation` is another. The task row stays in `tasks.md` as the scope it was reviewed at."

## Flagged ambiguities

- `origin` carries two meanings: the **acceptance source** of a scenario in `scenarios.json`, and the provenance of a stored block (`Origin`, `messages`). Name them separately.
- "spec" names both a **capability spec** (normative) and the superseded v0.13 monolith at `docs/gohan-spec.md` and `docs/archive/gohan-spec-v0.13.md`. Name the file.
- "state" names a stepper's `State` (`runtime`), a run's `RunState` (`stores`), `SharedState` (`agui`) and durable **working state** (`working-state`). Name which.
- "test" names both a Go test function and the subtest whose name is a **scenario ID**; only the latter satisfies scenario binding.
