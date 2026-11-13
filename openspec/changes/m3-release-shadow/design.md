# Design

## Context

ADR-0096 froze shadow mode (`RunMode`), release identity (`ReleaseManifest.ID()`) and the pairwise `evals.Shadow` runner. `production-online-evaluation` landed the bounded online slice: bounded judges, unscored outcomes, durable provenance and explicit calibration. What remains for M3 is the release pipeline itself: orchestrating a candidate against recorded or scripted traffic and gating the release with checks driven by the `ReleaseManifest`.

## Goals / Non-Goals

Goals:

- One orchestration entry that runs a candidate build over recorded traffic or a scripted suite, in frozen `Shadow` mode, without serving callers.
- Per-input pairwise reports attributed to champion and challenger release ids.
- A check suite computed from the `ReleaseManifest`, producing a durable, replayable release decision.

Non-Goals:

- Traffic splitting and automatic promotion (stays with the flags provider).
- Re-implementing online sampling, unscored semantics, export provenance or calibration (`production-online-evaluation`).
- Storing new first-class store ports; the decision is a report artifact under existing release reporting.

## Decisions

1. **Orchestration wraps the frozen `evals.Shadow` runner, not a new engine.** The orchestrator resolves inputs (cassettes or a scripted suite), runs champion and challenger with the challenger in `Shadow` mode, and returns the pairwise report. Suppression, journal replay, shadow sessions and budget separation are already frozen; no new behavior there.
2. **No caller is served.** The orchestrator refuses to install any caller transport on the candidate run; shadow results are observable only through the report and the shadow session. This makes "release candidate" a structural property, not a discipline.
3. **Checks are data driven from the manifest.** The `ReleaseManifest` from `Build` (via `Stack.Manifest()`) enumerates declared models, prompts, tools, skills, chains and definitions. Each check consumes the manifest plus declared evidence: dependency evidence (pinned dependency versions named by the manifest entries) and compatibility evidence (compatibility policy records for the declared surface). A check with absent evidence fails closed.
4. **The decision is a durable artifact.** The suite emits one decision record: checks with pass/fail/unscored and reasons, report reference, `ReleaseID`, `Variant`, dataset hashes and judge fingerprints. Unscored checks cannot pass a required gate, mirroring the frozen online slice rule.
5. **Additive Go surface.** New exported functions in the `evals` module and release checks package; no frozen port or existing exported name changes.

## Risks / Trade-offs

- Reuse of `evals.Shadow` keeps the surface small but couples orchestration scope to the frozen pairwise contract; deviations need a spec change.
- Fail-closed checks may block releases on missing evidence until producers exist; the tasks sequence evidence wiring before gate enforcement.
- Manifest-driven checks must not recompute identity; they consume `Stack.Manifest()` to avoid divergence from the frozen deterministic `ID()`.

## Migration Plan

Additive only. Authoring registers nothing; task 1.1 registers the proposed scenario ids at implementation admission, then checks ship behind the existing evals module boundary. No stored shape migration; the decision record is a new report artifact.

## Compliance

This change extends `openspec/specs/release/spec.md` without contradicting its frozen requirements: shadow mode, release identity and evals requirements are untouched; all deltas are `## ADDED Requirements`.
