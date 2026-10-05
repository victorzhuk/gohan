# Design: m0-public-path-closure

Sealed decisions. A chunk implements them and does not re-decide them. The audit that produced them is the stage review recorded in `tasks.md`; the ADRs that change recorded decisions are ADR-0153 and ADR-0154.

## D1 — Untrusted ingress is refused, not filtered

`rejectReservedMeta` is wired at every point where caller-supplied messages enter history: `Send` (both the hand-off append and `appendInput`), `Steer` before the mailbox signal, and `OperatorSend` before the append. A decoded receipt additionally requires a non-empty approver list, so the grant lookup treats a shapeless receipt as no grant. Trusted writes by a `SessionLog` implementation are outside this boundary: the reserved key is a guard against the message surface, not against the store.

## D2 — Run identity is minted, not inherited

A public drive installs the identity of the run it acquired — `RunID`, `RootRunID` for an independent root, `SessionID`, `Flow`, `Mode` and the verified `Principal` — replacing any ambient `RunInfo` and preserving `PrincipalFrom`. Installation happens after the lease is acquired and before the input append, for `Send` and `Continue` alike. The caller's principal is never replaced.

## D3 — Recovery fails closed without stored authority

When neither a checkpoint originator nor a stored session owner resolves, recovery returns a wrapped existing error through the existing abandonment path, closes the acquired run `Failed`, and executes no model, tool or credential-source call. The reaper's ambient principal and credentials never become the driver's. An execution failure cause is propagated instead of being reported as recovery success.

## D4 — The native phase carries a driver record

`State.Backend` holds the phase and a private driver record as separate fields. Advancing the phase preserves the bytes the effect produced. `State.Turn` counts model calls and is authoritative for event turns, message identities, checkpoints and assembly; the model phase is entered only when the turn is below the limit, the pending batch settles first, and the run ends with one `Done{StopLimit}`. The record, its validation and its compatibility rules are ADR-0154.

## D5 — Append shape is chosen, not global

A turn with no calls or only `ReadOnly` calls appends its assistant message and results in one `Append` at step end; a turn carrying `Idempotent`/`SideEffect` calls appends the assistant message with its pending calls before the batch executes and its results at settlement. A read-only turn that suspends persists its assistant message and settled results before the checkpoint. A non-read-only turn never delays its call persistence to satisfy a one-append budget. Recovery may repeat an uncommitted read-only turn; that is the documented trade-off, not a durability defect.

## D6 — One bounded handoff, one arming rule

The run worker and the attached consumer are connected by exactly one bounded handoff: `DefaultStreamBuffer` ordinary tuples plus one separate terminal slot, with cancellable blocking production and no drops, overwrites or reordering. The stall deadline is armed only while a consumer callback is outstanding. Explicit abandonment wakes empty and full waits and applies the run's stall action even when `ConsumerStall` is 0. Preemption shutdown never cancels the lifecycle, and detach keeps recording under the same identity, lease, ledger and wall clock. ADR-0153 seals this and the resumed worker.

## D7 — Tool predicates and definition ownership

A tool step's `Applies` predicate is evaluated against the registered specification snapshot for the original call before the step's middleware runs; a nil predicate applies universally, and `Explain` and execution agree. `Build` copies the `Tools`, `ToolChain` and `ModelChain` containers it publishes, including when the stack supplies no model middleware. Container ownership only: the stack does not claim deep immutability of tool implementations or closures.

## D8 — Assembly reads the resolution

The native request preparation fills the existing `AssembleInput` fields from the resolved configuration and the current state: registered instruction and schemas, selected profile, current run identity and turn, and the dynamic history. `Explain` keeps its documented pre-middleware equality claim; arbitrary middleware may still change a prepared request.

## D9 — Evidence binds the public path

Conformance runs the shipped native runtime and governed effects through their public entry points, with doubles only at the model and tool ports. A scenario ID is bound by a test that asserts the scenario's whole contract; a narrower test gets an ordinary name. The gate tools' tests and the runnable examples run in CI, and the benchmark job runs the base/head gate with event-provided revisions under a bounded step, reporting the shared-runner limitation rather than implying a stable reference host.

## D10 — The `Attach` ownership question is recorded, not answered here

`Attach` today treats possession of a run identifier as sufficient, and the `streams` spec defines no ownership rule for it; `identity` rule 6 covers `Send`, `Continue`, `Steer` and `Cancel`, not `Attach`. The audit treats this as a boundary gap to record, not to fill: a run identifier is a bearer capability until the spec says otherwise, and `interop`'s `GET /runs/{id}/events` is the surface where that rule belongs. No authorization check is added here, and the gap is named in the changelog so it is not mistaken for a covered case.

## Rejected

- Approving every pending call in one round to avoid a per-ask flow. Rejected: the `runtime` spec requires one ask at a time, and a session grant recorded for one call would silently authorize an unidentified ask.
- Carrying the continuation in new public `State` fields. Rejected: that surface is the frozen v1-candidate; a private record in `Backend` needs no port change.
- Letting an in-test repetition count stand in for a deterministic stream fixture where a gate exists. Rejected in general; accepted only for the closed-buffer/terminal-error selection, where the language's random selection among ready cases leaves no deterministic ordering, and the assertion is therefore an in-test bounded repetition with a failure probability below any practical threshold.
- Suppressing the observed `TestStreamStall` failure or relaxing its expectation to one model call. Rejected: the fixture cancels the run context instead of arming the production preemptor, so it tests a disconnect, not the stall contract. The fixture is replaced with production-path coverage under D6.
- Making the event log the delivery queue. Rejected: it needs cursor and retention coordination for ordinary streams and gives no provider backpressure.
