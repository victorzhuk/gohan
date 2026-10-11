# Spec Delta

## ADDED Requirements

### Requirement: Atomic shutdown admission

The shutdown admission decision and the registration of a starting run in
the in-flight set MUST happen as one atomic operation. `Shutdown` MUST NOT
be able to begin between the decision and the registration and leave the
run unregistered. Registration MUST happen before `Runs.Start` on every
constructor path. A run that races `Shutdown` MUST either be registered
before the shutdown set is armed or be refused with
`types.ErrShuttingDown`.

#### Scenario: registration is not lost to shutdown
ID: `runtime.shutdown-admission-atomic`
- WHEN a `Send` registers a run concurrently with `Shutdown`
- THEN the run is either registered before the preemption set is armed, or refused with `types.ErrShuttingDown`; no run starts unregistered

#### Scenario: every constructor admits before start
ID: `runtime.shutdown-admission-every-constructor`
- WHEN a run starts through the native constructor or the foreign constructor
- THEN it is registered in the in-flight set before `Runs.Start`

#### Scenario: resume admits before start
ID: `runtime.shutdown-admission-resume`
- WHEN `Resume` starts a run
- THEN it is registered in the in-flight set before `Runs.Start`

### Requirement: Terminal stop reason preservation

The lifecycle MUST pass the run's terminal `StopReason` to both the final
`Done` event and the durable run record. It MUST NOT substitute
`StopCompleted` for a terminal reason. A run that ends because a limit was
reached, because a cancel signal arrived, or because the model requested a
human MUST report that reason.

#### Scenario: done carries the limit reason
ID: `runtime.done-carries-limit-reason`
- WHEN a run ends because `MaxTurns` was reached
- THEN `Done.Reason` is `StopLimit`

#### Scenario: durable record carries the terminal reason
ID: `runtime.finish-carries-terminal-reason`
- WHEN a run ends with a non-completed reason and the runs store implements `FinishReasonRecorder`
- THEN the stored reason is that terminal reason, not `StopCompleted`

#### Scenario: cancelled run keeps its reason
ID: `runtime.cancelled-run-keeps-reason`
- WHEN a pending cancel signal ends the run at a safe point
- THEN `Done.Reason` is `StopCancelled` and the durable reason matches

#### Scenario: handed-off run keeps its reason
ID: `runtime.handed-off-run-keeps-reason`
- WHEN the model requested a human and the run closes
- THEN `Done.Reason` is `StopHandedOff` and the durable reason matches
