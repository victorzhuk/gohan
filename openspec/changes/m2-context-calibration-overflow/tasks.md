# Tasks

Commands use module boundaries: `go -C adapter/openai test ...` for an adapter module. Every verification command carries `timeout` and a test-time limit. A `-run` expression that matches no test is a failure, not a pass; each acceptance check names the subtest it must match.

## 1. Calibration from reported usage

- [ ] 1.1 Bind the declared estimator to the turn independently of the compactor: add an estimator seam to `turnConfig` beside `compactionCount` and populate it from `Stack.estimator` (`core/build_options.go:212-214`) for every resolved flow, not only one with an active compactor (`core/native_resolve.go:71`). A flow with no declared estimator carries nil. Verify: `timeout 2m go test -timeout 2m ./core -run 'TestCalibrationWiring'` asserts a compactor-free flow resolves a non-nil estimator and the estimator is consulted by calibration.
  - evidence: `core/drive_turn.go:56-58`; `core/native_resolve.go:98-110`; design D5, D1.
- [ ] 1.2 Record the calibration after a response: capture the last non-nil `chunk.Usage` in the `modelEffect` stream loop (`core/drive_turn.go:369`) and, when it is not `Estimated` and the estimator resolves a positive count for the sent request, set `st.Calibration[profile] = clamp(InputTokens/estimate, 0.5, 2.0)` and record `gohan.context.estimate_error{profile} = |calibration − 1|` through `Telemetry.Record` (`core/types/telemetry.go:9-20`). An `Estimated` usage or a nil estimator writes nothing. Verify: `timeout 2m go test -timeout 2m ./core -run 'TestCalibrationFromUsage'` covers the 12 000/10 000 case and the estimated-usage skip.
  - evidence: `core/types/model_types.go:28-42`; `adapter/anthropic/stream.go:89,99`; `adapter/openai/stream.go:232`; `openspec/specs/context/spec.md:113-116`; design D1.
- [ ] 1.3 Apply the ratio at the compaction decision: `runCompactionStage` passes `1/calibration[profile]` (clamped) instead of `0` to `types.ContextBudget` at both the trigger (`core/compaction_stage.go:282`) and the fallback fit check (`:441`), leaving the frozen formula untouched. A profile whose real usage exceeds its estimate gets a smaller ceiling. Verify: `timeout 2m go test -timeout 2m ./core -run 'TestCompactionRatioApplied'` asserts the ceiling tightens by 1.2 for a profile calibrated at 1.2 and the decision flips on a request that previously fit.
  - evidence: `core/types/context_budget.go:16-38`; `openspec/specs/context/spec.md:64`; design D2.

## 2. The persisted replay ratio

- [ ] 2.1 Ensure the calibration is written onto the run state before the checkpoint is taken, so `encodeCheckpoint` carries it through the existing envelope key and `decodeCheckpoint` restores it (`core/checkpoint_envelope.go:207-208`, `:60`; `core/drive_lifecycle.go:410-414`). No new field, column or migration. Verify: `timeout 2m go test -timeout 2m ./core -run 'TestCalibrationCheckpointRoundTrip'` writes a ratio, encodes, decodes into a fresh state and reads the same value back.
  - evidence: `core/runtime/runtime.go:35`; `core/checkpoint_envelope.go:60,207-208`; design D3.
- [ ] 2.2 Register `context.calibration-persisted-for-replay` in `TestCompactionPublicPath` (`core/compaction_public_path_test.go:179`): a run compacted with ratio 1.2 suspends, is resumed on a fresh store (another pod), reads the ratio from the checkpoint and reaches the same compaction decision. Verify: `timeout 10m go test -timeout 10m ./core -run 'TestCompactionPublicPath/context\.calibration-persisted-for-replay'` reports ok with the resume leg executed.
  - evidence: `core/compaction_public_path_test.go:179`; `openspec/specs/context/spec.md:118-121`; design D3.
- [ ] 2.3 Register `context.calibration-from-usage` on the public path beside 2.2: a response reporting 12 000 input tokens for a request estimated at 10 000 makes the next decision for the same profile use the 1.2 multiplier. Verify: `timeout 10m go test -timeout 10m ./core -run 'TestCompactionPublicPath/context\.calibration-from-usage'`.
  - evidence: `openspec/specs/context/spec.md:113-116`; design D1, D2.

## 3. The single overflow retry

- [ ] 3.1 Add the bounded overflow retry in `modelEffect` (`core/drive_turn.go:295-383`): when the stream returns a `*types.ModelError` with `Class == ClassContextOverflow` (`core/types/errors.go:196`) for a request the stage accepted, raise the ratio used for the retry to `calibration × 1.25`, count `gohan.context.overflow_retry{profile}` through `Telemetry.Count`, re-run the compaction stage with the raised ratio and retry the provider call once. The loop is bounded at two attempts. Verify: `timeout 2m go test -timeout 2m ./core -run 'TestOverflowRetryOnce'` asserts one compaction, one retry and the counter at 1.
  - evidence: `core/types/errors.go:196,204-211`; `core/drive_turn.go:369-383`; `openspec/specs/context/spec.md:65`; design D4.
- [ ] 3.2 A second `ClassContextOverflow` for the same request fails the turn `Permanent`: return the failure as a `ClassPermanent` `*types.ModelError` so the driver ends the turn non-retryably and no third attempt is made. Verify: `timeout 2m go test -timeout 2m ./core -run 'TestOverflowSecondFailsPermanent'` asserts the turn status is a permanent failure after exactly two attempts.
  - evidence: `core/types/errors.go:199`; `openspec/specs/context/spec.md:122-126`; design D4.
- [ ] 3.3 Register `context.overflow-compacts-and-retries-once` in `TestCompactionPublicPath` (`core/compaction_public_path_test.go:179`): a provider stub returns `ClassContextOverflow` once for a request the budget accepted, then a normal reply, and a second variant returns overflow twice. Verify: `timeout 10m go test -timeout 10m ./core -run 'TestCompactionPublicPath/context\.overflow-compacts-and-retries-once'` executes both variants and reports none missing.
  - evidence: `core/compaction_public_path_test.go:179`; `openspec/specs/context/spec.md:122-126`; design D4.

## 4. Artifact and repository gates

- [ ] 4.1 Validate the change artifact: `timeout 60s openspec validate m2-context-calibration-overflow --strict --no-interactive` exits 0, and the `context` delta's MODIFIED block reproduces the main *Counting and calibration* requirement and all three scenarios verbatim (a diff of the extracted blocks is empty) while the delta adds no scenario id.
  - evidence: `openspec/specs/context/spec.md:109-126`; design Non-Goals.
- [ ] 4.2 Run the repository gates after implementation: `timeout 10m task lint && timeout 30m task test` pass, and `timeout 5m task spec:gate GATE=M2` reports no new `missing_in_scope` for `context.calibration-from-usage`, `context.calibration-persisted-for-replay` and `context.overflow-compacts-and-retries-once`. Record the observed output; a gate that has not been run is not evidence.
  - evidence: `Taskfile.yml:28-36`, `:69-84`; `openspec/changes/m2/tasks.md` (M2 is a milestone gate).
- [ ] 4.3 Run `timeout 2m task spec:types` only if a Contract code block changed. This change adds no normative declaration and edits no main spec, so the expected result is no diff; if a Contract block did change, regenerate `docs/design/types.md` so `git diff --exit-code docs/design/types.md` is clean after regeneration.
  - evidence: `Taskfile.yml:9-13`; design Non-Goals.
- [ ] 4.4 Confirm no change to a sealed public contract: `git diff --exit-code openspec/specs/context/spec.md openspec/specs/telemetry/spec.md core/types/context_budget.go` is empty, proving the delta touched no main spec and the frozen budget formula is intact.
  - evidence: `core/types/context_budget.go:5-7`; design Non-Goals, D2.

## Workflow follow-up

- Archive the change after the project's review requirements are satisfied.
- Verify the archived result.
