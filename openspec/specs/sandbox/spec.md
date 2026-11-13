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
	SetEnv(ctx context.Context, env map[string]string) error
	Snapshot(ctx context.Context) (WorkspaceRef, error)
	Close(ctx context.Context) error
}

// SandboxHandles is the optional lifecycle coordinator beside the Sandbox port.
// It owns one live Workspace per (session, owner) and never grows Sandbox or
// Workspace. There is no Withdraw operation.
type SandboxHandles interface {
	Acquire(ctx context.Context, sessionID string, owner WorkspaceOwner) (Workspace, error)
	Publish(ctx context.Context, sessionID string, owner WorkspaceOwner) (WorkspaceSnapshot, error)
	Release(ctx context.Context, sessionID string, owner WorkspaceOwner) error
	Sweep(ctx context.Context, limit int) error
}

type WorkspaceRef string

// WorkspaceOwner fences a live handle to one run and one lease generation.
type WorkspaceOwner struct {
	RunID           string
	LeaseGeneration uint64
}

// WorkspaceSnapshot is the durable outcome of one publication.
type WorkspaceSnapshot struct {
	Ref        WorkspaceRef
	Generation int64
}

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
2. **Secrets.** `Secrets` are resolved by the harness through `SecretResolver` for the run originator at `Open` and injected as environment inside the workspace through `Workspace.SetEnv`. Secret values never appear in tool args, results, history, journal or spans; `read_file`/`run_code` output is scanned for injected values and redacted (`gohan.sandbox.secret_leak` increments).
3. **Outputs.** `Stdout`/`Stderr` are capped by `ToolSpec.MaxOutput`; overflow and files the model asks to return go to `OutputStore` and come back as `File` blocks with `Ref`.
4. **Lifecycle.** `PerCall`: `Open` → `Exec` → `Close` inside one tool call. `PerSession`: one workspace per session, kept open across turns through one live handle fenced by the run id and the store-minted lease generation; on suspension the harness calls `Snapshot` and stores the ref and its generation in `Checkpoint.Workspace` and `Checkpoint.WorkspaceGeneration` before the suspension is persisted; at the end of a detached run it publishes the ref as the session's durable workspace metadata and creates no checkpoint; `Resume` opens from that ref under the generation-and-owner restore rules; the live handle is released after the suspension or terminal write succeeds, and a failed close leaves a cleanup record the sweep retries.
5. **Replay.** Exec results are journaled like any tool result; `Replay` replays them without re-executing and restores the workspace from the checkpoint ref, so a run resumed on another pod sees the same files.
6. **Limits.** `RunLimits.MaxSandboxSeconds` bounds cumulative `ExecResult.Seconds` per tree; `Usage.SandboxSeconds` reports it; exceeding it is `*LimitExceededError`. `MaxExecTime` kills the process group inside the workspace; a kill on a `SideEffect` policy is `Outcome: Unknown`, otherwise `Failed`.
7. **Guards.** Model-written code is `OriginModel`; `run_code` args pass the permission gate (`Ask` recommended for `SideEffect` policies); results pass `StageToolResult`.
8. **Host runner.** `tool/exec` stays the no-isolation runner for fixed-argv commands. `Build` warns when it is registered without `AllowHostExec()`.
9. **Placement.** The coordinator port `SandboxHandles` and its `WorkspaceOwner` and `WorkspaceSnapshot` value types live in `core/types`; the coordinator implementation lives in `std/sandbox` and is constructed there once and shared by the tool set and the lifecycle hooks; the optional durable surface `SessionWorkspaceMeta` lives in `core/stores`.

Adapters: `adapter/docker` (dev, reference), `adapter/e2b`; a Kubernetes agent-sandbox adapter later. Conformance: `sandboxtest` exercises every rule against an adapter with a deny-all egress probe.

Metrics: `gohan.sandbox.seconds` by lifecycle, `gohan.sandbox.opens`, `gohan.sandbox.cold_start` histogram, `gohan.sandbox.secret_leak`, `gohan.sandbox.egress_denied`.

### Workspace interface

`core/types` MUST declare `Sandbox.Open(ctx, WorkspaceRef, SandboxPolicy) (Workspace, error)` and `Workspace.Exec`, `Put`, `Get`, `SetEnv`, `Snapshot` and `Close`; `WorkspaceRef` MUST be one shared type aliased where it is consumed. `SetEnv(ctx, map[string]string) error` MUST be the only injection path into a workspace.

### Workspace listing

Listing MUST NOT be a `Workspace` method. `WorkspaceLister` is an optional interface declaring `List(ctx, path) ([]WorkspaceEntry, error)`, and the harness MUST reach it by type assertion on the opened workspace. A workspace that does not implement it MUST fail `list_files` with `ErrWorkspaceListingRequired`; the harness MUST NOT return a partial listing as the answer.

### Secret value exposure

An injected value MUST NOT appear in tool arguments, results, history, journal, audit records, spans or logs.

### Secret output redaction

`read_file` and `run_code` output MUST be scanned for every injected value, a hit MUST be replaced by a redaction marker, and `gohan.sandbox.secret_leak` MUST increment.

### Workspace handle fencing

The live handle MUST be bound to the owning run id and the store-minted lease generation, and a stale owner MUST be refused.

### Suspension workspace snapshot

On suspension the harness MUST snapshot the live handle through the fallible lifecycle path and MUST store the produced `WorkspaceRef` and its generation in `Checkpoint.Workspace` before the suspension is persisted.

### Detached workspace publication

At the end of a detached run the harness MUST snapshot the live handle and MUST persist the produced ref as the session's durable workspace ref; a detached run creates no checkpoint.

### Workspace restoration

On resume the harness MUST open the workspace from `Checkpoint.Workspace` only when the checkpoint generation is not older than the session's stored generation, and a missing or expired snapshot MUST return an error instead of opening fresh.

### Terminal workspace publication

At the end of a non-detached run the harness MUST publish the durable session ref before the run's terminal write, and MUST close the live handle only after that terminal write succeeds.

## Requirements

### Requirement: Policy and effect

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

The `PerSession` lifecycle MUST keep one workspace per session across turns through one live handle owned by the run: a later call in the same run MUST reuse that handle, and a call in a later run MUST open from the session's durable `WorkspaceRef` so files written in an earlier turn are visible without re-upload.

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

### Requirement: Per-session workspace handle ownership

One live workspace handle MUST exist per `(session, run owner)`, and the coordinator MUST own the exact `Workspace` that `Acquire` received from `Open`. Ownership MUST be bound to the run id and the store-minted lease generation; `Publish` and `Release` from a stale owner MUST be rejected. `Release` MUST close that exact handle, MUST NOT open a workspace in order to close it, and MUST NOT clear the durable snapshot reference. `Acquire`, `Publish` and `Release` MUST propagate their errors.

#### Scenario: handle reused by one owner
ID: `sandbox.handle-owner-exclusive`
- WHEN two runs in one session start on two pods
- THEN only the run holding the session lease acquires the live handle and no second workspace opens

#### Scenario: stale lease generation is fenced
ID: `sandbox.handle-owner-fenced`
- WHEN a run whose lease generation is no longer the current one calls `Publish` or `Release`
- THEN both calls are rejected and the durable snapshot reference stays unchanged

#### Scenario: release closes the acquired handle
ID: `sandbox.release-closes-acquired-handle`
- WHEN `Release` runs for a session that acquired a live handle
- THEN the handle returned by that `Acquire` is closed exactly once and no fresh workspace is opened

### Requirement: Bounded cleanup sweep

`Sweep` MUST accept a positive limit and MUST process at most that many records. The stack recovery path MUST invoke `Sweep` even when the stale-run query returns no rows. Claiming a record for cleanup and acquiring a live handle for use MUST exclude each other. A failed close MUST retain a cleanup record and the durable snapshot so a later sweep retries.

#### Scenario: cleanup claim excludes live use
ID: `sandbox.cleanup-claim-excludes-live-use`
- WHEN a sweep claims a session record while a live run still uses its handle
- THEN the claim is refused and the handle is not closed

#### Scenario: sweep honours its limit
ID: `sandbox.sweep-bounded-limit`
- WHEN `Sweep` is called with limit three and five cleanup records exist
- THEN at most three records are processed

#### Scenario: recovery sweeps without stale runs
ID: `sandbox.recover-sweeps-without-stale-runs`
- WHEN recovery finds no stale running rows but cleanup records exist
- THEN the sweep still runs and processes them

#### Scenario: failed close retains the snapshot
ID: `sandbox.close-failure-retains-snapshot`
- WHEN closing a tracked handle fails
- THEN a cleanup record remains and the durable snapshot reference is unchanged

### Requirement: Per-session build admission

A build that declares `PerSession` MUST fail `Build` when no workspace coordinator or no durable workspace-metadata surface is wired, and MUST return no tool set.

#### Scenario: build refuses an incapable per-session build
ID: `sandbox.build-refuses-incapable-persession`
- WHEN `Build` is given a `PerSession` sandbox policy without a coordinator or durable metadata surface
- THEN `Build` fails and no tool set is registered

### Requirement: Durable workspace metadata with generation compare-and-set

Durable per-session workspace metadata MUST hold a snapshot ref, a generation and an owner, and the generation MUST be the sole compare-and-set token. A write MUST atomically compare the expected generation and validate the current lease owner. A publication MUST write the expected generation plus one. A compare-and-set loser MUST return an error and MUST NOT report another owner's snapshot as its own.

#### Scenario: generation fenced compare-and-set
ID: `sandbox.ref-generation-fenced`
- WHEN two publications race for one session
- THEN exactly one compare-and-set succeeds and the newer generation is kept

### Requirement: Owner proof in the atomic workspace write

The owner proof in a workspace metadata write MUST match the session's current run id, the run's own session, a nonzero matching lease generation and a live unexpired lease. A store with no wired owner guard MUST refuse every write. A stale owner proof MUST return `types.ErrRunNotActive`, and a stale generation token MUST return `types.ErrVersionConflict`. A refusal MUST leave the stored metadata unchanged.

#### Scenario: owner validated in the same atomic write
ID: `sandbox.owner-cas-atomic`
- WHEN a write presents a stale lease generation
- THEN the write fails and the stored metadata is unchanged

#### Scenario: absent owner guard refuses writes
ID: `sandbox.owner-guard-required`
- WHEN a workspace metadata write runs on a store with no wired owner guard
- THEN the write is refused and the stored metadata is unchanged

### Requirement: Restore rules for session workspace metadata

A restore MUST NOT rewrite metadata that is already equal. A checkpoint older than the stored snapshot MUST leave the stored snapshot in place. A checkpoint newer than the stored snapshot MUST have a matching durable publication, and the restore MUST fail without it. Generation zero with an empty ref MUST open a fresh workspace. Generation zero with a nonempty legacy ref MUST be imported explicitly into absent metadata.

#### Scenario: restore keeps a later generation
ID: `sandbox.restore-keeps-newer-generation`
- WHEN a checkpoint's workspace generation is older than the session's stored generation
- THEN the resume keeps the stored ref and does not restore the older snapshot

#### Scenario: equal metadata is not rewritten
ID: `sandbox.restore-skips-equal-metadata`
- WHEN a restore presents metadata equal to the stored metadata
- THEN no write is issued

#### Scenario: session without a ref migrates
ID: `sandbox.no-ref-session-migrates`
- WHEN an existing session with no stored workspace metadata runs a `PerSession` call
- THEN the workspace opens fresh and its first generation is published at terminal end

#### Scenario: legacy ref requires explicit import
ID: `sandbox.legacy-ref-explicit-import`
- WHEN stored metadata is absent and a legacy nonempty ref is presented
- THEN the import writes first generation metadata only after an explicit import call

### Requirement: Explicit legacy workspace import

An explicit import operation MUST write a legacy workspace snapshot into absent durable metadata. It MUST require session-write authority and a current owner lease, and it MUST validate the owner in the store rather than trust a caller-supplied value. It MUST reject a nonzero input generation, an empty ref and existing metadata, and every rejection MUST leave the stored metadata unchanged. `Resume` and `Recover` MUST NOT invoke it implicitly.

#### Scenario: import writes generation one
ID: `sandbox.import-writes-generation-one`
- WHEN an authorized caller with a live lease imports a generation-zero nonempty ref into absent metadata
- THEN the store holds generation one with that ref and the validated owner

#### Scenario: import refuses without mutation
ID: `sandbox.import-refuses`
- WHEN an import presents an unauthorized caller, a stale owner, a nonzero input generation, an empty ref or existing metadata
- THEN the import returns an error and the stored metadata is unchanged

### Requirement: Workspace mutation durability

A `PerSession` call that may mutate the workspace MUST publish the workspace durably after every body invocation that may mutate, including a body that reports a partial-write error and a body whose execution exit is nonzero. A read-only call MUST NOT publish.

#### Scenario: partial write still publishes
ID: `sandbox.partial-mutation-publishes`
- WHEN a mutating body returns a partial-write error
- THEN the harness still attempts the durable publication

#### Scenario: nonzero exit still publishes
ID: `sandbox.nonzero-exit-publishes`
- WHEN a mutating body returns a nonzero execution exit
- THEN the harness still attempts the durable publication

### Requirement: Publication failure settlement

A durable publication failure MUST return an explicit `Unknown` result whose error kind is `OutcomeUnknown`. The existing execution path MUST record that uncertainty under the original call key through one governance and journal path, and the harness MUST NOT run a second such path. Any recorded error or output MUST be sanitized before persistence.

#### Scenario: mutation published before completion
ID: `sandbox.mutation-published-before-complete`
- WHEN a `write_file` call mutates the workspace and the durable publication fails
- THEN the result outcome is `Unknown` with error kind `OutcomeUnknown` under the original call key

#### Scenario: publication error sanitized before persistence
ID: `sandbox.publication-error-sanitized`
- WHEN a publication failure records an error message
- THEN the persisted message carries no unsanitized adapter output

### Requirement: Suspension release ordering

Suspension MUST publish through the coordinator before the checkpoint is saved, and MUST release only after the checkpoint is stored and the run's suspension transition succeeded. A snapshot or compare-and-set failure MUST save no checkpoint and MUST complete no suspension. A run with no acquired workspace MUST NOT publish. A failed close during the release MUST retain the durable snapshot and a cleanup record.

#### Scenario: release follows suspension storage
ID: `sandbox.release-after-suspend-storage`
- WHEN a `PerSession` run suspends
- THEN `Publish` precedes the checkpoint save, `Release` follows the suspension transition, and the durable snapshot survives

#### Scenario: failed suspension publish saves no checkpoint
ID: `sandbox.suspend-publish-failure-aborts`
- WHEN the snapshot or the compare-and-set fails during suspension
- THEN no checkpoint is saved and the run does not suspend

### Requirement: Terminal release ordering

The terminal path MUST publish before the terminal write and MUST release only after that write succeeds. A terminal write refused with `ErrSignalsPending` MUST keep the live handle and the heartbeat. A terminal publication failure MUST record `Failed` without a successful result ref, MUST NOT invent an uncertain-workspace run state, and MUST NOT repeat the failing publication while recording the failure.

#### Scenario: terminal release follows the terminal write
ID: `sandbox.terminal-publish-before-complete`
- WHEN a `PerSession` run reaches its terminal write
- THEN the snapshot is published before the terminal write and the handle is released only after it succeeds

#### Scenario: refused terminal write keeps the handle
ID: `sandbox.signals-pending-keeps-handle`
- WHEN the terminal write is refused with `ErrSignalsPending`
- THEN the live handle and the run heartbeat stay alive and no release runs

### Requirement: Cross-pod restore with independent coordinators

A restore on another pod MUST use its own coordinator instance and adapter instance, and MUST restore through the shared durable metadata and snapshot backend. A snapshot MUST survive the close of the original live resource. A restore MUST NOT depend on the live handle recorded by another process.

#### Scenario: independent pod restores the snapshot
ID: `sandbox.cross-pod-independent-coordinators`
- WHEN pod A writes, snapshots and closes its handle and pod B restores the ref with its own coordinator
- THEN pod B reports the file written by pod A and opens no fresh workspace

#### Scenario: missing snapshot returns an error
ID: `sandbox.restore-missing-snapshot-errors`
- WHEN the stored snapshot is missing or expired at restore time
- THEN the restore returns an error and opens no fresh workspace

### Requirement: Data analyst suspend, resume and page composition

A read-only warehouse query MUST carry a `PerSession` `SandboxPolicy` with an empty `Egress.Allow`, MUST inject its credential only through `Workspace.SetEnv`, and MUST redact output for injected values and personally identifying patterns BEFORE journaling and before an overflow body reaches the `OutputStore`. On suspension the produced workspace ref MUST be stored before the suspension persists, and a later turn on another stack MUST open from that ref and replay a journaled result.

#### Scenario: data analyst resumes and pages
ID: `sandbox.data-analyst-resume-and-page`
- WHEN a session runs a read-only warehouse query that writes an extract, then suspends and resumes on a second stack that opens from the stored ref
- THEN the resumed turn reads the extract with no extra `Put` and no re-execution, and `read_output(ref, range)` pages back the requested slice with no personally identifying value

### Requirement: Workload execution cannot administer network enforcement

Untrusted workspace execution MUST have no authority to change its network enforcement or regain that authority. Trusted policy setup MUST complete before the workspace becomes usable. Failed setup MUST return no usable workspace and MUST clean its created resources.

#### Scenario: Workload code attempts to remove the firewall
ID: `sandbox.firewall-authority-isolated`
- WHEN workspace execution attempts firewall removal or capability regain
- THEN the attempt fails, an allowed controlled peer remains reachable, and a denied controlled peer remains unreachable

#### Scenario: Privileged setup fails
ID: `sandbox.policy-setup-failure-cleanup`
- WHEN trusted network policy setup fails
- THEN opening the workspace fails and no usable unprotected workspace or setup resource is left behind

#### Scenario: A restored snapshot replaces its startup executable
ID: `sandbox.restored-startup-policy-first`
- WHEN a restored workspace snapshot contains a modified startup executable or an inherited healthcheck
- THEN no workload process executes before policy is established and inherited healthchecks do not run

### Requirement: Snapshot references identify immutable content

A successful snapshot reference MUST retain its content across later publications from the same workspace. A failed publication or metadata compare-and-set MUST NOT change content behind an earlier committed reference. Existing labelled snapshot references MUST remain readable and removable.

#### Scenario: One live workspace publishes twice
ID: `sandbox.same-workspace-snapshots-immutable`
- WHEN a workspace publishes A, changes a file, then publishes B
- THEN restoring A and B yields their respective file contents

#### Scenario: A later metadata publication loses its compare-and-set
ID: `sandbox.snapshot-cas-failure-keeps-content`
- WHEN a later snapshot publication fails its metadata compare-and-set
- THEN restoring the previously committed reference yields its original content

#### Scenario: A legacy tagged snapshot is restored
ID: `sandbox.legacy-snapshot-ref-readable`
- WHEN an existing labelled tagged snapshot reference is opened or removed
- THEN restore and cleanup remain supported without retagging or deleting another snapshot

