# Design: m2-sandbox-governance

## Context

The `sandbox` capability spec states the port, the policy and the model-facing tool set in its Contract block and eight numbered rules (openspec/specs/sandbox/spec.md:24-103), then restates them as five requirements with fifteen scenarios (openspec/specs/sandbox/spec.md:85-168). Nothing in the tree declares any of it: no `Sandbox` interface, no `Workspace`, no `std/sandbox`. `core/stores/checkpoint.go:31-34` carries a `WorkspaceRef` local to the `Checkpoints` port with a comment that the alias step replaces it with the shared type, and `docs/design/types.md:269-270,642` lists `Workspace`, `WorkspaceRef` and the port tier as belonging to `sandbox` — so the type index already expects this change to put them there.

This change is the first of the three sandbox lanes in the M2 matrix (openspec/changes/m2/design.md §2.3). The port and its governance land here; adapter conformance (`m2-sandbox-adapter-conformance`) and the persisted workspace lifecycle (`m2-sandbox-lifecycle-resume`) build on this one. It owns seven sandbox ids and one identity id.

## Goals / Non-Goals

**Goals:** Settle and declare the sandbox port and its policy types in `core/types`; ship `std/sandbox` as one governed tool set; derive effects from the policy and lifecycle; resolve and redact secrets through the shipped `CredentialSource`; cap outputs into the shipped `OutputStore`; run the `PerCall` lifecycle with a close-exactly-once guarantee; charge `MaxSandboxSeconds` per run tree; keep the host runner's `Build` warning; turn the eight owned scenarios green on the public path.

**Non-Goals:** No isolation technology. No `adapter/docker` and no `adapter/e2b` (separate changes); this change adds an in-memory fake only for the tests, in `_test.go` files, and never exports it. No network enforcement inside the workspace: the adapter enforces `EgressPolicy` at the network layer, and this change only passes the policy and classifies the resulting effect. No `PerSession` persistence: `Snapshot`, session metadata, `Checkpoint.Workspace`, the reaper and `Replay` belong to `m2-sandbox-lifecycle-resume`. No new store, no new credential store, no token refresh loop, no `tool/exec` behaviour change beyond keeping its existing warning, and no provider compaction.

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
	Snapshot(ctx context.Context) (WorkspaceRef, error)
	Close(ctx context.Context) error
}
```

Six decisions inside that shape, each taken deliberately:

1. **`Open` takes a `WorkspaceRef`, not an empty string.** A `PerCall` call passes the zero `WorkspaceRef`; a `PerSession` session passes the ref restored from `Checkpoint.Workspace`. One signature covers both lifecycles, so the port does not gain an `OpenSession` method when the lifecycle change lands.
2. **`Exec` returns `(ExecResult, error)` with the process outcome inside `ExecResult`, not as the error.** A non-zero exit is a result the tool turns into an `Outcome`, and the adapter's failure to start the process is the error. A kill is `ExitCode` plus the caller's own timeout signal, not a distinguished error class.
3. **`Snapshot` is on `Workspace`, not on `Sandbox`.** A snapshot is a property of the open workspace; `Sandbox` stays a one-method factory. That is what lets `m2-sandbox-lifecycle-resume` add snapshot-on-suspend without a second factory method.
4. **`Close` takes a `context.Context` and returns only `error`.** No `Close(ctx, force bool)`: force-closing after a `MaxExecTime` kill is the adapter's own obligation inside `Exec`, and a second shape would be a method the freeze would keep forever.
5. **`Put`/`Get` are the file surface; there is no `List`.** `list_files` is a tool that reads the workspace through the adapter's own listing, and the port does not carry it until an adapter demonstrates it needs a distinct shape. **This is the one open item**: if `m2-docker-sandbox` cannot enumerate a directory without a seventh method, the port grows a `List(ctx, path) ([]string, error)` here, before the freeze, not after. The design states it as a condition, not a settled fact.
6. **`WorkspaceRef` moves to `core/types` and `core/stores` aliases it.** The `Checkpoints` port depends on the type, and the local declaration in `core/stores/checkpoint.go` says so in its own comment. One type, declared in `sandbox`, aliased where it is consumed, matching how the other capability types are exposed through `core/aliases.go`.

The values around the port follow the spec exactly and are not open: `Lifecycle` (`PerCall`, `PerSession`), `SandboxPolicy{Lifecycle, Egress, CPU, Memory, Disk, MaxExecTime, Secrets}`, `SecretRef{Name, Env}`, `ExecRequest{Argv, Stdin, Cwd, Timeout}`, `ExecResult{ExitCode, Stdout, Stderr, Truncated, Seconds}`.

### D2 — `std/sandbox` is one governed tool set, and effect is derived, never declared by the caller

`New(sb Sandbox, p SandboxPolicy, opts ...Option) ToolSet` returns the four tools (`run_code`, `write_file`, `read_file`, `list_files`) as one registration unit, so `Build` sees one governed set rather than four unrelated tools. The effect of every tool in the set is derived from the policy before the first call, following Contract rule 1:

- `PerCall` + empty `Egress.Allow` → `ReadOnly`.
- `PerSession` + empty `Egress.Allow` → `Idempotent`.
- any `Egress.Allow` entry → `SideEffect`.

`WithEffect` may only tighten: an option that names a weaker effect than the derived one is refused at construction, so a host cannot downgrade a `SideEffect` policy to `ReadOnly` and skip the permission gate. Model-written code is `OriginModel` and `run_code` arguments pass the permission gate (`Ask` recommended under a `SideEffect` policy); results pass `StageToolResult` like every other tool. That gate and guard wiring belongs to the existing chain, so the tool set adds no permission code — it only declares its effect honestly, which is what the gate reads.

An empty `Egress` means deny-all, including DNS. That is the derived default and the policy value the adapter receives; nothing in `std/sandbox` interprets the allowlist.

### D3 — `EgressPolicy` is shared with `tools`, by type, with no second vocabulary

`SandboxPolicy.Egress` is `types.EgressPolicy` — the same type a harness tool carries in `ToolSpec.Egress` (core/types/tool.go:59-68) — with its private-range and hop rules. One type means one `Explain` shape and one policy a reviewer reads: `openspec/specs/sandbox/spec.md` *Host runner* shows a sandbox tool set and a harness tool rendering the same shape, and the conformance id `sandbox.egress-shares-policy-type` (owned by `m2-sandbox-adapter-conformance`) proves it against an adapter. `std/sandbox` therefore defines no egress struct, no host matcher and no allowlist parser. It hands the policy to the adapter and, on a denial the adapter reports, returns the shared `types.EgressDenied` to the model with the host and the reason only — the same shape `std/egress` already produces.

### D4 — Secrets come from `CredentialSource` for the originator, and the harness `Credential` is a different thing

`SandboxPolicy.Secrets` names a `SecretRef{Name, Env}`. At `Open` the harness resolves each one through the `CredentialSource` the build already carries (core/build_options.go:147-151) for the run's originator `Principal`, and injects the resolved value as `Env` inside the workspace. Two paths, deliberately not merged:

- **Harness credential.** The `Credential` in the run `ctx` is never exported into a workspace. It is not a `SecretRef`, no policy can name it, and it appears in no process environment and no mounted file. `identity.credential-not-in-sandbox`, whose id is registered under `identity`, asserts exactly this, and the propagation table already gives the `sandbox` process row `Credential: none (secrets via CredentialSource only)` (openspec/specs/identity/spec.md).
- **Authorised secret.** A `SecretRef` the operator put in the policy is an explicit grant for one named value and one named env var. The scanner must therefore be able to tell an injected value apart from model-written text; the redaction set is the values injected for this call.

From `Open` on, the value is invisible outside the workspace: it never enters tool arguments, results, history, the journal, an audit record, a span or a log line, and `read_file`/`run_code` output is scanned for every injected value before it becomes a result. A hit is replaced by a redaction marker and increments `gohan.sandbox.secret_leak`. A build with no `CredentialSource` injects nothing and the secret-named tool call fails as an ordinary tool error — the same stance `m1-identity-detach` took for a detached run without a source (D3 there): an absent source is a wiring failure the host sees, never a fallback that passes the caller's token through.

### D5 — Output cap and `OutputStore` spill reuse the shipped store and the shipped `read_output`

`Stdout` and `Stderr` are capped by `ToolSpec.MaxOutput`, whose default is already the tools contract's `64 << 10` (core/tool.go:26-28,180). Bytes beyond the cap and any file the model asks to return go to the `OutputStore` through the shipped `Put(ctx, RunInfo, []Block)`, and the result carries a head excerpt plus `types.File` blocks whose `Blob.Ref` names the stored content, readable back through the harness `read_output` tool (std/outputs/read_output.go). The tool set adds no new store interface, no ref format and no paging scheme; the store already claims refs per session and per subject, so the erasure and fork cascades cover spilled sandbox output without extra work. `ExecResult.Truncated` is what tells the tool set that the cap fired.

### D6 — `PerCall` is `Open` → `Exec` → `Close` in one call, and `Close` happens exactly once

`PerCall` workspaces exist for the duration of a single tool call. The call opens, executes, reads back what it needs, and closes — and the close runs on the panic path too, through a deferred close guarded by a once-only flag, so a panicking call cannot leak a workspace or double-close one. `PerCall` is the only lifecycle this change implements: the `PerSession` half needs session metadata, the checkpoint hook and the reaper, and it is `m2-sandbox-lifecycle-resume`'s work. The port and the policy already carry it, so this change leaves `PerSession` an accepted, unexercised value rather than a rejected one.

### D7 — Sandbox seconds are charged per run tree, next to the existing limit chain

`RunLimits.MaxSandboxSeconds` (core/types/limits.go:19) bounds cumulative `ExecResult.Seconds` for the tree; `Usage.SandboxSeconds` (core/types/model_types.go:38) reports what was spent, and `core/chains/limits.go:169` already prices the field for quota pools. The charge lands in the harness limit chain beside `MaxToolCalls` and `MaxWallClock`, so it is keyed by the root run the way `limits` and the run tree require — not by the pod and not by the session. Exceeding it returns `*LimitExceededError{Limit: "MaxSandboxSeconds"}`, the same error class every other limit raises (core/types/errors.go:144).

`MaxExecTime` bounds one `Exec`: the adapter kills the process group inside the workspace, and the outcome follows the effect — `Unknown` under a `SideEffect` policy, because the command may have taken effect, `Failed` otherwise. That mapping is the same one `std/tool/exec` already implements for host timeouts (std/tool/exec/exec.go, `timeoutResult`). The kill itself is the adapter's obligation and its scenario id is `sandbox.exec-timeout`, owned by `m2-sandbox-adapter-conformance`; this change owns the seconds budget only.

### D8 — The host runner stays

`tool/exec` remains the no-isolation runner for fixed-argv commands, and sandbox execution does not replace it: `exec.New` takes a typed `Cmd` with an allowlisted `Env` and no shell, which is a different tool from one that executes model-authored code. Its package comment already says model-authored code must go through the sandbox capability, and this change makes that true. The `Build` warning when `tool/exec` is registered without `AllowHostExec()` already exists and ships (std/tool/exec/exec.go:70-73); `sandbox.host-exec-warning` is an inherited acceptance check, so this change's obligation is to keep the warning naming the tool and to add the scenario-named subtest, not to invent new behaviour. `AllowHostExec()` acknowledges; it does not isolate and it does not grant egress.

### D9 — Public-path proofs

The eight owned scenarios are subtests named exactly their ids, driven through the public `Build` path with an in-package fake `Sandbox` that records `Open`/`Exec`/`Close` calls and returns scriptable `ExecResult` values:

- `sandbox.effect-derived` (`std/sandbox/scenarios_test.go`) — a `PerSession` tool set with empty `Egress` registers `run_code` as `Idempotent`; adding one egress host makes it `SideEffect`.
- `sandbox.secret-injected` (same file) — a policy naming `SecretRef{Name: "warehouse", Env: "PGPASSWORD"}` sets that env var inside the fake workspace, records the `CredentialSource` call for the originator, and asserts the value appears in no history message, journal entry or span.
- `sandbox.secret-leak-redacted` (same file) — code that prints the injected value yields a result carrying the redaction marker and one `gohan.sandbox.secret_leak` increment.
- `sandbox.per-call-closes` (same file) — a normal call and a panicking call each close exactly once.
- `sandbox.seconds-budget` (same file) — cumulative `ExecResult.Seconds` past `MaxSandboxSeconds` returns `*LimitExceededError{Limit: "MaxSandboxSeconds"}` and `Usage.SandboxSeconds` reports the spend.
- `sandbox.output-to-store` (same file) — stdout past `MaxOutput` and a returned file land in the `MemoryOutputs` store, and the result carries a head excerpt plus `File` blocks with `Ref`.
- `sandbox.host-exec-warning` (`std/tool/exec/host_exec_scenarios_test.go`) — `tool/exec` built without `AllowHostExec()` logs a warning naming the tool; with it, no warning.
- `identity.credential-not-in-sandbox` (`std/sandbox/credential_scenarios_test.go`) — a tool set executing a command with a `Credential` in the run `ctx` puts neither the token nor any value derived from it in the process environment or in mounted files.

No test reaches a network or a container: the fake is in `_test.go`, the store is `MemoryOutputs`, and the credential source is a stub recording its calls.

## Risks / Trade-offs

- [Risk] **The eight scenarios read as complete sandbox conformance.** The matrix row set this change owns is eight of fifteen sandbox ids plus one identity id; seven sandbox ids stay with `m2-sandbox-adapter-conformance` (egress enforcement, allowlist, `MaxExecTime` kill, shared egress type) and `m2-sandbox-lifecycle-resume` (per-session persistence, snapshot on suspend, resume on another pod, replay without re-execution). None of them becomes true because this change lands, and a reviewer who reads the proposal's id table as the capability's full acceptance list will over-read it. → Mitigated by the explicit out-of-scope paragraph in `proposal.md` *Impact*, by naming the two other owners in D2, D4, D6 and D7 where each seam is left open, and by the matrix row in `openspec/changes/m2/design.md` §2.3 that keeps the remaining ids owned elsewhere.
- [Risk] **The port method set freezes before any adapter proves it.** `Sandbox.Open` plus five `Workspace` methods is fixed by the M2 exit, and an adapter that cannot honour one of them will be rewritten around it rather than the other way round. → Accepted: the spec already names these methods and `docs/design/types.md` already indexes them, so the alternative is a second port at the freeze. The one genuinely open item is the `list_files` path in D1.5, which the adapters may still add; the design states the condition so the decision lands here, before `m2-api-freeze`, rather than as a post-freeze compatibility shim.
- [Risk] Two ways to reach a secret — a `SecretRef` grant and a `Credential` in `ctx` — invite one implementation that serves both. → Accepted and separated in D4: the credential is unreachable by construction (no policy names it), and a build without a `CredentialSource` injects nothing rather than falling back to the caller's token.
- [Risk] An in-package fake `Sandbox` could drift from a real adapter's behaviour, so a green scenario proves the governance and not the isolation. → Accepted: the fake tests what this change owns (derivation, secret handling, lifecycle, budget, spill), and the adapter suite is a separate change with its own ids. The fake is never exported, so a host cannot build against it.
- [Risk] The `identity` *Context values* block is already 708 characters of prose before this change and the added credential clause pushes it to 1059, past the 500-character guidance. → Accepted: the archive merge replaces a requirement block whole, so splitting it would mean dropping sentences that `m1-identity-detach` and `m1-run-notifications` own. The delta adds one sentence and the `sandbox` *Secrets* requirement carries the same rule, so the credential clause has one normative home per capability and a reader who lands on either sees it.
- [Risk] Charging sandbox seconds in the harness limit chain touches a shared file (`core/chains/limits.go`) that the eino runtime lane also depends on. → Sequenced: `m2-eino-runtime` lands its limit work first, and this change edits only the sandbox charge and its error, in a separate function.
- [Trade-off] `Put`/`Get` without a `List` method means `list_files` cannot be served by the port as declared. → Accepted for now; the alternative is a seventh frozen method that no adapter has asked for. D1.5 names the condition that revises it.
- [Trade-off] `PerSession` is an accepted policy value with no implementation here. → Accepted: the port and policy carry it so the lifecycle change is an implementation, not a signature change, and the tool set derives `Idempotent` for it (D2) without running a second workspace.

## Migration Plan

No data migration. `core/types` gains the port and its value types; `core/aliases.go` gains the sandbox aliases; `core/stores/checkpoint.go` aliases the shared `WorkspaceRef` instead of declaring it; `std/sandbox` is new; `core/chains/limits.go` gains the sandbox-seconds charge and the `SandboxSeconds` reporting path; `std/tool/exec` is unchanged. No stored shape changes — `Checkpoint.Workspace` already holds a `WorkspaceRef` — and no exported contract changes outside the new port.

`docs/design/types.md` is regenerated after the type addition, because the index already lists `Workspace`, `WorkspaceRef` and the port tier and the duplicate-definition section must show that `core/stores.WorkspaceRef` is now one alias. Keep the module pre-v1 and create no release tag while authoring; `m2-api-freeze` runs the compatibility check and cuts `v1.0.0` at the M2 exit.