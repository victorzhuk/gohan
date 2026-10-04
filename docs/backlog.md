# Backlog

Candidates raised after the capability freeze (ADR-0098). Each entry names the evidence and the seam it would touch; none is normative until promoted by an ADR.

| Candidate | Evidence | Seam |
|---|---|---|
| Detached children with push-based completion (Q25) | harness guides: push completion, kill by label | `subflows`, `suspension` |
| CaMeL-style planner over `flowdef`/`ExprLang` (Q26) | CaMeL, dual-LLM | `languages`, `taint` |
| Script tier for embedded languages (Q21/Q22) | WASM host-driven suspension | `languages` |
| `ToolProgress` event for long-running tools (`gohan.Progress(ctx, msg)`) | data-analyst UX | `tools`, `streams`, `agui` |
