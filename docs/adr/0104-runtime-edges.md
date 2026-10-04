# ADR-0104: Runtime edges — model timeouts, mid-stream failure, cancel, idempotent send, default limits, cost, session delete

Status: accepted · Origin: review round 30 (2026-09-30)

## Decision

`ModelProfile.Timeout{Connect, FirstChunk, Idle}` with `LatencyClass` defaults; connect/first-chunk expiry is `Transient`, idle expiry after the first chunk is `Permanent`. Mid-stream failure appends the partial message with `FinishError` and an `Error` block, ends the run `StopFailed`, and `Replay` treats it as terminal. `Conversation.Cancel(ctx, sessionID)` is owner-checked and observed by the lease holder at the next safe point; core never queues (`ErrRunActive`). `Send`/`Invoke` dedup through `Runs.ByOperation` when `IdempotencyKey(ctx)` is set. Default `RunLimits` per preset are constants; unbounded runs fail `Build`. Cost is computed by the telemetry step at message end (`Usage × Pricing`, sandbox seconds) and checked against `MaxCost` after every effect. `SessionLog.Delete` and `Stores.DeleteSession` cascade.

## Context and evidence

These are the unhappy paths a transport hits in week one: hung providers, stalled streams, users typing during a stream, client retries, and "delete this chat". None had a contract.

## Consequences

Nine scenarios across `model`, `flow`, `limits`, `stores`; tasks 7, 18, 23 updated.
