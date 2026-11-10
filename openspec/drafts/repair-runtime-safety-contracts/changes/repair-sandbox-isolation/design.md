# Design

## Context

This child implements parent decision 9 and the controlled Docker fixture repair from decision 12.
Policy enforcement must precede all untrusted restored workload execution.
These documents define intended behavior, not current source implementation evidence.

## Decisions

### Trusted setup before workload execution

Start a trusted namespace anchor from the configured trusted base image.
Never start the anchor from a restored workspace snapshot.
Keep its root filesystem separate from the workload.
The anchor owns `NET_ADMIN` for policy setup and counter reads.
Establish the complete policy before starting any workload process.

The workload joins the anchor's network namespace.
Drop all workload capabilities and enable `no-new-privileges`.
Override the image entrypoint and disable inherited healthchecks.
Preserve existing UID and file API semantics.
A restored snapshot can replace an idle executable or loader.
Entrypoint override alone therefore cannot establish a pre-policy execution barrier.
Never administer policy through public workload `Exec`.
Bound each administration operation to ten seconds.
Deny-all retains `NetworkMode: none` and needs no anchor.

### Resource ownership and cleanup

Label anchors and associate each anchor with its workload resource.
Close and failed setup remove both resources.
Enumeration and crash cleanup recognize orphaned anchors.
Preserve anchors that serve live workloads.
Setup failure returns no usable unprotected workspace.
Tests must assert cleanup under partial setup failure, not merely an error result.

### Immutable publication

Return the immutable image ID from `ContainerCommit`.
Enumerate and remove labelled immutable snapshot IDs as well as existing labelled tagged snapshots.
Two publications from one live workspace must not change content behind the earlier reference.
A metadata compare-and-set failure must preserve prior committed content.
Keep legacy restore and close-by-ref behavior without retagging or deleting another snapshot.

### Controlled network fixtures

Replace public DNS in the standalone Docker allowlist test with existing isolated fixture infrastructure.
Prove both allowed and denied outcomes without public resolvers or internet listeners.
Do not treat capability regain or firewall mutation as successful policy enforcement.

## Scope and Ordering

Integrate authority, recovery and provider outcomes before this child.
The manifest orders shared root, store, testkit and documentation contracts explicitly.
Preserve recovery workspace-owner time and metadata behavior.
The network child integrates this child before reconciling parent task 7.7.
This child supplies the sandbox architecture, privilege split and helper-cleanup text.
The network child owns the final combined sandbox/egress documentation acceptance.

## Acceptance

Use public Sandbox and Workspace APIs with real controlled Docker peers.
Attempt firewall removal and capability regain from workload execution.
Verify policy with an allowed positive control and denied peer assertions.
Inject setup failures and inspect all created resource classes.
Restore a snapshot containing a modified startup executable and inherited healthcheck.
Verify no workload process executes before trusted policy setup.
Assert unchanged UID and file behavior.
Publish and restore two snapshots from one workspace.
Inject a later metadata publication failure and restore the earlier committed reference.
Exercise labelled immutable and legacy enumeration and cleanup.

## Limits and Compatibility

No third-party upgrades, frozen port changes, production migrations, tag rewrites or publication are authorized.
Preserve independently owned research-report edits.
Update existing sandbox architecture docs and changelog entries before handing final reconciliation to the network child.

Focused commands have a three-minute process deadline and a two-minute Go test timeout.
Full module/store commands have a twenty-minute process deadline and a ten-minute test timeout.
Use two package workers, two parallel tests and two lint workers.
Preserve configured cache and temporary build paths.
Allow one fixture repair and at most two further diagnostic reruns per boundary.
Do not count a flaky retry as acceptance.
Stop for changed contracts, destructive operations, missing credentials, unresolved published dependencies or exhausted repair limits.
Parent integration retains failure cleanup, race and broad gate acceptance.
