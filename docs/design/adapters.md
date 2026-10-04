## 7. Adapter mapping

Verified against eino v0.9.x (`adk`, `compose`, `components/tool`) and adk-go v1.7.0 (`agent/llmagent`, `model`, `agent/workflowagents/sequentialagent`).

### 7.1 eino

| gohan | eino |
|---|---|
| agent runtime | `adk.ChatModelAgent` + `adk.NewRunner(ctx, RunnerConfig{CheckPointStore: …})`; `Runner.Run(ctx, []*schema.Message, …)` |
| native resume | `Runner.Resume(ctx, checkPointID, …)` / `ResumeWithParams` |
| suspend from tool | `adk.StatefulInterrupt(ctx, info, state)` (*verify* callable from tool context) |
| graph flow | `compose.Graph[I, O]` / `Workflow` / `Chain` compiled to `Runnable[I, O]`; `Invoke`, `Stream`; interrupts via `compose.StatefulInterrupt`, `Resume`, `ResumeWithData` |
| checkpoint store | `CheckPointStore` backed by `Checkpoint.Data` (*verify* interface methods) |
| events | `AgentEvent` = `TypedAgentEvent[*schema.Message]`; `AgentAction.Interrupted` → `Suspended`; `Exit` → `Done` |
| Message | `*schema.Message` ↔ `Message` |
| Tool export/import | `tool.InvokableTool{Info(ctx) (*schema.ToolInfo, error); InvokableRun(ctx, argsJSON string, ...Option) (string, error)}`; `StreamableTool` imported by draining |
| Model export/import | `model.ToolCallingChatModel` (*verify* name/methods) |

gohan does not use eino agent middleware (`ChatModelAgentMiddleware`) for governance; governance is already in the exported components. Framework callbacks/tracing are disabled by default to avoid duplicate spans.

### 7.2 adk-go (v2)

Target: `google.golang.org/adk/v2` (Go 1.25+). v1 is backport-only.

| gohan | adk-go v2 |
|---|---|
| agent runtime | LLM agent constructed per run with governed `Model` and `Tool`s exported through the adapter; callbacks not used for governance (*verify* v2 constructor and config names) |
| `agent.Context` | adapter derives it from gohan `ctx`; `RunInfo`/`Principal` remain in the Go `ctx` so governed components read them unchanged |
| workflow flow | v2 graph engine (conditional routing, parallel workers) → `adkflow.FromGraph[In, Out]` (*verify* API) |
| suspension | v2 native human-in-the-loop pause/resume → `Native` strategy; `Replay` fallback per ADR-0054 (*verify* pause payload and resume input shapes) |
| session / memory | adapter implements the v2 session service over `SessionLog`; memory services are `ContextProvider`s (*verify* interface) |
| Model export/import | `model.LLM{Name(); GenerateContent(ctx, *LLMRequest, stream bool) iter.Seq2[*LLMResponse, error]}` ↔ `Model` (*verify* unchanged in v2) |
| Message | `*genai.Content` parts ↔ ordered `Block`s; thought parts ↔ `Reasoning{Provider: "gemini"}` |
| Tool export/import | v2 tool interface (*verify*) |
| multi-user | one agent instance per run, never a shared root agent; principal scoping verified by a conformance scenario |

### 7.3 native

```
for turn := 1; turn <= MaxTurns; turn++ {
    req := Assemble(history)
    msg := collect(Model.Generate(req))
    append msg to history
    if no tool calls → Done(end_turn)
    for each call (sequential v0.2; ParallelTools option later): Tool.Call → append result
}
Done(max_turns)
```

Resume strategy: `Replay`.

### 7.4 Jev

`adapter/jev` implements `Decider[S, D]`: marshals `S` as state, requests output typed by `D`'s schema, maps returned probability to `Confidence`. Primary uses: router, permission gate, guards. Transport pending (Q2).

### 7.5 Providers

Both providers implement the provider adapter contract in `openspec/specs/model/` (error classes, fidelity declaration, cache modes, schema handling, usage, streaming, pass-through, `Raw`) and pass `conformance.Model`.

- `adapter/openai`: OpenAI and any OpenAI-compatible endpoint (vLLM, enterprise gateways). `CacheAuto` only (provider prefix caching); `ResponseSchema` → `response_format` with `strict` where the endpoint declares `Caps.Constrained`; `Extra` → request body extension minus governed keys; `AffinityKey` → configurable header; `Priority` → engine priority where supported (*verify* vLLM parameter).
- `adapter/anthropic`: `CacheExplicit` with `CacheBreakpoints: 4` (`CacheBreak` → `cache_control`), `CacheWriteTokens` from usage; `ResponseSchema` → forced tool schema; opaque `Reasoning` with signature round-trip; tool use; streaming.

### 7.6 Postgres adapter contract

- `journal`, `audit`, `events` and `outputs` are range-partitioned by time (daily by default); TTL and retention run as `DROP PARTITION`, never as bulk `DELETE`. `session_log` is partitioned by tenant hash and time.
- `runs` is a small hot table (`fillfactor 70`); heartbeats are coalesced into turn writes (ADR-0048); a standalone heartbeat runs only for `Detached` runs and shielded tools longer than `ttl/3`.
- Journal entries and checkpoints are insert-then-one-update; grants and audit are insert-only.
- `Recover` reaps stale runs with `FOR UPDATE SKIP LOCKED` in batches of at most 100 and commits per batch.
- Every pool gohan uses sets `statement_timeout`, `lock_timeout` and `idle_in_transaction_session_timeout`; values are `Build` options with defaults (5 s / 2 s / 10 s).
- **No transaction may be open across a model call or a tool call.** `CompleteTx` is called inside a tool, around the business write only. The adapter ships an analyzer (`adapter/postgres/lint`) run by golangci-lint in `examples/` that flags `pgx.Tx` values live across `Flow`, `Model` or `Tool` calls.
- Schema ownership, embedded goose migrations (`postgres.Migrate`, `gohan-postgres migrate`), the expand/contract skew rule, partition maintenance and `storetest.Migrations`: `stores` *Postgres schema* (ADR-0125).
- `storetest.Bloat`: 1 M reserve/complete cycles with autovacuum enabled must keep p99 `Reserve` latency under a configurable bound and dead tuples bounded.

### 7.7 Interop adapters

- `adapter/mcp` (targets MCP 2026-07-28, contract in `openspec/specs/interop/`): client registers server tools as `Untrusted` + `Deferred`, catalog `ttlMs` drives manifest refresh, `input_required` becomes `HumanApproval`/`AwaitingInput` and is replayed with `inputResponses` on resume, Tasks extension becomes `AwaitingTool`; server exposes one `Flow` as one MCP tool, suspensions become `input_required` with the token in `_meta`, long flows go through Tasks. Roots, sampling and logging RPCs are not used.
- `adapter/agui` (contract in `openspec/specs/agui/`): server transport for browsers and apps; `RunAgentInput` → `Send`/`Resume`, gohan events → AG-UI events, frontend tools as suspending `Untrusted` tools, interrupts ↔ `Suspended`, `Seq` as SSE id.
- `adapter/a2a` (later): server maps `Flow` + `Detached` + `EventLog` to A2A tasks (`submitted`/`working`/`input-required`/`completed`/`failed`); `HumanApproval` → `input-required`; event `Seq` → status updates.
- Exposure rule: single-shot recipes via MCP; agent flows via A2A; a flow is exposed on both only with an explicit option.

### 7.8 External engines (composition first)

The engine owns the business process; gohan owns bounded runs.

| Engine | gohan call | Owned by engine | Owned by gohan | Dedup |
|---|---|---|---|---|
| Kafka consumer | `Invoke` per message (or `Resume` for correlated callbacks) | delivery, offsets, retries, DLQ | the run, its journal, audit, limits | `OperationID` = message key or business ID (ADR-0077); journal fingerprints inside the run |
| Temporal | `Invoke`/`Resume` inside an activity; suspension returned as a typed activity result; approvals arrive as signals/updates that call `Resume` | workflow history, timers, process retries, compensation, continue-as-new | same | `OperationID` = workflow ID + step; activity retries hit `ErrOperationExists` and read the result |
| Camunda / Zeebe | `Invoke` from a job worker; `HumanApproval` → BPMN user task; `Resume` from the task completion | process state, timers, incidents | same | `OperationID` = process instance + element; duplicate workers after job timeout hit `ErrOperationExists` or `ErrRunActive` |

Under `WithHost(engine)`, `Waker` and `Recover` remain gohan's for the *run* only; process-level retries and timers are never re-implemented. Engine-driven loops (engine calling `Step` inside activities) are a later mode over ADR-0076.

### 7.9 Embedded languages: definitions, expressions, scripts

Three tiers, one contract each:

| Tier | What it is | v1 | Requirement on the language |
|---|---|---|---|
| Definitions | `core/flowdef` data model, compiled by `std/flow.Compile` into recipes | yes | a parser producing `flowdef.Definition` |
| Expressions | routing and transforms over run `Data`, evaluated by an `ExprLang` | yes | `Deterministic + StepLimit` (+ `TypeCheck` preferred) |
| Scripts | Turing-complete programs calling governed components via injected host functions, resumable across suspension | later | `Hermetic` + (`Deterministic` for replay over journaled host calls, or `Snapshot` / `Continuations` for native suspension) |

```go
type Capability uint

const (
	Deterministic Capability = 1 << iota
	StepLimit
	MemoryAccounting
	TypeCheck
	Hermetic
	Snapshot
	Continuations
)

type Lang interface {
	Name() string
	Capabilities() Capability
}

type ExprLang interface {
	Lang
	Compile(src string, env ExprEnv) (Program, error)
}

type Program interface {
	Eval(ctx context.Context, data Data, budget ExprBudget) (Value, error)
	Cost() CostEstimate
}

type DefinitionParser interface {
	Lang
	Parse(src []byte) (flowdef.Definition, error)
}
```

`flowdef.Definition` (borrowed from the Serverless Workflow vocabulary, trimmed to gohan's seams): `call` (flow | tool | model by registered name, typed `In`/`Out`, optional `approval: required` → forces `Ask`), `do`, `for` (with a mandatory `max`), `fork` (with a mandatory `parallelism`), `switch` (expression or `Classify`), `try` (step-level catch → fallback step; model/tool retries stay in the chains), `wait` (→ `Scheduled`), `listen` (→ `AwaitingExternal` with a correlation key), `set` (expression), `raise`. Each step has `input`/`output` expressions over a run-scoped `Data` object. Steps map onto `std/flow` recipes (`Pipeline`, `MapReduce`, `Route`, …) and the agent flow; nothing in a definition or expression can call a tool, a model or `suspend` directly.

`Build`: parse with the declared front-end → validate every `call` against the registry (`stack.RegisterFlow(name, flow)`, tools by name) including `In`/`Out` schemas → compile expressions with the configured `ExprLang` and reject any referencing host calls → hash the canonical JSON form into the manifest (ADR-0081) → `Explain` prints the compiled definition in canonical form. `Build` refuses an `ExprLang` lacking `Deterministic + StepLimit`, and a `ScriptLang` role entirely in v1.

Adapters: `adapter/cel` (default `ExprLang`; non-Turing-complete, linear evaluation, cost estimate and runtime cost limit, type-checked against the `Data` schema); `adapter/lispico` (definition front-end; `ExprLang` if go-lispico declares `Deterministic + StepLimit` for a restricted evaluation mode; the `Continuations` route for scripts is the natural Lisp option and is tracked in Q21); `std` ships the JSON/YAML front-end with no dependencies. `adapter/starlark`, `adapter/goja`, `adapter/wasm` are possible later under the same matrix and none is required.
