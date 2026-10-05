# temporal-travel

An offline durable workflow shaped like a travel booking. It demonstrates
gohan's run vocabulary — suspension, resume input, journal replay, frozen
flags — with no workflow engine, no network and no API keys.

## What it demonstrates

- **An activity runs.** The runtime's first effect searches flights; the
  result is recorded in the journal (`stores.MemoryJournal`).
- **A signal arrives.** The run suspends behind a single-use resume token
  (`stores.MemoryCheckpoints`) and waits for the approval signal. The
  checkpoint carries the serialized `runtime.State`, including the
  snapshot of the rollout flags taken at start.
- **A crash interrupts.** The fixture (`crash`) drops every in-memory
  runtime fact after the signal was consumed, keeping only the stores.
- **Recovery replays from the journal.** The resumed run reads the
  consumed input back through `PendingInput` and re-drives the
  checkpointed state. The token is single-use (`types.ErrTokenConsumed` on
  the second consume), so the booking happens exactly once, and the
  search is answered by the journal instead of re-executing.
- **The frozen flag survives the suspension.** `booking.online` flips in
  the live provider while the run is suspended; the recovered booking
  still reads the pinned `runtime.State.Flags` value.
- **Replay is a `Step` re-execution.** Re-running `Step` over the
  recorded results reproduces the same `State` sequence up to the
  suspension point.

## Run it

```
go run ./temporal-travel/
```

## Test it

```
go test -short -timeout 2m ./temporal-travel/ -run TestTemporalTravelOffline -v
```

The subtests map to the `engines` spec scenarios
`engines.crash-after-consume`, `engines.frozen-flag-on-replay` and
`engines.replay-is-step-re-execution`.
