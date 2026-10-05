## 9. Reference scenarios

Domain: excursion assistant for a travel marketplace. The same service evolves S1 → S4. Acceptance criterion for the whole spec: **S1 → S4 changes only files under `internal/infra/wiring` and adds new infrastructure adapters; `internal/domain`, `internal/usecase`, tool implementations and transport handlers are unchanged.**

### Layout

```
internal/domain/excursion/        entities, value objects (no tags, no deps)
internal/usecase/assist/          use case + port: type Assistant interface{ Answer(ctx, Question) (Answer, error) }
internal/usecase/booking/         use case + port for creating bookings
internal/infra/tools/             search, availability, price, voucher, booking (call use cases/repos)
internal/infra/assistant/         implements assist.Assistant over gohan.Flow[askIn, askOut]
internal/infra/wiring/            gohan.Build, profiles, flows, strategies
internal/transport/http/          ogen handlers; sets Principal
```

### S1 — Day-1 MVP

- `POST /assist`; tools `search_excursions`, `get_availability` backed by fixture repos.
- One model via the company gateway (`adapter/openai`) or adk-go Gemini.
- Native runtime, memory stores, scripted model in tests.

```go
stack, err := gohan.Build(
	gohan.WithModels(openai.New(cfg.Gateway, types.ModelProfile{
		Name:         "gw-default",
		Version:      "gpt-5.2-2025-12-11",
		Caps:         types.Caps{Tools: true, Streaming: true, Cache: types.CacheAuto},
		MaxInFlight:  64,
		LatencyClass: types.Interactive,
	})),
	gohan.WithStores(memory.New()),
	std.Interactive().Options()...,  // chains, prompts, guards, output mode, limits
)
if err != nil {
	return err
}
```

The S1 assistant is a native definition registered with the build option that takes a `NativeSpec`, and its handle is what the conversation is obtained from — the wiring is spelled out in §4.2b of `docs/design/architecture.md`. The definition carries the flow's name in its request, the model profile, the instruction blocks, the tools, the assembler, the two chains, the run limits and the flow's own tool decider.

`std.Interactive()` is one exported function: it registers `std.ToolChain`, `std.ModelChain`, `std.DefaultPrompts`, the rule-based guards, `Windowed` output and default `RunLimits`, and `Options()` presents it as `Build` options. Its body is the documentation; `stack.Explain(conv)` prints the result. A team that wants less copies the function and deletes lines.

The S1 assistant is a `Conversation`, so the use case streams events and maps a suspension:

```go
func (a *Assistant) Answer(ctx context.Context, q assist.Question, yield func(gohan.Event) bool) (assist.Answer, error) {
	for ev, err := range a.conv.Send(ctx, q.SessionID, toAskIn(q)) {
		if err != nil {
			return assist.Answer{}, err
		}
		if !yield(ev) {
			break
		}
	}
	return assist.Answer{}, nil
}
```

### S2 — Enterprise integrations

- `get_price` → closed gRPC pricing engine, forwarding principal via `CredentialSource`.
- `render_voucher` → legacy CLI via `tool/exec`.
- `create_booking` → `SideEffect`, `RequiredScopes: {"booking:write"}`, gate `Ask` → `HumanApproval`; operator approves in back-office; approval arrives via Kafka on another pod → `Resume(ctx, token, Approve())` with the operator principal in `ctx`.
- Postgres stores; `create_booking` uses `CompleteTx` with the booking insert.

Wiring diff: new tools, `adapter/postgres`, gate options, Kafka consumer calling the resume use case.

### S3 — Highload production

- 1–3k RPS SSE over N pods via `Conversation`.
- Profiles: gateway primary + secondary; `Router: ByLatencyClass`, fallback; `adapter/redis` quota pool; budgets per tenant; `SessionHash` affinity to self-hosted vLLM.
- Guards: input (rules + Jev injection detector), tool-result, output `Windowed`.
- Load test proves: no duplicate bookings under pod kill; resume on any pod; token reuse rejected; TTFT/TPOT dashboards.

Wiring diff: profiles, strategies, guards, redis limiter.

### S4 — Advanced backends

- RAG + pre-processing as an eino graph built from governed nodes → `einoflow.FromRunnable[askIn, askOut]`.
- Itinerary building as sub-flows via `FlowAsTool` (the direction eino v0.9 itself converged on after removing its transfer-based workflow agents), or as adk-go workflow agents → `adkflow.FromAgent`.

Wiring diff:

```go
flow, err := einoflow.FromRunnable[askIn, askOut](stack, graph)
```

Domain, use cases, tools, transport: unchanged. Governance, telemetry and suspension behave identically (R3, R4, R12).

### Native path seams

The shipped `examples/quickstart` is the smallest complete service on the governed path. Its imports are the whole wiring surface — core as the driver, plus the store and type packages:

```go
import (
	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)
```

`examples/excursions` adds exactly one more import, `github.com/victorzhuk/gohan/core/permission`, for the flow's own decider. Neither example writes a runtime, a stepper or a batch: the conversation loads the history, gates the batch, drives the run and persists every append.

| Seam | What it does | What the tests assert |
|---|---|---|
| Construction | A native definition registers with `Build`; the constructor returns a `Conversation` from the resolved configuration | A valid registration resolves; a constrained request without the profile's cap, an unknown profile and an unknown fallback are each rejected; a rejected definition makes zero provider calls; an unknown flow name and a nil stack are refused by the constructor; two conversations over one stack stay isolated; build middleware runs inside `Send` |
| Effect seam | `Stack.nativeTurnConfig` binds the model chain, the assembler with the resolved prompt set as the single system insertion point, the tool registry, the governed call path, the limits and the scheduler strategy; `nativeRun` pairs the model and batch effects over one per-run scope | The turn config binds the resolved values; the turn sequence alternates effects; a tool call keeps its original identity through the chain; a control error stays a control error; a batch reservation keeps call order and refunds on refusal; a side effect appends before it executes; batch results keep call order; the step-0-outermost model chain composes the stack middleware; an unknown profile is refused before a call |
| Lifecycle seam | `Send`, `Continue`, `Resume` and `Recover` each build a fresh run through the same governed factory; a steer during a batch reaches the next turn; the ledger is per run | A steer during a batch is applied and acknowledged; a resume keeps the spend the record reports; a run with no ledger still completes; a resume can suspend again; an empty record starts an empty ledger |
| Gate | The batch gate consults the flow's `Decider` for every call before the first one executes | A denied side effect never executes and renders as an ordered `not_executed` result; a decider error asks instead of allowing; a nil decider keeps the read-only/idempotent-pass default; an unknown tool denies even with a decider |
| Accounting | `Send` mints a ledger into the run context; `Resume` mints one seeded from the record; `std/limit` reads it | One run's spend never touches another's; reaching `MaxTurns` ends the run with `Done{StopLimit}` at its effect boundary |
| Explain | `stack.Explain(handle)` projects the resolved configuration | The projection carries the profile, both chains' steps, the strategy plan, the limits, the prompts and the release; the sample request matches execution preparation; a handle resolves the same configuration as its flow name; an unknown flow yields an empty explanation; the release follows the resolved prompt set |

`examples/excursions` covers the same ground end to end: the offline plan streams an assistant message and one `Done`, the fixture decider denies `book_cabin` so the booking tool never runs, and the persisted history carries the denial as an ordered `not_executed` result.

### 9.0 Quickstart (canonical smoke test; every identifier is checked against `docs/design/types.md`)

```go
package main

import (
	"context"
	"fmt"
	"os"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/flow"
)

type Ticket struct {
	Category string `json:"category" desc:"one of billing, refund, other" enum:"billing,refund,other"`
	Urgent   bool   `json:"urgent" desc:"customer threatens to churn or mentions a deadline"`
}

func main() {
	// Illustrative only — no provider adapter exists yet. The tree ships
	// adapter/otel (telemetry) alone, and the only types.Model implementations
	// are the scripted one in testkit/gohantest and the per-example fakes. A
	// provider adapter will return a types.Model built from a types.ModelProfile:
	//
	//	model, err := adapteropenai.New(os.Getenv("OPENAI_BASE_URL"), types.ModelProfile{
	//		Name: "default", Version: "gpt-5.2-2025-12-11",
	//		Caps:         types.Caps{Tools: true, Streaming: true, Constrained: true, Cache: types.CacheAuto},
	//		LatencyClass: types.Interactive,
	//	})
	var model types.Model

	stack, err := gohan.Build(
		gohan.WithModels(model),
		gohan.WithStores(stores.Stores{
			SessionLog:  stores.NewMemorySessionLog(),
			Runs:        stores.NewMemoryRuns(),
			Checkpoints: stores.NewMemoryCheckpoints(),
			Journal:     stores.NewMemoryJournal(),
		}),
	)
	if err != nil {
		panic(err)
	}
	classify := flow.Extract[Ticket](stack, "default")
	ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "demo", Subject: "dev"})
	out, err := classify.Invoke(ctx, []types.Block{types.Text{Text: os.Args[1]}})
	if err != nil {
		panic(err)
	}
	fmt.Printf("%+v\n", out)
}
```

`ModelProfile`, `Caps`, `CacheAuto` and `Interactive` live in `core/types`, not in the driver package; `flow.Extract` takes the `*gohan.Stack` and a profile name, not a `types.Model`.

No preset, no chain, no prompt text: the empty-chain path with `Extract`'s schema-only request. Spreading `std.Interactive().Options()` into `Build` is the first governance step.

### 9.1 Example catalog

Selection is driven by what is actually in production: customer service (26.5 %), research and data analysis (24.4 %) and internal workflow automation (18 %) lead the LangChain survey; data extraction (47 %), document analysis (41 %), support triage (41 %) and report generation (36 %) lead Zapier's; engineering is the third-largest adopting department; 38 % of enterprises require human review before autonomous actions. Each example is a runnable service under `examples/`, with fixtures and mock enterprise APIs, replay cassettes, an acceptance test per listed scenario, and an `explain` endpoint. Together they exercise every requirement group at least once.

| Example | Real-world shape | Recipes / flows | What it proves |
|---|---|---|---|
| `quickstart` (new capabilities exercised: none — the empty-chain path) | "Classify this ticket" in 30 lines: `FlowFunc` + `Extract`, memory stores, no std preset | `Extract` | The empty-chain path (R5i): core alone, no hidden behavior, zero persistence writes |
| `excursions` (exercises `agui`, `subflows` nested approval, `Verify` on `create_booking`, `interop` MRTR for a partner MCP tool) | The S1–S4 travel assistant: SSE chat, search/availability tools, gRPC pricing, CLI voucher, `create_booking` behind approval, payment webhook, eino RAG graph | `agent`, `Conversation`, `RAG`, `FlowAsTool` | Backend swap without rewrites (R1), approval grants (R5p), suspension reasons incl. `AwaitingExternal` and `Scheduled`, crash recovery under pod kill (R5b), reconnect via `Seq` (R5h) |
| `support-triage` (exercises `skills` refund-dispute procedure, `taint` on customer data → email, `context` compaction on long threads) | Ticket intake from a Kafka topic: classify (Jev or LLM decider) → route → extract entities → draft reply → judge/refine; refunds are `SideEffect` with `Ask`; at-least-once delivery | `Classify`, `Route`, `Extract`, `Judge`/`Refine`, `Pipeline` | Idempotent consumption via journal fingerprints (R5, R5a), confidence-escalation, batch admission class vs interactive (R5n), approval expiry and pending cap |
| `invoice-extraction` | PDFs arrive as `File` blocks; `Extract[Invoice]` with constrained or validate/repair output; read-back against a mock ERP; booking the invoice uses `CompleteTx`; nightly batch via provider batch API | `Extract`, `Pipeline` | Fidelity matrix and `AllowDrop` (R5o), structured output modes (R9), same-transaction journaling (R5), `AwaitingBatch` suspension (R4) |
| `research-report` (exercises `context` projections + persisted compaction, `subflows` fan-out with `Collect`) | "Write me a market brief": deferred `search_tools` over 40 sources, `MapReduce` over documents, notes for progress, report generation, run detached with client attach | `MapReduce`, `RAG`, `Judge` | Deferred tools (R5q), context guards on retrieved content (R5e), tree budgets (R5g), `Detached` + `Attach` (R5h), notes surviving reset (R5d) |
| `ops-agent` (exercises `taint` trifecta check with `Untrusted` MCP tools, `interop` client, `flags` kill switch) | Internal chat-ops bot: read-only `kubectl`-style CLI via `tool/exec`, untrusted MCP tools deferred and effect-capped, restarts require approval, per-principal scopes, audit reconstruction for an incident | `agent`, `Route` | `tool/exec` safety (R14), tool policy and pinned manifest (R5f), scopes and identity (R8), `Reconstruct` (R5m), `MaxPendingApprovals` |
| `pr-review` (exercises `release`: shadow challenger and eval gate in CI) | Review a diff: `MapReduce` over hunks → `Judge` with a rubric → summary; everything `ReadOnly`; runs in CI against cassettes | `MapReduce`, `Judge` | Read-only tools pay nothing (R5i), `Strict` replay with pinned version (R5r), tolerance-band evals, `pass@k`/`pass^k`, version drift detection (R5j) |
| `data-analyst` (exercises `sandbox` per-session workspace, `working-state` output paging) | NL → SQL over a warehouse, `std/sandbox` (`PerSession`, no egress, warehouse secret injected) for pandas over query results: read-only query tool, tool-result guard for PII, large results to the output store, cheap→strong `Cascade` routing, quota pool shared with a batch re-summarization job | `Route`, `agent` | Output store and `read_output` (R5d), `Cascade` router and class-based failover (R5j), shared quota pool and batch yielding (R5n), `ContextOverflow` re-fit |

| `kafka-refunds` | Refund requests on a topic; refund succeeds, worker crashes before the offset commit; redelivery | `Route`, `Extract` | `OperationID` dedup returns the same result, no second refund (R5v); journal + audit under at-least-once; live emergency flag stops refunds mid-queue |
| `temporal-travel` | Temporal workflow: book → pay → supplier confirm with uncertainty → cancellation; gohan runs are activities | `agent`, `Pipeline` | Composition boundary (R5v); `Unknown` outcomes surfaced to the workflow, compensation owned by Temporal; approvals as signals; definition version pinned across a deploy |
| `camunda-invoice` | BPMN invoice approval: approval delayed days, worker timeout duplicates a job, approver's scopes revoked before resume | `Extract`, `Judge` | `Suspended`/`Resuming` states and lease release (R5v); duplicate workers (`ErrOperationExists`/`ErrRunActive`); scope re-check on resume; flag outage → `AwaitingControl` |
| `catalog-enrichment` | Marketplace batch: 1 M product descriptions via provider batch API, quality judge, kill switch | `MapReduce`, `Judge`, `Extract` | `AwaitingBatch`, `pool/day` budget and batch admission, frozen rollout flag per tenant, live kill switch |

The three process examples (`kafka-refunds`, `temporal-travel`, `camunda-invoice`) are the **acceptance set for the core API review (M0.5)**: their offline contract scenarios must pass before core is frozen; their live-engine tests gate the corresponding adapter's support status.

Rules for the catalog: every example must run offline from cassettes and fixtures (`task examples:test`), and once live against real providers in the scheduled workflow; each example's README states which spec requirements it covers; a requirement with no example is a spec smell.
