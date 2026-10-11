# Proposal: m2-e2b-sandbox

## Why

`openspec/changes/m2/design.md` §2.3 names `adapter/e2b` as the remote sandbox adapter, the lane matrix lists it as `| adapter/e2b | m2-e2b-sandbox | missing | |` (`openspec/changes/m2/design.md:144`; `docs/design/architecture.md:149` lists `e2b/  Sandbox`), and `openspec/changes/m2/tasks.md:42` requires the shared adapter suite to pass unchanged against both adapters. `m2-sandbox-adapter-conformance` task 5.4 keeps the E2B leg blocked until `adapter/e2b` lands with provider evidence, and ADR-0168 records the same lane as blocked (`docs/adr/0168-sandbox-lifecycle-coordinator-fences-workspace-metadata.md:108`).

Facts verified in the worktree this session:

- **No `adapter/e2b` module.** `go.work` lists eight `use` entries (`.`, `./adapter/anthropic`, `./adapter/docker`, `./adapter/eino`, `./adapter/openai`, `./adapter/otel`, `./adapter/postgres`, `./examples`); none is E2B. `Taskfile.yml` enumerates each adapter module explicitly in `spec:coverage`, `spec:gate`, `test` and `test:race` (`Taskfile.yml:23-24,40-41,84-85,107-108`).
- **No E2B credential, endpoint or URL anywhere.** No `E2B_API_KEY`, `E2B_API_URL`, `E2B_DOMAIN` or sandbox URL in the repository or the environment, and no E2B dependency in any `go.mod`. Nothing in this tree can reach a live E2B sandbox today.
- **No official E2B Go SDK.** `e2b-dev/E2B` issue #985 requests one and is open; the only Go client is unofficial (`github.com/matiasinsaurralde/go-e2b`, MIT, `v0.1.2`, published 2026-04-04; `pkg.go.dev` reports the module is not at its latest version).
- **E2B's documented shape.** Control plane REST on `https://api.e2b.app` with `X-API-Key`: `POST /v2/sandboxes` (template, timeout, `network`, `metadata`, `envVars`, `autoPause`), `POST /v2/sandboxes/{id}/connect` (resumes a paused sandbox), `GET /sandboxes` (filter by `metadata` and `state`: `running`, `paused`; cursor pagination via `nextToken`), `PUT /sandboxes/{id}/network` (`allowOut`, `denyOut`, `allow_internet_access`, `egressProxy`); the data plane is the in-sandbox envd daemon. `allow_internet_access: false` is documented as equivalent to denying all outbound traffic. A snapshot is created from a live sandbox, the sandbox is paused while the snapshot is created, the snapshot is persistent and survives sandbox deletion, and a new sandbox is created from it by passing the snapshot id where a template id goes.

`adapter/docker` is the pattern this change mirrors: the same module shape, the same frozen port, the same one-file conformance binding against `testkit/sandboxtest`, and the same rule that the adapter owns no scenario id.

## What Changes

- Add the Go module `adapter/e2b` (`github.com/victorzhuk/gohan/adapter/e2b`) mirroring `adapter/docker`: its own `go.mod` with a `replace`-free dependency on the root module, one `use` line in `go.work`, and the E2B leg in the explicit module lists in `Taskfile.yml`.
- Build the client in-house: the control plane over `net/http` against the documented v2 REST API, the data plane over the sandbox's envd API. No third-party E2B client is adopted; the unofficial Go client is recorded as the rejected alternative with its facts.
- Implement `types.Sandbox.Open(ctx, WorkspaceRef, SandboxPolicy) (Workspace, error)` and the six-method `types.Workspace` (`core/types/sandbox.go:69-85`): `Open` creates a sandbox from the configured template or from a snapshot ref, `Exec` runs argv through envd, `Put`/`Get` move files, `SetEnv` is the injection path, `Snapshot` creates an E2B snapshot, `Close` kills the sandbox this workspace opened.
- Enforce `types.EgressPolicy` at E2B's sandbox network layer: empty `Egress` creates the sandbox with `allow_internet_access: false`; a non-empty allowlist configures `allowOut`/`denyOut` at create time and through `PUT /sandboxes/{id}/network`; private-range rules are expressed as the provider's rule entries. The rule grammar is not documented to the level the port needs, so the allowlist and private-range tasks stay blocked behind a live-provider confirmation (design O2).
- Kill the process group inside the sandbox at `MaxExecTime` and return the `ExecResult` rather than hang; the provider-level command timeout is set above `MaxExecTime` so the adapter's kill is the one that fires.
- Establish the snapshot contract for the E2B leg: `Snapshot` creates an E2B snapshot (which pauses the sandbox), the returned `WorkspaceRef` is the snapshot id, snapshots survive sandbox deletion, `Open` on an unknown ref returns an error naming the ref and opens no fresh workspace, and retention is explicit rather than time-based.
- Supply the E2B leg of durable enumeration and close-by-resource-identity: implement the optional `types.SandboxResources` and `types.WorkspaceResource` interfaces (`core/types/sandbox.go:85-105`) by listing provider sandboxes that carry the adapter metadata and killing one by its identity, so the lifecycle change's killed-pod cleanup case has an E2B provider as well as the Docker one (`m2-sandbox-lifecycle-resume` task 6.5; no method is added to `Sandbox` or `Workspace`).
- Add the conformance binding `adapter/e2b/sandbox_conformance_test.go` — one `_test.go` file supplying a `Factory` (and its `NetworkFixtureProvider` companion) and calling `sandboxtest.Sandbox(t, factory)`. The binding task stays blocked until E2B credentials and provider evidence exist; this change claims no green run without them.

No spec delta: the adapter implements the frozen `sandbox` port and runs the existing suite, so no requirement text and no scenario id changes (`skip_specs: true`).

Explicitly out of scope: `adapter/docker` (`m2-docker-sandbox`, landed), the two examples, S4, and any change to `std/sandbox`, to the frozen `Sandbox`/`Workspace` method sets, or to `openspec/specs/sandbox/spec.md`.

## Capabilities

### New Capabilities

- (none)

### Modified Capabilities

- (none)

The adapter implements the existing `sandbox` capability contract rather than introducing a new behavioral capability, and it changes no requirement: the port, the policy, the four adapter scenario ids and the lifecycle scenario ids are already stated in `openspec/specs/sandbox/spec.md` and owned by `m2-sandbox-governance`, `m2-sandbox-adapter-conformance` and `m2-sandbox-lifecycle-resume`. This change owns no scenario id and invents none; it therefore sets `skip_specs: true`, the pattern `m2-docker-sandbox` and `m2-sandbox-adapter-conformance` use.

## Impact

Affected code: a new module `adapter/e2b` (`go.mod`, the REST control-plane client, the envd data-plane client, the `Sandbox`/`Workspace` implementation, the optional resource interface implementations, and the single conformance binding `_test.go`), `go.work` (one `use` line) and `Taskfile.yml` (the E2B leg in the four explicit module lists). No exported contract outside the new module changes; the frozen `Sandbox` and `Workspace` method sets do not move (`docs/design/compatibility.md:16`).

Dependencies: `m2-sandbox-governance` (shipped: the frozen port and `std/sandbox`), `testkit/sandboxtest` (`m2-sandbox-adapter-conformance`, whose E2B leg task 5.4 this change unblocks once evidence exists), and `m2-sandbox-lifecycle-resume` (whose task 6.5 E2B leg this change supplies and whose owner keeps the sweep wiring).

Unmet admission: no E2B credential or endpoint exists in this repository or environment, so no live provider evidence can be established while authoring or at first apply. Every task that needs a live sandbox is marked blocked and stays unchecked until a run is observed; the E2B conformance leg is reported blocked, never assumed.

No release tag: `api:check` skips an untagged module (`Taskfile.yml`), and `m2-api-freeze` cuts `v1.0.0` at the M2 exit.
