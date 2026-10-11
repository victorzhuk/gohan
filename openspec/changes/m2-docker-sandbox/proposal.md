# Proposal: m2-docker-sandbox

## Why

`openspec/changes/m2/design.md` §2.3 names `adapter/docker` as the dev/reference sandbox adapter ("Adapters: `adapter/docker` (dev, reference), `adapter/e2b`", `openspec/specs/sandbox/spec.md:77`), and `openspec/changes/m2/tasks.md` item 6 requires the shared suite to pass unchanged against both adapters. `testkit/sandboxtest` now ships that suite and its `Factory` seam (`testkit/sandboxtest/sandboxtest.go`), and `m2-sandbox-adapter-conformance` task 5.3 keeps the Docker leg blocked until an adapter lands with provider evidence. No `adapter/docker` module exists: `go.work` lists five adapter modules and none is Docker, `adapter/` has no Docker package, and no Go file imports a Docker client.

The Docker daemon is live on the workstation (server 29.8.2 linux, verified this session), so the Docker leg is the one sandbox adapter whose provider evidence can be established now. Until `adapter/docker` lands, the four conformance ids `sandbox.egress-denied`, `sandbox.egress-allowlisted`, `sandbox.exec-timeout` and `sandbox.egress-shares-policy-type` have no real adapter to run against, and the lifecycle change's killed-pod cleanup case (`m2-sandbox-lifecycle-resume` task 6.5, R6) stays unmet because no installed provider supplies durable enumeration and close by resource identity.

## What Changes

- Add the Go module `adapter/docker` (`github.com/victorzhuk/gohan/adapter/docker`), mirroring the existing adapter modules, and wire it into `go.work` and the explicit module lists in `Taskfile.yml`.
- Depend on the current Docker Engine Go SDK, `github.com/moby/moby/client v0.6.2` with `github.com/moby/moby/api v1.56.1` for the request and response types. The legacy `github.com/docker/docker` module is stale (`v28.5.2+incompatible`, 2025-11) and is not used.
- Implement `types.Sandbox` and `types.Workspace` over Docker containers: `Open` creates a container from a configured base image or restores one from a committed snapshot image, `Exec` runs an argv through `ExecCreate`/`ExecAttach`, `Put`/`Get` copy files with the container archive API, `SetEnv` is the injection path, `Snapshot` commits the container to a labelled image, and `Close` removes the container.
- Enforce `types.EgressPolicy` at the container network layer: empty `Egress` is a network-less container, an allowlist is a per-workspace bridge with a default-deny egress chain and the same private-range and hop rules as `std/egress`.
- Kill the exec process group at `MaxExecTime` and return rather than hang.
- Establish the snapshot contract for the Docker leg: a snapshot is a committed image that survives `Close` and a daemon restart, snapshots are labelled and retained until removed, and `Open` on a missing snapshot returns an error instead of a fresh workspace.
- Supply durable enumeration and close-by-resource-identity so the lifecycle change's killed-pod cleanup case has a provider: an optional interface beside the port, implemented by the adapter, with no method added to `Sandbox` or `Workspace`.
- Add the conformance binding `adapter/docker/sandbox_conformance_test.go` — one `_test.go` file that supplies a `Factory` and calls `sandboxtest.Sandbox(t, factory)` — and make it pass against the live daemon. This change owns no scenario id; the four adapter ids stay with `m2-sandbox-adapter-conformance`.

No spec delta: the adapter implements the frozen `sandbox` port and runs the existing suite, so no requirement text and no scenario id changes (`skip_specs: true`).

Explicitly out of scope: `adapter/e2b` (`m2-e2b-sandbox`), the two examples (`m2-research-report-example`, `m2-data-analyst-example`), and S4.

## Capabilities

### New Capabilities

- (none)

### Modified Capabilities

- (none)

The adapter implements the existing `sandbox` capability contract rather than introducing a new behavioral capability, and it changes no requirement: the port, the policy and the four adapter scenario ids are already stated in `openspec/specs/sandbox/spec.md` and owned by `m2-sandbox-governance` and `m2-sandbox-adapter-conformance`. The change therefore sets `skip_specs: true`, the pattern `m2-sandbox-adapter-conformance` uses.

## Impact

Affected code: a new module `adapter/docker` (`go.mod`, package `docker`, the `Factory`/`Probe`/`Recorder` implementation, the conformance binding `_test.go`), `go.work` (one `use` line) and `Taskfile.yml` (the Docker leg in the explicit module lists). One optional interface is proposed in `core/types` beside `WorkspaceLister` and implemented in the adapter; the frozen `Sandbox` and `Workspace` method sets do not move (`docs/design/compatibility.md:16`).

Dependencies: `m2-sandbox-governance` (shipped: the frozen port and `std/sandbox`), `m2-sandbox-adapter-conformance` (the suite and `Factory` seam, whose Docker leg this change unblocks), and `m2-sandbox-lifecycle-resume` (whose task 6.5 seam this change supplies a provider for). No `std/sandbox` behavior change: effect derivation, secrets, outputs, budget and lifecycle stay with their owners.

No release tag: `api:check` skips an untagged module (`Taskfile.yml`), and `m2-api-freeze` cuts `v1.0.0` at the M2 exit.
