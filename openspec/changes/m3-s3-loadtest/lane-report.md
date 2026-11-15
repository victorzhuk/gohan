# Lane report: m3-s3-loadtest

Status of every task in `tasks.md`, the evidence observed in this apply round, and the
admission state of the three live-engine legs. A task is checked only when the listed
scenario ids pass and the named commands are green. A blocked task is reported blocked,
never passed.

## Delivered

| Task | State | Evidence |
|---|---|---|
| 1.1 Registry | done | six scenario ids registered in `openspec/scenarios.json` with `origin: m3-s3-loadtest` and `deferred_to: M3`; `task spec:types` exits zero (`dups: {} undefined: [] uncatalogued: []`) |
| 1.2 Harness | done | `testkit/loadtest/fault.go`, `driver.go`: fault schedule keyed on per-port operation numbers (fail next N, fail range, fail all, clear) over the existing store ports, and a K-worker driver with an entry barrier and per-run wall time; `go test ./testkit/loadtest/` green, `-race` green |
| 2.1 Storm | done | `core/load_storm_test.go`, `performance.disconnect-storm-exactly-once`: K=6 over one stack, write-side drop on the turn relay plus read-side drop on the reattach read, 6/6 runs `Finished`, one Completed journal entry and one tool execution per call, duplicate `OperationID` refused with zero execution delta |
| 2.2 Storm recovery | done | `performance.disconnect-storm-recovery`: `ResumeReady` lists the consumed-token suspension exactly once, `PendingInput` read once, one tool execution, `Finished`; the untouched suspension stays `Suspended` and is never reclaimed; the interrupted running run is reclaimed once |
| 3.1 Noisy neighbor | done | `core/load_neighbor_test.go`, `performance.noisy-neighbor-isolation`; mean per-step ratio 1.80-2.16 across runs on this workstation against the frozen 4.17 bound; per-step samples and the worst-step ratio are recorded as diagnostics, since an 8-step worst-case sample proved unreproducible (5.59, 6.03, 24.02 on an idle machine) |
| 3.2 Gate wiring | load half done, combined target environment-blocked | `tools/loadgate` requires the expected run set, binds each measurement to the current baselines (limit equality, ratio cross-check against `loaded_ns/isolated_ns`), fails on a non-zero suite exit even with parsed lines, judges the mean ratio and prints the worst step; strict in `.github/workflows/performance.yml` (`go run ./tools/loadgate --latency`) and advisory inside `task bench:gate`. `task bench:gate` as a whole cannot complete here: the sandbox keeps `.git` read-only and the benchmark gate's `git worktree add` fails before the load half runs |
| 4.1-4.3 Live legs | blocked | admitted below; no live run exists and none is reported passed |
| 4.4 Admission report | done | this report, "Live-engine legs" |
| 5.1 Floor | done | `task lint` 0 issues across four lint legs; `task test` green across every module; `task bench` green |
| 5.2 Validate | done | `openspec validate m3-s3-loadtest --strict` reports the change is valid |

## Load harness contract

`testkit/loadtest` implements the existing store ports without adding a port method. The
fault schedule is keyed on operation numbers, never wall time: `FailNext`, `FailRange`,
`FailAll`, `Clear`, with `Operations` and `Failures` counters. `Wrap` preserves the
optional interfaces the drive and recovery consult (`PreemptedLister`, `ResumeReadyLister`,
`CheckpointRuns`). `Run(ctx, k, fn)` starts K workers behind an entry barrier and reports
per-run start, end, wall time and error.

## Verification floor

| Command | Result |
|---|---|
| `task lint` | 0 issues on all four lint legs (root, examples, pgtxlint, adapter/redis) |
| `task test` | green: root module, every adapter module, examples |
| `task spec` | `missing_in_scope=0 unregistered=0 failed_legs=0`; types index clean |
| `task bench` | green |
| `go test ./core/ -run 'TestLoadDisconnectStorm' -v` | PASS, both scenario subtests |
| `go test ./core/ -run 'TestLoadNoisyNeighbor' -v` | PASS, `LOADRATIO` mean ratio 1.80 within the 4.17 bound, worst step 5.47 recorded |
| `GOHAN_PERF_REFERENCE=1 go test ./core/ -run TestLoadNoisyNeighbor` | PASS twice in a row (strict mode) |
| `go test -race ./core/ -run TestLoadNoisyNeighbor` | PASS, no data race |
| `go test ./testkit/loadtest/` and `-race` | PASS, including the capability matrix and per-method fault counters |
| `go test ./tools/loadgate/` | PASS |
| `go run ./tools/loadgate --latency` | exit 0 on this tree (the strict command CI runs) |
| `task bench:gate` | cannot complete in this environment: the sandbox keeps `.git` read-only and the benchmark gate's `git worktree add` fails before the load half runs. The load half is verified directly (previous row); the benchmark half is unchanged by this change and runs in CI |
| `openspec validate m3-s3-loadtest --strict` | valid |

## Storage-failure semantics observed

The shipped drive has no transient/terminal classification for store errors: a journal
`Reserve` or session-log error mid-run finishes the run `Failed`
(`core/drive_lifecycle.go`, `core/conversation.go`). The event-log relay path is the one
storage failure that leaves the run reclaimable: `drivePrepared` returns on a relay
failure without a terminal transition (`core/stream_lifetime.go`), so the run stays
`Running` until its lease expires and `Recover` reclaims it. The storm therefore injects
its connection drops on the event log — during writes (the turn relay) and during reads
(the reattach read) — and the scenario's "every affected run completes exactly once" is
evidenced on that path. Journal and session-store outages terminate runs by design and are
not claimed as recovered.

## Independent review

Two read-only lenses (scenario/evidence QA and behavioral quality) reviewed the added files. Resolved in this round:

- The fault wrapper advertised the optional store surfaces unconditionally and put `CheckpointRuns` on the checkpoints wrapper, so a wrapped stack silently lost the token-fenced resume surface and the resume-reader path. `Runs` now owns `CheckpointRuns`, the checkpoints wrapper owns `CheckpointResumer`, and `Wrap` selects capability-exact variants so a store without an optional surface still falls back exactly as the core probes expect.
- Every optional-surface method now counts an operation and records its method name, so fault schedules reach recovery-side discovery and the suites can assert which kind of operation was dropped.
- The load gate accepted a suite failure that still printed a ratio line, and accepted stale or hand-written measurements. It now fails on a non-zero suite exit, requires the expected run set, requires the measurement's limit to equal the frozen one, and cross-checks the ratio against its own nanosecond fields.
- The gate was advisory in CI. The reference-runner workflow now runs `go run ./tools/loadgate --latency`, mirroring the benchmark gate; `task bench:gate` keeps the advisory load half.
- Storm assertions tightened: exactly one `Done` per run, and write-side and read-side drops asserted through the recorded fault kinds instead of inferred from error text.

Rejected findings:

- Skipping the load suites under `-short`: the spec gate counts scenario coverage from the `-short` lane, and both suites run in milliseconds, so a skip would fail coverage and hide the scenarios.
- Gating the worst single-step ratio: measured 5.59, 6.03 and 24.02 across runs on an idle workstation, so an 8-step worst-case statistic cannot be frozen without random failures. The mean per-step ratio is gated; per-step samples and the worst step stay in the measurement as diagnostics.

Product observation reported, not changed: `nativeTurnConfig` never fills the runtime scheduler's journal fields, so the scheduler's journaled path is inert for native flows and journaling comes from the flow's tool chain (`std.Journal`). The storm fixture mirrors that production wiring with a journal chain step.

## Live-engine legs — blocked admission

No Kafka broker, Temporal server or Camunda/Zeebe broker exists in this repository, its
CI, or the environment of this apply round. Every leg is blocked on external
infrastructure; each is unchecked and none is reported passed. The offline acceptance legs
of the three processes are green on master (`task examples:test`) and cover the engine ids
each live leg would assert under load.

| Leg | Blocked on | Pinned version slot | Assertions when admitted |
|---|---|---|---|
| `performance.live-kafka-refunds-under-load` | a reachable Kafka broker | unset: the driven broker's version is pinned in the evidence of the first admitted run | `engines.redelivery-returns-the-same-result`, `engines.crash-after-consume` as subtests named exactly their ids |
| `performance.live-temporal-travel-under-load` | a reachable Temporal server | unset: the server version is pinned in the evidence of the first admitted run | `engines.redelivery-returns-the-same-result`, `engines.duplicate-workers` |
| `performance.live-camunda-invoice-under-load` | a reachable Camunda/Zeebe broker | unset: the broker version is pinned in the evidence of the first admitted run | `engines.redelivery-returns-the-same-result`, `engines.duplicate-workers` |

Unblock condition for each leg: the named infrastructure reachable from the test process,
the leg's process driven against it while the storm and neighbor suites generate
background load, and the pinned engine version recorded in the evidence. Until then the
three registry ids stay `deferred_to: M3` and uncovered; the M3 spec gate reports them
missing, which is the admitted state, not a pass.
