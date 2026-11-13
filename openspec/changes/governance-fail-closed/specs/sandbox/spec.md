# sandbox delta

## ADDED Requirements

### Requirement: Workload anchor posture

A networked workload MUST drop all capabilities, add none and enable no-new-privileges; its image entrypoint MUST be overridden and inherited healthchecks disabled. Network policy administration MUST target the anchor, never public `Workspace.Exec`, and each administration operation MUST be bounded to ten seconds.

#### Scenario: workload posture locked
ID: `sandbox.workload-posture-locked`
- WHEN a networked workload starts from a trusted base image
- THEN it runs with zero capabilities, no-new-privileges and an overridden entrypoint, and a `Workspace.Exec` request cannot administer network policy

#### Scenario: administration channel isolated and bounded
ID: `sandbox.admin-channel-isolated`
- WHEN a private administration operation on the anchor exceeds ten seconds
- THEN the operation is cancelled, the failure stays visible and retryable, and no workload process started before policy setup completed