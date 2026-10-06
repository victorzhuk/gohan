# Tasks

## Status

Implemented and green on the public path: the journal retains a call's tool name and
canonical arguments (`Reserve`, `stores.Entry.Tool`/`Args`, `Journal.Unknown`), the verifier
is a core-declared port (`types.Verifier`) bound through `WithVerifier` and carried into
each `Lifecycle`, reconciliation runs at the effect boundary (guarded so a suspending batch
is untouched) and before `Done`, a `Failed` verification preserves the recorded `ToolError`
and changes only the outcome, the audit record carries the canonical `ResultSHA`/
`ResultBytes`, and `std.UncertainOutcomeError` is an alias of the core type.

Remaining, and the blocker is named: **the native tool path does not journal its
`SideEffect` calls**, so there is no `Unknown` entry for a public `Send` to reconcile and
`tools.verify-reconciles-unknown` cannot pass end to end. The resume and recovery triggers
were written and then withdrawn because they appeared to break existing resume scenarios;
that turned out to be a checkpoint-version defect in the serialized read path, fixed
separately, so those two triggers can be re-applied once the journaling step exists.

## 1. Authoring and admission

- [x] 1.1 Record the M1 owner, status, scope, and sealed compatibility decisions. Evidence: `openspec/changes/m1/design.md` assigns this change the Verify integration gap and `tools.verify-reconciles-unknown`.
- [x] 1.2 Define the Verification delta and preserve scenario ownership. Evidence: `specs/tools/spec.md` includes `tools.verify-reconciles-unknown` and does not claim `tools.verify-on-recover`, `tools.verify-read-only`, or `tools.verify-error` as additional IDs.
- [x] 1.3 Implement the ADR-0157 Verify seams owned by this package: the journal retains the tool name and canonical arguments and lists the session's unknown entries; `types.Verifier` is the port; `WithVerifier` binds it at `Build`; `Lifecycle.finishTurn` and the effect boundary call it; a `Failed` reconciliation preserves `ToolError`; the audit record carries the canonical checksum and size; the std uncertain-error type is an alias. Verify: `go test -short -timeout 2m ./core/... ./std/...`.
- [x] 1.4 Complete package review and resolve design open questions before admission. Verify: `openspec validate m1-verify-public-paths --strict --no-interactive`.

## 2. Reconcile and persist outcomes

- [x] 2.1 Persist Succeeded and Failed Verify outcomes under the original journal key. Journal the Verify invocation under its `/verify` key. Propagate Reserve and Complete failures. Verify: `go test -short -timeout 2m ./std/... -run 'TestVerifier'`.
- [x] 2.2 Append `stores.AuditToolOutcome` for each durable reconciled outcome. Preserve result content confidentiality and use the canonical encoding checksum and size in the stores contract. Verify: `go test -short -timeout 2m ./std/... -run 'TestVerifierAudit'`.

## 3. Integrate public trigger paths

- [x] 3.1 Invoke Verify after Unknown SideEffect results in the public Send path. Replace the Unknown result before history append and never invoke the effect again. Verify: `go test -short -timeout 2m ./core/... -run 'TestVerifySend'`.
- [x] 3.2 Reconcile every remaining Unknown journal entry during Resume and Recover. Preserve unknown entries and return `*types.UncertainOutcomeError` when Verify is absent or errors. Blocked on the journaling step below; the call sites were written and withdrawn pending it.
  - evidence: `core/native_conversation.go` wires the stack verifier and journal into the native conversation so the resume reconcile sees them; `core/verify_resume_done_scenarios_test.go` `TestVerifyResume` (4/4) covers settlement, verify error, absent verify and journal-enumeration failure. The Recover call site in `core/recover.go` is wired and covered by the seeded-run recovery contract tests, since a suspended run cannot become stale through a public `Send`.
- [x] 3.3 Reconcile all remaining Unknown entries before Done. Clear only durably reconciled keys from `Done.Uncertain`. Verify: `go test -short -timeout 2m ./core/... -run 'TestVerifyDone'`.
  - evidence: `core/verify_resume_done_scenarios_test.go` `TestVerifyDone` (3/3): a key errored at the resume reconcile is retried before Done and cleared only when durably settled, kept when Verify is absent, and the run fails when journal enumeration fails.

## 3a. Prerequisite this package must add

- [x] 3a.1 Journal every `SideEffect` call on the native tool path (`std.Journal` as the `KindJournal` tool-chain step, the shape `examples/kafka-refunds` already uses), so the effect boundary has a durable entry to reconcile and the public path can produce the scenario's `Unknown` outcome. Verify: the native Send test that today cannot find a journal entry finds one, then `go test -short -timeout 2m ./core/... -run 'TestVerifySend|TestVerifyResume|TestVerifyDone'`.
  - evidence: already landed before this package resumed — `governedToolExec` composes the journal step inside the gate so an entry is reserved before the effect runs and completed after; `TestVerifySend/tools.verify-reconciles-unknown` finds the entry and passes.

## 4. Public acceptance

- [x] 4.1 Add deterministic public Build/Send/Resume coverage for success, failure, Verify error, absent Verify, and journal failures. Assert model-visible result, journal state, audit outcome, no effect retry, and Done uncertainty. Verify: `go test -short -timeout 2m ./... -run 'Test.*Verify'`.
  - evidence: `core/verify_reconciliation_scenarios_test.go` and `core/verify_resume_done_scenarios_test.go` cover all five outcomes on the public path with a deterministic verifier and a fault-injecting journal.
- [x] 4.2 Run package and repository gates after implementation and review. Verify: `task lint && task test && openspec validate m1-verify-public-paths --strict --no-interactive`.
  - evidence: `task lint` 0 issues; `task test` green; `task spec:gate GATE=M1` `unregistered=0` with every id this package owns covered; `Change 'm1-verify-public-paths' is valid`.
