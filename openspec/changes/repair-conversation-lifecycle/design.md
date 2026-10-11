# Design

## Context

See `proposal.md` for motivation. The conversation lifecycle lives in
`core`. `Conversation` is defined in `core/conversation.go`, and both
`NewNativeConversation` (`core/native_conversation.go:19`) and
`NewForeignConversation` (`core/foreign_conversation.go:133`) build the
`conversation` struct. `conversation.stack` exists
(`core/conversation.go:60`) but neither constructor sets it, and neither
binds `tenant`, `titleProfile`, `flags` or `frozen`
(`core/conversation.go:104-109`). `admitRun`
(`core/conversation.go:127`) is a no-op without a stack.

Shutdown state is kept in package-level maps keyed by `*Stack`
(`core/shutdown.go`): `shutdownState` and `inflight`. `beginRun`
(`core/shutdown.go:80`) reads the down flag, unlocks, then registers under a
different mutex, so `Shutdown` can arm the set in between.

`Lifecycle.drive` (`core/drive_lifecycle.go:411`) handles
`*types.HandoffError` twice: once at the error branch around line 485 and
again around line 530. `finishTurn` (`core/drive_lifecycle.go:958`) accepts
a `terminal types.StopReason` but passes `types.StopCompleted` to both
`finishRun` (line 977) and `done` (line 1002). `MemoryRuns.noticeFor`
(`core/stores/runs_notices.go:56`) maps the recorded reason to
`StopCompleted` by default.

`SessionRunFinder` exists (`core/conversation.go:44`) and `find`
(`core/conversation.go:466`) uses it, but no shipped runs store implements
`RunForSession`; only the test fixture does
(`testkit/conformance/flow.go:184`). `core/identity_detach_scenarios_test.go`'s
`runDetachedReissue` closes the runtime gate before reading the run ID
through `liveRunID`.

## Goals / Non-Goals

**Goals:**

- Bind the full stack state on both constructors.
- Make shutdown admission atomic.
- One handoff terminal transition with a once-only callback.
- Preserve the real terminal `StopReason`.
- Ship cross-pod run discovery and expose `ActiveRun`.
- Repair the detached credential fixture without widening a timeout.

**Non-Goals:**

- No new capability category.
- No Postgres concrete store implementation; that is
  `repair-postgres-store-contracts`.
- No change to `Send`, `Resume` or `Recover` signatures.
- No retry or fallback framework.

## Decisions

### D1: Shared constructor binding helper

Both constructors MUST call one helper that binds `stack`, `tenant`,
`titleProfile`, `flags` and `frozen` before the option loop. Alternative
considered: duplicate per-constructor wiring. Rejected because the two
constructors already drifted.

### D2: One mutex for admission

The shutdown flag and the in-flight registry MUST be guarded so a run is
either refused or registered under one lock. Alternative considered: keep
two maps and an ordering rule. Rejected because the race is the defect.

### D3: `ActiveRun` as a new handle method

`Conversation` gains
`ActiveRun(ctx context.Context, sessionID string) (string, error)`. It
applies the same owner check as `Cancel` (`checkSteerOwner`), then resolves
the in-process live map, then `SessionRunFinder`. Active means state
`Running` or `Resuming` with a live lease. `Suspended`, terminal and
lease-expired runs are inactive and report `types.ErrRunNotActive`. `Send` is
unchanged. Alternative considered: reuse idempotency keys for discovery.
Rejected: the locked decision forbids requiring silent idempotency keys.

### D4: Runtime composition lands before the lifecycle edits

`restore-runtime-governance` edits the same file
(`core/foreign_conversation.go`) for the governed tool wrapper and the
caller-derived run context. That package MUST land its edit first. This
change then rebinds the foreign constructor in `NewForeignConversation`
(`core/foreign_conversation.go:133`) without reverting the governed wrapper
or the run context. Both edits must survive; a lifecycle edit that drops
either one is rejected. The landing order is proven by the source check in
task 1.5 plus the driver integration evidence; a commit-ancestor check is
not required when the driver delivers the packages without commits.

### D5: One handoff branch

The duplicate `*types.HandoffError` branch MUST collapse into one helper
that persists the `HandoffContext`, sets `ControlHandoffRequested`, audits
`handoff_requested`, fires the optional callback once and closes the run
with `StopHandedOff`. Each optional store step is guarded independently, so
an absent store skips only its own step.

### D6: Terminal reason flows through

`finishTurn` MUST pass its `terminal` argument to `finishRun` and `done`.
`MemoryRuns.noticeFor` MUST return the recorded reason. The cache-write
gate keeps using the completed reason check.

### D7: Memory `RunForSession` and optional interface

`MemoryRuns` gains `RunForSession` returning the session's active run — state
`Running` or `Resuming` with a live lease — or `stores.ErrRunNotFound`. The
`Runs` port does not gain a method; the seam stays the optional
`SessionRunFinder` interface. `repair-postgres-store-contracts` implements
the same interface and the same active rule; this change does not define the
Postgres query.

### D8: Fixture captures the ID before the gate

`runDetachedReissue` MUST read the run ID through `ActiveRun` before closing
`rt.gate`. The run must be `Running` or `Resuming` with a live lease at that
moment. The timeout is not widened.

## Risks / Trade-offs

- Contract edit for `Conversation` and the testkit conformance fixture must
  land together at apply time, or downstream fixtures stop compiling.
- Cross-pod `ActiveRun` depends on an optional finder; a store without it
  reports `ErrRunNotActive` for a run on another pod. This is reported, not
  hidden.
- Preserving the terminal reason can change an existing notice reason from
  `StopCompleted` to the real reason; consumers keyed on `StopCompleted` must
  accept the more precise word.

## Migration Plan

At apply time: edit the `streams` handle Contract to add `ActiveRun`,
register the 27 scenario ids in `openspec/scenarios.json`, synchronize the
`streams`, `runtime`, `flow`, `stores` and `identity` main specs,
regenerate the types index, update the testkit conformance runs fixture, and
record the design decision in an ADR chosen at apply time. The ADR and the
generated index are driver-owned. No ADR number is reserved during
authoring. No data migration is required. `M1` remains the completed
regression floor; global `M2` is a milestone gate, not a lane gate.

## Seal requirements

1. Confirm `beginRun` (`core/shutdown.go:80`) and `MemoryRuns.RunForSession`
   keep the active rule in one place: state `Running` or `Resuming` with a
   live lease, and `stores.ErrRunNotFound` otherwise. `ActiveRun` maps the
   miss to `types.ErrRunNotActive`; it does not redefine the rule.
2. Record the owner-authorization path exactly: `ActiveRun` applies the same
   check as `Cancel` (`checkSteerOwner`) and returns `ErrSessionForbidden`
   without disclosing a run id.
3. Map every task in `tasks.md` to a named observable transition assertion,
   or mark it internal-only with its internal observable. Task 1.5 is the
   only internal-only task in this package.
4. `openspec validate repair-conversation-lifecycle --strict
   --no-interactive` passes before the handoff is sealed.
