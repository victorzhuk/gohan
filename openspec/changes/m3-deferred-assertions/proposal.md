# Proposal: Deferred scenario assertions

## Why

On the tree where every M3 child change is archived, `task spec:gate GATE=M3` reported
`missing_in_scope=31`. Nine of those were invisible only because the gate's redis leg
skipped the `full` lane; that wiring is fixed and those nine now pass by their exact ids,
leaving `missing_in_scope=22`. They split in two:

- Nineteen are registered, named in a live capability spec, and asserted nowhere:
  `subflows.*` (7), `release.*` (6), `interop.awaiting-input-invalid`,
  `model.version-drift`, `stores.pg-auto-migrate-refused-in-prod`,
  `structured-output.constrained`, `context.research-report-claim-boundary` (the one M2
  deferral the M3 gate also covers) and `taint.subflow-carries-origin`.
- Three are the `performance.live-*` legs of `m3-s3-loadtest`, blocked on external Kafka,
  Temporal and Zeebe infrastructure and recorded as blocked admission, not as a gap.

M3 integration acceptance cannot close while nineteen named requirements have no
executable evidence.

## What Changes

- Author one subtest per id, named exactly its registry id, in the package that owns the
  requirement.
- Implement the requirement where the assertion exposes a gap. The spec text does not
  change: every one of the nineteen ids is already normative in `openspec/specs`.
- Drop each id's `deferred_to` once its subtest passes, so the gate counts the change's
  progress instead of a later milestone absorbing it.
- Resolve `taint.subflow-carries-origin` explicitly — asserted, or superseded by a
  recorded decision. It has been marked a milestone conflict since M1.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

None. This change adds executable evidence for requirements that already exist.

## Impact

Test files plus implementation only where a task's assessment finds the requirement
unimplemented, in `core` and `std` for subflows, taint, context and structured output;
`std/evals` with `core` for release; `adapter/postgres` for the production migration
refusal; `adapter/mcp` for the interop reply; and the model catalog surface.
`task spec:gate GATE=M3` is the acceptance gate.
