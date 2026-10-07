# 0161. Requirement bodies state the normative contract

Status: accepted

## Context

The `context` capability carried two requirements whose bodies were scenario-only: `Counting and calibration` and `Compaction is persisted` each went straight from the heading to their scenario blocks. A requirement without a normative body fails archive validation ("must contain SHALL or MUST"), so the corpus could not be merged into the main specs even though every behaviour was already pinned by its scenarios and by the capability's Contract rules.

## Decision

Every requirement body in a capability spec states the normative contract its own scenarios bind. The two scenario-only requirements gain bodies that restate exactly what their scenarios and the existing Contract rules already require — calibration from reported usage persisted for replay, the overflow compact-and-retry-once rule, the `Compaction` block with `CoversUpTo`, pending-call survival, replay equality, guarded summaries, opaque-block model binding, and the `Compacted` audit record. The bodies pre-decide nothing beyond those scenarios, so the later compaction and budget changes keep their design freedom.

The projection requirement records in the same pass that `gohan.context.tokens_cleared` belongs to the budget stage: a projection clearing does not emit it.
