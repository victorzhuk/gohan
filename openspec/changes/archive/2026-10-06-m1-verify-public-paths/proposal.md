# Proposal: m1-verify-public-paths

## Why

The `Verify` declaration exists, and `std.Verifier.ReconcileUnknown` handles only `Succeeded`. It discards errors from the verify-key `Journal.Reserve` and `Journal.Complete`, writes no audit record, and has no call from the turn, resume, recovery, or Done paths. A failed reconciliation therefore cannot reach the journal or model as the true outcome. The M1 matrix assigns `tools.verify-reconciles-unknown` and the end-to-end Verify obligation to this change as an integration gap (`openspec/changes/m1/design.md:70,197-205`).

## What Changes

- Reconcile both `Succeeded` and `Failed` Verify outcomes into the original journal entry, including the verified result.
- Journal every Verify invocation under its `CallKey` with `"/verify"` and surface every Reserve or Complete error.
- Audit reconciled outcomes with `stores.AuditToolOutcome`, including `Outcome: Succeeded` or `Outcome: Failed`.
- Connect reconciliation to the tool-result, Resume/Recover, and pre-Done paths without re-executing the effect.
- Preserve uncertainty when Verify is absent or returns an error. Return `*std.UncertainOutcomeError` for those unresolved entries.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `tools`: complete the public Verification requirement and `tools.verify-reconciles-unknown` scenario.

## Impact

The change affects `std/verify.go` and its callers, lifecycle wiring in `core/drive_lifecycle.go`, and Resume/Recover orchestration in `core/resume.go` and `core/recover.go`. It adds no capability outside `tools`, no new audit kind, no HTTP transport, and no release tag.