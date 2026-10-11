# Spec Delta

## ADDED Requirements

### Requirement: Detached reissue timing and run-ID stability

For a `Detached` run, the harness MUST re-issue the credential through
`CredentialSource` before the first effect executes, and it MUST call the
source exactly once for the run. The run's ID MUST stay stable and MUST be
discoverable for the whole run lifetime, so a caller can capture the ID
before the run reaches a terminal state. A test harness MUST capture the
run ID before it releases any run gate and MUST NOT widen a timeout to
observe it.

#### Scenario: reissue precedes the first effect
ID: `identity.detached-reissue-before-first-effect`
- WHEN a `Detached` run starts and a `CredentialSource` is configured
- THEN the source is called once before the first model step and the fresh credential is visible on that step

#### Scenario: run id is captured before the gate opens
ID: `identity.detached-run-id-captured-before-gate`
- WHEN a detached-run test needs the run ID
- THEN it reads the ID through the handle before it closes the runtime gate, and no timeout is widened

#### Scenario: no credential reuse on detach
ID: `identity.detached-no-credential-reuse`
- WHEN a `Detached` flow starts with a stale `Credential` in the caller's context
- THEN the run observes the freshly issued token and never the caller's token
