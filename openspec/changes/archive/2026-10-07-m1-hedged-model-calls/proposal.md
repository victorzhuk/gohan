# Proposal: m1-hedged-model-calls

## Why

The `chains` contract declares hedging in full — the `std/hedge.New(HedgePolicy{After, Targets, Budget, MaxExtra})` step, the timer, the never-same-endpoint rule, the loser's charge, the budget bucket and the single journal step — and no `std/hedge` package exists (openspec/specs/chains/spec.md:60,87-88,224-254). Six M1 scenarios are owned by this change and nothing implements them: `chains.hedge-fires-on-ttft`, `chains.hedge-never-same-endpoint`, `chains.hedge-loser-cancelled-and-charged`, `chains.hedge-budget-cap`, `chains.hedge-single-journal-step` and `chains.hedge-refused-with-provider-tools` (openspec/changes/m1/design.md §2.8). `std/route` ships the router and the breakers the step must respect, and `std/telemetry` records the TTFT the default `After` reads (openspec/changes/m1/design.md §2.13). The budget cap is blocked on the run's cost tags, which now ship: `ModelRequest.Metadata.CostTags` is populated in `std/assembly.go:355` and both adapters map or drop it (docs/adr/0159-request-metadata-cost-tags-never-content.md).

## What Changes

- Add `std/hedge` as the `KindHedge` model middleware: a TTFT timer on the primary attempt, at most `MaxExtra` further attempts on the router's next targets, first body chunk wins, the losers cancelled through their own context (openspec/specs/chains/spec.md:60,88).
- Add the non-consuming eligibility view on `std/route`, so a hedge decides admission before a half-open probe is consumed, and only the attempt it commits to takes the probe (openspec/changes/m1/design.md §2.8; std/route/route.go:84-92).
- Charge the losing attempt's reported usage as `gohan.usage.hedge_loser` against `RunLimits.MaxCost` through the run's ledger (openspec/specs/chains/spec.md:60; core/types/telemetry.go:78; docs/adr/0147-limit-termination-semantics.md).
- Journal the hedged pair as one model step recording `Attempts` and the winner's endpoint, and replay the winner's response (core/stores/journal.go:31-37; docs/adr/0149-journal-identity-refusals.md).
- Refuse the hedge when the assembled request declares a provider-executed tool, however late the primary's first body chunk is (core/provider_result.go:60-81; openspec/specs/chains/spec.md:60).
- Preserve and cover the six assigned M1 scenario IDs on the public model path.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `chains`: add only the facts the §6.8 *Hedging* paragraph leaves open for the implementation — the `Targets` supplier shape, the admission-before-probe rule, the refusal's `ClassPermanent`, the cost-tag input that resolves the budget cap, and the journal record that carries `Attempts` — without rewording the requirement or its scenario blocks (openspec/changes/m1-hedged-model-calls/specs/chains/spec.md).

## Impact

Adds the `std/hedge` package and a read-only eligibility call on `std/route`; extends `stores.Entry` with `Attempts` and `Endpoint` and adds the model-step reserve/complete to the driver's model-call path (`core/drive_turn.go`). No third-party dependency. `std.ModelChain` places the step inside `Router` and outside `Fallback`, which the existing ordering rules already validate (core/chains/model_chain.go:21-25). No release tag is cut; the root module stays pre-v1 through M2.
