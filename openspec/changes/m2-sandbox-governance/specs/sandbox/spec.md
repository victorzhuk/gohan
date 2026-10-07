# Spec Delta

## Context

This delta MODIFIES all five requirements of the `sandbox` capability (openspec/specs/sandbox/spec.md:85-168). Every existing requirement block carries scenario blocks and no normative prose; each reproduced block below carries its scenarios byte-identical and adds the clauses the capability had only as Contract prose (rules 1-8), including the port method set, which lands under *Policy and effect*. The seven scenarios this change does not own stay in their blocks unchanged: the egress and exec-timeout rows belong to `m2-sandbox-adapter-conformance` and the `PerSession` and `Replay` rows to `m2-sandbox-lifecycle-resume`. This package is not the whole of sandbox conformance (openspec/changes/m2/design.md §2.3).

## MODIFIED Requirements

### Requirement: Policy and effect

`core/types` MUST declare `Sandbox.Open(ctx, WorkspaceRef, SandboxPolicy) (Workspace, error)` and `Workspace.Exec`, `Workspace.Put`, `Workspace.Get`, `Workspace.Snapshot`, `Workspace.Close`, and `WorkspaceRef` MUST be one shared type aliased where it is consumed. `Lifecycle`, `SandboxPolicy`, `SecretRef`, `ExecRequest` and `ExecResult` MUST take the shapes the capability Contract block states.

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

Each `SecretRef` MUST be resolved by the harness through `CredentialSource` for the run originator at `Open` and injected as `Env` inside the workspace. A build without a source MUST inject nothing. A secret value MUST NOT appear in tool arguments, results, history, journal, audit records, spans or logs. `read_file` and `run_code` output MUST be scanned for every injected value, a hit MUST be replaced by a redaction marker, and `gohan.sandbox.secret_leak` MUST increment.

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