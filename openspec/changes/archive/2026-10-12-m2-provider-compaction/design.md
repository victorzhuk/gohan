# Design: m2-provider-compaction

## Context

See `proposal.md` — Why. The behaviour is already normative: `openspec/specs/context/spec.md` (*Contract* rule 4; scenarios `context.opaque-bound-to-model`, `context.opaque-fallback-rejected`). This design decides the seams that connect it to the live tree. Read against the tree on 2026-10-11:

- **The compactor seam ships; one implementation exists.** `stores.Compactor.Compact(ctx, h, p) (types.Compaction, error)` (`core/stores/projection.go:24-33`); `std/context.Summarize` is the only implementation (`std/context/summarize.go`). The consumer is `runCompactionStage` (`core/compaction_stage.go:273-300`), which reads `c.compactor` from the resolved flow.
- **The compactor is caller-bound.** `WithContextPolicy` binds one policy to every native flow (`core/build_options.go:240-247`); `NativeSpec.ContextPolicy` binds it per flow (`core/native_options.go:36-43`); the resolved seams are validated by `activeCompactor` (`core/native_resolve.go:64-88`).
- **`Caps.Compaction` is declared and nearly unused.** `CompactionMode` with `CompactionNone`/`CompactionTextCap`/`CompactionOpaqueCap` (`core/types/model_profile.go:114-122`) is a `Caps` field (`core/types/model_profile.go:56-61`); the only non-test reader is the fidelity gate.
- **The `Build` rejection already ships.** `CheckFidelity` rejects `ProviderCompact && p.Caps.Compaction == CompactionOpaqueCap && p.Name != fb.Name` with `ErrOpaqueCompaction` naming flow, profile and `"provider_compact"` (`core/build_fidelity.go:35-58`), and `Stack.resolveNative` calls it (`core/native_resolve.go:154`), so it is reachable through `Build`. Its test calls `CheckFidelity` directly (`core/build_fidelity_test.go:90`), so the `Build` path itself is unproven.
- **The collapse ignores kind and model.** `collapseCompactionHistory(messages []types.Message)` selects the newest `Compaction` and inserts `current.Summary` at the head for every kind (`core/native_effects.go:337-360`), inside `collapseBeforeProjections` (`core/native_effects.go:328-335`), which runs before `withProjections` and the caller assembler. `types.AssembleInput` carries `Profile ModelProfile` (`core/types/assembly.go:29-38`), so the target identity is reachable without widening the assembler port.
- **The block carries what binding needs.** `types.Compaction{BlockBase, CoversUpTo, Kind, Summary, Opaque Raw, Model string, Tokens int}` (`core/types/message.go:156-172`); `CompactionOpaque` is a registered kind (`core/types/message.go:166-170`, codec at `:408-442`).
- **The binding rule already exists once.** `keepBlock` drops a `CompactionOpaque` whose `Model` is not the projection target (`core/fidelity_projection.go:75-81`). `ProjectFidelity` has no non-test caller today, so the collapse is the live renderer and must apply the same rule rather than a second convention.
- **Provider primitives.** Anthropic `compact` returns a summary block with custom instructions and atomically replaces the transcript; OpenAI `/responses/compact` returns an opaque encrypted item that must be passed as-is and is never pruned (`docs/design/evidence.md:16`).
- **Adapters hide their client behind a transport seam.** `adapter/openai.Config`/`Transport` and `Provider.Profile()` (`adapter/openai/openai.go:19-45`); `adapter/anthropic.Config` and `Model.Profile()` (`adapter/anthropic/anthropic.go:29-47`). A compactor can be proven against a stub transport without a live call.
- **Scenario registration.** The public-path suite is `TestCompactionPublicPath` (`core/compaction_public_path_test.go:179`), subtests named exactly by scenario id; the collapse unit suite is `TestCollapseCompactionHistory` (`core/compaction_assembly_test.go:59`). `m2-context-persisted-compaction` task 8.3 reserves the two opaque ids for this package.

## Goals / Non-Goals

**Goals:** ship `ProviderCompact` in the adapters that own a provider compaction primitive, with a declared mode; make the persisted-compaction view drop an opaque block bound to another endpoint and re-fit; bind `context.opaque-bound-to-model` on the native and foreign public paths and `context.opaque-fallback-rejected` through `Build`.

**Non-Goals:** no Contract text change — the delta reproduces the requirement verbatim and adds no scenario id; no change to `Summarize`, the safe-prefix selection, the publication payload, the audit hash, the reconciliation protocol, or the three existing `Build` refusals for an active compaction (`core/native_resolve.go:64-88`); no M4 `Summarize` policy surface (`PromptSet.Compaction`, tuning, policy source — `openspec/changes/m2/design.md` §3 item 2); no calibration, `State.Calibration`, or overflow retry (`m2-context-calibration-overflow`).

## Decisions

### D1 — `ProviderCompact` is one adapter-side `stores.Compactor` per primitive, constructed by the caller

Each adapter ships its own value: `adapter/anthropic` over the `compact` primitive, `adapter/openai` over `/responses/compact`. Both live in their module beside the existing model type, take that module's existing `Config` (client, credentials, profile) and implement `Compact(ctx, h, p) (types.Compaction, error)`. Core imports neither: `Compactor` is a `core/stores` interface, so an adapter module may satisfy it, and the caller binds it through `ContextPolicy.Compactor` (`core/build_options.go:240-247`). Alternative rejected: one `std/context.ProviderCompact` delegating to a new provider port — it would move provider wire shapes behind a `std` port, and the Contract places the implementation adapter-side.

### D2 — The produced kind follows `Caps.Compaction`, and the compactor declares the modes it serves

`Compact` reads `p.Caps.Compaction` (`core/types/model_profile.go:56-61`): `CompactionTextCap` produces a `CompactionText` block whose `Summary` blocks carry `Origin{Kind: OriginModel}`; `CompactionOpaqueCap` produces a `CompactionOpaque` block whose `Opaque` raw holds the returned item. `CompactionNone`, and any mode the adapter does not serve, is refused with an error naming the profile and the mode; it is never silently downgraded to text. Each compactor also exposes a served-mode declaration so a caller pairs compactor and profile by declaration rather than by assumption. The block always records `Model: p.Name` and `CoversUpTo: h.Version`, and folds only the covered history it is handed, never the full log.

### D3 — The view drops an opaque block bound to another endpoint and re-fits

`collapseCompactionHistory` gains the assembled endpoint identity and drops the selected block when it is `CompactionOpaque` and `Model` is not that endpoint; it then renders no summary at the head and leaves the messages at or below `CoversUpTo` visible, so the request is re-fitted through the declared projections and the policy compactor (`Summarize`), as the scenario states. A `CompactionText` block is not model-bound and keeps today's behaviour. The predicate is one shared function beside `keepBlock` (`core/fidelity_projection.go:75-81`), used by both the collapse and the projection filter, so the tree holds one binding rule.

### D4 — The target identity comes from `AssembleInput.Profile`, and the endpoint that assembles owns the decision

`collapseBeforeProjections` already receives `types.AssembleInput` (`core/native_effects.go:328-335`), which carries `Profile ModelProfile` (`core/types/assembly.go:29-38`); the collapse reads `in.Profile.Name`. Assembly is per endpoint — the `model` spec requires a fallback target to get its own context policy before the request is sent (`openspec/specs/model/spec.md:290`, `:450`) — so a block bound to the primary is dropped when the fallback endpoint assembles. Contingency: if the router reuses the primary assembly for a fallback endpoint, the target name must be threaded at that re-assembly point before the drop is meaningful. Task 2.3 verifies reachability on the live path and fixes the wiring if the profile is stale there; that is a wiring correction, not a new decision.

### D5 — The `Build` rejection is already correct; the change binds it, it does not re-implement it

`CheckFidelity` (`core/build_fidelity.go:35-58`) already refuses the cross-provider case with an error naming flow, profile and `"provider_compact"`, and `Stack.resolveNative` calls it during `Build` (`core/native_resolve.go:154`). This change adds the `context.opaque-fallback-rejected` subtest through `Build` and leaves the gate untouched. Re-implementing the check inside `ProviderCompact` would move a build-time decision to call time and duplicate the rule. The existing `build.opaque-compaction-fallback` subtest (`core/build_fidelity_test.go:90`) stays owned by the `build` capability and must keep passing unchanged.

### D6 — Subtests bind by scenario id on the existing suites

`context.opaque-bound-to-model` is registered in `TestCompactionPublicPath` (`core/compaction_public_path_test.go:179`) with a native-flow variant and a foreign-flow variant, and the drop is unit-proven in `TestCollapseCompactionHistory` (`core/compaction_assembly_test.go:59`). Adapter compactors are proven against each adapter's stub transport, so no live provider call is evidence. No new suite is created and no existing subtest is renamed.

## Risks / Trade-offs

- [The `compact` and `/responses/compact` wire shapes are unverified against live providers] → prove each compactor through the existing `Transport` seam (`adapter/openai/openai.go:29-32`) with fixture bodies; the declared `Caps.Compaction` is what `Build` validates, so shape drift fails an adapter test rather than a run.
- [Dropping the opaque block un-hides covered history and can overflow] → the re-fit runs projections and `Summarize`; a request still over budget follows the fail-closed path already in `runCompactionStage` (`core/compaction_stage.go:273-300`). No new retry is invented.
- [Two assemblers could hold stale target profiles] → D4's task 2.3 asserts the endpoint whose profile reaches the collapse equals the endpoint that sends the request.
- [A caller could pair a text compactor with an opaque profile] → D2 refuses the mismatched mode at `Compact`, naming the profile, so the view never meets a kind/model combination it did not expect.

## Migration Plan

Additive first: the adapter compactors and the shared predicate land without changing behaviour (a log with no opaque block collapses exactly as today). The model-aware drop is the only behavioural change, confined to blocks bound to another endpoint. Rollback is reverting the collapse wiring. No stored shape, event, or goose migration is involved, so there is no data migration.

## Open Questions

None that change the specs, the approach, or the task breakdown.
