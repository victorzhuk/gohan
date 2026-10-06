# Tasks

## 1. Authoring and admission

- [x] 1.1 Record the M1 owner, status, scope, and sealed compatibility decisions. Evidence: `openspec/changes/m1/design.md:70,197-205,230-249` assigns this change the Verify integration gap and scenario ID.
- [x] 1.2 Define the Verification delta and preserve scenario ownership. Evidence: `specs/tools/spec.md` includes `tools.verify-reconciles-unknown` and does not claim `tools.verify-on-recover`, `tools.verify-read-only`, or `tools.verify-error` as additional IDs.
- [x] 1.3 Trace live Verify, journal, audit, lifecycle, Resume, Recover, and test seams. Evidence: `design.md` names `std.Verifier.ReconcileUnknown`, `core/drive_turn.go:batchEffect`, `core/drive_lifecycle.go:Lifecycle.finishTurn`, `core/resume.go:conversation.Resume`, `core/recover.go:Stack.driveRecovered`, and records unresolved identifiers as admission blockers.
- [ ] 1.4 Resolve all admission blockers against live code and review the completed package. Verify: `openspec validate m1-verify-public-paths --strict --no-interactive`.

## 2. Reconcile and persist outcomes

- [ ] 2.1 Persist both Succeeded and Failed Verify outcomes under the original journal key. Journal the Verify invocation under its `/verify` key. Propagate Reserve and Complete failures. Verify: `go test -short -timeout 2m ./std/... -run 'TestVerifier'`.
- [ ] 2.2 Append `stores.AuditToolOutcome` for each durable reconciled outcome. Preserve result content confidentiality and use existing checksum and size fields. Verify: `go test -short -timeout 2m ./std/... -run 'TestVerifierAudit'`.

## 3. Integrate public trigger paths

- [ ] 3.1 Invoke Verify after Unknown SideEffect results in the public Send path. Replace the Unknown result before history append and never invoke the effect again. Verify: `go test -short -timeout 2m ./core/... -run 'TestVerifySend'`.
- [ ] 3.2 Reconcile every remaining Unknown journal entry during Resume and Recover. Preserve unknown entries and return `*std.UncertainOutcomeError` when Verify is absent or errors. Verify: `go test -short -timeout 2m ./core/... -run 'TestVerifyResume|TestVerifyRecover'`.
- [ ] 3.3 Reconcile all remaining Unknown entries before Done. Clear only durably reconciled keys from `Done.Uncertain`. Verify: `go test -short -timeout 2m ./core/... -run 'TestVerifyDone'`.

## 4. Public acceptance

- [ ] 4.1 Add deterministic public Build/Send/Resume coverage for success, failure, Verify error, absent Verify, and journal failures. Assert model-visible result, journal state, audit outcome, no effect retry, and Done uncertainty. Verify: `go test -short -timeout 2m ./... -run 'Test.*Verify'`.
- [ ] 4.2 Run package and repository gates after implementation and review. Verify: `task lint && task test && openspec validate m1-verify-public-paths --strict --no-interactive`.