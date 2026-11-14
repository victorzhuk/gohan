# Design

## Context

The audit verified four record-integrity defects. State at admission:

- `docs/adr/` holds 189 files. Two claim 0170; 0177 is absent; the highest number is 0189.
- `openspec/scenarios.json` holds 764 rows; `openspec/specs/` holds 36 capability specs.
- `docs/overview.md` still reports 169 ADRs and 664 scenarios, points at `m0-core` as the only tasked change, breaks the capability table with a note between table rows, counts nine exception IDs as "Eight", and describes an `origin` vocabulary that omits the change-slug labels 166 registry rows already use.
- `docs/design/types.md` predates the optional-interface addition: regeneration adds two `CheckpointRuns` rows.

No port, type, adapter or runtime behavior is involved. Every defect is a record that disagrees with the tree.

## Goals and non-goals

Goals:

- Exactly one ADR per number, 0001–0190, with no gap and no duplicate.
- `routing` carries the same header and `Contract` structure as the other 35 specs, with its requirements, scenarios and IDs untouched.
- Every count and list in `docs/overview.md` reproduces from a fresh recount of the tree.
- `docs/design/types.md` equals a fresh `task spec:types` output.

Non-goals:

- No requirement semantics change, no scenario ID change, no test change, no registry change.
- No renumbering of the governed-call-authority record and no touching of its citations.
- No rewrite of `docs/overview.md` prose that is still true; only the stale claims change.

## Decisions

### 1. The research-report record takes 0177

`rg '0170-research-report'` over the tree, excluding `scratchpad/` and `.omp/`, returns two hits, both inside this change's own `proposal.md` and `tasks.md`. The governed-call-authority record keeps 0170 and its eight citing sites. The rename therefore touches no normative citation, and 0177 is the only free number below the max, so the file moves there rather than to the end of the sequence.

The file's heading form (`# NNNN. Title`) is its own and stays; only the number changes. The governed record is not rewritten to the `# ADR-NNNN:` form new records use, because that would churn a record eight sites already cite.

### 2. ADR-0190 records both decisions at admission

The renumber and the routing structure repair are record hygiene a reader will otherwise re-litigate: the next audit sees the same gap and the same structural outlier. ADR-0190 states the evidence (zero citations, shared-structure requirement from `openspec/project.md`) and the resulting inventory, so the repaired state is a documented state rather than an accident.

### 3. `routing` gets structure, not substance

`openspec/project.md` requires a `Capability:` header and a `## Contract` section on every spec. `routing` is the one spec without them. The header names the capability and the baseline version; the `Contract` section states, as normative prose, the ordering and fallback invariants the three requirements already impose. Nothing is added that the requirements do not already say, and no requirement, `Scenario:` heading or `ID:` line changes — the five IDs are bound to passing subtests and stay bound.

### 4. Overview counts come from the tree, never from the previous text

Each number in the "Where things are" and capability tables is produced by a count at write time: ADR files on disk, registry rows, spec directories, and the per-capability scenario counts. The capability-freeze note moves below the table so the table renders as one table. The exception list is the exact set difference between registry IDs and spec `ID:` lines, enumerated with a count that matches the enumeration, and the M4 scope line names `httpapi`, which `scenario-record-reconciliation` task 2.6 handed over.

The origin vocabulary gains the change-slug form with its resolution path: a slug label is provenance, acceptance is `deferred_to` plus `task spec:coverage`, and a slug with no live change directory is historic (ADR-0189).

### 5. `types.md` is generated, never hand-edited

The index is a projection of the spec tree. The repair is a regeneration, and acceptance is a second run producing no diff — a hand edit would pass the first check and fail the next spec change.

## Risks

- The working tree carries the in-flight `governance-fail-closed` batch. This change touches `docs/`, `openspec/specs/routing/spec.md`, `docs/design/types.md` and its own change directory; it does not read or write that batch's files, except that the overview recount counts registry rows it already added.
- The ADR count in the overview is only correct after ADR-0190 lands and the rename completes, so the recount runs last.
- `task spec:types` depends on a readable spec tree; a strict-validation failure aborts the repair instead of being recorded as a pass.
