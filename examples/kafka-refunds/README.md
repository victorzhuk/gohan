# kafka-refunds

Offline demonstration of the engine-composition contract for a Kafka-style
consumer. A delivered message drives one bounded run; the refund side effect
is journaled; a redelivery of the same message key returns the recorded
result instead of refunding twice. No Kafka client and no network — the
fixture in the test feeds the events.

What it demonstrates:

- `OperationID` dedup: the message key is the operation id, so a redelivery
  hits `types.ErrOperationExists` (as `ErrRedelivered` with the existing run
  id) before any component runs, and the run row's `ResultRef` yields the
  result the first delivery recorded.
- Journal fingerprinting: inside the run, the refund call is fingerprinted
  with `gohan.ToolFingerprint`, and a retried effect replays the completed
  journal entry instead of re-executing.
- Live kill flag: `refunds.kill` is consulted after the approval is
  audited, so a flag turned on between approval and execution records
  `flag_denied` after `approved` and the effect never runs.

Run it:

```
go test -short -timeout 2m ./kafka-refunds/ -run 'TestKafkaRefundsOffline' -v
```

Subtests map to the `engines` scenarios
`engines.redelivery-returns-the-same-result` and
`engines.live-deny-beats-approval`, plus a journal-replay case.
