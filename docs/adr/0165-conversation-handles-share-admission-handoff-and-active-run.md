# 0165. Conversation handles share admission, one handoff transition and ActiveRun

Status: accepted

## Context

The `Conversation` constructors bound `stack`, `tenant`, `titleProfile`, `flags`
and `frozen` by hand in two places and the two copies had drifted. Shutdown
admission kept the flag and the in-flight registry separately, so a run could be
admitted and then be unresolvable, or registered without the flag having been
read. The handoff path carried two competing `*types.HandoffError` branches,
each closing the run. `finishTurn` dropped its `terminal` argument on the way to
`finishRun` and `done`, so a limit, a cancel and a handoff all reported
`StopCompleted`. A caller had no way to ask a conversation which run is live,
and the only discovery path was an idempotency key.

## Decision

Both constructors call one shared binding helper that sets `stack`, `tenant`,
`titleProfile`, `flags` and `frozen` before the option loop. One binding, one
constructor.

One mutex guards both the shutdown flag and the in-flight registry, so a run is
either refused or registered under one lock. Two maps plus an ordering rule is
the race this repair exists to remove.

There is one handoff branch. It persists the `HandoffContext`, sets
`ControlHandoffRequested`, audits `handoff_requested`, fires the optional
callback exactly once and closes the run with `StopHandedOff`. Each optional
store step is guarded independently, so an absent store skips only its own step
and the run still closes. A handed-off run mints no `ResumeToken`.

`finishTurn` passes its `terminal` argument through to `finishRun` and `done`,
and `MemoryRuns.noticeFor` returns the recorded reason. The lifecycle never
substitutes `StopCompleted` for a terminal reason, and the cache-write gate keeps
using the completed-reason check it already had.

`Conversation` gains
`ActiveRun(ctx context.Context, sessionID string) (string, error)`. It applies
the same owner check as `Cancel`, then resolves the in-process live map, then
`SessionRunFinder`. Active means state `Running` or `Resuming` with a live lease.
`Suspended`, terminal and lease-expired runs report `types.ErrRunNotActive`.
`Send` is unchanged. The `Runs` port does not gain a method: the seam stays the
optional `SessionRunFinder` interface, which `MemoryRuns` satisfies through a new
`RunForSession` returning the session's active run or `stores.ErrRunNotFound`, and
which the PostgreSQL store implements with the same active rule.