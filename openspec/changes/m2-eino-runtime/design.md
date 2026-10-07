# Design: m2-eino-runtime

## 1. The shipped seam

A foreign runtime enters gohan through four shipped symbols and no others.

| Symbol | Location | What it fixes for a backend |
|---|---|---|
| `runtime.Runtime` | `core/runtime/runtime.go:49-52` | the backend's `Start`, `Step`, `Name`, `Granularity` |
| `runtime.StepGranularity` | `core/runtime/runtime.go:57-61` | `GranularityTurn` is the value an eino backend declares |
| `runtime.AgentRun` | `core/runtime/runtime.go:70-82` | the per-run wiring `Drive` hands the backend |
| `runtime.State` | `core/runtime/runtime.go:28-37` | the resumable position, including `Backend []byte` |
| `Preemptor.Runtime` → `preemptableRT` | `core/preemption.go:62-76` | the wrapper every run is driven through, native or foreign |
| `WithNativeAgent` | `core/native_options.go:48-52` | where the resolution-time registration pattern lives; the foreign hook sits beside it |

`Drive` (`core/drive.go:45`) and `DriveLifecycle` (`core/drive_lifecycle.go:362`) take a `runtime.Runtime` and a `runtime.AgentRun`. Neither branches on which backend it holds, which is the point: the loop, the limit checks, the event log writes, the mailbox drain, the suspend ordering and the `Done`-after-`Finish` ordering are the same code for every backend.

## 2. The construction seam must be extended first

The seam above is a *stepping* seam. The seam a backend actually dies on is *run construction*, and it is not shipped in a usable form.

`NewConversation(stack, spec, rt, opts...)` (`core/conversation.go:171`) installs `rt` as `c.rt`, and every run path reads it:

- `core/stream_lifetime.go:150-152` builds `ag := runtime.AgentRun{Input: input}` — no `Model`, no `Tools`, no `Assemble`, no `History`, no `Save`.
- `core/resume.go:330` builds `runtime.AgentRun{}` — not even `Input`.
- `core/recover.go:246` builds `runtime.AgentRun{Save: …}` — only the checkpoint sink.
- Only `c.newRun != nil` fills a governed run, and that branch hard-codes `rt = runtime.NewNative()` (`core/stream_lifetime.go:157`); `c.newResumeRun` (`core/native_conversation.go:82`) returns `runtime.Runtime` for the same reason.

So today a foreign runtime reaches the driver with an `AgentRun` carrying no governed model and no governed tools. Every component the spec says it must call — the governed model, the gated tools, the run-scoped sink's decorators — is absent, and `conformance.Runtime` fails on the first scenario for a reason that has nothing to do with eino.

**Decision.** The construction seam is extended before any adapter work. `conversation` grows one foreign builder beside `newRun`/`newResumeRun`, and the native branch stops forcing `runtime.NewNative()` for that path. The native paths keep their exact behaviour: same ledger seeding, same gate and reservation closure, same re-drive. The new hook is optional, so a flow that does not set it keeps today's fixed foreign runtime, and no port gains a method — `conversation` is a struct, not a frozen interface, and the option is `ConversationOption`.

Three paths must be covered, not one: send, resume and recovery. A backend that suspends and is later recovered must re-drive through the same builder, or the resumed run silently loses its governed components.

## 3. The port method set to settle before the v1 freeze

`m2/design.md` §4 assigns the compatibility check to `m2-api-freeze`, and M2 is the freeze point (`docs/overview.md:110-112`, `docs/design/compatibility.md:40-46`). What this change must settle before that tag:

1. `runtime.Runtime` — `Start`, `Step`, `Name`, `Granularity`. Frozen as is; no method is added for a backend's benefit.
2. `runtime.AgentRun` — `Model`, `Tools`, `Assemble`, `History`, `Input`, `Save`, `ModelEffect`, `BatchEffect`, `Mode`. A field may be added only if two shipped backends (`native`, `eino`, then `adkgo`) both need it; otherwise it is an optional interface.
3. `runtime.State` — `Backend []byte` is the frozen carrier for backend checkpoint bytes; `stores.Checkpoint.Backend`/`BackendVersion` (`core/stores/checkpoint.go:44-45`) are frozen beside it. No new per-backend state field.
4. `runtime.StepGranularity` — two values, `GranularityEffect` and `GranularityTurn`, frozen. A backend that needs a third value is refused, not accommodated.
5. `Runtime.Granularity` reach `Explain`: `chains.Explanation.Granularity` (`core/chains/explain.go:54`) exists and the native path writes `"effect"` (`core/explain.go:47`). The eino backend's `"turn"` must appear there, so `Granularity()` becomes an observable, not a declaration nobody reads.

`spec:types`' port-method-set clause is still unimplemented (`docs/design/compatibility.md:46`), so nothing mechanically prevents drift after this change lands. That is `m2-api-freeze`'s gap to close, not this change's.

## 4. Boundary rules

**No second limit loop.** The adapter advances turns; it never counts them and never checks `RunLimits`. `Drive` applies limits after every effect and emits `LimitWarning` at `SoftRatio`; `runtime.max-turns` and `limits.limits-under-foreign-backend` are proven by observing that an eino-driven run stops with `Done{Reason: StopLimit}` for the same reason a native run does. An adapter-side counter is a second limit loop and is refused.

**No adapter-side lifecycle.** `Shutdown`, `Ready`, `Health`, the mailbox drain, the lease, `Runs.Start`/`Suspend`/`Finish` and the checkpoint ordering stay in `core`. A backend that catches `SIGTERM`, that holds a lease, or that emits `Done` itself violates the run-lifecycle ordering scenario `runtime.done-after-finish` names. Preemption reaches a backend only through `preemptableRT.Step` (`core/preemption.go:85-115`), which cancels the step ctx and waits for the step to settle. Cancellation therefore lands inside the turn, not only between two turns: the adapter passes the run ctx unchanged into every model and tool call, so the cancelled turn aborts its in-flight model call, drops the deltas it had not appended, and starts no further component. What waits for the next safe point is the *publication* — checkpoint, `Runs.Suspend`, `Suspended{Preempted}`, the handoff token — and a `Turn`-granularity backend reaches that safe point as soon as the cancelled turn has returned. The one thing that outlives cancellation is a shielded side effect, which runs to completion, is journaled and appended, and is then followed by preemption. A backend that ignores the ctx and finishes its graph violates `runtime.shutdown-partial-model-call-dropped`; one that abandons a shielded tool violates `runtime.shutdown-side-effect-completes`.

**No runtime-emitted component events.** `TextDelta`, `ToolStarted`, `ToolFinished` and the turn counter come from the governed decorators through the run-scoped sink `Drive` installs in ctx. `Step` returns runtime-originated events only (`core/runtime/runtime.go:42-47`). An adapter that emits `TextDelta` because eino streamed text would double the events and move the turn counter off `Drive`.

**No ungoverned path into the model or the tools.** A graph node or a tool that calls a provider directly bypasses the gate, the journal, the limit ledger and the cancel shield. `runtime.foreign-tool-under-native` exists to keep the mixing impossible in the native direction: an imported eino `InvokableTool` runs the full chain under the native runtime. The other direction — `einoflow.ModelNode`/`ToolsNode` carrying governed components into the graph, so `runtime.governed-components-in-eino-graph` holds — is **owned by `m2-eino-graph-native-resume`**, as `m2/design.md` §2.1 already assigns. This package carries no acceptance task for that row: `einoflow` node builders, the eino graph flow and `BackendVersion` mismatch behaviour are in that change's scope and in this change's *not in scope* list, so a task here would bind excluded work. What this package owes is the run-construction seam and the `runtime.Runtime` that row's graph needs: the delta's Component mixing requirement is restated unmodified so the scenario block stays byte-identical, and the graph side of the acceptance is proven there.

**Evidence sets are named, not implied.** The ids this change binds fall into three sets, and each set is proved in the module that owns the claim (§7). A row does not move because a neighbouring module's test happens to be green.

**Native scheduling is not required inside eino.** The batch protocol is the native runtime's governed turn path (`openspec/specs/runtime/spec.md:258`). eino schedules its own tool calls, and the spec already says a foreign runtime does. `runtime.readonly-parallel`, `runtime.side-effects-sequential`, `runtime.parallel-tools-cap` and `runtime.batch-*` stay native regression obligations: they prove the native path did not regress, not that eino schedules calls the native way. The clauses that hold for eino are the per-call gate and the limits `Drive` applies.

## 5. Resume

`DriveResume` (`core/drive.go:60`) rebuilds `State` from the checkpoint, appends the resume input — approved, rejected, edited or delivered — and re-executes `Step` from there; completed calls are answered from the journal and never re-executed. For an eino ADK or compose graph the backend's own checkpoint bytes live in `State.Backend`, serialized into `Checkpoint.Data` with `BackendVersion`, and gohan consumes the token first, then calls the backend's resume. `ResumeNative` resolution and the version-mismatch behaviour (`core/stores/checkpoint_compatibility.go:41-49`) ship with `m2-eino-graph-native-resume`; this change makes the carrier reachable and keeps `runtime.preempted-resume-continue` and `runtime.suspend-order` true for the runtime level.

The message bridge is the one place where fidelity is a claim, not a property. `runtime.message-round-trip` names eino *and* adk-go. This change proves the eino leg — `Message` → `*schema.AgenticMessage` → `Message`, block order preserved, each block matching the declared fidelity, `Reasoning` only through its own provider — and does not claim the id closed; `m2-eino-bridges` owns the adk-go leg.

## 6. Risks

**Registry coverage is not proof that eino passes.** The registry is a claim list (`openspec/scenarios.json` plus the acceptance matrix in `m2/design.md` §2), and a row that moves off `missing` on the strength of a green native test proves nothing about the adapter. The rows this change closes on the eino path must each be named by a subtest that drives the eino runtime, in `adapter/eino` output. But the bound set is not uniform, and saying so is the point: the delta carries **27 scenario blocks**, not 29. `runtime.readonly-parallel`, `runtime.side-effects-sequential`, `runtime.parallel-tools-cap`, `runtime.sequential-tools-hint`, `runtime.batch-gate-first` and `runtime.batch-limit-before-execute` are native scheduling obligations kept as native regressions (§4), so they are proved in `core`, not in the adapter. The two ids with no test anywhere on the tree (`flow.backend-swap`, `limits.limits-under-foreign-backend`) are bound by subtest name only: they stay defined in the `flow` and `limits` specs, the runtime delta does not redefine them, and their acceptance is a claim about both backends that holds only when one suite runs unchanged against both. `runtime.message-round-trip` is proved here for the eino leg alone and the id is not claimed closed. §7 names the three evidence sets that follow from this.

**A `Turn` boundary hides sub-turn cancellation.** `preemptableRT` cancels the step ctx and waits for the step to settle. Under `Effect` granularity the worst case is one model call; under `Turn` it is a whole eino turn with its own graph and tool calls. The delta therefore makes the cancelled turn stop rather than merely unwind at the turn edge (§4): the run ctx reaches every model and tool call, the in-flight model call aborts, its unappended deltas are dropped, and no further component of that turn starts. The shielded side effect inside that turn is the deliberate exception `runtime.shutdown-side-effect-completes` names. If the adapter's turn does not return on ctx cancellation, `Shutdown` degrades to `ErrShutdownIncomplete` on every in-flight run; the failure mode is a backend that swallows the ctx and finishes its graph, which is why the delta forbids continuing the graph after cancellation rather than leaving it to the adapter.

**A second loop creeps in through convenience.** eino offers graph-level retry, a `ReAct`-style loop and per-node timeouts. Any of them applied above `Step` becomes a second loop with its own counts, and `runtime.max-turns` stops being provable. The tell is a metric or an event that only an adapter can emit.

**eino v0.9 surface drift.** Q4 (`docs/overview.md:122`) fixes the `TypedChatModelAgent[*schema.AgenticMessage]` path and flags `CheckPointStore` methods and graph introspection as unverified. The runtime's own construction path is stable; the resume path is not, which is why `State.Backend` carries opaque bytes and a version rather than a typed backend handle.

## 7. Evidence this change owes

The bound set is 27 `runtime.*` scenario blocks in the delta plus two subtest-bound cross-capability ids. It does not all land in one module, so the evidence is declared as three sets. A row moves off `missing` only on the evidence its own set names.

**Set A — native regressions, proved in `core`.** These six rows are the native runtime's own scheduling contract. `Drive` and the governed batch path own them; no adapter assertion is permitted to stand in for them, because eino schedules its own calls and a passing adapter test would prove nothing about the native path. Evidence: the named subtests in `core/runtime_batch_test.go` and `core/runtime_schedule_test.go`, run by `go test -short -timeout 2m ./core/... -run 'TestRuntimeBatchProtocol|TestRuntimeScheduling'`.

| id | test | subtest |
|---|---|---|
| `runtime.batch-gate-first` | `TestRuntimeBatchProtocol` | `runtime.batch-gate-first` |
| `runtime.batch-limit-before-execute` | `TestRuntimeBatchProtocol` | `runtime.batch-limit-before-execute` |
| `runtime.readonly-parallel` | `TestRuntimeScheduling` | `runtime.readonly-parallel` |
| `runtime.side-effects-sequential` | `TestRuntimeScheduling` | `runtime.side-effects-sequential` |
| `runtime.parallel-tools-cap` | `TestRuntimeScheduling` | `runtime.parallel-tools-cap` |
| `runtime.sequential-tools-hint` | `TestRuntimeScheduling` | `runtime.sequential-tools-hint` |

**Set B — eino acceptance, proved in `adapter/eino`.** Every remaining bound row is proved by a subtest that drives the eino runtime, named by its scenario id in the adapter module's `go test -json` output. This set covers the lifecycle, suspension, preemption, health, limit and cross-backend rows: `runtime.plain-answer`, `runtime.tool-round-trip`, `runtime.parallel-calls-ordering`, `runtime.max-turns`, `runtime.cancellation`, `runtime.suspend-order`, `runtime.append-before-tool`, `runtime.done-after-finish`, `runtime.shutdown-rejects-new`, `runtime.shutdown-preempts-at-safe-point`, `runtime.shutdown-side-effect-completes`, `runtime.shutdown-partial-model-call-dropped`, `runtime.preempted-resume-continue`, `runtime.shutdown-grace-exhausted`, `runtime.batch-ask-after-allowed`, `runtime.batch-one-result-per-call`, `runtime.readyz-false-on-schema-skew`, `runtime.readyz-false-during-shutdown`, `runtime.foreign-tool-under-native`, plus the two subtest-bound ids `flow.backend-swap` and `limits.limits-under-foreign-backend`. `runtime.governed-components-in-eino-graph` is **not** in this set: it is owned by `m2-eino-graph-native-resume` (§4). Evidence: `conformance.Runtime` green with `newRuntime` bound to the eino backend, the suite's own leak check passing, and each id above named in the adapter's `go test -json` output.

**Set C — the message bridge, proved for the eino leg only.** `runtime.message-round-trip` names eino *and* adk-go. This change proves the eino leg: `Message` → `*schema.AgenticMessage` → `Message`, block order preserved, each block matching the declared fidelity, `Reasoning` only through its own provider. Evidence: the `runtime.message-round-trip` subtest in `adapter/eino/message_bridge_test.go`. The id is **not claimed closed**; the adk-go leg is `m2-eino-bridges`' (M3).

Beyond the three sets: `Explain` prints the eino granularity (`turn`) where the native flow reports `effect`, proved by `go -C adapter/eino test -short -timeout 2m -run TestExplain ./...`; and `task lint`, `task test` (`-short`), `task spec` are green with the module in the coverage sweep.
