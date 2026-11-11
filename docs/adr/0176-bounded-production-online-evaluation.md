# 0176. Bounded production online evaluation

Status: accepted

## Context

The `release` capability defines online sampling, repeated judges, median scoring, instability flags and release identity, but production evaluation has no failure, evidence or calibration semantics: timeouts, missing evidence and judge failures are undefined, results carry no durable provenance, and judge activation has no acceptance gate. `telemetry-export-contracts` and `telemetry-local-wal` (ADR-0174, ADR-0175) now provide the durable, identity-stable export seam such results need.

## Decision

Pull the minimum online evaluation slice forward from M4. Evaluation schedules only after primary completion, retains no request context or mutable history, and cannot recursively trigger evaluation. Admission is nonblocking and bounded by explicit queue capacity, worker cap, job deadline, retry bound, token/cost budget, sampling rate and closure policy; a full queue produces a skipped outcome. Timeouts, missing evidence, absent references and judge failures produce explicit unscored outcomes that never pass a required gate.

Quality, safety and execution results stay separate and named. Every result carries a stable evaluation ID, run correlation, evaluator/rubric/model versions, evidence references, status, score and scale when scored, sampling probability and reason, time, cost and error category. Results export durably through the spool pipeline with destination capability negotiation; retried exports preserve evaluation IDs. Scores are never disguised metric observations: numeric aggregation runs through existing bounded labels, and run IDs or evidence never enter metric dimensions.

Judge activation requires a versioned human-labelled calibration set, per-class error review and configured acceptance criteria. A judge model or rubric change invalidates prior approval until calibration passes again for that version. Temperature zero does not establish determinism; repeated judgments, median and instability handling are preserved. Judges receive untrusted evidence with no execution tools and no production credentials.

## Consequences

This slice owns exactly `release.online-bounded`, `release.online-unscored`, `release.online-provenance` and `release.online-calibration`; `release.online-sampling` and the remaining evals scenarios keep their milestone deferrals. Existing shadow, sampling and evals contracts are preserved unchanged. Full shadow orchestration, datasets and Langfuse integration keep their milestone owners. Bounded admission can skip work, so rates expose eligible, evaluated, skipped, failed and unknown counts with the selection policy.

## Alternatives

Landing the full M4 evals module first would carry dataset gating and shadow orchestration into production before failure semantics exist. Unbounded asynchronous judges stay cheap only until a provider outage stacks work behind primary completion. Treating missing evidence as a zero score hides coverage gaps and lets required gates pass on absent data; declaring unscored outcomes keeps the gap visible.
