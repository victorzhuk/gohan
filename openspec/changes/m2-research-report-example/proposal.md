# Proposal: m2-research-report-example

## Why

`docs/design/scenarios.md:186` names `research-report` as the example that exercises deferred tool discovery, context guards over retrieved content and tree budgets through one composed run: "Write me a market brief" — deferred `search_tools` over 40 sources, fan-out over documents, notes for progress, report generation, run detached with client attach. `openspec/changes/m2/design.md:140` records the 2026-10-12 resolution that the two examples are M2-owned, and `:155` records the lane row: `research-report example | m2-research-report-example | open (M2-owned) | ... no directory exists under examples/ yet`. `openspec/changes/m2/tasks.md:45-47` requires the package itself to be authored first and requires each package to name the composed public-path dependencies it accepts, its offline verify commands and its scenario ids.

The composed behaviour is already implemented and proved upstream; this change composes it, it does not add harness behaviour. `m2-context-projections` (archived 2026-10-07) shipped projections that never write to the session log and never clear notes results (`core/projection_scenarios_test.go`). `m2-context-persisted-compaction` (archived 2026-10-11) shipped the durable compaction publication, the safe prefix and the append-and-hide view (`core/compaction_public_path_test.go`). `std/toolsearch` ships the `search_tools` meta-tool, the per-turn filter and the checkpointed activation set (`std/toolsearch/search.go`, `std/toolsearch/activation.go`). `core.Detached` and `Conversation.Attach` ship the detached lifetime (`core/stream_lifetime.go:19-27`, `core/conversation.go:30`, `core/attach.go:14`). None of them has an example that reaches them from one offline run, so a composition defect in their interaction — a deferred tool set that does not survive a compaction, notes that vanish when the summary hides the turn that wrote them, an attach cursor that replays a compacted span — has no example-level guard.

The gap is the composed example, plus the one requirement it makes observable that no single upstream change owns: that the persisted compaction survives *the example's* retrieval-heavy, deferred-tool tree and the client's reattachment still reads a coherent history.

## What Changes

- Add `examples/research-report`: an offline entry under `examples/` (its own package in the `examples` module) that composes `m2-context-projections` (`stores.ContextPolicy.Projections` carrying `std/context.ClearToolResults`) with `m2-context-persisted-compaction` (`std/context.Summarize` as the policy `Compactor`) into one `gohan.NativeSpec` flow, driven through `gohan.NewNativeConversation` with memory stores, exactly as `examples/excursions/main.go:29-85` composes its scripted path.
- Register exactly 40 `Deferred: true` source tools beside the always-loaded fixtures, so `std/toolsearch`'s filter decides what the first request carries. The activation set rides the checkpoint, so a run that discovers a source, compacts, suspends and resumes through `Replay` keeps that source active.
- Compose notes (`std/notes`) with compaction: the flow writes progress notes before the retrieval fan-out, and the notes still reach the next request after compaction hides the turn that wrote them.
- Compose a numeric tree budget over the fan-out (`limits.cost-accumulates`): the per-child cost accumulates into the parent run's total, and the run's own ceiling refuses a fan-out wider than the budget admits.
- Run the example detached with an `EventLog` and reattach a client from its last `Seq`, so the report body arrives through `Attach` after `Send` returned.
- Add one requirement block `Example composition: research report` to the `context` capability with the scenario id `context.research-report-compaction`, and its registry entry in `openspec/scenarios.json`. Existing capability ids keep their names; no existing scenario id changes owner.
- Fixtures are deterministic and offline-only: a scripted `types.Model` and in-memory stores. No credential, no network call, no daemon, no container. The example's `README.md` states the requirement ids it covers, the offline commands, the fixtures, the versions it pins and its `explain` surface.
- Add `research-report` to the `examples:run` target loop in `Taskfile.yml` and let `examples:test` pick it up through `./...`.
- This change's own package documents — `proposal.md`, `tasks.md`, `design.md` and `specs/context/spec.md` under `openspec/changes/m2-research-report-example/` — are deliverables of this change, not a prior authoring step outside its scope. The implementation scope therefore covers the authoring lane alongside `examples/research-report`, `Taskfile.yml`, `openspec/scenarios.json`, the lane row in `openspec/changes/m2/design.md` and the M2 exit row in `docs/overview.md`.

Catalog recipes are named as prerequisite lanes, not reimplemented. The catalog row for `research-report` lists `MapReduce`, `RAG` and `Judge` (`docs/design/scenarios.md:186`); the M2 milestone assigns the `std/flow` recipe set — `Extract`, `Classify`, `Route`, `MapReduce`, `Pipeline`, `RAG`, `Judge`/`Refine` — to M4 (`docs/overview.md:116`). `std/flow` currently ships only `Extract`, `Classify`, `Title` and the quarantine helper (`std/flow/*.go`). The example therefore composes the M2 surfaces it can reach and marks the recipe-dependent fan-out as an unmet prerequisite: the retrieval fan-out runs as the composed path the example can prove offline today (deferred discovery, per-child cost, ordered collection of per-source results), and it must not claim a `MapReduce`/`RAG`/`Judge` leg it cannot execute. The `subflows.collect-partials` id is registry-marked `deferred_to: "M3"` (`openspec/scenarios.json:1104-1109`), so the example records partial collection as a prerequisite-lane obligation, never as evidence. The data-analyst sibling package (`m2-data-analyst-example`) is a separate change.

No new `Sandbox` or `Workspace` port method, no change to `types.SandboxResources` (`core/types/sandbox.go:96-99`), no new harness seam and no change to any existing scenario requirement. The example adds no core, std or adapter code.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `context`: one ADDED requirement block `Example composition: research report` binds the composed example to `context.research-report-compaction` — the persisted compaction, the deferred tool activation and the progress notes all survive one detached, compacted, reattached research run. Every existing `context` requirement block stays verbatim.

## Impact

Affected code: `examples/research-report/` (new package: entry, fixtures, offline test, `README.md`), `examples/go.mod` (no new dependency — the entry uses `github.com/victorzhuk/gohan/core`, `core/types`, `core/stores` and the already-required `std` packages), `Taskfile.yml` (`examples:run` loop gains `research-report`), `openspec/scenarios.json` (one registry entry for `context.research-report-compaction`), `openspec/changes/m2/design.md` (lane row marked done) and `docs/overview.md` (M2 exit row). No `core`, `std` or `adapter/*` file changes: the example consumes shipped public paths only.

Affected documents: this package's own `openspec/changes/m2-research-report-example/proposal.md`, `tasks.md`, `design.md` and `specs/context/spec.md`, plus the mirrored copy under `openspec/drafts/m2/changes/m2-research-report-example/`. The four documents are deliverables of this change and stay inside its scope; the mirror carries the same four documents.

The authoring lane lands no Go code of its own. The implementation tasks are numbered in `tasks.md` and are executed after this package validates.

The M2 exit row in `docs/overview.md:114` requires both example packages to be valid and run offline; this package supplies the valid-and-offline half for `research-report` and states the unmet recipe prerequisite explicitly rather than hiding it behind the archived M2 lanes. Strict validation is not the check: this package's `.openspec.yaml` carries unrecognised `parent`/`revision`/`split` keys, so `openspec validate --strict` reports issues on the keys regardless of the documents. The acceptance is the worktree check in tasks.md 1.1 — the four document greps plus the `openspec/scenarios.json` parse.