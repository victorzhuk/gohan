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

- `Resume` authorizes before it consumes: it checks the transport principal against the session owner, the approval policy and the request's eligibility, and leaves the token pending when it refuses. It no longer continues a run under the originator's identity without that check.
- Resume, crash recovery and preempted recovery run one lifecycle: a resumed run takes a lease, starts a heartbeat, persists a new checkpoint when it suspends again, and reaches a terminal transition before the terminal event is delivered.
- The lease heartbeat runs in production, so a long step or a blocked consumer no longer lets the lease expire, and a lost lease ends the run instead of finishing under it.
- An event that cannot be recorded ends the stream with a single terminal error instead of being delivered first.
- `Send` acquires the run lease before it appends the input, so a refused send leaves the history unchanged, and a duplicate operation reattaches to its recorded run.
- Every call of a turn is gated before any call executes, and a suspending call no longer drops the calls decided after it.
- `ModelStream` joins its provider and buffer helpers before the iterator returns, so an early consumer break releases provider resources synchronously.
- The memory event log is a ring: a saturated append overwrites one slot instead of copying its capacity.
- Session metadata reads require a principal of the owner's tenant, and a hold update applies only after its audit record is written.
- Metric labels are enforced at emission as well as at registration: an unregistered metric is not exported, an unregistered label is dropped, and `session_id`, `run_id`, `subject` and `approver` never reach a sink; `WithTenantLabel()` admits `tenant`.
- The governed seams emit the model, tool, guard and decider spans the contract requires, and count unknown tools and rejected arguments.
- The performance gate requires every gated benchmark and its raw comparator, enforces the frozen absolute budgets on the reference runner, and caches a verdict by the full measurement identity, so an advisory pass can no longer satisfy a strict run.
- The tool-chain benchmark no longer exhausts its own run budget.
- The leak check compares goroutine identities, so an unrelated goroutine exiting cannot cancel a detected leak.
- `adapter/otel` requires the published root version instead of a placeholder with a local replace directive.

### Added

- `AllowAnonymous()`, a build option for unowned function-flow invocation; it invents no tenant and grants no access to an owned session.
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
