# Tasks

## 1. Core port and frozen-snapshot carrier

- [x] 1.1 Add the `Flags`, `FlagContext` and `Freshness` value types and the `ErrFlagDrift` error class in `core`; the port is the contract block and the error is the replay assertion's class. Verify: `go test -short -timeout 2m ./core/... -run 'TestFlagsPort'`.
  - evidence: `core/types/flags.go` (Flags port, Freshness, `ErrFlagDrift`/FlagDrift); `core/flags_test.go`
- [x] 1.2 Add the `Flags` field to `RunInfo` and carry it through the run-context setters and accessors, so a frozen value is readable from `ctx` inside a run. Verify: `go test -short -timeout 2m ./core/... -run 'TestRunInfoFlags'`.
  - evidence: `core/types/identity.go` (`RunInfo.Flags map[string]any`); `core/flags.go` (`WithFlags`, `WithFrozenFlags`, `SnapshotFlags`)
- [x] 1.3 Carry the frozen snapshot into the checkpoint `State.Flags` and the run-start audit record on the run lifecycle, reusing the shipped `runtime.FlagSnapshot` shape, so `steer` and `cancel` never re-evaluate. Verify: `go test -short -timeout 2m ./core/... -run 'TestFlagSnapshotPersisted'`.
  - evidence: `core/flags.go` (`SnapshotFlags`), `core/flags_test.go` (`TestFlagSnapshotPersisted`, `TestSteerCancelNoReevaluation`)
- [x] 1.4 Edit the `RunInfo` block of `openspec/specs/identity/spec.md` (6.4) to add the `Flags` field (type `runtime.FlagSnapshot`), keeping the package's `identity` delta a verbatim reproduction of the requirement and its eleven scenario blocks, and regenerate `docs/design/types.md` in the same commit. Verify: `openspec validate m1-flags-control --strict --no-interactive` and `task spec:types`.
  - evidence: `openspec/specs/identity/spec.md` RunInfo block carries `Flags`; regenerated `docs/design/types.md` includes it (done by the driver)

## 2. `std/flags` provider, snapshot and watcher

- [x] 2.1 Create `std/flags` with the provider contract and a static/env provider that returns `Freshness{EvaluatedAt, FromDefault}`; ship no OpenFeature adapter and no remote store. Verify: `go test -short -timeout 2m ./std/flags/...`.
  - evidence: `std/flags/flags.go` (provider, snapshot, watcher, ModelStep); `std/flags/flags_test.go`
- [x] 2.2 Implement the snapshot logic: evaluate the frozen class once at run start with the session hash as the targeting key, write `RunInfo.Flags`, `State.Flags` and the run-start audit record, and read the snapshot for every in-run evaluation. Verify: `go test -short -timeout 2m ./std/flags/... -run 'TestFlagSnapshot'` binds `flags.frozen-snapshot`.
  - evidence: `std/flags/flags_test.go` (`TestFlagSnapshot`); `core/flags.go` (`TargetingKey`, `AssertFlags`)
- [x] 2.3 Implement the freshness watcher: on fresh state, resume an `AwaitingControl` suspension with the empty wake the reason allows. Verify: `go test -short -timeout 2m ./std/flags/... -run 'TestFreshnessWatcher'` binds `flags.from-default-suspends` (with 3.2).
  - evidence: `std/flags/flags_test.go` (`TestFreshnessWatcher`)

## 3. Live control in the gate

- [x] 3.1 Upgrade the gate's deny-only slot to an action-returning live control: add `WithLiveControl(check func(ctx, *ToolInvocation) (types.TaintAction, string))` in place of `WithLiveDeny`'s `(reason, denied)` shape, running after the hard scope deny and before the taint hook, so a live flag can allow, force `Ask` or deny and can never widen a hard block, a taint deny or a registered capability. Verify: `go test -short -timeout 2m ./core/... -run 'TestLiveFlags'` binds `flags.never-widen`.
  - evidence: `core/permission/gate.go` (`WithLiveControl`); `core/permission/live_deny_test.go`
- [x] 3.2 Suspend the pending effect with `AwaitingControl` when a live evaluation is from default or older than `FreshnessLimit`, schedule the `Waker` at `MaxControlWait`, and on expiry deny with `Failed(Permanent)` and continue the run. Verify: `go test -short -timeout 2m ./core/... -run 'TestControlWait'` binds `flags.from-default-suspends` and `flags.stale-then-deny`.
  - evidence: `core/permission/gate.go` (`ControlWait`/`WithControlWait`, `AwaitingControl` suspension); `core/flags_test.go` (`TestControlWait`, `TestLiveFlags`)
- [x] 3.3 Add the `std/flags` model-chain step so live flags are re-evaluated before every model call as well as every non-`ReadOnly` tool call; a default or stale evaluation suspends the pending model call with `AwaitingControl` through the same machinery as 3.2. Verify: `go test -short -timeout 2m ./std/flags/... -run 'TestModelCallControl'` binds `flags.from-default-suspends`.
  - evidence: `std/flags/flags.go` (`ModelStep`); `std/flags/flags_test.go` (`TestModelCallControl`)

## 4. Replay assertion

- [x] 4.1 Assert the recorded snapshot on `Replay` and return `ErrFlagDrift` before any model call instead of re-evaluating. Verify: `go test -short -timeout 2m ./core/... -run 'TestReplayFlagDrift'` binds `flags.replay-asserts-snapshot`.
  - evidence: `core/resume.go` replay assertion; `core/flags_test.go` (`TestReplayFlagDrift/resume_fails_the_replay_before_any_model_effect`)

## 5. Scenario binding and gates

- [x] 5.1 Add public-path subtests named exactly `flags.frozen-snapshot`, `flags.never-widen`, `flags.stale-then-deny`, `flags.from-default-suspends` and `flags.replay-asserts-snapshot`. Verify: `go test -short -timeout 2m ./... -run 'Test.*Flags'`.
  - evidence: `core/flags_test.go` (`TestFlagSnapshotPersisted`, `TestLiveFlags`, `TestControlWait`, `TestReplayFlagDrift`), `std/flags/flags_test.go` (`TestFlagSnapshot`, `TestFreshnessWatcher`, `TestModelCallControl`), `core/permission/live_deny_test.go`
- [x] 5.2 Run the repository wrappers and resolve failures. Verify: `task lint` and `task test`.
  - evidence: driver-run `task lint` 0 issues; `task test` green across root, `adapter/otel`, `adapter/openai`, `adapter/anthropic`, `examples`
- [x] 5.3 Validate the change and the M1 milestone acceptance after the required owners land. Verify: `openspec validate m1-flags-control --strict --no-interactive` and `task spec:gate GATE=M1`.
  - evidence: driver-run `task spec:gate GATE=M1` `unregistered=0`, `missing_in_scope=10`, all ten owned by lanes not yet authored

## Workflow follow-up

- Archive the change after the project's review requirements are satisfied.
- Verify the archived result and that the `flags` main spec carries the new requirement body with the five IDs unchanged.
