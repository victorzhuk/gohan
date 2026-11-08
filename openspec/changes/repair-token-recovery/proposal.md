# Proposal

## Why

Lease acquisition, checkpoint consumption and recovery discovery must agree on one current suspension and session owner.
Recovered output and cumulative tool accounting must survive crashes through the same public boundaries as live execution.

## What Changes

- Enforce session exclusivity and stored-state checks for every lease acquisition.
- Use PostgreSQL database time for production ownership and expiry.
- Add the optional token-fenced recovery surface without changing frozen methods.
- Separate checkpoint TTL from execution leases with explicit store options.
- Keep conditional consumption, eligible discovery and public resume in one outcome.
- Persist recovered events before delivery and retain cumulative tool admissions exactly once.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `stores`: ownership, time, checkpoint policy, token fencing and discovery.
- `recovery`: conditional consumption, optional support and durable recovered output.
- `runtime`: terminal persistence and cumulative tool admission accounting.

## Impact

This child owns parent tasks 4.1-4.8 and 5.1-5.6.
Its scope includes concrete memory/PostgreSQL stores, public resume/recovery, lifecycle helpers, storetest factories and shipped memory wiring.
It updates existing architecture, lifecycle, compatibility and testing docs and changelog entries.

## Acceptance

Real-store conformance proves exclusive acquisitions, stale-generation refusal and one token-fenced winner.
Public Resume and recovery preserve pre-consumption tokens and recover recorded post-consumption decisions.
A fresh conversation attaches to the recovered tail and observes exactly one terminal boundary.
Send, approval Resume and crash recovery retain cumulative tool budgets without charging pending prepaid calls again.

## Boundaries

Integrate `repair-call-authority` first.
No DDL migration, frozen method change, root dependency upgrade or research-report implementation edit belongs here.
The release child later verifies PostgreSQL against its declared published root without referencing an unpublished interface assertion.
