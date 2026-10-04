# Fourth consistency pass: cross-spec reconciliation

Status: accepted · Origin: review round, 2026-10-04

## Decision

Twenty-seven contradictions found by reading every capability spec against the others are reconciled in the specs, each resolved to the side its scenarios assert. No acceptance criterion changes.

1. `assembly`: the second `ContextProvider` declaration and the second slot enum (`Slot`) are deleted; `AssembleInput.Providers` is keyed by `ContextSlot`.
2. `streams`, `stores`, `recovery`: `Done` gains `Seq`; the stored `Run` gains `Turn`, `Seq`, `Pending` and `Cost` so `Inspect` can report what `tools.inspect-from-another-pod` asserts; `Inspect` stops claiming limits-remaining and returns `RunView{Run, Input}`.
3. `messages`: the error catalog gains rows for `ErrVersionConflict` and `ErrStructuredOutput`, `ErrNotSuspendable` joins the `gohan.input_invalid` row, and `ErrToolDescription` joins the `gohan.configuration` row; `gohan.tool_denied` cites gate `DenyVerdict`; `InlineBlobBytes` is declared as the harness spill constant, with `Caps.Blobs.MaxBytes` as the hard per-block cap.
4. `tools`: provider provenance sets `Executor` on the registered spec, not on `ToolUse`, which has no such field.
5. `model`: a mid-stream failure appends the partial assistant message with `Finish: FinishError`; the trailing `Error` block is dropped, because `BlockKind` is closed and has no error kind.
6. `limits`, `permission`: the mandatory-when-`Pricing` clause on `MaxCost` is dropped and the limit word is `"MaxCost"` everywhere; the pending-approval cap is per subject and tenant, so it is `ApprovalPolicy.MaxPending` and not a `RunLimits` field; `ApproveScope(2h)` keeps its one-argument form; the preset defaults sentence distinguishes fields a preset leaves zero from the preset's own values.
7. `stores`: the originator token rule is restated for a `Principal` that cannot carry a token, and the `no-secrets-stored` scenario becomes expressible against the declared types.
8. `chains`: the ordering constraints name declared kinds — `Hooks`, `Router` and `Budget` join `StepKind`, and the user slots are named `KindUser`; `ClassContentPolicy` surfaces the `gohan.content_policy` problem rather than a guard block.
9. `streams`, `suspension`: notice `Reason` carries the Go constant name (`"HumanApproval"`); `NoticeKind` gains its rendering rule and the notice body gains its JSON field names.
10. `context`, `identity`: both rule lists are renumbered into order, so cross-spec references such as "identity rule 8" resolve by number.
11. `subflows`, `sandbox`: `MaxSandboxSeconds` is named as a per-tree limit.
12. `release`, `messages`: `ShadowSuppressed` is declared and catalogued under `gohan.tool_denied`.
13. `interop`: `AwaitingInput` is stated as already declared by `suspension`.

## Context and evidence

The pass read all 35 capability specs, `openspec/scenarios.json` and the ADRs the specs cite. Four of the findings stopped implementation outright: assembly declared one interface twice, `Done` lacked the field its own scenario asserts, `Inspect` promised stores-only answers the stored record could not give, and eight package-scope name collisions made the single `core` package uncompilable (ADR-0139). ADR-0099, ADR-0100 and ADR-0137 are the earlier passes of the same kind.

## Considered options

- Rename the losing member in each of the eight collisions and keep one package. ADR-0139 supersedes the single-package layout instead, because the collision class repeats with every capability added.
- Record all findings and let the first implementer of each capability choose the shape. That moves decisions into code and out of the record.

## Consequences

Each reconciled site now agrees with the scenario that tests it. The generated index reported no duplicates before this pass and after it, because it never compared a `type` against a `const` of the same name — task 5 in `openspec/changes/m0-core/tasks.md` adds that check. The pass does not clear the corpus: the same review's narrower lenses found further contradictions, recorded with their evidence under *Spec consistency debt* in `docs/design/risks.md`.
