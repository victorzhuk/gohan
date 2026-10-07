# Design: m2-sandbox-governance

## Context

The `sandbox` capability spec states the port, the policy and the model-facing tool set in its Contract block and eight numbered rules (openspec/specs/sandbox/spec.md:24-103), then restates them as five requirements with fifteen scenarios (openspec/specs/sandbox/spec.md:85-168). Nothing in the tree declares any of it: no `Sandbox` interface, no `Workspace`, no `std/sandbox`. `core/stores/checkpoint.go:31-34` carries a `WorkspaceRef` local to the `Checkpoints` port with a comment that the alias step replaces it with the shared type, and `docs/design/types.md:269-270,642` lists `Workspace`, `WorkspaceRef` and the port tier as belonging to `sandbox` — so the type index already expects this change to put them there.

This change is the first of the three sandbox lanes in the M2 matrix (openspec/changes/m2/design.md §2.3). The port and its governance land here; adapter conformance (`m2-sandbox-adapter-conformance`) and the persisted workspace lifecycle (`m2-sandbox-lifecycle-resume`) build on this one. It owns seven sandbox ids and one identity id.

## Goals / Non-Goals

**Goals:** Settle and declare the sandbox port and its policy types in `core/types`, with named-secret resolution and listing as optional interfaces beside the port; ship `std/sandbox` as one governed tool set; derive effects from the policy and lifecycle; resolve and redact secrets through the new `SecretResolver` and inject them through `Workspace.SetEnv`; cap outputs into the shipped `OutputStore`; run the `PerCall` lifecycle with a close-exactly-once guarantee; charge `MaxSandboxSeconds` per run tree; make the host runner's warning fire at `Build` as the requirement states; turn the eight owned scenarios green on the public path.

**Non-Goals:** No isolation technology. No `adapter/docker` and no `adapter/e2b` (separate changes); this change adds an in-memory fake only for the tests, in `_test.go` files, and never exports it. No network enforcement inside the workspace: the adapter enforces `EgressPolicy` at the network layer, and this change only passes the policy and classifies the resulting effect. No `PerSession` persistence: `Snapshot`, session metadata, `Checkpoint.Workspace`, the reaper and `Replay` belong to `m2-sandbox-lifecycle-resume`. No new store, no secret store implementation (the `SecretResolver` port ships with no in-tree implementation beyond the test stub), no token refresh loop, no `tool/exec` behaviour change beyond the registration warning, and no provider compaction.

## Decisions

### D1 — The port method set is settled here, because the v1 freeze lands at the M2 exit

`docs/overview.md:108-112` puts the API freeze at the M2 exit: every port that exists at the end of M2 is frozen at v1, and `m2-api-freeze` owns the compatibility check. `docs/design/compatibility.md:16` makes `Sandbox` and `Workspace` named ports that from `v1` never gain, lose or change a method. This change is the only M2 change that declares them, so the method set below is the milestone's highest-risk shape: an adapter written against it in `m2-docker-sandbox` and `m2-e2b-sandbox` and an outside implementation both bind to it for the rest of the project. The exact list, as the spec's Contract block states it and as `docs/design/types.md` already indexes:

```go
type Sandbox interface {
	Open(ctx context.Context, ref WorkspaceRef, p SandboxPolicy) (Workspace, error)
}

type Workspace interface {
	Exec(ctx context.Context, req ExecRequest) (ExecResult, error)
	Put(ctx context.Context, path string, r io.Reader) error
	Get(ctx context.Context, path string) (io.ReadCloser, error)
	SetEnv(ctx context.Context, env map[string]string) error
	Snapshot(ctx context.Context) (WorkspaceRef, error)
	Close(ctx context.Context) error
}

type SecretResolver interface {
	Resolve(ctx context.Context, p Principal, name string) (string, error)
}

type WorkspaceLister interface {
	List(ctx context.Context, path string) ([]WorkspaceEntry, error)
}

type WorkspaceEntry struct {
	Path string
	Size int64
	MIME string
}
```

Seven decisions inside that shape, each taken deliberately:

1. **`Open` takes a `WorkspaceRef`, not an empty string.** A `PerCall` call passes the zero `WorkspaceRef`; a `PerSession` session passes the ref restored from `Checkpoint.Workspace`. One signature covers both lifecycles, so the port does not gain an `OpenSession` method when the lifecycle change lands.
2. **`Exec` returns `(ExecResult, error)` with the process outcome inside `ExecResult`, not as the error.** A non-zero exit is a result the tool turns into an `Outcome`, and the adapter's failure to start the process is the error. A kill is `ExitCode` plus the caller's own timeout signal, not a distinguished error class.
3. **`Snapshot` is on `Workspace`, not on `Sandbox`.** A snapshot is a property of the open workspace; `Sandbox` stays a one-method factory. That is what lets `m2-sandbox-lifecycle-resume` add snapshot-on-suspend without a second factory method.
4. **`Close` takes a `context.Context` and returns only `error`.** No `Close(ctx, force bool)`: force-closing after a `MaxExecTime` kill is the adapter's own obligation inside `Exec`, and a second shape would be a method the freeze would keep forever.
5. **`Put`/`Get`/`SetEnv` are the file and environment surface; listing is an optional interface, never a `Workspace` method.** `WorkspaceLister` declares `List(ctx context.Context, path string) ([]WorkspaceEntry, error)` over a declared `WorkspaceEntry{Path, Size, MIME}` (`Entry` is already `stores.Entry`, the journal record, so the sandbox type takes its own name), and `list_files` reaches it by type assertion on the value `Open` returned. A workspace that does not implement `WorkspaceLister` makes `list_files` fail with `types.ErrWorkspaceListingRequired`; there is no partial listing and no fallback path. This is the compatibility rule, not a convenience: `docs/design/compatibility.md:16,19` says a port never gains a method and that growth from `v1` arrives as an optional interface whose absent-assertion behaviour the introducing spec states, and it is already real twice over (`stores.PreemptedLister` with `types.ErrSessionIndexRequired`, `MemoryStore` with `types.ErrMemoryStoreRequired`, core/types/errors.go:30,63). So the shape is settled here — `Workspace` stays exactly the six methods below, and `m2-docker-sandbox` and `m2-e2b-sandbox` bind to `WorkspaceLister` or do not list. Adding `List` to `Workspace` before the freeze would freeze a method neither lane needs and break every implementation already compiled against the six.
6. **`WorkspaceRef` moves to `core/types` and `core/stores` aliases it.** The `Checkpoints` port depends on the type, and the local declaration in `core/stores/checkpoint.go` says so in its own comment. One type, declared in `sandbox`, aliased where it is consumed, matching how the other capability types are exposed through `core/aliases.go`.
7. **`SetEnv` on `Workspace` is the injection method, and named-secret resolution is its own optional port.** `SetEnv(ctx, map[string]string) error` is the only way a resolved value reaches a workspace, and `Open` does not carry the environment because a policy names refs, not values. On the resolution side, `types.CredentialSource` cannot serve it: `Credentials(ctx, Principal) (Credential, error)` (core/types/credential.go:24-27) takes no secret name and returns one harness credential for the run, so a `SecretRef{Name: "warehouse"}` has nothing to call and a token would be the wrong value even if it did. This change therefore declares `types.SecretResolver` with `Resolve(ctx context.Context, p Principal, name string) (string, error)` — a second, distinct port for named values — and wires it through `Build` beside `WithCredentialSource` (core/build_options.go:147-151), so the harness resolves each `SecretRef.Name` at `Open` and injects the map through `SetEnv`. It is optional: no resolver means nothing is injected (D4).

The values around the port follow the spec exactly and are not open: `Lifecycle` (`PerCall`, `PerSession`), `SandboxPolicy{Lifecycle, Egress, CPU, Memory, Disk, MaxExecTime, Secrets}`, `SecretRef{Name, Env}`, `ExecRequest{Argv, Stdin, Cwd, Timeout}`, `ExecResult{ExitCode, Stdout, Stderr, Truncated, Seconds}`, `WorkspaceEntry{Path, Size, MIME}`. Two optional interfaces ship beside the ports and are discovered by type assertion, never asserted by embedding: `SecretResolver` and `WorkspaceLister` (D1.5, D1.7).

### D2 — `std/sandbox` is one governed tool set, and effect is derived, never declared by the caller

`New(sb Sandbox, p SandboxPolicy, opts ...Option) ToolSet` returns the four tools (`run_code`, `write_file`, `read_file`, `list_files`) as one registration unit, so `Build` sees one governed set rather than four unrelated tools. The effect of every tool in the set is derived from the policy before the first call, following Contract rule 1:

- `PerCall` + empty `Egress.Allow` → `ReadOnly`.
- `PerSession` + empty `Egress.Allow` → `Idempotent`.
- any `Egress.Allow` entry → `SideEffect`.

`WithEffect` may only tighten: an option that names a weaker effect than the derived one is refused at construction, so a host cannot downgrade a `SideEffect` policy to `ReadOnly` and skip the permission gate. Model-written code is `OriginModel` and `run_code` arguments pass the permission gate (`Ask` recommended under a `SideEffect` policy); results pass `StageToolResult` like every other tool. That gate and guard wiring belongs to the existing chain, so the tool set adds no permission code — it only declares its effect honestly, which is what the gate reads.

An empty `Egress` means deny-all, including DNS. That is the derived default and the policy value the adapter receives; nothing in `std/sandbox` interprets the allowlist.

### D3 — `EgressPolicy` is shared with `tools`, by type, with no second vocabulary

`SandboxPolicy.Egress` is `types.EgressPolicy` — the same type a harness tool carries in `ToolSpec.Egress` (core/types/tool.go:59-68) — with its private-range and hop rules. One type means one `Explain` shape and one policy a reviewer reads: `openspec/specs/sandbox/spec.md` *Host runner* shows a sandbox tool set and a harness tool rendering the same shape, and the conformance id `sandbox.egress-shares-policy-type` (owned by `m2-sandbox-adapter-conformance`) proves it against an adapter. `std/sandbox` therefore defines no egress struct, no host matcher and no allowlist parser. It hands the policy to the adapter and, on a denial the adapter reports, returns the shared `types.EgressDenied` to the model with the host and the reason only — the same shape `std/egress` already produces.

### D4 — Named secrets resolve through the new `SecretResolver`, and the harness `Credential` is a different thing

`SandboxPolicy.Secrets` names a `SecretRef{Name, Env}`. At `Open` the harness resolves each `SecretRef.Name` through the optional `types.SecretResolver` the build carries (`Resolve(ctx, Principal, name) (string, error)`, D1.7) for the run's originator `Principal`, collects the results into one environment map keyed by `Env`, and injects it through `Workspace.SetEnv`. Two paths, deliberately not merged:

- **Harness credential.** The `Credential` in the run `ctx` is never exported into a workspace. It is not a `SecretRef`, no policy can name it, and it appears in no process environment and no mounted file. `identity.credential-not-in-sandbox`, whose id is registered under `identity`, asserts exactly this, and the propagation table already gives the `sandbox` process row `Credential: none (secrets via SecretResolver only)` (openspec/specs/identity/spec.md). The run `Credential` is never a substitute for a resolver: a missing resolver or an unresolvable `Name` is a wiring failure the host sees, and no implementation path passes the caller's token where a named secret was asked for.
- **Authorised secret.** A `SecretRef` the operator put in the policy is an explicit grant for one named value and one named env var. The scanner must therefore be able to tell an injected value apart from model-written text; the redaction set is the values injected for this call.

From `SetEnv` on, the value is invisible outside the workspace: it never enters tool arguments, results, history, the journal, an audit record, a span or a log line, and `read_file`/`run_code` output is scanned for every injected value before it becomes a result. A hit is replaced by a redaction marker and increments `gohan.sandbox.secret_leak`. A build with no `SecretResolver` injects nothing and the secret-named tool call fails as an ordinary tool error — the same stance `m1-identity-detach` took for a detached run without a source (D3 there): an absent source is a wiring failure the host sees, never a fallback that passes the caller's token through.

The `CredentialSource` the build already carries (core/build_options.go:147-151) is untouched and keeps its one job: re-issuing the run's own credential on resume and detached runs (`identity.detached-reissues-credential`). It is not the named-secret path and the two are never satisfied by one object.

### D5 — Output cap and `OutputStore` spill reuse the shipped store and the shipped `read_output`

`Stdout` and `Stderr` are capped by `ToolSpec.MaxOutput`, whose default is already the tools contract's `64 << 10` (core/tool.go:26-28,180). Bytes beyond the cap and any file the model asks to return go to the `OutputStore` through the shipped `Put(ctx, RunInfo, []Block)`, and the result carries a head excerpt plus `types.File` blocks whose `Blob.Ref` names the stored content, readable back through the harness `read_output` tool (std/outputs/read_output.go). The tool set adds no new store interface, no ref format and no paging scheme; the store already claims refs per session and per subject, so the erasure and fork cascades cover spilled sandbox output without extra work. `ExecResult.Truncated` is what tells the tool set that the cap fired.

### D6 — `PerCall` is `Open` → `Exec` → `Close` in one call, and `Close` happens exactly once

`PerCall` workspaces exist for the duration of a single tool call. The call opens, executes, reads back what it needs, and closes — and the close runs on the panic path too, through a deferred close guarded by a once-only flag, so a panicking call cannot leak a workspace or double-close one. `PerCall` is the only lifecycle this change implements: the `PerSession` half needs session metadata, the checkpoint hook and the reaper, and it is `m2-sandbox-lifecycle-resume`'s work. The port and the policy already carry it, so this change leaves `PerSession` an accepted, unexercised value rather than a rejected one.

### D7 — Sandbox seconds are charged per run tree, next to the existing limit chain

`RunLimits.MaxSandboxSeconds` (core/types/limits.go:19) bounds cumulative `ExecResult.Seconds` for the tree; `Usage.SandboxSeconds` (core/types/model_types.go:38) reports what was spent, and `core/chains/limits.go:169` already prices the field for quota pools. The charge lands in the harness limit chain beside `MaxToolCalls` and `MaxWallClock`, so it is keyed by the root run the way `limits` and the run tree require — not by the pod and not by the session. Exceeding it returns `*LimitExceededError{Limit: "MaxSandboxSeconds"}`, the same error class every other limit raises (core/types/errors.go:144).

`MaxExecTime` bounds one `Exec`: the adapter kills the process group inside the workspace, and the outcome follows the effect — `Unknown` under a `SideEffect` policy, because the command may have taken effect, `Failed` otherwise. That mapping is the same one `std/tool/exec` already implements for host timeouts (std/tool/exec/exec.go, `timeoutResult`). The kill itself is the adapter's obligation and its scenario id is `sandbox.exec-timeout`, owned by `m2-sandbox-adapter-conformance`; this change owns the seconds budget only.

### D8 — The host runner stays

`tool/exec` remains the no-isolation runner for fixed-argv commands, and sandbox execution does not replace it: `exec.New` takes a typed `Cmd` with an allowlisted `Env` and no shell, which is a different tool from one that executes model-authored code. Its package comment already says model-authored code must go through the sandbox capability, and this change makes that true. `AllowHostExec()` acknowledges; it does not isolate and it does not grant egress.

The `sandbox.host-exec-warning` scenario says `Build` logs the warning when `tool/exec` is registered without `AllowHostExec()`, and that is not what ships today: the only warning fires inside `exec.New` (std/tool/exec/exec.go:70-73), one call earlier than the scenario names. A constructor that is built and then dropped warns; a tool that reaches a run through `Build` warns at construction whether or not it is ever registered, so the requirement's WHEN clause is unsatisfied even though a warning exists. The gap is real work, not a test rename:

- The registration fact reaches `Build` through the tool value. `exec.New` records the acknowledgement and exposes it as an optional interface on the returned tool — `HostExecAcknowledged() bool` — beside the existing package. `Build` then checks each registered tool for that interface during the same pass that derives `types.Capabilities` (`core/native_resolve.go:67-82`) and warns once per registration naming the tool when the value identifies as a host runner and the allowance is absent.
- The seam is an optional interface, not a `ToolSpec` field and not a `core` → `std` import (depguard denies `core` importing `std/**`, .golangci.yml:11-20). `Build` never names `std/tool/exec`; it names the fact, which is the pattern ADR-0157 already settled for residency (`RegionProvider`).
- The `New` warning stays. It becomes defence in depth: it fires at the earlier point, so a host that builds a runner and never registers it is still told. The scenario-named subtest asserts both halves — `Build` with the tool registered and no allowance warns naming the tool, `Build` with `AllowHostExec()` does not warn, and the un-registered constructor still warns on its own.

### D9 — Public-path proofs

The eight owned scenarios are subtests named exactly their ids, driven through the public `Build` path with an in-package fake `Sandbox` that records `Open`/`Exec`/`Close` calls and returns scriptable `ExecResult` values:

- `sandbox.effect-derived` (`std/sandbox/scenarios_test.go`) — a `PerSession` tool set with empty `Egress` registers `run_code` as `Idempotent`; adding one egress host makes it `SideEffect`.
- `sandbox.secret-injected` (same file) — a policy naming `SecretRef{Name: "warehouse", Env: "PGPASSWORD"}` has the fake resolver called with that name for the originator, injects `PGPASSWORD` through `Workspace.SetEnv`, and asserts the value appears in no history message, journal entry or span.
- `sandbox.secret-leak-redacted` (same file) — code that prints the injected value yields a result carrying the redaction marker and one `gohan.sandbox.secret_leak` increment.
- `sandbox.per-call-closes` (same file) — a normal call and a panicking call each close exactly once.
- `sandbox.seconds-budget` (same file) — cumulative `ExecResult.Seconds` past `MaxSandboxSeconds` returns `*LimitExceededError{Limit: "MaxSandboxSeconds"}` and `Usage.SandboxSeconds` reports the spend.
- `sandbox.output-to-store` (same file) — stdout past `MaxOutput` and a returned file land in the `MemoryOutputs` store, and the result carries a head excerpt plus `File` blocks with `Ref`.
- `sandbox.host-exec-warning` (`core/build_host_exec_test.go`) — `Build` with a `tool/exec` value registered and no `AllowHostExec()` logs a warning naming the tool, `Build` with the allowance is silent, and the same test asserts the un-registered constructor still warns, which is the defence-in-depth half of D8.
- `identity.credential-not-in-sandbox` (`std/sandbox/credential_scenarios_test.go`) — a tool set executing a command with a `Credential` in the run `ctx` puts neither the token nor any value derived from it in the process environment or in mounted files.

No test reaches a network or a container: the fake is in `_test.go`, the store is `MemoryOutputs`, the secret resolver is a stub recording its calls, and the fake workspace implements `WorkspaceLister` for the listing path so the absent-assertion case has its own test beside it.

## Risks / Trade-offs

- [Risk] **The eight scenarios read as complete sandbox conformance.** The matrix row set this change owns is eight of fifteen sandbox ids plus one identity id; seven sandbox ids stay with `m2-sandbox-adapter-conformance` (egress enforcement, allowlist, `MaxExecTime` kill, shared egress type) and `m2-sandbox-lifecycle-resume` (per-session persistence, snapshot on suspend, resume on another pod, replay without re-execution). None of them becomes true because this change lands, and a reviewer who reads the proposal's id table as the capability's full acceptance list will over-read it. → Mitigated by the explicit out-of-scope paragraph in `proposal.md` *Impact*, by naming the two other owners in D2, D4, D6 and D7 where each seam is left open, and by the matrix row in `openspec/changes/m2/design.md` §2.3 that keeps the remaining ids owned elsewhere.
- [Risk] **The port method set freezes before any adapter proves it.** `Sandbox.Open` plus six `Workspace` methods is fixed by the M2 exit, and an adapter that cannot honour one of them will be rewritten around it rather than the other way round. → Accepted: the spec already names these methods and `docs/design/types.md` already indexes them, so the alternative is a second port at the freeze. The one method beyond the spec's own list, `SetEnv`, is the injection seam D4 needs and has no other home on the port. Listing is deliberately *not* in that frozen set (D1.5), so the adapters have room to shape it after this change without a compatibility shim.
- [Risk] A second port for secrets invites an implementer to satisfy `CredentialSource` and `SecretResolver` with one object, which would collapse the two paths the identity delta keeps apart. → Accepted and separated in D1.7 and D4: the shapes differ in both argument and result (`Principal` → one harness `Credential` versus `Principal` plus a name → one opaque value), a build without a `SecretResolver` injects nothing, and `identity.credential-not-in-sandbox` proves the token never enters the workspace either way.
- [Risk] The `Host runner` requirement claims a `Build` warning that today fires in the constructor, so the package would close `sandbox.host-exec-warning` on a test of the wrong seam. → Closed by making the warning real work (D8): the acknowledgement becomes an optional interface on the tool value, `Build` warns on registration, and the scenario subtest drives `Build` rather than the constructor. The constructor warning is kept and asserted separately.
- [Risk] Two ways to reach a secret — a `SecretRef` grant and a `Credential` in `ctx` — invite one implementation that serves both. → Accepted and separated in D4: the credential is unreachable by construction (no policy names it), and a build without a `SecretResolver` injects nothing rather than falling back to the caller's token.
- [Risk] An in-package fake `Sandbox` could drift from a real adapter's behaviour, so a green scenario proves the governance and not the isolation. → Accepted: the fake tests what this change owns (derivation, secret handling, lifecycle, budget, spill), and the adapter suite is a separate change with its own ids. The fake is never exported, so a host cannot build against it.
- [Risk] The `identity` *Context values* block is already 708 characters of prose before this change and the added credential clause pushes it to 1059, past the 500-character guidance. → Accepted: the archive merge replaces a requirement block whole, so splitting it would mean dropping sentences that `m1-identity-detach` and `m1-run-notifications` own. The delta adds one sentence and the `sandbox` *Secrets* requirement carries the same rule, so the credential clause has one normative home per capability and a reader who lands on either sees it.
- [Risk] Charging sandbox seconds in the harness limit chain touches a shared file (`core/chains/limits.go`) that the eino runtime lane also depends on. → Sequenced: `m2-eino-runtime` lands its limit work first, and this change edits only the sandbox charge and its error, in a separate function.
- [Trade-off] A workspace without `WorkspaceLister` cannot serve `list_files`, and the tool refuses rather than degrading. → Accepted: a partial listing would be a silent wrong answer to a model that asked for a directory, and a `List` method on `Workspace` would freeze a capability no adapter has asked for (D1.5). An adapter that lists implements the optional interface; one that does not refuses with `types.ErrWorkspaceListingRequired`.
- [Trade-off] `PerSession` is an accepted policy value with no implementation here. → Accepted: the port and policy carry it so the lifecycle change is an implementation, not a signature change, and the tool set derives `Idempotent` for it (D2) without running a second workspace.

## Migration Plan

No data migration. `core/types` gains the port, its value types, the `SecretResolver` and `WorkspaceLister` optional interfaces, the `ErrWorkspaceListingRequired` sentinel and the `WithSecretResolver` build option; `core/aliases.go` gains the sandbox aliases; `core/stores/checkpoint.go` aliases the shared `WorkspaceRef` instead of declaring it; `std/sandbox` is new; `core/chains/limits.go` gains the sandbox-seconds charge and the `SandboxSeconds` reporting path; `core/build.go` gains the registration-time host-exec warning; `std/tool/exec` gains only the `HostExecAcknowledged` accessor over its existing flag. No stored shape changes — `Checkpoint.Workspace` already holds a `WorkspaceRef` — and no exported contract changes outside the new port.

`docs/design/types.md` is regenerated after the type addition, because the index already lists `Workspace`, `WorkspaceRef` and the port tier and the duplicate-definition section must show that `core/stores.WorkspaceRef` is now one alias. Keep the module pre-v1 and create no release tag while authoring; `m2-api-freeze` runs the compatibility check and cuts `v1.0.0` at the M2 exit.