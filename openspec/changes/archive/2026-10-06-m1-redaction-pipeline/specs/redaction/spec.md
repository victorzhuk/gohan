# Spec Delta

## MODIFIED Requirements

### Requirement: Redaction, erasure, residency

The implementation MUST redact prompt input, tool results, and provider context before assembly. It MUST apply the same flow policy before every named store write and keep each `RedactionMap` separate with tenant, session, and independent retention. It MUST use tenant-keyed keys and stable pseudonyms within a session. Output rehydration MUST run before each released output window. It MUST drop unknown token-shaped values and count `gohan.redact.unknown_token` without recording entity or token content. Build MUST reject residency-bound flows when any reachable model profile or configured store has a region that does not match the tenant residency. Build MUST also fail closed when a configured store does not implement `RegionProvider` or returns an empty region. `EraseSubject` and its scenarios remain owned by `m1-subject-erasure-residency`.

#### Scenario: pseudonym stable within session
ID: `redaction.pseudonym-stable-within-session`
- THEN the model MUST receive the same token both times, and stored history MUST contain only the token

#### Scenario: stores never see raw entities
ID: `redaction.stores-never-see-raw-entities`
- THEN `SessionLog`, `EventLog`, `OutputStore`, and checkpoints MUST contain no detected entity, and the `RedactionMap` MUST be stored separately with tenant and session ownership

#### Scenario: streaming rehydration
ID: `redaction.streaming-rehydration`
- THEN the client MUST receive the real value intact and MUST NOT receive a partial token

#### Scenario: hallucinated token dropped
ID: `redaction.hallucinated-token-dropped`
- THEN the token MUST be removed before release and `gohan.redact.unknown_token` MUST increment without token content

#### Scenario: residency enforced at build
ID: `redaction.residency-enforced-at-build`
- THEN `Build` MUST fail and MUST name the tenant, profile or store, required region, and actual region
#### Scenario: erase reports held sessions
ID: `redaction.erase-reports-held`
- WHEN `EraseSubject` runs for a subject with three sessions, one under legal hold
- THEN `EraseSubject` MUST erase the two other sessions, MUST leave the held session untouched, MUST list it in `EraseReport.Held`, and MUST name it as held in `AuditErasure`

#### Scenario: erasure
ID: `redaction.erasure`
- WHEN `EraseSubject(tenant, subject)` runs
- THEN no session, event, output, checkpoint, grant, or redaction map for the subject MUST remain; audit records MUST remain with checksums; `AuditErasure` MUST be appended

#### Scenario: erasure removes blobs
ID: `redaction.erase-removes-blobs`
- WHEN `EraseSubject(tenant, subject)` runs for a subject whose sessions hold blob refs
- THEN `OutputStore.Get` MUST fail for every ref owned only by those sessions, and shared refs owned by other subjects MUST remain

#### Scenario: erasure removes memory
ID: `redaction.erase-removes-memory`
- WHEN `EraseSubject(tenant, subject)` runs for a subject with memory entries
- THEN `Stack.Memory` MUST return nothing for the owner, and the entries MUST be absent from the store
