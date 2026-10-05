## 4. Architecture

### 4.1 Service layering

```
transport (ogen HTTP/SSE, gRPC, Kafka consumer)
   │  sets Principal, calls use case
   ▼
use case ── depends on ──▶ domain port        e.g. type Assistant interface{ Answer(ctx, Question) (Answer, error) }
                                ▲
                                │ implements (maps DTOs, maps *SuspendError → domain error)
infrastructure adapter ─────────┘
   │ holds
   ▼
gohan.Flow[In, Out] / gohan.Conversation     ◀── the only gohan type infrastructure calls
   │ implemented by
   ├─ agent flow on runtime native | eino | adkgo
   ├─ eino graph flow (compose.Runnable)
   ├─ adk-go workflow flow (sequential/parallel/loop agents)
   └─ FlowFunc (plain Go / mock)

tools ── are driving adapters: they call use cases / repositories, like HTTP handlers do
```

### 4.2 gohan internals

```
                 ┌──────────────── Flow / Conversation ────────────────┐
 Invoke/Send ──▶ │ input guard → run (backend) → structured output →   │──▶ Out / events
 Resume ───────▶ │ output guard (Buffered|Windowed) → fallback          │
                 └───────┬───────────────────────────────┬─────────────┘
                         │ backend calls governed components
            ┌────────────▼───────────┐       ┌───────────▼────────────┐
            │ Governed Model chain   │       │ Governed Tool chain    │
            │ (canonical order §6.8) │       │ (canonical order §6.8) │
            └────────────┬───────────┘       └───────────┬────────────┘
                         │                               │
          adapter/openai|anthropic|eino|adkgo      your tools, eino/adk tools, tool/exec
                         │
        ModelProfile · Strategies · Assembler · Limiter · Router (Decider)
                         │
     Stores: SessionLog · Checkpoints · Journal · Runs · AuditLog · EventLog    Telemetry: OTel spans + metrics
```

### 4.2a Core budget rule (ADR-0098)

`core` (package `gohan`, a tree — ADR-0139) contains ports, types, events, error classes and the driver (`Drive`, chain validation, the permission gate skeleton, stores contracts). Any default, matcher, heuristic, preset or policy value lives in `std`; anything protocol- or vendor-specific lives in `adapter`. A capability spec may name a core hook (e.g. `TaintHook`, `ContextPolicy`) but its implementations are `std`. Consequences applied: `SkillSource` is a `std/skills` interface; `TaintPolicy` defaults and the substring matcher are `std/taint` behind the gate's `TaintHook`; JSON-Patch diffing for `StateChanged` is `std/state`; sandbox effect derivation and secret scanning are `std/sandbox`. New capabilities are frozen until M0 ships; later grill rounds append to `docs/backlog.md`.

### 4.3 Adapter roles

| Module | Runtime | Flow backends | Model | Tool | Decider |
|---|---|---|---|---|---|
| core `runtime/native` | ✓ | agent | – | – | – |
| `adapter/openai` (incl. vLLM, OpenAI-compatible gateways) | – | – | ✓ | – | ✓ (LLM + schema) |
| `adapter/anthropic` | – | – | ✓ | – | ✓ |
| `adapter/eino` | ✓ | agent, graph | ✓ ↔ | ✓ ↔ | – |
| `adapter/adkgo` | ✓ | agent, workflow | ✓ ↔ | ✓ ↔ | – |
| `adapter/jev` | – | – | – | – | ✓ |
| `testkit/gohantest` | – | – | scripted, record/replay | fakes | fixed |

`↔` = import (foreign → gohan, for mixing) and export (governed gohan → foreign, for execution).


## 5. Repository layout and modules

Three parts: **core** (contracts and driver code), **std** (the recommended behavior as readable code), **adapter** (everything with a third-party dependency).

```
github.com/victorzhuk/gohan                 ← module A: core + std + testkit
  go.mod                                      deps: stdlib only; telemetry deps live in adapter/otel; tool schemas use the core reflection walker
  go.work                                     dev only
  core/          package gohan                driver: Build/Stack/options, Flow/FlowFunc/Conversation,
                                              Drive/DriveResume, Recover, Inspect, Explain, NewTool and type
                                              aliases for the vocabulary below. Zero default middleware,
                                              zero prompt text.
    types/       package types                value types and their enums, and the ports
    stores/ chains/ guards/ permission/ suspension/ streams/   ports, enums and their packages
    runtime/     package runtime              Stepper, Runtime, State, Status, native runtime
    flowdef/     package flowdef              definition model (M4); Lang/ExprLang ports
  std/           package std                  Interactive(), Agentic(), Batch() presets; ToolChain(), ModelChain();
                                              DefaultPrompts; explain HTTP handler
    permission/  guard/  structured/  route/  limit/ (local)  retry/  notes/  outputs/  tool/exec/  tool/http/  egress/  keys/  notify/
    toolsearch/  telemetry/ (GenAI, Langfuse conventions)
    context/ (projections, Summarize)  sandbox/ (tool set, policy)  skills/ (SkillSource, meta-tools)
    taint/ (policy, matcher)  state/ (shared state, JSON Patch)  cache/  tokens/ (Heuristic estimator)
    flow/        recipes: Extract, Classify, Route, MapReduce, Pipeline, RAG, Judge/Refine; Compile(flowdef)
    flowdef/     JSON/YAML front-end (stdlib only)
  testkit/
    gohantest/   scripted model, record/replay, fakes, fault injection, leak checks
    conformance/ runtime, chain, flow, model suites
    storetest/   SessionLog, Checkpoints, Journal, Runs, EventLog, AuditLog suites
  adapter/                                    ← each directory is its OWN module with its own version
    eino/        runtime, graph flows, Model/Tool bridges         github.com/cloudwego/eino
    adkgo/       runtime, workflow flows, Model/Tool bridges      google.golang.org/adk
    openai/      Model + Decider (OpenAI, vLLM, OpenAI-compatible gateways)
    anthropic/   Model + Decider
    jev/         Decider
    cel/         ExprLang (default)                                    cel-go
    lispico/     definition front-end, ExprLang where declared          go-lispico
    postgres/    SessionLog, Checkpoints, Journal, Runs, AuditLog, OutputStore; CompleteTx   pgx, sqlc, goose
    redis/       Limiter with quota pools + admission control, EventLog                     go-redis
    mcp/         client + server (2026-07-28)                                              official go-sdk
    agui/        AG-UI server transport (SSE, WebSocket)
    httpapi/     REST + SSE transport generated from api/gohan.yaml                         ogen
    docker/      Sandbox (dev)                                                             docker client
    e2b/         Sandbox
    langfuse/    PromptSource, SkillSource, telemetry convention                           OTLP
    openfeature/ Flags                                                                    openfeature go-sdk
  examples/                                   ONE module depending on all adapters; each example is a runnable
                                              service with fixtures, cassettes, acceptance tests, depguard config
    quickstart/  excursions/  support-triage/  invoice-extraction/  research-report/
    ops-agent/   pr-review/   data-analyst/
  docs/
```

Release rules for the multi-module repository:

- Tags: `vX.Y.Z` for the root module; `adapter/<name>/vX.Y.Z` for each adapter; `examples/vX.Y.Z` never tagged (examples pin by pseudo-version in CI).
- A released adapter `go.mod` requires a published root version; `replace` directives are rejected by the release task. `go.work` at the repo root is for development and is excluded from release verification.
- CI: per-module jobs with path filters; a matrix job runs `testkit/conformance` and `testkit/storetest` for every adapter against the root version it requires; adapters needing provider secrets run live tests in a scheduled workflow, never on PRs.
- Dependency updates are grouped per module. A `task release:adapter -- <name>` verifies `go mod tidy` cleanliness, runs the module's conformance, and tags.
- Promotion to a separate repository (ADR-0069) is a `git subtree split` plus an import-path change; nothing in core references adapter paths.

Rules:

1. "Adapter" means "imports a third-party dependency". Core and std never do. Providers and stores are adapters like frameworks are.
2. Core and std are one module and version together: a std preset can never compile against the wrong core, and the ordering constraints std relies on ship in the same release. After M2 the core API changes only additively.
3. Adapters are separate modules: bumping, pinning or archiving one never touches core or other adapters. No gohan feature may depend on a capability only one adapter has; S1–S3 must pass on core + std + provider adapters alone.
4. Directory `core/`, package clause `package gohan`: explicit folder, idiomatic call sites (`gohan.Flow`, `gohan.Build`).
5. Services import adapters only from their infrastructure/wiring packages. `examples/` ships a `depguard` rule that enforces this; teams copy it.
6. `Explain` is a library call. There is no external CLI: a service exposes it as its own `explain` subcommand and/or mounts `std.ExplainHandler` on a debug port.

Tooling: Go 1.27 (see §6.0), `go.work`, Taskfile, golangci-lint (incl. depguard) and govulncheck via `tool` directives, testcontainers, `-race` everywhere.
