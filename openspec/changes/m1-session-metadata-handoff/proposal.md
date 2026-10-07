# Proposal: m1-session-metadata-handoff

## Why

The `flow` *Flow contract* requirement fixes the title, takeover and hand-back rules and the `telemetry` *Telemetry conventions and prompts* requirement fixes the `gohan.feedback` labels, but the lane is unbuilt. No title generator exists: `std/flow` holds `Extract`, `Classify` and `Quarantine` only (openspec/specs/flow/spec.md:118; openspec/changes/m1/design.md:2.12). `request_human` is not a tool, and `ControlHandoffRequested` is declared but never written (`core/stores/session.go:88`). `HandBack` only flips the control bit and leaves the operator turns unfenced (`core/session_takeover.go:107`). `gohan.feedback` is registered nowhere and emitted nowhere, and the public `Stack.Feedback` recording path the `flow` *Feedback* rule names does not exist (`core/telemetry.go:23-29`, `std/telemetry/metrics.go:38-44`). Five registered scenarios are left `missing` or `integration gap` for exactly that absent path.

## What Changes

- Add `std/flow.Title`: the `Extract[string]` recipe of at most six words, offering no tool.
- Add the title trigger in core: after the first `Done` of a session's first primary run, one `SessionChild` run with cost tag `title` on the `WithTitleProfile` profile (default: the cheapest configured profile), written with `UpdateSession`; the primary `Done` is not delayed.
- Add the rename-lock precedence: generation is skipped for a locked or already-titled session, and a rename always wins.
- Add `request_human` as a governed built-in tool and the terminal control error it returns: the run ends `Done{Reason: StopHandedOff}` with no `ResumeToken`, `Control` becomes `ControlHandoffRequested` with the `HandoffContext`, and the transition is audited.
- Fence the operator turns: after `HandBack`, assembly wraps every `OriginOperator` turn with `PromptSet.OperatorTurn`; history is not rewritten.
- Add `Stack.Feedback` as the public recording path and register `gohan.feedback{name, flow, release, variant}`.
- Preserve and cover the five scenarios this lane owns: `flow.title-generated-after-first-done`, `flow.title-locked-by-rename`, `flow.request-human-ends-run`, `flow.handback-fences-operator-turns`, `telemetry.feedback-metric-labels`.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `flow`: the *Flow contract* requirement already carries the five scenarios, so the delta reproduces it and adds the title trigger and lock precedence, the `request_human` terminal class, and the hand-back fence shape (openspec/specs/flow/spec.md:118).
- `telemetry`: the *Telemetry conventions and prompts* requirement already carries `telemetry.feedback-metric-labels`, so the delta reproduces it and adds the metric's exact label set and cardinality bound (openspec/specs/telemetry/spec.md:160).
- `streams`: the *Cancellation and streams* requirement lacks the handoff terminal, so the delta reproduces it and adds that a request-human end is a `Done` and never a `Suspended` (openspec/specs/streams/spec.md:192).
- `stores`: the *Session index* requirement lacks the handoff-persistence rule, so the delta reproduces its five scenarios and adds that `SessionMeta.Handoff`/`SessionPatch.Handoff` carry the `HandoffContext`; the Contract `SessionMeta` and `SessionPatch` blocks gain the fields (openspec/specs/stores/spec.md:210, :243, :487).

## Impact

The design adds one recipe (`std/flow/title.go`), one core orchestration file (`core/session_title.go`), one built-in tool (`core/tool_request_human.go`), one public recording method (`core/feedback.go`), one terminal control error (`types.HandoffError`) with its context value (`types.HandoffContext`), two session-index fields (`SessionMeta.Handoff`, `SessionPatch.Handoff`), one build option (`agent.WithTitleProfile`), one metric name (`core.MetricFeedback`) and its registration in `std/telemetry`. It adds no third-party dependency and no transport module. The fence rides the shipped `std/assembly` projection and the shipped `PromptSet.OperatorTurn` field; the title run rides the shipped conversation lifecycle and runs store. The lane is a `std`/`core` change only — adapter and `interop` work is out of scope (openspec/specs/interop/spec.md:48). Decisions cite ADR-0131 (the flow lane), ADR-0123 (the feedback metric) and ADR-0133 (the stream terminal); the core/std split follows ADR-0139.
