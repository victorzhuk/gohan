# guards delta

## ADDED Requirements

### Requirement: Guard error fails closed

A guard error at any stage MUST be treated as `Block` at that stage: the output is cut and `*GuardBlockedError` carries the stage and reason.

#### Scenario: guard error blocks
ID: `guards.guard-error-fails-closed`
- WHEN the injection detector returns an error at the input stage
- THEN the stage blocks with `*GuardBlockedError{Stage: StageInput}` and no unguarded content reaches the model