# ADR-0187: Contract drift alignment across limits, flow, recovery and secrets

Date: 2026-11-12
Status: Proposed (`repair-contract-drift`)
Supersedes: none

## Context

An existing-system audit of `openspec/specs` found five normative contradictions left behind by additive contract evolution. In each pair both clauses are live, and no implementation satisfies the pair at once:

- `limits` ended counter exhaustion with `Done{Reason: StopLimit}` in one clause and aborted `MaxToolCalls` with `*LimitExceededError` in the batch path; `chains` labelled `MaxTurns` a hard abort.
- `recovery` prescribed `Consume` → `Resuming` without a token fence while its own automatic-preemption requirement demands conditional consumption and a token-fenced lease; `stores` described the unfenced path as the recovery route and `runtime` routed `Resume` through `Runs.Start`.
- `flow` still specified `agent.New(stack, spec, runtime, ...)`, an API no shipped definition path uses.
- `sandbox` rule 2 and the `identity` ctx-propagation table resolved workspace secrets through `CredentialSource`, which the same specs forbid and which would route run credentials into workspace env.
- The normative `Workspace` block omitted `SetEnv` while the only legal injection path and the frozen type shape both require it.

## Decision

1. **Two limit paths, plus distinct refusals.** `MaxTurns` and `MaxToolCalls` stop the run at an effect boundary with `types.Done{Reason: types.StopLimit}`. `MaxCost` and `MaxWallClock` are hard checks that abort with `*LimitExceededError`. An oversized ledger reservation spends no budget and the native driver returns its error matching `types.ErrBatchOverrun` directly. An overrun from `runtime.Batch.Run` instead causes a refund and a driver error of `*LimitExceededError{Limit: "MaxToolCalls"}`. The foreign path renders reservation failures per call as permanent `not_executed:` results.
2. **Primary resume choreography, discovery and skips.** External `Resume` requires `stores.CheckpointRuns` and returns `types.ErrCheckpointIncompatible` before consumption when absent. `Recover` processes preempted runs before stale runs, using `ConsumeIf` and a token-fenced `ResumingCheckpoint` lease. It skips the preempted path without `CheckpointRuns`. After the stale pass, `Recover` calls `ResumeReady` when available and propagates discovery errors. Unbound memory checkpoint discovery returns an error matching `types.ErrCheckpointIncompatible`. Only after successful discovery does recovery check each consumed candidate for `CheckpointRuns` and skip unsupported candidates. Supported candidates use their already-consumed token to fence the lease. The stale pass does not require `CheckpointRuns`, but a later discovery error can still make `Recover` fail. Frozen `Consume` and `Resuming(runID)` remain available. `Runs.Start` belongs to new `Invoke`/`Send`; `Resume` uses `CheckpointRuns`.
3. **Construction vocabulary.** A native flow is a `gohan.NativeSpec` registered through `gohan.WithNativeAgent` before `Build`. `Build` resolves it once and `gohan.NewNativeConversation(stack, name, opts...)` reads that resolution instead of recomputing it. `Request.Tools` derives from `NativeSpec.Tools` during resolution and is never supplied independently; chains arrive through the definition's `ModelChain` and `ToolChain` fields. An unknown definition or profile fails before any provider call.
4. **Secret injection boundary.** Workspace secrets resolve through the optional `types.SecretResolver` port for the run originator and enter only through `Workspace.SetEnv`. The run `Credential` never substitutes for a resolver and never enters a workspace. The normative `Workspace` interface carries `SetEnv(ctx context.Context, env map[string]string) error`, matching the frozen shape.

Shipped behavior is the ground truth for every edit. `limits.provider-calls-count` names the ledger refusal, and `flow` gains a Native definition construction requirement with two registered scenario IDs.

## Consequences

- The specs and the shipped core agree on limit outcomes, resume acquisition and construction, so a reader cannot implement a contradiction.
- `repair-contract-drift` adds no code change; it fixes text and registers two `flow` scenarios. `docs/design/types.md` regenerates for the already-pending `CheckpointRuns` rows.
- `governance-fail-closed` edits `sandbox` and `identity` after this change lands, since the files are shared and admission is sequential.

## References

- `docs/design/architecture.md` — limit paths (§4.2b), lease and checkpoint ownership (§4.2c), native definition resolution.
- `docs/design/compatibility.md` — frozen `Workspace` and `SecretResolver` shapes.
- `core/chains/limits.go`, `core/drive_turn.go`, `core/native_conversation.go`, `core/foreign_conversation.go`, `core/recover.go`, `core/stores/checkpoint_memory.go`, `core/native_resolve.go`, `core/native_options.go`.
