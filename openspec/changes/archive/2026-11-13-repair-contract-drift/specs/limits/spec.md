# limits delta

## MODIFIED Requirements

### Requirement: Run limits

The harness MUST stop a run with `Done{Reason: StopLimit}` when `MaxTurns` or `MaxToolCalls` is reached at an effect boundary, and MUST abort with `*LimitExceededError` when `MaxCost` or `MaxWallClock` is exceeded. A ledger reservation exceeding the remaining `MaxToolCalls` MUST refuse the whole batch before any call executes, without charging it, and the native driver MUST return the error matching `types.ErrBatchOverrun` directly. If `runtime.Batch.Run` instead returns an error matching `types.ErrBatchOverrun`, the native driver MUST refund the reservation and return `*LimitExceededError{Limit: "MaxToolCalls"}`. The foreign path renders reservation failures per call as permanent `not_executed:` results. Wall clock MUST be measured on monotonic time.

#### Scenario: hard cost abort
ID: `limits.hard-cost-abort`
- WHEN cumulative cost exceeds `MaxCost`
- THEN the run aborts with `*LimitExceededError{Limit: "MaxCost"}` before the next model call; `LimitWarning` was emitted once at `SoftRatio`

#### Scenario: wall clock
ID: `limits.wall-clock`
- WHEN a run exceeds `MaxWallClock` during a tool call
- THEN the tool's ctx is cancelled and the run aborts

#### Scenario: provider calls count
ID: `limits.provider-calls-count`
- WHEN `MaxToolCalls` is 3 and one model response performs three provider searches
- THEN the next ledger reservation is refused with an error matching `types.ErrBatchOverrun` and spends no additional budget

#### Scenario: wall clock is monotonic
ID: `limits.wall-clock-monotonic`
- WHEN the run's budget is measured while the system wall clock is stepped backwards by one hour
- THEN `MaxWallClock` still expires after the configured duration of elapsed monotonic time

#### Scenario: limits under foreign backend
ID: `limits.limits-under-foreign-backend`
- WHEN the same limits are configured on an eino graph flow
- THEN the same scenarios pass