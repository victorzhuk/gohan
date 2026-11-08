# Spec Delta

## ADDED Requirements

### Requirement: Data analyst suspend, resume and page composition

A read-only warehouse query MUST carry a `PerSession` `SandboxPolicy` with an empty `Egress.Allow`, MUST inject its credential only through `Workspace.SetEnv`, and MUST redact output for injected values and personally identifying patterns BEFORE journaling and before an overflow body reaches the `OutputStore`. On suspension the produced workspace ref MUST be stored before the suspension persists, and a later turn on another stack MUST open from that ref and replay a journaled result.

#### Scenario: data analyst resumes and pages
ID: `sandbox.data-analyst-resume-and-page` (proposed)
- WHEN a session runs a read-only warehouse query that writes an extract, then suspends and resumes on a second stack that opens from the stored ref
- THEN the resumed turn reads the extract with no extra `Put` and no re-execution, and `read_output(ref, range)` pages back the requested slice with no personally identifying value