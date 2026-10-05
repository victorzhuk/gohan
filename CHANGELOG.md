# Changelog

All notable changes to the `github.com/victorzhuk/gohan` root module are documented here. Format: Keep a Changelog 1.1.0. Breaking changes while the module is `v0.x` are listed under *Breaking* (`docs/design/compatibility.md`).

## [Unreleased]

### Breaking

- `stores.Lease` carries a `Generation` ownership token, and `Heartbeat`, `Finish`, `Suspend` and `Drain` refuse a stale generation with `ErrRunNotActive`: a driver that lost its lease can no longer mutate the run a reclaimer owns. Store implementations mint a new generation on `Start`, `Resuming` and `Reclaim`.
- `stores.Checkpoint` carries `RunID`, and checkpoint data is a versioned envelope. A legacy raw state is still read for the non-approval suspensions that carry a persisted run identity; an approval suspension written by an older build is refused with `ErrCheckpointIncompatible`, because a raw state cannot prove the request it approved.
- A checkpoints implementation that serves approvals must supply the optional `Peek`, `ConsumeIf`, `UpdatePending` and `ResumeReady` surfaces. A conversation configured for approvals refuses to run against a store without them instead of consuming a token unconditionally.
- A conversation that accepts approvals must be built with an approval policy source (`WithConversationApprovalPolicy`, with `std/permission.PolicySource` as the default) and a tool-spec lookup (`WithConversationToolSpecs`). Without them an approval decision is refused rather than granted, and a suspension that cannot resolve the pending call's declaration fails closed instead of persisting an incomplete approval.
- The `Message.Meta` key `gohan.approval` is reserved for the harness's approval receipt; a caller appending it is refused, and the previous approved arguments for a request are read from that receipt instead of from the journal.
- A session-owner exception no longer bypasses the `RiskHigh` approval scope.

### Fixed

- One send emits exactly one terminal event and persists the assistant reply: the reply was delivered both as an event and through the sink, and a turn that ended on a tool batch never appended its final message, so the next turn and the next caller saw an assistant turn that was gone.
- `Resume` keeps the calls it was waiting on, replays the batch from the checkpointed decision rather than the pre-decision per-run scope, and reports the run's outcome: a resumed batch used to restart from the state before the decision, so no call ran, no result landed in history, and the run completed with nothing completed.
- `Resume` authorizes before it consumes: it checks the transport principal against the session owner, the approval policy and the request's eligibility, and leaves the token pending when it refuses. It no longer continues a run under the originator's identity without that check.
- Resume, crash recovery and preempted recovery run one lifecycle: a resumed run takes a lease, starts a heartbeat, persists a new checkpoint when it suspends again, and reaches a terminal transition before the terminal event is delivered.
- The lease heartbeat runs in production, so a long step or a blocked consumer no longer lets the lease expire, and a lost lease ends the run instead of finishing under it.
- An event that cannot be recorded ends the stream with a single terminal error instead of being delivered first.
- A failed run reports one terminal indication: one recorded `TerminalError` and one `(nil, error)` tuple, never a `Done` beside them, and the payload carries the `Seq` a reattaching client resumes from. A reattaching client stops at the run's boundary — after a `Done`, a `Suspended` event or a failure terminal — instead of being handed records beyond it.
- `Attach` refuses a replay cursor older than the event log's retained window with `ErrStaleCursor` and reports the oldest retained sequence, instead of serving a run's history as if nothing had been evicted.
- A run executes on a harness-owned worker and the consumer's iterator only reads what the worker already produced, so a consumer that stops taking events never stops the run and event order no longer depends on how fast the consumer reads.
- A model call hands chunks to the consumer through a bounded FIFO (`DefaultStreamBuffer`, 64 chunks): a provider that produces faster than the consumer reads blocks on its next read instead of racing ahead, and no chunk is dropped or reordered. The idle clock measures provider reads only, a terminal provider error does not wait behind queued chunks, and every blocked read is counted under `gohan.stream.buffer_full`.
- A consumer that takes no event for `RunLimits.ConsumerStall` no longer hangs the run. The default action preempts it: the drive stops at its next safe point, the run is suspended `Preempted` and the client resumes with `Resume(token, Continue())`. `gohan.OnStall(gohan.StallDetach)` continues the run under a harness-owned context, keeping the run's identity, lease and accounting, and the client reattaches through `Attach` from its last `Seq`; a detached run with no event log is refused at construction. Both actions are counted under `gohan.stream.consumer_stalled`.
- `Send` acquires the run lease before it appends the input, so a refused send leaves the history unchanged, and a duplicate operation reattaches to its recorded run.
- Every call of a turn is gated before any call executes, and a suspending call no longer drops the calls decided after it.
- A recorded approval is honored on the next drive: a call a run was already granted runs on resume and on recovery instead of being asked again.
- A resumed or recovered drive runs with the run's own identity, so the tool gate's scope check sees the real run instead of a fresh one; restoring that identity fails closed when the checkpoint's originator carries no scope, and a `Resume` that is offered a request its caller already approved is refused with `ErrApproverNotEligible`.
- `ModelStream` joins its provider and buffer helpers before the iterator returns, so an early consumer break releases provider resources synchronously.
- The memory event log is a ring: a saturated append overwrites one slot instead of copying its capacity.
- Session metadata reads require a principal of the owner's tenant, and a hold update applies only after its audit record is written.
- Metric labels are enforced at emission as well as at registration: an unregistered metric is not exported, an unregistered label is dropped, and `session_id`, `run_id`, `subject` and `approver` never reach a sink; `WithTenantLabel()` admits `tenant`.
- The governed seams emit the model, tool, guard and decider spans the contract requires, and count unknown tools and rejected arguments.
- The performance gate requires every gated benchmark and its raw comparator, enforces the frozen absolute budgets on the reference runner, and caches a verdict by the full measurement identity, so an advisory pass can no longer satisfy a strict run.
- The tool-chain benchmark no longer exhausts its own run budget.
- The leak check compares goroutine identities, so an unrelated goroutine exiting cannot cancel a detected leak.
- `adapter/otel` requires the published root version instead of a placeholder with a local replace directive.
- `Send` and `Continue` mint a collision-resistant run id, so two conversations over one store no longer overwrite each other's run row or mix their event streams.
- `Cancel` authorizes the caller against the session owner before it signals, as every other session-mutating seam does.
- A steer is persisted before it is acknowledged: `SteerApplied` follows a successful append, and the advanced history version reaches the next step.
- `Resume` records its events through the same relay as the initial stream, so a resumed run can be reattached and reports terminal completion.
- Recovery restores the run's own authority — the checkpoint originator, else the session owner — and refuses to execute without one; a credential failure fails the run instead of continuing under the reaper's identity.
- A recovered run can suspend again: it is driven with the same lifecycle wiring and checkpoint save path as a fresh run.
- A consumed preempted checkpoint whose client died before resuming is recovered (ADR-0146).
- The assembly prefix memo applies only to an unfiltered build, so a per-turn tool filter is always evaluated and two filters from one factory can no longer share a cached request.
- The scenario coverage gate counts only executed passing subtests: a skipped or failed test no longer satisfies its scenario.
- The API comparison ignores a parameter rename while still reporting type, arity, variadic, result and method-set changes.

### Added

- Governed native construction: `WithNativeAgent(NativeSpec)` registers a flow definition with `Build`, and `NewNativeConversation(stack, name, opts…)` returns a `Conversation` built from the resolved configuration. A service writes a definition instead of passing a runtime and a stepper; `Build` resolves each definition once and refuses an invalid one before any provider call, and the constructor refuses an unregistered name before any invocation. The definition carries the model profile, instruction, tools, assembler, both chains, the run limits and the flow's own tool decider.
- `Explain` projects that same resolved configuration — profile, strategy plan, both chains' steps, tools, prompt set, limits and release — with zero provider calls and no run budget spent, from a flow name or a handle.
- Per-run accounting: each run carries its own ledger, reached from the run's invocation context, with the executable limit policy in `std/limit`. Reaching the turn or tool-call ceiling stops the run at its effect boundary with `Done{StopLimit}`, while a cost overrun or an expired wall clock aborts it.
- `AllowAnonymous()`, a build option for unowned function-flow invocation; it invents no tenant and grants no access to an owned session.
- Every shipped example builds its governed native path: all five example packages register a flow with `WithNativeAgent`, construct the handle with `NewNativeConversation` and drive it offline against the memory stores, with the excursions flow carrying its own decider so a denied booking executes nothing and its ordered `not_executed` result reaches the persisted history.
- `task spec:gate` runs the scenario coverage gate over every module in the workspace, and `task spec` regenerates the type index before it.

## [0.1.0] - 2026-10-05

### Added

- Attached and detached runs: a consumer attaches to a live run, catches up from the event log in order, and reattaches after a disconnect, while a detached run keeps going and coalesces its deltas in that log.
- Crash recovery: `Recover` reclaims a stale run and replays it from the last persisted turn — completed calls from the journal, reserved ones under their pinned key, never-started ones normally — resumes preempted runs before stale ones, and fails a run that cannot be re-run headlessly without breaking the session.
- `Inspect` answers a run's stored state and the input a suspended run waits for, from stores only and owner-checked, so any pod can serve it.
- Graceful shutdown and health: `Shutdown` refuses new runs, stops in-flight runs at persisted safe points, flushes pending writes and reports `ShutdownIncomplete` when the grace period ends with runs still in flight; `Ready` and `Health` report readiness during shutdown and under schema skew.
- A preempted run resumes through `Continue()` without approval, and a run stopped mid-call loses only its partial model call.
- Telemetry: a dependency-free port in the core with canonical `gohan.*` keys and emission from the governed call sites, a convention layer that renames keys per backend without a core change, TTFT/TPOT and loop-detection metrics with a label allowlist, and an `adapter/otel` module exporting the port over OpenTelemetry.
- Per-run logging carrying the canonical attributes, with message content, credentials and raw values excluded by construction at every level.
- Shared testkit: a scripted model, a cassette recorder and replayer with strict, by-turn and rerecord modes, fakes, fault injection, a goroutine-leak profile, and conformance suites for the model, runtime, chain and flow contracts.
- Host exec runner: typed argv, an explicit environment allowlist, a timeout that kills the whole process group, output capped per stream with a marker and the full content stored behind a retrievable reference.
- Notes: `notes_write` plus a context provider that reassembles saved notes into the session slot, so a note survives a run reset and a truncated history.
- An `examples` module of offline, key-free examples: a quickstart, an excursion with a visible permission decision, refunds redelivery, a durable workflow resumed from its journal, and a live denial that beats a later approval.
- Benchmarks for the tool chain, a raw call, the model chain and the prefix build, with a regression gate comparing a base and a head commit and caching its verdict by the commit pair.
- `task api:check` diffs each module's exported API against its last tag, and says so explicitly when a module has no tag instead of reporting a comparison it did not make.
- Core types and ports declared **v1-candidate** (M0.5): the port method sets, handle shapes (`Conversation`, `Flow[In, Out]`), sentinel/typed error set and the optional-interface growth pattern are fixed in shape; the root module is not tagged `v1.0.0` until M2's exit criteria pass. See `docs/design/api-review-m0-5.md`.
- M0.5 API review published after the three offline acceptance processes (`examples/kafka-refunds`, `examples/temporal-travel`, `examples/camunda-invoice`) passed against the memory stores (ADR-0083, ADR-0136). The `api/gohan.yaml` / `adapter/httpapi` half of the `api:check` gate is recorded as a gap and lands in M4.

### Changed

- The wall-clock budget is measured in elapsed monotonic time, so neither a store timestamp nor a system clock jump can move it.
- A repeated prefix build for the same request is allocation-free: the assembler memoises instead of allocating on every call.

[unreleased]: https://github.com/victorzhuk/gohan/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/victorzhuk/gohan/releases/tag/v0.1.0
