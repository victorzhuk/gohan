# Tasks

## 1. Carrier and recipe

- [x] 1.1 Add `types.Quarantined[Out]{Value Out; Origins []Origin}` next to `Origin` and `BlockBase`, with a doc comment that names the recording rule; verify `go build ./...` and `go test -short -timeout 2m ./core/types/` pass (`core/types/message.go:27-46`; `openspec/specs/taint/spec.md:50`).
  - evidence: `core/types/message.go` carries `Quarantined[Out]` beside `Origin` (`:38-41`) and `BlockBase` (`:49-52`), with the recording-rule doc comment; `go build ./...` and `go test -short -timeout 2m ./core/types/` green.
- [x] 1.2 Add `flow.Quarantine[Out]` in `std/flow` over the existing `Extract` decode, schema and repair path, offering no tool and computing `Origins` from the distinct `BlockBase.Origin` values of the input blocks it read; verify a focused test decodes a scripted extraction and reports the input block's origin (`std/flow/extract.go:38-70`; `openspec/specs/taint/spec.md:50`).
  - evidence: `std/flow/quarantine.go` reuses the shared `Extract` path and its `WithRepairs` machinery; `std/flow/quarantine_test.go` proves origin reporting with dedup.
- [x] 1.3 Verify `Quarantine` refuses like `Extract` when the decoded value fails validation and reports no repair-only success; run `go test -short -timeout 2m ./std/flow/` (`std/flow/extract.go:146-193`).
  - evidence: `std/flow/quarantine_test.go` (structured-output refusal returns a zero value with the classified error; no repair-only success).

## 2. Recording path

- [x] 2.1 Add `gohan.RecordQuarantined[Out]` in `core`, appending one `ToolResult` block per distinct origin: `BlockBase.Origin` set to that origin, `Content` one `Text` block of the canonical JSON of `Value`, `Outcome` `Succeeded`; verify a focused test shows one block per distinct origin and the canonical JSON text (`core/types/message.go:106-113`; `core/drive_turn.go:944-948`).
  - evidence: `core/quarantine.go` (`RecordQuarantined`, one `ToolResult` per distinct origin in first-seen order, canonical JSON text); `core/quarantine_test.go` (`record-one-block-per-origin`).
- [x] 2.2 Verify the recorded block is inside the untrusted window and a later argument containing the extracted substring is tainted with that origin; run `go test -short -timeout 2m ./core/ -run TestQuarantine` (`std/taint/taint.go:113-116`; `openspec/specs/taint/spec.md:47`).
  - evidence: `core/quarantine_test.go` (`recorded-block-in-window`); the window rule that keeps the recorded block is the `OriginSystem`/`OriginUser` skip at `std/taint/taint.go:71`, with `afterLastUser` (`:113`) truncating at the last user turn.

## 3. Scenario and contract

- [x] 3.1 Drive `taint.quarantine-carries-origin` end to end: untrusted PDF blocks from a tool whose result carries `OriginTool{Name: "read_pdf"}`, `Quarantine[Invoice]` over them, `RecordQuarantined`, then an agent flow whose model calls `pay_supplier`; assert the run suspends for `HumanApproval` with `ArgOrigins["iban"].Name == "read_pdf"` and `gohan.taint.asked` increments (`openspec/specs/taint/spec.md:129-133`).
  - evidence: `core/quarantine_test.go` subtest named exactly `taint.quarantine-carries-origin` — suspension for `HumanApproval`, `ArgOrigins["iban"].Name == "read_pdf"`, `gohan.taint.asked{tool=pay_supplier}` incremented.
- [x] 3.2 Confirm the subflow scenario is untouched and unbuilt: it stays in the delta verbatim and its registry entry reads `deferred_to: M3`; verify with `grep -n "taint.subflow-carries-origin" openspec/scenarios.json` (`openspec/changes/m1/design.md:75`, `:81`).
  - evidence: the registry entry reads `deferred_to: M3` and the delta carries the scenario verbatim; no code builds the subflow case.
- [x] 3.3 Run `task lint`, `task test`, `task spec` and `task spec:gate GATE=M1`; verify `taint.quarantine-carries-origin` passes and no previously passing taint id regresses (`openspec/specs/taint/spec.md:61-133`).
  - evidence: `task lint` 0 issues; `task test` green; `task spec` `missing_in_scope=0`; `task spec:gate GATE=M1` `unregistered=0` and the only missing `taint.*` ids are `provider-tool-post-hoc` (provider change) and `subflow-carries-origin` (M3).
- [x] 3.4 Run `openspec validate m1-extraction-provenance --strict --no-interactive` and verify it passes.
  - evidence: `Change 'm1-extraction-provenance' is valid`.

## Workflow follow-up

- Apply the two delta requirements to `openspec/specs/taint/spec.md` and `openspec/specs/flow/spec.md`, and add the `Quarantine` row to the flow contract table, when the change is applied (`openspec/project.md`, *Change workflow* step 5).
- Archive the change after the project's review requirements are satisfied.
