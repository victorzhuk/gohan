## ADDED Requirements

### Requirement: Online evaluation is bounded
Evaluation MUST NOT delay primary completion. Queue, worker, deadline and budget limits MUST be explicit and enforced.

#### Scenario: Judges exceed capacity
ID: `release.online-bounded`
- **WHEN** judges block while primary completions exceed evaluation capacity
- **THEN** primary completion remains available and excess work is skipped without exceeding worker limits

### Requirement: Unscored evaluation cannot pass
Missing evidence, absent references and evaluator failures MUST produce explicit unscored outcomes. Required gates MUST NOT treat them as success.

#### Scenario: Evidence is unavailable
ID: `release.online-unscored`
- **WHEN** faithfulness has no claims, recall lacks references or a judge times out
- **THEN** each result is unscored with a reason and cannot pass a required gate

### Requirement: Evaluation provenance is durable
Results MUST retain evaluator versions, evidence references, sampling probability and release identity. Retried exports MUST preserve evaluation IDs.

#### Scenario: Result export resumes
ID: `release.online-provenance`
- **WHEN** a sampled result is exported after an outage and restart
- **THEN** its identity, provenance and independent scores survive without identity metric labels

### Requirement: Judge calibration is explicit
Activation MUST require configured criteria and versioned human labels. Model or rubric changes MUST invalidate prior calibration approval.

#### Scenario: Judge version changes
ID: `release.online-calibration`
- **WHEN** the judge model changes after calibration
- **THEN** activation is refused until the configured calibration gate passes for that version
