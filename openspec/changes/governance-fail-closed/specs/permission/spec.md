# permission delta

## ADDED Requirements

### Requirement: Decider error fails closed

A decider error MUST resolve to `Ask`: the gate suspends the call with an `ApprovalRequest` and MUST NOT widen to `Allow`. A gate without a decider keeps the effect defaults.

#### Scenario: decider error asks
ID: `permission.decider-error-asks`
- WHEN the decider returns an error for a `SideEffect` call
- THEN the call suspends with an `ApprovalRequest`, no tool executes, and the run is resumable through the normal approval path