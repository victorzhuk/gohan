# ADR-0118: Token budget — deterministic heuristic, usage calibration, overflow backstop

Status: accepted · Origin: grill round 44 (2026-09-30)

## Decision

`model.ContextBudget` is the one place tokens are counted: `Limit = ContextWindow − MaxTokens − Margin`, `Estimated` from the `TokenEstimator` port (default `std/tokens.Heuristic`, deterministic per-block byte ratios) times a per-profile calibration ratio learned from `Usage.InputTokens` and clamped to [0.5, 2.0]. The ratio is persisted in the checkpoint and the `Compacted` audit record so replay reproduces decisions. `TokenCounter` is an optional model interface used only at the compaction decision. A `ClassContextOverflow` on an accepted request forces one compaction with a raised ratio and one retry, then fails `Permanent`.

## Context and evidence

Practitioner guidance: exact tokenizers or provider count endpoints where available, calibration from the previous response's usage, byte heuristics otherwise, an explicit output reserve plus cushion, and overflow errors as the backstop. Three gohan rules depended on a token count no spec defined, and an adaptive estimate would have broken projection determinism unless the ratio travels with the run.

## Consequences

`model` v1.2 (`TokenBudget`, `TokenEstimator`, `TokenCounter`, `ContextBudget`, `ErrNoTokenEstimator`, three scenarios), `context` v1.2 (rules 9–10, three scenarios), `State.Calibration`, `assembly` wording; task 19; calibration scenarios in M2.
