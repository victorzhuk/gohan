# Tasks: m2-e2b-sandbox

A task is done when its `verify:` command is green and `task lint` and `task test` are green for what it touched. This change owns no scenario id: the four adapter ids — `sandbox.egress-denied`, `sandbox.egress-allowlisted`, `sandbox.exec-timeout` and `sandbox.egress-shares-policy-type` — stay owned once by `m2-sandbox-adapter-conformance`, and the lifecycle rows this change serves (`sandbox.snapshot-on-suspend`, `sandbox.resume-restores`, `sandbox.per-session-persists`, `sandbox.secret-injected`) stay owned by `m2-sandbox-lifecycle-resume` and `m2-sandbox-governance`. A task below that names an owned id names it as the suite subtest or normative row the adapter must satisfy, not as a locally-defined test. Every id referenced is an existing normative scenario in `openspec/specs/sandbox/spec.md`; no scenario id is invented.

**Blocking fact for every live task.** No E2B credential, API URL or sandbox domain exists in this repository or the environment (verified 2026-10-12), and no E2B dependency exists in any `go.mod`. Tasks marked **blocked** MUST stay unchecked until a live E2B sandbox run is observed; they are reported blocked, never reported passed. A blocked task is still authored and reviewed: its shape, evidence line and unblock condition are stated, so the work starts the moment credentials exist.

## 1. Module shape and workspace wiring

- [ ] 1.1 Create the module `adapter/e2b` (`go.mod`, module `github.com/victorzhuk/gohan/adapter/e2b`) mirroring `adapter/docker`: a `replace`-free dependency on the root module `github.com/victorzhuk/gohan v0.1.0` and no third-party E2B dependency (design D1). Verify `timeout 300s go -C adapter/e2b build ./...`.
  - evidence: `adapter/e2b/go.mod` (root dependency, no E2B client); `adapter/e2b/go.sum`
- [ ] 1.2 Add `use ./adapter/e2b` to `go.work` and the E2B leg to the explicit module lists in `Taskfile.yml` (`spec:coverage`, `spec:gate`, `test`, `test:race`), matching the existing adapter lines. Verify `timeout 300s go work sync` and `timeout 300s task --list` exit zero.
  - evidence: `go.work` (`use ./adapter/e2b`); `Taskfile.yml` (the E2B leg in the four lists)
- [ ] 1.3 Wire configuration: API key from `E2B_API_KEY` or an explicit config value sent as `X-API-Key`, base URL from `E2B_API_URL` or the documented default `https://api.e2b.app`, sandbox domain from `E2B_SANDBOX_URL`/`E2B_DOMAIN`, and a fail-fast error when no credential is configured. Verify `timeout 300s go -C adapter/e2b build ./...` and `timeout 120s go -C adapter/e2b test -timeout 2m ./... -run TestClientConfig`.
  - evidence: `adapter/e2b/client.go` (config resolution and fail-fast); `adapter/e2b/client_test.go` (`TestClientConfig`)

## 2. Control-plane client and data-plane spike

- [ ] 2.1 **(blocked: no E2B credential or sandbox domain exists in this repo or environment, verified 2026-10-12)** Run the bounded data-plane spike (design D1, O4) against one live sandbox: record the envd endpoints, payload shapes, streaming and kill semantics the adapter uses, and prove `Exec`, `Put` and `Get` through them. Record in the lane notes whether the in-house envd path is kept or the pinned unofficial Go client is wrapped behind the same adapter surface. Verify `timeout 300s go -C adapter/e2b test -timeout 10m ./... -run TestEnvDSpike` against a live sandbox.
  - evidence: `adapter/e2b/envd.go` (the recorded endpoints); `adapter/e2b/envd_spike_test.go` (`TestEnvDSpike`); the recorded spike notes naming the client path chosen
- [ ] 2.2 Implement the control-plane client over `net/http`: `POST /v2/sandboxes`, `POST /v2/sandboxes/{id}/connect`, the pause and kill calls, `GET /sandboxes` (metadata and state filters, `nextToken` paging), `PUT /sandboxes/{id}/network`, with typed errors for not-found, unauthorized and conflict so a missing ref is distinguishable from a transport failure. Verify `timeout 300s go -C adapter/e2b build ./...` and `timeout 120s go -C adapter/e2b test -timeout 2m ./... -run TestControlPlaneClient`.
  - evidence: `adapter/e2b/client.go` (the six documented calls and typed errors); `adapter/e2b/client_test.go` (`TestControlPlaneClient`, request/response fixtures)

## 3. The `Sandbox` and `Workspace` implementation over E2B sandboxes

- [ ] 3.1 Implement `Open` (D2): an empty `WorkspaceRef` creates a sandbox from the configured template with the adapter metadata, the initial `envVars` and the network configuration per D4; a non-empty ref creates a sandbox from the snapshot and a not-found response returns an error naming the ref. Verify `timeout 300s go -C adapter/e2b build ./...` and `timeout 120s go -C adapter/e2b test -timeout 2m ./... -run TestOpen`.
  - evidence: `adapter/e2b/sandbox.go` (`Open`, create call, ref resolution); `adapter/e2b/sandbox_test.go` (`TestOpen`)
- [ ] 3.2 **(blocked: needs a live sandbox)** Implement `Exec` (D3): argv, working directory, stdin, accumulated environment and deadline over envd, returning `types.ExecResult{ExitCode, Stdout, Stderr, Truncated, Seconds}` — a non-zero exit is a result, a transport failure is an error. Verify `timeout 120s go -C adapter/e2b test -timeout 2m ./... -run TestExec`.
  - evidence: `adapter/e2b/workspace.go` (`Exec`); `adapter/e2b/workspace_test.go` (`TestExec`)
- [ ] 3.3 **(blocked: needs a live sandbox)** Implement `Put`/`Get` over the envd filesystem calls and `SetEnv` as the only injection path — the accumulated map is passed to every subsequent exec, serving the injection rule behind `sandbox.secret-injected` (owned by `m2-sandbox-governance`) — plus `Close` as the kill of the sandbox this workspace opened, idempotent for an already-killed sandbox. Verify `timeout 120s go -C adapter/e2b test -timeout 2m ./... -run 'TestPutGet|TestSetEnv|TestClose'`.
  - evidence: `adapter/e2b/workspace.go` (`Put`, `Get`, `SetEnv`, `Close`); `adapter/e2b/workspace_test.go` (`TestPutGet`, `TestSetEnv`, `TestClose`)

## 4. Egress enforcement in the provider's network layer

- [ ] 4.1 **(blocked: needs a live sandbox)** Implement the deny-all path (D4): an empty `Egress` creates the sandbox with `allow_internet_access: false`, so the dial probe and name resolution fail inside the workspace. This is the adapter half of `sandbox.egress-denied`, which stays owned by `m2-sandbox-adapter-conformance`. Verify `timeout 120s go -C adapter/e2b test -timeout 2m ./... -run TestEgressDenyAll` against a live sandbox.
  - evidence: `adapter/e2b/egress.go` (deny-all network configuration); `adapter/e2b/egress_test.go` (`TestEgressDenyAll`)
- [ ] 4.2 **(blocked: needs a live sandbox and the O2 rule-grammar confirmation)** Implement the allowlist path (D4): a non-empty `Egress.Allow` is expressed through `network.allowOut` at create time and through `PUT /sandboxes/{id}/network` on a running sandbox, with the complete rule set sent because the provider replaces rules rather than merging. This is the adapter half of `sandbox.egress-allowlisted`. The task stays blocked until the live provider confirms whether a non-empty `allowOut` implies default-deny; if it does not, the gap is reported to `m2-sandbox-adapter-conformance` rather than papered over with a rule shape the provider does not support (O2). Verify `timeout 120s go -C adapter/e2b test -timeout 2m ./... -run TestEgressAllowlist` against a live sandbox.
  - evidence: `adapter/e2b/egress.go` (allowlist configuration and the network update); `adapter/e2b/egress_test.go` (`TestEgressAllowlist`); the recorded O2 confirmation
- [ ] 4.3 **(blocked: needs a live sandbox and the O2/O3 confirmations)** Implement the private-range half (D4): `PrivateRanges: DenyPrivate` denies RFC1918 and link-local destinations and `AllowPrivate` does not, expressed through the provider's documented rule entries. This is the adapter half of `sandbox.egress-shares-policy-type`, which also depends on the fixture mismatch recorded in O3; the E2B leg is reported blocked until the grammar and a reachable provider-controlled private endpoint exist. Verify `timeout 120s go -C adapter/e2b test -timeout 2m ./... -run TestEgressPrivateRanges` against a live sandbox.
  - evidence: `adapter/e2b/egress.go` (private-range rules); `adapter/e2b/egress_test.go` (`TestEgressPrivateRanges`); the recorded O2/O3 findings
- [ ] 4.4 Report the refusal through the shared observation seam so the governed path appends `stores.AuditEgressDenied` (`core/tool_call.go`, ADR-0156); assert no metric name (the counter-name reconciliation is the conformance change's open item O1). Verify `timeout 120s go -C adapter/e2b test -timeout 2m ./... -run TestEgressDenialObserved`.
  - evidence: `adapter/e2b/recorder.go` (`Recorder.Denied`, `Recorder.Kinds`); `adapter/e2b/egress_test.go` (`TestEgressDenialObserved`)

## 5. Process timeout

- [ ] 5.1 **(blocked: needs a live sandbox)** Honour `MaxExecTime` (D5): run the exec under the deadline, kill the process group inside the sandbox on expiry, set the provider-level command timeout strictly above `MaxExecTime` so the adapter's kill is the one that fires, and return the `ExecResult` rather than hang. This is the adapter half of `sandbox.exec-timeout`; the effect-keyed classification stays with the harness. Verify `timeout 120s go -C adapter/e2b test -timeout 2m ./... -run TestExecTimeoutKill` against a live sandbox.
  - evidence: `adapter/e2b/workspace.go` (exec deadline, process-group kill, provider timeout); `adapter/e2b/timeout_test.go` (`TestExecTimeoutKill`)

## 6. Snapshot contract in E2B semantics

- [ ] 6.1 **(blocked: needs a live sandbox)** Implement `Snapshot` (D6) as an E2B snapshot of the sandbox's current state, returning the snapshot id as `types.WorkspaceRef`, and resume the live handle through `connect` before a subsequent exec because the provider pauses the sandbox while the snapshot is created and leaves it paused. Verify `timeout 120s go -C adapter/e2b test -timeout 2m ./... -run TestSnapshotCreate` against a live sandbox.
  - evidence: `adapter/e2b/snapshot.go` (`Snapshot`, snapshot call, resume-before-exec); `adapter/e2b/snapshot_test.go` (`TestSnapshotCreate`)
- [ ] 6.2 **(blocked: needs a live sandbox)** Prove snapshot survival: after `Close` kills the sandbox, `Open` on the returned ref creates a new sandbox and the previously written files are present. This is the E2B half of `sandbox.snapshot-on-suspend` and `sandbox.resume-restores`, which stay owned by `m2-sandbox-lifecycle-resume`. Verify `timeout 300s go -C adapter/e2b test -timeout 10m ./... -run TestSnapshotSurvivesClose` against a live sandbox.
  - evidence: `adapter/e2b/snapshot_test.go` (`TestSnapshotSurvivesClose`); the recorded observation that the killed sandbox's snapshot still restores
- [ ] 6.3 **(blocked: needs a live sandbox)** Prove the missing-ref contract: `Open` on a ref that is not a snapshot returns an error naming the ref and opens no fresh workspace; a fresh workspace is created only for an empty ref. Verify `timeout 120s go -C adapter/e2b test -timeout 2m ./... -run TestSnapshotMissing`.
  - evidence: `adapter/e2b/snapshot_test.go` (`TestSnapshotMissing`)
- [ ] 6.4 **(blocked: needs a live sandbox)** Record snapshot retention: the provider holds a snapshot until it is removed, the adapter removes one only when told (explicit, not time-based), and `CloseByRef` never deletes a snapshot. Verify `timeout 120s go -C adapter/e2b test -timeout 2m ./... -run TestSnapshotRetention`.
  - evidence: `adapter/e2b/snapshot.go` (retention rule); `adapter/e2b/snapshot_test.go` (`TestSnapshotRetention`)

## 7. Durable enumeration and close-by-resource-identity

- [ ] 7.1 Implement `types.SandboxResources` and `types.WorkspaceResource` in the adapter (D7): `Enumerate` lists provider sandboxes carrying the adapter metadata in both `running` and `paused` states through `GET /sandboxes` with `nextToken` paging, `CloseByRef` kills the named sandbox and tolerates an already-killed one, and `ResourceRef` returns the live sandbox's identity. No method is added to `Sandbox` or `Workspace`. Verify `timeout 300s go -C adapter/e2b build ./...` and `timeout 120s go -C adapter/e2b test -timeout 2m ./... -run TestResources`.
  - evidence: `adapter/e2b/resources.go` (`Enumerate`, `CloseByRef`, `ResourceRef`); `adapter/e2b/resources_test.go` (`TestResources`)
- [ ] 7.2 **(blocked: needs a live sandbox)** Prove reclamation: a sandbox left by a simulated dead pod is enumerated by the next pod and killed by `CloseByRef`, and a live handle is not closed. Verify `timeout 120s go -C adapter/e2b test -timeout 2m ./... -run TestEnumerateCloseByRef` against a live sandbox.
  - evidence: `adapter/e2b/resources_test.go` (`TestEnumerateCloseByRef`)
- [ ] 7.3 Record that this supplies the E2B provider contract for `m2-sandbox-lifecycle-resume` task 6.5 (durable enumeration and close by resource identity), complementing the Docker leg `m2-docker-sandbox` supplies; the lifecycle change still owns the sweep wiring. Verify the lane report names the supplied contract and the lifecycle owner.
  - evidence: the lane acceptance report; `openspec/changes/m2-sandbox-lifecycle-resume/tasks.md` task 6.5

## 8. Conformance binding, blocked until credentials and provider evidence exist

- [ ] 8.1 Add `adapter/e2b/sandbox_conformance_test.go` — the single binding file that constructs the adapter `Factory` (`Sandbox`, `Probe`, `Recorder`) and its `NetworkFixtureProvider` companion and calls `sandboxtest.Sandbox(t, factory)`, behind a skip guard so the short `task test` needs no credentials. Verify `timeout 300s go -C adapter/e2b vet ./...` and `timeout 120s go -C adapter/e2b test -timeout 2m ./... -run TestSandboxConformance` skips cleanly with no credential configured.
  - evidence: `adapter/e2b/sandbox_conformance_test.go` (the binding); `adapter/e2b/factory.go` (`Factory`, `Probe`, `Recorder`, `NetworkFixtureProvider`)
- [ ] 8.2 **(blocked until an E2B credential, the reconciled network fixture and provider evidence exist — none present on 2026-10-12; keep this checkbox unchecked and report the leg as blocked, not passed)** Run the suite unchanged against a live E2B sandbox and confirm all four owned ids pass as subtests named exactly their id: `sandbox.egress-denied`, `sandbox.egress-allowlisted`, `sandbox.exec-timeout`, `sandbox.egress-shares-policy-type`. These ids are owned by `m2-sandbox-adapter-conformance`; the adapter defines none. Verify `timeout 600s go -C adapter/e2b test -timeout 10m ./... -run TestSandboxConformance` is green with a live credential and the output names all four ids.
  - evidence: the observed run output naming the four ids; the endpoint and API version recorded
- [ ] 8.3 Confirm the adapter keeps no private copy of an owned id and the binding is one file: `adapter/e2b` contains exactly one `_test.go` file that imports `testkit/sandboxtest`, and no `t.Run("<owned id>")` appears outside the suite. Verify `grep -c 'sandboxtest.Sandbox' adapter/e2b/sandbox_conformance_test.go` is 1 and `grep -rn 'sandbox.egress-denied\|sandbox.egress-allowlisted\|sandbox.exec-timeout\|sandbox.egress-shares-policy-type' adapter/e2b` matches no test name.
  - evidence: the grep output; `adapter/e2b/sandbox_conformance_test.go`
- [ ] 8.4 **(blocked: same condition as 8.2)** Record the E2B leg as unblocked for `m2-sandbox-adapter-conformance` task 5.4 and report the observed run as the provider evidence; until then report the leg blocked and name the exact blocker (credential, fixture reconciliation or rule-grammar confirmation). Verify the lane report names the E2B leg state and the blocker.
  - evidence: the lane acceptance report; `openspec/changes/m2-sandbox-adapter-conformance/tasks.md` task 5.4

## 9. Lane acceptance

- [ ] 9.1 Run the checks that need no credential: `timeout 600s task lint`, `timeout 300s go -C adapter/e2b test -timeout 2m ./...` and `timeout 300s go build ./...`. Resolve every failure in the code this change touched. Verify all three exit zero.
  - evidence: `task lint` 0 issues; the adapter module test run green with the live tasks skipped; `go build ./...` clean
- [ ] 9.2 **(blocked: same condition as 8.2)** Confirm the conformance run matches all four owned ids with a non-zero count and reject a query that matches zero tests: `timeout 600s go -C adapter/e2b test -json -timeout 10m ./... -run TestSandboxConformance | python3 tools/spec_coverage.py --registry openspec/scenarios.json` lists the four ids as covered from the E2B leg. Verify the coverage output per owned id.
  - evidence: the coverage output per owned id; the observed run
- [ ] 9.3 Validate the change package. Verify `timeout 30s openspec validate m2-e2b-sandbox --strict --no-interactive` reports `is valid`.
  - evidence: `timeout 30s openspec validate m2-e2b-sandbox --strict --no-interactive`

## 10. Lane report

- [ ] 10.1 Report the lane: the authored package, the task block, the blocked tasks with their exact unblock condition, the four owned ids this adapter must pass and their owner (`m2-sandbox-adapter-conformance`), the E2B leg of the lifecycle provider contract, and open items O1-O5. Report no green conformance run and no provider evidence; the E2B leg is blocked.
  - evidence: the lane acceptance report naming the blocked legs, the owners and O1-O5
