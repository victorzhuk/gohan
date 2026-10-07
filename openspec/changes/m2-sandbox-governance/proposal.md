# Proposal: m2-sandbox-governance

## Why

The `sandbox` capability spec declares the port, the policy and the governed tool set, but no Go code declares them: `Sandbox`, `Workspace`, `WorkspaceRef`, `Lifecycle`, `SandboxPolicy`, `SecretRef`, `ExecRequest` and `ExecResult` exist only in the spec's Contract block (openspec/specs/sandbox/spec.md:24-83), and there is no `std/sandbox` package. `core/stores.WorkspaceRef` is a local placeholder declared only because the `Checkpoints` port depends on it, with a comment that the alias step replaces it with the shared type (core/stores/checkpoint.go:31-34), and `core/aliases.go` carries no sandbox alias yet. The model-facing path a run needs — write a file, run code, read the result back under the same permission gate, journal, budget and redaction as every other tool — does not exist.

Seven registered sandbox scenarios and one identity scenario therefore stay unimplemented (`openspec/scenarios.json`, all `deferred_to: M2`):

| ID | Title |
|---|---|
| `sandbox.effect-derived` | effect derived |
| `sandbox.secret-injected` | secret injected not visible |
| `sandbox.secret-leak-redacted` | leak redacted |
| `sandbox.per-call-closes` | per-call closes |
| `sandbox.seconds-budget` | sandbox seconds budget |
| `sandbox.output-to-store` | large output to store |
| `sandbox.host-exec-warning` | host exec warning |
| `identity.credential-not-in-sandbox` | credential not in sandbox |

The pieces those scenarios lean on already ship and are reusable: `types.EgressPolicy` with its private-range and hop rules (core/types/tool.go:59-68), `RunLimits.MaxSandboxSeconds` (core/types/limits.go:19), `Usage.SandboxSeconds` (core/types/model_types.go:38), `stores.OutputStore` with `MemoryOutputs` and the harness `read_output` tool (core/stores/outputs.go, std/outputs/read_output.go), `types.CredentialSource` (core/types/credential.go:24) and the `WithEffect` tightening option (core/tool.go:44). What is missing is the port, the derivation, the secret path and the tool set that binds them.

## What Changes

- Declare the sandbox port in `core/types`: `Sandbox.Open`, `Workspace.Exec/Put/Get/SetEnv/Snapshot/Close`, `WorkspaceRef`, `Lifecycle`, `SandboxPolicy`, `SecretRef`, `ExecRequest` and `ExecResult`, and reconcile `core/stores.WorkspaceRef` onto the one shared type through the alias step. Two capabilities ship as optional interfaces beside the port rather than as methods: `SecretResolver` (`Resolve(ctx, Principal, name) (string, error)`) for named secrets and `WorkspaceLister` (`List(ctx, path) ([]WorkspaceEntry, error)`) for `list_files`. The port's method set is settled here because `m2-api-freeze` freezes every port that exists at the M2 exit (openspec/changes/m2/design.md §1), and a port that grows is frozen with a method no adapter asked for (docs/design/compatibility.md:16).
- Add `std/sandbox.New(sb Sandbox, p SandboxPolicy, opts ...Option) ToolSet`, one governed tool set exposing `run_code`, `write_file`, `read_file` and `list_files`.
- Derive each tool's effect from the policy and lifecycle: `PerCall` with empty `Egress.Allow` is `ReadOnly`, `PerSession` with empty `Egress.Allow` is `Idempotent`, any `Egress.Allow` entry is `SideEffect`. `WithEffect` may only tighten. The tool set passes the policy's `EgressPolicy` to `Build` as the single egress vocabulary.
- Resolve each `SecretRef` through the optional `SecretResolver` for the run originator at `Open` and inject the resolved environment through `Workspace.SetEnv`. `CredentialSource` keeps its own job, re-issuing the run's credential on resume and detached runs: the run `Credential` is never a substitute for a resolver and never enters a workspace. No secret value reaches tool arguments, results, history, journal or spans. `read_file` and `run_code` output is scanned for injected values and redacted; the leak increments `gohan.sandbox.secret_leak`.
- Cap `Stdout` and `Stderr` by `ToolSpec.MaxOutput`; overflow and files the model asks to return go to the `OutputStore` and come back as `File` blocks with `Ref`.
- Run the `PerCall` lifecycle as `Open` → `Exec` → `Close` inside one tool call, with `Close` called exactly once even when the call panics.
- Charge cumulative `ExecResult.Seconds` per run tree against `RunLimits.MaxSandboxSeconds`, report it in `Usage.SandboxSeconds`, and return `*LimitExceededError{Limit: "MaxSandboxSeconds"}` when the budget is exceeded. `MaxExecTime` kills the process group; a kill under a `SideEffect` policy is `Outcome: Unknown`, otherwise `Failed`.
- Keep `tool/exec` as the no-isolation host runner and make its warning fire where the requirement puts it: `Build` warns when the tool is registered without `AllowHostExec()`, through an optional acknowledgement interface on the tool value. The constructor warning stays as defence in depth.
- Add the public-path subtests for the eight scenarios above, each named exactly its registered id.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `sandbox`: the delta modifies all five requirements of the capability (openspec/specs/sandbox/spec.md:85-168). Each block reproduces its existing scenario blocks byte-identical and adds the normative clauses the capability had only as Contract prose: the port method set, the effect derivation and `WithEffect` tightening, the secret resolution and redaction path, the output cap and `OutputStore` spill, the `PerCall` close-exactly-once rule, and the seconds budget with its `LimitExceededError`.
- `identity`: the delta modifies the *Context values* requirement (openspec/specs/identity/spec.md:202-229), whose existing `identity.credential-not-in-sandbox` scenario this change owns. The block reproduces its five scenario blocks byte-identical and adds the clause that a harness `Credential` is never exported into a workspace and that only an explicitly authorised `SecretRef` may be injected.

## Impact

The change adds a port file under `core/types`, one alias, one new `std/sandbox` package with its scenario tests, the sandbox-seconds charge in the harness limit chain, and the `SandboxSeconds` reporting path. It reuses the shipped `types.EgressPolicy`, `types.CredentialSource`, `stores.OutputStore`, `std/outputs.NewReadOutput` and `std/tool/exec`; it adds two optional ports (`SecretResolver`, `WorkspaceLister`), one sentinel and one build option beside them, no third-party dependency, no transport module and no store. `core` gains no sandbox implementation: isolation stays an adapter, and the adapters (`m2-docker-sandbox`, `m2-e2b-sandbox`) and their conformance suite are separate changes.

Out of scope for this change: adapter egress and timeout conformance (`sandbox.egress-denied`, `sandbox.egress-allowlisted`, `sandbox.exec-timeout`, `sandbox.egress-shares-policy-type`), the persisted `PerSession` workspace lifecycle and `Replay` (`sandbox.per-session-persists`, `sandbox.snapshot-on-suspend`, `sandbox.resume-restores`, `sandbox.replay-no-reexec`), and provider compaction. The eight scenarios this change closes are not the whole of sandbox conformance — `openspec/changes/m2/design.md` §2.3 assigns the remaining seven ids to two other changes, and this package must not be read as claiming them.