# Tasks

Commands carry a `timeout`. A `-run` expression or a grep that matches nothing is a failure, not a pass: each check names the output it must match and records the observed result. This change is authored after the fact of the M2 lanes; every task below starts unchecked because the tags it cuts depend on lanes that are still landing.

## 1. The freeze record

- [ ] 1.1 Add an `M2 freeze` section to `docs/design/compatibility.md`, beside the existing `v1-candidate (M0.5)` section, carrying the frozen port shapes as one table: port, declared method set, the file:line that ships it, and the stance from `openspec/changes/m2/tasks.md:53` — `runtime.Runtime` unchanged, `runtime.AgentRun` grown only when two shipped backends need it, `State.Backend` the only backend state carrier, `StepGranularity` two values, `types.Sandbox` with `Open`, `types.Workspace` with its six methods, and `types.SecretResolver` as declared. Verify: `timeout 60s git diff docs/design/compatibility.md` shows the section and every table row names a `core/...` file:line; `timeout 2m task spec:types` reports no drift (`docs/design/types.md` unchanged).
  - evidence: `openspec/changes/m2/tasks.md:53`; `core/runtime/runtime.go:28-39`, `:62-67`, `:70-75`, `:83-97`; `core/types/sandbox.go:71-84`; `core/types/secret.go:8-10`; design D2.
- [ ] 1.2 Record the per-module `v1.0.0` tag plan in the same section as one table over every module the `api:check` loop visits — module dir, module path, current baseline, freeze tag, precondition — with the release order "root, then adapters" and each adapter's precondition "conformance suite green against core v1". Verify: the table has a row for `.`, `adapter/anthropic`, `adapter/docker`, `adapter/eino`, `adapter/openai`, `adapter/otel`, `adapter/postgres` and a noted conditional row for `adapter/e2b`; each current-baseline cell equals the tag `timeout 5m task api:check` prints for that module.
  - evidence: `Taskfile.yml:53-69`; `docs/design/compatibility.md:7-9`; design D2, D4.
- [ ] 1.3 Record the `examples/` exclusion in the same section: the loop visits `.` and `adapter/*` only, so the `examples` module has no version line to baseline and carries no tag after the freeze; name `task examples:test` and `task examples:run` as its witness. Verify: `timeout 60s grep -n 'for dir in' Taskfile.yml` shows `. adapter/*` with no `examples`, and the recorded text matches it.
  - evidence: `Taskfile.yml:58`, `:124-132`; `docs/design/compatibility.md:9`; design D5.

## 2. The module boundary in `tools/apicheck`

- [ ] 2.1 Make `loadPackages` (`tools/apicheck/main.go:205-244`) skip a directory that holds its own `go.mod`, so a run freezes exactly the module directory it is given and a root run stops reporting `examples/**` and `adapter/**` symbols under the root module. Verify: `timeout 2m go test -timeout 2m ./tools/apicheck` passes, and `timeout 2m go run ./tools/apicheck -dir . -tag v0.1.0` prints no `examples/` symbol in its report.
  - evidence: `tools/apicheck/main.go:205-244`; `docs/design/api-review-m0-5.md` (*Gate evidence*, "Surface covered"); design D3.
- [ ] 2.2 Confirm the change keeps the existing tool behaviour: `timeout 2m go test -timeout 2m ./tools/apicheck -run 'TestDiffTrees|TestDiffTreesUnexportedChangeCompatible|TestReportBreaks'` executes and reports no subtest missing, so the v1-fails policy (`reportBreaks`, `tools/apicheck/main.go:84-101`) is still the one the tool applies.
  - evidence: `tools/apicheck/main_test.go:30`, `:136`, `:152`; design D1, Risks.

## 3. Cutting `v1.0.0`

- [ ] 3.1 Confirm the preconditions before any tag is cut: the M2 adapters are landed and their conformance suites are green (`adapter/docker` and `adapter/eino` present, `adapter/e2b` present if M2 lands it), and `timeout 5m task spec:gate GATE=M2` reports `missing_in_scope=0`. Verify: the gate output names `missing_in_scope=0`; a nonzero value stops the tag cut and the unmet row is recorded in the freeze section instead of hidden.
  - evidence: `openspec/changes/m2/tasks.md:45`, `:50`; `openspec/changes/m2/design.md:149-150`; design D4.
- [ ] 3.2 Tag the root module `v1.0.0` first, at the HEAD the gate in 3.1 passed on. Verify: `timeout 60s git tag --list 'v*'` lists `v1.0.0` and the tag resolves to that HEAD; the release-order rule `docs/design/compatibility.md:9` is met because it precedes every adapter tag.
  - evidence: `docs/design/compatibility.md:7-9`; design D4.
- [ ] 3.3 Tag each adapter `<dir>/v1.0.0` after the root tag, each one only after its conformance suite passes against core `v1`: `adapter/anthropic/v1.0.0`, `adapter/docker/v1.0.0`, `adapter/eino/v1.0.0`, `adapter/openai/v1.0.0`, `adapter/otel/v1.0.0`, `adapter/postgres/v1.0.0`, and `adapter/e2b/v1.0.0` when that module lands. Verify: `timeout 60s git tag --list 'adapter/*'` lists every adapter module's tag, and each one is cut only after its own conformance run is green against core `v1`.
  - evidence: `docs/design/compatibility.md:8-9`; `openspec/changes/m2/design.md` §2; design D4.
- [ ] 3.4 Record the cut: add the `1.0.0` release entry that closes `## [Unreleased]` in `CHANGELOG.md`, and update the M2 row's exit line in `docs/overview.md` to what shipped. Verify: `timeout 60s git diff CHANGELOG.md docs/overview.md` shows both records, and no other process doc changed.
  - evidence: `CHANGELOG.md:5-7`; `docs/design/compatibility.md:7`; `docs/design/api-review-m0-5.md`; `openspec/changes/m2/tasks.md:52`; design D6.

## 4. `api:check` meaningful again

- [ ] 4.1 Run `timeout 5m task api:check` after the tags and confirm every module in the loop diffs against its own tag and no module is skipped: the output carries seven `diffing HEAD against <module>/v1.0.0` lines and zero `no tag yet, skipped (no baseline to diff against)` lines (five such lines before the freeze). Record the observed output in the freeze section.
  - evidence: `Taskfile.yml:53-69`; design D1, D4.
- [ ] 4.2 Positive control: with a scratch edit removing one method from a frozen port (for example `types.Workspace.Close`), `timeout 5m task api:check` MUST exit nonzero naming that symbol, then reverting the scratch edit MUST restore a green run. Verify: the failing output names the port and the method, and the reverted run reports every module compatible; a green run without the failing control is not evidence the gate works.
  - evidence: `tools/apicheck/main.go:84-101`, `:170-201`; `tools/apicheck/main_test.go:152`; design D1, Risks.

## 5. Artifact and repository gates

- [ ] 5.1 Validate the change artifact: `timeout 60s openspec validate m2-api-freeze --strict --no-interactive` exits 0, with `.openspec.yaml` carrying `skip_specs: true` and no `specs/` directory present.
  - evidence: `openspec/changes/m2-api-freeze/.openspec.yaml`; design Non-Goals.
- [ ] 5.2 Run the repository gates after the tool edit: `timeout 10m task lint && timeout 30m task test` pass, and `timeout 5m task spec:gate GATE=M2` still reports `missing_in_scope=0`. Record the observed output; a gate that has not been run is not evidence.
  - evidence: `Taskfile.yml:29-50`, `:71-88`; `openspec/changes/m2/tasks.md:50-51`.
- [ ] 5.3 Run `timeout 2m task spec:types` and confirm no drift: this change adds no normative declaration, edits no main spec and changes no scenario, so `timeout 60s git diff --exit-code docs/design/types.md` is clean.
  - evidence: `Taskfile.yml:9-12`; design Non-Goals.
- [ ] 5.4 Confirm the record and the tree agree at the end: `timeout 5m task api:check` output, the tag list from 3.2 and 3.3, and the freeze section's two tables all name the same modules, the same baselines and the same tags. Verify: a diff of the module names in the `api:check` output against the table's module column is empty.
  - evidence: `Taskfile.yml:53-69`; design D2, D5.
