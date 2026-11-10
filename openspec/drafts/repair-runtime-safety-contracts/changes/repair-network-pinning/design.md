# Design

## Context

This child implements parent decision 10 and owns final documentation acceptance for parent task 7.7.
The archived egress decision accepted proxy-side DNS.
The approved repair explicitly replaces that acceptance with checked-address proxy dialing.
These documents define intended behavior, not implementation evidence.

## Decisions

### Shared-address policy

Add the explicit `100.64.0.0/10` prefix to `DenyPrivate`.
Reject resolved peers in that range before dialing.
Preserve the existing exception for explicitly allowed literal addresses.
Test both range boundaries and existing allowed/private cases.

### Proxy destination pinning

For supported HTTP proxies, use the already validated target IP as the proxy destination.
Do not let proxy DNS replace the checked address.
Preserve original HTTP Host, TLS server name and certificate verification.
Preserve proxy credentials, per-response deadlines, redirect validation and response release.
Unsupported proxy modes fail before dispatch.
Do not invent additional supported modes beyond the approved HTTP proxy contract.

Use independent controlled client/proxy DNS results.
The positive control proves the allowed destination is reachable.
The divergent-DNS test proves the proxy cannot reach the denied peer through hostname substitution.
For HTTPS, assert the CONNECT destination address and the original TLS/HTTP authority separately.
Retain timeout and body-release assertions.

### Documentation reconciliation

Integrate the sandbox child's existing architecture and helper-cleanup updates first.
Update existing sandbox/egress architecture documentation and changelog entries.
State that checked-address proxy dialing supersedes the archived DNS-risk acceptance.
Keep archived historical records unchanged.
Verify trusted anchor cleanup, workload privilege isolation and immutable snapshot documentation against the sandbox outcome.
This child owns the combined parent 7.7 acceptance.
The sandbox child supplies evidence but does not duplicate primary ownership.

## Scope and Ordering

Integrate all four preceding children as declared by the manifest.
Preserve the authority child's `tools` requirements when adding the network requirements.
Preserve existing registry entries, shared docs, changelog and controlled Docker fixture changes.
The release child follows this child before final cross-module compatibility validation.
Parent integration checks shared outbound and sandbox behavior on the merged tree.

## Acceptance and Limits

Use controlled peers and resolvers, not public internet fixtures.
Prove positive allowed traffic and negative private/CGNAT traffic at the actual dialing boundary.
Verify both HTTP proxy routing and HTTPS authority preservation.
Retain literal exceptions, auth, redirect, deadline and response-release behavior.

Focused commands have a three-minute process deadline and a two-minute Go test timeout.
Full module/store commands have a twenty-minute process deadline and a ten-minute test timeout.
Use two package workers, two parallel tests and two lint workers.
Preserve configured cache and temporary build paths.
Allow one fixture repair and at most two further diagnostic reruns per boundary.
A flaky retry does not establish acceptance.
Stop for changed contracts, destructive operations, missing credentials, unresolved published dependencies or exhausted repair limits.
No third-party upgrade, publication, tag rewrite, production migration or research-report ownership change is authorized.
