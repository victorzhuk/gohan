# 0152. Stream ownership, buffering, and the run worker

Status: accepted

## Context

Row 10 turns on the three stream protections the `streams` spec requires on the public path: a bounded provider buffer, stall detection that preempts or detaches a run, and exactly one terminal indication per run. Four of those requirements cannot be implemented against the current code without a normative decision, because the specs disagree with each other or with the code, and the public stream surface cannot carry the sequence envelope the spec assumes. The gaps are recorded in the row 10 design pass; this ADR resolves them.

The code today: `Drive` drains a sink into an unbounded slice and only then yields step events (`core/drive_lifecycle.go`, `core/drive.go`), so provider backpressure can never reach the consumer; `ModelStream` and `StallGuard` exist with no production caller; and `Done` is emitted by the model effect as well as by the lifecycle's terminal transition.

## Decision

1. **Ownership and ordering.** The lifecycle owns the effect-to-consumer seam. Component events are delivered in emission order *while the effect runs*; events a step returns are delivered only after the component emissions that preceded them. Every event is recorded before it is delivered. The conversation owns durable recording, stream identity and `Attach`, and creates no second queue. The lifecycle owns the terminal transition and emits at most one terminal result; the model effect stops emitting `Done`. `Sink.Emit` tracks whether delivery stopped, so cancellable effect work is cancelled when the consumer stops taking.

2. **Bounded provider buffering.** One FIFO per model call with a default capacity of 64 chunks (`DefaultStreamBuffer`). A full queue blocks further provider reads; chunks are never dropped, overwritten or reordered, and no retry or second call is started because the consumer is slow. `gohan.stream.buffer_full` counts a send that cannot proceed at once. The idle deadline is measured on the provider read and never while blocked on the full queue. Supervision sits inside the model chain around the provider, not around the whole middleware chain. The numeric default is sealed here; `WithStreamBuffer(n)` stays illustrative and no public configuration field is added by this change.

3. **Consumer stall, and the run worker.** A stall is detected by the run, not by the provider timer. The two actions are the ones the spec names: preemption and detach. Preemption aborts cancellable model work without appending a partial turn, preserves a shielded `SideEffect` until its result is journaled and appended, checkpoints at the safe point, suspends with `Suspended{Preempted, Token}`, and emits no `Done` and no failure; detach keeps the same run id, lease, sequence writer and accounting state, continues under a harness-owned context bounded by `MaxWallClock`, stops attached delivery, and is not itself a terminal outcome.
   This requires one exception to the model spec's iterator contract: a **public run is driven by one harness-owned run worker**, and the iterator the caller holds only reads recorded events. Provider helpers stay joined to their iterators and terminate before those iterators return, and the helper lifetime rule is unchanged for everything else. Without the exception the spec's own sentence — a stalled consumer preempts the run at its next safe point — cannot hold, because a consumer that has stopped calling `yield` cannot be interrupted by its producer.
   Budgets come from `RunLimits.ConsumerStall` (interactive 30s, agentic 120s, batch 0 disables detection) and `gohan.stream.consumer_stalled{action}` is counted once per run.

4. **One terminal.** A failing run ends with a single terminal indication per channel and never with `Done`. In-process, the iterator ends with one `(zero, error)` tuple, which is the stream protocol the error-tuple requirement already states. Durably and over SSE, the failure is one recorded `TerminalError` payload (`Problem`, next `Seq`) that `Attach` treats as terminal; and the SSE stream then closes. `Done` has no error field and will not grow one, so the model spec sentence that says a mid-stream failure ends with `Done` carrying the error is corrected to say the run ends with `StopFailed` and the failure is reported by the terminal error tuple. The lifecycle finishes the run `Failed` before the failure is reported, and no effect may emit its own terminal event.

5. **Partial history on failure.** An ordinary mid-stream provider failure appends the accumulated partial assistant message with `FinishError`, as the model spec requires; preemption does not, as the runtime spec requires. The two are distinguished by cause, not by the error's shape.

6. **Attach stops at a boundary.** Replay stops at `Suspended` and at the failure terminal as well as at `Done`. The memory event log must refuse a cursor older than what it retains instead of silently serving an overlapping window: "no gap or duplicate" cannot be claimed from a ring buffer that overwrites, so the store fails a stale cursor and the limitation is stated rather than papered over.

7. **The sequence envelope is a separate change.** The spec pairs every delivered event with `EventMeta`, which carries `Seq`; the public stream yields bare payloads and the event log assigns `Seq` inside the store without returning it. Exposing that envelope is a store-port and API change that touches `stores.EventLog`, which is frozen at v1 and may grow only through an optional interface. It is not part of this row: the durable-failure scenario is proven at the store and iterator level here, and the transport-level assertion waits for that change.

## Consequences

- Provider backpressure reaches the consumer for the first time, so a slow consumer can no longer let the model effect drain an unbounded slice.
- Preemption and detach become implementable as the spec describes them, at the cost of one harness-owned goroutine per public run — a lifetime rule that must be stated in the spec, not implied.
- A failure has exactly one visible indication on each channel, and reconnecting clients see the same terminal the live consumer saw.
- The row splits: ownership, buffering and stall handling can be implemented now; the sequence envelope needs its own change against the `EventLog` port.

## Alternatives

- Letting the conversation own the component queue. Rejected: it duplicates the delivery logic three drivers share and makes lease and transport policy part of each runtime.
- Keeping the unbounded slice and bounding only the provider queue. Rejected: the effect drains the provider queue into the slice, so the bound would never be reached and `streams.stream-buffer-bound` would be unprovable.
- Cancelling the caller's context on stall. Rejected: it surfaces as `context.Canceled` or an ordinary `Failed`, which the spec forbids for preemption, and it destroys the detached case.
- Emitting `Done` and then an error. Rejected: two terminal indications for one segment, which the one-terminal rule forbids.
- Proving the transport scenario with a locally invented SSE serializer in this row. Rejected: the transport surface does not exist yet, and a local counter would fake the sequence authority the spec assigns to the store.
