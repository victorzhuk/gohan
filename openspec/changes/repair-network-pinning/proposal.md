# Proposal

## Why

`DenyPrivate` must cover shared-address destinations, and proxy routing must preserve the address the client validated.
Proxy-side DNS cannot substitute another destination after policy approval.

## What Changes

- Reject resolved `100.64.0.0/10` destinations under `DenyPrivate` before dialing.
- Preserve the existing explicit literal allow exception.
- Send validated destination IPs through supported HTTP proxy routes.
- Preserve HTTP authority, TLS identity, proxy auth, redirects, deadlines and response release.
- Refuse unsupported proxy modes before dispatch.
- Reconcile existing sandbox/egress docs and explicitly supersede the archived proxy DNS-risk acceptance.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `tools`: shared-address policy and checked-address proxy dialing.

## Impact

This child owns parent tasks 7.4, 7.5 and 7.7.
It changes `std/egress`, relevant HTTP tool regressions, existing architecture/compatibility/testing docs and changelog entries.
The sandbox child supplies its privilege, immutable-snapshot and helper-cleanup documentation for final reconciliation here.

## Acceptance

Controlled peers prove CGNAT rejection at both range boundaries before any connection opens.
Controlled divergent client/proxy DNS proves the checked address remains the actual peer.
HTTPS CONNECT uses that address while certificate verification and HTTP Host retain the original authority.
Existing literal exceptions, allowed destinations, redirect checks and release behavior remain correct.

## Boundaries

Integrate the preceding authority, recovery, provider and sandbox outcomes first.
Do not weaken deny policy or modify archived historical bytes to describe the new decision.
Do not upgrade dependencies, publish releases or alter independently owned research-report implementation.
