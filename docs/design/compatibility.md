# API compatibility

Normative process for `github.com/victorzhuk/gohan` and the `adapter/*` modules (ADR-0117).

## Versions and timeline

- The root module is `v0.x` through M2 and is tagged `v1.0.0` when M2's exit criteria pass. Within `v0`, breaking changes are allowed but every one is listed in the changelog under *Breaking*.
- Each adapter is its own module and its own version line (`adapter/openai/v0.4.0`). An adapter reaches `v1.0.0` when its conformance suite passes against core `v1`. An adapter's `go.mod` requires the lowest core version its conformance suite ran against.
- Release order is core, then adapters. `examples/` is a development workspace consumer. `go.work` is not the source of truth for released versions.
- Major bumps are reserved for overhauls, never for a single removal.

Published root identity is `v0.2.0`. All six landed adapters require that version. Their first-publication identity is `v0.1.0`, except that the already-published `adapter/otel/v0.1.0` is followed by `v0.1.1`. ADR-0173 retires the seven unpublished local `v1.0.0` drafts and supersedes the M2 per-module tag plan below for publication identity only.

`examples/` remains a development workspace consumer, not a release baseline. Its workspace dependencies do not establish an independent release verdict.

## Ports and handles

Every exported interface is one of two kinds, recorded in `docs/design/types.md`:

- **Port** — implemented by users or adapters (`Model`, `Tool`, `Decider`, `SessionLog`, `Runs`, `Checkpoints`, `Journal`, `AuditLog`, `EventLog`, `OutputStore`, `NotesStore`, `Sandbox`, `Workspace`, `Waker`, `CredentialSource`, `ContextProvider`, `Runtime`, `Stepper`, and the rest). From `v1` a port never gains, loses or changes a method. New capability arrives as a **new optional interface** discovered by type assertion, and the spec that introduces it states core's behaviour when the assertion fails. `storetest` and `conformance.*` exercise both paths. `PreemptedLister` (`stores`) and `MemoryStore` (`working-state`) are the first two.

`stores.CheckpointRuns` is the newest optional `Runs` surface: `SuspensionToken` reads the current suspension token, and `ResumingCheckpoint` fences lease acquisition by that token. The `Runs` method set remains unchanged.

The sandbox lifecycle work (ADR-0168) adds no method to any port: `Sandbox` and `Workspace` keep their exact method sets, and the growth is the optional `types.SandboxHandles` coordinator port and the optional `stores.SessionWorkspaceMeta` surface (with its `SessionWorkspaceOwnerGuard` seam and `ImportWorkspaceMeta` operation), each discovered by type assertion with core's absent-assertion behaviour stated in its capability spec. `stores.Checkpoint` gains only the additive `WorkspaceGeneration` field, which its zero value preserves.
- **Handle** — implemented by gohan only and called by users (`Conversation`, `Flow[In, Out]`). Handles may gain methods in a minor release. Users do not implement handles; `testkit/gohantest` ships the fakes.

Before `v1` a port may still change, but every growth is expressed as an optional interface from the start so the pattern is real, not aspirational.

## Functions, structs, options

- Behaviour is extended with functional options (`Option`, `notes.Option`, …) or new functions; signatures never change within a major.
- New behaviour within a major is opt-in; defaults do not change.
- Exported value structs in core (`RunLimits`, `Caps`, `Usage`, `ModelProfile`, `ToolSpec`, …) are documented as *not comparable, keyed literals only*: fields may be added when the zero value preserves behaviour, and a field of non-comparable type may be added at any time.
- Sentinel errors and error types are part of the API: their identity is stable; their messages are not.

### Egress proxy routing correction (security)

`Client`, `WithProxy` and `Option` keep their exact signatures. Both `http` and `https` proxy schemes remain supported. A supported proxy route now connects to the destination address the client policy validated; proxies that reject IP-destination request targets fail explicitly, and `ErrEgressProxyUnsupported` remains the error for an unsupported proxy mode. Rolling back to the previous proxy-side-DNS source reopens the proxy-DNS and CGNAT destination vulnerabilities; it is not a security-preserving fallback (ADR-0170, `docs/adr/0170-governed-call-authority-and-runtime-repair-baseline.md`).

## Approval and journal fingerprint transition

An ordinary approval receipt authorizes only its recorded session, run, argument generation, call ID, tool and complete arguments. It is not a reusable session grant. A scoped approval uses a subject-keyed, time-bounded grant instead. Resume checks eligibility and current policy, including scopes, exclusions, separation and each recorded quorum vote; an ineligible decision leaves the token pending.

New approval and journal fingerprints carry the `exact-v1:` prefix and canonicalize numbers by their exact decimal values. Legacy rounded scoped grants do not match and must be renewed. New journal lookup treats a legacy fingerprint only as an unresolved-intent candidate: it reuses the entry and its key only when the complete stored arguments prove an exact match. Missing, unreadable or unequal evidence is refused.

Drain old side-effect workers before transferring ownership to the new fingerprint format. Do not mix old and new intent lookup indefinitely. Before rollback, drain new workers and preserve journal records; old fingerprint lookup must not resume uncertain new-format intents.

## Deprecation

- `// Deprecated: use X.` is the one comment that is not a *why* and is still allowed (Go tooling reads it).
- A deprecated symbol survives at least two minor releases in `v0` and for the whole `v1` line.

## Gate

- `task api:check` compares each tagged root or adapter module with its latest tag using separate, module-aware `go/types` graphs. It reports generic method removals and inferred variable type changes, and permits additive fields under the keyed-struct rule. Incompatibilities are reported without failing for `v0` and fail at `v1` or later. Untagged modules are skipped.
- The compatibility contract requires detection of package-path-qualified imported type identity changes. Unrelated load errors must fail comparison.
- Only the verified published `adapter/otel/v0.1.0` baseline may receive a historical correction. Its tag and root `v0.1.0` must peel to the same release commit. The correction changes root `v0.0.0` and its workspace replacement to published root `v0.1.0` in a temporary comparison copy, prints the exception and changes no other dependencies.
- `task adapters:verify` checks isolated copies with `GOWORK=off`, rejects manifest replacements, checks `go mod tidy -diff`, builds and runs short tests including conformance. CI's `release-verify` job invokes the same task. It creates no tag or publication, and the historical comparison correction does not apply to it.
- The planned `api/gohan.yaml` comparison against the generated `adapter/httpapi` server is not implemented.
- `task spec:types` records the kind of every interface; after the freeze tag it fails when a port's method set changes (not yet implemented — the generator reads the capability specs, not the shipped packages; the gap is recorded under *v1-candidate* below).

## v1-candidate (M0.5)

Core types and ports are declared **v1-candidate** in `docs/design/api-review-m0-5.md` (2026-10-05, after the three offline acceptance processes passed). What that means and does not mean:

- The port method sets, the handle shapes (`Conversation`, `Flow[In, Out]`), the sentinel and typed error set, and the optional-interface growth pattern are fixed in shape and intended to survive into `v1.0.0`. Ports grow only through new optional interfaces whose absent-assertion behaviour the introducing spec states.
- The root module is **not** tagged `v1.0.0` here. It stays `v0.x` until M2's exit criteria pass; until then breaking changes remain possible and are listed under *Breaking* in `CHANGELOG.md`.
- Illustrative-tier types may still change before the freeze; v1-candidate covers the normative tier's shape and the process.
- The `api/gohan.yaml` diff against the generated `adapter/httpapi` server remains unavailable. Neither artifact exists in M0 (both are M4, with `adapter/httpapi` per `docs/design/architecture.md` §5); the gap is recorded in `docs/design/api-review-m0-5.md`.
- The `spec:types` port-method-set clause is unimplemented for the same reason stated plainly: the generator reads `openspec/specs/*/spec.md`, so it records declared intent and observes neither shipped method sets nor a change to one. No gate would fail today if a frozen port grew or lost a method.

## M2 freeze

M2's exit criteria pass, so the shapes recorded as v1-candidate above are frozen and every module in the release order is tagged. The stance is `openspec/changes/m2/tasks.md:53`: the port shapes recorded in the three first-wave designs are settled before the freeze — `runtime.Runtime` unchanged, `runtime.AgentRun` grown only when two shipped backends need it, `State.Backend` the only backend state carrier, `StepGranularity` two values, the sandbox port and `SecretResolver` as declared.

### Frozen port shapes

| Port | Declared method set | Ships at | Stance |
|---|---|---|---|
| `runtime.Runtime` (embeds `runtime.Stepper`) | `Start`, `Step`, `Name`, `Granularity` | `core/runtime/runtime.go:62-67` (`Stepper` `:55-58`) | unchanged |
| `runtime.AgentRun` | value struct: `Model`, `Tools`, `Assemble`, `History`, `Input`, `Save`, `ModelEffect`, `BatchEffect`, `Mode` | `core/runtime/runtime.go:83-97` | grown (`ModelEffect`, `BatchEffect`) only when two shipped backends need it |
| `runtime.State.Backend` | `[]byte`, the only backend-specific state carrier | `core/runtime/runtime.go:36` | the only backend state carrier |
| `runtime.StepGranularity` | two values: `GranularityEffect`, `GranularityTurn` | `core/runtime/runtime.go:70-75` | two values |
| `types.Sandbox` | `Open` | `core/types/sandbox.go:71-73` | as declared |
| `types.Workspace` | `Exec`, `Put`, `Get`, `SetEnv`, `Snapshot`, `Close` | `core/types/sandbox.go:77-84` | as declared |
| `types.SecretResolver` | `Resolve` | `core/types/secret.go:8-10` | as declared |

Every row names a `core/...` file:line that ships its method set. Growth beyond these sets arrives only as a new optional interface reached by type assertion (`WorkspaceLister`, `SandboxResources`, `SandboxHandles`, `stores.SessionWorkspaceMeta`), as the section above records, never as a new method on a frozen port.

### Per-module `v1.0.0` tag plan

This table and the transcript below record the retired local drafts, not published release identity. See ADR-0173 for the superseding identity.

`task api:check` visits `.` and `adapter/*` (`Taskfile.yml:53-69`). Release order is root, then adapters; each adapter's precondition is its conformance suite green against core `v1`. The current-baseline cell is the tag `task api:check` prints for that module before the freeze.

| Module dir | Module path | Current baseline | Freeze tag | Precondition |
|---|---|---|---|---|
| `.` | `github.com/victorzhuk/gohan` | `v0.1.0` | `v1.0.0` | M2 gate `missing_in_scope=0` |
| `adapter/anthropic` | `github.com/victorzhuk/gohan/adapter/anthropic` | none | `adapter/anthropic/v1.0.0` | conformance suite green against core `v1` |
| `adapter/docker` | `github.com/victorzhuk/gohan/adapter/docker` | none | `adapter/docker/v1.0.0` | conformance suite green against core `v1` |
| `adapter/eino` | `github.com/victorzhuk/gohan/adapter/eino` | none | `adapter/eino/v1.0.0` | conformance suite green against core `v1` |
| `adapter/openai` | `github.com/victorzhuk/gohan/adapter/openai` | none | `adapter/openai/v1.0.0` | conformance suite green against core `v1` |
| `adapter/otel` | `github.com/victorzhuk/gohan/adapter/otel` | `adapter/otel/v0.1.0` | `adapter/otel/v1.0.0` | conformance suite green against core `v1` |
| `adapter/postgres` | `github.com/victorzhuk/gohan/adapter/postgres` | none | `adapter/postgres/v1.0.0` | conformance suite green against core `v1` |
| `adapter/e2b` (conditional) | `github.com/victorzhuk/gohan/adapter/e2b` | none | `adapter/e2b/v1.0.0` | only when `m2-e2b-sandbox` lands |

`adapter/e2b` has not landed (`m2-e2b-sandbox` is open), so the freeze cuts seven tags and leaves that row conditional: the module carries no version line and no tag yet, exactly as the loop's `[ -f "$dir/go.mod" ] || continue` guard reports.

### `examples/` exclusion

The `api:check` loop visits `.` and `adapter/*` only (`Taskfile.yml:58`), so the `examples` module has no version line to baseline and carries no tag after the freeze; `task examples:test` and `task examples:run` are its witnesses (`Taskfile.yml:124-132`). The `tools/apicheck` loader skips any directory that holds its own `go.mod` (`tools/apicheck/main.go:205-244`), so a root run reports no `examples/**` or `adapter/**` symbol under the root module.

### Observed `api:check` after the freeze

`timeout 5m task api:check` at the frozen HEAD:

```
api:check: github.com/victorzhuk/gohan: diffing HEAD against v1.0.0
api:check: github.com/victorzhuk/gohan: exported API compatible with v1.0.0
api:check: github.com/victorzhuk/gohan/adapter/anthropic: diffing HEAD against adapter/anthropic/v1.0.0
api:check: github.com/victorzhuk/gohan/adapter/anthropic: exported API compatible with adapter/anthropic/v1.0.0
api:check: github.com/victorzhuk/gohan/adapter/docker: diffing HEAD against adapter/docker/v1.0.0
api:check: github.com/victorzhuk/gohan/adapter/docker: exported API compatible with adapter/docker/v1.0.0
api:check: github.com/victorzhuk/gohan/adapter/eino: diffing HEAD against adapter/eino/v1.0.0
api:check: github.com/victorzhuk/gohan/adapter/eino: exported API compatible with adapter/eino/v1.0.0
api:check: github.com/victorzhuk/gohan/adapter/openai: diffing HEAD against adapter/openai/v1.0.0
api:check: github.com/victorzhuk/gohan/adapter/openai: exported API compatible with adapter/openai/v1.0.0
api:check: github.com/victorzhuk/gohan/adapter/otel: diffing HEAD against adapter/otel/v1.0.0
api:check: github.com/victorzhuk/gohan/adapter/otel: exported API compatible with adapter/otel/v1.0.0
api:check: github.com/victorzhuk/gohan/adapter/postgres: diffing HEAD against adapter/postgres/v1.0.0
api:check: github.com/victorzhuk/gohan/adapter/postgres: exported API compatible with adapter/postgres/v1.0.0
```

Seven `diffing HEAD against <module>/v1.0.0` lines and zero `no tag yet, skipped (no baseline to diff against)` lines (five before the freeze).
