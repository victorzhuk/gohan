# Spec Delta

This delta ADDS one requirement to the `permission` capability. It does not restate or replace the broad `Approval scope and requests` requirement or its scenarios, which stay with their current package; the new ids below are unused by another package.

## ADDED Requirements

### Requirement: Foreign tool approval reaches the gate on native terms

A tool call made by a foreign runtime MUST reach the gate on the same terms a native call does: hard blocks, taint policy, existing session grants and the decider MUST all run per call. `Ask` MUST suspend the run with one `ApprovalRequest` built from the foreign call's identity and arguments, and `Deny` MUST yield the `Failed(Permanent)` `not_executed:` result without running the tool.

#### Scenario: foreign ask suspends
ID: `permission.foreign-ask`
- WHEN a foreign run calls a `SideEffect` tool with no matching grant
- THEN the run suspends with one `ApprovalRequest` naming the call and, on `Resume(Approve())`, the call executes once

#### Scenario: foreign deny never runs the tool
ID: `permission.foreign-deny`
- WHEN the policy denies a foreign run's call
- THEN the tool implementation does not run, no journal entry is written, and the model receives a `not_executed:` result

### Requirement: Approved foreign call honours the session grant

An approved foreign call MUST store and honour the same session `Grant` a native call does, MUST execute once across the drive that suspended it and the resume that approved it, and MUST NOT charge its tool budget twice. The grant MUST be keyed on the verifying approving subject and never on the session originator, and its expiry MUST derive from the approval instant.

#### Scenario: foreign grant reused
ID: `permission.foreign-grant-reused`
- WHEN a foreign call is approved with `ApproveScope(2h)` by subject `op` and the model issues the same fingerprint again in the session under a fresh call id
- THEN the second call executes without suspension, the stored grant names subject `op` and the granted tool, and the audit records the scope grant

### Requirement: Scoped grant lifetime is the grant store's policy

A stored grant's lifetime MUST be capped by the bound grant store's policy and MUST NOT be set by a driver default. A scoped approval delivered with no bound grant store MUST fail closed instead of executing the approved call.

#### Scenario: scoped approval without a grant store fails closed
ID: `permission.foreign-grant-store-required`
- WHEN a resume delivers `ApproveScope(2h)` for a foreign call and the stack bound no grant store
- THEN the resume fails before the approved call runs and no grant is stored