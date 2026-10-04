# ADR-0131: Run mailbox — cancel and mid-run steering through one channel

Status: accepted · Origin: grill round 57 (2026-09-30)

## Decision

`Runs` gains a per-run mailbox: `Signal(runID, Signal)` from any pod, `Drain(lease)` by the holder at every safe point, `Finish` atomic with pending steers (`ErrSignalsPending` → one more turn). `Cancel` becomes `SignalCancel`. `Conversation.Steer` posts `SignalSteer{Message}` after the `StageInput` guard; the runtime appends drained steers as `OriginUser` messages after the batch's tool results, emits `SteerApplied`, and only the root run drains. `Steer` on a finished or suspended run is `ErrRunNotActive`, so clients fall back to `Send`; queueing stays in the client.

## Context and evidence

Agent products have converged on three delivery modes for input typed while the agent works: steer at the next tool boundary, queue after the run, interrupt (cancel then send). Implementations inject the steer as a user message after the tool results to keep call/result adjacency, cap the queue, report unreached steers, skip subagents and degrade to a normal send when no run is active. gohan only offered cancel-then-send, which discards in-flight tool work, and its cross-pod cancel had no port.

## Consequences

`stores` v1.8 (`Signal`, `Drain`, `SignalKind`, `MaxPendingSignals`, three sentinels, two scenarios), `flow` v1.5 (`Steer`, steering rule, four scenarios), `runtime` v1.4 (drain step), `streams` v1.6 (`SteerApplied`), `agui` v1.5 (one scenario), `identity` v1.4 (one scenario), two catalog rows; tasks 10 and 23; AG-UI scenario M4.
