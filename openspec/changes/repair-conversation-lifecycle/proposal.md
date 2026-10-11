# Proposal

## Why

The second-wave audit found defects on the conversation lifecycle. Both
conversation constructors drop the stack, tenant, title profile and frozen
flag bindings, so shutdown admission and titling are silently off. Shutdown
admission is not atomic with run registration. The lifecycle carries two
competing handoff branches. The durable finish path forces `StopCompleted`
and discards the real terminal reason. A detached run is not discoverable,
and no shipped store implements `SessionRunFinder`, so `Cancel` cannot reach
a run another pod started.

## What Changes

- Bind `Stack`, tenant, title profile and frozen flag values in both the
  native and the foreign conversation constructor before any option runs.
- Make the shutdown admission decision and the run registration one atomic
  operation, so a run never starts unregistered during `Shutdown`.
- Collapse the duplicate handoff branches into one terminal transition.
  Persist the handed-off state and fire the optional handoff callback once,
  including on optional-store configurations.
- Preserve the terminal `StopReason` through the final `Done` event and the
  durable run record instead of substituting `StopCompleted`.
- Ship `RunForSession` on the core memory `Runs` store so `Cancel`,
  `Steer` and discovery reach a cross-pod run.
- Add the owner-authorized handle method
  `Conversation.ActiveRun(ctx, sessionID) (string, error)`. It returns the
  ID of a run that is `Running` or `Resuming` with a live lease, or
  `types.ErrRunNotActive` for a `Suspended`, terminal or expired run. It
  leaves `Send`'s signature and detached behavior unchanged.
- Repair `core/identity_detach_scenarios_test.go` to capture the run ID
  before the runtime gate closes. Do not widen the timeout.

## Capabilities

### New Capabilities

None. The change repairs behavior already owned by existing capability
specs.

### Modified Capabilities

- `streams`: conversation handle construction, terminal reason and active-run
  discovery.
- `runtime`: atomic shutdown admission and terminal stop-reason preservation.
- `flow`: single handoff terminal transition.
- `stores`: shipped `SessionRunFinder` and durable finish-reason reporting.
- `identity`: detached credential reissue timing and run-ID stability.

## Impact

Touched core driver files are `core/conversation.go`,
`core/native_conversation.go`, `core/foreign_conversation.go`,
`core/drive_lifecycle.go`, `core/shutdown.go` and `core/stores`. The testkit
conformance fixture and `core/identity_detach_scenarios_test.go` change. The
concrete Postgres store implementation belongs to
`repair-postgres-store-contracts`; this change owns only the core memory
implementation. The `core/foreign_conversation.go` edit lands after the
`restore-runtime-governance` edit to the same file and keeps both changes.
Public entry paths `Send`, `Resume` and `Recover` keep their
signatures. The handle Contract edit and the types-index regeneration happen
at apply time. No ADR number is reserved here.
