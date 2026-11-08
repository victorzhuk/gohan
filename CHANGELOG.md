# Changelog

All notable changes to the `github.com/victorzhuk/gohan` root module are documented here. Format: Keep a Changelog 1.1.0. Breaking changes while the module is `v0.x` are listed under *Breaking* (`docs/design/compatibility.md`).

## [Unreleased]

### Changed

- External `Resume` requires the optional `stores.CheckpointRuns` run-store surface; custom stores without it return `ErrCheckpointIncompatible` without consuming the token.
- The recommended policy values `Build` applies moved to `std`, which owns them as `std.SunsetNoticeWindow`, `std.LatencyTimeouts`, `std.ControlWait` and `std.StreamBuffer`. Each value is unchanged and `core` keeps a mirrored default with the same value, so no caller has to pass an option; a parity test fails the moment one side drifts.
- The cache outcome seam is canonical in `core/chains` (`chains.ExtraKey`, `chains.Outcome`, `chains.Mark`, `chains.OutcomeOf`). `std/cache` re-exports the four names, so existing importers compile and behave unchanged.

### Fixed

- Anthropic completes tool blocks by content-block index, emits identity-only argument previews, and rejects malformed explicit completions as protocol errors.
- OpenAI stream termination preserves terminal reasons, reports usage once, stops callbacks after consumer rejection, handles missing cache details and reports cancellation during silent reads.
- The Eino foreign runtime installs call-ID middleware so backend identity reaches governed admission, permission and journal operations.
- The `einoflow` graph collector no longer folds argument previews into executable calls.
- Shipped provider fixtures now carry real wire boundaries, and both provider packages run the legacy conformance suite again.
- Recovered runs record their redacted event tail for `Attach`, including the terminal boundary.
- A failed terminal store write surfaces an error instead of emitting `Done`.
- Admitted tool calls retain their cumulative budget across resume and recovery; prepaid pending calls are not charged twice.
- Recovery discovery excludes obsolete or expired checkpoints and non-preempted suspensions before applying limits; memory checkpoint discovery requires a bound run store.
- Session lease acquisition refuses competing live runs, and reclaim accepts only expired running or resuming runs. Checkpoint TTL is configured independently of the lease, with explicit expiry preserved.
- PostgreSQL uses database time for lease, checkpoint, recovery discovery, workspace-owner and notice-claim operations instead of the process clock.
- Ordinary approval receipts authorize only their recorded call and complete arguments, not later calls in the same session.
- Resume revalidates current approval policy and recorded quorum votes before execution, leaving the token pending when a decision is refused.
- Approval and journal fingerprints use exact numeric canonicalization and the `exact-v1:` format; legacy rounded scoped grants require renewal, and legacy unresolved journal entries require exact full-argument proof before reuse.
- The host command runner advertises and uses one effective effect: `exec.WithEffect` takes precedence, followed by a nonzero declaration and the `SideEffect` default.
- Journaled tool calls receive the reserved downstream idempotency key on initial execution as well as replay.
- `ExcludeFields` also clears excluded arguments during typed decoding, including nested fields, so supplied identity values do not reach the tool function.

- `core` no longer imports `std`: the cache key seam it used now lives in `core/chains`, and the depguard rules that were meant to hold the line match the real import paths (the `std/**` prefix never matched, and the shared root allow admitted `adapter/*`).

## [1.0.0] - 2026-10-12

### Breaking

- `stores.Lease` carries a `Generation` ownership token, and `Heartbeat`, `Finish`, `Suspend` and `Drain` refuse a stale generation with `ErrRunNotActive`: a driver that lost its lease can no longer mutate the run a reclaimer owns. Store implementations mint a new generation on `Start`, `Resuming` and `Reclaim`.
- `stores.Checkpoint` carries `RunID`, and checkpoint data is a versioned envelope. A legacy raw state is still read for the non-approval suspensions that carry a persisted run identity; an approval suspension written by an older build is refused with `ErrCheckpointIncompatible`, because a raw state cannot prove the request it approved.
- A checkpoints implementation that serves approvals must supply the optional `Peek`, `ConsumeIf`, `UpdatePending` and `ResumeReady` surfaces. A conversation configured for approvals refuses to run against a store without them instead of consuming a token unconditionally.
- A conversation that accepts approvals must be built with an approval policy source (`WithConversationApprovalPolicy`, with `std/permission.PolicySource` as the default) and a tool-spec lookup (`WithConversationToolSpecs`). Without them an approval decision is refused rather than granted, and a suspension that cannot resolve the pending call's declaration fails closed instead of persisting an incomplete approval.
- The `Message.Meta` key `gohan.approval` is reserved for the harness's approval receipt; a caller appending it is refused, and the previous approved arguments for a request are read from that receipt instead of from the journal.
- A session-owner exception no longer bypasses the `RiskHigh` approval scope.
- `std.Preset.PromptMiddleware` is removed: a preset declares its prompt set through `gohan.WithPrompts`, and a model chain takes the renderer as `std.PromptMiddleware(promptSet)` (`std/prompts.go`).
- `runtime.ErrBatchOverrun` is removed and moved to `types.ErrBatchOverrun`, so `core/chains` no longer depends on `core/runtime`; callers importing the sentinel from `runtime` must switch to `types`.
- `gohan.FlowFunc` gained a variadic `...FlowOption` tail (`gohan.AllowAnonymousFlow` and friends). Existing positional call sites still compile; wrappers that pin the exact function type do not.
- `std/flow.Extract` and `std/flow.Classify` now take the resolved `*gohan.Stack` and a profile name instead of a `types.Model`: `Extract[Out](stack, "cheap", opts...)` and `Classify[L](stack, "cheap", labels, opts...)`.
- The `chains.Explanation`, `runtime.AgentRun` and `runtime.Batch` structs gained fields (`Explanation`: `Fallback`, `Granularity`, `Limits`, `Sample`, `Strategies`, `ToolSteps`; `AgentRun`: `ModelEffect`, `BatchEffect`; `Batch`: `Scheduler`). Unkeyed composite literals over these types break; keyed literals are unaffected.
- `chains.ToolInfo` gained an `Egress` field (`*types.EgressPolicy`): `Explain` renders each tool's declared network policy, `nil` for an undeclared policy and non-nil zero for deny-all. Unkeyed composite literals over `ToolInfo` break; keyed literals are unaffected.
- The example-local shapes `examples/excursions.Trip` and `examples/temporal-travel`'s `(*LiveFlags).Snapshot` changed with the governed path; code copying those examples updates with them.
- `Build` fails when a model profile's `Sunset` falls inside the notice window — 30 days by default, set with `gohan.WithSunsetNoticeWindow`. A flow pinned to a retiring model no longer builds and fails later at the provider instead: migrate the flow to the profile's declared `Successor` before the sunset, or pass `gohan.WithSunsetWarnOnly` to downgrade the failure to a warning while the migration is staged. `Build` also refuses a flow's static model options the declared successor's `Caps` cannot express, and a deprecated model call is never silent: it routes to the successor or returns `*types.ModelError{Class: types.ClassDeprecated}` with no fallback assistant message.
- `stores.SessionMeta` and `stores.SessionPatch` carry a `Handoff` field (`types.HandoffContext`) for the human-handoff lane. Unkeyed composite literals over those types break; keyed literals are unaffected.

- The grant contract is declared by `core/permission`: `permission.Grant` (tool, fingerprint, subject, approver, expiry) and `permission.GrantStore` moved there, and `std/permission` keeps the implementation and the policy — `MemoryGrantStore`, `MaxGrantTTL`, `ApproveGrant`, `HasGrant`, `GrantedByScope` and `ErrGrantRefused` (ADR-0162). An approval store or a grant store wired from `core` must satisfy the core-declared interface; `core` and `core/permission` import no `std` package and declare no grant TTL of their own.
- `exec.DefaultMaxOutput` and `sandbox.DefaultMaxOutput` are removed: the per-stream inline cap default is `outputs.DefaultMaxOutput` (64 KiB), the one source every tool, the sandbox spiller and `web_fetch` resolve. Code referring to either removed constant switches to `outputs.DefaultMaxOutput`; there is no alias.

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
- A native batch that asks for approval settles one ask at a time: each checkpoint carries exactly one active request, an approval, rejection or edit touches only the head of the durable ask queue, and no model call or tool reservation runs between asks. A queued fingerprint identical to an already-approved one still requires its own approval.
- Results of calls already settled before an approval suspension survive the resume and the recovery: a mixed batch (allowed, denied, asked) keeps its completed results in history, and the final settlement appends only the results not already persisted, never a duplicate assistant call or result.
- Turn accounting is authoritative: `State.Turn` counts one increment per permitted model call across resume and recovery, `MaxTurns` stops the run before the next model phase with one `Done{StopLimit}`, and a native batch overrun translates to `types.LimitExceededError` at the run boundary while low-level callers keep `types.ErrBatchOverrun`.
- A run recovered in `Running` state reconstructs its phase from durable history — the latest assistant tool turn minus the recorded results — instead of failing to decode an empty backend, and replays without appending the same assistant calls or results twice; uncommitted read-only work may repeat, as documented.
- Recovery refuses to execute when the checkpoint originator or stored session owner cannot be resolved: it fails the run through the abandon path instead of continuing under the ambient reaper's principal or credentials.
- The assembler receives the resolved configuration — the registered instruction, profile, schemas, actual run identity and history — instead of only history, and the tool `Step.Applies` predicate is evaluated against that resolved spec snapshot before middleware execution, so execution and `Explain` agree.
- A run reports its charged cost: the ledger is bound before the lifecycle is constructed for `Send`, `Continue` and resume, so the initial `Done.Cost` includes what the run actually spent.
- Stream delivery uses one bounded handoff between the run worker and the consumer, so a fast producer can no longer queue an unbounded backlog; explicit abandonment wakes blocked empty/full waits even when `ConsumerStall` is zero, and a detached run stops retaining undelivered output while it keeps recording.
- Stall detection arms only while a consumer callback is outstanding: a slow producer or a silent tool no longer preempts a draining consumer, and the deadline measures waiting-for-output as zero stall time.
- A resumed run executes on the same private worker as an initial run — with the resolved `ConsumerStall` limit, the restored ledger bound before the lifecycle starts, and a post-acquisition preparation failure releasing the lease — and reports one durable failure terminal plus one live error tuple instead of failing silently.
- `Attach` catches up across conversation instances over shared stores: a local waiter notification remains the fast path, and a replay poll observes another conversation writer's live records so an ordered tail arrives without duplicates.
- The wrapped event ring expires in logical ring order: a saturated expiry preserves the retained payloads and their replay sequence instead of overwriting unread slots, keeps the per-run next sequence, and still refuses a stale cursor.
- Untrusted ingress refuses the reserved `gohan.approval` metadata before any session append, control-human append, mailbox signal or operator mutation, and a shaped receipt without approvers grants nothing.
- `Send` and `Continue` install the minted run identity — run id, session, flow, mode, principal — into the run record and the execution context before the run starts, so scope checks and journal keys never see an absent identity, and an ambient `RunInfo` cannot override the acquired run.
- Resolution copies the resolved definition slices (`Tools`, `ToolChain`, `ModelChain`) instead of handing out caller-owned backing arrays, so a caller mutating a definition after registration cannot change execution.
- A sub-flow invoked as its parent's tool no longer inherits the caller's idempotency key or approval, and a detached drive re-issues its credential for the copied principal instead of reusing the caller's token.
- Native replies carry the model origin, matching the function-flow path.
- Tool output storage is one ordered boundary: sanitize, resolve the effective cap, store what exceeds it, then render the excerpt and its marker (`outputs.StoreOutput`). Sanitization runs before the cap check, before `OutputStore.Put` and before the excerpt, so no raw byte reaches the store; a second pass over already-redacted bytes counts no leak twice; a body over the cap with no store wired returns a bounded excerpt naming the dropped bytes instead of the whole body; and `web_fetch` bounds the rendered text — status line plus body — so the status prefix cannot push inline content past the tool's cap.
- The HTTP egress response lifetime follows the body: the policy time limit is released by the response's release closure instead of the request return path, every early refusal releases it, `Close` is idempotent and a terminal read releases before the caller closes, and the per-request transport's idle connections are closed on release. Rebinding, redirect, proxy and checked-dial behaviour is unchanged.
- Postgres store contracts: migration `00005_output_claims.sql` makes output ownership per session claim with a legacy tenant/subject claim fallback, so a shared ref survives erasing one session and the last claim removes the bytes; session listing selects its membership and filters in SQL and paginates over a materialized, owner-scoped, `LeaseTTL`-expiring cursor instead of a bare `session_id`; `Journal.Reserve` and `ReserveTx` insert first under a unique constraint instead of checking then writing; `Runs.Start` serializes a non-empty operation id across sessions; `Runs.RunForSession` finds the live run for a session so `ActiveRun`, `Cancel` and `Steer` reach a cross-pod run; notice claiming locks the rows it claims; and deleting a session drops its dependents in one transaction.

### Added

- Governed native construction: `WithNativeAgent(NativeSpec)` registers a flow definition with `Build`, and `NewNativeConversation(stack, name, opts…)` returns a `Conversation` built from the resolved configuration. A service writes a definition instead of passing a runtime and a stepper; `Build` resolves each definition once and refuses an invalid one before any provider call, and the constructor refuses an unregistered name before any invocation. The definition carries the model profile, instruction, tools, assembler, both chains, the run limits and the flow's own tool decider.
- `Explain` projects that same resolved configuration — profile, strategy plan, both chains' steps, tools, prompt set, limits and release — with zero provider calls and no run budget spent, from a flow name or a handle.
- Per-run accounting: each run carries its own ledger, reached from the run's invocation context, with the executable limit policy in `std/limit`. Reaching the turn or tool-call ceiling stops the run at its effect boundary with `Done{StopLimit}`, while a cost overrun or an expired wall clock aborts it.
- `AllowAnonymous()`, a build option for unowned function-flow invocation; it invents no tenant and grants no access to an owned session.
- Every shipped example builds its governed native path: all five example packages register a flow with `WithNativeAgent`, construct the handle with `NewNativeConversation` and drive it offline against the memory stores, with the excursions flow carrying its own decider so a denied booking executes nothing and its ordered `not_executed` result reaches the persisted history.
- `task spec:gate` runs the scenario coverage gate over every module in the workspace, and `task spec` regenerates the type index before it.
- Session titles: after a session's first completed run, a governed child run titles the exchange and writes the result through the session index. A rename always wins, a session is titled at most once, and a fork inherits the parent's title marked as edited.
- The human-handoff lane: the `request_human` tool ends a run as a terminal `Done{StopHandedOff}` carrying a `HandoffContext` — never a suspension — and `TakeOver`, `OperatorSend` and `HandBack` move control between the operator and the agent. Operator turns are fenced in the model request and history is never rewritten.
- Feedback: `Stack.Feedback` records a signal per name, idempotently and owner-checked, appends a `feedback` audit record and a `FeedbackRecorded` event, and emits `gohan.feedback` with the labels `name` and `flow`.
- Blob provider id reuse: a blob uploads once per profile and later turns send the provider's id instead of the bytes. The ledger survives a resume on another store, an id from a different provider is never reused, and oversized input spills to the content store before it is persisted.
- The `adapter/postgres` module joins the workspace: the session log, runs, checkpoints, journal, audit, outputs, events, notes and redaction map ports on Postgres, with embedded migrations and an atomic journal seam that commits a tool write and its journal entry in one transaction.
- Scoped approvals: `ApproveScope(ttl)` resumes a suspended call with a granted scope, `ApprovalGrants(store)` binds the store, `stores.ResumeInput.ScopeTTL` carries the lifetime, and `Resume` writes the grant and its `granted_by_scope` audit record before the approved call executes. A grant is keyed on the verifying approving subject, never on the checkpoint's originator, its expiry derives from the approval instant rather than the recorded request expiry, and the bound store's TTL policy caps whatever the caller asked for (ADR-0162).
- Persisted compaction: a summary is appended with a deterministic publication identity (`stores.CompactionPublicationID`), the assembly collapses the covered history into one message, the same stage writes the `Compacted` audit record and the `Compacted` event, and every provider call reconciles a publication interrupted before its sinks landed — bound at each provider invocation, so a configured fallback or hedge path cannot bypass it. `WithCompactionFallback` and `WithCompactionGuard` are the build gates: a flow whose policy carries a compactor with no resolvable token count, no validating fallback or no guard is refused at `Build` by name and profile, and a blocked summary persists nothing, applies the injected fallback once and still refuses the provider call when the request stays over budget (ADR-0163).
- `task security:scan` runs `govulncheck` in every workspace module, taking the inventory from `go list -m` and failing closed on an empty inventory or a non-zero scan. The CI `security` job runs that task instead of a root-only scan, so every module of the workspace is scanned.

### Known limitations

- A native approval checkpoint written by a build before the checkpoint-envelope format change is refused with `ErrCheckpointIncompatible` rather than migrated; the tokens it carries are preserved, and the run must be re-driven from its persisted history.
- `Attach` still treats possession of a run identifier as sufficient to observe it: no ownership rule is specified for attach yet, so the repair records the boundary instead of silently introducing an access policy.

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

[unreleased]: https://github.com/victorzhuk/gohan/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/victorzhuk/gohan/releases/tag/v1.0.0
[0.1.0]: https://github.com/victorzhuk/gohan/releases/tag/v0.1.0
