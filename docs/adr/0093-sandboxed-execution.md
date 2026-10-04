# ADR-0093: Sandboxed execution port

Status: accepted · Amended by ADR-0129 (typed egress). · Origin: grill round 19 (2026-09-29)

## Decision

Core gains a `Sandbox` port (`Open(ref, SandboxPolicy) → Workspace{Exec, Put, Get, Snapshot, Close}`) and `std/sandbox` ships the model-facing tool set (`run_code`, `write_file`, `read_file`). Effect is derived from policy (`PerCall`+no egress → `ReadOnly`; `PerSession`+no egress → `Idempotent`; any egress → `SideEffect`); egress is deny-all by default including DNS; secrets are resolved through `CredentialSource` and injected as env, never visible to the model, with output scanning; outputs overflow to `OutputStore`; `PerSession` workspaces are snapshotted on suspension into `Checkpoint.Workspace` and restored on resume; `Replay` never re-executes; `RunLimits.MaxSandboxSeconds` and `Usage.SandboxSeconds` make sandbox time a budget dimension. Isolation is an adapter (`adapter/docker` dev, `adapter/e2b`, Kubernetes later); gohan ships no runner. `tool/exec` stays the host runner with a `Build` warning unless `AllowHostExec()`.

## Context and evidence

Three of the reference examples need model-authored code; without a port each would hand-roll platform glue and lose journal, guard and resume semantics, and workspace state would not survive a pod change. Field consensus covers isolation and egress but leaves the harness contract (lifecycle, secrets path, cost dimension, replay) open.

See `docs/design/evidence.md` for the published sources behind this decision.

## Consequences

New capability `sandbox`; `stores.Checkpoint` gains `Workspace`; `limits.RunLimits` gains `MaxSandboxSeconds`; `model.Usage` gains `SandboxSeconds`; `tools` `tool/exec` gets the host-runner note; `telemetry` gains `gohan.sandbox.*`; `data-analyst` example updated.
