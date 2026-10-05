# 0153. The conversation's delivery handoff, stall arming, and resumed worker

Status: accepted · Amends ADR-0152 decision 1 and decision 3

## Context

ADR-0152 decision 1 states that the conversation "creates no second queue", and decision 2 seals a bounded per-call provider buffer. On the public path neither holds: the run worker drains the bounded provider buffer into an unbounded tuple slice (`core/stream_lifetime.go`, `runQueue`), so the provider never reaches its bound and `streams.stream-buffer-bound` cannot be satisfied through `Send`. Three further defects follow from the same seam. `StallGuard.Watch` starts its clock before the first take and measures time since the last take, so it cannot distinguish a consumer blocked inside `yield` from a consumer awaiting producer output, and a quiet model call or tool batch is reported as a stall. When the consumer breaks early, `Watch` closes its `gone` channel and joins only its watcher goroutine, so a producer already blocked in the handoff stays blocked and the run retains its helper, worker, lease heartbeat and queue. And `Resume` drives `DriveLifecycle` directly on the caller's iterator, installing neither the worker nor the stall guard and recording no durable failure terminal, so a run loses its protections after its first suspension.

## Decision

1. The conversation owns exactly one bounded handoff between the run worker and the attached consumer: `DefaultStreamBuffer` (64) ordinary tuples and one separate terminal slot. Ordinary production blocks cancellably when the handoff is full; nothing is dropped, overwritten or reordered. This replaces the unbounded slice. It is not a second queue layered under the guard: `StallGuard`'s forwarding producer and drain goroutines are removed from the public worker path, and the handoff is the single worker-to-consumer seam. `DefaultStreamBuffer` is reused rather than a new constant added.
2. The stall deadline is armed only while a consumer `yield` callback is outstanding. Time a consumer spends waiting for producer output is not a stall. A blocked callback cannot be unwound; the guard acts at the run's next safe point.
3. Explicit consumer abandonment wakes empty and full handoff waits and applies the run's stall action even when `ConsumerStall` is 0. Zero disables timed detection only, never cleanup.
4. Under preemption, delivery shutdown must not cancel the lifecycle: the worker remains able to settle shielded work, persist the `Preempted` checkpoint, release the lease and suspend with no `Done` and no failure. Under detach, attached retention stops and the worker continues recording under the same run identity, lease, ledger and wall-clock bound.
5. `Resume` drives a resumed run on the same harness-owned worker, with its restored state, ledger and identity. There is no separate resumed delivery implementation.
6. `Attach` observes writes made by any conversation instance over shared stores: local waiter notification stays the fast path, and a cancellable replay poll covers cross-instance writes. `stores.EventLog` is unchanged and the public sequence envelope stays deferred (ADR-0152 decision 7).

## Consequences

- The provider bound is reachable from `Send`, so `streams.stream-buffer-bound` becomes provable on the public path.
- A slow or absent consumer costs bounded memory instead of unbounded growth, and a stalled consumer is distinguished from a slow producer.
- One private prepared-execution seam replaces two divergent execution paths; resumed runs gain the protections initial runs have.
- Cross-instance `Attach` trades latency (one poll interval) for observability it cannot get from the frozen `EventLog` port.

## Alternatives

- Bounding only the provider queue. Rejected: the worker drains it into the slice, so the bound is never reached — the same reason ADR-0152 rejected keeping the unbounded slice.
- Cancelling the caller's context on stall. Rejected in ADR-0152; it surfaces as `context.Canceled` or an ordinary failure, which the spec forbids for preemption.
- Making the event log the only delivery path. Rejected: it needs cursor and retention coordination for ordinary attached streams and provides no provider backpressure by itself.
- Forking a second execution path for resumed runs. Rejected: it is the defect this ADR repairs.
