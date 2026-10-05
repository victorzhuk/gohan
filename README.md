<p align="center">
  <img src="assets/logo_wide.png" alt="gohan — a blue Go gopher with a rice bowl beside the project wordmark" width="760" style="max-width:100%;height:auto">
</p>

<h1 align="center">gohan</h1>

<p align="center">
  <a href="https://github.com/victorzhuk/gohan/actions/workflows/ci.yml"><img src="https://github.com/victorzhuk/gohan/actions/workflows/ci.yml/badge.svg" alt="CI: spec, lint, test, security, api-check, bench"></a>
  <a href="https://pkg.go.dev/github.com/victorzhuk/gohan/core"><img src="https://pkg.go.dev/badge/github.com/victorzhuk/gohan/core.svg" alt="Go Reference"></a>
  <img src="https://img.shields.io/badge/go-1.27.1-00ADD8?logo=go&logoColor=white" alt="Go 1.27.1">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue" alt="License: Apache-2.0"></a>
</p>

<p align="center">Structure and governance for AI features in Go services — boundaries, identity, durability, approvals, telemetry — with loop execution and providers left to adapters.</p>

**Status:** Early development, not production ready. Core contracts, driver components, memory stores, governance building blocks, and offline examples are implemented. The public governed native-agent path (`Build` + `WithNativeAgent` + `NewNativeConversation`) and `Stack.Explain` are implemented and exercised by the shipped examples. `std.ExplainHandler` — the HTTP surface for `Explain` — does not exist yet and is M1 scope. Integration and compatibility review is open. See the [hardening review and next plan](openspec/changes/m0-hardening/plan.md). Requires Go 1.27.1.

- [Quickstart](#quickstart)
- [What is implemented](#what-is-implemented)
- [What is planned](#what-is-planned)
- [Modules](#modules)
- [Task targets](#task-targets)
- [Where to read](#where-to-read)
- [Compatibility review: pending](#compatibility-review-pending)

## Quickstart

`examples/quickstart` runs offline: no network, no API keys, no provider account. It appends a user message to a memory session log, drives one scripted turn through `gohan.Drive`, streams text deltas and persists the assistant reply.

```
timeout 2m go -C examples run ./quickstart
```

Expected output: `Hello, quickstart!`

The same run is asserted in CI by `TestQuickstartOffline` in `examples/quickstart/main_test.go`. The run registers a flow definition with `gohan.WithNativeAgent` and drives it through the conversation returned by `gohan.NewNativeConversation` — the shipped governed native path, not a hand-written `runtime.Stepper`.

## What is implemented

M0 task-list scope, in the root module unless noted:

- **Driver** — `Build`, `Stack`, `Drive`/`DriveResume`, `NewTool`, recovery, inspection, health probes and shutdown. Construction and governance integration remain under review.
- **Value types and ports** — messages and blocks, `Principal` and scopes, run trees, events, error classes, and the store ports. `core/types` holds the shared types; `core` holds the driver; `std` holds the recommended behavior.
- **Flow seams** — `Flow[In, Out]`, `FlowFunc` and `Conversation`, with attach/detach runs, steer, continue and mailbox delivery.
- **Runtime contract** — the `Stepper`/`Runtime` interface, the governed native execution path (`WithNativeAgent` registration, `NewNativeConversation`, per-run ledger, sequential approvals), plus batch and schedule execution helpers. A service registers a flow definition; the driver runs it.
- **Governance** — the permission gate and approval round-trip in `core/permission`; in `std`: the canonical model and tool chains with gating, guards, journal, shield, structured output with validate/repair, prompts, and presets (`Interactive`, `Agentic`, `Batch`). These pieces are wired into the driver's governed native execution path: a definition registered with `Build` resolves its chains, prompts, tool definitions and limits once, `NewNativeConversation` returns a `Conversation` over that resolution, and `Stack.Explain` projects it without spending a run.
- **Durability and recovery** — run leases with generation ownership, checkpoints as a versioned envelope, `Replay` resume, and the memory stores for sessions, checkpoints, journal, runs, audit log and event log.
- **Telemetry** — a dependency-free emission port in `core/types` (spans, counters, histograms) with the governed call sites emitting through it, and a backend-mapping convention layer in `std/telemetry`. The OpenTelemetry implementation lives in the separate `adapter/otel` module, which is the only module with OTel dependencies; core and std import no third-party package.
- **Testing** — `testkit/gohantest` (scripted model, cassettes, fakes, fault injection, leak checks), `testkit/conformance` and `testkit/storetest` suites.

Current boundaries: `std/flow.Extract` and `Classify` do more than a bare model call: each derives a strict schema, decodes, validates through `std/structured`, and spends a bounded repair turn (`WithRepairs`, one by default) before failing — `Extract` returns the typed value only after validation passes, and `Classify` fails as a permanent model error once the label set is missed. What they do not carry is the full documented governance seam: each resolves a governed model call from the Stack and the named profile (`Stack.RecipeModel` returns the `RecipeCall`) and takes its repair instruction from the resolved prompt set (`chains.PromptSet.RepairInstruction`, overridable per stack), but the recipe runs a bare model call — no journal, guard, shield or permission step, and no tool is offered to the call. And `Explain` ships: `Stack.Explain` projects the resolved configuration — profile, chains' steps, limits, prompts, release — from a flow name or handle with zero provider calls. `std.ExplainHandler`, the HTTP surface over it, does not exist yet and is specified in [the `chains` contract](openspec/specs/chains/spec.md) §6.8a for M1.

## What is planned

Not in the workspace. `docs/design/architecture.md` §4.3 is the normative mapping.

| Milestone | Adapters |
|---|---|
| M1 | `openai` (incl. vLLM and OpenAI-compatible gateways), `anthropic`, `postgres` (all store ports, `CompleteTx`) |
| M2 | `eino` (runtime, graph flows, model/tool bridges), `docker` (sandbox) |
| M3 | `adkgo` (runtime, workflow flows), `mcp` (client and server), `redis` (limiter, event log) |
| M4 | `agui` (transport), `jev` (decider), `cel` and `lispico` (expression/definition front-ends), `langfuse`, `openfeature`, `httpapi` |

## Modules

In `go.work` today — three modules, all `go 1.27.1`:

| Module | Path | Holds |
|---|---|---|
| `github.com/victorzhuk/gohan` | `.` | `core` (driver), `core/types`, `core/stores`, `core/runtime`, `std`, `testkit`. Root dependencies: stdlib only. |
| `github.com/victorzhuk/gohan/adapter/otel` | `adapter/otel` | OTel exporter; requires the published root version, no local `replace`. |
| `github.com/victorzhuk/gohan/examples` | `examples` | `quickstart`, `excursions`, `kafka-refunds`, `temporal-travel`, `camunda-invoice`. Offline fixtures and a scripted model. |

Every future `adapter/<name>` is its own module with its own version and its own third-party dependencies. Core and std never import a third-party dependency.

## Task targets

[Task](https://taskfile.dev) v3, installed in CI at 3.54.0.

```
task spec           # spec:types, then spec:gate
task spec:types     # regenerate docs/design/types.md; fails on duplicate or undefined identifiers
task spec:coverage  # scenarios without a subtest, and subtests named like an unregistered ID
task spec:gate      # coverage gate across every module in the workspace (GATE overrides the milestone)
task api:check      # apidiff of every module against its last tag
task lint           # golangci-lint v2.14.0; depguard enforces the core budget rule
task test           # go test -timeout 2m -short over root, adapter/otel, examples
task examples:test  # the examples module only
task test:race      # -race, -timeout 10m
task tools:test     # the Python gate tools' own unittest suite
task examples:run   # run each shipped example main offline
task bench          # benchmarks once each
task bench:gate     # base/head performance regression gate
```

A scenario ID is the exact subtest name that covers it. `task spec:coverage` reports both directions: a registered scenario with no subtest, and a subtest named like an unregistered ID.

## Where to read

- [docs/overview.md](docs/overview.md) — purpose, principles, capability map, milestones, open questions
- [docs/design/architecture.md](docs/design/architecture.md) — layering, the core budget rule, repository layout, release rules
- [docs/design/types.md](docs/design/types.md) — generated index of every normative type, function and sentinel error
- [docs/design/api-review-m0-5.md](docs/design/api-review-m0-5.md) — the M0.5 review record and the v1-candidate declaration
- [docs/design/compatibility.md](docs/design/compatibility.md) — normative compatibility policy
- [docs/design/scenarios.md](docs/design/scenarios.md) — reference scenarios and the example catalog
- [openspec/specs/&lt;capability&gt;/spec.md](openspec/specs/) — the contracts, with scenario IDs that are also test names
- [docs/adr/](docs/adr/) — the architecture decision records
- [AGENTS.md](AGENTS.md) — how to work in this repository, for humans and agents alike
- [CHANGELOG.md](CHANGELOG.md) — released changes, Keep a Changelog format

## Compatibility review: pending

The v1-candidate declaration is a shape decision, not a passed gate. Read it as pending review in three specific ways.

- **The gate is not fully exercisable.** `task api:check` implements the apidiff-per-module half only. The second clause — diffing `api/gohan.yaml` against a generated `adapter/httpapi` server — cannot run, because neither artifact exists yet. Both land in M4; the gap is recorded in [the M0.5 review](docs/design/api-review-m0-5.md), not silently dropped.
- **A module with no tag is skipped, not compared.** `api:check` prints that it skipped a module and exits 0. An untagged module therefore reports no incompatibility, which is weaker evidence than a clean diff. A passing run is not proof of compatibility.
- **The root module stays `v0.x`.** `v1.0.0` is not tagged; it waits on M2's exit criteria per the policy above. Breaking changes remain possible while the module is `v0.x`, each listed under *Breaking* in [CHANGELOG.md](CHANGELOG.md).

## A note on exactly-once

Tool effects are deduplicated through the journal and the store contracts: a `CallKey` fingerprint, a journal reservation, and a store whose concurrency guarantees are part of the port contract, not of this README. The guarantees a deployment actually gets depend on the store implementation it supplies and on how the two compose — the ports state the obligation, the implementation discharges it. `testkit/storetest` is the suite that checks a store against that obligation.

## License

Apache-2.0. See [LICENSE](LICENSE).
