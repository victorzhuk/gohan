# Tasks

Delivered in `feat(flow): generate titles and fence the handoff lane`. A task is done when its listed scenario IDs pass and `task lint`, `task spec` and `task test` are green.

## 1. Title generation

- [ ] 1.1 Add `std/flow.Title(s *gohan.Stack, profile string) gohan.Flow[[]types.Block, string]`: the `Extract[string]` recipe of at most six words offering no tool; verify `go test -short -timeout 2m ./std/flow/... -run 'TestTitleRecipe'`.
  - evidence: `std/flow/title.go` (`Title`); `std/flow/title_test.go` (`TestTitleRecipe`)
- [ ] 1.2 Add `agent.WithTitleProfile(profile string)` to `core/build_options.go` and store the resolved profile on the stack, defaulting to the cheapest configured profile; verify `go test -short -timeout 2m ./core/... -run 'TestTitleProfileOption'`.
  - evidence: `core/build_options.go` (`WithTitleProfile`); `core/session_title_test.go` (`TestTitleProfileOption`)
- [ ] 1.3 Trigger the title run after the first `Done` of a session's first primary run: one `SessionChild` run with `CostTags.Feature = "title"`, written with `SessionIndex.UpdateSession`, without delaying the primary `Done`; verify `go test -short -timeout 2m ./core/... -run 'TestTitleGeneratedAfterFirstDone'` with the subtest named exactly `flow.title-generated-after-first-done`.
  - evidence: `core/session_title.go` (`maybeTitle`); `core/session_title_test.go` (`TestTitleGeneratedAfterFirstDone`, `flow.title-generated-after-first-done`)
- [ ] 1.4 Enforce the lock precedence: generation is skipped when `TitleLocked` is set or a title exists, the generated write locks the title, and a rename after generation keeps the user's title; verify `go test -short -timeout 2m ./core/... -run 'TestTitleLockedByRename'` with the subtest named exactly `flow.title-locked-by-rename`.
  - evidence: `core/session_title.go`; `core/session_title_test.go` (`TestTitleLockedByRename`, `flow.title-locked-by-rename`)

## 2. Request human

- [ ] 2.1 Add `types.HandoffContext{Reason, Skill string; Fields map[string]string}` and the terminal control error `types.HandoffError{Context HandoffContext}`; treat it as a control error in `CallTool` and `isControlError`; verify `go test -short -timeout 2m ./core/types/... -run 'TestHandoffContextShape'` and `go test -short -timeout 2m ./core/... -run 'TestHandoffControlError'`.
  - evidence: `core/types/errors.go` (`HandoffContext`, `HandoffError`); `core/tool_call.go`, `core/drive_turn.go` (control class); `core/types/errors_test.go`, `core/handoff_control_test.go`
- [ ] 2.2 Register `request_human` as a governed built-in tool that returns `*types.HandoffError` carrying the model's reason; verify `go test -short -timeout 2m ./core/... -run 'TestRequestHumanTool'`.
  - evidence: `core/tool_request_human.go` (`requestHuman`); `core/tool_request_human_test.go` (`TestRequestHumanTool`)
- [ ] 2.3 Map the handoff error at the lifecycle terminal seam to `finishRun(ctx, st, stores.Finished, nil, types.StopHandedOff)` and `done(st, nil, types.StopHandedOff)` with no `ResumeToken`; write `ControlHandoffRequested`, persist the `HandoffContext` through `SessionMeta.Handoff`/`SessionPatch.Handoff`, and audit `handoff_requested`; verify `go test -short -timeout 2m ./core/... -run 'TestRequestHumanEndsRun'` with the subtest named exactly `flow.request-human-ends-run`.
  - evidence: `core/drive_lifecycle.go` (handoff terminal branch); `core/stores/session.go` (`SessionMeta.Handoff`, `SessionPatch.Handoff`); `core/drive_lifecycle_test.go` (`TestRequestHumanEndsRun`, `flow.request-human-ends-run`)

## 3. Hand-back fence

- [ ] 3.1 Project every `OriginOperator` block in assembly with a preceding `Text{OriginSystem, PromptSet.OperatorTurn}` block and never rewrite history; verify `go test -short -timeout 2m ./std/... -run 'TestOperatorFence'`.
  - evidence: `std/assembly.go` (operator-turn projection); `std/assembly_test.go` (`TestOperatorFence`)
- [ ] 3.2 Let the next `Send` after `HandBack` assemble both operator turns wrapped by `PromptSet.OperatorTurn`, with the model's reply `OriginModel`; verify `go test -short -timeout 2m ./core/... -run 'TestHandbackFencesOperatorTurns'` with the subtest named exactly `flow.handback-fences-operator-turns`.
  - evidence: `core/session_takeover.go` (`HandBack`); `core/conversation.go` (next assembly); `core/session_takeover_test.go` (`TestHandbackFencesOperatorTurns`, `flow.handback-fences-operator-turns`)

## 4. Feedback metric

- [ ] 4.1 Add `Stack.Feedback` as the public recording path: owner-checked and idempotent through `stores.FeedbackStore.PutFeedback`, appending the `feedback` audit record and the `FeedbackRecorded` event through `stores.RecordFeedback`; verify `go test -short -timeout 2m ./core/... -run 'TestStackFeedback'`.
  - evidence: `core/feedback.go` (`Stack.Feedback`); `core/feedback_test.go` (`TestStackFeedback`)
- [ ] 4.2 Add `core.MetricFeedback = "gohan.feedback"` and emit it from `Stack.Feedback` with `name` and `flow`; register `std/telemetry.Metric{Name: "gohan.feedback", Labels: []string{"name", "flow"}}`; a numeric value records, any other value counts; verify `go test -short -timeout 2m ./std/telemetry/... -run 'TestFeedbackMetricRegistration'` and `go test -short -timeout 2m ./core/... -run 'TestFeedbackMetricEmit'`.
  - evidence: `core/telemetry.go` (`MetricFeedback`); `core/feedback.go` (emission); `std/telemetry/metrics.go` (registration); `core/feedback_test.go`, `std/telemetry/metrics_test.go`
- [ ] 4.3 Prove the label set: `gohan.feedback{name=csat, flow, release=r7, variant=b}` observes 4 and no label carries the subject or comment; verify `go test -short -timeout 2m ./std/telemetry/... -run 'TestFeedbackMetricLabels'` with the subtest named exactly `telemetry.feedback-metric-labels`.
  - evidence: `std/telemetry/metrics_test.go` (`TestFeedbackMetricLabels`, `telemetry.feedback-metric-labels`)

## 5. Scenario behavior and package gates

- [ ] 5.1 Register a top-level `TestSessionMetadataHandoff` whose subtests are named exactly `flow.title-generated-after-first-done`, `flow.title-locked-by-rename`, `flow.request-human-ends-run`, `flow.handback-fences-operator-turns` and `telemetry.feedback-metric-labels`, driving the shipped runs store, session index, assembler and metric layer through the public path; verify `go test -short -timeout 2m ./core/... -run 'TestSessionMetadataHandoff'`.
  - evidence: `core/session_metadata_handoff_test.go` (`TestSessionMetadataHandoff` with the five ID-named subtests)
- [ ] 5.2 Run the repository wrappers and resolve failures; verify `task lint` and `task test`.
  - evidence: driver-run `task lint` 0 issues; `task test` green across root, `adapter/otel`, `adapter/openai`, `adapter/anthropic`, `examples`
- [ ] 5.3 Validate the change after all required M1 owners land; verify `openspec validate m1-session-metadata-handoff --strict --no-interactive` and `task spec:gate GATE=M1`.
  - evidence: driver-run `task spec:gate GATE=M1` with the five IDs no longer `missing` or `integration gap`
