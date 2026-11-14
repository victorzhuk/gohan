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

`Runs` keeps its frozen method set. The optional `CheckpointRuns` surface adds `SuspensionToken` and token-fenced `ResumingCheckpoint`; both `MemoryRuns` and PostgreSQL `Runs` implement it.

### 4.2a Core budget rule (ADR-0098)

`core` (package `gohan`, a tree — ADR-0139) contains ports, types, events, error classes and the driver (`Drive`, chain validation, the permission gate skeleton, stores contracts). Any default, matcher, heuristic, preset or policy value lives in `std`; anything protocol- or vendor-specific lives in `adapter`. A capability spec may name a core hook (e.g. `TaintHook`, `ContextPolicy`) but its implementations are `std`. Consequences applied: `SkillSource` is a `std/skills` interface; `TaintPolicy` defaults and the substring matcher are `std/taint` behind the gate's `TaintHook`; JSON-Patch diffing for `StateChanged` is `std/state`; sandbox effect derivation and secret scanning are `std/sandbox`. One exception is recorded rather than implied: the executable limit **policy** moved out of the floor into `std/limit` (`Limits`, `ToolLimits`, plus the per-endpoint `MaxInFlight` bulkhead), sequenced after per-run accounting (ADR-0148). The concrete **default values** did not move with it: the run-limit presets (`InteractiveLimits`, `AgenticLimits`, `BatchLimits`) and the zero-fill defaults `Build` applies to a preset's zero fields are still declared in the floor package (`core/types`, `core/limits.go`), and `core/chains` keeps the ledger and its counters. New capabilities are frozen until M0 ships; later grill rounds append to `docs/backlog.md`.

### 4.2b Governed native construction

A service registers its flow as a **native definition** and takes its handle from the constructor. Nothing passes a runtime or a stepper: the definition is the only thing a service writes, and the handle is what the conversation is obtained from.

```go
stack, err := gohan.Build(
	gohan.WithModels(model),
	gohan.WithStores(stores.Stores{SessionLog: sessions}),
	gohan.WithNativeAgent(gohan.NativeSpec{
		Request:  gohan.FlowRequest{Name: "quickstart"},
		Profile:  "scripted",
		Assemble: assemble,
	}),
)
if err != nil {
	return err
}
conv, err := gohan.NewNativeConversation(stack, "quickstart",
	gohan.WithConversationRuns(stores.NewMemoryRuns()),
	gohan.WithConversationEventLog(stores.NewMemoryEventLog()),
)
```

`WithNativeAgent(gohan.NativeSpec)` is a `Build` option, so the definition is registered before `Build` runs. `NativeSpec` carries what the flow requests at build time and what it runs: the validated `FlowRequest` (whose name is the flow name), the model `Profile` name, the `Instruction` blocks, the `Tools`, the `Assemble` function, the `chains.ModelChain` and `chains.ToolChain` to run, the flow's `RunLimits`, and the flow's own `Decider` — the tool policy the batch gate consults before any call executes. The tool list has one source: `Request.Tools` is derived from `Tools` during resolution, never supplied independently. A nil `Decider` keeps the default gate — read-only and idempotent calls pass, side effects ask, a decider error asks rather than widening permission.

`Build` resolves each registered definition once — model by profile name, strategies, fidelity, then chain ordering — and a rejected definition fails the build **before any provider call**. The constructor reads that resolution instead of recomputing it: an unknown name is refused before any invocation, and `stack.Explain(conv)` projects the same resolved configuration execution uses (profile, strategy plan, named chain steps, tools, prompt set, limits, release) with zero provider calls and no run budget spent.

#### Per-run accounting

The ledger is **per run** and reached from the run's invocation context, not from a chain or a preset. Each `Send` mints a `chains.LimitsState` and installs it with `chains.WithLimitsState`; each `Resume` mints one seeded from the spend the persisted run record already reports. Concurrent sessions share neither runtime state nor spend, and a branch under one tree shares one tree total rather than a second budget.

The executable policy lives in `std/limit` and reads that ledger from the context: `limit.Limits` is the model-chain step (charges usage through the profile's pricing, warns once at the soft ratio, aborts on a hard cost overrun or an expired wall clock), `limit.ToolLimits` is the tool-chain step (consumes a reserved batch slot, bounds each call by the wall clock the run has left so an overrun cancels the tool's context), and `limit.Endpoint` is the per-endpoint `MaxInFlight` bulkhead. `std` presets cross into a build through `Preset.Options()`. Without a ledger in the context the steps forward unchanged.

The two ceilings behave differently on purpose. Reaching `MaxTurns` or `MaxToolCalls` **stops** the run: those are budgeting counters, the step only records itself, and the run ends with `types.Done{Reason: types.StopLimit}` at its effect boundary. A cost overrun or an expired wall clock **aborts** the run: those are the hard checks, and they fire before or right after the call that crossed them. A ledger reservation exceeding `MaxToolCalls` refuses the whole batch without charging it, and the native driver returns an error matching `types.ErrBatchOverrun`. If `runtime.Batch.Run` later refuses with an error matching `types.ErrBatchOverrun`, the driver refunds the reservation and returns `*LimitExceededError{Limit: "MaxToolCalls"}`.

The native driver persists the cumulative admitted tool-call total in its continuation. Resume and recovery restore that total once into the fresh ledger; already admitted pending calls use prepaid slots, while later batches consume the remaining budget.

### 4.2c Conversation lifecycle

Two properties a conversation handle owes its caller: how a run leaves, and how a client finds the run that is still going.

**Handoff is one transition, performed once.** A model-requested human handoff (`request_human`) performs a single control transition — there are no competing handoff branches that each close the run. With a session index and a control writer present, the transition persists the `HandoffContext`, sets `Control` to `ControlHandoffRequested` and audits `handoff_requested` — once. An optional store being absent skips only its own step and never the close: the run still ends with `Done{Reason: StopHandedOff}`, and the optional handoff callback fires exactly once. The persisted `HandoffContext` and the state the run returns both match the error's `HandoffContext`, so a reattached client reads the same handover it was given. Terminal reasons are preserved on the way out: a run that ends on a limit, a cancel or a handoff reports that reason in its final `Done` and in the durable run record (`FinishReasonRecorder`) instead of substituting `StopCompleted` (`flow` handoff terminal transition, `runtime` terminal stop reason preservation).

**ActiveRun is owner-authorized discovery.** A client calls `ActiveRun` before `Cancel`; the handle answers with the id of the session's active run. Active means exactly one thing: state `Running` or `Resuming` with a live lease. A `Suspended`, terminal, lease-expired or null-lease run is inactive and the call returns `types.ErrRunNotActive` with no id. Discovery is authorized like `Cancel` — a principal of another tenant, or one without session write scope, gets `ErrSessionForbidden` and no run id is disclosed. A run another pod started is still found, through the local run state where the handle holds it and through the optional `SessionRunFinder` on `Runs` otherwise; the `Runs` port does not grow a method for it. `ActiveRun` changes no `Send` semantics and requires no idempotency key. A detached run stays discoverable while it is `Running` or `Resuming` with a live lease.

**Lease and checkpoint ownership.** Memory and PostgreSQL `Start`, `Resuming` and `Reclaim` refuse a competing live session lease. `Reclaim` accepts only a stored `Running` or `Resuming` run with an expired lease. `ResumingCheckpoint` also requires the run's current suspension token.

Checkpoint lifetime is store policy, independent of the run lease. The driver does not derive `ExpiresAt` from the lease. `WithMemoryCheckpointTTL` and PostgreSQL's `WithCheckpointTTL` apply a positive TTL from store time at `Put` when no explicit expiry exists; zero adds no expiry, and a negative TTL is refused before a checkpoint write.

PostgreSQL uses `clock_timestamp()` for lease, checkpoint, discovery, workspace-owner and notice-claim times. Lease and workspace-owner checks read store time after acquiring the relevant ownership locks; notice claimability is checked in the selection statement and claims are stamped after row selection. `postgres.WithClock` replaces the server clock for tests.

`ResumeReady` filters for consumed, unexpired checkpoints whose token is current on a suspended run before applying its limit. Memory discovery requires `WithMemoryCheckpointRuns`; without it, `ResumeReady` returns `types.ErrCheckpointIncompatible`. `Preempted` filters suspended runs by their stored preemption reason before applying its limit.

External `Resume` requires the run store's optional `stores.CheckpointRuns` surface. Without it, `Resume` returns `types.ErrCheckpointIncompatible` before consuming the token. Custom run stores must implement this surface to support resume; new `Send` calls and stale-run recovery do not require it. Automatic preempted recovery skips the unsupported token-fenced path. After successful consumed-checkpoint discovery, recovery skips candidates without token-fenced support. Discovery errors propagate: an unbound memory `ResumeReady` can return `types.ErrCheckpointIncompatible` before the per-candidate skip.

`Stack.Recover` handles preempted runs before stale runs. For a current `Preempted` checkpoint, it conditionally consumes the checkpoint with `ConsumeIf` before acquiring a token-fenced resume lease. It does not decide pending human approvals or other suspension reasons. A stale token cannot replace a newer suspension. Consumption is recorded separately from the resume input, so a consumed empty `Continue()` can be recovered once if the client fails before acquiring the lease.

With an event log configured, recovered execution records its redacted event tail before delivery. Completion writes use a bounded context independent of reaper cancellation. Another conversation sharing the log can `Attach` from its last sequence and reads through the next `Done`, `Suspended` or `TerminalError` boundary. A failed terminal store write surfaces an error instead of delivering `Done`.

**Sub-flow calls derive their budget at the boundary.** `Stack.FlowAsTool` runs a child flow under its own tree identity and ledger branch. Depth, wall clock and cost share come from the calling run's limits and from what the parent has left; the child's spend charges the shared tree budget; the derived budget travels as `types.RunBudget` and persists in the child's suspension checkpoint, so a resumed level reuses the recorded values. A child suspension checkpoints its token in the parent's `Checkpoint.Child`, and `Resume` consumes one token per level, routes the input into the child and re-drives the parent from the settled history. Fan-out waves (`std/flow.Fork`, `MapReduce`) collect results keyed to input positions, cap their own concurrency per wave and report failures as `*PartialError` positions.

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

`↔` = import (foreign → gohan, for mixing) and export (governed gohan → foreign, for execution). `adapter/adkgo` (M3) and `adapter/jev` (M4) are scheduled roles: no module exists for either yet (§5).


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
    types/       package types                value types and ports, including portable telemetry export records and lifecycle
    stores/ chains/ guards/ permission/ suspension/ streams/   ports, enums and their packages
    runtime/     package runtime              Stepper, Runtime, State, Status, native runtime
    flowdef/     package flowdef              definition model (M4); Lang/ExprLang ports
  std/           package std                  Interactive(), Agentic(), Batch() presets; ToolChain(), ModelChain();
                                              DefaultPrompts; explain HTTP handler
    permission/  guard/  structured/  route/  limit/ (local)  retry/  notes/  outputs/  tool/exec/  tool/http/  egress/  keys/  notify/
    toolsearch/  telemetry/ (GenAI, Langfuse conventions; bounded export delivery and admission policy)
    evals/       bounded online evaluation, judge calibration and provenance export
    context/ (projections, Summarize)  sandbox/ (tool set, policy)  skills/ (SkillSource, meta-tools)
    taint/ (policy, matcher)  state/ (shared state, JSON Patch)  cache/  tokens/ (Heuristic estimator)
    flow/        recipes: Extract, Classify, Route, MapReduce, Pipeline, RAG, Judge/Refine; Compile(flowdef)
    flowdef/     JSON/YAML front-end (stdlib only)
  testkit/
    gohantest/   scripted model, record/replay, fakes, fault injection, leak checks
    conformance/ runtime, chain, flow, model suites
    storetest/   SessionLog, Checkpoints, Journal, Runs, EventLog, AuditLog suites
    sandboxtest/ sandbox conformance suite (policy, egress, timeout)
  adapter/                                    ← each directory is its OWN module with its own version
    anthropic/   Model + Decider                                               ON DISK
    docker/      Sandbox (dev)                                                ON DISK     docker client
    eino/        runtime, graph flows, Model/Tool bridges         ON DISK     github.com/cloudwego/eino
    openai/      Model + Decider (OpenAI, vLLM, OpenAI-compatible gateways)   ON DISK
    otel/        telemetry: borrowed providers or owned export pipeline        ON DISK
    postgres/    SessionLog, Checkpoints, Journal, Runs, AuditLog, OutputStore; CompleteTx   ON DISK  pgx, sqlc, goose
    adkgo/       runtime, workflow flows, Model/Tool bridges      M3          google.golang.org/adk
    mcp/         client + server (2026-07-28)                      M3          official go-sdk
    redis/       Limiter with quota pools + admission control, EventLog       M3     go-redis
    jev/         Decider                                           M4
    langfuse/    PromptSource, SkillSource, telemetry convention   M4          OTLP
    openfeature/ Flags                                            M4          openfeature go-sdk
    agui/        AG-UI server transport (SSE, WebSocket)           M4
    httpapi/     REST + SSE transport generated from api/gohan.yaml  M4       ogen
    cel/         ExprLang (default)                                M4          cel-go
    lispico/     definition front-end, ExprLang where declared      M4         go-lispico
    e2b/         Sandbox                                           blocked (no E2B credential)
  examples/                                   ONE module depending on all adapters; each example is a runnable
                                              service with fixtures, cassettes, acceptance tests, depguard config
    quickstart/  excursions/  kafka-refunds/  temporal-travel/  camunda-invoice/
  docs/
```

`ON DISK` marks a module that exists in this repository today; a bare milestone mark marks one the milestone table in `docs/overview.md` §12 schedules and the tree does not hold yet. `adapter/e2b` is blocked: no E2B credential, API URL or sandbox domain exists in the repository or the environment, so `m2-e2b-sandbox` is 0/28 with every live task blocked.

Telemetry export keeps records, including evaluation signals, and destination, sink and optional lifecycle ports in `core/types`. `std/telemetry` owns bounded delivery and admission policy; `adapter/otel` translates SDK signals. `otel.New` borrows caller-owned providers, while `otel.NewExportPipeline` owns its providers and exposes flush and shutdown (ADR-0174). The opt-in durable local spool in `std/telemetry` owns delivery without wrapping `Pipeline` (ADR-0175). On Linux local filesystems it uses exclusive flock ownership, syncs before durable acknowledgement, delivers at least once with per-destination durable cursors, and supports crash-safe subject erasure. `adapter/otel` reaches it through `SpoolSink`, which promises bounded admission only; critical callers use explicit `Append`.

**Docker sandbox boundary.** For networked workloads, the adapter creates a labelled stopped namespace anchor from the configured trusted base image, never from a restored snapshot. The anchor has a separate root filesystem and receives `NET_ADMIN` for policy setup, address resolution and counter reads. It receives no workload files, environment secrets or snapshot contents. The adapter creates the stopped workload in the anchor's network namespace, starts the anchor and establishes the complete policy before starting any workload process.

The workload drops all capabilities, adds none and enables `no-new-privileges`. Its image entrypoint is overridden and inherited healthchecks are disabled, for fresh and restored workloads alike. Existing UID and file API semantics remain unchanged. Public `Workspace.Exec` cannot administer network policy or regain that authority. Private administration targets the anchor, never public `Workspace.Exec`, and each operation is bounded to ten seconds. Deny-all retains `NetworkMode: none` and requires no anchor.

The workload, anchor and private network carry the private labels `gohan.sandbox.role` (`workload`, `anchor` or `network`) and `gohan.sandbox.owner`, with one immutable owner name for the group. New workload `ResourceRef` values encode both identities as `gohan-workload:v1:<full-container-id>:<owner-name>`. Close and setup-failure cleanup remove the workload, anchor and private network. Cleanup errors remain visible and retryable. Helpers associated with an existing workload, running or stopped, are never reclaimed as orphans.

`Handles.Sweep` processes durable cleanup records under the existing owner and generation fence. A live lease prevents cleanup. The record retains the workload ID and immutable owner name in its original `ResourceRef`, so a fresh coordinator can retry `CloseByRef` after workload deletion without `Enumerate` or prior process state. A failed helper removal retains the record, and completion waits until the entire group is absent. Before a record exists, `Enumerate` and explicit `CloseByRef` recover unrecorded workloads and orphan helpers instead; `Sweep` does not discover them.

Snapshot publication returns the immutable image ID from `ContainerCommit`. `Enumerate` and `CloseByRef` support labelled immutable IDs and legacy labelled tags. A later publication or a failed metadata compare-and-set cannot change content behind an earlier committed reference. Legacy tags remain readable and removable without retagging or removing another snapshot.

**Egress policy boundary.** `std/egress` enforces the tool's `EgressPolicy` before any connection is opened. Under `DenyPrivate`, resolved destinations in the shared-address range `100.64.0.0/10` are refused before dialing, alongside the existing private, loopback and link-local refusals; an exact literal address listed in `Allow` (for example `100.64.9.9`) remains reachable, while other literals inside the range stay denied. When a proxy is configured, supported `http` and `https` proxy modes connect to the destination address the client already validated: the proxy receives that client-validated IP instead of re-resolving the hostname through its own DNS. The HTTP `Host` header, the TLS server name, proxy credentials, cross-host redirect validation, per-response deadlines and the exact-once release behavior of the response lifetime are preserved through the proxied path. Unsupported proxy modes fail before dispatch with `ErrEgressProxyUnsupported`. This checked-address proxy routing supersedes the archived proxy-side-DNS acceptance in `openspec/changes/archive/2026-10-06-m1-egress-policy/design.md`; the correction is approved in `docs/adr/0170-governed-call-authority-and-runtime-repair-baseline.md`, section 5.

Release rules for the multi-module repository:

- Tags: `vX.Y.Z` for the root module and `adapter/<name>/vX.Y.Z` for each adapter. Examples are a development workspace consumer, not a release module.
- Published root identity is `v0.2.0`; the six adapters require it. Adapter first-publication identity is `v0.1.0`, with `adapter/otel` next at `v0.1.1`. ADR-0173 records the retired local `v1.0.0` drafts.
- `task adapters:verify` checks isolated copies with `GOWORK=off`, verifies the published dependency contract, builds and runs full conformance with required passing test events. CI's `release-verify` job invokes it and needs Docker for PostgreSQL testcontainers and Docker sandbox conformance. The task creates no tag or publication; workspace replacements cannot satisfy its verdict.
- `task examples:test` checks examples in the development workspace, separately from release verification.
- Promotion to a separate repository (ADR-0069) is a `git subtree split` plus an import-path change; nothing in core references adapter paths.

Rules:

1. "Adapter" means "imports a third-party dependency". Core and std never do. Providers and stores are adapters like frameworks are.
2. Core and std are one module and version together: a std preset can never compile against the wrong core, and the ordering constraints std relies on ship in the same release. After M2 the core API changes only additively.
3. Adapters are separate modules: bumping, pinning or archiving one never touches core or other adapters. No gohan feature may depend on a capability only one adapter has; S1–S3 must pass on core + std + provider adapters alone.
4. Directory `core/`, package clause `package gohan`: explicit folder, idiomatic call sites (`gohan.Flow`, `gohan.Build`).
5. Services import adapters only from their infrastructure/wiring packages. `examples/` ships a `depguard` rule that enforces this; teams copy it.
6. `Explain` is a library call. There is no external CLI: a service exposes it as its own `explain` subcommand and/or mounts `std.ExplainHandler` on a debug port.

Tooling: Go 1.27 (see §6.0), `go.work`, Taskfile, golangci-lint (incl. depguard) and govulncheck via `task security:scan`, which scans every workspace module reported by `go list -m`, testcontainers, `-race` everywhere.
