# Spec Delta

## MODIFIED Requirements

### Requirement: Verification

`Verify` is a read-only declaration that reads authoritative state and returns `types.Succeeded` or `types.Failed`. The harness MUST invoke it after every `SideEffect` result with `Outcome: Unknown`, for every Unknown journal entry during Resume or Recover, and before Done while Uncertain is non-empty. Verify MUST NOT re-execute the effect. It MUST use the original arguments and result and MUST be journaled under the original `CallKey` with `"/verify"` appended to its call ID.

When Verify returns `Succeeded` or `Failed`, the harness MUST persist the verified result under the original journal key and append a `stores.AuditToolOutcome` record with the reconciled outcome. The verified result MUST be made available to the model or lifecycle continuation. The harness MUST propagate Journal Reserve, Journal Complete, and audit append errors. It MUST NOT report reconciliation or clear uncertainty when persistence fails.

When no Verify declaration exists or Verify returns an error, the original entry MUST remain `Unknown`, remain in Uncertain, and surface as `*std.UncertainOutcomeError` at the public caller. A Verify error MUST NOT cause the effect to execute again.

#### Scenario: unknown reconciled
ID: `tools.verify-reconciles-unknown`
- WHEN a `SideEffect` call times out with `Outcome: Unknown` and the tool declares `Verify` that finds the booking exists
- THEN the entry's recorded result becomes `Outcome: Succeeded`, the model receives the verified result, and `Done.Uncertain` is empty

The following existing scenarios are restated as unchanged coverage of this modified requirement; this package claims only `tools.verify-reconciles-unknown`.
#### Scenario: verify on recover
ID: `tools.verify-on-recover`
- WHEN a run is reclaimed after a crash with two `Unknown` entries, one with `Verify`
- THEN the one with `Verify` is reconciled before replay continues and the other stays in `Uncertain`

#### Scenario: verify never re-executes
ID: `tools.verify-read-only`
- WHEN `Verify` runs
- THEN no `Call` is issued for the tool and the journal shows a `/verify` entry

#### Scenario: verify error keeps uncertainty
ID: `tools.verify-error`
- WHEN `Verify` returns an error
- THEN the entry stays `Unknown` and `*UncertainOutcomeError` is returned

#### Scenario: timeout kills group
ID: `tools.timeout-kills-group`
- WHEN a command spawns children and exceeds its timeout
- THEN the whole process group is killed and the result is an error result

#### Scenario: output cap
ID: `tools.output-cap`
- WHEN stdout exceeds the cap
- THEN the result is truncated with a marker

#### Scenario: env allowlist
ID: `tools.env-allowlist`
- WHEN the parent has `SECRET=x` and the allowlist is empty
- THEN the child environment does not contain `SECRET`

#### Scenario: failed outcome reconciled
- WHEN a declared Verify returns `Outcome: Failed` for an Unknown SideEffect entry
- THEN the original journal entry becomes `Outcome: Failed`, an audit outcome records Failed, and the effect is not re-executed

#### Scenario: journal error is surfaced
- WHEN Reserve or Complete fails while the harness journals Verify or its reconciled result
- THEN the public operation returns that journal error and does not report the entry as reconciled

#### Scenario: verify error preserves uncertainty
- WHEN Verify returns an error for an Unknown entry
- THEN the original entry remains Unknown and the public caller receives `*UncertainOutcomeError`