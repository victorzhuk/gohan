# Spec Delta

## ADDED Requirements

### Requirement: Handoff terminal transition

A model-requested human handoff MUST perform its control transition exactly
once. The lifecycle MUST NOT carry two competing handoff branches that each
close the run. With a session index, the transition MUST persist the
`HandoffContext`, set `ControlHandoffRequested` and audit `handoff_requested`.
When an optional store is absent, the transition MUST skip only that step and
MUST still close the run with `Done{Reason: StopHandedOff}`. An optional
handoff callback MUST fire exactly once.

#### Scenario: control transition happens once
ID: `flow.handoff-control-transition-once`
- WHEN `request_human` ends a run and the session index with a control writer is present
- THEN the `HandoffContext` is persisted, `Control` is `ControlHandoffRequested`, `handoff_requested` is audited once, and the handoff callback fires once

#### Scenario: optional-store configuration still closes the run
ID: `flow.handoff-optional-store`
- WHEN the session index or the control writer is absent
- THEN only the missing step is skipped, the run still ends with `Done{Reason: StopHandedOff}`, and the handoff callback fires once

#### Scenario: handed-off state is preserved
ID: `flow.handoff-state-preserved`
- WHEN the handoff closes the run
- THEN the persisted `HandoffContext` and the returned state match the error's `HandoffContext`

#### Scenario: no resume token after handoff
ID: `flow.handoff-no-resume-token`
- WHEN a run ends from `request_human`
- THEN no `ResumeToken` is minted and `Resume` cannot continue the run
