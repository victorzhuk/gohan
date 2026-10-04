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
	gohan.WithModels(openai.New(cfg.Gateway, gohan.ModelProfile{
		Name:         "gw-default",
		Version:      "gpt-5.2-2025-12-11",
		Caps:         gohan.Caps{Tools: true, Streaming: true, Cache: gohan.CacheAuto},
		MaxInFlight:  64,
		LatencyClass: gohan.Interactive,
	})),
	gohan.WithStores(memory.New()),
	std.Interactive(),  // an Option: chains, prompts, guards, output mode, limits
)
if err != nil {
	return err
}
flow, err := agent.New[askIn, askOut](stack, gohan.AgentSpec{
	Name:        "excursion-assistant",
	Instruction: prompts.Assistant,
	Tools:       []gohan.Tool{tools.Search(repo), tools.Availability(repo)},
	Limits:      gohan.RunLimits{MaxTurns: 6},
}, native.Runtime())
```

`std.Interactive()` is one exported function: it registers `std.ToolChain`, `std.ModelChain`, `std.DefaultPrompts`, the rule-based guards, `Windowed` output and default `RunLimits`. Its body is the documentation; `stack.Explain(flow)` prints the result. A team that wants less copies the function and deletes lines.

```go
func (a *Assistant) Answer(ctx context.Context, q assist.Question) (assist.Answer, error) {
	out, err := a.flow.Invoke(ctx, toAskIn(q))
	var se *gohan.SuspendError
	if errors.As(err, &se) {
		return assist.Answer{}, assist.ErrPending{Ref: string(se.Token)}
	}
	if err != nil {
		return assist.Answer{}, fmt.Errorf("invoke assistant: %w", err)
	}
	return toAnswer(out), nil
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

### 9.0 Quickstart (canonical smoke test; every identifier is checked against `docs/design/types.md`)

```go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/victorzhuk/gohan"
	"github.com/victorzhuk/gohan/adapter/openai"
	"github.com/victorzhuk/gohan/core/memory"
	"github.com/victorzhuk/gohan/std/flow"
)

type Ticket struct {
	Category string `json:"category" desc:"one of billing, refund, other" enum:"billing,refund,other"`
	Urgent   bool   `json:"urgent" desc:"customer threatens to churn or mentions a deadline"`
}

func main() {
	stack, err := gohan.Build(
		gohan.WithModels(openai.New(os.Getenv("OPENAI_BASE_URL"), gohan.ModelProfile{
			Name: "default", Version: "gpt-5.2-2025-12-11",
			Caps: gohan.Caps{Tools: true, Streaming: true, Constrained: true, Cache: gohan.CacheAuto},
			LatencyClass: gohan.Interactive,
		})),
		gohan.WithStores(memory.New()),
	)
	if err != nil {
		panic(err)
	}
	classify := flow.Extract[Ticket](stack, "default")
	ctx := gohan.WithPrincipal(context.Background(), gohan.Principal{Tenant: "demo", Subject: "dev"})
	out, err := classify.Invoke(ctx, []gohan.Block{gohan.Text{Text: os.Args[1]}})
	if err != nil {
		panic(err)
	}
	fmt.Printf("%+v\n", out)
}
```

No preset, no chain, no prompt text: the empty-chain path (`chains.empty-chains`) with `Extract`'s schema-only request. Adding `std.Interactive()` to `Build` is the first governance step.

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
