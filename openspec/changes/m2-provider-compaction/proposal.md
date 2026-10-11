# Proposal: m2-provider-compaction

## Why

The `context` capability names two compaction implementations — harness-side `Summarize` and adapter-side `ProviderCompact` (`openspec/specs/context/spec.md`, *Contract*, "Two stages with different persistence") — and two of its scenario ids still have no behaviour: `context.opaque-bound-to-model` and `context.opaque-fallback-rejected` (`openspec/changes/m2/design.md:66-67`).

`m2-context-persisted-compaction` landed the seam this change needs and deferred exactly this work: `stores.Compactor` now returns `types.Compaction` (`core/stores/projection.go:24-33`), `std/context.Summarize` is the single implementation (`std/context/summarize.go`), and the assembly collapse renders the newest block (`core/native_effects.go:337-372`). Its Non-Goals put `ProviderCompact`, `Caps.Compaction` handling and the `Build` fallback rejection in this package (`openspec/changes/m2-context-persisted-compaction/design.md`, Non-Goals).

Two gaps remain in the tree. No adapter implements `stores.Compactor`: `ProviderCompact` appears only as a build-time flag (`core/build.go:62-63`) and in the fidelity gate (`core/build_fidelity.go:53`), and `Caps.Compaction` (`core/types/model_profile.go:56-61`) is read only by that gate. And the collapse ignores the block's kind and model: `collapseCompactionHistory` inserts `current.Summary` for every selected `Compaction` (`core/native_effects.go:337-360`), so a `CompactionOpaque` block bound to another endpoint reaches the model as an empty summary instead of being dropped and re-fitted — the rule `core/fidelity_projection.go:75-81` already states for block projection.

## What Changes

- Add `ProviderCompact` as an adapter-side `stores.Compactor` in the adapters that ship a provider compaction primitive: `adapter/anthropic` (the `compact` primitive — a readable summary block) and `adapter/openai` (`/responses/compact` — an opaque encrypted item passed as-is). Each produces the kind its profile's `Caps.Compaction` declares, records the endpoint identity in `Compaction.Model`, and declares the mode it serves. The caller binds it through `ContextPolicy.Compactor`, exactly as `WithContextPolicy` binds `Summarize` (`core/build_options.go:240-247`); core constructs neither.
- Make the persisted-compaction view model-aware: the collapse takes the assembled endpoint's identity from `types.AssembleInput.Profile` (`core/types/assembly.go:29-38`) and drops a `CompactionOpaque` block whose `Compaction.Model` is not that endpoint, leaving the messages it covered visible so the request re-fits through the declared projections and the policy compactor. The predicate is shared with the existing `keepBlock` rule (`core/fidelity_projection.go:75-81`) instead of restated.
- Bind both owned ids on the public path: `context.opaque-bound-to-model` through `Build`/`Send` on the native and foreign paths, and `context.opaque-fallback-rejected` through `Build` (the gate already ships in `core/build_fidelity.go:35-58` and is reached from `Stack.resolveNative`, `core/native_resolve.go:154`).
- **No Contract text changes.** The delta reproduces the *Compaction is persisted* requirement verbatim, because the behaviour it names is already normative and the two ids already exist; the change closes them rather than rewriting them (`openspec/specs/context/spec.md:131-166`).

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `context`: close the two `CompactionOpaque` scenarios the *Compaction is persisted* requirement already carries — the model-bound drop on endpoint change (re-fit with projections and `Summarize`), and the `Build` refusal of `ProviderCompact` under a cross-provider `Fallback`. The requirement text is reproduced verbatim; no scenario id is added or renamed.

## Impact

The package touches `adapter/anthropic` and `adapter/openai` (a `ProviderCompact` `stores.Compactor` beside the existing `Model`/`Provider`, over the same client and credentials seam — `adapter/openai/openai.go:19-45`, `adapter/anthropic/anthropic.go:29-47`), `core/native_effects.go` (the model-aware collapse), and `core` tests (`core/compaction_assembly_test.go`, `core/compaction_public_path_test.go`) plus adapter tests. `core/fidelity_projection.go` gains one shared predicate and no behaviour change. No store, no publication payload, no audit hash and no `Build` gate changes: the rejection already ships and stays as it is.

Out of scope, deliberately: the M4 `Summarize` policy surface (`PromptSet.Compaction`, tuning, policy source — `openspec/changes/m2/design.md` §3 item 2), calibration and overflow retry (`m2-context-calibration-overflow`), and the persisted-compaction publication and reconciliation machinery (`m2-context-persisted-compaction`).
