# Spec Delta

## MODIFIED Requirements

### Requirement: Counting and calibration

The harness MUST calibrate each profile's token estimator from reported usage and MUST persist the calibration ratio with the checkpoint, so a replayed run reaches the same compaction decision. On `ClassContextOverflow` for a request the budget accepted, the harness MUST compact once with the raised ratio and MUST retry the request once; a second overflow MUST fail the turn `Permanent`.

#### Scenario: calibration from usage
ID: `context.calibration-from-usage`
- WHEN a response reports 12 000 input tokens for a request estimated at 10 000
- THEN the next estimate for the same profile is multiplied by 1.2

#### Scenario: calibration persisted for replay
ID: `context.calibration-persisted-for-replay`
- WHEN a run compacted with ratio 1.2 is replayed on another pod
- THEN the replay reads the ratio from the checkpoint and reaches the same compaction decision

#### Scenario: overflow compacts and retries once
ID: `context.overflow-compacts-and-retries-once`
- WHEN the provider returns `ClassContextOverflow` for a request the budget accepted
- THEN one compaction runs with the raised ratio, the request is retried once, and a second overflow fails `Permanent` with `gohan.context.overflow_retry` at 1
