# Proposal

## Why

An existing-system audit of `openspec/specs` (2026-11-12, audit mode) verified five normative contradictions where stale clauses survived additive contract evolution. Each pairs two live MUST sets that no implementation can satisfy simultaneously, or directs callers to an API the shipped code replaced:

1. **Limits stop-vs-abort** (Medium). `limits/spec.md:48` ends counter exhaustion with `Done{Reason: StopLimit}`; `limits/spec.md:73` aborts `MaxToolCalls` with `*LimitExceededError`; `chains/spec.md:83` labels `MaxTurns` a hard abort; `runtime/spec.md:113` says a batch overrun "aborts the run". The ledger refuses an oversized reservation without charging it and the native driver returns the error matching `types.ErrBatchOverrun` directly. Only an overrun returned by `runtime.Batch.Run` triggers a refund and translation to `*LimitExceededError{Limit: "MaxToolCalls"}` (`core/drive_turn.go:849-879`). Foreign calls render reservation failures per call.
2. **Resume choreography** (Medium). `recovery/spec.md:21` (step 3a) prescribes `Consume` → `Resuming` without a token fence while `recovery/spec.md:65` requires conditional consumption plus a token-fenced lease; `stores/spec.md:417,425` describe the unfenced path as the recovery route; `runtime/spec.md:96-99` routes `Resume` through `Runs.Start`. External `Resume` requires `CheckpointRuns` and refuses with `ErrCheckpointIncompatible` before consuming when absent. `Recover` uses `ConsumeIf` before `ResumingCheckpoint`. Consumed-checkpoint discovery runs after stale recovery and propagates errors before any per-candidate `CheckpointRuns` check.
3. **Flow construction** (Medium). `flow/spec.md:91-92` still specifies `agent.New(stack, spec, runtime, ...)` / `agent.NewConversation(stack, spec, runtime, ...)`. Shipped API (`core/native_options.go:18,53`, `core/native_conversation.go:22`, `core/foreign_conversation.go:334`): `gohan.WithNativeAgent(gohan.NativeSpec)` registers a definition at `Build`; `gohan.NewNativeConversation(stack, name, opts...)` reads the resolved definition; nothing passes a runtime. No spec declares the `NativeSpec` single-source tool invariant (`architecture.md:74-76`).
4. **Sandbox secret resolution** (High, security). `sandbox/spec.md:91` (rule 2) and `identity/spec.md:109` (ctx-propagation table) resolve workspace secrets through `CredentialSource`; `sandbox/spec.md:132` and `identity/spec.md:205` mandate the distinct `SecretResolver` port and forbid credential substitution. `docs/design/compatibility.md:86` freezes `types.SecretResolver{Resolve}`. The stale pair would route run credentials into workspace env, violating the injection-boundary MUST.
5. **Workspace port shape** (Medium, security). The normative `Workspace` block (`sandbox/spec.md:18-24`) omits `SetEnv` while `sandbox/spec.md:132` makes it the only injection path and `compatibility.md:85` freezes `Workspace{Exec, Put, Get, SetEnv, Snapshot, Close}`.

## What Changes

- `limits`: the Run limits requirement distinguishes counter stops from cost/time aborts and the two distinct batch-refusal sources. The provider-calls-count scenario names the ledger refusal identity.
- `flow`: a new Native definition construction requirement declares `WithNativeAgent`/`NewNativeConversation`, build-time resolution reuse, and the single-source tool invariant; the constructor table drops the runtime-injection rows.
- `runtime`, `recovery`, `stores`, `chains`: Contract prose aligns with the shipped batch protocol, token-fenced resume choreography, discovery errors, and stop/abort labels. Recovery requirements also distinguish discovery failure from unsupported candidate skips.
- `sandbox`, `identity`: rule 2 and the ctx-propagation table name `SecretResolver`; the `Workspace` block gains `SetEnv` matching the frozen shape.

Shipped behavior is the ground truth for every edit; no production code change is intended. `runtime.batch-limit-before-execute` specifies the `runtime.Batch.Run` refusal path. Its existing subtest checks the batch error but does not establish the driver translation.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `limits`: stop/abort/batch-refusal distinction (delta).
- `flow`: native definition construction requirement (delta).
- `runtime`, `recovery`, `stores`, `chains`, `sandbox`, `identity`: contract alignment, including recovery discovery requirements and port-block alignment.

## Impact

Spec text and one generated index only. ADR-0187 records the alignment decisions, including distinct limit refusals and discovery-before-skip recovery. `docs/design/types.md` regenerates (`CheckpointRuns` rows are already pending from the in-flight structural batch). Scenario IDs are proposed at authoring and registered at admission per ADR-0171. No frozen port grows: `SetEnv` is already frozen in `compatibility.md:85`.

Dependencies: none. `governance-fail-closed` edits `sandbox` and `identity` after this change lands (shared files, sequential admission).