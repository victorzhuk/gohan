# Design: m1-hedged-model-calls

## Context

`std/hedge` does not exist. The `chains` contract states the whole hedge: the step kind and its ordering position, the TTFT threshold, the never-same-endpoint rule, the loser's charge, the budget bucket, the single journal step and the metrics (openspec/specs/chains/spec.md:60,87-88,224-254). `core/chains` already validates `Hedge` inside `Router` and outside `Fallback` (core/chains/model_chain.go:21-25). `std/route` ships `Router`, `Candidates()` and the per-endpoint `Breaker` (std/route/route.go, std/route/breaker.go); `std/telemetry` ships `MetricTTFT` and `ObserveStream` but keeps no readable rolling window (std/telemetry/metrics.go:14,232-241); `chains.LimitsState` owns the run's cost and exposes `Charge` (core/chains/limits.go). The run's cost tags reach `ModelRequest.Metadata.CostTags` (core/types/model_request.go:22; std/assembly.go:355; ADR-0159). The journal records tool calls only, keyed by `CallKey`, with `Entry{Tool, Args, Result}` (core/stores/journal.go:31-37); no model step is journaled today, and the contract's `Attempts` field has no carrier (ADR-0149, ADR-0157 §4). The M1 matrix adds one rule this design must honour: a hedge must not consume a half-open breaker probe for a request it discards (openspec/changes/m1/design.md §2.8).

## Goals / Non-Goals

**Goals:** Ship `std/hedge` on the public model chain, give the router a non-consuming eligibility view, charge the loser, bound hedging by the run's cost tags, journal the pair once, and refuse hedging beside provider-executed tools.

**Non-Goals:** This change adds no speculative decoding, no fan-out beyond the two-call hedge, and no hedging of tool calls. It does not hedge across providers beyond the router's ordered targets and does not change the fallback or retry policy. The rolling P90 is shipped rather than deferred: `std/telemetry` gains the per-endpoint window and `std.ModelChain` wires it, so the contract's default `After` (the endpoint's rolling P90, 800 ms floor) holds with no host configuration.

## Decisions

### D1 — The hedge step owns the TTFT timer, started before the primary's first body chunk

`std/hedge.New(p HedgePolicy, opts ...Option) chains.ModelMiddleware` returns a `KindHedge` step. The step starts a `time.Timer` for `After` when it begins pulling the primary attempt's `iter.Seq2`, and stops it on the first chunk that carries body content — a content delta or a tool-call delta. A metadata-only, usage-only or reasoning-only first yield does not stop the timer: the contract measures a body chunk, and `chains.hedge-fires-on-ttft` is written against an endpoint that returns headers at once but no body chunk within `After` (openspec/specs/chains/spec.md:226-227).

`After` is `HedgePolicy.After time.Duration`. When it is zero the step asks its `Latency` source for the endpoint's rolling P90 time-to-first-chunk and clamps the result to a floor of 800 ms; a fixed duration bypasses the source. The timer is measured per attempt from that attempt's own start, so a slow primary does not shorten a later attempt's window. The step cancels the timer on every path where it stops, including a primary error and a primary that finishes before `After`.

`std/telemetry` gains the window the default reads: a bounded per-endpoint ring of the TTFT samples `ObserveStream` already records (a fixed sample count, no third-party dependency), reporting the endpoint's rolling P90 time-to-first-chunk. `std.ModelChain` wires it into the step through `WithLatency`, so the contract's default `After` — the endpoint's rolling P90, never below the 800 ms floor — holds without host configuration; a flow that builds its own chain may pass a fixed duration or its own source.

### D2 — Route selection is a live supplier, and the never-same-endpoint rule is a filter

`HedgePolicy.Targets` is `func() []types.Model`, the router's ordered candidate supplier — declaration order, or the `WithLatencyClass` order when the router was built with one (std/route/route.go:29-52). The step reads it at admission time, not at construction, so a breaker that opens between attempts is reflected.

The step picks the first target whose `ModelProfile.Name` differs from the in-flight endpoint's name. A same-name target is skipped, never preferred: "never the same endpoint" is a filter on the candidate list, not a preference inside it. When the supplier returns no target other than the one already in flight — the single-target router of `chains.hedge-never-same-endpoint` — the step is inert, fires nothing, and `Explain` reports the step with an `Applies`-style note that it is inert for the flow's single target.

This is also where the matrix's probe rule is honoured: the step decides admission — never-same-endpoint, breaker eligibility (a closed breaker only), budget, provider-tool ineligibility, `Caps.Streaming` false and `LatencyClass == Batch` — before it calls anything that consumes a half-open probe. `std/route` gains one read-only method, `Router.Eligible() []types.Model`, which returns the endpoints whose breaker is closed, reading a new read-only predicate `Breaker.State() BreakerState` (`Closed`/`Open`/`HalfOpen`) and never calling `Breaker.Allow()`, so a half-open breaker is not free admission. Only the target the step commits to runs through `Candidates()`/`Allow()`, so a hedge that is declined consumes no probe (openspec/changes/m1/design.md §2.8). The choice — fired, refused with a reason, or budget-exhausted — is recorded as a span event and counted in `gohan.hedge.fired{endpoint}`, `gohan.hedge.refused{reason}` and `gohan.hedge.budget_exhausted`.

### D3 — The step owns cancellation; the loser's usage is charged through the run ledger

Each attempt runs under its own `context.WithCancel(ctx)` derived from the call's context. The step owns both cancel funcs. When one attempt delivers the winning body chunk, the step cancels the other attempt's context, stops forwarding its chunks, and lets it terminate; the winner's chunks stream on unchanged. Cancellation of the primary never cancels the call itself, and shutdown, a caller cancellation or a `ConsumerStall` cancels every attempt (openspec/specs/chains/spec.md:60).

The losing attempt may already have reported `Usage` before its cancellation is honoured. That usage is not dropped: the step is constructed with the run's ledger and pricing (`hedge.WithLedger(st *chains.LimitsState, p types.Pricing)`), and when the loser reports usage the step calls `st.Charge(loserUsage, p)` once. The charge adds to the run's cost and therefore counts against `RunLimits.MaxCost`, which is the hard limit ADR-0147 keeps for spent money, and the step tags the model span with `gohan.usage.hedge_loser`. The pair is charged exactly once per attempt: the winner charges through the chain's own `Limits` step, the loser through the hedge step, and never both.

### D4 — The budget cap is a token bucket whose rate the run's cost tags resolve

`HedgePolicy.Budget float64` defaults to 0.05 and is a token bucket of hedges per base call: the step keeps one bucket for the run, refills it by `Budget` tokens on each base model call, caps it at one token, and spends one token per hedge. At 0.05 roughly one call in twenty hedges; when the bucket is empty no hedge fires, the primary proceeds to its own timeout, and `gohan.hedge.budget_exhausted` increments once per declined hedge (openspec/specs/chains/spec.md:60,234-237).

The effective rate is resolved per run from the run's cost tags. `HedgePolicy.BudgetByTag func(types.CostTags) float64` is an optional field; when it is nil the base `Budget` applies. The step reads the tags from `types.RunInfoFrom(ctx).CostTags`, the same values the cost-tags lane forwards on `ModelRequest.Metadata.CostTags` (core/types/identity.go:41-45; docs/adr/0159-request-metadata-cost-tags-never-content.md). A run whose tags resolve to zero never hedges; this is how an operator keeps a feature or a development environment on the primary endpoint without a second policy type.

### D5 — The hedged pair journals as one model step

The pair is one model call, so it journals one record. `stores.Entry` gains `Attempts int` and `Endpoint string`; the driver's model-call path reserves one entry for the call under the call's `types.CallKey` and completes it once, when the winner's stream ends. `Attempts` counts the attempts that actually started — 1 when the timer did not fire, 2 when it did — and `Endpoint` names the winner's `ModelProfile.Name`. The loser is not journaled separately. This is what `chains.hedge-single-journal-step` asserts: "the journal holds one model step with `Attempts: 2` and the winner's endpoint, and replay reproduces the winner's response" (openspec/specs/chains/spec.md:249-251).

The two new fields are the only change to the stored shape: the `Entry` block of the stores contract (`openspec/specs/stores/spec.md`, Contract 6.x) is edited directly in the same implementation commit (the egress precedent for the `AuditKind` block), and the package ships a `stores` delta that declares the extension on the `Store contracts` requirement. One record, not two, because replay and reconciliation read the journal for calls that could not be settled. Two entries would leave the loser `Reserved` or `Unknown` forever — it produced no model-visible result, so no verifier can settle it — and every resumed run would raise a spurious uncertainty for it (ADR-0149; ADR-0157 §4). The loser's usage and cost still reach the run (D3); only its identity as a separate journal row is refused. Recording the winner's endpoint on the one entry keeps the replay deterministic: a re-drive reproduces the winner's response and never re-issues the pair.

### D6 — A provider-executed tool in flight refuses the hedge, permanently

A call is ineligible for hedging when its assembled `ModelRequest.Tools` declares any spec whose `Executor` is `types.ByProvider`: the provider runs that tool inside the model call, so a second attempt would run it a second time. The step reads the request's declared tools at admission time and, when one is provider-executed, declines to hedge however late the primary's first chunk is (core/provider_result.go:60-81; openspec/specs/chains/spec.md:239-242).

The ineligibility is permanent for that call, not transient: the step records `types.ClassPermanent` on the refusal event and counts `gohan.hedge.refused{reason="provider_tool"}`, and the primary proceeds unchanged. The call does not fail — only the hedge is refused — because a run that declared one provider call must not pay for or effect a second one. `tools` owns the provider call's own governance; this change adds only the hedge's refusal to stand beside it. Admission also refuses the hedge, on the same permanent refusal path and before any probe is taken, when the profile's `Caps.Streaming` is false or its `LatencyClass` is `Batch` (D2).

### D7 — Wiring: the step is placed by `std.ModelChain`, and `Explain` shows its inertness

`std/hedge.New` is added to the recommended model chain between the router and the fallback, the position the ordering rules require (core/chains/model_chain.go:21-25). A flow that copies the chain and removes the step keeps a valid order and behaves identically minus the hedge, per `chains.preset-is-copyable`. `Explain` lists the step with its `KindHedge` and reports it as inert for a flow whose router has a single target, so `chains.hedge-never-same-endpoint`'s `Explain` clause is observable without a live call.

## Risks / Trade-offs

- [Risk] A hedge that fires on a stale P90 could spend two calls where one would have sufficed. → Bounded by D1's per-attempt timer and D4's bucket: the default rate hedges roughly one call in twenty, and the run's cost tags can set it to zero.
- [Risk] Charging the loser through the ledger while the chain's `Limits` step charges the winner can double-count. → D3 keeps the split by attempt: each attempt reports its own usage once, and the step charges only the attempt the chain did not.
- [Risk] Extending `stores.Entry` changes a stored shape. → The two new fields are additive and zero-valued for every tool entry; the root module is pre-v1 through M2, so no compatibility shim is added and the change is recorded in the changelog (docs/design/compatibility.md).

## Migration Plan

No data migration is required. Existing tool entries decode with `Attempts == 0` and an empty `Endpoint`, which the model-call reader treats as a non-hedged call; the `Entry` block of `openspec/specs/stores/spec.md` is edited in the same implementation commit and the package ships a `stores` delta declaring it. `std/hedge` lands with its constructor, its options and the tests; `std/telemetry` gains the rolling latency window; `std.ModelChain` and the presets include the step and wire the window through `WithLatency` where the ordering rules allow. No release tag is cut during M1 authoring.
