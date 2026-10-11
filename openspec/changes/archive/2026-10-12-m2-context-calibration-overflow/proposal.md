# Proposal: m2-context-calibration-overflow

## Why

The `context` capability's *Counting and calibration* requirement names three scenario ids that still have no behaviour: `context.calibration-from-usage`, `context.calibration-persisted-for-replay` and `context.overflow-compacts-and-retries-once` (`openspec/specs/context/spec.md:109-126`; owners in `openspec/changes/m2/design.md:69-71`). The seams already ship but are inert. `runtime.State.Calibration` is carried, checkpointed and cloned (`core/runtime/runtime.go:35`, `core/checkpoint_envelope.go:207-208`, `core/drive_lifecycle.go:410-414`) yet no code ever writes or reads it. `types.ContextBudget` accepts a calibration ratio that the compaction stage always passes as zero (`core/types/context_budget.go:16`, `core/compaction_stage.go:282`), so the decision never uses reported usage. And no overflow path retries: a `ClassContextOverflow` from the provider (`core/types/errors.go:196`) leaves the streaming loop as an ordinary failure (`core/drive_turn.go:369-383`).

`m2-context-persisted-compaction` landed the persisted compaction this change builds on and deferred exactly this work: its Non-Goals ship no calibration ratio, no `State.Calibration` write, no provider-usage trigger and no overflow retry, and name this package as their owner (`openspec/changes/m2-context-persisted-compaction/design.md:25`).

## What Changes

- **Calibrate each profile's estimator from reported usage.** After a model call that reports `Usage.InputTokens` (and not `Estimated`), set `State.Calibration[profile] = clamp(InputTokens / estimate, 0.5, 2.0)` and record `gohan.context.estimate_error{profile}`.
- **Apply the calibration at the compaction decision.** The stage scales the effective ceiling by the calibration factor, so a profile that reports 1.2× its estimate compacts sooner. The estimate multiplier (the Contract's ratio) and the shipped `ContextBudget` window scale are reciprocals.
- **Persist the ratio with the checkpoint.** The calibration travels on `runtime.State`, which the checkpoint envelope already permits (`core/checkpoint_envelope.go:207-208`); a replay on another pod reads it back and reaches the same decision. No new store, field or migration.
- **Add one overflow retry.** A `ClassContextOverflow` for a request the budget accepted raises the ratio (×1.25 today — `ModelError` carries no reported factor, `core/types/errors.go:204-211`), counts `gohan.context.overflow_retry{profile}`, compacts once with the raised ratio and retries the request once; a second overflow fails the turn `Permanent`.
- **No Contract text changes.** The delta reproduces the *Counting and calibration* requirement verbatim, because the behaviour it names is already normative and its three ids already exist; this change closes them rather than rewriting them (`openspec/specs/context/spec.md:109-126`).

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `context`: close the three *Counting and calibration* scenarios the requirement already carries — estimate calibration from reported usage, the ratio's persistence across a replay, and the single overflow compaction-retry that fails `Permanent` on the second overflow. The requirement text is reproduced verbatim; no scenario id is added or renamed.

## Impact

The change touches `core` only: `core/drive_turn.go` (the reported-usage capture and the bounded overflow retry inside `modelEffect`, `:295-383`), `core/compaction_stage.go` (the calibration-scaled ceiling at `:282` and the raised-ratio retry path), `core/drive_lifecycle.go` (the state write the checkpoint already carries, `:410-414`), and their tests. `core/types/context_budget.go` is read, not changed: its formula is frozen, so the calibration is expressed through its existing `ratio` parameter. No store shape, event, goose migration, audit hash or `Build` gate changes.

Out of scope, deliberately: the M4 `Summarize` policy surface (`PromptSet.Compaction`, tuning, policy source — `openspec/changes/m2/design.md` §3 item 2), provider compaction (`m2-provider-compaction`), and the persisted-compaction publication and reconciliation machinery (`m2-context-persisted-compaction`).
