# Spec Delta

## Context

This delta MODIFIES the `telemetry` *Telemetry conventions and prompts* requirement. The main requirement block carries no normative prose and five scenario blocks. This delta is a strict superset of that block: it reproduces every scenario unchanged and adds the `gohan.feedback` label set and its cardinality bound, which the requirement lacked. The archive merge MUST union the added rule and MUST keep every reproduced scenario.

## MODIFIED Requirements

### Requirement: Telemetry conventions and prompts

`Stack.Feedback` MUST record each signal through `stores.FeedbackStore`, append the `feedback` audit record and the `FeedbackRecorded` event, and emit `gohan.feedback`. The metric MUST be registered with the labels `name` and `flow` only; the metric layer stamps `release` and `variant`, so the observed series is exactly `gohan.feedback{name, flow, release, variant}`. A numeric `Value` MUST record a histogram and any other `Value` MUST count. No label MAY carry the subject, session, run, target, comment or correction: the allow-list admits none of them and forbids the identity names. The cardinality is bounded by the declared `name` set (the tool-name grammar `^[a-z][a-z0-9_]{0,63}$`), the registered flow names, the release and the variant — a fixed four-label product with no unbounded dimension.

#### Scenario: rename is config
ID: `telemetry.rename-is-config`
- WHEN the `GenAI` convention maps `gohan.model.version` to a new attribute name
- THEN no core package changes and existing spans carry the new name after redeploy

#### Scenario: Langfuse preset
ID: `telemetry.langfuse-preset`
- WHEN the `Langfuse` convention is active and content capture is off
- THEN spans carry `langfuse.session.id`, `langfuse.release` and `langfuse.prompt.version`, and no `langfuse.observation.input`

#### Scenario: prompt source outage
ID: `telemetry.prompt-source-outage`
- WHEN the `PromptSource` is unreachable and the cache is empty
- THEN the run uses the embedded fallback text, stamps `version=fallback`, and `gohan.prompt.fallback` increments

#### Scenario: feedback metric labels
ID: `telemetry.feedback-metric-labels`
- WHEN feedback `csat: 4` is recorded for a run under release `r7`, variant `b`
- THEN `gohan.feedback{name=csat, release=r7, variant=b}` observes 4 and no label carries the subject or comment

#### Scenario: replay strictness
ID: `telemetry.replay-strictness`
- WHEN a cassette was recorded with `Version: v1` and the profile now pins `v2`
- THEN `Strict` replay fails naming the version mismatch; `ByTurn` replays with a warning
