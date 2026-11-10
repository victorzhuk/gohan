# Design: Bounded production evaluation

## Context

The release spec already defines online sampling, repeated judges, median scoring, instability and fingerprints. Preserve these contracts.

## Goals / Non-Goals

Deliver the minimum online evaluation slice before the remaining M4 scope. No automatic release promotion or universal judge threshold.

## Decisions

Use the existing evals boundary with deterministic evaluators and external judge adapters. No model SDK enters core. Snapshot redacted evidence, release identity and sampling decisions at completion. Schedule after primary completion without retaining request contexts or mutable history. Evaluation jobs cannot recursively trigger evaluation.

Require queue capacity, worker cap, job deadline, retry bound, token/cost budget, sampling rate and closure policy. Admission is nonblocking. Full queues produce skipped outcomes. Timeouts, missing evidence, absent references and judge failures produce unscored outcomes. They never pass required gates.

Keep separate named quality, safety and execution results. Records carry stable evaluation ID, run correlation, evaluator/rubric/model versions, evidence references, status, score and scale when scored, sampling probability and reason, time, cost and error category. Extend export records additively for evaluation results with destination capability negotiation. Scores are not disguised metric observations. Aggregate numeric scores through existing bounded labels. Never place run IDs or evidence in metric dimensions.

Faithfulness measures support in supplied context, not truth. No extracted claims is unscored. Context recall requires reference claims. Safe success requires established functional success and absence of policy/state violations. Otherwise report false or unknown according to evidence. Rates expose eligible, evaluated, skipped, failed and unknown counts and selection policy. Tool schema validation is deterministic; tool selection needs expected intent or a versioned rubric. Token-log-probability utility is excluded because portable providers do not guarantee its evidence.

Judge activation requires a versioned human-labelled calibration set, per-class error review and configured acceptance criteria. Model/rubric changes invalidate prior calibration approval. Temperature zero does not establish determinism. Preserve repeated judgments, median and instability. Judges receive untrusted evidence with no execution tools or production credentials.

## Research review

The attached research has numbered references without a bibliography. Its 85 percent agreement gate is unverified and is not adopted. Faithfulness score 1 cannot establish real-world truth. Keep dimensions separate and declare denominator and unknown handling.

Primary definitions: https://docs.ragas.io/en/stable/concepts/metrics/available_metrics/faithfulness/ and https://docs.ragas.io/en/stable/concepts/metrics/available_metrics/answer_relevance/.

## Risks / Trade-offs

Risk depth: existing-service-strict. Judge bias requires calibrated labels. Sampling bias requires recorded probabilities. Bounded admission can skip work and must expose coverage. Focused accounting tests cover rare budget races beyond broad acceptance.

## Migration Plan

Land after telemetry-local-wal and required release identity support. Opt in per flow. Disable new evaluation admission and drain owned jobs to roll back. Update existing architecture, testing and compatibility guidance with implementation.
