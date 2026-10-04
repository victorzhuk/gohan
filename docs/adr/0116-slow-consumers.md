# ADR-0116: Slow consumers — run-owned heartbeat, buffered provider read, stall as preemption

Status: accepted · Origin: grill round 42 (2026-09-30) · Amends ADR-0102, ADR-0108

## Decision

Pull semantics stay (the iterator body runs on the consumer's goroutine), with three run-owned protections: the lease heartbeat runs on a helper goroutine for the run's lifetime; the model chain reads the provider stream on a helper goroutine into a bounded `StreamBuffer` (default 64) so `Timeout.Idle` never measures the consumer; and a consumer that takes no event for `RunLimits.ConsumerStall` (30 s / 120 s / disabled) preempts the run at the next safe point as in ADR-0110, or detaches it when `OnStall(Detach)` and an `EventLog` are configured. No coalescing or dropping.

## Context and evidence

Backpressure guidance for streamed LLM output is to stall the producer rather than buffer without bound — but a stalled *harness* starves its own lease, turns a slow client into a provider idle-timeout retry, and can hang a run until `MaxWallClock`. Reusing the preemption path gives a stalled client the same recovery as a pod restart.

## Consequences

`streams` v1.1 (rule block, five scenarios), `limits` v1.4 (`ConsumerStall`), `runtime` v1.3 (step 4), `model` idle note, `build` options `OnStall`, `WithStreamBuffer`; task 26 updated.
