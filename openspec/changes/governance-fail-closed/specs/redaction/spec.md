# redaction delta

## ADDED Requirements

### Requirement: Redaction failure fails closed

A Detector, Redactor or KeySource error MUST fail the turn before unredacted content passes the redaction placement. The harness MUST NOT continue with raw entities in the prompt, stores or output.

#### Scenario: detector outage fails the turn
ID: `redaction.failure-fails-closed`
- WHEN the detector errors during input redaction
- THEN the turn fails with the redaction error and no raw content reaches the model or any store

### Requirement: Telemetry and evaluation export carry no raw content

Telemetry capture MUST run after redaction at its placement, and exported evaluation signals MUST carry no raw entity or injected secret.

#### Scenario: telemetry capture is redacted
ID: `redaction.telemetry-capture-redacted`
- WHEN a turn containing a detected entity is captured by telemetry
- THEN spans and events carry the redacted content only

#### Scenario: evaluation export is redacted
ID: `redaction.eval-export-redacted`
- WHEN evaluation signals for a run with detected entities and injected secrets are exported
- THEN no raw entity or injected secret value appears in the export records

### Requirement: Erasure covers notes

Subject erasure MUST delete the subject's notes and note-derived records together with the subject's other records.

#### Scenario: notes erased with the subject
ID: `redaction.erasure-covers-notes`
- WHEN a subject with stored notes is erased
- THEN no note or note-derived record for that subject remains in any store