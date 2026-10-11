# Design: m2-docker-sandbox

## Context

`adapter/docker` is the dev/reference sandbox adapter the `sandbox` capability names (`openspec/specs/sandbox/spec.md:77`), and `m2-sandbox-adapter-conformance` task 5.3 keeps the Docker leg blocked until an adapter lands with provider evidence. The port and the governed tool set ship from `2026-10-07-m2-sandbox-governance`; the suite and its `Factory` seam ship from `m2-sandbox-adapter-conformance`. Facts verified in the worktree this session:

- The port under test is `types.Sandbox.Open(ctx, WorkspaceRef, SandboxPolicy) (Workspace, error)` with the six-method `Workspace` `Exec/Put/Get/SetEnv/Snapshot/Close` (`core/types/sandbox.go`). `SandboxPolicy.Egress` is `types.EgressPolicy` with `Allow []string` and `PrivateRanges` (`DenyPrivate`/`AllowPrivate`) (`core/types/tool.go`).
- The suite is `sandboxtest.Sandbox(t, f Factory)`; `Factory` supplies `Sandbox(t *testing.T) types.Sandbox`, `Probe() Probe` and `Recorder() Recorder`. `Probe` is `Dial(host string, port int) []string`, `Sleep(d time.Duration) []string`, `Echo(s string) []string`. `Recorder` is `Denied() int`, `Kinds() []stores.AuditKind`, `Reset()`. The binding is one `_test.go` file that calls `sandboxtest.Sandbox(t, factory)` (`testkit/sandboxtest/sandboxtest.go`; `adapter/eino/conformance_test.go` is the binding pattern).
- `go.work` lists five adapter modules (`.`, `./adapter/anthropic`, `./adapter/eino`, `./adapter/openai`, `./adapter/otel`, `./adapter/postgres`, `./examples`) and no Docker module. `Taskfile.yml` enumerates each adapter module explicitly in `spec:coverage`, `spec:gate`, `test` and `test:race`, so a new module needs a line in each list.
- No Go file imports a Docker client. The only Docker-adjacent dependency in the tree is `testcontainers-go` under `adapter/postgres`, which pulls `github.com/moby/moby/client v0.5.0` and `github.com/moby/moby/api v1.55.0` as indirect dependencies.
- The Docker daemon is live: server `29.8.2`, `linux`, verified this session.

The current Docker Engine Go SDK is `github.com/moby/moby/client v0.6.2` (released 2026-10-08) with `github.com/moby/moby/api v1.56.1` (released 2026-10-01) for the request and response types. The legacy monolith `github.com/docker/docker` is stale (`v28.5.2+incompatible`, 2025-11-05) and is not the client this change pins.

## Goals / Non-Goals

**Goals:** a new `adapter/docker` module on the current Docker Engine Go SDK; a `types.Sandbox`/`types.Workspace` implementation over containers; container network-layer `EgressPolicy` enforcement with the shared private-range and hop rules; a process-group kill at `MaxExecTime`; a snapshot contract (commit to a labelled image, survives `Close` and daemon restart, missing-snapshot errors); durable enumeration and close-by-resource-identity for the lifecycle cleanup case; a conformance binding that is green against the live daemon.

**Non-Goals:** no `adapter/e2b`; no examples; no S4; no change to the frozen `Sandbox`/`Workspace` method sets; no change to `std/sandbox` behavior; no change to `openspec/specs/sandbox/spec.md` (the change adds an adapter, so `skip_specs: true`); no new scenario id; no `std/sandbox` egress or secret reimplementation — the adapter enforces egress, governance owns derivation and redaction.

## Admission

Two provider facts the Docker leg must establish, and one it cannot:

- **Daemon liveness — established.** The daemon answers on the default socket: server `29.8.2`, `linux`, observed this session. This is the provider evidence `m2-sandbox-adapter-conformance` task 5.3 requires before the Docker leg leaves blocked, and this change records it.
- **Snapshot survival and enumeration — establishable on Docker.** A committed image survives `Workspace.Close` and a daemon restart; labelled images are enumerable through `ImageList`; a missing snapshot image is a distinguishable error. Docker therefore answers the R6 questions for the Docker leg: snapshot survival, retention (labelled images retained until explicitly removed), and a missing-snapshot error.
- **Killed-pod close by resource identity — establishable on Docker, bounded.** A container carries the workspace's resource identity in a label, so `Close`-by-identity is a label-filtered `ContainerRemove`; a dead pod's containers are enumerable by label through `ContainerList` and removable by the next pod. This satisfies the durable-enumeration and close-by-resource-identity shape `m2-sandbox-lifecycle-resume` task 6.5 needs. The E2B leg of that case stays with `m2-e2b-sandbox`; this change does not claim it.

This change does not reopen R6 as a whole: it supplies the Docker provider contract the lifecycle change consumes and records the Docker-specific answers. The lifecycle change still owns wiring the sweep.

## Decisions

### D1 — Module shape, workspace wiring and the pinned client

`adapter/docker` is a separate module `github.com/victorzhuk/gohan/adapter/docker`, mirroring `adapter/eino` and `adapter/postgres` (its own `go.mod` with a `replace`-free dependency on the root module `github.com/victorzhuk/gohan v0.1.0`, the same shape `adapter/eino/go.mod` uses). The module is added to `go.work` as `use ./adapter/docker` and to the explicit module lists in `Taskfile.yml` (`spec:coverage`, `spec:gate`, `test`, `test:race`).

The client dependency is `github.com/moby/moby/client v0.6.2` for the API methods and `github.com/moby/moby/api v1.56.1` for the request/response types. The package is imported as:

```go
import (
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)
```

`client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())` builds the client from `DOCKER_HOST` and the default socket, the same constructor `pkg.go.dev/github.com/moby/moby/client v0.6.2` documents. The API methods this adapter uses, all verified against the pinned module source, are `ContainerCreate`, `ContainerStart`, `ContainerRemove`, `ContainerList`, `ContainerInspect`, `ContainerCommit`, `CopyToContainer`, `CopyFromContainer`, `ExecCreate`, `ExecAttach`, `ExecInspect`, `ImageInspect`, `ImageList`, `ImageRemove` and `ImageTag`. The legacy `github.com/docker/docker` module is not imported.

### D2 — `Open` creates or restores a container; `WorkspaceRef` is the container identity

```go
// adapter/docker — illustrative; the port shapes are fixed by core/types.
func (s *Sandbox) Open(ctx context.Context, ref types.WorkspaceRef, p types.SandboxPolicy) (types.Workspace, error)
```

`Open` is the only factory. With an empty `ref` it creates a fresh container from the configured base image (`ContainerCreate` with the workspace labels, the `HostConfig` the policy dictates, and `Egress` applied per D4), starts it (`ContainerStart`) and returns a `*workspace` whose identity is the container id. With a non-empty `ref` it resolves the ref to a committed snapshot image (D5), inspects it (`ImageInspect`), and creates a container from that image. `ref` is opaque to the harness; the adapter defines it as the snapshot image tag or id.

The adapter configures the container so `Exec` can run the `Probe` vocabulary without a shell wrapper: the base image is configurable (default a small image carrying a POSIX shell and the dial/sleep/echo tools), and `ExecCreateOptions.Cmd` is the argv the probe returns. The suite never names an image: the image, socket and any credential arrive through the adapter's `Factory`, not through the suite (`testkit/sandboxtest` D2).

### D3 — `Exec` runs argv through `ExecCreate`/`ExecAttach`; a non-zero exit is a result, a start failure is an error

`Workspace.Exec` maps `types.ExecRequest{Argv, Stdin, Cwd, Timeout}` onto `ExecCreateOptions{Cmd: Argv, WorkingDir: Cwd, AttachStdout: true, AttachStderr: true, AttachStdin: req.Stdin != nil, Env: <injected env>}`, then `ExecStart`/`ExecAttach` and `ExecInspect` for the exit code. It returns `types.ExecResult{ExitCode, Stdout, Stderr, Truncated, Seconds}`, matching the port's rule that a non-zero exit is a result and the adapter's failure to start the process is the error (`2026-10-07-m2-sandbox-governance` D1.2). `Seconds` is measured around the exec. Output beyond `ToolSpec.MaxOutput` is the harness's cap, not the adapter's: the adapter returns the bytes and `Truncated=false`; the governance layer caps and spills (`std/sandbox` D5).

### D4 — Egress is enforced at the container network layer, with the shared private-range and hop rules

`std/sandbox` passes `Egress` through and interprets no allowlist (`…-governance/design.md` D2/D3); enforcement is the adapter's obligation. The Docker adapter enforces `types.EgressPolicy` at the container network layer:

- Empty `Egress` (deny-all, including DNS) — the container runs with `NetworkMode: "none"`, so the dial probe and any name resolution fail inside the workspace and `Exec` returns a non-zero result.
- Non-empty `Egress.Allow` — the container attaches to a per-workspace bridge network with a default-deny egress chain that permits only the allowlisted hosts and ports, and `PrivateRanges: DenyPrivate` blocks RFC1918 and link-local destinations while `AllowPrivate` does not, applying the same private-range and hop rules as `std/egress` (`core/types/tool.go`). The allowlisted dial succeeds and a dial to another host is denied inside the workspace.

The denial is reported to the harness so the governed path can classify it: the adapter records the refusal through the shared observation seam and the harness appends `stores.AuditEgressDenied` (`core/tool_call.go`, ADR-0156). The denial-counter name (`gohan.sandbox.egress_denied` vs `gohan.egress.requests{decision=denied}`) is the suite's open item O1 and is not settled here; the adapter reports through the seam and asserts no metric name.

### D5 — Snapshot is a committed, labelled image; survival, retention and missing-snapshot are defined

`Workspace.Snapshot` calls `ContainerCommit` with `NoPause: true` and a deterministic reference derived from the workspace's session/run identity, producing a committed image tagged and labelled as a sandbox snapshot. The returned `types.WorkspaceRef` is that image reference. The Docker-leg answers to the R6 questions:

- **Survival.** A committed image outlives the container: after `Workspace.Close` removes the container, `Open` on the returned ref creates a new container from the image and the previously written files are present. The image also survives a daemon restart because it is in the daemon's image store.
- **Retention.** A snapshot image is retained until explicitly removed; it is labelled so a sweep can enumerate snapshots (`ImageList` with the label filter) and remove stale ones (`ImageRemove`). Retention is explicit, not time-based; the adapter removes a snapshot only when told to.
- **Missing snapshot.** `Open` on a ref whose image does not exist returns an error naming the missing ref (`ImageInspect` reports not-found) and opens no fresh workspace, matching the lifecycle change's missing-snapshot rule (`m2-sandbox-lifecycle-resume` D3/D9). A fresh workspace is created only for an empty ref.

The snapshot ref stays opaque to the harness; the adapter owns the tag scheme and the labels.

### D6 — `MaxExecTime` kills the process group and `Exec` returns

`Exec` honours `MaxExecTime` (from `SandboxPolicy` and, per call, `ExecRequest.Timeout`): it runs the exec under a context with that deadline, and on expiry it kills the process group inside the container (the exec process and its children, not the container) and returns the `ExecResult` rather than hanging. The harness classifies the effect: `Unknown` under a `SideEffect` policy and `Failed` otherwise, the same mapping `std/tool/exec` already implements (`std/tool/exec/exec.go` `timeoutResult`). The suite's `sandbox.exec-timeout` subtest asserts the kill at the adapter (the sleep probe does not survive its deadline) and the effect-keyed outcome at the harness.

### D7 — Durable enumeration and close-by-resource-identity via an optional interface

The lifecycle change needs a provider that keeps durable records and closes by resource identity (`m2-sandbox-lifecycle-resume` task 6.5, R6). Docker supplies it through an optional interface declared in `core/types` beside `WorkspaceLister` and discovered by type assertion — never a method added to `Sandbox` or `Workspace`, which are frozen (`docs/design/compatibility.md:16`):

```go
// core/types/sandbox.go — proposed, optional, beside WorkspaceLister
// SandboxResources enumerates durable sandbox resources and closes one by
// its resource identity, so a later pod can reclaim what a dead pod left.
// It is reached by type assertion on the Sandbox value; the port does not grow.
type SandboxResources interface {
	Enumerate(ctx context.Context) ([]WorkspaceRef, error)
	CloseByRef(ctx context.Context, ref WorkspaceRef) error
}
```

`adapter/docker` implements it: `Enumerate` lists containers and snapshot images by the sandbox label; `CloseByRef` removes the container by label-filtered `ContainerRemove` and, for a snapshot, `ImageRemove`. A dead pod's containers and images carry the label, so the next pod enumerates and closes them without the dead pod's process. `std/sandbox`'s bounded `Sweep` (lifecycle change D7) consumes the interface; the adapter only supplies it. The interface is proposed here and lands at apply time under the ADR that settles the lifecycle change, so the two changes agree on one shape.

### D8 — The conformance binding is one file and the adapter owns no scenario id

`adapter/docker/sandbox_conformance_test.go` is the only binding file: it constructs the adapter's `Factory` (the `Sandbox`, the `Probe` and the `Recorder`), and calls `sandboxtest.Sandbox(t, factory)`. The four owned scenario ids `sandbox.egress-denied`, `sandbox.egress-allowlisted`, `sandbox.exec-timeout` and `sandbox.egress-shares-policy-type` stay owned once by `m2-sandbox-adapter-conformance`; the adapter defines none locally and keeps no private copy (`openspec/changes/m2/design.md` §2.3, "adapters implement the suite contract and claim no duplicate coverage"). A second copy of an owned id is a review defect.

The binding is behind a build tag or a skip guard so `task test` (short) does not require a daemon, and the full leg runs against the live daemon: `go -C adapter/docker test -timeout 10m ./... -run TestSandboxConformance`. `Recorder` reports the denial observations through `stores.AuditKind` and the adapter's own sink; it carries no URL, path, query, user-info or body (ADR-0156).

### D9 — Public-path proofs and provider evidence

The binding drives the public path only: `types.Sandbox.Open` → the six `Workspace` methods, plus `std/sandbox.New` over the same adapter and the tool-call path for classification. No test reaches a private field. Provider evidence this change records:

- the daemon version and OS observed on the socket (server `29.8.2`, `linux`);
- a snapshot taken, the container closed, the daemon-restart survival of the image, and `Open` on the ref restoring the files;
- `Open` on a missing ref returning an error and opening no fresh workspace;
- `Enumerate` listing a labelled container left by a simulated dead pod and `CloseByRef` removing it.

## Risks / Trade-offs

- [Risk] A network-less container may not have the tools the `Probe` needs, so a real deny-all probe cannot run. → Mitigated: the base image is configurable and the adapter documents an image carrying a shell and the probe tools; the deny-all probe asserts the dial fails inside the workspace, which `NetworkMode: "none"` guarantees even when the tool runs.
- [Risk] The per-workspace bridge and egress chain add daemon state that a crash can leak. → Accepted and bounded: networks and containers carry the sandbox label, so `Enumerate`/`CloseByRef` (D7) reclaim them; retention is explicit, not time-based.
- [Risk] `Snapshot` via `ContainerCommit` pauses the container by default and can race a live exec. → Mitigated: `ContainerCommit` uses `NoPause: true` and the snapshot runs on the lifecycle's suspension or terminal ordering, which is serial to exec.
- [Risk] Pinning `moby/moby/client v0.6.2` couples the adapter to a fast-moving client. → Accepted: the module is pre-v1 and `api:check` skips an untagged module; the pin is explicit in `go.mod` and updated deliberately.
- [Risk] The optional `SandboxResources` interface is proposed here but lands under the lifecycle change's ADR, so the two could drift. → Mitigated: the shape is declared once in `core/types` and both changes reference it; the lifecycle change owns the ADR and the sweep, this change owns the Docker implementation.
- [Trade-off] The adapter does not interpret the allowlist itself; it delegates to the daemon's network layer. → Accepted: it is the isolation technology's job, and it keeps `std/sandbox` provider-neutral.

## Open items

- **O1 — Denial counter name.** Inherited from `m2-sandbox-adapter-conformance` design O1: `gohan.sandbox.egress_denied` (normative scenario) vs `gohan.egress.requests{decision=denied}` + `AuditEgressDenied` (shipped, ADR-0156). The adapter reports through the observation seam and asserts no metric name; the reconciliation stays with the conformance change.
- **O2 — E2B leg of the killed-pod cleanup case.** This change supplies the Docker provider contract for `m2-sandbox-lifecycle-resume` task 6.5; the E2B leg stays with `m2-e2b-sandbox` and is not claimed here.

## Migration Plan

No data migration. The change adds a module, one `use` line in `go.work`, the Docker legs in `Taskfile.yml`, one optional interface in `core/types` at apply time under the lifecycle change's ADR, and the adapter and its binding. No exported contract outside the new module changes; the frozen `Sandbox`/`Workspace` method sets do not move. Keep the module pre-v1 and create no release tag while authoring; `m2-api-freeze` cuts `v1.0.0` at the M2 exit.
