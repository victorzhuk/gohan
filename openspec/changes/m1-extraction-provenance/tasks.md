# Tasks

## 1. Carrier and recipe

- [ ] 1.1 Add `types.Quarantined[Out]{Value Out; Origins []Origin}` next to `Origin` and `BlockBase`, with a doc comment that names the recording rule; verify `go build ./...` and `go test -short -timeout 2m ./core/types/` pass (`core/types/message.go:27-46`; `openspec/specs/taint/spec.md:50`).
- [ ] 1.2 Add `flow.Quarantine[Out]` in `std/flow` over the existing `Extract` decode, schema and repair path, offering no tool and computing `Origins` from the distinct `BlockBase.Origin` values of the input blocks it read; verify a focused test decodes a scripted extraction and reports the input block's origin (`std/flow/extract.go:38-70`; `openspec/specs/taint/spec.md:50`).
- [ ] 1.3 Verify `Quarantine` refuses like `Extract` when the decoded value fails validation and reports no repair-only success; run `go test -short -timeout 2m ./std/flow/` (`std/flow/extract.go:146-193`).

## 2. Recording path

- [ ] 2.1 Add `gohan.RecordQuarantined[Out]` in `core`, appending one `ToolResult` block per distinct origin: `BlockBase.Origin` set to that origin, `Content` one `Text` block of the canonical JSON of `Value`, `Outcome` `Succeeded`; verify a focused test shows one block per distinct origin and the canonical JSON text (`core/types/message.go:106-113`; `core/drive_turn.go:944-948`).
- [ ] 2.2 Verify the recorded block is inside the untrusted window and a later argument containing the extracted substring is tainted with that origin; run `go test -short -timeout 2m ./core/ -run TestQuarantine` (`std/taint/taint.go:113-116`; `openspec/specs/taint/spec.md:47`).

## 3. Scenario and contract

- [ ] 3.1 Drive `taint.quarantine-carries-origin` end to end: untrusted PDF blocks from a tool whose result carries `OriginTool{Name: "read_pdf"}`, `Quarantine[Invoice]` over them, `RecordQuarantined`, then an agent flow whose model calls `pay_supplier`; assert the run suspends for `HumanApproval` with `ArgOrigins["iban"].Name == "read_pdf"` and `gohan.taint.asked` increments (`openspec/specs/taint/spec.md:129-133`).
- [ ] 3.2 Confirm the subflow scenario is untouched and unbuilt: it stays in the delta verbatim and its registry entry reads `deferred_to: M3`; verify with `grep -n "taint.subflow-carries-origin" openspec/scenarios.json` (`openspec/changes/m1/design.md:75`, `:81`).
- [ ] 3.3 Run `task lint`, `task test`, `task spec` and `task spec:gate GATE=M1`; verify `taint.quarantine-carries-origin` passes and no previously passing taint id regresses (`openspec/specs/taint/spec.md:61-133`).
- [ ] 3.4 Run `openspec validate m1-extraction-provenance --strict --no-interactive` and verify it passes.

## Workflow follow-up

- Apply the two delta requirements to `openspec/specs/taint/spec.md` and `openspec/specs/flow/spec.md`, and add the `Quarantine` row to the flow contract table, when the change is applied (`openspec/project.md`, *Change workflow* step 5).
- Archive the change after the project's review requirements are satisfied.
