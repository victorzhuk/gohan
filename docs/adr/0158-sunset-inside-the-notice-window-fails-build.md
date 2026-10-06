## 0158. A sunset inside the notice window fails `Build`, deprecation is never silent

Status: accepted

## Context

A `ModelProfile` can announce its own retirement through `Sunset` and name the
profile that replaces it through `Successor` (`core/types/model_profile.go`).
ADR-0087 recorded the shape; nothing enforced it. A flow pinned to a profile
that shuts down kept building and failed later, in production, at the provider —
after the code that had to migrate was already written against the old model.

`openspec/specs/lifecycle/spec.md` ("Retirement, schema evolution, cost") states
the three facts this ADR implements:

- `lifecycle.sunset-inside-notice-window` — with a `Sunset` 20 days away and a
  30-day notice window, `Build` fails (or warns under `SunsetWarnOnly`) naming
  the profile and successor.
- `lifecycle.deprecated-never-silent` — a provider deprecation error routes to
  `Successor` when declared, and otherwise the call returns
  `*ModelError{Class: Deprecated}` with no fallback message.
- `lifecycle.successor-params-validated` — when the successor lacks a `Caps`
  entry for a static model option the flow sets, `Build` fails naming the
  option.

The evidence behind the original decision is that most migrations landed after
the shutdown date, a fraction of them failed silently, and successors rejected
the parameters the retired model accepted (`docs/design/evidence.md`). Each of
those three failures is one of the three requirements above.

## Decision

1. **A sunset inside the notice window fails `Build`.** `WithSunsetNoticeWindow(d)`
   sets how long before `Sunset` a pinned profile must still be buildable, and
   `DefaultSunsetNoticeWindow` (30 days) applies when no option narrows or widens
   it. A zero `Sunset` means no announced retirement and is never a failure.
   The error names the profile, the sunset date and the successor (`none` when
   the profile declares none), so the fix is in the message the caller reads.
   A flow must move to the successor before the sunset date; the build is the
   cheapest place to say so.
2. **`WithSunsetWarnOnly` downgrades the failure, it does not remove it.**
   Under the option the build proceeds, a warning record names the same facts,
   and `gohan.model.sunset_warning` is counted with the `profile` label. It is
   the escape hatch for a staged migration: adopt it once, watch the metric
   until it reaches zero, then build with the failure back on. A window that
   warned forever would be a silent deprecation with a metric attached.
3. **A deprecated call is never silent, in either direction.** A provider error
   with class `Deprecated` is counted under `gohan.model.deprecated_calls` and
   then routed to the profile's declared successor, counted under
   `gohan.model.deprecated_routed` with `profile` and `successor`. With no
   successor declared, the original `*types.ModelError{Class:
   types.ClassDeprecated}` surfaces to the caller and no assistant message is
   produced. Deprecated models do not get a fallback responder: a fallback
   message is a successful-looking answer to a call the platform already said
   it should stop serving.
4. **The successor's `Caps` are checked against the flow's static model options
   at `Build`.** `temperature` needs `Caps.Temperature`, a `tool_choice` other
   than `none` needs `Caps.Tools`, and a `response_format` needs
   `Caps.Constrained`. A knob the successor cannot serve fails the build naming
   the option, before any provider call, because the migration to the successor
   is where the incompatibility surfaces — not the first request that needs the
   knob.

## Consequences

- `Build` gains a new failure mode for any caller whose profile sunset is inside
  the notice window. `gohan.WithSunsetWarnOnly` is the migration path; without
  it the caller updates the profile or moves the flow to the successor. The
  entry is recorded in the changelog under *Breaking* while the root module is
  pre-v1 (`docs/design/compatibility.md`).
- `gohan.model.sunset_warning`, `gohan.model.deprecated_calls` and
  `gohan.model.deprecated_routed` are new metrics. Under warn-only the first is
  the migration queue; in production the last two count how often a reroute
  saved a turn and how often a deprecation reached a caller unresolved.
- A profile with no declared successor still fails the build once its sunset
  enters the window; the message says `successor none`, which is the actionable
  fact — the profile must either name its replacement or leave the fleet.
- Rejected: warning by default (a warning nobody must act on is the silent
  failure this decision exists to remove); refusing only at call time (the
  provider call is the expensive place to learn that a profile is retiring, and
  nothing at call time knows the successor's `Caps`); a fallback assistant
  message for a deprecated call (ADR-0022's fallback responder exists for
  output-guard blocks, where the harness refused to answer, not for a provider
  that has retired a model).