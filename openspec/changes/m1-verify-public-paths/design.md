# Design: m1-verify-public-paths

## Context

The tools contract assigns Verify to three driver boundaries: immediately after an Unknown SideEffect result, while Resume or Recover handles journal entries that remain Unknown, and before Done when uncertainty remains. `std.Verifier.ReconcileUnknown` currently implements only the success case. Its verify-key journal errors are ignored, and the main entry's result is not replaced for a Failed outcome. It also has no audit dependency. `core.Lifecycle.finishTurn` already injects a verifier callback before `Runs.Finish`, but no live setup connects it to `std.Verifier`.

The M1 acceptance matrix owns only `tools.verify-reconciles-unknown` and the obligation “Verify end-to-end (Failed reconciliation, audit, lifecycle execution).” The sibling IDs `tools.verify-on-recover`, `tools.verify-read-only`, and `tools.verify-error` remain owned by the tools Verification requirement and are not claimed as additional scenario IDs here.

## Goals / Non-Goals

**Goals:** Persist true successful and failed outcomes; record reconciliations in the audit log; invoke Verify at each contract trigger; preserve and report unresolved uncertainty.

**Non-Goals:** Re-execute a SideEffect, add audit kinds, change the `Verify` signature, weaken journal idempotency, or edit the normative capability spec in this authoring package.

## Decisions

### D1 — Failed reconciliation and journal ordering

Keep the exported verifier method signature:

```go
func (v *Verifier) ReconcileUnknown(ctx context.Context, sessionID, tool string, args json.RawMessage, verifyKey types.CallKey, entry stores.Entry) (types.ToolResult, bool, error)
```

After `spec.Verify(ctx, args, entry.Result)` returns `types.Succeeded` or `types.Failed`, complete the `/verify` journal entry first, then complete the original journal key with a copy of `entry.Result` whose `Outcome` is the returned outcome. Preserve the effect's recorded result payload and clear its error only for success; for failure, preserve the Verify failure details in the recorded result using the existing `types.ToolResult` and `types.ToolError` representation. The exact mapping from the current `Verify` return type (`types.Outcome`) to a failure message is not present in the live signature. This is an admission blocker: define a deterministic failed-result mapping in the implementation plan before implementation. Do not invent a new public result or error identifier in the package plan.

The Verify journal key is the supplied `verifyKey`, whose call ID is the original call ID plus `"/verify"`. Reserve it with `std.CanonicalFingerprint(tool+"/verify", args)`. Handle all three `Journal.Reserve` results: return a Reserve error; if the key is already completed, use its stored outcome instead of invoking Verify again; otherwise invoke Verify and propagate `Journal.Complete` errors. Then complete the original key. Never discard a Reserve or Complete error. If persistence fails, return the store error and do not report reconciliation as successful.

### D2 — Audit record and failures

Use the existing `stores.AuditToolOutcome` kind. The normative audit kinds in `openspec/specs/stores/spec.md` include `AuditToolOutcome`, and `stores.AuditRecord` carries `SessionID`, `RunID`, `Tool`, `Outcome`, `ResultSHA`, and `ResultBytes`. Append one record for the reconciled original outcome after its journal completion. Record the actual `Succeeded` or `Failed` outcome. Do not add an audit kind or record Verify arguments or result content. The exact `ResultSHA` encoding is not specified by a production writer and remains an admission blocker.

`Verifier` has no audit field, but context already carries the required seam. The `WithDecisionAudit` and `AuditLogFrom` functions described below remove the need for a new option or field. Audit payload hashing still needs an implementation contract because the repository has no production tool-outcome writer.

The live signature for the tool declaration is `types.ToolSpec.Verify func(ctx context.Context, args json.RawMessage, r types.ToolResult) (types.Outcome, error)` (`core/types/tool.go:99`). The exported method remains `ReconcileUnknown` with the exact signature above. A Failed outcome supplies no failure detail beyond the existing Unknown `ToolResult`; retaining its `types.ToolError` is therefore required unless a separate normative decision changes this signature. The implementation must not fabricate a Verify error message.

The audit injection seam already exists. `core.WithDecisionAudit(ctx, stores.AuditLog)` binds the log, `core.AuditLogFrom(ctx) (stores.AuditLog, bool)` retrieves it, and `core.RunInfoFrom(ctx) (types.RunInfo, bool)` supplies session and run IDs (`core/session_takeover.go:40-54`, `core/identity.go:32-34`). No new option or `Verifier` field is required. `std.Verifier` can append through the bound context. Use `stores.AuditToolOutcome` from `core/stores/audit.go:19-33` and `openspec/specs/stores/spec.md:265-300`. Append errors must be returned. The existing codebase has no production AuditToolOutcome writer; the exact `ResultSHA` encoding is not established by a live writer, so pin it against the store audit contract before implementation admission.

The journal's `ByFingerprint(ctx, sessionID, fp)` can enumerate entries by fingerprint, but the exact fingerprint scheme for locating original Unknown calls and their serialized arguments is not specified by the live `std.Verifier` API. The implementation plan must pin that scheme and its caller. Do not treat a synthetic result as proof or derive arguments from a projected model message.

The exact public driver sites are:

- `core/drive_turn.go:batchEffect`, after `runtime.Batch.Run` and before aligned results are appended to history. It has `calls`, `report.Results`, each call's `Effect`, arguments, and result. It currently has no Verify callback, and the scheduler-produced `Unknown` result is not directly journal-enumerated there. The implementation must resolve how to obtain the durable original journal entry and construct the `/verify` key without re-executing the effect. The exact turn wiring site and journal lookup are not present in inspected live code. This is an admission blocker.
- `core/drive_lifecycle.go:Lifecycle.finishTurn`, which calls `lc.verify(ctx, unknown)` before `lc.finishRun` and `lc.done`. Its existing injection point is `WithLifecycleVerify(func(ctx context.Context, keys []types.CallKey) ([]types.CallKey, error))`. Preserve the order and return errors before Finish or Done.
- `core/resume.go:conversation.Resume`, lines 395-422, constructs `NewLifecycle(opts...)` without `WithLifecycleVerify`. Resume then drives that lifecycle at lines 422-423. Add the verifier at this option assembly point only after its journal entry and original-arguments source is pinned; neither is currently supplied here.
- `core/recover.go:Stack.driveRecovered`, lines 172-188, also constructs `NewLifecycle(opts...)` without `WithLifecycleVerify`; `DriveLifecycle` runs at lines 236-240. Add the same verifier there after determining how recovered original arguments and journal entries are found.

The journal exposes `ByFingerprint(ctx, sessionID, fp)`, but `Verifier` has no method to enumerate all Unknown entries or decode original arguments. The exact fingerprint and entry lookup contract for the three triggers is unresolved. Do not claim full Resume/Recover integration until this is specified.

An error returned by `Verify` currently passes directly through `ReconcileUnknown`. The public boundary must retain the key as Uncertain and wrap the failure in `*std.UncertainOutcomeError`, whose live shape is `Out any, Keys []types.CallKey` (`std/uncertainty.go:11-30`). Neither Resume nor Recover currently has an import or injection path to construct that error. Pin a non-cyclic boundary adapter before implementation admission; do not add a core-to-std dependency.

### D5 — Matrix and sealed decisions

The owner and scope remain those in `openspec/changes/m1/design.md:70,197-205`; do not claim sibling scenario IDs. Preserve the M1 compatibility schedule: root stays pre-v1 through M2; do not add HTTP API comparisons, server behavior, or release tags. No shared M1 design or main capability spec edits are part of this package. No normative capability change is planned. If implementation research establishes that the tools contract itself must change, stop and return the amendment and ADR requirement to the milestone driver.

## Risks / Trade-offs

- A journal error after Verify but before original-key completion leaves an unresolved entry. Surface the error and retain uncertainty rather than claiming a reconciliation that was not durably recorded.
- The `Verifier` needs no new audit field because the run context exposes `AuditLogFrom`. Pin the result checksum encoding before implementation admission.
- Resume and Recover do not expose verifier setup in the inspected code. Resolve their exact wiring before implementation admission; do not claim coverage from Done-only callback tests.

## Migration Plan

No data migration is specified. Existing Unknown entries remain unresolved until a Verify-capable public path processes them. No normative spec, ADR, types-index, or release change is authorized in this package.