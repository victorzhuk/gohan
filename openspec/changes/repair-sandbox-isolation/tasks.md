# Tasks

Use the existing-service-strict contract and limits in `design.md`.

## 0. Local pre-fix evidence

- [x] 0.1 Preserve existing Docker baseline checks before local repairs. Record pre-fix behavior failures through public Sandbox and Workspace boundaries using controlled attack, setup-failure and snapshot fixtures. Include source revision, bounded command, scenario, expected behavior and failing assertion. Reject compilation, missing-command and infrastructure errors as reproductions. Supply this evidence to integration-owned parent 1.3.

## 1. Sandbox authority and snapshots

- [x] 1.1 Add controlled Docker attack and setup-failure scenarios through Sandbox and Workspace APIs. Ensure capability regain or firewall mutation cannot count as policy enforcement. Parent 7.1.
- [x] 1.2 Establish policy in a labelled trusted namespace anchor before workload startup. Keep administration/counter reads outside workload Exec. Drop capabilities, enable `no-new-privileges` and disable inherited healthchecks. Verify `sandbox.firewall-authority-isolated`, `sandbox.policy-setup-failure-cleanup` and `sandbox.restored-startup-policy-first`. Include a tampered restored executable and unchanged UID/file behavior. Parent 7.2.
- [x] 1.3 Publish immutable image IDs. Support labelled immutable/legacy resources in enumeration and cleanup. Verify `sandbox.same-workspace-snapshots-immutable`, `sandbox.snapshot-cas-failure-keeps-content` and `sandbox.legacy-snapshot-ref-readable`. Parent 7.3.
- [x] 1.4 Replace public DNS in the standalone Docker allowlist test with existing isolated fixtures. Verify allowed and denied outcomes without public resolvers or internet listeners. Parent 7.6.

## 2. Documentation and child acceptance

- [x] 2.1 Update existing sandbox architecture docs and changelog for the trusted setup boundary, immutable snapshots and helper cleanup. Supply this supporting evidence to the network child's primary acceptance of parent 7.7.
- [x] 2.2 Run all assigned public scenarios and enumeration/cleanup regressions within the design bounds. Include positive allowed-peer controls and injected failure cleanup.
- [x] 2.3 Supply relevant sandbox/store lifecycle race evidence for parent integration. Preserve frozen APIs and independently owned research-report edits.
