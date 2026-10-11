# Proposal: harden-workspace-security-gates

## Why

The CI `security` job runs `govulncheck` inside the root module only (`.github/workflows/ci.yml:65`: `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...`). The workspace has seven modules (`go.work:1-10`): the root module, five adapters (`adapter/anthropic`, `adapter/eino`, `adapter/openai`, `adapter/otel`, `adapter/postgres`) and `examples`. A root-only invocation never scans those six, so a vulnerability in an adapter or in `examples` cannot fail the gate.

A scan of `adapter/postgres` exits 3 with GO-2026-6629 for `golang.org/x/text` v0.40.0, fixed in v0.41.0. The version is pinned `// indirect` in `adapter/postgres/go.mod:66` and `examples/go.mod:67`. Scans of the root module, `adapter/eino`, `adapter/otel`, `adapter/openai`, `adapter/anthropic` and `examples` reported no vulnerabilities. The advisory panic affects `Nickname`; `pgx` calls `OpaqueString`, and no exploitable `pgx` path is established.

The root-only command and the workspace in `go.work` are two hand-maintained lists that already disagree. The repair must not add a third one in Go source.

## What Changes

- Add one `security:scan` task to `Taskfile.yml`. The task asks the toolchain for the workspace main modules with `go list -m` and runs the pinned `govulncheck` once per module directory, so the scan inventory is the workspace itself and no parser of `go.work` exists.
- The task fails closed: a non-zero scanner exit, a vulnerability report, or a failed `go list -m` all exit non-zero. It prints one line per module so the scanned set is visible in CI output.
- Wire CI to the task: the `security` job adds the `arduino/setup-task@v3.0.0` step and runs `task security:scan`, replacing the root-only command (`.github/workflows/ci.yml:58-65`).
- Bump `golang.org/x/text` to v0.41.0 in the two modules that pin it (`adapter/postgres`, `examples`) and refresh only those `go.sum` entries. No other dependency moves and no dependency-wide update runs.
- Verify read-only after the bump: `adapter/postgres` no longer reports GO-2026-6629, the six other modules stay clean, and the task scans all seven workspace modules.
- Add no capability, spec delta, main-spec requirement, registry entry, ADR or generated-index change; the package declares `skip_specs: true`. Scanning behaviour is build-time dependency hygiene, not system behaviour.
- Preserve the spec-gate default `M0.5` and the M1 explicit floor (`Taskfile.yml:28-31`, `spec:gate`). No deployment surface exists.

## Capabilities

### New Capabilities

- (none)

### Modified Capabilities

- (none)

## Impact

Edited `Taskfile.yml` (one `security:scan` task), `.github/workflows/ci.yml` (the `security` job), `adapter/postgres/go.mod`/`go.sum`, `examples/go.mod`/`go.sum` (one indirect dependency each), and `docs/design/architecture.md` (the tooling line at `docs/design/architecture.md:168`). No new Go source file, no new test package, no public API, no `openspec/specs/` edit, and no release.