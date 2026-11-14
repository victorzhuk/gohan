# ADR-0190: Record integrity repairs

Date: 2026-11-14
Status: Proposed (`records-map-integrity`)
Supersedes: none

## Context

`docs/adr/` held two records numbered 0170. Number 0177 was the only free number below the maximum 0189. `openspec/specs/routing/spec.md` lacked the header and Contract structure used by the other capability specs.

## Decision

### Research-report record takes 0177

Rename the research-report record to `docs/adr/0177-research-report-resume-and-cost-evidence.md`. Its old path has no citation outside `openspec/changes/records-map-integrity/`. The record keeps its existing heading form with the new number.

`docs/adr/0170-governed-call-authority-and-runtime-repair-baseline.md` keeps 0170, its heading and its eight citing sites outside this change.

### Routing spec structure

`openspec/specs/routing/spec.md` now matches the `Capability:` header and `## Contract` structure carried by every other capability spec. `openspec/project.md` requires the Contract section. The routing Contract derives from its three existing requirements, without changing requirements, scenario headings or ID lines.

## Consequences

- `docs/adr/` has one record per number 0001-0190, without gaps or duplicates.
- All 36 capability specs share the header and Contract structure.
- Future audits read the inventory as repaired state.
- Two specs still carry a requirement body above 500 characters: `limits` (*Run limits*) and `recovery` (*Missing optional recovery support has explicit behavior*). Both predate this change and sit outside its scope, so `openspec validate --specs --strict` reports 34 of 36 until a change splits those bodies along scenario boundaries.
