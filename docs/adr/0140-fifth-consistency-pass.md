# Fifth consistency pass: the open cross-spec findings

Status: accepted · Origin: review round, 2026-10-04

## Decision

The 42 contradictions that ADR-0138 left open are reconciled in the specs, each resolved to the side its scenario asserts. Three of them were contract choices, decided by the maintainer:

1. **`Block` origins.** `messages` declares `BlockBase{Origin Origin; Seq int64}` with `BlockOrigin() Origin`; every block struct embeds it and the interface is `{ isBlock(); BlockOrigin() Origin }`. A field and a method cannot share a name, so the accessor moved rather than the field.
2. **Metric labels.** The allow-list gains the labels telemetry's own metric table registers — `mode`, `name`, `target`, `provider`, `notes`, `from`, `to` — and "any other label fails `Build`" stands.
3. **`Partial`.** Two entry points: `Partial[Out](acc) (Out, error)` returns the validated declared type, and `PartialView[Out](acc) (PartialValue, error)` returns the unvalidated deep-partial view with unterminated containers closed. The scenario that needs the nested line object is written against `PartialView`.

Two further rulings the pass made, where the packets' own reading would have broken a rule of this corpus:

- `EntryState` does **not** gain `Succeeded`/`Failed`: `stores` already declares `RunState.Failed`, and one package cannot hold two members of that name (ADR-0139). The reconciliation instead lands on the entry's recorded result — a reconciled entry's `Result.Outcome` becomes `Succeeded` or `Failed` — which is what the `tools` rule and `tools.verify-reconciles-unknown` need.
- `performance` names the taint window `MaxWindowBytes` (default 256 KiB) as `taint` rule 1 already defines it; the budget row was referring to an undefined "64 KiB window".

Everything else is mechanical: the append shape per turn; `Done(StopCompleted)`/`Done(StopLimit)`; `Stale` covering `Resuming`; the notice outbox's at-least-once delivery with a consumer idempotency key; lease refresh at `HeartbeatEvery`; retention tiers without an archived threshold; `ClassVersionDrift` as a class that fails over, with its chains row and its `gohan.model_version_drift` catalog row; `Exfil` derived from the egress policy; provider tools shipping their own `ToolPolicy`; build-time versus call-time validation; the `Ask` payload being the `ApprovalRequest` (with `ApprovalRequest.Call` holding the `ToolUse`); a token consumed only by the `Resume` that decides the suspension, so quorum and single-use both hold; `HumanHandoff` as the one non-resumable reason; the `approval_refused` reason vocabulary; `not_executed:` outcomes; `OutputStore.Get` returning bytes; `Truncate` as a view that never reduces `SessionLog`; the allow-list, span and operation-name repairs; `AllowDrop` declared as a `std/flow` option; and the rule-numbering and metric-label drifts.

## Context and evidence

ADR-0138 reconciled 27 contradictions. Four narrower lenses then re-read the corpus slice by slice and returned 56 further items (11 BLOCKER, 28 MAJOR, 17 MINOR); 14 of those restated the earlier pass and were closed without an edit. The remaining 42 were reconciled here: 70 edits across 20 capability specs, every `old` string verified unique in its file before it was applied.

## Considered options

- Reconcile only the BLOCKERs and MAJORs and leave the MINORs recorded. Rejected: the MINORs are vocabulary and numbering drift that makes the record tiring to read, and they cost one edit each.
- Keep `EntryState` growing. Rejected: it recreates the collision class ADR-0139 removed.

## Verification

Two lenses re-read all 56 findings rows after the edits, independently of the packets. They confirmed each row's sites now agree and found seven residuals, each fixed in this pass: the `guards.origin-survives-conversion` scenario still called `Origin()`; the metric table's own `limit` label sat outside the allow-list, and its cost row needed the `WithTenantLabel()` note; `taint` rule 1's normative sentence still mandated an uncapped full-history match its own window cap forbids; `AllowDrop` was declared in `messages` but missing from `build`'s option list; `PartialView`'s type parameter was unused (it is now `PartialValue map[string]any` with no parameter); the `runtime.append-before-tool` and `tools.verify-reconciles-unknown` scenarios still carried wording the prose had outgrown; `flow`'s "a steer is never lost" lacked the `MaxTurns` carve-out; and the retention paragraph's first wording denied the archived threshold its own skip list asserts.

## Consequences

Every open BLOCKER and MAJOR from the review is now closed, and `docs/design/review-2026-10-04-findings.md` is the table of what each lens found, with `closed` marked per row. `python3 tools/gen_types_index.py` exits 0 and `docs/design/types.md` is regenerated. The corpus remains a specification without an implementation, so nothing here was executed: the gates that would exercise these contracts are `task spec:coverage` and the M0 chunks, and they arrive with the module in `openspec/changes/m0-core/tasks.md` row 1.
