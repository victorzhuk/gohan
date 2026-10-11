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

`core/types` MUST declare `Sandbox.Open(ctx, WorkspaceRef, SandboxPolicy) (Workspace, error)` and `Workspace.Exec`, `Put`, `Get`, `SetEnv`, `Snapshot` and `Close`; `WorkspaceRef` MUST be one shared type aliased where it is consumed. `SetEnv(ctx, map[string]string) error` MUST be the only injection path into a workspace.

Listing MUST NOT be a `Workspace` method. `WorkspaceLister` is an optional interface declaring `List(ctx, path) ([]WorkspaceEntry, error)`, and the harness MUST reach it by type assertion on the opened workspace. A workspace that does not implement it MUST fail `list_files` with `ErrWorkspaceListingRequired`; the harness MUST NOT return a partial listing as the answer.

The tool set MUST derive each tool's effect from the policy: `PerCall` with empty `Egress.Allow` is `ReadOnly`, `PerSession` with empty `Egress.Allow` is `Idempotent`, and any `Egress.Allow` entry is `SideEffect`. `WithEffect` MAY only tighten and a loosening option MUST be refused. `Egress` MUST be the `EgressPolicy` of `tools`, enforced by the adapter at the network layer with the same private-range and hop rules. Empty `Egress` MUST mean deny-all, including DNS.

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

Each `SecretRef` MUST be resolved at `Open` for the run originator through the optional `SecretResolver` port, `Resolve(ctx, Principal, name) (string, error)`, which is distinct from `CredentialSource` and takes the `SecretRef` name, and the resolved environment MUST be injected through `Workspace.SetEnv`. The run `Credential` MUST NOT substitute for a resolver and MUST NOT enter the workspace; a build without a resolver MUST inject nothing and the secret-named call MUST fail.

An injected value MUST NOT appear in tool arguments, results, history, journal, audit records, spans or logs. `read_file` and `run_code` output MUST be scanned for every injected value, a hit MUST be replaced by a redaction marker, and `gohan.sandbox.secret_leak` MUST increment.

#### Scenario: secret injected not visible
ID: `sandbox.secret-injected`
- WHEN the policy names `SecretRef{Name: "warehouse", Env: "PGPASSWORD"}`
- THEN the env var is set inside the workspace and the value appears in no history message, journal entry or span

#### Scenario: leak redacted
ID: `sandbox.secret-leak-redacted`
- WHEN `run_code` prints the injected value
- THEN the tool result carries a redaction marker and `gohan.sandbox.secret_leak` increments


### Requirement: Lifecycle and resume

The `PerCall` lifecycle MUST run `Open`, `Exec` and `Close` inside one tool call, and `Close` MUST be called exactly once per call, including when the call panics.

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

`Stdout` and `Stderr` MUST be capped by `ToolSpec.MaxOutput`; overflow and files the model asks to return MUST go to the `OutputStore` and come back as `File` blocks with a `Ref` beside a head excerpt. `RunLimits.MaxSandboxSeconds` MUST bound cumulative `ExecResult.Seconds` per run tree and `Usage.SandboxSeconds` MUST report the spend; exceeding the budget MUST return `*LimitExceededError{Limit: "MaxSandboxSeconds"}`.

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

`tool/exec` MUST stay the no-isolation runner for fixed-argv commands, and sandbox execution MUST NOT replace it. `Build` MUST warn when `tool/exec` is registered without `AllowHostExec()`, and the warning MUST name the tool.

#### Scenario: egress shares the policy type
ID: `sandbox.egress-shares-policy-type`
- WHEN a sandbox tool set is built with `Egress: EgressPolicy{Allow: ["api.example.com"]}`
- THEN `Explain` shows the same policy shape as a harness tool's egress and code inside the workspace cannot reach a private range

#### Scenario: host exec warning
ID: `sandbox.host-exec-warning`
- WHEN `tool/exec` is registered without `AllowHostExec()`
- THEN `Build` logs a warning naming the tool


### Requirement: Sanitization precedes persistence

Tool output bytes MUST be sanitized for every injected secret value before any byte is persisted to the `OutputStore` or rendered as an inline excerpt. The stored document and the head excerpt MUST both derive from the sanitized bytes, so a `Ref` served by `read_output` cannot carry an injected value. Sanitization MUST be idempotent, and MUST NOT alter bytes when the policy injects no value.

#### Scenario: stored output is redacted
ID: `sandbox.output-stored-redacted`
- WHEN `run_code` prints an injected value and stdout exceeds `MaxOutput`
- THEN the `OutputStore` holds the redacted bytes and the result's head excerpt carries the redaction marker

#### Scenario: read_output cannot re-enter the value
ID: `sandbox.output-ref-sanitized`
- WHEN the `Ref` produced by a redacted result is read back through `read_output`
- THEN the returned content contains no injected value

#### Scenario: redaction is deterministic
ID: `sandbox.output-redaction-deterministic`
- WHEN two calls emit equal stdout containing the same injected value
- THEN both results carry the same content-addressed `Ref` and the same redacted excerpt

### Requirement: Route-independent leak counting

Every occurrence of an injected value in a stream MUST increment `gohan.sandbox.secret_leak` exactly once, whether the bytes stay inline or spill to the store, and a second sanitization pass over an already-redacted result MUST NOT count again. The inline text MUST be bounded by `ToolSpec.MaxOutput` measured after sanitization, plus the truncation marker.

#### Scenario: count is route independent
ID: `sandbox.leak-count-route-independent`
- WHEN a call prints an injected value twice with stdout under the cap and a second call prints it over the cap
- THEN `gohan.sandbox.secret_leak` counts every occurrence once for both routes

#### Scenario: excerpt bounded after redaction
ID: `sandbox.excerpt-cap-boundary`
- WHEN sanitized stdout reaches exactly `MaxOutput` bytes and one byte beyond it
- THEN the first result is returned inline with no `Ref` and the second carries a head excerpt of at most `MaxOutput` bytes plus the marker

### Requirement: Overflow without a store

When a stream exceeds `MaxOutput` and no `OutputStore` is wired, the tool MUST return the sanitized head excerpt with a marker that names the dropped byte count and states that no store is configured. It MUST NOT return the whole body inline, and it MUST NOT persist bytes.

#### Scenario: no-store overflow is explicit
ID: `sandbox.no-store-overflow`
- WHEN stdout exceeds `MaxOutput` and no `OutputStore` is wired
- THEN the result carries a bounded head excerpt and a marker naming the dropped bytes and the missing store, and no `Ref` is produced

