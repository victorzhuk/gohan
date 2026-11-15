# Proposal: Shadow core enforcement

## Why

The release spec froze `RunMode`, `ShadowSuppressed`, release identity on `RunInfo` and the shadow store rules, but the driver never enforces them: `AgentRun.Mode` is declared and never set, `runIdentityCtx` rebuilds `RunInfo` with `Mode: Primary` and drops release identity, no sentinel exists in code, the batch path executes side-effect tools under a shadow run, and a shadow suspension would persist a checkpoint. Six `release.*` scenarios stayed deferred because of that gap.

## What Changes

- Add `WithShadowMode` and `WithReleaseIdentity` build options that carry mode, release id and variant into every run the stack's conversations start.
- Enforce the frozen shadow rules in the driver: read-only tools run, idempotent and side-effect calls replay the primary journal or fail with `ShadowSuppressed`, shadow appends go to `<session>/shadow/<runID>`, the primary `SessionLog`, `Journal`, `AuditLog` and tree budget stay untouched, and a suspension finishes terminally with `Done{Reason: ShadowSuspended}`.
- Carry release identity and variant on spans, audit records, `Done` metadata (the event meta) and `gohan.*` metrics.
- Emit `gohan.shadow.runs` and `gohan.shadow.suppressed{tool}`.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `release`: core enforcement of the frozen shadow-mode and release-identity contracts.

## Impact

Implementation stays inside `core` plus the metric registrations in `std/telemetry`. No frozen port method changes; `EventMeta` and `stores.AuditRecord` grow additively. Shadow enforcement covers the native run path; foreign runtimes are not built with `WithShadowMode`.
