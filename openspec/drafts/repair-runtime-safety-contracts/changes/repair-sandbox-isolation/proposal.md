# Proposal

## Why

Untrusted Docker workloads must not administer network policy or execute restored startup content before trusted setup.
Snapshot references must identify immutable content across later publications and failed metadata updates.

## What Changes

- Establish network policy in a labelled trusted namespace anchor before workload startup.
- Remove workload capabilities, enable `no-new-privileges` and disable inherited healthchecks.
- Keep network administration and counter reads outside public workload `Exec`.
- Preserve UID and file behavior while cleaning anchors and workloads after failures.
- Publish immutable image IDs and retain labelled legacy snapshot reads and cleanup.
- Replace public DNS in the standalone Docker allowlist test with controlled fixtures.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `sandbox`: policy authority isolation, setup cleanup and immutable snapshot publication.

## Impact

This child owns parent tasks 7.1-7.3 and 7.6.
It changes the Docker adapter, relevant sandbox/lifecycle integration and controlled fixtures.
It supplies sandbox architecture and helper-cleanup documentation for parent task 7.7, whose primary owner is the network child.

## Acceptance

Public Sandbox and Workspace APIs prove denied capability regain and firewall mutation.
A controlled allowed peer remains reachable while a denied peer remains unreachable.
A tampered restored startup executable and inherited healthcheck cannot run before policy establishment.
Snapshots A and B retain distinct contents, including after a later metadata compare-and-set failure.
Legacy labelled tagged references remain readable and removable.

## Boundaries

Integrate preceding authority, recovery and provider outcomes first.
Do not change UID/file semantics, publish releases, run production migrations or upgrade Docker dependencies.
Do not alter independently owned research-report implementation.
The network child owns checked-address proxy routing and final sandbox/egress documentation reconciliation.
