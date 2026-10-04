# ADR-0128: Hedged model calls — TTFT-triggered, budgeted, never on the same endpoint

Status: accepted · Origin: grill round 54 (2026-09-30)

## Decision

`KindHedge` is a model-chain step outside `Fallback` and inside `Router`. `std/hedge` starts one extra attempt on the next router target when the primary has produced no body chunk within `After` (default the endpoint's rolling P90 time-to-first-chunk, minimum 800 ms); the first body chunk wins and the rest are cancelled. A token bucket caps hedges at `Budget` (5 %) of base calls. Hedging is refused for open/half-open breakers, calls declaring provider-executed tools, non-streaming profiles and the batch class. The loser's usage is charged to the run as `gohan.usage.hedge_loser`; the journal keeps one model step with the attempt count and winner.

## Context and evidence

Tail-tolerant gateway practice measures on the first body byte, fires a hedge at the primary's P90, caps hedges at 5–10 % of traffic to avoid doubling load on a degraded provider, cancels the loser and hedges across providers rather than the same path. gohan's chain reacted only to errors and timeouts, so a slow-but-alive primary consumed the whole latency budget.

## Consequences

`chains` v1.1 (`KindHedge`, ordering rule, hedging rule block, six scenarios), telemetry attribute; task 13 ordering; `std/hedge` in M1.
