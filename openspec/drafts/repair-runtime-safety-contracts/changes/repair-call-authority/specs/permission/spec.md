## ADDED Requirements

### Requirement: Ordinary approval is bound to one complete call

An ordinary approval MUST authorize only its recorded session, run, argument generation, call ID, tool and complete argument value.
An ordinary receipt MUST NOT become a reusable session grant.
A scoped grant MUST retain its subject and lifetime checks.

#### Scenario: A later call cannot reuse an ordinary approval
ID: `permission.ordinary-approval-call-bound`
- WHEN a call was approved and a later call has another ID or run with the same fingerprint
- THEN the later call requires its own decision and no side effect executes from the earlier approval

#### Scenario: A non-fingerprinted argument changes
ID: `permission.approval-full-arguments-bound`
- WHEN an approved pending call changes an argument outside its fingerprint fields
- THEN its ordinary approval does not authorize the changed call

### Requirement: Current approval policy governs every counted approval

Every approval counted toward quorum MUST satisfy the current policy and recorded request restrictions.
A stricter scope, separation rule or exclusion MUST apply to already collected approvals before execution.
Refusal MUST preserve the pending token.

#### Scenario: Required scope tightens while approval is pending
ID: `permission.tightened-scope-refused`
- WHEN the current policy requires a scope the approving principal lacks
- THEN the decision is refused and the token remains unconsumed

#### Scenario: Earlier quorum votes no longer qualify
ID: `permission.tightened-votes-revalidated`
- WHEN the policy changes after an earlier vote and that vote no longer qualifies
- THEN that vote does not complete quorum and the call does not execute

### Requirement: Legacy rounded grants do not prove exact authority

A grant with a lossy argument fingerprint MUST NOT authorize a call under the exact-value fingerprint format.
Ordinary receipts with complete argument evidence MUST remain readable for their original call and argument history.

#### Scenario: A legacy grant encounters the exact-value format
ID: `permission.legacy-rounded-grant-refused`
- WHEN a later call finds only a grant recorded with the legacy fingerprint format
- THEN the caller must obtain a new approval before execution
