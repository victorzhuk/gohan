# taint delta

## ADDED Requirements

### Requirement: Taint survives content transforms

ClearToolResults, compaction, notes and memory read-back MUST NOT launder taint: transformed or read-back blocks carry their origins. Taint matching MUST run over the post-redaction text the model receives, and window truncation MUST increment `gohan.taint.window_truncated`.

#### Scenario: clearing tool results does not launder taint
ID: `taint.clear-tool-results-no-launder`
- WHEN ClearToolResults replaces a tainted tool result and the history is compacted
- THEN the surviving blocks carry the original origins and the trifecta check still fires

#### Scenario: notes read-back carries origin
ID: `taint.notes-readback-carries-origin`
- WHEN a note written from tainted content is read back into assembly
- THEN the read-back blocks carry the note's origin and taint propagates

#### Scenario: window truncation reported
ID: `taint.window-truncated-reported`
- WHEN the taint window exceeds its cap and is truncated
- THEN `gohan.taint.window_truncated` increments once

#### Scenario: matching runs over post-redaction text
ID: `taint.matches-post-redaction`
- WHEN a Redactor pseudonymizes a tainted entity before assembly
- THEN taint matching evaluates the post-redaction text the model receives and the exfil check still fires on the pseudonymized content