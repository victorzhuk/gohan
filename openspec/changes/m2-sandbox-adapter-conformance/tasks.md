# Tasks: m2-sandbox-adapter-conformance

A task is done when its listed scenario IDs have a passing subtest named exactly the ID and its `verify:` command is green. The suite drives the public path — `types.Sandbox.Open` and the six `Workspace` methods, plus `std/sandbox.New` over the same adapter and the tool-call path for classification and observation. The suite itself builds an in-memory fake `Sandbox` in its own `_test.go` files, never exported, and reaches no container, socket, network or credential. Every owned ID is an existing normative scenario in `openspec/specs/sandbox/spec.md`; no scenario ID is invented.

The four owned IDs are `sandbox.egress-denied`, `sandbox.egress-allowlisted`, `sandbox.exec-timeout` and `sandbox.egress-shares-policy-type` (`openspec/changes/m2/design.md` §2.3). The other eleven sandbox IDs stay with their owners: `m2-sandbox-governance`, `m2-sandbox-lifecycle-resume`, `m2-docker-sandbox` and `m2-e2b-sandbox`. This change carries no spec delta (`skip_specs: true`); it proves behavior the `sandbox` capability already states.

The Docker and E2B legs of this suite stay blocked until `m2-docker-sandbox` and `m2-e2b-sandbox` land with provider evidence (design O3). This change's own acceptance is the suite passing against the in-package fake.

## 1. The `testkit/sandboxtest` package and its factory seam

- [x] 1.1 Create `testkit/sandboxtest` beside `testkit/storetest` and `testkit/conformance`. Declare the `Factory` seam (`Sandbox(t) types.Sandbox`, `Probe() Probe`, `Recorder() Recorder`), the provider-neutral `Probe` vocabulary (`Dial(host, port)`, `Sleep(d)`, `Echo(s)`) and the `Recorder` observation sink (`Denied()`, `Kinds()`, `Reset()`), per design D2. The package imports `core/types`, `core/stores`, `core/chains` and `std/sandbox`; it is never imported by production code, `core` or `std`. Verify `timeout 300s go build ./testkit/...` and `timeout 120s go test -timeout 2m ./testkit/sandboxtest/... -run TestFactorySeam`.
  - evidence: `testkit/sandboxtest/sandboxtest.go` (`Factory`, `Probe`, `Recorder`, `Sandbox`); `testkit/sandboxtest/seam_test.go` (`TestFactorySeam`)
- [x] 1.2 Declare the suite entry point `Sandbox(t *testing.T, f Factory)` that opens through `types.Sandbox.Open` and closes through `Workspace.Close`, so the adapter supplies no lifecycle and the suite owns it. Verify `timeout 120s go test -timeout 2m ./testkit/sandboxtest/... -run TestSuiteEntrypoint`.
  - evidence: `testkit/sandboxtest/sandboxtest.go` (`Sandbox`); `testkit/sandboxtest/seam_test.go` (`TestSuiteEntrypoint`)
- [x] 1.3 Add the in-package fake `Sandbox` and fake `Workspace` used by the suite's own tests: the fake records `Open`, `Exec` and `Close`, enforces the `EgressPolicy` it is opened with at a fake network seam, and kills the process group at `MaxExecTime`. The fake is unexported and lives in `_test.go` files. Verify `timeout 120s go test -timeout 2m ./testkit/sandboxtest/... -run TestFakeAdapter`.
  - evidence: `testkit/sandboxtest/fake_test.go` (fake `Sandbox` and `Workspace`); `testkit/sandboxtest/fake_test.go` (`TestFakeAdapter`)

## 2. Egress conformance

- [x] 2.1 Implement the `sandbox.egress-denied` subtest, named exactly its ID: open a workspace with `types.SandboxPolicy{Egress: types.EgressPolicy{}}`, run the `Dial` probe, and assert the call fails inside the workspace (non-zero exit or connection error), and that the refusal is observed through `Recorder.Denied()` and `Recorder.Kinds()` (`stores.AuditEgressDenied`). Assert nothing against a metric name that is not emitted; the counter-name reconciliation is design O1. Verify `timeout 120s go test -timeout 2m ./testkit/sandboxtest/... -run 'TestSuiteEntrypoint/sandbox.egress-denied'`.
  - evidence: `testkit/sandboxtest/egress_test.go` (`TestSuiteEntrypoint/sandbox.egress-denied`)
- [x] 2.2 Implement the `sandbox.egress-allowlisted` subtest, named exactly its ID: open with `Egress.Allow: []string{"api.example.com"}`, assert the `Dial` probe to `api.example.com` succeeds and a `Dial` probe to another host is denied and observed. Verify `timeout 120s go test -timeout 2m ./testkit/sandboxtest/... -run 'TestSuiteEntrypoint/sandbox.egress-allowlisted'`.
  - evidence: `testkit/sandboxtest/egress_test.go` (`TestSuiteEntrypoint/sandbox.egress-allowlisted`)
- [x] 2.3 Assert the adapter's private-range rule at the same seam: a `Dial` probe into a private address range from inside a workspace whose `Egress.PrivateRanges` is `DenyPrivate` is denied, with a positive control that the same probe under `AllowPrivate` is not refused by policy (`core/types/tool.go:50-56,59-67`). Verify `timeout 120s go test -timeout 2m ./testkit/sandboxtest/... -run 'TestSuiteEntrypoint/sandbox.egress-allowlisted/private-range'`.
  - evidence: `testkit/sandboxtest/egress_test.go` (private-range case and its positive control)

## 3. Process timeout conformance

- [x] 3.1 Implement the `sandbox.exec-timeout` subtest, named exactly its ID: open a workspace with a short `MaxExecTime`, run the `Sleep` probe past that bound, and assert the process group is killed — `Exec` returns rather than hangs, the kill is reported in `ExecResult.ExitCode`, and no probe process survives the call. Verify `timeout 120s go test -timeout 2m ./testkit/sandboxtest/... -run 'TestSuiteEntrypoint/sandbox.exec-timeout'`.
  - evidence: `testkit/sandboxtest/timeout_test.go` (`TestSuiteEntrypoint/sandbox.exec-timeout`)
- [x] 3.2 Assert the effect-keyed outcome at the harness: build the tool set with `std/sandbox.New` over the same adapter, drive the `run_code` call through the tool-call path, and assert the outcome is `Unknown` with `Error.Kind: OutcomeUnknown` under a `SideEffect` policy (`Egress.Allow` non-empty, mirroring `std/sandbox/effect.go:16-21`) and `Failed` otherwise, matching `std/tool/exec/exec.go:170-192`. Read the derived effect from the tool set; do not re-derive it in the suite. Verify `timeout 120s go test -timeout 2m ./testkit/sandboxtest/... -run 'TestSuiteEntrypoint/sandbox.exec-timeout/outcome'`.
  - evidence: `testkit/sandboxtest/timeout_test.go` (outcome case, both policies)

## 4. Policy-shape conformance

- [x] 4.1 Implement the `sandbox.egress-shares-policy-type` subtest, named exactly its ID: assert at compile time that `types.SandboxPolicy.Egress` and `types.ToolSpec.Egress` are the identical `types.EgressPolicy` type (a `var _ types.EgressPolicy = types.SandboxPolicy{}.Egress` check and the reverse), so no second egress struct exists in `std/sandbox`. Verify `timeout 300s go build ./testkit/...` and `timeout 120s go test -timeout 2m ./testkit/sandboxtest/... -run 'TestSuiteEntrypoint/sandbox.egress-shares-policy-type/shared-type'`.
  - evidence: `testkit/sandboxtest/policy_test.go` (shared-type assertion)
- [ ] 4.2 Assert the rendered policy shape: a sandbox tool set built with `Egress: types.EgressPolicy{Allow: []string{"api.example.com"}}` and a harness tool carrying the same policy render the same shape through `Explain` (`core/explain.go:26`, `core/chains/explain.go:38-62`), and a `Dial` into a private range from inside the workspace is denied. This task is gated on the Explain policy-shape slice (design O2): if `chains.ToolInfo` (`core/chains/explain.go:38-41`) gains no egress rendering, this task stays unchecked and the gap is reported, not worked around with a private shape. Verify `timeout 120s go test -timeout 2m ./testkit/sandboxtest/... -run 'TestSuiteEntrypoint/sandbox.egress-shares-policy-type'`.
  - evidence: `testkit/sandboxtest/policy_test.go` (`TestSuiteEntrypoint/sandbox.egress-shares-policy-type`); the O2 slice's outcome for `core/chains/explain.go`

## 5. Coverage, adapter bindings and provider legs

- [x] 5.1 Register the suite in the scenarios coverage run and confirm the four owned IDs each match a non-zero count and no zero-match id passes as acceptance. Verify `timeout 300s go test -json -timeout 2m -short ./testkit/sandboxtest/... | python3 tools/spec_coverage.py --registry openspec/scenarios.json` lists `sandbox.egress-denied`, `sandbox.egress-allowlisted`, `sandbox.exec-timeout` and `sandbox.egress-shares-policy-type` as covered.
  - evidence: the coverage output per owned ID
- [x] 5.2 Document the binding contract for an adapter: one `_test.go` file importing `testkit/sandboxtest`, supplying a `Factory`, and calling `sandboxtest.Sandbox(t, factory)`. Record in the lane report that `m2-docker-sandbox` and `m2-e2b-sandbox` own the binding files and that no adapter may keep a private copy of an owned ID. Verify the doc text names both adapters and both binding files.
  - evidence: `testkit/sandboxtest/README.md` or the package doc comment (binding contract); the lane report
- [ ] 5.3 Run the suite against `adapter/docker` once `m2-docker-sandbox` has landed an adapter and provider evidence exists; the Docker leg stays blocked until then. Keep this checkbox unchecked and report the leg as blocked, not passed, until a real adapter run is observed. Verify the binding file exists and `timeout 600s go -C adapter/docker test -timeout 10m ./... -run 'TestSandboxConformance'` is green with a real adapter.
  - evidence: `adapter/docker` conformance binding file; the observed run
- [ ] 5.4 Run the suite against `adapter/e2b` once `m2-e2b-sandbox` has landed an adapter and provider evidence exists; the E2B leg stays blocked until then. Keep this checkbox unchecked and report the leg as blocked, not passed, until a real adapter run is observed. Verify the binding file exists and `timeout 600s go -C adapter/e2b test -timeout 10m ./... -run 'TestSandboxConformance'` is green with a real adapter.
  - evidence: `adapter/e2b` conformance binding file; the observed run

## 6. Lane acceptance

- [x] 6.1 Run the touched-module checks: `timeout 600s task lint`, `timeout 300s go test -timeout 2m ./testkit/...` and `timeout 300s go build ./...`. Resolve every failure in the code this change touched. Verify all three exit zero.
  - evidence: `task lint` 0 issues; `go test ./testkit/...` green; `go build ./...` clean
- [x] 6.2 Confirm each owned scenario ID has a passing subtest named exactly the ID against the in-package fake, and reject a query that matches zero tests. Verify `timeout 300s go test -timeout 2m ./testkit/sandboxtest/... -run 'sandbox'` reports all four owned IDs passing. Record the Docker and E2B legs and the O1/O2 open items as unmet in the lane acceptance report rather than closing them.
  - evidence: the four-ID run output; the lane acceptance report listing the blocked provider legs and open items O1, O2
- [x] 6.3 Validate the change package. Verify `timeout 30s openspec validate m2-sandbox-adapter-conformance --strict --no-interactive` reports `is valid`.
  - evidence: `timeout 30s openspec validate m2-sandbox-adapter-conformance --strict --no-interactive`
