# Sandboxed execution

Capability: `sandbox` · Spec v1.2 (ADR-0129) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

Lets a model write and run code under the same governance as every other tool: journaled, guarded, budgeted, resumable. gohan owns the port, the policy and the model-facing tool set; isolation (microVM, gVisor, container) is an adapter. Out of scope: fixed-argv host commands (`tool/exec` in `tools`), the isolation technology itself.


## Contract

```go
type Sandbox interface {
	Open(ctx context.Context, ref WorkspaceRef, p SandboxPolicy) (Workspace, error)
}

type Workspace interface {
	Exec(ctx context.Context, req ExecRequest) (ExecResult, error)
	Put(ctx context.Context, path string, r io.Reader) error
	Get(ctx context.Context, path string) (io.ReadCloser, error)
	Snapshot(ctx context.Context) (WorkspaceRef, error)
	Close(ctx context.Context) error
}

type WorkspaceRef string

type Lifecycle int

const (
	PerCall Lifecycle = iota
	PerSession
)

type SandboxPolicy struct {
	Lifecycle   Lifecycle
	Egress      EgressPolicy
	CPU         float64
	Memory      int64
	Disk        int64
	MaxExecTime time.Duration
	Secrets     []SecretRef
}

type SecretRef struct {
	Name string
	Env  string
}

type ExecRequest struct {
	Argv    []string
	Stdin   io.Reader
	Cwd     string
	Timeout time.Duration
}

type ExecResult struct {
	ExitCode  int
	Stdout    []byte
	Stderr    []byte
	Truncated bool
	Seconds   float64
}
```

`std/sandbox` ships `New(sb Sandbox, p SandboxPolicy, opts ...Option) ToolSet` exposing `run_code`, `write_file`, `read_file` (and `list_files`) as one governed tool set. Rules:

1. **Effect from policy.** `PerCall` + empty `Egress.Allow` → `ReadOnly`; `PerSession` + empty `Egress.Allow` → `Idempotent`; any `Egress.Allow` → `SideEffect`. `Egress` is the `EgressPolicy` of `tools`, enforced by the adapter at the network layer with the same private-range and hop rules. `WithEffect` may only tighten. Empty `Egress` means deny-all, including DNS.
2. **Secrets.** `Secrets` are resolved by the harness through `CredentialSource` for the originator at `Open` and injected as environment inside the workspace. Secret values never appear in tool args, results, history, journal or spans; `read_file`/`run_code` output is scanned for injected values and redacted (`gohan.sandbox.secret_leak` increments).
3. **Outputs.** `Stdout`/`Stderr` are capped by `ToolSpec.MaxOutput`; overflow and files the model asks to return go to `OutputStore` and come back as `File` blocks with `Ref`.
4. **Lifecycle.** `PerCall`: `Open` → `Exec` → `Close` inside one tool call. `PerSession`: one workspace per session, kept open across turns, `WorkspaceRef` stored in session metadata; on any suspension and at the end of a detached run the harness calls `Snapshot` and stores the ref in `Checkpoint.Workspace`; `Resume` opens from that ref. `Close` is called by the reaper when the session's lease expires.
5. **Replay.** Exec results are journaled like any tool result; `Replay` replays them without re-executing and restores the workspace from the checkpoint ref, so a run resumed on another pod sees the same files.
6. **Limits.** `RunLimits.MaxSandboxSeconds` bounds cumulative `ExecResult.Seconds` per tree; `Usage.SandboxSeconds` reports it; exceeding it is `*LimitExceededError`. `MaxExecTime` kills the process group inside the workspace; a kill on a `SideEffect` policy is `Outcome: Unknown`, otherwise `Failed`.
7. **Guards.** Model-written code is `OriginModel`; `run_code` args pass the permission gate (`Ask` recommended for `SideEffect` policies); results pass `StageToolResult`.
8. **Host runner.** `tool/exec` stays the no-isolation runner for fixed-argv commands. `Build` warns when it is registered without `AllowHostExec()`.

Adapters: `adapter/docker` (dev, reference), `adapter/e2b`; a Kubernetes agent-sandbox adapter later. Conformance: `sandboxtest` exercises every rule against an adapter with a deny-all egress probe.

Metrics: `gohan.sandbox.seconds` by lifecycle, `gohan.sandbox.opens`, `gohan.sandbox.cold_start` histogram, `gohan.sandbox.secret_leak`, `gohan.sandbox.egress_denied`.


## Requirements

### Requirement: Policy and effect

#### Scenario: effect derived
ID: `sandbox.effect-derived`
- WHEN a tool set is built with `PerSession` and empty `Egress`
- THEN `run_code` is `Idempotent`; adding one egress host makes it `SideEffect`

#### Scenario: deny-all egress
ID: `sandbox.egress-denied`
- WHEN code inside a workspace with empty `Egress` opens a TCP connection or resolves a name
- THEN the call fails inside the sandbox and `gohan.sandbox.egress_denied` increments

#### Scenario: allowlisted egress
ID: `sandbox.egress-allowlisted`
- WHEN `Egress` lists `api.example.com` and code connects to it and to another host
- THEN the first succeeds and the second is denied

### Requirement: Secrets

#### Scenario: secret injected not visible
ID: `sandbox.secret-injected`
- WHEN the policy names `SecretRef{Name: "warehouse", Env: "PGPASSWORD"}`
- THEN the env var is set inside the workspace and the value appears in no history message, journal entry or span

#### Scenario: leak redacted
ID: `sandbox.secret-leak-redacted`
- WHEN `run_code` prints the injected value
- THEN the tool result carries a redaction marker and `gohan.sandbox.secret_leak` increments

### Requirement: Lifecycle and resume

#### Scenario: per-session persistence
ID: `sandbox.per-session-persists`
- WHEN turn 1 writes `data.csv` and turn 2 runs code reading it
- THEN the read succeeds without re-uploading

#### Scenario: snapshot on suspension
ID: `sandbox.snapshot-on-suspend`
- WHEN a run suspends for `HumanApproval` after writing files
- THEN `Checkpoint.Workspace` holds a `WorkspaceRef` produced by `Snapshot`

#### Scenario: resume on another pod
ID: `sandbox.resume-restores`
- WHEN the run is resumed on another pod
- THEN the workspace opened from `Checkpoint.Workspace` contains the files written before suspension

#### Scenario: replay does not re-execute
ID: `sandbox.replay-no-reexec`
- WHEN `Replay` resumes a run that executed code before suspending
- THEN the journaled `ExecResult` is used and the adapter's `Exec` is not called again

#### Scenario: per-call closes
ID: `sandbox.per-call-closes`
- WHEN a `PerCall` tool call finishes or panics
- THEN `Close` has been called exactly once

### Requirement: Limits and outputs

#### Scenario: sandbox seconds budget
ID: `sandbox.seconds-budget`
- WHEN cumulative `ExecResult.Seconds` exceeds `MaxSandboxSeconds`
- THEN the next `run_code` returns `*LimitExceededError{Limit: "MaxSandboxSeconds"}`

#### Scenario: exec timeout
ID: `sandbox.exec-timeout`
- WHEN code exceeds `MaxExecTime`
- THEN the process group is killed; the outcome is `Unknown` under a `SideEffect` policy and `Failed` otherwise

#### Scenario: large output to store
ID: `sandbox.output-to-store`
- WHEN stdout exceeds `MaxOutput` or the model returns a produced file
- THEN the result carries a head excerpt plus `Ref` and the content is in `OutputStore`

### Requirement: Host runner

#### Scenario: egress shares the policy type
ID: `sandbox.egress-shares-policy-type`
- WHEN a sandbox tool set is built with `Egress: EgressPolicy{Allow: ["api.example.com"]}`
- THEN `Explain` shows the same policy shape as a harness tool's egress and code inside the workspace cannot reach a private range

#### Scenario: host exec warning
ID: `sandbox.host-exec-warning`
- WHEN `tool/exec` is registered without `AllowHostExec()`
- THEN `Build` logs a warning naming the tool
