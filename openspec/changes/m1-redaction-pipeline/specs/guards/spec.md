# Spec Delta

## ADDED Requirements

### Requirement: Guard redaction rewrite

The input guard MUST apply the flow's redaction policy to new user input before assembly. When the guard returns `Rewrite`, the model request and stored history MUST contain the rewritten input only. The guard MUST preserve the resulting redaction map for storage protection and later output rehydration.

#### Scenario: redaction
ID: `guards.redaction`
- WHEN the input guard returns `Rewrite` with personally identifying information removed
- THEN the model request and stored history contain the rewritten input only
