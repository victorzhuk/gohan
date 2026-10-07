# Design: m1-flags-control

## Context

The `flags` contract already fixes the two classes, the freshness rule, the frozen snapshot's placement and the replay assertion (openspec/specs/flags/spec.md:41-44). ADR-0078 owns the port shape and ADR-0079 owns the stale-control behaviour (docs/adr/0078-flags-a-core-flags-port-with-two-classes-frozen-rollout.md, docs/adr/0079-stale-emergency-control-state-beyond-a-freshness-limit-or.md), so this change implements a settled decision.

The constraints the approach must respect:

- The permission gate already defines the live-deny slot and the ordering: missing scopes deny, then `WithLiveDeny`, then the taint hook, then session grants, then the decider (core/permission/gate.go:139-176). The shared M1 contract fixes the same order — hard and live denies, taint hook, grants, decider (openspec/changes/m1/design.md:239, ADR-0155 §6).
- `runtime.State.Flags` is a shipped checkpoint field of type `runtime.FlagSnapshot = map[string]any` (core/runtime/runtime.go:23,33); nothing populates it. `RunInfo` has no `Flags` field (core/types/identity.go:70-86).
- An `AwaitingControl` resume accepts only the empty wake, already enforced in `Resume` (core/resume.go:117), and the suspension contract names the flags freshness watcher or the `Waker` maximum as the resumer (openspec/specs/suspension/spec.md:89).
- No `ErrFlagDrift` exists anywhere in the tree; no `std/flags` package exists.
- The frozen snapshot's consumers are existing seams — `Router`, `ToolFilter`, `PromptRef.Label`, `RunLimits`, chain step `Applies` (openspec/specs/flags/spec.md:41).

## Goals / Non-Goals

**Goals:** ship the `Flags` port and its value types in `core`, give the frozen snapshot a carrier on the run, the checkpoint and the audit record, install live control in the gate's live-control slot for tool calls and in a `std/flags` model-chain step for model calls, suspend and deny on stale or default control state, and assert the snapshot on replay. Ship `std/flags` with the static/env provider, the snapshot logic and the watcher.

**Non-Goals:** no OpenFeature adapter, no remote flag store, no per-session flag overrides. `adapter/openfeature` is explicitly later than M1 (openspec/changes/m1/design.md:210). This change does not re-decide ADR-0078 or ADR-0079 and does not add a new ADR.

## Decisions

### D1 — Core owns the port; `std/flags` owns provider values and the snapshot logic

`core/types` gains `Flags`, `FlagContext` and `Freshness`, matching the contract block (openspec/specs/flags/spec.md:14-45). The provider that answers them, the frozen evaluation at run start and the freshness watcher live in `std/flags`, under the core-budget rule that keeps policy values and provider code out of `core` (docs/design/architecture.md §4.2a, ADR-0155 §2). `core` declares the port and the ordering rules; the host that never declares flags pays nothing.

### D2 — The frozen snapshot lives on `RunInfo.Flags`, the checkpoint and the run-start audit record

The frozen values are evaluated once at run start with the session hash as the targeting key and written to three places: `RunInfo.Flags` (new field), the checkpoint `runtime.State.Flags` (the shipped `runtime.FlagSnapshot`, reused so the checkpoint shape is unchanged), and a run-start audit record. Every in-run evaluation reads the snapshot rather than the provider, so `steer` and `cancel` never re-evaluate and mid-run store changes never alter a live run — `flags.frozen-snapshot`. The contract block that declares the port (openspec/specs/flags/spec.md:14-37) is marked `(illustrative)`, so the carrier type is the design's to pin: `RunInfo.Flags` is `runtime.FlagSnapshot` (`map[string]any`), the same type the checkpoint field reuses, and the delta body records it. The `RunInfo` block of `openspec/specs/identity/spec.md` (6.4, normative) is edited in the same implementation commit so the contract gains the `Flags` field; the change ships an `identity` delta that declares it.

### D3 — Live flags plug into the gate's live-control slot (an action) and a `std/flags` model-chain step

The gate today exposes a deny-only slot, `WithLiveDeny(check func(ctx, *ToolInvocation) (reason string, denied bool))` (core/permission/gate.go:87-89, invoked at core/permission/gate.go:166-170). The live requirement is wider than a denial — a live flag can disable a tool, force `Ask` or block a flow — so the gate's slot is upgraded to an action-returning one in the shape the taint hook already uses: `WithLiveControl(check func(ctx, *ToolInvocation) (types.TaintAction, string))` returning `types.TaintAllow`, `types.TaintAsk` or `types.TaintDeny` (core/types/taint.go:11-16, 52). It runs after the hard scope deny and before the taint hook (core/permission/gate.go:139-176), so it satisfies `flags.never-widen` and the ordering decision: a live `Deny` beats a taint `Ask` and requests no approval, a live `Ask` forces suspension exactly as a taint `Ask` does, and neither can enable a tool wiring did not register, raise an effect or override a hard block. `flags` grants no scopes and no effects; an approval cannot override a live deny.

The model-call half of the requirement has no gate to hang on, so `std/flags` ships a model-chain step placed beside the ordering rules `core/chains/model_chain.go` already validates (core/chains/model_chain.go:21-25). The step runs before every model call, performs the live evaluation and, on a default or stale evaluation, suspends the pending effect as D4 describes. A flow with no provider wires neither the gate check nor the step and evaluates no flags.

### D4 — Default or stale live state suspends the pending effect with `AwaitingControl`

A live evaluation carries `EvaluatedAt` and `FromDefault`. When `now - EvaluatedAt > FreshnessLimit` or `FromDefault` is true, the next effect suspends with `AwaitingControl` (ADR-0079) through the existing suspension machinery: `SuspendError` with the control reason, an empty-wake token, and a `Waker` schedule at `MaxControlWait`. Both live seams suspend the same way: the gate check suspends the pending non-`ReadOnly` tool call, and the `std/flags` model-chain step (D3) suspends the pending model call before it is issued. The freshness watcher resumes with the empty wake once fresh state arrives; `Resume` already accepts only that wake for the reason (core/resume.go:117) — `flags.from-default-suspends`.

### D5 — Past `MaxControlWait` the effect is denied and the run continues

If no fresh state arrives within `MaxControlWait`, the effect is denied `Failed(Permanent)` and the run continues rather than suspending forever — `flags.stale-then-deny`. In-flight effects follow existing completion rules; the denial applies to the next effect only.

### D6 — Replay asserts the recorded snapshot and returns `ErrFlagDrift`

`Replay` asserts `RunInfo.Flags` against the current provider before any model call. A snapshot the provider cannot honour returns the new `ErrFlagDrift` error class instead of re-evaluating, so a replayed run observes the values its first execution observed — `flags.replay-asserts-snapshot`. The error is checked at the replay boundary, ahead of the model call, matching the contract sentence (openspec/specs/flags/spec.md:44).

### D7 — `std/flags` ships a static/env provider, the snapshot logic and the freshness watcher

One package, `std/flags`, provides the provider contract, a static/env provider returning `Freshness{EvaluatedAt, FromDefault}`, the run-start snapshot logic and the freshness watcher. It ships the pieces the contract names (openspec/specs/flags/spec.md:44) and stops there: no OpenFeature SDK, no network provider, no adapter.

### D8 — Wiring is a Build-time option pair; no second builder

The provider and the freshness watcher reach a flow through `Build` options, following the existing option pattern (e.g. `WithEgressClient`, core/build_options.go). A flow that declares no provider keeps today's behaviour and evaluates no flags.

## Risks / Trade-offs

- [Risk] A frozen snapshot read from a live provider mid-run would reintroduce drift. → D2 reads the snapshot, never the provider, inside a run; only run start and `Replay` consult the provider.
- [Risk] A live seam wired after the taint hook would let a flag widen a taint deny. → D3 uses the pre-taint live-control slot (and the `std/flags` model-chain step); `flags.never-widen` fails any wiring that can raise an effect or follow the taint hook.
- [Risk] `AwaitingControl` suspended forever when no watcher is configured. → D5 caps the wait at `MaxControlWait` and denies; the watcher is an optimisation, not the safety bound.
- [Risk] Replay re-evaluating produces a run that diverges silently. → D6 fails fast with `ErrFlagDrift` before any model call.

## Migration Plan

No stored data migrates: the frozen carrier reuses the shipped `runtime.FlagSnapshot` checkpoint field and adds one `RunInfo` field. A flow that declares no flags provider is unaffected and evaluates nothing; the live slot installs only when a provider is wired. The `RunInfo` block of `openspec/specs/identity/spec.md` gains the `Flags` field in the same implementation commit (the package ships an `identity` delta declaring it), and `docs/design/types.md` is regenerated in that commit. The module stays pre-v1 and M1 cuts no release tag (openspec/changes/m1/design.md:27). The identity delta adds one sentence to the Identity requirement; the archive merge must union it with the other change's identity delta, not replace the body.

## Open Questions

None. The OpenFeature adapter and any remote store are out of M1 by the milestone matrix and change neither the specs, the approach nor the task breakdown.
