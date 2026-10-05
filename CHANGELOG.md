# Changelog

All notable changes to the `github.com/victorzhuk/gohan` root module are documented here. Format: Keep a Changelog 1.1.0. Breaking changes while the module is `v0.x` are listed under *Breaking* (`docs/design/compatibility.md`).

## [Unreleased]

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
