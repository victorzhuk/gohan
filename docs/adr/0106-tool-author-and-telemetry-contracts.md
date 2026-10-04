# ADR-0106: Tool author contract and telemetry attribute/logging contract

Status: accepted · Origin: review round 32 (2026-09-30)

## Decision

`tools`: `In` is an infrastructure struct with the tag vocabulary `json`, `desc`, `enum`, `min`, `max`, `pattern`; depth ≤ 4; untyped fields rejected unless `WithRawArgs()`; schema derived by a reflection walker in `core` (Q2 resolved, no third-party library). `Out` of `[]Block` or `ToolResult` passes through, `string` becomes `Text`, other values become JSON. Panics are recovered (`Failed(Permanent)` or `Outcome: Unknown` by effect) with the stack in audit only. Defaults: `Timeout` 10 s / 30 s / 60 s by effect, `MaxOutput` 64 KiB. `telemetry`: canonical attribute key table with GenAI mappings; metric label allow-list enforced at `Build`; logging contract (`WithLogger`, levels, content never logged).

## Context and evidence

The first tool and the first dashboard are M0 deliverables; neither contract existed beyond prose.

## Consequences

Eight scenarios; tasks 6 and 28 updated; `ToolProgress` added to the backlog.
