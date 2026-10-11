# Tasks: m2-docker-sandbox

A task is done when its `verify:` command is green and `task lint` and `task test` are green for what it touched. This change owns no scenario id: the four adapter ids — `sandbox.egress-denied`, `sandbox.egress-allowlisted`, `sandbox.exec-timeout` and `sandbox.egress-shares-policy-type` — stay owned once by `m2-sandbox-adapter-conformance`, and this change's binding runs that suite unchanged. A task below that names an owned id names it as the suite subtest the adapter must pass, not as a locally-defined test; the adapter defines no owned id. Every id referenced is an existing normative scenario in `openspec/specs/sandbox/spec.md`; no scenario id is invented.

The Docker daemon is live (server `29.8.2`, `linux`, verified this session). The conformance leg is the real adapter run; the daemon evidence is recorded, not assumed.

## 1. Module shape and workspace wiring

- [ ] 1.1 Create the module `adapter/docker` (`go.mod`, module `github.com/victorzhuk/gohan/adapter/docker`) mirroring `adapter/eino`: a `replace`-free dependency on the root module `github.com/victorzhuk/gohan v0.1.0`, and the pinned client `github.com/moby/moby/client v0.6.2` with `github.com/moby/moby/api v1.56.1`. Verify `timeout 300s go -C adapter/docker build ./...`.
  - evidence: `adapter/docker/go.mod` (`github.com/moby/moby/client v0.6.2`, `github.com/moby/moby/api v1.56.1`); `adapter/docker/go.sum`
- [ ] 1.2 Add `use ./adapter/docker` to `go.work` and the Docker leg to the explicit module lists in `Taskfile.yml` (`spec:coverage`, `spec:gate`, `test`, `test:race`), matching the existing adapter lines. Verify `timeout 300s go work sync` and `timeout 300s task --list` exit zero.
  - evidence: `go.work` (`use ./adapter/docker`); `Taskfile.yml` (the Docker leg in the four lists)
- [ ] 1.3 Build the client from the environment with `client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())` and a `Ping`/`ServerVersion` handshake that fails fast when no daemon is reachable. Verify `timeout 300s go -C adapter/docker build ./...` and `timeout 120s go -C adapter/docker test -timeout 2m ./... -run TestClientConnect`.
  - evidence: `adapter/docker/client.go` (`NewClientWithOpts` wiring); `adapter/docker/client_test.go` (`TestClientConnect`)

## 2. The `Sandbox` and `Workspace` implementation over containers

- [ ] 2.1 Implement `Open` (D2): an empty `WorkspaceRef` creates and starts a container from the configured base image with the sandbox labels; a non-empty ref resolves to a snapshot image and creates the container from it. Verify `timeout 300s go -C adapter/docker build ./...` and `timeout 120s go -C adapter/docker test -timeout 2m ./... -run TestOpen`.
  - evidence: `adapter/docker/sandbox.go` (`Open`, `ContainerCreate`, `ContainerStart`); `adapter/docker/sandbox_test.go` (`TestOpen`)
- [ ] 2.2 Implement `Exec` (D3): map `types.ExecRequest{Argv, Stdin, Cwd, Timeout}` onto `ExecCreateOptions`/`ExecStart`/`ExecAttach`, read the exit code through `ExecInspect`, and return `types.ExecResult{ExitCode, Stdout, Stderr, Truncated, Seconds}` — a non-zero exit is a result, a start failure is an error. Verify `timeout 120s go -C adapter/docker test -timeout 2m ./... -run TestExec`.
  - evidence: `adapter/docker/workspace.go` (`Exec`, `ExecCreate`/`ExecAttach`/`ExecInspect`); `adapter/docker/workspace_test.go` (`TestExec`)
- [ ] 2.3 Implement `Put`/`Get` over the container archive API (`CopyToContainer`/`CopyFromContainer`) and `SetEnv` as the only injection path, and `Close` as `ContainerRemove` of the container this workspace opened. Verify `timeout 120s go -C adapter/docker test -timeout 2m ./... -run 'TestPutGet|TestClose'`.
  - evidence: `adapter/docker/workspace.go` (`Put`, `Get`, `SetEnv`, `Close`); `adapter/docker/workspace_test.go` (`TestPutGet`, `TestClose`)

## 3. Egress enforcement at the container network layer

- [ ] 3.1 Implement the deny-all path (D4): an empty `Egress` runs the container with `NetworkMode: "none"`, so the dial probe fails inside the workspace. This is the adapter half of `sandbox.egress-denied`, which stays owned by `m2-sandbox-adapter-conformance`. Verify `timeout 120s go -C adapter/docker test -timeout 2m ./... -run TestEgressDenyAll`.
  - evidence: `adapter/docker/egress.go` (deny-all network mode); `adapter/docker/egress_test.go` (`TestEgressDenyAll`)
- [ ] 3.2 Implement the allowlist path with the shared private-range and hop rules: a per-workspace bridge network with a default-deny egress chain permitting only `Egress.Allow` hosts, `DenyPrivate` blocking RFC1918/link-local and `AllowPrivate` not, matching `std/egress`. This is the adapter half of `sandbox.egress-allowlisted` and `sandbox.egress-shares-policy-type`. Verify `timeout 120s go -C adapter/docker test -timeout 2m ./... -run 'TestEgressAllowlist|TestEgressPrivateRanges'`.
  - evidence: `adapter/docker/egress.go` (bridge network and egress chain); `adapter/docker/egress_test.go` (`TestEgressAllowlist`, `TestEgressPrivateRanges`)
- [ ] 3.3 Report the refusal through the shared observation seam so the governed path appends `stores.AuditEgressDenied` (`core/tool_call.go`, ADR-0156); assert no metric name (the counter-name reconciliation is the conformance change's open item O1). Verify `timeout 120s go -C adapter/docker test -timeout 2m ./... -run TestEgressDenialObserved`.
  - evidence: `adapter/docker/recorder.go` (`Recorder.Denied`, `Recorder.Kinds`); `adapter/docker/egress_test.go` (`TestEgressDenialObserved`)

## 4. Process timeout

- [ ] 4.1 Honour `MaxExecTime` (D6): run the exec under the deadline, kill the process group inside the container on expiry, and return the `ExecResult` rather than hang. This is the adapter half of `sandbox.exec-timeout`; the effect-keyed classification stays with the harness. Verify `timeout 120s go -C adapter/docker test -timeout 2m ./... -run TestExecTimeoutKill`.
  - evidence: `adapter/docker/workspace.go` (`Exec` deadline and process-group kill); `adapter/docker/timeout_test.go` (`TestExecTimeoutKill`)

## 5. Snapshot contract

- [ ] 5.1 Implement `Snapshot` (D5) as `ContainerCommit` with `NoPause: true` and a deterministic labelled reference, returning the image ref as `types.WorkspaceRef`. Verify `timeout 120s go -C adapter/docker test -timeout 2m ./... -run TestSnapshotCommit`.
  - evidence: `adapter/docker/snapshot.go` (`Snapshot`, `ContainerCommit`); `adapter/docker/snapshot_test.go` (`TestSnapshotCommit`)
- [ ] 5.2 Prove snapshot survival: after `Close` removes the container and after a daemon restart, `Open` on the ref creates a container from the image and the previously written files are present. Verify `timeout 300s go -C adapter/docker test -timeout 10m ./... -run TestSnapshotSurvivesClose`.
  - evidence: `adapter/docker/snapshot_test.go` (`TestSnapshotSurvivesClose`); the recorded daemon-restart observation
- [ ] 5.3 Prove the missing-snapshot contract: `Open` on a ref whose image does not exist returns an error naming the ref and opens no fresh workspace; a fresh workspace is created only for an empty ref. Verify `timeout 120s go -C adapter/docker test -timeout 2m ./... -run TestSnapshotMissing`.
  - evidence: `adapter/docker/snapshot_test.go` (`TestSnapshotMissing`)
- [ ] 5.4 Implement labelled retention: a snapshot image is enumerable by label (`ImageList`) and removed only by an explicit `ImageRemove`; record the retention rule (explicit, not time-based). Verify `timeout 120s go -C adapter/docker test -timeout 2m ./... -run TestSnapshotRetention`.
  - evidence: `adapter/docker/snapshot.go` (label scheme and retention); `adapter/docker/snapshot_test.go` (`TestSnapshotRetention`)

## 6. Durable enumeration and close-by-resource-identity

- [ ] 6.1 Declare the optional `types.SandboxResources` interface in `core/types` beside `WorkspaceLister` (D7): `Enumerate(ctx) ([]WorkspaceRef, error)` and `CloseByRef(ctx, WorkspaceRef) error`. It adds no method to `Sandbox` or `Workspace`. Verify `timeout 300s go build ./...`.
  - evidence: `core/types/sandbox.go` (`SandboxResources`)
- [ ] 6.2 Implement `Enumerate` and `CloseByRef` in the adapter: label-filtered `ContainerList`/`ImageList` and `ContainerRemove`/`ImageRemove`. Prove a labelled container left by a simulated dead pod is enumerated and closed by the next pod, and a live handle is not closed. Verify `timeout 120s go -C adapter/docker test -timeout 2m ./... -run TestEnumerateCloseByRef`.
  - evidence: `adapter/docker/resources.go` (`Enumerate`, `CloseByRef`); `adapter/docker/resources_test.go` (`TestEnumerateCloseByRef`)
- [ ] 6.3 Record that this supplies the Docker provider contract for `m2-sandbox-lifecycle-resume` task 6.5 (durable enumeration and close by resource identity) and that the E2B leg stays with `m2-e2b-sandbox`; the lifecycle change still owns the sweep wiring. Verify the lane report names both the supplied contract and the E2B boundary.
  - evidence: the lane acceptance report; `openspec/changes/m2-sandbox-lifecycle-resume/tasks.md` task 6.5

## 7. Conformance binding against the live daemon

- [x] 7.1 Add `adapter/docker/sandbox_conformance_test.go` — the single binding file that constructs the adapter `Factory` (`Sandbox`, `Probe`, `Recorder`) and calls `sandboxtest.Sandbox(t, factory)`, behind a skip guard so the short `task test` does not require a daemon. Verify `timeout 300s go -C adapter/docker vet ./...` and `timeout 120s go -C adapter/docker test -timeout 2m ./... -run TestSandboxConformance -skip-guard`.
  - evidence: `adapter/docker/sandbox_conformance_test.go` (the binding); `adapter/docker/factory.go` (`Factory`, `Probe`, `Recorder`)
- [x] 7.2 Run the suite unchanged against the live daemon and confirm all four owned ids pass as subtests named exactly their id: `sandbox.egress-denied`, `sandbox.egress-allowlisted`, `sandbox.exec-timeout`, `sandbox.egress-shares-policy-type`. These ids are owned by `m2-sandbox-adapter-conformance`; the adapter defines none. Verify `timeout 600s go -C adapter/docker test -timeout 10m ./... -run TestSandboxConformance` is green with the live daemon and the output names all four ids.
  - evidence: the observed run output naming the four ids; the daemon version/OS recorded
- [x] 7.3 Confirm the adapter keeps no private copy of an owned id and the binding is one file: `adapter/docker` contains exactly one `_test.go` file that imports `testkit/sandboxtest`, and no `t.Run("<owned id>")` appears outside the suite. Verify `grep -c 'sandboxtest.Sandbox' adapter/docker/sandbox_conformance_test.go` is 1 and `grep -rn 'sandbox.egress-denied\|sandbox.egress-allowlisted\|sandbox.exec-timeout\|sandbox.egress-shares-policy-type' adapter/docker` matches no test name.
  - evidence: the grep output; `adapter/docker/sandbox_conformance_test.go`
- [ ] 7.4 Record the Docker leg as unblocked for `m2-sandbox-adapter-conformance` task 5.3 and report the observed run as the provider evidence, without closing the E2B leg (task 5.4). Verify the lane report names the Docker leg observed green and the E2B leg still blocked.
  - evidence: the lane acceptance report; `openspec/changes/m2-sandbox-adapter-conformance/tasks.md` tasks 5.3 and 5.4

## 8. Lane acceptance

- [ ] 8.1 Run the touched-module checks: `timeout 600s task lint`, `timeout 300s go -C adapter/docker test -timeout 2m ./...` and `timeout 300s go build ./...`. Resolve every failure in the code this change touched. Verify all three exit zero.
  - evidence: `task lint` 0 issues; the adapter module test run green; `go build ./...` clean
- [ ] 8.2 Confirm the conformance run matches all four owned ids with a non-zero count and reject a query that matches zero tests: `timeout 600s go -C adapter/docker test -json -timeout 10m ./... -run TestSandboxConformance | python3 tools/spec_coverage.py --registry openspec/scenarios.json` lists the four ids as covered from the Docker leg. Verify the coverage output per owned id.
  - evidence: the coverage output per owned id; the observed run
- [ ] 8.3 Validate the change package. Verify `timeout 30s openspec validate m2-docker-sandbox --strict --no-interactive` reports `is valid`.
  - evidence: `timeout 30s openspec validate m2-docker-sandbox --strict --no-interactive`
