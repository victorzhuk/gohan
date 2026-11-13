# ADR-0184: Shadow runs and manifest-driven release checks

Status: proposed · Source change: `openspec/changes/m3-release-shadow` · Governs ADR-0096 scope completion for M3

## Decision

Release candidates go through one orchestration entry in the `evals` module: it replays recorded traffic (cassettes) or a scripted suite against the challenger build, with the challenger in the frozen `Shadow` run mode, and refuses any caller transport on the candidate run. The pairwise per-input report stays the frozen `evals.Shadow` shape, attributed to champion and challenger `ReleaseID` and `Variant`.

Release gating is manifest-driven: the check suite enumerates its checks from the `ReleaseManifest` computed by `Build` and consumed via `Stack.Manifest()`, requiring declared dependency evidence (pinned versions) and compatibility evidence (policy records) per declared surface. Any failing, unscored or unrun check denies the release. The suite emits one durable decision record whose re-emission preserves its identity.

## Context and evidence

ADR-0096 froze shadow mode, release identity and the pairwise shadow runner but left the release pipeline implicit: nothing defined who drives a candidate over traffic or how a release becomes a decision. `production-online-evaluation` (ADR-0176) landed the bounded online slice and deferred full shadow orchestration to its milestone owner, this change.

## Consequences

Additive `evals` and release-check surface; no frozen port method changes and no new store ports. Release checks fail closed until evidence producers exist, so evidence wiring precedes gate enforcement in the tasks. Traffic splitting and automatic promotion remain out of scope. Resolves the orchestration and gating half of the release milestone; extends ADR-0096 and ADR-0176.
