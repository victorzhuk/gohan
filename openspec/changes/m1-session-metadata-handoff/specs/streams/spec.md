# Spec Delta

## Context

This delta MODIFIES the `streams` *Cancellation and streams* requirement. The main requirement block carries no normative prose and eleven scenario blocks. This delta is a strict superset of that block: it reproduces every scenario unchanged and adds the handoff terminal rule the requirement lacked — a request-human end is a `Done` and never a `Suspended`. The archive merge MUST union the added rule and MUST keep every reproduced scenario.

## MODIFIED Requirements

### Requirement: Cancellation and streams

A run that the model ends by requesting a human MUST terminate as a completed event stream, not a suspension: the run MUST close with `Done{Reason: StopHandedOff}` and MUST NOT emit a `Suspended` event or mint a `ResumeToken`, so the stream's last event is the `Done` and `Resume` cannot continue the run. Every other invariant of this requirement is unchanged.

#### Scenario: disconnect during side effect
ID: `streams.disconnect-during-side-effect`
- WHEN the client ctx is cancelled while `create_booking` is executing
- THEN the tool completes under the shield, its result is journaled and appended, `gohan.run.cancelled_shielded` increments, and only then does the run stop with `context.Canceled`

#### Scenario: disconnect during model call
ID: `streams.disconnect-during-model-call`
- WHEN the client ctx is cancelled mid-stream on a `ReadOnly` turn
- THEN the model call is cancelled immediately and no tool runs

#### Scenario: monotonic seq
ID: `streams.monotonic-seq`
- WHEN a run emits N events
- THEN `Seq` values are exactly 1..N in delivery order and `Done.Seq == N`

#### Scenario: feedback recorded event
ID: `streams.feedback-recorded-event`
- WHEN feedback is recorded for a message of a run that has an `EventLog`
- THEN `FeedbackRecorded` is appended to the run's `EventLog` with the next `Seq` after `Done`, outside the finished run's event stream, and carries no comment or correction text

#### Scenario: heartbeat independent of consumer
ID: `streams.heartbeat-independent-of-consumer`
- WHEN a consumer blocks inside the iterator for twice `LeaseTTL`
- THEN the run's lease stays fresh and `Recover` on another pod reclaims nothing

#### Scenario: slow consumer causes no idle retry
ID: `streams.slow-consumer-no-idle-retry`
- WHEN the provider streams at full speed and the consumer takes one event per `Timeout.Idle`
- THEN the model call completes without an idle timeout and the model is called once

#### Scenario: consumer stall preempts
ID: `streams.consumer-stall-preempts`
- WHEN the consumer takes no event for longer than `ConsumerStall`
- THEN the run suspends `Preempted` at its next safe point, the lease is released, and `Resume(token, Continue())` continues the run

#### Scenario: consumer stall detaches with log
ID: `streams.consumer-stall-detaches-with-log`
- WHEN the flow is built with `OnStall(Detach)` and an `EventLog`
- THEN the run continues detached and `Attach(runID, lastSeq)` delivers the events the client missed

#### Scenario: stream buffer bound
ID: `streams.stream-buffer-bound`
- WHEN the consumer stalls and the provider has produced more than `StreamBuffer` chunks
- THEN the provider read blocks, no chunk is dropped or reordered, and `gohan.stream.buffer_full` increments

#### Scenario: detached reconnect
ID: `streams.detached-reconnect`
- WHEN a `Detached` conversation run is in progress, a client consumed events up to `Seq 17`, disconnected, and calls `Attach(runID, 17)`
- THEN it receives events 18.. in order with no duplicates, including live events after catch-up

#### Scenario: detached requires log
ID: `streams.detached-requires-log`
- WHEN a flow is built with `Detached()` and no `EventLog`
- THEN the flow constructor returns an error
