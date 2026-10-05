# 0148. The executable limit middleware stays in core for M0

Status: accepted

## Context

The core budget rule (`docs/design/architecture.md` § 4.2a) assigns every default, matcher, preset and policy value to `std`. Two things in the tree violate it:

- `core/chains` implements the executable limit middleware (`Limits`, `ToolLimits`, `LimitsState`): turn counting, wall-clock enforcement, charging, warnings and abort decisions.
- `core/types` carries the concrete preset values `InteractiveLimits`, `AgenticLimits`, `BatchLimits`, and `core/limits.go` carries default values.

The `limits` capability's own contract block declares those preset variables, so the contract and the budget rule disagree about where the values belong. A hardening review recorded the mismatch; the fix is either a move or a recorded exception, and a move touches exported API.

## Decision

For M0, the executable limit middleware and the preset values stay where they are, and the exception is recorded rather than implied. `docs/design/architecture.md` § 4.2a names it.

The reason is that the move is not a file shuffle: `LimitsState` is created per run and reached by middleware that must obtain it from the run context (see `openspec/changes/m0-hardening/design.md` D-per-run-accounting), and `core/chains` is where the model and tool chains consume it. Moving the middleware before that accounting change would create a second migration for the same code. The ownership migration is therefore sequenced after per-run accounting, not before it. That accounting has since landed (ADR-0150 item 4, ADR-0151 item 4): the migration is additive in C10 and C11, and C17 deletes the legacy middleware with its ledger and context accessors, which is the step that closes this exception.

## Consequences

- The budget rule's consequence list gains an explicit, dated exception instead of a silent violation a reader has to notice.
- The `limits` contract stays truthful about where the values live today.
- The migration remains open work with a stated prerequisite, so a later change does not have to re-derive why the values are still in `core`.
- Nothing about the exported surface changes: `types.InteractiveLimits` and the `core/chains` middleware keep their names and behaviour for the lifetime of `v0.x`.

## Alternatives

- Moving the middleware and the preset values now. Rejected: the per-run accounting change rewrites the same code paths, and a move first would leave `std` owning middleware that cannot yet obtain a run's state.
- Deleting the executable middleware from core and leaving the rule unstated. Rejected: the values are consumed by the chains the driver runs, so removing them removes behaviour.
