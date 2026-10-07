# Tasks

Delivered in `feat(chains): hedge the model call path on the public model chain`. Observed:
`task lint` (0 issues), `task test`, `task spec` (`missing_in_scope=0`) are green; the six
owned scenario IDs pass, and `task spec:gate GATE=M1` shows none of them missing.

## 1. Route eligibility view

- [x] 1.1 Add a read-only `Breaker.State() BreakerState` (`Closed`/`Open`/`HalfOpen`) to `std/route/breaker.go`, and the read-only `Router.Eligible() []types.Model` to `std/route/route.go`: `Eligible` returns only the endpoints whose breaker is `Closed`, reading `Breaker.State()` and never calling `Breaker.Allow()`, so a half-open breaker is not free admission and no probe is consumed; verify `go test -short -timeout 2m ./std/route/... -run 'TestRouterEligible'` proves `Eligible` leaves a half-open probe unconsumed while `Candidates` (std/route/route.go:84-92) consumes it.
  - evidence: `std/route/breaker.go` (`BreakerState`, read-only `State()`); `std/route/route.go` (`Eligible()` closed-only)

## 2. `std/hedge` step

- [x] 2.1 Create `std/hedge` with `HedgePolicy{After, Targets, Budget, MaxExtra, BudgetByTag}` and `New(HedgePolicy, ...Option) chains.ModelMiddleware` returning a `KindHedge` step, plus `WithLedger(*chains.LimitsState, types.Pricing)` and `WithLatency(func(endpoint string) time.Duration)`; verify `go test -short -timeout 2m ./std/hedge/... -run 'TestHedgePolicy'`.
  - evidence: `std/hedge/hedge.go` (`HedgePolicy`); `std/hedge/hedge_test.go`
- [x] 2.2 Implement the TTFT timer: it starts when the primary attempt's iterator is first pulled, stops on the first content or tool-call chunk, defaults `After` to the latency source's rolling P90 clamped to 800 ms, and cancels the timer on every exit path; verify `go test -short -timeout 2m ./std/hedge/... -run 'TestHedgeTimer'`.
  - evidence: `std/hedge/hedge.go`; `std/hedge/hedge_test.go` (`TestHedgeTimer`)
- [x] 2.3 Implement attempt orchestration: read `Targets()` live, filter same-name and breaker-open targets through `Router.Eligible()`, start at most `MaxExtra` further attempts with their own `context.WithCancel`, stream the winner and cancel the losers; verify `go test -short -timeout 2m ./std/hedge/... -run 'TestHedgeAttempts'` names the subtests `chains.hedge-fires-on-ttft` and `chains.hedge-never-same-endpoint`.
  - evidence: `std/hedge/hedge.go`; `std/hedge/hedge_test.go` (`chains.hedge-fires-on-ttft`, `chains.hedge-never-same-endpoint`)
- [x] 2.4 Charge the loser: on a loser's reported usage call `LimitsState.Charge(usage, pricing)` once and tag the model span with `gohan.usage.hedge_loser`, never charging the winner twice; verify `go test -short -timeout 2m ./std/hedge/... -run 'TestHedgeLoser'` names the subtest `chains.hedge-loser-cancelled-and-charged`.
  - evidence: `std/hedge/hedge.go`; `std/hedge/hedge_test.go` (`chains.hedge-loser-cancelled-and-charged`)
- [x] 2.5 Implement the budget token bucket: one bucket per run refilled by `Budget` per base call, capped at one, resolved per run from `types.RunInfoFrom(ctx).CostTags` through `BudgetByTag`, incrementing `gohan.hedge.budget_exhausted` and declining the hedge when empty; verify `go test -short -timeout 2m ./std/hedge/... -run 'TestHedgeBudget'` names the subtest `chains.hedge-budget-cap`.
  - evidence: `std/hedge/hedge.go` (budget bucket); `std/hedge/hedge_test.go` (`chains.hedge-budget-cap`)
- [x] 2.6 Refuse the hedge when the request declares a `types.ByProvider` tool: record `types.ClassPermanent` on the refusal event, count `gohan.hedge.refused{reason="provider_tool"}`, and let the primary proceed; verify `go test -short -timeout 2m ./std/hedge/... -run 'TestHedgeProviderTools'` names the subtest `chains.hedge-refused-with-provider-tools`.
  - evidence: `std/hedge/hedge.go` (refusals); `std/hedge/hedge_test.go` (`chains.hedge-refused-with-provider-tools`)
- [x] 2.7 Refuse the hedge when the profile's `Caps.Streaming` is false or its `LatencyClass` is `Batch`, on the same permanent refusal path and before any probe is taken; verify `go test -short -timeout 2m ./std/hedge/... -run 'TestHedgeIneligible'` names the refusal reasons.
  - evidence: `std/hedge/hedge.go` (refusals); `std/hedge/hedge_test.go`
- [x] 2.8 Ship the rolling latency window in `std/telemetry` — a bounded per-endpoint ring of the TTFT samples `ObserveStream` records, reporting the endpoint's rolling P90 time-to-first-chunk (fixed sample count, no third-party dependency) — and wire it into the step in `std.ModelChain` through `WithLatency`, so the contract default `After` (rolling P90, 800 ms floor) holds without host configuration; verify `go test -short -timeout 2m ./std/telemetry/... -run 'TestLatencyWindow'` and `go test -short -timeout 2m ./std/hedge/... -run 'TestHedgeTimer'`.
  - evidence: `std/telemetry/latency.go` (`LatencyWindow` rolling P90); `std/model_chain.go` wiring

## 3. Single journal step

- [x] 3.1 Add `Attempts int` and `Endpoint string` to `stores.Entry`, carry them through `MemoryJournal` reserve and complete, and decode them from the record envelope; edit the `Entry` block of `openspec/specs/stores/spec.md` directly in the same commit (egress precedent) and keep the package's `stores` delta in sync; verify `go test -short -timeout 2m ./core/stores/... -run 'TestJournalEntryAttempts'`.
  - evidence: `core/stores/journal.go`/`journal_memory.go` (`Entry.Attempts`/`Endpoint`); `openspec/specs/stores/spec.md` `Entry` block carries both (done by the driver); `core/stores/journal_model_test.go`
- [x] 3.2 Journal the hedged pair once in the driver's model-call path (`core/drive_turn.go`): one reserve and one complete per model call, `Attempts` counting the attempts that started and `Endpoint` naming the winner, with a re-drive replaying the winner's response instead of re-issuing the pair; verify `go test -short -timeout 2m ./core/... -run 'TestModelStepJournal'` names the subtest `chains.hedge-single-journal-step`.
  - evidence: `core/drive_model_journal.go`; `core/drive_model_journal_test.go` (`chains.hedge-single-journal-step`)

## 4. Wiring and package gates

- [x] 4.1 Place `std/hedge.New` between the router and the fallback in `std.ModelChain` and the presets, wire the `std/telemetry` rolling window into the step through `WithLatency`, and have `Explain` report the step as inert for a single-target router; verify `go test -short -timeout 2m ./std/... -run 'TestModelChainHedge'`.
  - evidence: `std/model_chain.go` (`std.ModelChain` builder, `Explain` inertness); `std/model_chain_test.go`
- [x] 4.2 Run the repository wrappers and resolve failures; verify `task lint` and `task test`.
  - evidence: driver-run `task lint` 0 issues; `task test` green across root, `adapter/otel`, `adapter/openai`, `adapter/anthropic`, `examples`
- [x] 4.3 Validate the change and the M1 milestone acceptance after all required M1 owners land; verify `openspec validate m1-hedged-model-calls --strict --no-interactive` and `task spec:gate GATE=M1`.
  - evidence: `Change 'm1-hedged-model-calls' is valid`; `task spec:gate GATE=M1` reports `unregistered=0` and no missing `chains.hedge-*` id.
  - The package validates and its own ids pass; the milestone gate stays open until the remaining owners land.
