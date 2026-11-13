# Proposal

## Why

The audit verified four record-integrity defects in the non-normative map and the ADR inventory:

1. **Duplicate ADR number** (Low). Two files claim 0170: `0170-governed-call-authority-and-runtime-repair-baseline.md` and `0170-research-report-resume-and-cost-evidence.md`. Number 0177 is absent (the directory jumps 0176 → 0178). The governed-call-authority record has 8 citing sites (path-form); the research-report record has zero citations in `openspec/specs/`, `docs/` and `openspec/changes/` — renumbering it to 0177 needs no normative citation updates.
2. **Stale entry map** (Low). `docs/overview.md:50` claims 169 ADRs (disk: 186 files, 185 unique numbers until the renumber); `:101` claims 664 scenarios (registry: 732 at audit time); `:53` points at `m0-core` as fully tasked and `m1`-`m4` as stubs (current planning is the `m3` umbrella plus eight `m3-*` changes); `:84` inserts a prose line inside the capability table, breaking it; `:103` says "Eight registered IDs" but enumerates nine; `:105` defines an origin vocabulary that 166 registry rows (change-slug origins) already use without sanction.
3. **Routing spec structure** (Low). `openspec/specs/routing/spec.md` is the only one of 36 specs without a `Capability:`/version header and without a `## Contract` section (`openspec/project.md:16` requires both). Its five scenarios carry IDs and bind tests; only the document structure is missing.
4. **Stale types index** (Low, driver-demonstrated). Regenerating `docs/design/types.md` from the working-tree specs adds two `CheckpointRuns` rows (types table and interfaces table); the committed index predates the optional-interface addition.

## What Changes

- Renumber the research-report ADR to 0177 (file rename plus its own heading); the governed-call-authority ADR keeps 0170 and all its citations.
- Refresh `docs/overview.md`: ADR and scenario inventories recounted at admission, current milestone paths, one continuous capability table, corrected exception count and list (after `scenario-record-reconciliation` settles it), and an origin vocabulary that sanctions change-slug origins with their resolution path.
- Give `routing` the shared header and a Contract section derived from its existing requirements; no requirement, scenario or ID changes.
- Regenerate `docs/design/types.md`.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `routing`: document structure only (header and Contract section). No requirement text changes; this change edits main specs directly and archives with `--skip-specs`, following the `repair-spec-strict` precedent.

## Impact

Docs and one structural spec edit. ADR-0190 records the renumber and the routing structure repair at admission. Non-normative mentions of the research-report ADR in `.omp/plans/` and `scratchpad/` are out of scope. No scenario, registry row or test changes.

Dependencies: admit after `scenario-record-reconciliation` so the overview exception list and scenario count reflect the settled registry. The renumber task itself is independent and may land first.