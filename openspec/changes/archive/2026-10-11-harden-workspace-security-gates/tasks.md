# Tasks

## 1. Authoring

- [x] 1.1 Record the CI gap and the module inventory: the root-only scan at `.github/workflows/ci.yml:65` and the seven workspace modules in `go.work:1-10`. Record the flagged advisory (GO-2026-6629, `golang.org/x/text` v0.40.0 → v0.41.0) and the clean result for the root module, the other four adapters and `examples`.
  - evidence: `proposal.md` Why; `design.md` Context.
- [x] 1.2 Record that this package declares `skip_specs: true` and changes no capability, main spec, registry entry, ADR or generated index, and that the spec-gate default `M0.5` and the M1 floor stay unchanged.
  - evidence: `.openspec.yaml` (`skip_specs: true`); `proposal.md` Capabilities; `design.md` Non-Goals.
- [x] 1.3 Verify the package against its task spec. Boundary note: the `openspec` CLI in this workspace is a stub that exits 64, so no `openspec validate` gate is run or claimed; `timeout 300 task spec` is the check, and this package declares `skip_specs: true`.

## 2. The scan task

- [x] 2.1 Add the `security:scan` task to `Taskfile.yml`: a `set -euo pipefail` block that takes the workspace inventory from `go list -m` at the repository root and runs `go -C <dir> run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./... || exit $?` once per module, echoing each module directory before its scan. Add a one-line `desc:`. Add no Go helper and no `go.work` parser.
  - Carry the `go list -f` format string in a `vars` entry holding the escaped template `{{"{{.Dir}}"}}`, referenced as `{{.MODFMT}}`. Do not write the bare `{{.Dir}}` template in a `cmds` block.
  - verify: `timeout 60 task --dry security:scan` prints the block with `go list -m -f '{{.Dir}}'` and an empty `-f` argument nowhere; `timeout 60 task --list` shows `security:scan` with its description; the block contains no module list literal and no `tools/security_scan.go`.
  - evidence: `design.md` D1, D2, D3, D3a; `Taskfile.yml:120-128` (`examples:run` loop form); `Taskfile.yml:29-31` (the repository's existing Task template expressions); `.github/workflows/ci.yml:65` (the pinned scanner version).
- [x] 2.2 Prove the escaping with a throwaway Taskfile before the task lands, and prove it by running Task, not by matching text in `Taskfile.yml`. Write three tasks in a scratch directory outside the repository: one whose `cmds` block uses the bare `{{.Dir}}` template, one that uses `{{"{{.Dir}}"}}` inline, and one that holds `{{"{{.Dir}}"}}` in a `vars` entry (`MODFMT`). Run all three against the real workspace with `task --dir <repo> -t <scratch Taskfile> <task>`, three runs per form, nine runs total, all captured to one log.
  - Recorded outcomes (captured 2026-10-11, task 3.54.0, go1.27.2, real workspace `github.com/victorzhuk/gohan`; scratch `/tmp/security-proof-k7f2`, `proof.log` 163 lines, removed with the scratch directory — content embedded below). All three forms ran three times with identical output (bare `proof.log:1-14`, `15-28`, `29-42`; escaped `43-56`, `57-70`, `71-84`; vars-held `85-98`, `99-112`, `113-126`). Captured lines, bare form run 1 (`proof.log:1-14`):
    ```
    task: [fmt-bare] set -euo pipefail
    echo "MODFMT=[]"
    go list -m -f ''

    MODFMT=[]
    github.com/victorzhuk/gohan
    github.com/victorzhuk/gohan/adapter/anthropic
    ...
    task_exit=0
    ```
  - Captured lines, escaped-inline form run 1 (`proof.log:43-56`):
    ```
    task: [fmt-escaped] set -euo pipefail
    echo "MODFMT=[{{.Dir}}]"
    go list -m -f '{{.Dir}}'

    MODFMT=[{{.Dir}}]
    /home/zhuk/Projects/own/gohan
    /home/zhuk/Projects/own/gohan/adapter/anthropic
    /home/zhuk/Projects/own/gohan/adapter/eino
    /home/zhuk/Projects/own/gohan/adapter/openai
    /home/zhuk/Projects/own/gohan/adapter/otel
    /home/zhuk/Projects/own/gohan/adapter/postgres
    /home/zhuk/Projects/own/gohan/examples
    task_exit=0
    ```
  - Captured lines, vars-held form run 1 (`proof.log:85-98`): identical body with `task: [fmt-vars-held]` and the same `MODFMT=[{{.Dir}}]` plus the seven absolute `/home/zhuk/Projects/own/gohan/...` directories, `task_exit=0`.
  - Nuance recorded without redesign: bare `{{.Dir}}` collapses to empty, so `go list -m -f ''` runs with an empty `-f`; because the plain `go list` output is still seven lines, the bare form exits 0 — the proof signal is the collapsed `MODFMT=[]` line, not the exit code. The escaped form (inline and vars-held) expands to `go list -m -f '{{.Dir}}'` and prints seven absolute workspace directories. Both throwaway Taskfiles and the scratch directory were deleted after capture.
  - evidence: captured runs above (bare/escaped/vars-held × 3, all identical); `design.md` D3a (Task 3.54.0 measured behaviour).
- [x] 2.3 Confirm the task fails closed on all three inputs: a failing inventory command, an empty inventory, and a non-zero scanner exit.
  - verify: build two stub `go` executables in a scratch directory and put that directory first on `PATH` for the task run. The first stub exits 1 for `list -m`. The second stub exits 0 for `list -m` and prints nothing. Run `timeout 60 task security:scan` under each stub and confirm a non-zero task status and that no module is scanned. Then run it with a stub that returns two module directories for `list -m` and exits 3 for the govulncheck call, and confirm the task stops at the first directory with status 3 and never reaches the second. Confirm the inventory is a standalone checked assignment before the loop and that `set -euo pipefail` is the first line of the block. Remove the stubs and the scratch directories afterwards.
  - Recorded outcomes (captured 2026-10-11, same scratch run, stub `go` at `/tmp/security-proof-k7f2/bin/go`, `STUB_MODE` selected per case; `proof.log:127-163`, removed with the scratch directory — content embedded below). All three fail-closed inputs end with the task binary exiting 201; the inner shell statuses are 1, 1 and 3. Captured lines, failing inventory (`proof.log:127-138`):
    ```
    task: [scan] set -euo pipefail
    inventory="$(go list -m -f '{{.Dir}}')"
    ...
    stub: inventory command failure
    task: Failed to run task "scan": exit status 1
    task_exit=201
    ```
    Refusal line: `stub: inventory command failure` (the stub's own stderr naming the failed stage; `set -e` stops the block before the loop). Scanned modules: 0.
  - Captured lines, empty inventory (`proof.log:139-150`):
    ```
    security:scan: empty module inventory
    task: Failed to run task "scan": exit status 1
    task_exit=201
    ```
    Refusal line is the block's own `security:scan: empty module inventory` stderr message. Scanned modules: 0.
  - Captured lines, scanner exit 3 (`proof.log:151-163`):
    ```
    security:scan: /tmp/security-proof-k7f2/modA
    stub: govulncheck failure in run golang.org/x/vuln/cmd/govulncheck@v1.8.0
    task: Failed to run task "scan": exit status 3
    task_exit=201
    ```
    The stub inventory lists two directories (`modA`, `modB`); the task prints `modA`, the stubbed scanner exits 3, `|| exit $?` re-raises it and the loop never reaches `modB`. Scanned: 1 of 2, terminate-first.
  - The captured block header shows `set -euo pipefail` as the first line and the inventory as a standalone checked assignment before the loop (`proof.log:127-131`). Space-in-path nuance, recorded without redesign: the `for dir in $inventory` loop word-splits unquoted `$inventory`, so a workspace module path containing a space would be scanned as two arguments (the stub pair `spa ce`/`mod one` in the scratch tree exercised the shape; the shipped task keeps the loop). The freeze keeps the loop shape as written; recorded as a known limitation, not a change request. Stub script and scratch directory deleted after capture.
  - evidence: captured trio above (exits 201/201/201, inner statuses 1/1/3, scanned counts 0/0/terminate-first); `design.md` D3, D3a.
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