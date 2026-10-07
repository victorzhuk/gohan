# Tasks: m2-sandbox-governance

A task is done when its listed scenario IDs have a passing subtest named exactly the ID on the public path, its `verify:` command is green, and `task lint` and `task test` are green for what it touched. The fake `Sandbox` and the fake `CredentialSource` live in `_test.go` files and are never exported; no test reaches a network or a container.

## 1. Port and policy types in `core/types`

- [ ] 1.1 Declare the port and its value types in `core/types/sandbox.go`, exactly as the capability Contract block states: `Sandbox.Open(ctx, WorkspaceRef, SandboxPolicy) (Workspace, error)`; `Workspace.Exec`, `Workspace.Put`, `Workspace.Get`, `Workspace.Snapshot`, `Workspace.Close`; `WorkspaceRef`; `Lifecycle` with `PerCall` and `PerSession`; `SandboxPolicy{Lifecycle, Egress, CPU, Memory, Disk, MaxExecTime, Secrets}`; `SecretRef{Name, Env}`; `ExecRequest{Argv, Stdin, Cwd, Timeout}`; `ExecResult{ExitCode, Stdout, Stderr, Truncated, Seconds}`. Verify `go build ./...` and `go test -timeout 2m ./core/types/...`.
  - evidence: `core/types/sandbox.go` (`Sandbox`, `Workspace`, `WorkspaceRef`, `Lifecycle`, `SandboxPolicy`, `SecretRef`, `ExecRequest`, `ExecResult`); `core/types/sandbox_test.go` (`TestSandboxPolicyZeroValues`)
- [ ] 1.2 Add the sandbox aliases to `core/aliases.go` and replace the local `WorkspaceRef` declaration in `core/stores/checkpoint.go` with the alias, keeping `Checkpoint.Workspace` on the one shared type; verify `go build ./...` and `go test -timeout 2m ./core/stores/... -run TestCheckpoint`.
  - evidence: `core/aliases.go` (`Sandbox`, `Workspace`, `WorkspaceRef`); `core/stores/checkpoint.go` (`WorkspaceRef` alias); `docs/design/types.md` (regenerated, no duplicate definition)
- [ ] 1.3 Confirm the settled method set with the two adapter lanes before `m2-api-freeze` runs: `list_files` has no `Workspace` method to call, so if an adapter needs a distinct listing shape it returns `List(ctx, path) ([]string, error)` here, before the freeze, not as a post-freeze shim. Verify `go test -timeout 2m ./core/types/...` and a recorded decision in this change's `design.md` D1.5.
  - evidence: `openspec/changes/m2-sandbox-governance/design.md` (D1.5 outcome); `core/types/sandbox.go` (`Workspace` method set)

## 2. Governed tool set and effect derivation

- [ ] 2.1 Add `std/sandbox.New(sb Sandbox, p SandboxPolicy, opts ...Option) ToolSet` exposing `run_code`, `write_file`, `read_file` and `list_files` as one governed registration unit, built on the shipped `types.Tool` port with `OriginModel` model-written code and the existing permission gate and `StageToolResult` guard wiring; verify `go test -timeout 2m ./std/sandbox/... -run TestNew`.
  - evidence: `std/sandbox/sandbox.go` (`New`, `ToolSet`); `std/sandbox/sandbox_test.go` (`TestNewToolNames`)
- [ ] 2.2 Derive the effect from the policy and lifecycle and make `WithEffect` tighten-only, refusing a weaker effect at construction: `PerCall` with empty `Egress.Allow` → `ReadOnly`, `PerSession` with empty `Egress.Allow` → `Idempotent`, any `Egress.Allow` entry → `SideEffect`. Verify `go test -timeout 2m ./std/sandbox/... -run TestDeriveEffect`.
  - evidence: `std/sandbox/effect.go` (`deriveEffect`); `std/sandbox/effect_test.go` (`TestDeriveEffect`, `TestWithEffectTightenOnly`)
- [ ] 2.3 Pass the policy's `types.EgressPolicy` to `Build` as the single egress vocabulary and surface an adapter-reported denial as the shared `types.EgressDenied` carrying host and reason only, with no second allowlist type in `std/sandbox`. Verify `go test -timeout 2m ./std/sandbox/... -run TestEgressPolicyPassedThrough`.
  - evidence: `std/sandbox/sandbox.go` (`New` option wiring); `std/sandbox/egress_test.go` (`TestEgressPolicyPassedThrough`)
- [ ] 2.4 Add the `sandbox.effect-derived` subtest, named exactly its ID, in `std/sandbox/scenarios_test.go`: a `PerSession` tool set with empty `Egress` registers `run_code` as `Idempotent`, and adding one egress host makes it `SideEffect`. Verify `go test -timeout 2m ./std/sandbox/... -run TestSandboxEffectDerived`.
  - evidence: `std/sandbox/scenarios_test.go` (`TestSandboxEffectDerived/sandbox.effect-derived`)

## 3. Secrets

- [ ] 3.1 Resolve each `SandboxPolicy.Secrets` entry through the harness `CredentialSource` for the run originator `Principal` at `Open` and inject the value as its `Env` inside the workspace; inject nothing when no source is wired and never fall back to the ctx `Credential`. Verify `go test -timeout 2m ./std/sandbox/... -run TestResolveSecrets`.
  - evidence: `std/sandbox/secrets.go` (`resolveSecrets`); `std/sandbox/secrets_test.go` (`TestResolveSecrets`, `TestResolveSecretsWithoutSource`)
- [ ] 3.2 Scan `run_code` and `read_file` output for every injected value before the result is returned, replace a hit with the redaction marker, and increment `gohan.sandbox.secret_leak`; assert the value reaches no tool argument, result, history message, journal entry, audit record, span or log line. Verify `go test -timeout 2m ./std/sandbox/... -run TestSecretRedaction`.
  - evidence: `std/sandbox/secrets.go` (`redact`); `std/sandbox/secrets_test.go` (`TestSecretRedaction`)
- [ ] 3.3 Add the `sandbox.secret-injected` and `sandbox.secret-leak-redacted` subtests, named exactly their IDs, in `std/sandbox/scenarios_test.go`: the first asserts the `PGPASSWORD` env var inside the fake workspace plus one `CredentialSource` call for the originator and no trace in history, journal or spans; the second asserts the redaction marker and one `gohan.sandbox.secret_leak` increment. Verify `go test -timeout 2m ./std/sandbox/... -run TestSandboxSecrets`.
  - evidence: `std/sandbox/scenarios_test.go` (`TestSandboxSecrets/sandbox.secret-injected`, `TestSandboxSecrets/sandbox.secret-leak-redacted`)
- [ ] 3.4 Add the `identity.credential-not-in-sandbox` subtest, named exactly its ID, in `std/sandbox/credential_scenarios_test.go`: a tool set executing a command with a `Credential` in the run `ctx` puts neither the token nor a value derived from it in the process environment or in mounted files. Verify `go test -timeout 2m ./std/sandbox/... -run TestCredentialNotInSandbox`.
  - evidence: `std/sandbox/credential_scenarios_test.go` (`TestCredentialNotInSandbox/identity.credential-not-in-sandbox`)

## 4. Lifecycle, outputs and limits

- [ ] 4.1 Run the `PerCall` lifecycle as `Open` → `Exec` → `Close` inside one tool call with a deferred once-only close so a panicking call cannot leak a workspace or close twice; leave `PerSession` accepted-but-unimplemented for `m2-sandbox-lifecycle-resume`. Verify `go test -timeout 2m ./std/sandbox/... -run TestPerCallLifecycle`.
  - evidence: `std/sandbox/percall.go` (`withWorkspace`); `std/sandbox/percall_test.go` (`TestPerCallLifecycle`)
- [ ] 4.2 Add the `sandbox.per-call-closes` subtest, named exactly its ID, in `std/sandbox/scenarios_test.go`: a normal call and a panicking call each record exactly one `Close`. Verify `go test -timeout 2m ./std/sandbox/... -run TestSandboxPerCallCloses`.
  - evidence: `std/sandbox/scenarios_test.go` (`TestSandboxPerCallCloses/sandbox.per-call-closes`)
- [ ] 4.3 Cap `Stdout` and `Stderr` by `ToolSpec.MaxOutput`, spill overflow and any model-returned file into the shipped `OutputStore` through `Put(ctx, RunInfo, []Block)`, and return a head excerpt plus `types.File` blocks whose `Blob.Ref` reads back through the harness `read_output`; use `ExecResult.Truncated` as the cap signal. Verify `go test -timeout 2m ./std/sandbox/... -run TestOutputSpill`.
  - evidence: `std/sandbox/output.go` (`spill`); `std/sandbox/output_test.go` (`TestOutputSpill`, `TestReturnedFileToStore`)
- [ ] 4.4 Add the `sandbox.output-to-store` subtest, named exactly its ID, in `std/sandbox/scenarios_test.go`: stdout past `MaxOutput` and a returned file land in `MemoryOutputs` and the result carries a head excerpt plus `Ref`. Verify `go test -timeout 2m ./std/sandbox/... -run TestSandboxOutputToStore`.
  - evidence: `std/sandbox/scenarios_test.go` (`TestSandboxOutputToStore/sandbox.output-to-store`)
- [ ] 4.5 Charge cumulative `ExecResult.Seconds` per run tree in the harness limit chain against `RunLimits.MaxSandboxSeconds`, report the spend in `Usage.SandboxSeconds`, and return `*LimitExceededError{Limit: "MaxSandboxSeconds"}` when the budget is exceeded; edit only the sandbox charge and its error in `core/chains/limits.go`, after `m2-eino-runtime` has landed its limit work. Verify `go test -timeout 2m ./core/... -run TestLimits` and `go test -timeout 2m ./std/sandbox/... -run TestSandboxSecondsBudget`.
  - evidence: `core/chains/limits.go` (sandbox-seconds charge); `std/sandbox/scenarios_test.go` (`TestSandboxSecondsBudget/sandbox.seconds-budget`)

## 5. Host runner

- [ ] 5.1 Keep `tool/exec` as the no-isolation runner for fixed-argv commands and keep the existing `Build` warning when it is registered without `AllowHostExec()`; change no behaviour of the package, since sandbox execution does not replace it. Verify `go test -timeout 2m ./std/tool/exec/... -run TestHostExecWarning`.
  - evidence: `std/tool/exec/exec.go` (`New` warning, unchanged); `std/tool/exec/host_exec_scenarios_test.go` (`TestHostExecWarning/sandbox.host-exec-warning`)

## 6. Package gates

- [ ] 6.1 Regenerate `docs/design/types.md` after the type addition and confirm the duplicate-definition section shows one `WorkspaceRef`; verify `task spec:types` leaves `docs/design/types.md` with no duplicate definition.
  - evidence: `docs/design/types.md` (`Workspace`, `WorkspaceRef`, duplicate definitions: none)
- [ ] 6.2 Run the repository wrappers and resolve failures; verify `task lint` and `task test`.
  - evidence: driver-run `task lint` 0 issues; `task test` green across root and modules
- [ ] 6.3 Validate the change package; verify `openspec validate m2-sandbox-governance --strict --no-interactive` → `is valid`, and `task spec:gate GATE=M2` reports the seven owned sandbox ids and `identity.credential-not-in-sandbox` as covered, with the seven adapter and lifecycle ids still open under their own owners.
  - evidence: driver-run `openspec validate m2-sandbox-governance --strict --no-interactive`; `task spec:gate GATE=M2`