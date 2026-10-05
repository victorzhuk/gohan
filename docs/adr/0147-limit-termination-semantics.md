# 0147. Limit termination semantics

Status: accepted

## Context

Two normative contracts describe what happens when a limit is reached, and they disagree:

- `openspec/specs/runtime/spec.md` scenario `runtime.max-turns`: with the model calling a tool every turn and `MaxTurns=3`, the run ends with `Done(StopLimit)` after three model calls and `gohan.max_turns.reached` increments. This is acceptance.
- `openspec/specs/limits/spec.md` § 6.13b: "Exceeding a limit aborts the run (`*LimitExceededError`, not resumable)".

Both cannot hold for `MaxTurns`. The distinction matters to a caller: `Done(StopLimit)` is a finished run whose partial result is usable, while `*LimitExceededError` is a failed run. It also decides how the native runtime terminates a turn loop, which is the first thing the hardened native path has to seal.

## Decision

The termination mode depends on the limit:

- `MaxTurns` and `MaxToolCalls` are budgeting stop conditions. Reaching one ends the run with `Done{Reason: StopLimit}` and increments the matching counter (`gohan.max_turns.reached`, `gohan.max_tool_calls.reached`). The run is not resumable, and the partial outcome is reported normally.
- `MaxCost` and `MaxWallClock` are hard limits. Exceeding one aborts the run with `*LimitExceededError`, which is not resumable.

`limits` § 6.13b is amended to state this, and the `runtime.max-turns` scenario stands as written.

## Consequences

- The native runtime can implement `MaxTurns` as a loop-exit condition instead of an error path, and `Done(StopLimit)` stays an ordinary terminal transition that the lifecycle already handles.
- Callers must distinguish a stop-limited run from a failed one by `Done.Reason`, not by the presence of an error.
- `LimitExceededError` keeps its meaning for the two limits where continuing is unsafe rather than merely expensive.
- `runtime.max-tool-calls` and `limits.hard-cost-abort` keep their existing expectations under this split.

## Alternatives

- Making every limit abort with `*LimitExceededError`. Rejected: it contradicts `runtime.max-turns`, which is an acceptance scenario, and would report a completed partial result as a failure.
- Making every limit a stop. Rejected: a run that exceeds its cost ceiling has already spent the money, and reporting it as a clean stop with `Done(StopLimit)` would hide an operator-relevant event behind a normal terminal state.
