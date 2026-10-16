# Proposal: m2-data-analyst-example

## Why

The M2 milestone ships the governed sandbox twice over: `2026-10-07-m2-sandbox-governance` landed the `PerSession` tool set with deny-all egress, named-secret injection through `Workspace.SetEnv` and the output cap, and `2026-10-12-m2-sandbox-lifecycle-resume` landed the live handle, the suspension snapshot, the generation-fenced restore and the journaled replay. Every one of those rows is proven by a subtest inside `std/sandbox` or `core`, and no artifact in the repository shows a *program* composing them: an offline example entry under `examples/data-analyst` drives read-only warehouse queries, guards personally identifying rows before anything is persisted or paged, resumes a suspended session on a fresh workspace, and reads a paged slice back out of the output store.

Without that entry the composition stays untested at the level a user actually touches. A per-tool row can be green while the assembled path is unreachable, because nothing wires a `NativeSpec`, a `SandboxPolicy`, a `SecretResolver` and a run that suspends and resumes. The example is the only place where the ordering of those parts — secret injection before `Exec`, the guard before persistence, the snapshot before suspension, the restore before the re-drive, the head excerpt before paging — becomes one observable behavior.

## What Changes

- Add `examples/data-analyst` as an offline, deterministic entry: a native definition registered with `gohan.Build` in the shape `examples/excursions/main.go:29-85` uses (`gohan.Build`, `gohan.NativeSpec`, `gohan.NewNativeConversation`, `conv.Send`, `sessions.Load` on the persisted history), with a `PerSession` `SandboxPolicy` whose `Egress.Allow` is empty, so `run_code` is `Idempotent` and every outbound connection, including DNS, is denied.
- Resolve the warehouse credential through the optional `types.SecretResolver` and inject it only through `Workspace.SetEnv`; the injected value reaches no tool argument, tool result, history message, journal entry, audit record or span.
- Apply the personally-identifying-row guard before any tool result is persisted and before any output is handed to the `OutputStore`, so a masked identifier never enters a stored block, a `File` block `Ref` or a `read_output` page.
- Drive one session through two turns: turn 1 writes a warehouse extract, the run suspends, and turn 2 resumes from the durable session workspace ref without re-uploading the extract.
- Page a stored result back through `read_output(ref, range)` and assert the requested slice, reusing the `working-state.output-paging` row rather than restating it.
- State the offline-vs-real boundary in the package and in the entry README: the deterministic sandbox fixture proves public-path composition only. Real pandas execution, router `Cascade`, provider failover, shared quota, batch yielding and `ClassContextOverflow` backstop behavior are **not** proven by fixture replay. Each of those needs either a named prerequisite owner or an explicit catalog-scope exclusion, and this package names them rather than claiming them.
- Reuse ten existing scenario ids without renaming them, and add one composition id, `sandbox.data-analyst-resume-and-page`, whose requirement block makes the composed ordering observable for the first time. No `Sandbox` or `Workspace` port method is added, and no capability contract changes owner.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `sandbox`: one ADDED requirement, `Data analyst example composition`, carries the composed read-only-query path — deny-all egress, `SecretResolver` injection through `SetEnv`, the guard before persisted output and before paging, the suspend-and-resume workspace carry, and the single new composition scenario `sandbox.data-analyst-resume-and-page`. Every existing `sandbox` requirement is left byte-identical; the ten reused ids keep their names, their requirement blocks and their owners.

## Impact

Affected code: `examples/data-analyst` only (new `main.go`, `main_test.go`, `README.md`, and offline fixtures). Added registry row: `sandbox.data-analyst-resume-and-page` in `openspec/scenarios.json`, with `origin` `m2-data-analyst-example`.

No file under `core`, `std` or `adapter` changes. The `Sandbox` and `Workspace` method sets are untouched, and the durable-enumeration contract stays where `m2-docker-sandbox` put it: the optional `types.SandboxResources` interface with `Enumerate` and `CloseByRef` (`core/types/sandbox.go:97-102`), reached by type assertion, never grown into the port. The frozen port comparison in `m2-api-freeze` is unaffected.

Verification is offline and deterministic: `go -C examples test -json -short -timeout 2m ./data-analyst/... -run '^TestDataAnalystOffline$'` and `timeout 2m go -C examples run ./data-analyst`. Neither reaches a network, a container runtime or an API key.
## Context

The composed data-analyst path crosses the PerSession tool set from m2-sandbox-governance and the suspend/resume lifecycle from m2-sandbox-lifecycle-resume; this change binds their ordering normatively without adding redaction, egress, port, or secret-handling rules.
