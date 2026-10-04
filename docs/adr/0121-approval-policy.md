# ADR-0121: Approval eligibility by risk tier — separate identity, scopes, quorum, escalation

Status: accepted · Origin: grill round 47 (2026-09-30) · Amends ADR-0103

## Decision

`Resume` evaluates an `ApprovalPolicy` per `RiskTier` before accepting an approval: low risk — owner or `session:write`; medium — owner or `approve:<tool>`; high — `approve:<tool>` and a subject distinct from the originator and the calling principal, quorum 1 (2 when irreversible). Ineligible approvals return `ErrApproverNotEligible` and leave the token unconsumed. `ApprovalRequest.Eligible` tells transports where to route; `EscalateOnExpiry` walks the policy's escalation scopes. Audit and journal record `Approver` plus `ApprovedVia{Scope, Policy, ActingFor}`; grants inherit the tier's eligibility.

## Context and evidence

OWASP's agentic top-10 lists identity and privilege abuse and human-agent trust exploitation, recommending explicit multi-step approval for high-impact actions; identity guidance for agents insists that the proposer and the approver be distinct identities, approvals be risk-tiered and the path be verifiable in the audit trail. gohan accepted any tenant principal with `session:write` holding the token as approver, which let the originating user or the calling service approve its own high-risk actions.

## Consequences

`permission` v1.2 (`Eligibility`, `ApprovalPolicy`, `ApprovedVia`, `ErrApproverNotEligible`, rule block, seven scenarios), `identity` rule 5 wording; task 14.
