# Tasks

## 1. Authoring

- [x] 1.1 Record the CI gap and the module inventory: the root-only scan at `.github/workflows/ci.yml:65` and the seven workspace modules in `go.work:1-10`. Record the flagged advisory (GO-2026-6629, `golang.org/x/text` v0.40.0 → v0.41.0) and the clean result for the root module, the other four adapters and `examples`.
  - evidence: `proposal.md` Why; `design.md` Context.
- [x] 1.2 Record that this package declares `skip_specs: true` and changes no capability, main spec, registry entry, ADR or generated index, and that the spec-gate default `M0.5` and the M1 floor stay unchanged.
  - evidence: `.openspec.yaml` (`skip_specs: true`); `proposal.md` Capabilities; `design.md` Non-Goals.
- [x] 1.3 Validate the package.
  - verify: `timeout 120 openspec validate harden-workspace-security-gates --strict --no-interactive` → `Change 'harden-workspace-security-gates' is valid`.

## 2. The scan task

- [x] 2.1 Add the `security:scan` task to `Taskfile.yml`: a `set -euo pipefail` block that takes the workspace inventory from `go list -m` at the repository root and runs `go -C <dir> run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./... || exit $?` once per module, echoing each module directory before its scan. Add a one-line `desc:`. Add no Go helper and no `go.work` parser.
  - Carry the `go list -f` format string in a `vars` entry holding the escaped template `{{"{{.Dir}}"}}`, referenced as `{{.MODFMT}}`. Do not write the bare `{{.Dir}}` template in a `cmds` block.
  - verify: `timeout 60 task --dry security:scan` prints the block with `go list -m -f '{{.Dir}}'` and an empty `-f` argument nowhere; `timeout 60 task --list` shows `security:scan` with its description; the block contains no module list literal and no `tools/security_scan.go`.
  - evidence: `design.md` D1, D2, D3, D3a; `Taskfile.yml:120-128` (`examples:run` loop form); `Taskfile.yml:29-31` (the repository's existing Task template expressions); `.github/workflows/ci.yml:65` (the pinned scanner version).
- [ ] 2.2 Prove the escaping with a throwaway Taskfile before the task lands, and prove it by running Task, not by matching text in `Taskfile.yml`. Write two tasks in a scratch directory outside the repository: one whose `cmds` block uses the bare `{{.Dir}}` template, and one that uses `{{"{{.Dir}}"}}`. Run both against the real workspace with `task --dir <repo> -t <scratch Taskfile> <task>`.
  - verify: the bare form expands to `go list -m -f ''` and its first output line is the module path `github.com/victorzhuk/gohan`, not a directory. The escaped form expands to `go list -m -f '{{.Dir}}'` and prints seven absolute workspace directories. Repeat both three times and confirm the results are identical each run. Confirm the same escaped shell text when the escaped expression is held in a `vars` entry. Delete both throwaway Taskfiles and the scratch directory afterwards as part of this smoke verification.
  - evidence: `design.md` D3a (Task 3.54.0 measured behaviour).
- [ ] 2.3 Confirm the task fails closed on all three inputs: a failing inventory command, an empty inventory, and a non-zero scanner exit.
  - verify: build two stub `go` executables in a scratch directory and put that directory first on `PATH` for the task run. The first stub exits 1 for `list -m`. The second stub exits 0 for `list -m` and prints nothing. Run `timeout 60 task security:scan` under each stub and confirm a non-zero task status and that no module is scanned. Then run it with a stub that returns two module directories for `list -m` and exits 3 for the govulncheck call, and confirm the task stops at the first directory with status 3 and never reaches the second. Confirm the inventory is a standalone checked assignment before the loop and that `set -euo pipefail` is the first line of the block. Remove the stubs and the scratch directories afterwards.
  - evidence: `design.md` D3, D3a.
- [x] 2.4 Confirm the scanned set equals the workspace set and not a smaller list, and do not assert a captured-variable line count.
  - verify: compare the directory lines `timeout 900 task security:scan` prints against `go list -m -f '{{.Dir}}'` run at the repository root. The two sets must be equal. Confirm that counting a captured inventory with `printf '%s' "$inventory" | wc -l` reports 6 for the seven-module workspace, so no task or check depends on that count.
  - evidence: `design.md` D2, D3a.
- [x] 2.5 Document the scan task and its inventory in `docs/design/architecture.md` (the tooling line at `docs/design/architecture.md:168`), naming `task security:scan` and that it scans every module from `go list -m`.
  - verify: the documented command matches the task name in `Taskfile.yml` and in `task --list`.
  - evidence: `design.md` D1, D5; `docs/design/architecture.md:168`.

## 3. Fixed-version bump

- [x] 3.1 Bump `golang.org/x/text` to v0.41.0 in `adapter/postgres/go.mod:66` and refresh only its `go.sum` entries (`adapter/postgres/go.sum:149-150`). Touch no other requirement.
  - verify: `timeout 120 go -C adapter/postgres list -m golang.org/x/text` prints v0.41.0; `timeout 300 go -C adapter/postgres build ./...`.
  - evidence: `adapter/postgres/go.mod:66`; `adapter/postgres/go.sum:149-150`; `design.md` D4.
- [x] 3.2 Bump `golang.org/x/text` to v0.41.0 in `examples/go.mod:67` and refresh only its `go.sum` entries (`examples/go.sum:147-148`). Touch no other requirement.
  - verify: `timeout 120 go -C examples list -m golang.org/x/text` prints v0.41.0; `timeout 300 go -C examples build ./...`.
  - evidence: `examples/go.mod:67`; `examples/go.sum:147-148`; `design.md` D4.

## 4. CI wiring

- [x] 4.1 In the `.github/workflows/ci.yml` `security` job, add the `arduino/setup-task@v3.0.0` step (version 3.54.0) and replace the root-only `run` step with `task security:scan`; keep `actions/checkout@v7.0.1` and `actions/setup-go@v7.0.0`.
  - verify: run the same command locally (`timeout 900 task security:scan`) and confirm the workflow parses when `actionlint .github/workflows/ci.yml` is available.
  - evidence: `.github/workflows/ci.yml:58-65`; `design.md` D5.

## 5. Scan verification and gates

- [x] 5.1 Run `task security:scan` read-only and confirm the outcomes match the design: `adapter/postgres` no longer reports GO-2026-6629 after the bump, and the root module, `adapter/otel`, `adapter/openai`, `adapter/anthropic`, `adapter/eino` and `examples` report no vulnerabilities. Do not report a module as vulnerable when its scan is clean.
  - verify: `timeout 900 task security:scan` exits 0, and each printed module directory starts with `/`, which proves the `go list -f` format string reached the shell intact. Compare the printed set with `go list -m -f '{{.Dir}}'`.
  - evidence: `design.md` D3, D3a, D4, D6; `proposal.md` Why.
- [x] 5.2 Confirm the spec-gate default and floor are unchanged and the repository gates still pass.
  - verify: `timeout 900 task test` green and `task spec:gate` still runs with its default `GATE` of `M0.5`; `timeout 600 task lint` reports no issues.
  - evidence: `Taskfile.yml:29-31` (`spec:gate` `GATE` default `M0.5`).

## Workflow follow-up

- Archive the change after review. The M0.5 default and the M1 floor remain unchanged.