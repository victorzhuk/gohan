# Tasks: m1

The milestone plan. `proposal.md` is the scope, `design.md` the handoff, the acceptance matrix and the sealed contract decisions. This file holds two lists: what M1 authoring does before any implementation, and which change implements which obligations.

A task is done when its `verify:` command is green and `task lint` and `task test` are green for what it touched. Implementation tasks repeat the milestone definition of done: every listed scenario ID has a passing subtest named exactly by its ID, and `task lint`, `task spec` (types index then the M1 gate) and `task test` are green.

## Authoring

1. [x] `docs`, `openspec`: re-establish the M0 handoff on the current tree. Decision: the archived closure record is the baseline; the earlier `missing_in_scope=9` verdict in `docs/design/api-review-m0-5.md` is superseded.
  - files: `openspec/changes/m1/design.md` §1
  - verify: `task tools:test`, `task lint`, `task test`, `task spec`, `task api:check`, `task examples:run` — all green at `63598e4`, `missing_in_scope=0 unregistered=0`.

2. [x] `openspec`: assign every M1 obligation to one change. Decision: the union of the `deferred_to: M1` registry entries (85 IDs) and the explicit proposal and overview obligations.
  - files: `openspec/changes/m1/design.md` §2
  - verify: every M1 registry ID appears exactly once in the matrix; every 2.13 obligation has an owner; no dependency cycle.
  - also: `taint.subflow-carries-origin` moved to M3 by explicit decision — `openspec/scenarios.json` amended and the reason plus the M3 obligation recorded in `design.md` §2.3.

3. [x] `docs/adr`: seal the shared taint and egress contract. Decision: ADR-0155.
  - files: `docs/adr/0155-m1-taint-and-egress-contract.md`, `openspec/changes/m1/design.md` §3
  - content: `TaintPolicy.MaxWindowBytes`; `std/taint` owns policy values; the native definition carries the hook; `Build` runs the trifecta check; egress owns `Capabilities.Exfil` derivation; the permission order is hard deny → taint → grant → decider.
  - verify: both packages cite ADR-0155 for D1–D7 and invent no alternative.

4. [x] `openspec/changes/m1-egress-policy`: author the change package. First authoring wave.
  - files: `openspec/changes/m1-egress-policy/{proposal.md,design.md,tasks.md,specs/tools/spec.md}`
  - owns: `tools.egress-metadata-blocked`, `tools.egress-redirect-revalidated`, `tools.egress-rebinding-safe`, `tools.egress-size-limit`, `tools.egress-audited`, and the Build wiring that finishes `tools.egress-policy-required`.
  - verify: `openspec validate m1-egress-policy --strict --no-interactive` → `is valid`; the design names the option, client and error surface; five M1 IDs are claimed and the M0-ranked one is listed as the Build integration obligation.
  - also: `docs/adr/0156-network-declaration-and-egress-audit.md` records the network declaration and the audit fields the design needed; `design.md` §3 gains D8.

5. [x] `openspec/changes/m1-taint-enforcement`: author the change package. Runs in parallel with task 4; both consume `design.md` §3.
  - files: `openspec/changes/m1-taint-enforcement/{proposal.md,design.md,tasks.md,specs/taint/spec.md}`
  - owns: `taint.substring-match`, `taint.short-untainted`, `taint.user-untainted`, `taint.window-since-user-turn`, `taint.exfil-denied`, `taint.side-effect-asks`, `taint.read-only-allowed`, `taint.gate-order`, `taint.trifecta-fails-build`, `taint.trifecta-with-policy`, `taint.no-exfil`.
  - verify: `openspec validate m1-taint-enforcement --strict --no-interactive` → `is valid`; the delta carries the `MaxWindowBytes` amendment the ADR decides and preserves every in-scope scenario block; extraction provenance and provider post-hoc enforcement are named as separate owners, not absorbed.

6. [x] `openspec`: review both packages before implementation admission.
  - lenses: specification/quality for each, plus security for both (untrusted input, network access).
  - check: scenario ownership, shared identifiers, permission precedence, dependency cycles, exact public integration paths, and that every behaviour proof in `design.md` §2 and the package design names a public entry point rather than a fixture.
  - observed: nine findings across the three lenses, every one a real admission blocker or major (network-registration seam, proxy semantics, `WithHTTP()` without a seam, the client bypass boundary, audit/metric leakage, missing client-behaviour tests, the live-deny ordering, the raw-window binding, unspecified matcher types). All were resolved in the artifacts, `docs/adr/0156-*` was written for the two contract additions the egress design needed, and a confirmation pass reported both packages admission-ready with no new blocker. Matrix checked as data: all 85 M1 IDs appear, no dependency cycle, `openspec/specs/` untouched.

7. [ ] `openspec`: author the remaining M1 changes when their dependencies settle. Each needs its own proposal, design, tasks and delta spec, and its own contract review before implementation.
  - lanes and owners: `design.md` §2.15 and the owner column of §2.
  - verify: for each change, `openspec validate <name> --strict --no-interactive` exits 0 before its implementation starts.

## Implementation order

Each change below is implemented only after its package is authored and reviewed (task 7). Shared driver files — `core/permission`, `core/native_options.go`, `core/native_effects.go`, `core/tool.go` — are edited in dependency order, never concurrently merely because two change packages exist.

1. [x] `m1-egress-policy` — tools egress: `std/egress`, `std/tool/http`, `WithEgress`/`WithHTTP`/`WithExfil`/`WithPrivateRead`, `WithEgressClient`, `Build` refusal and derivation.
  - observed: the five M1 subtests plus `tools.egress-policy-required` pass on the public path; `std/egress` carries the checked-address dial, per-hop revalidation, `Authorization` stripping and the size bound; a refusal is a typed `EgressDenied` that the classifier records as `egress_denied` with the host and reason only. Floor green: `task lint` 0 issues, `task test`, `task spec` (`missing_in_scope=0`), `task api:check`, `task examples:run`.
2. [x] `m1-taint-enforcement` — `std/taint` policy, matcher and `Default()`; `NativeSpec.TaintPolicy` and `LiveDeny`; window binding at the effect boundary; `Build` trifecta; gate wiring; `ToolSpec.Trust` and its manifest hash input.
  - observed: all eleven subtests pass; a tainted argument into a side-effect tool suspends with `ArgOrigins` populated; a live deny beats a taint `Ask` with no approval requested. Two production defects surfaced and are fixed: tool results reached history with the zero `Origin` (so the matcher skipped them and `std/guard` fencing was off for every tool result), and the approval path carried no argument origins. Floor green, `task spec:gate GATE=M1` down to `missing_in_scope=68` with only ids owned by other changes still open.
3. [ ] `m1-verify-public-paths`, `m1-redaction-pipeline`, `m1-schema-read-paths`, `m1-provider-conformance-contract` — independent lanes.
4. [ ] `m1-subject-memory` after taint; `m1-flags-control` after permission precedence; `m1-extraction-provenance` after the subflow conflict in `design.md` §2.3 is settled.
5. [ ] `m1-openai-provider`, `m1-anthropic-provider` after the provider contract; `m1-provider-tool-governance` after the contract and taint.
6. [ ] `m1-exact-match-cache` after final guard acceptance; `m1-hedged-model-calls` after provider-tool exclusion and admission accounting; `m1-model-retirement-cost` after provider metadata.
7. [ ] `m1-subject-erasure-residency`, `m1-postgres-stores`, `m1-run-notifications`, `m1-session-metadata-handoff`, then the decomposition of `design.md` §2.14.
8. [ ] `m1-s1-s2-acceptance` — quickstart and excursions S1–S2, `std.ExplainHandler`, and the integration obligations of `design.md` §2.13. S2 keeps its Postgres, `CompleteTx` and cross-pod approval acceptance; an offline memory-store run is not a substitute.
  - verify: `task examples:run`, `task examples:test`, and the S2 acceptance against `adapter/postgres` with testcontainers.

## Milestone definition of done

- Every ID with `deferred_to: M1` plus the M0-ranked IDs assigned in `design.md` §2 passes: `task spec:gate GATE=M1` reports `missing_in_scope=0`.
- `task lint`, `task spec`, `task test`, `task api:check` and `task examples:run` are green, each reported with its observed output.
- Every normative spec edit made by an M1 change carries its ADR, and `docs/design/types.md` is regenerated after each such edit.
- `docs/overview.md`'s M1 row and exit line match what shipped, including the `deferred_to` moves any change made.
