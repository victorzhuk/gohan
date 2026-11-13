# Design

## Context

The audit verified each contradiction against current sources and shipped code. This change transcribes settled behavior into the specs; it opens no new design. Two points need precise wording to avoid replacing one contradiction with another.

## Decision 1: two-path limit semantics

`architecture.md:84` and the shipped ledger define two distinct paths:

- **Counter stop.** Reaching `MaxTurns` or `MaxToolCalls` at an effect boundary stops the run: `Done{Reason: StopLimit}`, partial outcome reported, not resumable. The step records itself; it does not abort.
- **Hard abort.** `MaxCost` or `MaxWallClock` exhaustion aborts with `*LimitExceededError`, not resumable.
- **Batch refusal.** The ledger refuses an oversized reservation without charging it. The native driver returns that error directly, preserving `errors.Is(err, types.ErrBatchOverrun)` (`core/chains/limits.go:561-566`, `core/drive_turn.go:849-851`). If `runtime.Batch.Run` returns an overrun after reservation, the driver refunds and returns `*LimitExceededError{Limit: "MaxToolCalls"}` (`core/drive_turn.go:871-879`). Foreign calls render reservation failures as permanent `not_executed:` results (`core/foreign_conversation.go:255-261`).

The `limits.provider-calls-count` scenario names the ledger refusal. Its existing subtest checks `ReserveBatch` directly, not the native driver return. `runtime.batch-limit-before-execute` names the separate `runtime.Batch.Run` refusal path. Its subtest checks the batch error, not driver translation.

## Decision 2: resume choreography, primary and skip

- External `Resume` requires `stores.CheckpointRuns`; without it, `Resume` returns `types.ErrCheckpointIncompatible` before consuming the token (`architecture.md:104`).
- `Recover` handles preempted runs before stale runs: `ConsumeIf` on the current `Preempted` checkpoint, then a token-fenced `ResumingCheckpoint` lease (`architecture.md:106`). A stale token never replaces a newer suspension.
- Without `CheckpointRuns`, automatic preempted recovery skips its token-fenced path. The stale pass does not require that surface. After that pass, `Recover` calls `ResumeReady` before checking `CheckpointRuns` for each consumed candidate. A discovery error propagates; unbound memory checkpoint discovery returns `types.ErrCheckpointIncompatible`. After successful discovery, candidates without `CheckpointRuns` are skipped. For supported candidates, recovery fences the lease without consuming again (`core/recover.go:77-114`). The frozen `Consume` and `Resuming(runID)` methods remain available.
- `runtime/spec.md:96-99`: `Runs.Start` belongs to new `Invoke`/`Send`; `Resume` acquires its lease through `CheckpointRuns.ResumingCheckpoint`. The lifecycle list separates the two entries. `architecture.md:84,102-104` records the same refusal and discovery distinctions.

## Decision 3: construction vocabulary

Verified shipped symbols: `gohan.WithNativeAgent(gohan.NativeSpec)` (`core/native_options.go:53`), `gohan.NewNativeConversation(stack, name, opts...)` (`core/native_conversation.go:22`), `gohan.NewForeignConversation` (`core/foreign_conversation.go:334`). `NativeSpec.Request.Tools` derives from `NativeSpec.Tools` during resolution (`architecture.md:74`). The flow constructor table replaces the two `agent.New*` rows with the definition-first pair; `chains/spec.md:58` rewords "agent.New takes chains explicitly" to the definition's `ModelChain`/`ToolChain` fields. Foreign backends keep `einoflow.FromRunnable` / `adkflow.FromAgent` rows unchanged.

## Decision 4: secret injection boundary

`SecretResolver` is the only workspace secret path (`sandbox/spec.md:132`, `identity/spec.md:205`, frozen `types.SecretResolver{Resolve}` at `compatibility.md:86`). Rule 2 and the identity table cell name it; the run `Credential` never enters the workspace. The `Workspace` interface block gains `SetEnv(ctx context.Context, env map[string]string) error` — the exact frozen shape (`compatibility.md:85`, `core/types/sandbox.go:77-84`).

## Risks / Trade-offs

- The model spec and other files shift under the in-flight `repair-spec-strict` batch; tasks cite requirement headings and scenario IDs, not line numbers.
- Existing scenario subtests do not prove native driver error propagation or translation. The recovery optional-surface test omits `ResumeReadyLister`, so it does not prove the discovery-error or successful-discovery skip paths. Required gates remain incomplete.