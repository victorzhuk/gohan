# 0146. A consumed preempted checkpoint is recoverable

Status: accepted

## Context

`Shutdown` parks a run at a persisted safe point as `Preempted` and the client keeps the choice to resume it. `Recover` has two passes: preempted runs first (`PreemptedLister`, no staleness wait), then consumed checkpoints (`ResumeReadyLister`).

The preempted pass skips a run whose checkpoint already carries a recorded client decision, and the consumed pass skipped every consumed `Preempted` checkpoint on the grounds that the client owns the resume. Both were right about a live client and wrong about a dead one. A client that consumes the token and then dies before `Runs.Resuming` leaves a run that neither pass touches: the checkpoint is consumed, so the preempted pass reads it as client-owned, and the consumed pass refuses it by reason. The run never finishes and the session cannot be advanced by another `Send` while the row is unsettled.

The lease, not the token, is what actually separates a live client from a dead one: `Runs.Resuming` fails while a live lease exists.

## Decision

`Recover` recovers a consumed `Preempted` checkpoint whose run holds no resume lease, driving it with the recorded input. A consumed checkpoint whose client still holds a live lease stays untouched, so a live resume always wins the race. The recovery spec states this in § 3a and the new scenario `recovery.consumed-preempted-is-recoverable` accepts it.

## Consequences

- A crash between `Consume` and `Resuming` is no longer a stranded run; recovery completes it exactly once.
- The lease is the single arbiter of client-versus-reaper ownership for both passes, which removes the reason-based special case rather than adding a second one.
- Recovery now depends on the run row being present and lease-checkable for a consumed preempted checkpoint. A store that cannot report the run for a checkpoint cannot be recovered this way; it reports the run as absent and the pass skips it, as before.

## Alternatives

- Leaving the case unrecoverable and documenting a manual repair. Rejected: recovery is the only path back for a headless run, and the failure is silent.
- Consuming the checkpoint inside the preempted pass so the token can no longer race. Rejected: the token exists so the client can decide; consuming it in the reaper removes the client's choice and is a larger contract change than the one needed.
- Deciding by a client-decision timestamp. Rejected: adds a clock dependency to a decision the lease already answers.
