# Design: m2-sandbox-adapter-conformance

## Context

The `sandbox` capability splits cleanly: `std/sandbox` ships the governed tool set and derives the effect (`std/sandbox/effect.go:16-21`), while isolation and network enforcement are the adapter's obligation (`openspec/specs/sandbox/spec.md:77`; `openspec/changes/archive/2026-10-07-m2-sandbox-governance/design.md:72,101`). `m2-sandbox-governance` proved the governance half with an in-package fake `Sandbox` and said so as a risk: "an in-package fake `Sandbox` could drift from a real adapter's behaviour, so a green scenario proves the governance and not the isolation" (`…-governance/design.md:135`). This change closes that half with one suite an adapter must pass unchanged.

Facts the suite builds on, all verified in the worktree:

- The port under test is `types.Sandbox.Open(ctx, WorkspaceRef, SandboxPolicy) (Workspace, error)` with the six-method `Workspace` (`core/types/sandbox.go:69-85`). `Egress` on `SandboxPolicy` is `types.EgressPolicy` (`core/types/sandbox.go:29`; `core/types/tool.go:59-67`), the same type a harness tool carries in `ToolSpec.Egress` (`core/types/tool.go:97`).
- The governed tool set is `std/sandbox.New(sb types.Sandbox, p types.SandboxPolicy, opts ...Option) (ToolSet, error)` (`std/sandbox/sandbox.go:101`). Effect derivation is unchanged and stays with governance.
- The timeout outcome mapping the suite asserts already exists for host commands and is the pattern the sandbox adapter mirrors: `timeoutResult` maps a `SideEffect` command to `Unknown` with `Error.Kind: OutcomeUnknown` and everything else to `Failed` (`std/tool/exec/exec.go:170-192`).
- `testkit/` holds the conformance kits: `storetest` (a factory-injected port suite, `testkit/storetest/session_log.go:1-31`), `gohantest` (fakes) and `conformance` (runtime suites imported by adapters' `_test.go` files, e.g. `adapter/eino/conformance_test.go:6`). `testkit` is part of the root module and may import `core` and `std` (the depguard budget rule covers `**/core/**` and `**/std/**` only, `.golangci.yml:8-16`).

Four ids are owned here and stay authoritative in `openspec/specs/sandbox/spec.md`: `sandbox.egress-denied` (`:96-100`), `sandbox.egress-allowlisted` (`:101-104`), `sandbox.egress-shares-policy-type` (`:163-166`) and `sandbox.exec-timeout` (`:170-174`). All four are registered with `origin: ADR-0093`/`R55` and `deferred_to: M2` (`openspec/scenarios.json`).

## Goals / Non-Goals

**Goals:** one shared `testkit/sandboxtest` suite; a provider-neutral factory seam; four subtests named exactly their owned ids; a deny-all egress probe, an allowlist probe, a `MaxExecTime` process-group kill with the effect-keyed outcome, and a policy-shape check; a suite that the Docker and E2B adapters import unchanged.

**Non-Goals:** no `adapter/docker` or `adapter/e2b` code (separate changes); no provider execution in this change's acceptance; no `Sandbox`/`Workspace` port change; no `std/sandbox` behavior change; no change to `openspec/specs/sandbox/spec.md` (this change changes no requirement, so `skip_specs: true`); no new scenario id invented from prose; no reopening of `m2-sandbox-lifecycle-resume` R6.

## Admission

Two seam questions are open. They are recorded as open items rather than settled by a guess, and the suite is designed so neither blocks the parts that are provable today.

- **Provider leg.** No installed module provides a sandbox, so no green run of the suite against a real adapter is possible in this change. The Docker and E2B legs are dependencies (`m2-docker-sandbox`, `m2-e2b-sandbox`) and stay blocked until those adapters land and provider evidence exists. This change's own acceptance is the suite compiling and passing against an in-memory fake `Sandbox` that lives in the suite's own `_test.go` files and is never exported. Docker-socket presence, E2B credentials or a `-tags full` run do not establish a provider contract.
- **R6 stays parked.** `m2-sandbox-lifecycle-resume` design R6 records that no installed provider establishes snapshot survival, retention or close-by-resource-identity. That blocker is not this change's to resolve and is not reopened here. The suite asserts nothing that depends on snapshot durability; per-session persistence scenarios stay with their owning change.

## Decisions

### D1 — The suite is `testkit/sandboxtest`, imported by adapters; one suite, no private copies

The suite lives in `testkit/sandboxtest`, mirroring `testkit/storetest` and `testkit/conformance`. Adapters import it from `_test.go` files only, as `adapter/eino` imports `testkit/conformance` today (`adapter/eino/conformance_test.go:6`). The package is never imported by production code and never by `core` or `std`.

`m2-docker-sandbox` and `m2-e2b-sandbox` each add one binding `_test.go` file that supplies a `Factory` and calls `sandboxtest.Sandbox(t, factory)`. Neither adapter defines the owned scenario ids locally: the suite owns them once and a second copy is a review defect (`openspec/changes/m2/design.md` §2.3, "adapters implement the suite contract and claim no duplicate coverage").

### D2 — The `Factory` seam keeps the suite provider-neutral

The suite hard-codes no image, socket, region, key or CLI. The adapter supplies everything provider-specific through one value:

```go
// testkit/sandboxtest — illustrative names; the shapes below are what the
// suite needs and are fixed during authoring only as a contract sketch.
type Factory interface {
	// Sandbox returns a fresh adapter bound to no live workspace. The suite
	// opens workspaces through types.Sandbox.Open.
	Sandbox(t *testing.T) types.Sandbox
	// Probe declares the argv shapes the adapter can run inside a workspace.
	Probe() Probe
	// Recorder receives the observations the suite asserts and never a
	// provider secret.
	Recorder() Recorder
}

// Probe is the provider-neutral command vocabulary. An adapter that runs
// no interpreter cannot satisfy the suite and fails the binding test at
// construction rather than at assertion time.
type Probe interface {
	Dial(host string, port int) []string // opens one TCP connection, exits non-zero on refusal
	Sleep(d time.Duration) []string      // runs past MaxExecTime without finishing
	Echo(s string) []string              // exits zero, prints s
}

// Recorder is the observation sink: denial counts, cold-start observations
// and the audit kinds the governed path reported. It carries no URL, path,
// query, user-info or body (ADR-0156).
type Recorder interface {
	Denied() int
	Kinds() []stores.AuditKind
	Reset()
}
```

The `Probe` and `Recorder` shapes are the suite's contract with an adapter. Their exact names are settled when the suite is written; the requirement is only that the suite reaches the in-workspace network and process behavior through them and never constructs a provider resource itself.

### D3 — The four subtests are named exactly their normative ids

Each subtest is `t.Run("<scenario id>", …)` so a coverage run matches it (`tools/spec_coverage.py --registry openspec/scenarios.json`). The assertions:

- `sandbox.egress-denied` — with `SandboxPolicy{Egress: types.EgressPolicy{}}`, the dial probe leaves the workspace and fails inside it (non-zero exit or connection error) and the denial observation the spec names increments.
- `sandbox.egress-allowlisted` — with `Egress.Allow: []string{"api.example.com"}`, a dial to `api.example.com` succeeds and a dial to another host is denied.
- `sandbox.exec-timeout` — with `MaxExecTime` set short, the sleep probe is killed with its process group, `ExecResult.ExitCode` reports the kill, and the outcome the governed path reports is `Unknown` under a `SideEffect` policy and `Failed` otherwise, matching `std/tool/exec/exec.go:170-192`.
- `sandbox.egress-shares-policy-type` — a sandbox tool set built with `Egress: types.EgressPolicy{Allow: []string{"api.example.com"}}` and a harness tool carrying the same policy render the same policy shape, and a dial into a private range from inside the workspace is denied.

The suite drives the public path: `types.Sandbox.Open` → `Workspace.SetEnv`/`Exec`/`Put`/`Get`/`Close` for the adapter mechanics, and `std/sandbox.New` over the same adapter plus the tool-call path for the classification and observation assertions. It reaches no private field and asserts no provider internal.

### D4 — Egress enforcement is the adapter's obligation; the denial observation is the seam

`std/sandbox` derives the effect and passes `Egress` through; it interprets no allowlist and runs no matcher (`…-governance/design.md:76`). The adapter enforces `EgressPolicy` at the network layer with the same private-range and hop rules as `std/egress`, and it reports the refusal so the governed path can classify it (`…-governance/design.md:76`). The suite therefore asserts two things: the dial fails inside the workspace (adapter truth) and the refusal is observed (harness truth).

**Open item — the denial counter name.** The normative scenario says `gohan.sandbox.egress_denied` increments (`openspec/specs/sandbox/spec.md:79,99`). The shipped classification path counts `gohan.egress.requests{tool, host, decision}` and appends `stores.AuditEgressDenied` (`core/tool_call.go:183-196`, ADR-0156); no `gohan.sandbox.egress_denied` counter is emitted anywhere. Two normative sources disagree, so the design does not pick one silently. Until it is settled, the suite asserts the denial through `Recorder.Denied()` and `Recorder.Kinds()` — the shared observation seam — rather than a hard-coded metric name, and the open item records the exact reconciliation needed (emit `gohan.sandbox.egress_denied` for a sandbox denial, or amend the sandbox metric line under the ADR procedure). No count is asserted against a name that is not emitted.

### D5 — Timeout is the adapter's process-group kill; the classification is the harness's

`MaxExecTime` bounds one `Exec`. The adapter kills the process group inside the workspace; the harness classifies the result by effect (`…-governance/design.md:101`; `std/tool/exec/exec.go:170-192`). The suite asserts the kill at the adapter (the probe does not survive its deadline and `Exec` returns rather than hanging) and the effect-keyed outcome at the harness (`Unknown` under `SideEffect`, `Failed` otherwise). A `SideEffect` policy here means `Egress.Allow` is non-empty, which is the same derivation `std/sandbox/effect.go:16-21` applies, so the suite builds both policies through `std/sandbox.New` and reads the derived effect rather than re-deriving it.

### D6 — Policy shape: one type, one rendered shape

`SandboxPolicy.Egress` and `ToolSpec.Egress` are both `types.EgressPolicy` (`core/types/sandbox.go:29`; `core/types/tool.go:97`), so there is one vocabulary and no second egress struct anywhere in `std/sandbox` (`…-governance/design.md:76`). The suite asserts the shared type at compile time (the field types are identical) and the rendered shape at runtime.

**Open item — `Explain` policy-shape rendering.** The scenario's THEN clause says "`Explain` shows the same policy shape as a harness tool's egress" (`openspec/specs/sandbox/spec.md:163-166`). The shipped `chains.Explanation.Tools` carries `ToolInfo{Name, Executor}` and no egress field (`core/chains/explain.go:38-41`), so Explain cannot render a policy shape today. This is a bounded slice: `Explain` must render the resolved `EgressPolicy` for a sandbox tool set and a harness tool identically. The design records the observable and defers the exact field to the implementer; no field is declared during authoring. If rendering the policy shape proves to widen a frozen surface, the slice returns to the ADR procedure instead of shipping a private shape.

### D7 — No spec delta, and the acceptance split

This change adds a test kit and binds existing normative behavior; no requirement text and no scenario id changes, so the change sets `skip_specs: true` (the pattern `harden-workspace-security-gates` uses). Acceptance splits three ways and is stated so a reviewer does not read a green suite as provider readiness:

1. **This change's own acceptance** — the suite compiles, its four subtests pass against an in-memory fake `Sandbox` in the suite's own tests, and a coverage run matches all four ids with a non-zero count.
2. **Adapter acceptance (blocked)** — `adapter/docker` and `adapter/e2b` pass the suite unchanged. Blocked until `m2-docker-sandbox` and `m2-e2b-sandbox` land with provider evidence; not claimed here.
3. **Milestone** — `task spec:gate GATE=M2` is the milestone gate, reported separately; not a per-lane gate.

A scenario run that matches zero tests is not acceptance. No historical test result, socket presence or credential establishes any leg above.

## Risks / Trade-offs

- [Risk] The suite's fake `Sandbox` could itself drift from a real adapter, reproducing the governance change's own risk one level up. → Accepted and bounded: the fake exercises only the suite's own mechanics; the owned ids are green for real only when `m2-docker-sandbox` and `m2-e2b-sandbox` run the suite unchanged, and that leg stays blocked here.
- [Risk] The `Probe` vocabulary may not fit an adapter that runs no interpreter. → Accepted: such an adapter fails the binding test at construction, which is the correct signal that it cannot serve `run_code`; the vocabulary is the smallest that lets the suite reach the network and process behavior without naming a provider.
- [Risk] Two open seam items (D4 counter name, D6 Explain shape) could be read as resolved by a green suite. → Mitigated: the suite asserts through the observation seam and the shared type, and each open item names the normative source and the exact reconciliation, so no task closes one by touching the same code.
- [Trade-off] Keeping the suite provider-neutral means the adapter owns the probe command strings. → Accepted: that is what keeps one suite valid for Docker, E2B and a later Kubernetes adapter; a suite that baked in `python3 -c` would bind the harness to one runtime.

## Open items

- **O1 — Denial counter name.** `gohan.sandbox.egress_denied` (normative scenario) vs `gohan.egress.requests{decision=denied}` + `AuditEgressDenied` (shipped path, ADR-0156). Needs one reconciliation: emit the sandbox counter on a sandbox denial at the adapter or harness boundary, or amend the sandbox metric line under the ADR procedure. The suite asserts through `Recorder`, so nothing is blocked meanwhile.
- **O2 — `Explain` policy-shape rendering.** The scenario names it; `chains.ToolInfo` has no egress field. Needs a bounded slice to render the resolved `EgressPolicy` for both a sandbox tool set and a harness tool, or an ADR-level reconciliation if it widens a frozen surface.
- **O3 — Provider legs.** `m2-docker-sandbox` and `m2-e2b-sandbox` are required for a real green run; both are blocked until an adapter lands. `m2-sandbox-lifecycle-resume` R6 stays parked and is not reopened.
