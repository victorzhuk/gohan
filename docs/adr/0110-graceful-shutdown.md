# ADR-0110: Graceful shutdown preempts in-flight runs at the safe point

Status: accepted · Origin: grill round 36 (2026-09-30) · Amends ADR-0102

## Decision

`Stack.Shutdown(ctx)` is the service's SIGTERM hook. It rejects new `Send`/`Invoke`/`Resume` with `ErrShuttingDown` (`Transient`, 503 + `Retry-After`), flips `Ready()`, and preempts every in-flight run at its next safe point — the persisted turn boundary `Cancel` already uses — as a normal suspension with the new reason `Preempted`. A `SideEffect` tool under the shield completes first; a streaming model call is aborted and re-issued on resume. Preempted runs resume on any pod either by the client (`Resume(token, Continue())`) or by `Recover`, which handles `Runs.Preempted` before stale runs and without the lease-TTL wait. If the grace budget runs out, `Shutdown` returns `ErrShutdownIncomplete` and the remaining runs take the existing crash path.

## Context and evidence

Without this, every rolling deploy turned streaming runs into the crash path: up to `LeaseTTL + ReaperEvery` before reclaim and `Failed{Uncertain}` for conversations whose client was on the dying pod. Temporal's worker shutdown gives in-flight work a grace period and then cancels it; the practitioner pattern for fast redistribution is to fail the unit on the stop signal so another worker takes it immediately. gohan already has the durable primitives (checkpoint, single-use token, `Resuming`), so preemption is a suspension rather than a failure and the stream stays continuous for the client.

## Consequences

`runtime` v1.1 (shutdown section, six scenarios), `suspension` v1.1 (`Preempted`, `Continue()`), `recovery` v1.1 (rule 3a, one scenario), `Runs.Preempted`, three metrics; task 27a in M0.
