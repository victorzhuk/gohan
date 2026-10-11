# Design: harden-workspace-security-gates

## Context

See `proposal.md` — Why. Read against the tree on 2026-10-07:

- The workspace modules come from the `go.work` `use` block (`go.work:1-10`): `.`, `./adapter/anthropic`, `./adapter/eino`, `./adapter/openai`, `./adapter/otel`, `./adapter/postgres`, `./examples`. Each module carries its own `go.mod`; the root module requires nothing (`go.mod:1-3`).
- `go list -m` run from the repository root resolves the workspace and prints exactly those seven main modules; `go list -m -f '{{.Dir}}'` prints their absolute directories. The toolchain already reads `go.work`, so the inventory needs no second parser.
- The only scan today is the `security` job's single step (`.github/workflows/ci.yml:58-65`), run in the root module with a versioned `go run`.
- `golang.org/x/text` v0.40.0 is pinned `// indirect` in `adapter/postgres/go.mod:66` and `examples/go.mod:67`, with sums at `adapter/postgres/go.sum:149-150` and `examples/go.sum:147-148`, so the workspace resolves v0.40.0. The advisory fixed version is v0.41.0.
- The Taskfile already owns multi-step shell orchestration and already uses an unquoted `for` loop over a fixed list (`Taskfile.yml:120-126`, `examples:run`) and per-module `go -C` commands (`Taskfile.yml:75-84`, `test`). Orchestration of environment-touching multi-step commands is the Taskfile's existing job.

## Goals / Non-Goals

**Goals:** one task that scans the exact workspace inventory; no new Go program, parser or helper; CI runs that task; the flagged advisory is remediated by the minimal version bump; the scan evidence is verified read-only at implementation.

**Non-Goals:** no `tools/security_scan.go`, no `tools/security_scan_test.go`, and no Go helper or `go.work` parser of any kind; no dependency-wide update; no deployment (none exists); no new capability, spec, registry entry, ADR or generated-index change; no test that greps `Taskfile.yml` or `.github/workflows/ci.yml` and no static wiring test; no change to the spec-gate default `M0.5` or the M1 floor.

## Decisions

### D1 — The scan is ordinary Taskfile orchestration, not a Go program

`Taskfile.yml` gains one task `security:scan` whose `cmds` is a short `set -euo pipefail` block: one `go list -m -f` call for the workspace inventory, then one `go -C <dir> run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...` per module, echoing the module directory first. The `-f` argument must reach the shell as the literal text `{{.Dir}}`, so the task declares it through the escaped template `{{"{{.Dir}}"}}`; D3a records the escaping rule and the observed behaviour. Rationale: the work is a shell loop over a toolchain query, which is exactly what the Taskfile already does for `examples:run` and `api:check`; a Go program would add a build target, a package, a test file and a second parser for a file the toolchain already reads. Alternative rejected: the earlier `tools/security_scan.go` design (a `moduleInventory` parser plus a `run` seam). It was rejected because it turned six lines of shell into new repository code that then needs its own test, while the behaviour it tested is `go`'s.

### D2 — The inventory comes from `go list -m`, so no module list is hand-maintained

The task derives its module set from `go list -m` at the workspace root, asking for the module directory through the `-f` format whose shell text is `{{.Dir}}`. A module added to `go.work` is therefore scanned with no task edit, and a module removed disappears with no task edit. Rationale: the failure being repaired is a hand-maintained list (the root-only command) diverging from the workspace; asking the toolchain removes the second list instead of adding a maintained one. Alternatives rejected: copying the seven directories as a shell literal (a new hand-maintained list); parsing `go.work` with a Go or awk helper (a new parser that must track the file syntax).

### D3 — Fail closed, scanner pinned at v1.8.0

`set -euo pipefail` makes the task exit non-zero on the first module that reports a vulnerability (govulncheck exits 3) or on any scanner error. The remaining modules are not scanned after a failure; the first failure is the gate result.

The inventory fails closed on its own, in a checked command before the loop. The task holds the format string in a `vars` entry so the escaped template and the shell text are separate:

```yaml
vars:
  MODFMT: '{{"{{.Dir}}"}}'
cmds:
  - |
    set -euo pipefail
    inventory="$(go list -m -f '{{.MODFMT}}')"
    [ -n "$inventory" ] || { echo "security:scan: empty module inventory" >&2; exit 1; }
    for dir in $inventory; do
      echo "security:scan: $dir"
      go -C "$dir" run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./... || exit $?
    done
```

`pipefail` governs pipelines. It does not govern a command substitution used as a `for` list, so a failing `go list -m` inside the substitution does not stop the loop. The separate assignment is what propagates that failure, and the explicit non-empty test refuses an empty inventory. Without both, a failing or empty inventory yields a zero exit and no scanned module, which is a green gate over zero modules. The scanner stays at v1.8.0, the version `.github/workflows/ci.yml:65` pins today, so the pin is unchanged and no module gains a dependency. Each module prints its directory before its scan, so the CI log names every module that was scanned. The `|| exit $?` guard makes the scanner exit status the task exit status explicitly, so the loop does not depend on `set -e` for a command inside a loop body.

### D3a — Two different templates: the shell `go list -f` string and the Taskfile expression that emits it

The `-f` argument carries two templates that must not be confused.

1. The **shell template** is the text the shell passes to `go list -m -f`. It must be exactly `{{.Dir}}`. It is the `go` format string and no other program reads it.
2. The **Taskfile expression** is the text Task writes into the `cmds` block. Task templates every `cmds` entry, so a bare `{{.Dir}}` in the Taskfile is a Task variable lookup for a variable named `Dir`. The repository declares no such `Dir` variable, so Task renders it as an empty string and the shell receives `go list -m -f ''`.

The escaping rule: write the shell template inside a Go-template string literal. `{{"{{.Dir}}"}}` makes Task emit the literal text `{{.Dir}}`. This is the same mechanism the repository already uses for ordinary Task variables such as `'{{.GATE | default "M0.5"}}'` (`Taskfile.yml:29-31`) and `'{{.CLI_ARGS}}'` (`Taskfile.yml:26`); the string literal keeps the braces that `go` needs while the surrounding Task expression stays valid.

Task 3.54.0 was measured with a throwaway Taskfile, not by reading source text. Observed results:

- A `cmds` block written with the bare `{{.Dir}}` template expands to `go list -m -f ''`. `go` then prints module paths instead of directories, so the first line is the module path `github.com/victorzhuk/gohan`, not a directory. `go -C` on that path fails, so the task breaks at the first module.
- The same block written with `{{"{{.Dir}}"}}` expands to `go list -m -f '{{.Dir}}'` and prints seven absolute workspace directories. A `vars` entry holding `{{"{{.Dir}}"}}`, referenced as `{{.MODFMT}}`, emits the same shell text. Both forms were run three times each with identical results.
- A failing `go list -m` (a stub that exits 1 for `list -m`) stops the task with a non-zero status and scans no module, in both the bare and the escaped form. The propagation comes from the standalone assignment, not from `pipefail`.
- An inventory that prints nothing but exits 0 is refused by the `[ -n "$inventory" ]` test with the message `security:scan: empty module inventory` and a non-zero status.
- A scanner that exits 3 stops the task with status 3 at the first module and does not reach the second.

The bare form is rejected. `go -C <module path>` is not a directory, so the loop fails on the first module and the gate never scans anything. The escaped form is the only one that yields directories.

A third form is also rejected: the shell template does not need to come from a variable at all. Writing the escaped expression inline, `go list -m -f '{{"{{.Dir}}"}}'`, produces the same shell text. The `vars` form is chosen because it names the format string once, so a reviewer reads `MODFMT` instead of two levels of nested braces, and because it gives one place to change the queried field.

One observed behaviour of the escaping must be recorded because it constrains verification. When the inventory is captured by assignment and counted with `printf '%s' "$inventory" | wc -l`, the count is 6 for a seven-module workspace, in the bare form and the escaped form alike. The trailing newline is not part of the variable, so the count is one less than the number of modules. Verification therefore compares the directory lines the task prints against `go list -m -f '{{.Dir}}'` and does not assert a captured-variable line count.

### D4 — Minimal fixed-version bump only where pinned

`golang.org/x/text` moves to v0.41.0 in `adapter/postgres/go.mod:66` and `examples/go.mod:67` — the two modules whose `go.mod` pins v0.40.0 — with the matching `go.sum` lines updated and nothing else. `adapter/postgres` is the module the advisory flagged; `examples` pins the same version, so the same minimal edit removes v0.40.0 from the workspace. No `go get -u`, no other dependency touched. The `examples` scan result stays reported as clean, not as vulnerable.

### D5 — CI scans through the task

The `security` job keeps `actions/checkout@v7.0.1` and `actions/setup-go@v7.0.0`, adds the `arduino/setup-task@v3.0.0` step (version 3.54.0, as `spec`, `test`, `api-check` and `bench` use) and replaces its `run` step with `task security:scan` (`.github/workflows/ci.yml:58-65`). Rationale: the CI command becomes the command a developer runs, and the inventory lives in one place.

### D6 — Verification is the observed command, not a test

There is no new Go code, so there is no new Go test. The verification is the task itself, run read-only:

- `task --dry security:scan` shows the expanded block with `go list -m -f '{{.Dir}}'`, so the reviewed command is the command that runs and the escaping is visible.
- `task security:scan` prints one line per module and the scanned set equals the seven `go.work` modules.
- After the bump, `adapter/postgres` no longer reports GO-2026-6629 and the other six report no vulnerabilities; the task exits 0.

No test greps `Taskfile.yml` or `.github/workflows/ci.yml`. Rationale: a grep test proves text, not behaviour; the gate's behaviour is that `govulncheck` runs in each module directory and its exit status reaches the task. The escaping is checked the same way: a throwaway Taskfile that runs both forms and reports what each shell receives, never a source-text match.

## Risks / Trade-offs

- [Risk] An unquoted `for dir in $(go list -m ...)` splits on whitespace, so a workspace path containing a space would break. → Accepted: the repository paths are fixed and contain no spaces, and `examples:run` (`Taskfile.yml:125-128`) already uses the same unquoted loop form.
- [Risk] `go run …@v1.8.0` per module is slower than one root run and downloads the scanner once. → Mitigation: the pin is unchanged and the build is cached across modules; the scan stays in one CI job.
- [Risk] A module added to `go.work` changes the scanned set silently. → Accepted and intended: the task reads the workspace, so a new module is scanned from the first run. The CI log names every scanned module, and the implementation review compares the logged set against `go.work`.
- [Risk] Bumping `examples` although its scan was clean looks unnecessary. → Mitigation: it pins the same advisory version; the edit is one indirect line and its sums, and the scan result stays reported as clean.
- [Risk] Fail-closed stops at the first failing module, so a later vulnerable module is not scanned in the same run. → Accepted: the gate has already failed; the first failure is the actionable result and the full set is scanned again on the next run.
- [Risk] The bare `{{.Dir}}` template is not an error at author time. Task renders it to an empty string without a warning, so a broken scan task is committed silently. → Mitigation: task 2.1 records the escaped form and task 2.3 proves the emitted shell text by running a throwaway Taskfile. The `task --dry` output shows the empty `-f` argument, so the failure is visible in one command.
- [Risk] A nested escaped template is hard to read and easy to break by a later edit that removes one brace pair. → Mitigation: the `vars` entry names the format string once, so the `cmds` block holds one ordinary Task reference and the nesting appears on one line only.

## Migration Plan

Additive and non-breaking. Land the task, the CI job step, the two pinned-version bumps with their sums, and the `docs/design/architecture.md` note. Rollback is restoring the root-only CI command and v0.40.0. No data migration, no release, no spec.