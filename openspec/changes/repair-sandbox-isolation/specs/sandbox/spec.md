## ADDED Requirements

### Requirement: Workload execution cannot administer network enforcement

Untrusted workspace execution MUST have no authority to change its network enforcement or regain that authority.
Trusted policy setup MUST complete before the workspace becomes usable.
Failed setup MUST return no usable workspace and MUST clean its created resources.

#### Scenario: Workload code attempts to remove the firewall
ID: `sandbox.firewall-authority-isolated`
- WHEN workspace execution attempts firewall removal or capability regain
- THEN the attempt fails, an allowed controlled peer remains reachable, and a denied controlled peer remains unreachable

#### Scenario: Privileged setup fails
ID: `sandbox.policy-setup-failure-cleanup`
- WHEN trusted network policy setup fails
- THEN opening the workspace fails and no usable unprotected workspace or setup resource is left behind

#### Scenario: A restored snapshot replaces its startup executable
ID: `sandbox.restored-startup-policy-first`
- WHEN a restored workspace snapshot contains a modified startup executable or an inherited healthcheck
- THEN no workload process executes before policy is established and inherited healthchecks do not run

### Requirement: Snapshot references identify immutable content

A successful snapshot reference MUST retain its content across later publications from the same workspace.
A failed publication or metadata compare-and-set MUST NOT change content behind an earlier committed reference.
Existing labelled snapshot references MUST remain readable and removable.

#### Scenario: One live workspace publishes twice
ID: `sandbox.same-workspace-snapshots-immutable`
- WHEN a workspace publishes A, changes a file, then publishes B
- THEN restoring A and B yields their respective file contents

#### Scenario: A later metadata publication loses its compare-and-set
ID: `sandbox.snapshot-cas-failure-keeps-content`
- WHEN a later snapshot publication fails its metadata compare-and-set
- THEN restoring the previously committed reference yields its original content

#### Scenario: A legacy tagged snapshot is restored
ID: `sandbox.legacy-snapshot-ref-readable`
- WHEN an existing labelled tagged snapshot reference is opened or removed
- THEN restore and cleanup remain supported without retagging or deleting another snapshot
