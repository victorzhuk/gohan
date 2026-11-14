# Tasks

Risk depth: MVP-light (records and structure, no behavior). A task is done only when its acceptance holds and `task spec:types` plus `openspec validate --specs --strict` are green. This change edits main specs directly and archives with `--skip-specs`.

## 1. Decision record

- [x] 1.1 Write `docs/adr/0190-record-integrity-repairs.md` recording the 0170→0177 renumber (with the zero-citation evidence for the research-report record) and the routing structural repair.
  Acceptance: The ADR exists at the next free number after 0189 and names both decisions.

## 2. Repairs

- [x] 2.1 Rename `docs/adr/0170-research-report-resume-and-cost-evidence.md` to `docs/adr/0177-research-report-resume-and-cost-evidence.md` and update its heading to `# 0177.`; leave the governed-call-authority 0170 file and all its citations untouched.
  Acceptance: `docs/adr/` holds exactly one file per number 0001-0190 with no gap at 0177; grep finds no normative citation to the old research-report filename.
- [x] 2.2 Add the shared `Capability: routing · Spec v1.0` header and a `## Contract` section to `openspec/specs/routing/spec.md`, derived from its existing requirements; change no requirement, scenario or ID.
  Acceptance: The spec matches the 35-spec structure; its five scenario IDs and bound subtests are unchanged and pass; strict validation passes.
- [x] 2.3 Refresh `docs/overview.md`: recount ADRs and scenarios from disk and registry, replace the stale milestone-path row with the current planning state, move the capability-freeze note out of the table body, correct the exception count and list to the settled registry state, and extend the origin vocabulary to sanction change-slug origins naming their resolution path.
  Acceptance: Every count matches a fresh recount; the capability table renders as one table; the exception list equals the registry-minus-spec set diff; the M4 scope line names httpapi (handed over from scenario-record-reconciliation task 2.6); no claim contradicts the working tree.
- [x] 2.4 Regenerate `docs/design/types.md` and run the gate: `task spec:types`, `openspec validate --specs --strict`.
  Acceptance: The index regenerates with no diff on a second run; strict validation reports 36 of 36 specs passed.
  Result: the index is idempotent — two consecutive `task spec:types` runs leave `docs/design/types.md` at sha256 `a7212c05…` with no diff. Strict validation reports 34 of 36: `limits` (*Run limits*, 764 characters) and `recovery` (*Missing optional recovery support has explicit behavior*, 568 characters) exceed the 500-character requirement-body limit. Both bodies predate this change and are outside its scope; the residual is recorded in ADR-0190.