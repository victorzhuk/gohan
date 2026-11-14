# Tasks

Risk depth: existing-service-strict (security boundaries). Scenario IDs are proposed at authoring and registered at admission (ADR-0171). A task is done only when its listed scenario IDs pass and `task lint`, `task spec` and `task test` are green. Bind each new scenario to a subtest named exactly by its ID: reuse an existing covering test where one exists, else write the focused enforcement test. Cite requirement headings, not line numbers.

## 1. Decision record

- [x] 1.1 Write `docs/adr/0188-governance-fail-closed.md` recording the fail-closed error contracts, the anchor-posture promotion, the literal-allow definition and the post-redaction taint-matching decision with the audit evidence.
  Acceptance: The ADR exists at the next free number after 0187 and states each decision with its threatened invariant.

## 2. Contracts

- [x] 2.1 Apply the permission delta and register `permission.decider-error-asks` (origin ADR-0188).
  Acceptance: The scenario passes as a subtest proving a decider error suspends with an ApprovalRequest and never widens to Allow.
- [x] 2.2 Apply the redaction delta (failure fails closed, telemetry/eval export carry no raw content, erasure covers notes), register `redaction.failure-fails-closed`, `redaction.telemetry-capture-redacted`, `redaction.eval-export-redacted`, `redaction.erasure-covers-notes`, and add notes to the existing erasure contract enumeration and scenario.
  Acceptance: All four scenarios pass as subtests; the erasure enumeration and its existing scenario name notes.
- [x] 2.3 Apply the sandbox delta (workload anchor posture) and register `sandbox.workload-posture-locked`, `sandbox.admin-channel-isolated`.
  Acceptance: Both scenarios pass as subtests in adapter/docker (they may skip without a Docker daemon only under the suite's existing named prerequisite pattern, and run in the full leg).
- [x] 2.4 Apply the tools delta (egress literal-allow exception) and register `tools.egress-glob-no-exempt`, `tools.egress-literal-cgnat-allowed`.
  Acceptance: Both scenarios pass as subtests proving exact-literal-only exemption and refusal before dialing.
- [x] 2.5 Apply the taint delta (taint survives content transforms, post-redaction matching) and register `taint.clear-tool-results-no-launder`, `taint.notes-readback-carries-origin`, `taint.window-truncated-reported`, `taint.matches-post-redaction`.
  Acceptance: All four scenarios pass as subtests; the matching-representation rule is stated in the taint Contract.
- [x] 2.6 Apply the guards delta (guard error fails closed) and register `guards.guard-error-fails-closed`.
  Acceptance: The scenario passes as a subtest proving a guard error blocks at its stage with *GuardBlockedError.
- [x] 2.7 Apply the identity delta (anonymous mode bypasses no authorization) and register `identity.anonymous-bypasses-no-authorization`.
  Acceptance: The scenario passes as a subtest proving an anonymous run is refused another owner's session.

## 3. Gate

- [x] 3.1 Run the change gate: `openspec validate governance-fail-closed --strict`, `task spec:types`, `task spec:coverage` and `task test`.
  Acceptance: Strict validation passes; the types index regenerates clean; every new scenario ID is registered and passing or carries the suite's named unmet-prerequisite skip; the short suite passes.