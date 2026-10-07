# Proposal: m1-flags-control

## Why

The `flags` contract declares a core `Flags` port with two flag classes, the freshness rule for live control, the frozen snapshot's placement and five registered M1 scenarios, but no code implements any of it. `core/types` ships no `Flags`, `FlagContext` or `Freshness` value (openspec/specs/flags/spec.md:14-45), `RunInfo` carries no `Flags` field (core/types/identity.go:70-86), no `ErrFlagDrift` error class exists, `std/flags` does not exist, and the permission gate ships only a deny-only live-deny slot (`WithLiveDeny`, core/permission/gate.go:87-89) — the change upgrades that slot to an action-returning live control and adds the model-call half in `std/flags`. The checkpoint carrier ships unused: `runtime.State.Flags` is declared and nothing populates it (core/runtime/runtime.go:23,33).

The milestone matrix assigns the five IDs `flags.frozen-snapshot`, `flags.never-widen`, `flags.stale-then-deny`, `flags.from-default-suspends` and `flags.replay-asserts-snapshot` to `m1-flags-control`, and the obligation `std/flags` with `adapter/openfeature` later (openspec/changes/m1/design.md:129-136, 210). ADR-0078 and ADR-0079 own the contract (docs/adr/0078-flags-a-core-flags-port-with-two-classes-frozen-rollout.md, docs/adr/0079-stale-emergency-control-state-beyond-a-freshness-limit-or.md), so this lane supplies the implementation, not a new decision.

## What Changes

- **Added** the `Flags` port and its value types (`Flags`, `FlagContext`, `Freshness`) in `core`, and the `Flags` field on `RunInfo`, so a frozen value is readable from the run context and survives `steer` and `cancel`.
- **Added** `std/flags`: the static/env provider, the snapshot logic that evaluates frozen flags once at run start and writes `RunInfo.Flags`, the checkpoint `State.Flags` and the run-start audit record, and the freshness watcher.
- **Added** live control: the gate's live-deny slot is upgraded to an action-returning live-control check (a live flag can deny, force `Ask` or allow), and a `std/flags` model-chain step performs the same evaluation before every model call. Both run after the hard scope denies and before the taint hook, so a flag can only disable a tool, force `Ask` or block a flow, and never widens a hard or taint deny (openspec/changes/m1/design.md:239).
- **Added** the `AwaitingControl` suspension for a default or stale live evaluation, resumed by the freshness watcher with the empty wake, denied `Failed(Permanent)` once `MaxControlWait` elapses (core/resume.go:117; openspec/specs/suspension/spec.md:89).
- **Added** the `ErrFlagDrift` error class and the replay assertion that returns it before any model call.

Not in this lane: no OpenFeature adapter, no remote flag store, no per-session flag overrides, and no second builder. The frozen carrier reuses the shipped `runtime.FlagSnapshot`, so the checkpoint shape does not change.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `flags`: the *Frozen and live flags* requirement gains its normative body and its implementing text in `openspec/specs/flags/spec.md:47-75`; its five scenario IDs keep their WHEN/THEN lines verbatim.
- `identity`: the *Identity* requirement's `RunInfo` block (6.4) gains the `Flags` field; the delta reproduces the requirement and its eleven scenario blocks verbatim and adds only that field.

## Impact

The design affects `core/types` (the port and the `RunInfo.Flags` field), `core/permission/gate.go` (the upgraded live-control slot), `core/resume.go` (the `AwaitingControl` wake), the replay path, and a new package `std/flags` (the provider, the snapshot logic, the freshness watcher and the model-chain step). It adds no third-party dependency. The `RunInfo.Flags` field edits the `RunInfo` block of the `identity` contract (`openspec/specs/identity/spec.md` 6.4) and the generated `docs/design/types.md` in the same commit; `adapter/openfeature` stays out of M1 (openspec/changes/m1/design.md:210).
