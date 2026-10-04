# API compatibility

Normative process for `github.com/victorzhuk/gohan` and the `adapter/*` modules (ADR-0117).

## Versions and timeline

- The root module is `v0.x` through M2 and is tagged `v1.0.0` when M2's exit criteria pass. Within `v0`, breaking changes are allowed but every one is listed in the changelog under *Breaking*.
- Each adapter is its own module and its own version line (`adapter/openai/v0.4.0`). An adapter reaches `v1.0.0` when its conformance suite passes against core `v1`. An adapter's `go.mod` requires the lowest core version its conformance suite ran against.
- Release order is core, then adapters. `examples/` pins exact versions of everything. `go.work` is for development only and is never committed as the source of truth for versions.
- Major bumps are reserved for overhauls, never for a single removal.

## Ports and handles

Every exported interface is one of two kinds, recorded in `docs/design/types.md`:

- **Port** — implemented by users or adapters (`Model`, `Tool`, `Decider`, `SessionLog`, `Runs`, `Checkpoints`, `Journal`, `AuditLog`, `EventLog`, `OutputStore`, `NotesStore`, `Sandbox`, `Workspace`, `Waker`, `CredentialSource`, `ContextProvider`, `Runtime`, `Stepper`, and the rest). From `v1` a port never gains, loses or changes a method. New capability arrives as a **new optional interface** discovered by type assertion, and the spec that introduces it states core's behaviour when the assertion fails. `storetest` and `conformance.*` exercise both paths. `PreemptedLister` (`stores`) and `MemoryStore` (`working-state`) are the first two.
- **Handle** — implemented by gohan only and called by users (`Conversation`, `Flow[In, Out]`). Handles may gain methods in a minor release. Users do not implement handles; `testkit/gohantest` ships the fakes.

Before `v1` a port may still change, but every growth is expressed as an optional interface from the start so the pattern is real, not aspirational.

## Functions, structs, options

- Behaviour is extended with functional options (`Option`, `notes.Option`, …) or new functions; signatures never change within a major.
- New behaviour within a major is opt-in; defaults do not change.
- Exported value structs in core (`RunLimits`, `Caps`, `Usage`, `ModelProfile`, `ToolSpec`, …) are documented as *not comparable, keyed literals only*: fields may be added when the zero value preserves behaviour, and a field of non-comparable type may be added at any time.
- Sentinel errors and error types are part of the API: their identity is stable; their messages are not.

## Deprecation

- `// Deprecated: use X.` is the one comment that is not a *why* and is still allowed (Go tooling reads it).
- A deprecated symbol survives at least two minor releases in `v0` and for the whole `v1` line.

## Gate

- `task api:check` runs `apidiff` for every module against its last tag and diffs `api/gohan.yaml` against the generated `adapter/httpapi` server; the document is versioned with the root module. CI fails on an incompatible change in any module at `v1` or later and only reports for `v0` modules.
- `task spec:types` records the kind of every interface; after the freeze tag it fails when a port's method set changes.
