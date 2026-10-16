# Design: m2-data-analyst-example

## Context

The composed path exists as capabilities and subtests; it does not exist as a program. Facts verified in the worktree this session:

- The composition pattern is `examples/excursions/main.go:29-85`: `gohan.Build` over `std.Defaults()` plus `gohan.WithStores`, `gohan.WithModels` and `gohan.WithNativeAgent(gohan.NativeSpec{Request, Profile, Assemble, Tools, Decider})`, then `gohan.NewNativeConversation(stack, name, gohan.WithConversationRuns(stores.NewMemoryRuns()), gohan.WithConversationEventLog(stores.NewMemoryEventLog()))`, then a ranged `conv.Send(ctx, session, types.Message{...})`, then `sessions.Load(ctx, session)` for the persisted history. The example persists nothing by hand.
- The example module is `examples` with its own `go.mod` (`examples/go.mod`, `examples/go.sum`), and `Taskfile.yml:124-132` enumerates each shipped example main explicitly in `examples:run` (`timeout 2m go -C examples run ./"$dir"`). Neither `examples/research-report` nor `examples/data-analyst` exists in the tree and neither is in that list.
- The sandbox port is frozen: `types.Sandbox.Open(ctx, WorkspaceRef, SandboxPolicy) (Workspace, error)` with the six-method `Workspace` `Exec/Put/Get/SetEnv/Snapshot/Close`. The optional companions live beside it and are reached by type assertion: `WorkspaceLister.List`, `SandboxResources` with `Enumerate(ctx) ([]WorkspaceRef, error)` and `CloseByRef(ctx, WorkspaceRef) error` (`core/types/sandbox.go:97-102`), `WorkspaceResource.ResourceRef()` and `WorkspaceOwner`. `m2-docker-sandbox` supplied that enumeration contract; **this change reuses it and grows no port method.**
- `types.SecretResolver` is `Resolve(ctx, Principal, name) (string, error)` (`core/types/secret.go:5-20`), it is distinct from `CredentialSource`, it is carried on the run context by `types.WithSecrets`, and `Workspace.SetEnv` is the only injection path into a workspace (`openspec/specs/sandbox/spec.md:106-118`).
- Empty `Egress` means deny-all including DNS (`openspec/specs/sandbox/spec.md:120`), and a `PerSession` policy with empty `Egress` derives `Idempotent` for `run_code` (`:118`).
- An injected value must not appear in tool arguments, results, history, journal, audit records, spans or logs (`openspec/specs/sandbox/spec.md:118`). Output sanitization is owned by `protect-tool-output-storage` (`sandbox.output-stored-redacted`); the publication-error sanitizer is owned by `m2-sandbox-lifecycle-resume` (`sandbox.publication-error-sanitized`). **This change adds no redaction and no conflicting rule.**
- Paging is `read_output(ref, range)`, built by `std/outputs.NewReadOutput` (`std/outputs/read_output.go:26-71`), and the `working-state.output-paging` row (`openspec/specs/working-state/spec.md:156-159`) already fixes its contract. **The example reuses that row; it does not restate it.**
- The lifecycle ordering is owned by `m2-sandbox-lifecycle-resume` D-sequence: `Publish` → checkpoint save → suspension transition → `Release`; restore before the run re-drives on both `Resume` and `Recover`, fenced on generation and owner.

## Goals / Non-Goals

**Goals:** one deterministic offline entry that composes `m2-sandbox-governance` and `m2-sandbox-lifecycle-resume` on the public path; an observable composed-ordering requirement; ten reused scenario ids each mapped to an owner, a subtest name and an evidence line; one new composition id registered in `openspec/scenarios.json`; a README that maps every requirement this entry exercises to its scenario id; an explicit offline-vs-real boundary that names six runtime promises the fixture does **not** prove.

**Non-Goals:** no change to `core`, `std` or `adapter`; no new `Sandbox`/`Workspace` method; no new egress, secret or redaction rule; no real pandas, no real warehouse client, no container runtime, no network, no API key; no change to the `sandbox` or `working-state` main specs beyond the delta this package carries; no change to `examples/excursions`; nothing about `m2-research-report-example`, which is a separate package.

## Offline-vs-real boundary

The sandbox adapter in the offline test is a deterministic in-process fake. It returns scripted `ExecResult` values and scripted file contents keyed by path, it counts `Open`, `Exec`, `Put`, `Get`, `SetEnv`, `Snapshot` and `Close`, and it holds its file map in a map guarded by a mutex so the assertions are order-independent.

**What the fixture does prove.** That the public composition is reachable and ordered: `Build` admits a `PerSession` policy with the coordinator and durable metadata surfaces wired, the tool set derives `Idempotent` under empty `Egress`, `SetEnv` is the injection path, the guard runs before persistence and before the store write, a suspend publishes a durable ref, a resume restores that ref and reads the earlier turn's file, and `read_output` returns the requested slice of a stored block. Every one of those is harness behavior above the adapter boundary.

**What the fixture does not prove, and who owns it instead.** A fixture records calls; it does not isolate a process, so it cannot enforce anything at the network layer. Each promise below therefore needs a named owner and stays open here:

| Promise | Why the fixture cannot prove it | Owner / scope |
|---|---|---|
| Deny-all egress enforcement (no TCP connect, no DNS resolution, `gohan.sandbox.egress_denied`) | Enforcement is at the adapter's network layer; a map-returning fake has no network layer | `m2-sandbox-adapter-conformance` (`sandboxtest.Sandbox` with a real `Factory`); the adapter leg is deferred past M2 with `m2-e2b-sandbox` |
| Real pandas / DataFrame execution, `MaxExecTime` process-group kill, `ExecResult.Seconds` accounting | A scripted `ExecResult` is a recording, not a process | `m2-sandbox-adapter-conformance` against `adapter/docker`; `sandbox.exec-timeout` |
| Router `Cascade` ordering and provider failover | Requires two live model adapters | Catalog scope: excluded, `build` capability owns `Router` and the `Cascade` row |
| Shared quota accounting across sessions | Requires two live stores and a shared quota source | Catalog scope: excluded, `limits`/`build` capability owns shared quota |
| Batch yielding and the batch gate under a real tool loop | The offline entry drives a single deterministic batch through the fixture | `m2-research-report-example` carries the multi-tool batch narrative; this entry proves one batch |
| `ClassContextOverflow` backstop (one compaction at the raised ratio, one retry, second overflow `Permanent`) | Requires a provider that reports `ClassContextOverflow` | `m2-context-calibration-overflow` (`openspec/specs/context/spec.md:111,125`); excluded here by catalog scope |

The composition scenario `sandbox.data-analyst-resume-and-page` therefore asserts **harness ordering and reachability only**. Its `THEN` clauses name harness-observable facts. The package does not claim fixture replay proves runtime enforcement, and the README repeats the table so a reader of the example sees the same boundary the design does.

## Dependency map — reused scenario ids

Ten ids are reused with their names, requirement blocks and owners unchanged. Each row names the owner, the subtest that carries the id inside the example, and the evidence the subtest produces.

| Reused id | Owner (unchanged) | Subtest in `examples/data-analyst` | Evidence |
|---|---|---|---|
| `sandbox.egress-denied` | `m2-sandbox-adapter-conformance` | `TestDataAnalystOffline/sandbox.egress-denied` | the policy under test carries empty `Egress.Allow`; the fake records the attempt and the count increments. **Reachability only — network-layer denial is not proven here** |
| `sandbox.secret-injected` | `m2-sandbox-governance` | `TestDataAnalystOffline/sandbox.secret-injected` | `SetEnv` recorded exactly once with the resolved value; `sessions.Load` history, the journal and the span set carry no occurrence of it |
| `sandbox.secret-leak-redacted` | `m2-sandbox-governance` | `TestDataAnalystOffline/sandbox.secret-leak-redacted` | the scripted stdout prints the injected value; the persisted result carries the redaction marker and the leak counter increments |
| `sandbox.per-session-persists` | `m2-sandbox-lifecycle-resume` | `TestDataAnalystOffline/sandbox.per-session-persists` | turn 2 `Get`s the extract written in turn 1; the fake's `Put` count does not grow between turns |
| `sandbox.snapshot-on-suspend` | `m2-sandbox-lifecycle-resume` | `TestDataAnalystOffline/sandbox.snapshot-on-suspend` | the checkpoint read back after suspension carries the ref the fake's `Snapshot` returned, with its generation |
| `sandbox.resume-restores` | `m2-sandbox-lifecycle-resume` | `TestDataAnalystOffline/sandbox.resume-restores` | a second stack with an independent coordinator and adapter instance over the same fake backend opens from the stored ref and reports the file; no fresh `Open` with an empty ref occurs |
| `sandbox.replay-no-reexec` | `m2-sandbox-lifecycle-resume` | `TestDataAnalystOffline/sandbox.replay-no-reexec` | replay of a run that executed before suspending does not grow the fake's `Exec` count |
| `sandbox.output-to-store` | `m2-sandbox-governance` | `TestDataAnalystOffline/sandbox.output-to-store` | stdout above `MaxOutput` yields a head excerpt plus a `Ref`, and the fake output store holds the full body |
| `sandbox.output-stored-redacted` | `protect-tool-output-storage` | `TestDataAnalystOffline/sandbox.output-stored-redacted` | the stored block and the `Ref`-bearing head excerpt carry no unmasked identifier and no injected secret |
| `working-state.output-paging` | `working-state` capability | `TestDataAnalystOffline/working-state.output-paging` | `read_output(ref, range)` returns exactly the requested slice and an out-of-range window is refused |

Every one of those ten subtest names is a real, distinct function of the example and never a closure over the same helper: each asserts its own recorded evidence. A `-run` filter that matches zero subtests is a failure, not a pass.

## Decisions

### D1 — Composition shape follows `examples/excursions` exactly

The entry registers one native definition over in-memory stores and a scripted model. It holds no HTTP handler, no repository package and no transport: the acceptance criterion that the composed path is reachable is answered by the program running offline, not by a service layout. The assembler returns the history clone, exactly as `examples/excursions/main.go` does, so no provider is contacted.

### D2 — One `PerSession` policy, empty `Egress.Allow`, deny-all

The warehouse is queried from inside the workspace, so the policy needs a credential but must not need a network. `PerSession` with an empty `Egress` is therefore the only shape that both persists the extract across turns and keeps egress denied, and it derives `Idempotent` for `run_code`. The example never sets an allowlist entry, so it never claims an allowlist case it cannot enforce.

### D3 — The secret is injected once, through `SetEnv`, and never leaves

The policy names `SecretRef{Name: "warehouse", Env: "PGPASSWORD"}` and the example registers a fixture `SecretResolver` on the build. The resolved value reaches the workspace only via `Workspace.SetEnv`. The example asserts, over the persisted history, the journal and the recorded spans, that the value appears in none of them — the same shape as `sandbox.secret-injected`, observed through the composed path rather than through `std/sandbox`.

### D4 — The personally-identifying guard runs before persistence and before paging

Two orderings are asserted, and both are harness behavior:

1. The guard is a tool-chain guard, so it rewrites the candidate result before the result is journaled. A masked identifier therefore never reaches a journal entry or a history message.
2. `read_file` and `run_code` output is scanned for both the injected values and the personally-identifying patterns before the overflow path hands the body to the `OutputStore`. The stored block, the head excerpt and the `Ref` carry the redacted form, so `read_output` can only ever page redacted bytes.

The example asserts the stored bytes by reading them back through `read_output`, not by trusting the writer. Ordering is asserted by the recorded call sequence, not by timing.

### D5 — The new composition id is `sandbox.data-analyst-resume-and-page`

The milestone task requires new composition ids to use the owning capability's prefix plus an example qualifier (`openspec/changes/m2/tasks.md:47`). The owning capability is `sandbox`, so the id is `sandbox.data-analyst-resume-and-page`, and it is registered in `openspec/scenarios.json` with `origin` `m2-data-analyst-example` and `title` `data analyst resumes and pages`, beside the other composition rows rather than beside the deferred `M2` block (it is authored, not deferred).

Its two `THEN` clauses name only harness-observable facts, per the boundary table above:

- the second turn's `run_code` observes the extract the first turn wrote, with the adapter's `Exec` count unchanged for the replayed step;
- `read_output(ref, range)` returns the requested slice of the stored body and no page contains a personally identifying value.

### D6 — Determinism and security rules for the fixtures

- No clock, no `math/rand`, no goroutine-order dependence, no map iteration in an assertion. Any randomness a fixture needs comes from `crypto/rand`, and nothing here is security-relevant enough to need randomness at all.
- The fake adapter is keyed by path, guarded by a `sync.Mutex`, and returns the same bytes for the same path on every call. The example's tests run under `-race` (task 4.1) because a shared fake that races under a resumption test is a real defect, not a flake.
- Every workspace path the fixture accepts MUST be confined to `t.TempDir()`: reject an absolute path, a `..` element and any path that escapes the root after `filepath.Join`, so a fixture never becomes a path-traversal sink the way a naive real adapter would be. `os.Root` is the Go 1.24+ form; the example uses the check rather than the root because the fake writes through a map, not a filesystem.
- The fixture resolver returns a **non-secret sentinel** such as `not-a-real-credential`. No fixture, README or task line carries credential-looking material, and the sentinel is never printed by the example's own `main`. The only credential-adjacent literal in the package is the environment variable *name* `PGPASSWORD`, which is a name and not a value.
- The scripted model returns a fixed tool call sequence per turn; the batch gate settles every call before the first executes.
- No fixture reaches the network, a container runtime, an API key or a filesystem outside `t.TempDir()`.

### D7 — Registry and README

One row is added to `openspec/scenarios.json`. The entry's `README.md` maps every requirement this example exercises to its scenario id — the ten reused ids plus the new one — and carries the boundary table so the reader of the example sees the same limits the design states. `examples:run` (`Taskfile.yml:124-132`) is the only target enumerating example dirs by name — `spec:coverage`, `spec:gate`, `lint`, `test`, `examples:test`, `test:race` all sweep `go -C examples` already — so the change is one `DIRS` var, not six edits, and no target is renamed.

### D8 — What stays open on purpose

`sandbox.egress-denied`'s network-layer half stays open under `m2-sandbox-adapter-conformance` and, past M2, under `m2-e2b-sandbox`. This package ticks neither it nor the five catalog-scope rows. The lane report names them as excluded by scope, not as passed, and no `evidence:` line in `tasks.md` claims a fixture observation for any of them.

## Admission

- **One prerequisite blocks full admission** and is stated, not papered over: the adapter leg of `sandbox.egress-denied` and every real-execution promise in the boundary table need a live sandbox provider. `m2-e2b-sandbox` is deferred past M2 (no credential, endpoint or dependency exists in the repository or the environment), and `m2-sandbox-lifecycle-resume` task 6.5 records the matching unmet durable-enumeration case. The composed public-path ordering this package owns is reachable without either, so this package proceeds; the runtime-enforcement claims stay open and are reported as such.
- **No port work remains.** `m2-docker-sandbox` supplied `types.SandboxResources`; `m2-api-freeze` froze the port method sets. `task api:check` must still report the `Sandbox` and `Workspace` entries unchanged.