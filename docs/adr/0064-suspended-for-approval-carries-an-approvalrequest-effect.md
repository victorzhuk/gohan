# ADR-0064: `Suspended` for approval carries an `ApprovalRequest` (effect, risk, args and diff vs last approved fingerprint, reversibility, read-back availability, argument origins, consequence text); expiry has a default action (`RejectOnExpiry`); `MaxPendingApprovals` per subject and tenant; approval-rate metrics

Status: accepted · Origin: gohan-spec v0.13 decision D64

## Decision

`Suspended` for approval carries an `ApprovalRequest` (effect, risk, args and diff vs last approved fingerprint, reversibility, read-back availability, argument origins, consequence text); expiry has a default action (`RejectOnExpiry`); `MaxPendingApprovals` per subject and tenant; approval-rate metrics.

## Context and evidence

Reviewers need the context in the request; queues must not be floodable; > 90 % approval rate signals over-broad gates.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

Binding on the capability specs under `openspec/specs/`; changes require a new ADR that supersedes this one.
