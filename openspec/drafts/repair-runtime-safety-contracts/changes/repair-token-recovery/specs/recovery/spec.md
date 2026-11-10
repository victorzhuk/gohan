## ADDED Requirements

### Requirement: Automatic preemption recovery consumes its exact token

Automatic preemption recovery MUST conditionally consume the current `Preempted` checkpoint before taking its token-fenced resume lease.
It MUST NOT automatically resume a human approval or another suspension reason.
An obsolete token MUST NOT replace a later suspension.

#### Scenario: Recovery reaches a second suspension
ID: `recovery.preemption-token-consumed`
- WHEN recovery consumes checkpoint A and execution later suspends on checkpoint B
- THEN A cannot be resumed again and B remains current

#### Scenario: A human approval is pending
ID: `recovery.human-approval-not-preempted`
- WHEN a run is suspended for human approval and automatic recovery runs
- THEN recovery does not decide or execute the approval-controlled call

### Requirement: Recorded consumption is independent of input contents

Recovery MUST use persisted consumption state and recorded input.
A consumed empty `Continue()` MUST remain distinguishable from an unconsumed checkpoint.
A crash between conditional consumption and lease acquisition MUST remain recoverable exactly once.

#### Scenario: The client dies after consuming an empty continuation
ID: `recovery.empty-continue-consumed`
- WHEN a client consumes a preempted token with empty continuation input and dies before acquiring its resume lease
- THEN recovery continues that consumed decision exactly once

### Requirement: Recovery records the public event stream durably

Recovered execution MUST persist ordinary events, terminal failure and the final boundary with live execution's ordering and redaction guarantees.
Another conversation instance MUST attach to the recovered tail and terminate at its boundary.

#### Scenario: A second instance attaches after recovery finishes
ID: `recovery.eventlog-tail-durable`
- WHEN recovery completes a run and another conversation attaches after the prior cursor
- THEN it receives the recovered tail and exactly one terminal boundary without waiting for cancellation

### Requirement: Missing optional recovery support has explicit behavior

Governed external resume without token-fenced store support MUST return `ErrCheckpointIncompatible` before consumption.
Automatic preempted or consumed-token recovery MUST skip an unsupported optional path.
Supported stale-run recovery and new sends MUST remain available.

#### Scenario: A custom run store has no token-fenced extension
ID: `recovery.optional-token-surface-absent`
- WHEN a custom store implements the frozen run port without the optional token-fenced surface
- THEN external resume refuses without consuming its token and automatic recovery does not use that unsupported path
