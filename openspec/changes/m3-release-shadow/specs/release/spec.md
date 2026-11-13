## ADDED Requirements

### Requirement: Shadow-run orchestration serves no caller

Shadow-run orchestration MUST replay recorded traffic or run a scripted suite against the release candidate with the candidate in the frozen `Shadow` run mode, and MUST refuse to install any caller transport on the candidate run.

#### Scenario: recorded traffic replayed
ID: `release.shadow-traffic-replay`
- WHEN the orchestrator replays a cassette of recorded inputs against the candidate build
- THEN the challenger runs in `Shadow` mode, every input yields a pairwise row and no caller transport exists on the run

#### Scenario: scripted suite accepted
ID: `release.shadow-scripted-suite`
- WHEN the orchestrator runs a scripted suite instead of a cassette
- THEN each scripted case yields a pairwise row with score delta, tool-sequence diff, cost and latency

#### Scenario: candidate cannot serve
ID: `release.shadow-no-caller-serving`
- WHEN the orchestrator is asked to attach a caller transport to the candidate run
- THEN the request fails before any run starts and no caller receives a response

### Requirement: Release checks are manifest-driven

The checks that gate a release MUST be enumerated from the `ReleaseManifest` computed by `Build` and MUST consume the manifest entries plus declared dependency and compatibility evidence.

#### Scenario: checks enumerated from manifest
ID: `release.check-manifest-driven`
- WHEN the release check suite runs
- THEN every check name names a `ReleaseManifest` entry and a check exists for each declared surface

#### Scenario: dependency evidence required
ID: `release.check-dependency-evidence`
- WHEN a declared dependency has no pinned-version evidence record
- THEN its check fails naming the dependency and the missing evidence

#### Scenario: compatibility evidence required
ID: `release.check-compat-evidence`
- WHEN a declared surface has no compatibility policy record for the release
- THEN its check fails naming the surface and the missing record

#### Scenario: checks fail closed
ID: `release.check-fail-closed`
- WHEN any check fails, is unscored or cannot run
- THEN the suite denies the release and names every failing check

### Requirement: The release decision is durable

The check suite MUST emit one durable decision record carrying the check results with reasons, the shadow report reference, dataset hashes, judge fingerprints, `ReleaseID` and `Variant`, and re-emitting it MUST preserve the decision identity.

#### Scenario: decision survives replay
ID: `release.decision-durable`
- WHEN the decision record is re-emitted after a restart
- THEN its identity, check results, evidence references and release attributes are unchanged
