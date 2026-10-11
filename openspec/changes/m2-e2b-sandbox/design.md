# Design: m2-e2b-sandbox

## Context

`adapter/e2b` is the remote sandbox adapter the `sandbox` capability names and the lane matrix lists (`openspec/changes/m2/design.md:144`; `docs/design/architecture.md:149`). The port and the governed tool set shipped from `2026-10-07-m2-sandbox-governance`; the suite and its `Factory` seam shipped from `m2-sandbox-adapter-conformance`; `adapter/docker` is the landed pattern (`m2-docker-sandbox`). Facts verified in the worktree this session:

- The port under test is `types.Sandbox.Open(ctx, WorkspaceRef, SandboxPolicy) (Workspace, error)` with the six-method `Workspace` `Exec/Put/Get/SetEnv/Snapshot/Close`, and the optional companions `WorkspaceLister`, `SandboxResources` (`Enumerate`, `CloseByRef`), `WorkspaceResource` (`ResourceRef`) and `WorkspaceOwner` live beside it (`core/types/sandbox.go:69-118`). `SandboxPolicy.Egress` is `types.EgressPolicy` with `Allow []string` and `PrivateRanges` (`DenyPrivate`/`AllowPrivate`) (`core/types/tool.go`).
- The suite is `sandboxtest.Sandbox(t, f Factory)`; `Factory` supplies `Sandbox(t *testing.T) types.Sandbox`, `Probe() Probe` and `Recorder() Recorder`; `Probe` is `Dial(host string, port int) []string`, `Sleep(d time.Duration) []string`, `Echo(s string) []string`; `Recorder` is `Denied() int`, `Kinds() []stores.AuditKind`, `Reset()`; the optional companion `NetworkFixtureProvider` supplies `NetworkFixture(t) NetworkFixture{PrivateHost, PrivatePort}` (`testkit/sandboxtest/sandboxtest.go:17-73`). The binding is one `_test.go` file that calls `sandboxtest.Sandbox(t, factory)`; `adapter/docker/sandbox_conformance_test.go` is the pattern.
- `go.work` lists eight `use` entries and no E2B module; `Taskfile.yml` enumerates each adapter module explicitly in `spec:coverage`, `spec:gate`, `test` and `test:race`. The four adapter-owned scenario ids (`sandbox.egress-denied` `:120`, `sandbox.egress-allowlisted` `:125`, `sandbox.exec-timeout` `:189`, `sandbox.egress-shares-policy-type` `:204` in `openspec/specs/sandbox/spec.md`) are owned once by `m2-sandbox-adapter-conformance`; the lifecycle rows this change serves (`sandbox.snapshot-on-suspend`, `sandbox.resume-restores`, `sandbox.per-session-persists`, `sandbox.replay-no-reexec`) are owned by `m2-sandbox-lifecycle-resume`. This change owns no scenario id.
- **No E2B access exists.** No `E2B_API_KEY`, `E2B_API_URL`, `E2B_DOMAIN`, sandbox URL, endpoint or credential appears in the repository or the environment, and no `go.mod` carries an E2B dependency. This is the blocking admission fact for every live task below.
- **E2B semantics, from the provider's documentation.** Control plane `https://api.e2b.app`, `X-API-Key`: `POST /v2/sandboxes` (required `templateID`; optional `timeout`, `autoPause`, `allow_internet_access`, `network{allowPublicTraffic, allowOut, denyOut, egressProxy, maskRequestHost, httpsPorts, rules}`, `metadata`, `envVars`); `POST /v2/sandboxes/{id}/connect` (returns sandbox details and resumes a paused sandbox; TTL only extended); `GET /sandboxes` (filters `metadata`, `state` ∈ {`running`, `paused`}, `template`, `startedAfter`; cursor `nextToken`, `limit` ≤ 100; each row carries `sandboxID`, `state`, `startedAt`, `endAt`, `metadata`); `PUT /sandboxes/{id}/network` (`allowOut`, `denyOut`, `egressProxy`, `allow_internet_access`; replaces rules rather than merging; create-only options cannot be changed later). Sandbox states are Running, Paused, Killed; `kill` works from any state. Data plane: the in-sandbox envd daemon. Snapshots: created from a live sandbox, **the sandbox is paused while the snapshot is created**, the snapshot is persistent and survives sandbox deletion, and a new sandbox is created from it by passing the snapshot id where a template id goes.
- **No official E2B Go SDK.** `e2b-dev/E2B` issue #985 is open. The only Go client is unofficial (`github.com/matiasinsaurralde/go-e2b`, MIT, `v0.1.2`, published 2026-04-04, and `pkg.go.dev` reports the module is not at its latest version).

## Goals / Non-Goals

**Goals:** a new `adapter/e2b` module on the frozen port; an in-house REST control-plane client and an envd data-plane path; `Open` create-or-restore; `Exec`/`Put`/`Get`/`SetEnv` over envd; E2B-native `EgressPolicy` enforcement including deny-all; a process-group kill at `MaxExecTime`; a snapshot contract stated in E2B's own semantics; the E2B leg of durable enumeration and close-by-resource-identity; a one-file conformance binding that is blocked, not claimed, until credentials and provider evidence exist.

**Non-Goals:** no change to the frozen `Sandbox`/`Workspace` method sets; no change to `std/sandbox` behavior (effect derivation, secret redaction, output caps and the budget stay with their owners); no change to `openspec/specs/sandbox/spec.md` (`skip_specs: true`); no new scenario id; no adoption of an unofficial third-party E2B client into a security boundary; no `adapter/docker` work; no examples; no S4; no release tag.

## Admission

Four provider facts, none of them established, and one that is establishable only after purchase:

- **Credentials and endpoint — absent.** No E2B API key, API URL or sandbox domain exists in the repository or the environment (verified 2026-10-12). No E2B task in this package can be exercised. Every task that needs a live sandbox is marked blocked and stays unchecked until a run is observed; the lane reports the E2B conformance leg as blocked, not as passed.
- **Data-plane transport — unverified.** E2B's command and filesystem traffic goes over the in-sandbox envd daemon, which no Go client implements officially. The exact envd endpoints, payload shapes and streaming behavior are not asserted here; a bounded spike records them against one live sandbox (D1, O4).
- **Network rule grammar — unverified.** The port needs deny-all, an allowlist and a private-range rule. E2B documents `allow_internet_access: false` as deny-all-outbound, and `allowOut`/`denyOut` as destination lists, but the documentation does not state whether a non-empty `allowOut` implies default-deny, what a `denyOut` entry may be (host pattern, CIDR, port), or how RFC1918/link-local destinations are expressed. The allowlist and private-range halves of the adapter therefore stay blocked behind a live confirmation (O2), and no rule shape is invented.
- **Conformance fixture — a cloud-provider mismatch to reconcile.** `NetworkFixtureProvider` requires a live RFC1918 endpoint the provider controls and normative hostnames that resolve to provider-controlled listeners without public DNS (`testkit/sandboxtest/sandboxtest.go:29-40`). On the Docker leg the host's own address supplies that. An E2B cloud sandbox sits in the provider's network, so an RFC1918 endpoint of ours is not reachable from inside it, while the suite's private-range case needs both a denial under `DenyPrivate` and a positive control under `AllowPrivate`. This change records the mismatch and its two bounded answers — a self-hosted E2B deployment, or an amendment to the fixture companion's definition of a private endpoint owned by `m2-sandbox-adapter-conformance` — and does not invent a fixture (O3). An E2B run without a reconciled fixture fails construction, which is the suite's designed behavior, not a silent skip.
- **Snapshot enumeration — unverified.** Snapshot creation, persistence across sandbox deletion and create-from-snapshot are documented. The provider's listing surface for snapshots (as opposed to sandboxes) is not verified here, so `Enumerate` is specified over sandboxes carrying the adapter metadata (D7) and snapshot retention is specified as explicit (D6).

## Decisions

### D1 — Module shape and the client decision

`adapter/e2b` is a separate module `github.com/victorzhuk/gohan/adapter/e2b`, mirroring `adapter/docker`: its own `go.mod` with a `replace`-free dependency on the root module `github.com/victorzhuk/gohan v0.1.0`, one `use ./adapter/e2b` line in `go.work`, and the E2B leg in the explicit module lists in `Taskfile.yml` (`spec:coverage`, `spec:gate`, `test`, `test:race`).

No official Go SDK exists (`e2b-dev/E2B#985` open), and the only Go client is unofficial and off-latest-version. **This module therefore implements its own client and adopts no third-party E2B package into a security boundary:**

- **Control plane** over `net/http` against the documented v2 REST API: `POST /v2/sandboxes`, `POST /v2/sandboxes/{id}/connect`, pause, `DELETE` kill, `GET /sandboxes`, `PUT /sandboxes/{id}/network`, base URL from `E2B_API_URL` or the documented default, API key from `E2B_API_KEY` or an explicit config value, sent as `X-API-Key`. Errors are mapped to typed adapter errors (not-found, unauthorized, conflict) so `Open` on a missing ref is distinguishable from a transport failure.
- **Data plane** over the sandbox's envd daemon at the sandbox domain (`E2B_SANDBOX_URL` / `E2B_DOMAIN`), for `Exec`, `Put` and `Get`.

**Gate.** A bounded spike task (task 2.1) records the envd endpoints, payload shapes and kill semantics it uses and proves `Exec`/`Put`/`Get` against one live sandbox, and its outcome is recorded in the lane ADR. If envd's protocol proves prohibitive for an in-house client, the fallback is the pinned unofficial Go client wrapped under the same adapter surface — a client swap behind `types.Sandbox`, not a port or binding change. The decision is recorded either way; nothing silently changes shape.

### D2 — `Open` creates a sandbox or restores one from a snapshot ref

```go
// adapter/e2b — illustrative; the port shapes are fixed by core/types.
func (s *Sandbox) Open(ctx context.Context, ref types.WorkspaceRef, p types.SandboxPolicy) (types.Workspace, error)
```

`Open` is the only factory. With an empty `ref` it creates a sandbox: `POST /v2/sandboxes` with the configured `templateID`, the sandbox `timeout` derived from the run limits, `envVars` for the policy's initial set, `metadata` carrying the adapter's resource labels (adapter name, run id, session id) and the network configuration per D4. With a non-empty `ref` it creates a sandbox from the snapshot: the ref is passed where the create call takes a template selector, which is how the provider documents create-from-snapshot (a snapshot id in the template position). A create for an unknown ref maps the provider's not-found response to an error naming the ref and opens no fresh workspace, matching the missing-snapshot rule `m2-sandbox-lifecycle-resume` states.

The workspace handle holds the sandbox id, the resolved sandbox URL, the envd client and the accumulated environment. The ref stays opaque to the harness; the adapter owns the snapshot-id scheme.

### D3 — `Exec` runs argv over envd; a non-zero exit is a result, a transport failure is an error

`Workspace.Exec` maps `types.ExecRequest{Argv, Stdin, Cwd, Timeout}` onto an envd command run: argv, working directory, stdin, the accumulated environment from `SetEnv`, and a deadline. It returns `types.ExecResult{ExitCode, Stdout, Stderr, Truncated, Seconds}`, matching the port's rule that a non-zero exit is a result and the adapter's failure to start the process is the error (`2026-10-07-m2-sandbox-governance` D1.2). `Seconds` is measured around the call. Output beyond `ToolSpec.MaxOutput` is the harness's cap: the adapter returns the bytes and `Truncated=false`, and the governance layer caps and spills.

`Put`/`Get` use the envd filesystem calls for one path each. `SetEnv` records the map on the workspace and passes it to every subsequent exec, because the provider sets environment at sandbox creation; the create-time `envVars` carries the initial set and `SetEnv` is the only path a resolved secret takes into the workspace. `Close` kills the sandbox (`Close` is a terminal state in the provider's model), once, and is idempotent for an already-killed sandbox.

### D4 — Egress is enforced in E2B's own network layer, and the rule grammar is confirmed live before either half is claimed

`std/sandbox` passes `Egress` through and interprets no allowlist; enforcement is the adapter's obligation. E2B owns the enforcement mechanism, so the adapter configures the sandbox's network:

- **Empty `Egress` (deny-all, including DNS)** — the sandbox is created with `allow_internet_access: false`, which the provider documents as equivalent to denying all outbound traffic, so the dial probe and name resolution fail inside the workspace and `Exec` returns a non-zero result.
- **Non-empty `Egress.Allow`** — the sandbox is created with the allowlist expressed through `network.allowOut`, and a running sandbox is tightened through `PUT /sandboxes/{id}/network` (which replaces rules rather than merging, so the adapter sends the complete rule set).
- **`PrivateRanges`** — `DenyPrivate` must deny RFC1918 and link-local destinations and `AllowPrivate` must not, with the same private-range and hop rules `std/egress` applies. E2B documents no dedicated private-range knob, so this half is expressed through the provider's `denyOut`/`allowOut` entries once their grammar is established (O2).

The refusal is reported through the shared observation seam so the governed path can classify it and append `stores.AuditEgressDenied` (`core/tool_call.go`, ADR-0156); the adapter asserts no metric name (conformance O1). One refusal observation per affected exec, no double counting between provider-observed packet refusals and classified in-workspace failures; a process failure or an unrelated timeout does not establish egress denial.

### D5 — `MaxExecTime` kills the process group and `Exec` returns

`Exec` runs under the deadline from `SandboxPolicy`/`ExecRequest.Timeout`, and on expiry kills the process group inside the sandbox — the exec process and its children, not the sandbox — and returns the `ExecResult` rather than hanging. The provider-level command timeout is configured strictly above `MaxExecTime`, so the adapter's kill is the one that fires and the result is attributable; a provider timeout that pre-empts it would make the kill indistinguishable. The harness classifies the effect (`Unknown` under a `SideEffect` policy, `Failed` otherwise), the same mapping `std/tool/exec` implements.

### D6 — Snapshot is an E2B snapshot; pause, survival, missing ref and retention are stated in the provider's terms

`Workspace.Snapshot` creates a snapshot of the sandbox's current state and returns the snapshot id as `types.WorkspaceRef`. E2B's semantics shape three adapter obligations:

- **The sandbox is paused while the snapshot is created, and stays paused afterwards.** A subsequent `Exec` on the same live handle must therefore resume first; the adapter resumes (`POST /v2/sandboxes/{id}/connect`) before an exec when the handle is paused, so the port's `Exec` contract holds. The harness's snapshot ordering is already serial to exec (the suspension or terminal path), so the common case does not resynchronize.
- **Survival.** A snapshot is persistent and survives sandbox deletion, which is exactly the durability the lifecycle change needs for `sandbox.snapshot-on-suspend` and `sandbox.resume-restores`: `Close` may kill the sandbox afterwards, and `Open` on the ref builds a new sandbox with the same filesystem and state. This is the E2B answer to the question R6 left open for the Docker leg.
- **Missing ref and retention.** A create-from-snapshot for an unknown id is an error naming the ref, never a fresh workspace; a fresh workspace is created only for an empty ref. Snapshots are provider-held and persist until removed; the adapter removes one only when told, so retention is explicit and not time-based.

The paused-sandbox path (`connect` resumes a paused sandbox with its state) is available to the lifecycle layer but is not the port's ref: a paused sandbox expires with its TTL, so the durable object the harness stores is the snapshot.

### D7 — Durable enumeration and close-by-resource-identity, in E2B's terms

`m2-sandbox-lifecycle-resume` task 6.5 needs a provider that enumerates durable resources and closes one by its identity. The optional interfaces already exist beside the frozen port and are reached by type assertion, so nothing is added to `Sandbox` or `Workspace` (`core/types/sandbox.go:85-105`):

- `Enumerate(ctx) ([]WorkspaceRef, error)` lists provider sandboxes that carry the adapter's metadata (`GET /sandboxes` filtered by the adapter metadata, both `running` and `paused` states, paging through `nextToken`), returning each sandbox's resource identity. A sandbox left by a dead pod is therefore visible to the next pod with no help from the dead process.
- `CloseByRef(ctx, ref) error` kills the sandbox named by the ref and tolerates an already-killed one.
- `WorkspaceResource.ResourceRef()` returns the live sandbox's identity so the harness can record which resource a handle owns.

The ref used on this path is the live resource identity, not a snapshot id: `Enumerate` does not return snapshots, and `CloseByRef` never deletes a snapshot, because snapshot retention is explicit (D6). The lifecycle change owns the sweep and the wiring; the adapter only supplies the interface.

### D8 — The conformance binding is one file, the adapter owns no scenario id, and the package carries no spec delta

`adapter/e2b/sandbox_conformance_test.go` is the only binding file: it constructs the adapter's `Factory` (`Sandbox`, `Probe`, `Recorder`) and the `NetworkFixtureProvider` companion, and calls `sandboxtest.Sandbox(t, factory)`. It sits behind a skip guard so the short lane needs no credentials, and the full leg requires a live E2B credential: `go -C adapter/e2b test -timeout 10m ./... -run TestSandboxConformance`.

The four adapter ids — `sandbox.egress-denied`, `sandbox.egress-allowlisted`, `sandbox.exec-timeout`, `sandbox.egress-shares-policy-type` — stay owned once by `m2-sandbox-adapter-conformance`; the lifecycle ids this change serves stay owned by `m2-sandbox-lifecycle-resume`. This change defines no id locally and keeps no private copy: a second copy of an owned id is a review defect. The package declares `skip_specs: true` because no requirement text changes.

### D9 — Acceptance split, and what this change may claim

1. **Authoring acceptance** — the package exists, names only existing normative ids, states the blocked tasks with their blocker, and `openspec validate m2-e2b-sandbox --strict` reports valid. This is what can be claimed today.
2. **Static adapter acceptance** — the module builds, the client fails fast without credentials, and the binding file compiles and skips in short mode. Claimable without credentials.
3. **Live adapter acceptance (blocked)** — the suite passes unchanged against a live E2B sandbox and the four owned ids pass as subtests named exactly their ids. Blocked until credentials, a reconciled network fixture and provider evidence exist; reported as blocked, never as passed.
4. **Provider evidence to record when unblocked** — the endpoint used, the API version observed, a snapshot created, the sandbox killed, `Open` on the ref restoring the files, `Open` on an unknown ref erroring, and `Enumerate`/`CloseByRef` reclaiming a sandbox left by a simulated dead pod.

No historical result, socket presence or credential presence establishes any leg by itself.

## Risks / Trade-offs

- [Risk] Writing an in-house client for a provider with no Go SDK means protocol drift the provider's own SDK would absorb. → Mitigated: the REST control plane is documented and small; the envd data plane is bounded by spike task 2.1, and the fallback (pin the unofficial Go client behind the same adapter surface) is named rather than improvised.
- [Risk] E2B's snapshot pauses the sandbox, so a live handle can be left paused mid-run. → Mitigated: the adapter resumes before exec, and the harness's snapshot ordering is serial to exec; the behavior is stated rather than discovered.
- [Risk] E2B network rules may not express the port's private-range rule, or `allowOut` may not imply default-deny. → Accepted and bounded: the two halves stay blocked behind a live confirmation (O2); nothing is claimed and no rule shape is invented.
- [Risk] The suite's `NetworkFixtureProvider` cannot supply an RFC1918 fixture inside the provider's cloud. → Mitigated by recording the mismatch and its two answers (self-hosted E2B, or an amendment owned by `m2-sandbox-adapter-conformance`); an unreconciled fixture fails construction, which is the suite's designed signal.
- [Risk] Paused sandboxes expire with their TTL, so a handle-based resume could outlive its object. → Accepted: the durable ref is the snapshot, which survives sandbox deletion; the paused path is not the ref.
- [Trade-off] The adapter delegates isolation to E2B and keeps `std/sandbox` provider-neutral. → Accepted: it is the provider's job, and it keeps one suite valid for Docker, E2B and a later Kubernetes adapter.

## Open items

- **O1 — Denial counter name.** Inherited from `m2-sandbox-adapter-conformance` design O1: `gohan.sandbox.egress_denied` (normative scenario) versus `gohan.egress.requests{decision=denied}` + `AuditEgressDenied` (shipped, ADR-0156). The adapter reports through the observation seam and asserts no metric name; the reconciliation stays with the conformance change.
- **O2 — E2B network rule grammar.** Whether a non-empty `allowOut` implies default-deny, what a `denyOut` entry may be, and how RFC1918/link-local destinations are expressed. Needs one live confirmation before the allowlist and private-range tasks leave blocked.
- **O3 — Cloud fixture shape.** The suite's private-range control needs a provider-controlled RFC1918 endpoint a cloud sandbox can reach. Needs either a self-hosted E2B deployment or an amendment to the fixture companion's definition, owned by `m2-sandbox-adapter-conformance`.
- **O4 — Data-plane client path.** The envd protocol coverage the spike establishes, and whether the in-house path or the pinned unofficial client is kept; recorded in the lane ADR either way.
- **O5 — Sandbox TTL versus run budget.** The create `timeout` interacts with `RunLimits.MaxSandboxSeconds`; the adapter derives it from the run limits and records the observed behavior rather than assuming a mapping.

## Migration Plan

No data migration. The change adds a module, one `use` line in `go.work`, the E2B legs in `Taskfile.yml`, the adapter with its two transport clients, the optional resource implementations, and one conformance binding file. No exported contract outside the new module changes; the frozen `Sandbox`/`Workspace` method sets do not move. Keep the module pre-v1 and create no release tag while authoring; `m2-api-freeze` cuts `v1.0.0` at the M2 exit. Until credentials exist, the module ships buildable with its live tasks blocked and its conformance leg reported blocked.
