# Proposal: Shadow runs and manifest-driven release checks

## Why

The release spec freezes shadow mode, release identity and the pairwise shadow runner, but nothing orchestrates a release candidate end to end: recorded or scripted traffic against the challenger build, side effects suppressed, results attributed to `ReleaseID`/`Variant`, and a release decision produced from evidence. Release gating also has no contract: which checks run, which evidence they require, and how a failing check blocks a release.

## What Changes

- Add shadow-run orchestration: a release candidate replays recorded traffic (cassettes) or runs a scripted suite against the new build without serving callers; the challenger executes in the frozen `Shadow` run mode.
- Add manifest-driven release checks: the `ReleaseManifest` computed by `Build` drives the checks that gate a release, including declared dependency evidence and compatibility evidence.
- Make the release decision a durable artifact: report, evidence references and decision carry release identity and are replayable.

This change complements the bounded online slice that `production-online-evaluation` landed; it does not re-implement online sampling, unscored semantics, provenance export or judge calibration.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `release`: shadow-run orchestration and manifest-driven release checks.

## Impact

Implementation stays inside the existing `evals` module boundary and builds on the frozen `RunMode`, `ReleaseManifest` and `evals.Shadow` contracts. No frozen port method changes. Scenario ids are proposed here and registered by task 1.1 at implementation admission.
