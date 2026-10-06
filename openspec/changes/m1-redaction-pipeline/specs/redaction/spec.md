# Spec Delta

## ADDED Requirements

### Requirement: Redaction pipeline and residency

The implementation MUST redact prompt input, tool results, and provider context before assembly. It MUST apply the same flow policy before every named store write and keep each `RedactionMap` separate with tenant, session, and independent retention. It MUST use tenant-keyed keys and stable pseudonyms within a session. Output rehydration MUST run before each released output window. It MUST drop unknown token-shaped values and count `gohan.redact.unknown_token` without recording entity or token content. Build MUST reject residency-bound flows when any reachable model profile or configured store has a region that does not match the tenant residency. `EraseSubject` and its scenarios remain owned by `m1-subject-erasure-residency`.

#### Scenario: pseudonym stable within session
ID: `redaction.pseudonym-stable-within-session`
- WHEN "Anna Petrova" appears in turn 1 and turn 4
- THEN the model receives the same token both times and stored history contains only the token

#### Scenario: stores never see raw entities
ID: `redaction.stores-never-see-raw-entities`
- WHEN a run completes
- THEN `SessionLog`, `EventLog`, `OutputStore`, and checkpoints contain no detected entity, and the `RedactionMap` is stored separately with tenant and session ownership

#### Scenario: streaming rehydration
ID: `redaction.streaming-rehydration`
- WHEN the model's answer contains a pseudonym split across streamed chunks or output windows
- THEN the client receives the real value intact and never receives a partial token

#### Scenario: hallucinated token dropped
ID: `redaction.hallucinated-token-dropped`
- WHEN the model emits a token that is not present in the run's map
- THEN the token is removed before release and `gohan.redact.unknown_token` increments without token content

#### Scenario: residency enforced at build
ID: `redaction.residency-enforced-at-build`
- WHEN a residency-bound tenant's flow can route to a profile in another region or uses a configured store outside the required region
- THEN `Build` fails and names the tenant, profile or store, required region, and actual region
