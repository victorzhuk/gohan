# Third consistency pass

Status: accepted · Origin: review round, 2026-10-04

## Decision

The following record defects are corrected in place, with no change of the contract:

1. `openspec/specs/tools/spec.md` stated the `MaxOutput` default as 16 KiB in its `ReadBack` rule and in `tools.large-output-stored`, against 64 KiB in its own defaults rule and in ADR-0106. The two 16 KiB sites now read 64 KiB.
2. ADR-0015 said its supersession left six store ports; the `Stores` bundle in `openspec/specs/build/spec.md` has nine mandatory ports (SessionLog, Checkpoints, Journal, Runs, AuditLog, EventLog, OutputStore, NotesStore, Feedback) with `RetentionSource` and `SessionIndex` as optional interfaces. Its status line now says so.
3. `openspec/specs/cache/spec.md` cited ADR-0019 for zero-usage cache hits. ADR-0071 owns the v1 cache contract; the citation now points at it.
4. Eight capability specs (`assembly`, `cache`, `decider`, `engines`, `flags`, `languages`, `lifecycle`, `performance`) declared their v0.13 restatement as the contract source, although later ADRs own parts of those contracts. Their headers now name the owning ADR where one exists and no longer claim the restatement is the whole record.
5. `docs/design/go-baseline.md` presented `errors.AsType`, `t.ArtifactDir` and `tool` directives as 1.27 additions; they arrived in 1.26, 1.26 and 1.24. The paragraph now says which release added each, and that the baseline is the 1.26 and 1.27 toolchains together.
6. `docs/design/scenarios.md` used `gpt-5.2-2026-06-01`, which is not a published snapshot; the pinned version in both listings is now `gpt-5.2-2025-12-11`.
7. 88 of the 89 v0.13-derived ADR H1 lines and filenames carried truncated prefixes of their decision line (`0058-go-1-27-baseline-…`, `0025-gohan-build-…`, `0052-modelprofile-version-pins-…`); the decision bodies were complete. The H1 of every ADR-0001–0089 now reproduces its `## Decision` text in full, and each filename carries a slug of that text.

## Context and evidence

The 2026-10-04 review read every capability spec against its ADRs and against `openspec/scenarios.json`. Findings 1, 2 and 7 are defects an implementer or a reader would have hit in M0 (`tools.large-output-stored` is a task-30 scenario); 3–6 are provenance defects that made the record misleading to read. ADR-0099 and ADR-0100 are the earlier passes of the same kind.

## Consequences

`tools.large-output-stored` asserts the 64 KiB default, so `std/tool` and `std/tool/exec` implement one number. The eight spec headers now point at the ADR that governs them, and the ADR-0001–0089 index reads its own decisions instead of mid-word prefixes. Not corrected by this pass: 90 ADRs are cited from no document outside `docs/adr/`; the ADR decision bodies were already sound, so nothing was lost by the titles' truncation.
