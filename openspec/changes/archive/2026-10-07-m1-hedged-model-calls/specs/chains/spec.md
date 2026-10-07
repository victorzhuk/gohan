# Spec Delta

## Context

The main `chains` spec already carries the hedging contract and the six
scenarios this change is written against. The *Hedging* paragraph in §6.8 names
the step and its ordering position, the `After` default (the endpoint's rolling
P90 time-to-first-chunk, never below 800 ms), the never-same-endpoint rule, the
loser's charge (`gohan.usage.hedge_loser`, counted against `RunLimits.MaxCost`),
the budget token bucket with `gohan.hedge.budget_exhausted`, the refusal when the
call declares provider-executed tools, the single journal step recording
`Attempts` and the winner's endpoint, and the metrics
(openspec/specs/chains/spec.md:60,87-88,224-254).

This delta adds only the facts that paragraph leaves open for the implementation,
and reproduces the `No hidden behavior` requirement with its scenario blocks
verbatim — no scenario text changes. It names the `Targets` supplier shape, the
admission-before-probe rule, the refusal's error class, the cost-tag input to the
budget cap, and the journal carrier the `Attempts` field is written on.

## MODIFIED Requirements

### Requirement: No hidden behavior

`std/hedge.New(HedgePolicy{After, Targets, Budget, MaxExtra})` MUST be a
`KindHedge` step placed inside `Router` and outside `Fallback`. `Targets` MUST be
`func() []types.Model`, the router's ordered candidate supplier, read live at
admission time. The never-same-endpoint rule MUST filter by `ModelProfile.Name`,
so a candidate naming the in-flight endpoint is skipped and never preferred; a
router with a single target MUST leave the step inert and `Explain` MUST report
it so.

The step MUST decide admission — never-same-endpoint, breaker eligibility (a closed breaker only), budget, provider-tool ineligibility, `Caps.Streaming` false and `LatencyClass == Batch` — before it calls anything that consumes a half-open breaker probe, and `std/route` MUST expose a read-only eligibility view for this decision so that only the target the step commits to takes the probe.

`Budget` MUST be a token bucket of hedges per base call, capped at one token, and
its effective rate MUST be resolvable per run from the run's `CostTags`; a run
whose tags resolve to zero MUST never hedge. When the bucket is empty no hedge
MUST fire and `gohan.hedge.budget_exhausted` MUST increment.

A hedge MUST be refused, and MUST record `types.ClassPermanent` on the refusal,
when the assembled request declares any tool whose `Executor` is
`types.ByProvider`; the primary MUST proceed unchanged and only the hedge is
refused.

The losing attempt's `Usage` MUST be charged once through the run's ledger as
`gohan.usage.hedge_loser` against `RunLimits.MaxCost`, and the hedged pair MUST
journal as one model step carrying `Attempts` and the winner's endpoint on the
journal record. Replay MUST reproduce the winner's response and MUST NOT re-issue
the pair; the loser MUST have no journal entry of its own.

#### Scenario: empty chains
ID: `chains.empty-chains`
- WHEN a flow is built with core only, no `std`, and empty chains
- THEN a run calls the raw model and tools with no added prompt text, no guards, no journal; `Explain` shows zero steps and the assembled request equals instruction + history + input

#### Scenario: prompt strings accounted for
ID: `chains.prompt-strings-accounted-for`
- WHEN `Explain` renders the assembled request for a `std.Interactive()` flow
- THEN every non-user, non-instruction string in it maps to a named `PromptSet` field, and the manifest carries the `PromptSet` hash

#### Scenario: step-named failure
ID: `chains.step-named-failure`
- WHEN a user middleware panics inside the tool chain
- THEN the flow returns `*StepError{Step: "<name>"}` wrapping the panic, and the span records the step

#### Scenario: read-only tools pay nothing
ID: `chains.read-only-tools-pay-nothing`
- WHEN a turn calls only `ReadOnly` tools
- THEN no journal read or write occurs, no cancel shield is applied, and exactly one `SessionLog.Append` happens

#### Scenario: hedge fires on late first chunk
ID: `chains.hedge-fires-on-ttft`
- WHEN the primary endpoint returns headers at once but no body chunk within `After`
- THEN a second attempt starts on the next router target and the first body chunk delivered wins

#### Scenario: hedge loser cancelled and charged
ID: `chains.hedge-loser-cancelled-and-charged`
- WHEN the hedge wins
- THEN the primary attempt's ctx is cancelled, its reported usage is added to the run as `gohan.usage.hedge_loser` and counts against `MaxCost`

#### Scenario: hedge budget cap
ID: `chains.hedge-budget-cap`
- WHEN every call to a degraded endpoint would hedge
- THEN at most `Budget` of them do, the rest wait for the primary or its timeout, and `gohan.hedge.budget_exhausted` increments

#### Scenario: hedge refused with provider tools
ID: `chains.hedge-refused-with-provider-tools`
- WHEN the call declares a provider-executed tool
- THEN no hedge fires however late the first chunk is

#### Scenario: hedge never same endpoint
ID: `chains.hedge-never-same-endpoint`
- WHEN the router has a single target
- THEN the hedge step is inert and `Explain` says so

#### Scenario: hedge single journal step
ID: `chains.hedge-single-journal-step`
- WHEN a hedged call completes
- THEN the journal holds one model step with `Attempts: 2` and the winner's endpoint, and replay reproduces the winner's response

#### Scenario: preset is copyable
ID: `chains.preset-is-copyable`
- WHEN `std.Interactive()`'s body is copied into a service and one step removed
- THEN `Build` validates the remaining order and the flow behaves identically minus that step
