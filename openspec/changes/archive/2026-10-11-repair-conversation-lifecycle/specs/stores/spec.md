# Spec Delta

## ADDED Requirements

### Requirement: Session run discovery

The core memory `Runs` implementation MUST implement
`SessionRunFinder.RunForSession(ctx, sessionID) (stores.Run, error)`. It MUST
return the session's active run, and MUST NOT return an inactive run. A run
is active only when its state is `Running` or `Resuming` and its lease is
live. It MUST return `stores.ErrRunNotFound` when the session has no active
run. `Cancel`, `Steer` and `ActiveRun` MUST reach a run another pod started
through this interface. The `Runs` port MUST NOT gain a method.

#### Scenario: memory finder reaches a cross-pod run
ID: `stores.run-for-session-cross-instance`
- WHEN a run is `Running` or `Resuming` with a live lease and was not started by this process
- THEN `RunForSession` returns it and `Cancel` reaches it

#### Scenario: finder refuses an absent run
ID: `stores.run-for-session-absent`
- WHEN the session has no active run
- THEN `RunForSession` returns `stores.ErrRunNotFound`

#### Scenario: finder refuses an inactive run
ID: `stores.run-for-session-terminal`
- WHEN the session's run is `Suspended`, terminal, or its lease expired
- THEN `RunForSession` returns `stores.ErrRunNotFound`

### Requirement: Finish reason reporting

A durable finish through the reason-recording path MUST report the run's
real `StopReason`. The terminal notice reason MUST be the recorded
`StopReason` when one was recorded. The store MUST NOT report
`StopCompleted` for a run that ended with `StopLimit`, `StopHandedOff` or
`StopCancelled`.

#### Scenario: notice carries the recorded reason
ID: `stores.finish-notice-recorded-reason`
- WHEN a run finishes through `FinishWithReason` with reason `StopLimit`
- THEN the terminal notice reason is `StopLimit`, not `StopCompleted`

#### Scenario: cancelled finish reports cancelled
ID: `stores.finish-notice-cancelled`
- WHEN a run finishes through `FinishWithReason` with reason `StopCancelled`
- THEN the terminal notice kind is `NoticeCancelled` and the reason word is `StopCancelled`
