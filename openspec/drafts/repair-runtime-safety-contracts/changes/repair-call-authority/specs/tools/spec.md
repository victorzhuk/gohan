## ADDED Requirements

### Requirement: Host execution advertises its effective effect

A host runner MUST expose the same effect used by permission, journal, shielding, scheduling and timeout behavior.
An unset host effect MUST resolve to `SideEffect`.
An explicit effect option MUST override the supplied declaration.

#### Scenario: Host effect is omitted
ID: `tools.host-effect-default-consistent`
- WHEN a host runner is registered without an explicit effect
- THEN it advertises `SideEffect` and requires the corresponding approval and journal path

#### Scenario: An effect option overrides the declaration
ID: `tools.host-effect-option-consistent`
- WHEN the host runner receives an explicit effect option
- THEN its declaration and execution use that effect consistently

### Requirement: External effects retain one idempotency identity

Initial execution and every retry of a reserved tool intent MUST receive the same nonempty downstream idempotency key.
A completed successful intent MUST NOT suppress a genuinely new call under the existing intent-level contract.

#### Scenario: Completion is lost after an external effect
ID: `tools.initial-and-replay-key-stable`
- WHEN an external effect runs and journal completion is lost before recovery
- THEN the initial call and replay reach the downstream boundary with the same reserved key

### Requirement: Argument identity preserves exact numeric values

Approval and journal fingerprints MUST distinguish unequal JSON numeric values without floating-point rounding.
Equivalent exact numeric spellings and object member order MUST preserve identity.
Legacy unresolved journal candidates MUST be reused only after their stored complete arguments prove an exact match.

#### Scenario: Adjacent large integer arguments differ
ID: `tools.exact-numeric-fingerprint`
- WHEN arguments contain 9007199254740992 and 9007199254740993
- THEN their fingerprints differ and one value's authority cannot cover the other

#### Scenario: An unresolved legacy intent is recovered
ID: `tools.legacy-intent-exact-match`
- WHEN a legacy journal candidate has complete argument evidence matching the new call exactly
- THEN replay preserves its original reserved key
- AND a candidate with different or absent argument evidence does not authorize a new effect
