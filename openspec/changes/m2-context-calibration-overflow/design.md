# Design: m2-context-calibration-overflow

## Context

See `proposal.md` — Why. The behaviour is already normative: `openspec/specs/context/spec.md` (*Contract*, rule 7 "Counting and calibration" at `:64`, rule 8 "Overflow backstop" at `:65`; requirement *Counting and calibration* at `:109-126`; scenarios `context.calibration-from-usage`, `context.calibration-persisted-for-replay`, `context.overflow-compacts-and-retries-once`). This design decides the seams that connect it to the live tree. Read against the tree on 2026-10-11:

- **The ratio slot ships and is never written.** `runtime.State.Calibration map[string]float64` (`core/runtime/runtime.go:35`) is an allowed envelope key (`core/checkpoint_envelope.go:207-208`), is deep-cloned on the checkpoint path (`core/drive_lifecycle.go:410-414`), and is restored by `decodeCheckpoint` (`core/checkpoint_envelope.go:60`). Nothing sets or reads a value.
- **`ContextBudget` scales the window by `ratio`.** `types.ContextBudget(p, req, est, ratio)` computes `window = p.ContextWindow`, then `window = int(float64(window) * ratio)` for a positive ratio, then `Limit = window − Reserved` (`core/types/context_budget.go:16-38`). Its comment records that the margin and rounding are frozen by row 19.2 with "no spec formula to lean on" (`core/types/context_budget.go:5-7`).
- **The compaction stage passes a zero ratio.** `runCompactionStage` measures `before, err := c.compactionCount(ctx, req)` (`core/compaction_stage.go:278`) and compares it against `types.ContextBudget(c.profile, req, nil, 0).Limit` (`:282`); the same zero-ratio budget governs the fallback fit check (`:441`). The ratio slot is therefore dead.
- **The count seam.** `turnConfig.compactionCount func(ctx, req) (int, error)` (`core/drive_turn.go:56-58`) is resolved by `compactionCountOf` as the model's `TokenCounter` when the model implements the port, otherwise the declared estimator (`core/native_resolve.go:98-110`), and is bound only for a flow with an active compactor (`core/native_resolve.go:71`, `:241`). The declared estimator is `Stack.estimator`, set by `WithEstimator` (`core/build_options.go:212-214`).
- **The call site.** `modelEffect` (`core/drive_turn.go:295-383`) assembles (`:314`), runs the stage (`:318`), then ranges `c.model(mctx, req)` (`:369`); on a stream error it flushes deltas and returns the error (`:370-383`), with no classification and no retry.
- **Reported usage is observable at the drive.** Each chunk carries `Usage *types.Usage` (`core/types/model_types.go:28`); the limits middleware already folds the last non-nil value (`core/chains/limits.go:304-305`) but does not surface it. `Usage.Estimated` marks adapter-synthesized usage (`adapter/anthropic/stream.go:89,99`, `adapter/openai/stream.go:232`).
- **Overflow is a class, not a retry.** `ClassContextOverflow` is an `ErrorClass` (`core/types/errors.go:196`) and `*types.ModelError` carries `Class`/`Provider`/`Status`/`Code`/`RetryAfter`/`Err` (`core/types/errors.go:204-211`) — no reported overflow factor field.
- **Telemetry counts and records.** `Telemetry.Count` and `Telemetry.Record` (`core/types/telemetry.go:9-20`); `gohan.context.overflow_retry{profile}` and `gohan.context.estimate_error{profile}` are named in the Contract rules (`openspec/specs/context/spec.md:64-65`) and the overflow scenario, though not yet in the telemetry spec's metric table (`openspec/specs/telemetry/spec.md:119`).
- **The public-path suite binds by scenario id.** `TestCompactionPublicPath` (`core/compaction_public_path_test.go:179`) names subtests exactly by scenario id and carries the suspend/replay harness `m2-context-persisted-compaction` landed.

## Goals / Non-Goals

**Goals:** calibrate each profile's estimator from provider-reported usage, apply the ratio at the compaction decision, persist it with the checkpoint so a replay reaches the same decision, and add exactly one bounded overflow compaction-retry that fails `Permanent` on the second overflow.

**Non-Goals:** no Contract text change and no scenario id added — the delta reproduces *Counting and calibration* verbatim; no change to `ContextBudget`'s frozen formula; no change to `NeedsCompaction`, the safe-prefix selection, the publication payload, the audit hash or the reconciliation protocol; no extension of the compaction publication/audit payload (owned by the completed `m2-context-persisted-compaction`); no M4 `Summarize` policy surface (`PromptSet.Compaction`, tuning, policy source — `openspec/changes/m2/design.md` §3 item 2); no provider compaction (`m2-provider-compaction`).

## Decisions

### D1 — Calibration is measured from provider-reported usage and stored on the run state

The drive captures the last non-nil `chunk.Usage` in its existing stream loop (`core/drive_turn.go:369`), mirroring the limits middleware (`core/chains/limits.go:304-305`). Calibration runs only on provider-reported usage: a usage with `Estimated == true` is adapter-synthesized (`adapter/anthropic/stream.go:89`, `adapter/openai/stream.go:232`) and calibrates nothing. The denominator is the declared estimator's value for the request that was actually sent — `est.Estimate(req, caps)` through the seam of D5 — never the model's `TokenCounter`, so the ordinary request path stays free of a provider count round-trip. The factor is `clamp(InputTokens / estimate, 0.5, 2.0)` stored in `st.Calibration[profile]`; a nil estimator or a non-positive estimate calibrates nothing. `gohan.context.estimate_error{profile}` records `|calibration − 1|` on every response that calibrates, through `Telemetry.Record` (`core/types/telemetry.go:9-20`).

### D2 — The calibration multiplies the estimate; the shipped budget scales the window, so the stage passes the reciprocal

Rule 7 multiplies "the next estimate for the same profile" (`openspec/specs/context/spec.md:113-116`). The compaction decision compares a harness count against `ContextBudget(...).Limit` (`core/compaction_stage.go:278-283`), and `ContextBudget` scales the window by its `ratio` argument. A profile whose real usage is 1.2× its estimate must compact sooner, so the stage passes `1/calibration` (clamped) as the budget ratio: the ceiling shrinks by 1.2 and the request crosses it before it would have. Alternative rejected: dividing inside `ContextBudget` would edit a formula frozen by row 19.2 with no spec formula to lean on (`core/types/context_budget.go:5-7`). The reciprocal is symmetric under the [0.5, 2.0] clamp, so a calibrated profile stays inside the clamp.

### D3 — The ratio persists through the checkpoint because it lives on `runtime.State`

`State.Calibration` is already an allowed envelope key and is cloned on the checkpoint path (`core/checkpoint_envelope.go:207-208`, `core/drive_lifecycle.go:410-414`), and `decodeCheckpoint` restores it on resume (`core/checkpoint_envelope.go:60`). Writing `st.Calibration` in the drive is therefore the whole persistence step: the ratio rides into the next checkpoint and back out on another pod, and the stage reads the restored value so the replay reaches the same decision. No new store, column, field or goose migration is involved, and no change to `encodeCheckpoint`/`decodeCheckpoint` is needed.

### D4 — One overflow retry, bounded in the drive, keyed on the class the provider reports

On a `*types.ModelError` whose `Class` is `ClassContextOverflow` (`core/types/errors.go:196,204-211`) for a request the stage accepted, the drive raises the calibration ratio used for the retry to `calibration × 1.25`, counts `gohan.context.overflow_retry{profile}`, re-runs the compaction stage with the raised ratio — which now fires because the ceiling tightened — and retries the provider call once. The retry is bounded at two attempts total and never loops: a second overflow is returned as a `ClassPermanent` failure so the turn ends `Permanent`, exactly as the scenario states. `ModelError` carries no provider-reported overflow factor today, so ×1.25 is the applied raise; this is recorded as a limitation, not a new field. The retry re-enters assembly and the stage rather than replaying a cached request, so the compaction sees the raised ratio.

### D5 — The estimator is bound to the turn independently of the compactor

`compactionCount` is resolved only for a flow with an active compactor (`core/native_resolve.go:71`, `:241`), but rule 7 calibrates "each profile's token estimator" (`openspec/specs/context/spec.md:64`), which is not limited to compacting flows. The declared estimator (`Stack.estimator`, `core/build_options.go:212-214`) is therefore bound to `turnConfig` as its own seam and read at calibration time, independent of `compactor`. The compactor build gates (`ContextPolicy.Compactor` requires a resolvable count, a validated fallback and a guard — rule 13) stay exactly as they are; a flow with a declared estimator and no compactor calibrates and uses the ratio in its ordinary budget decision.

### D6 — The checkpoint is the persistence surface; the compaction payload is not extended

The requirement the scenarios bind says the ratio is persisted "with the checkpoint" (`openspec/specs/context/spec.md:111`), and D3 does that. Contract rule 7 additionally says the ratio in force is recorded in the `Compacted` audit record (`:64`); that record's shape and additive hash segment are owned by the completed `m2-context-persisted-compaction` change, so this change does not restate or extend the payload. The ratio the drive used is on `runtime.State` and thus in the checkpoint at the same moment the `Compacted` record is written; extending the frozen publication payload is left to a change that owns it, so no hash or migration is touched here.

## Risks / Trade-offs

- [A model that implements `TokenCounter` but declares no estimator] → the decision already uses the exact count (rule 7); calibration, which is about the estimator, calibrates nothing rather than inventing an estimate. D1 states the nil case.
- [The reciprocal in D2 is easy to invert] → a test asserts the direction: a profile reporting more than it estimates gets a smaller ceiling and compacts sooner, and the observed multiplier matches the scenario's 1.2.
- [The retry could loop or double-charge] → the loop is bounded at two attempts, the counter fires once per retry, and a second overflow is `ClassPermanent`; the scenario asserts the counter sits at 1.
- [The ratio a replay needs could differ from the one written] → the value is written to `st.Calibration` before the checkpoint is taken, and the replay case resumes on a fresh store, so the test fails if the ratio is only held locally.
- [The Contract's audit-record clause (rule 7) is only partly served by this change] → D6 records the boundary; the checkpoint is the surface the requirement and all three scenarios bind, and the payload is owned elsewhere.
- [Reported usage can be absent on a stream error] → calibration runs only on a successful response carrying non-estimated usage; an overflow retry that finally fails calibrates from the last reported usage, never from a missing one.

## Migration Plan

Additive first: with no calibration written, the stage's ratio stays 0 and every existing decision is unchanged; the retry only adds a path where an overflow previously failed. The behavioural change is confined to profiles whose responses report usage and, for the retry, to a request the budget accepted that the provider then rejects with `ClassContextOverflow`. Rollback reverts the stage ratio and the bounded retry loop; the `State.Calibration` field, its envelope key and its clone already exist, so no stored shape or migration is involved.

## Open Questions

None that change the specs, the approach, or the task breakdown.
