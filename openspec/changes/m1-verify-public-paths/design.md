# Design: m1-verify-public-paths

## Context

The tools contract assigns Verify to three driver boundaries: after an Unknown SideEffect result, while Resume or Recover handles journal entries that remain Unknown, and before Done when uncertainty remains. `std.Verifier.ReconcileUnknown` currently discards journal results at `std/verify.go:39-40`; it only completes the original entry on success. The capability ports and journal argument retention are now decided by ADR-0157. This package owns the Verify integration gap and only `tools.verify-reconciles-unknown`; coverage of `tools.verify-on-recover`, `tools.verify-read-only`, and `tools.verify-error` is unchanged. Their scenarios remain restated as part of the modified Verification requirement.

## Goals / Non-Goals

**Goals:** Persist Succeeded and Failed outcomes, audit durable reconciliations, invoke Verify at each contract trigger, and preserve unresolved uncertainty.

**Non-Goals:** Re-execute a SideEffect, add audit kinds, edit normative capability specs, or add HTTP behavior or release tags.

## Decisions

### D1 — Verify API, journal arguments, and reconciliation

Keep the current public method signature:

```go
func (v *Verifier) ReconcileUnknown(ctx context.Context, sessionID, tool string, args json.RawMessage, verifyKey types.CallKey, entry stores.Entry) (types.ToolResult, bool, error)
```

The `stores.Journal` port changes under ADR-0157 decision 4, and the driver pins the two signatures: `Reserve(ctx context.Context, k types.CallKey, tool string, fp Fingerprint, args json.RawMessage) (Entry, bool, error)` retains the call's tool name and canonical argument bytes on the entry, `stores.Entry` gains `Tool string` and `Args json.RawMessage`, and the session lookup is `Unknown(ctx context.Context, sessionID string) ([]Entry, error)`, returning every entry whose result outcome is `Unknown` with both. ADR-0157 decision 10 pins the verifier port the driver binds: `types.Verifier` with `Reconcile(ctx, tool, args, key, res) (types.ToolResult, bool, error)`, resolved through the `WithVerifier` Build option and passed to each `Lifecycle` as a `LifecycleOption`. `std/journal.go` is the effect reservation adapter and supplies the call's tool name and canonical JSON arguments. Never infer either from projected history.

For `Send`, `core/drive_turn.go:batchEffect` consumes the Unknown scheduler result and obtains its original durable entry/arguments through the new journal lookup. It invokes the configured verifier without re-running the effect, then appends the reconciled result instead of the Unknown result. The current `batchEffect` implementation has no Verify callback or durable Unknown lookup; the concrete callback/field plumbing remains an implementation detail to pin against live `turnConfig` before implementation admission.

`ReconcileUnknown` calls `types.ToolSpec.Verify(ctx, args, entry.Result) (types.Outcome, error)` (`core/types/tool.go:99`). For either Succeeded or Failed, complete the `/verify` journal entry, then complete the original key with a copy of its result whose `Outcome` is the returned outcome. Failed preserves the recorded `ToolError` and changes only `Outcome`; success follows the existing contract for the verified result. A Verify error leaves the original entry Unknown. Reserve and Complete errors are returned. If either the journal completion or audit append fails, return that error and leave reconciliation unreported; the entry remains Unknown unless the original journal completion already persisted before audit failure. Do not report durable success when a required write fails.

The `/verify` key remains the original call ID plus `/verify`; its fingerprint remains `CanonicalFingerprint(tool+"/verify", args)`. Honor all Reserve results: propagate Reserve error, reuse the recorded outcome for an already-completed verify key, and invoke Verify only for a newly reserved key.

### D2 — Audit record and checksum

Use `core.AuditLogFrom(ctx) (stores.AuditLog, bool)` and append `stores.AuditToolOutcome` (`core/identity.go`; kind in `core/stores/audit.go:19-33`). Obtain session/run identity with `core.RunInfoFrom(ctx) (types.RunInfo, bool)`. Append after journal completion. Preserve confidentiality: do not record arguments or result content.

Set `AuditRecord.ResultSHA` to the SHA-256 of the canonical encoding of `types.Message{Blocks: result.Content}` and `ResultBytes` to the encoded byte count. This is the `stores` contract amended by ADR-0157 decision 5 and matches `core/stores/outputs.go:34-38`. Propagate audit append errors. The required context seam is already provided by `core.WithDecisionAudit` and `core.AuditLogFrom` (`core/session_takeover.go:40-54`); do not add a `Verifier` field or another option.

### D3 — Lifecycle, Resume, and Recover

Keep `WithLifecycleVerify(func(ctx context.Context, keys []types.CallKey) ([]types.CallKey, error)) LifecycleOption` (`core/drive_lifecycle.go:193-197`) as the lifecycle carrier. `Lifecycle.finishTurn` (`core/drive_lifecycle.go:664-714`) calls it before `Runs.Finish` and Done, removes only durably reconciled keys from the uncertainty list, and returns errors before Finish/Done.

The core-owned journal lookup from D1 supplies Unknown entries and original canonical arguments. The verifier callback uses that lookup and `ReconcileUnknown` for each key. `conversation.Resume` builds lifecycle options at `core/resume.go:395-422`; add `WithLifecycleVerify` there using its configured journal and tool specs. `Stack.driveRecovered` builds options at `core/recover.go:172-188` and rebuilds them for native runs at 233-235; add the same verifier option at both construction points, using recovered run identity and journal. Both paths execute through `DriveLifecycle` (`core/resume.go:422-423`, `core/recover.go:236-240`).

The live code does not yet expose a shared core-owned Verify callback factory or specify where Resume and Recover receive the journal and tool-spec lookup. Pin exact option plumbing and constructor signatures against the implementation branch before admission; this blocks implementation admission if the shared journal/API amendment does not supply it. Do not add a core-to-std dependency. Core may carry a callback typed in core and the std adapter may implement it.

### D4 — Uncertain error identity

`std.UncertainOutcomeError` becomes a type alias of `types.UncertainOutcomeError`, and `std.NewUncertainOutcomeError` returns that type (ADR-0157 decision 6). Preserve the existing `std` name and constructor. `types.UncertainOutcomeError` is defined in `core/types/errors.go:133-140`; `types.ProblemOf` already classifies it in `core/types/problem.go:307-310`. Do not create a second error representation or add a core-to-std import.

### D5 — Public test seeding and acceptance

Seed an Unknown journal reservation through the public Build-created native flow's journal, using the call key, canonical fingerprint, and canonical argument bytes supplied to Reserve. Register a tool whose `types.ToolSpec.Verify` inspects the original arguments and returns Succeeded or Failed. Exercise public Send, Resume, and Recover to reach their actual driver paths. Assert the effect executes once, Verify receives the retained arguments and recorded result, the journal outcome and Failed `ToolError` persist, audit kind/checksum/size are correct, and unresolved/error cases remain Unknown and appear in Done uncertainty. Use a failing journal and audit log to prove persistence errors surface. Matcher-only verifier tests do not prove public integration.

## Open Questions / Admission Blockers

1. Resolved by the driver: `Journal.Reserve` takes `args json.RawMessage`, `stores.Entry` gains `Args`, and `Journal.Unknown(ctx, sessionID)` lists the session's unknown entries with them. What remains is this package's own plumbing on the live path: the callback and the field that carry the verifier into `batchEffect`, `Resume`, `Recover` and `finishTurn`, and the configured-verifier binding at `Build`.
2. The live native flow does not yet expose the exact callback plumbing from `turnConfig`/`batchEffect` or journal/tool-spec dependencies in Resume/Recover. The implementation design must identify those exact fields and constructors after the journal port contract lands. This blocks implementation admission; do not infer arguments from history or add a core-to-std dependency.
3. Resolved: a reconciliation needs its audit record, so an unbound audit log is an error, not a silent no-audit path. `core.AuditLogFrom(ctx)` returns `(stores.AuditLog, bool)` (`core/session_takeover.go:52`); when the second value is false the reconciliation returns that error and leaves the entry `Unknown`, which is the same rule the `tools` requirement now states for a reconciliation whose audit record cannot be persisted. The implementation names the error it returns and asserts the branch in its test.

## Risks / Trade-offs

A journal error or audit error must surface instead of falsely clearing uncertainty. If original completion succeeds but audit append fails, the journal may be resolved while the caller receives an error; retry must recognize the completed state and finish the audit path without re-running the effect or Verify.

## Migration Plan

No data migration is required. Existing Unknown entries remain unresolved until a Verify-capable public path processes them. ADR-0157 authorizes the journal port change while the root remains pre-v1 through M2. This package does not edit normative specs or publish a release.
