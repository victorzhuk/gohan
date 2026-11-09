## 11. Testing strategy

- `gohantest.ScriptedModel`: canned turns (text, tool calls, errors, per-chunk delays), assertions on received requests.
- `gohantest.Recorder` / `Replayer`: each model call is keyed by `hash(assembled request)` + profile `Version` (prefix-stable assembly keeps keys stable across unrelated edits); streams are recorded as timed chunk sequences and replayed under `synctest`. Modes: `Strict` (key must match, else the test fails with the first differing block), `ByTurn` (match by turn index and tool names when prompts were edited deliberately), `Rerecord`. Cassettes live in `t.ArtifactDir()`-relative fixtures and record the model version they came from.
- `evals` (contract in `openspec/specs/release/`): SHA-pinned datasets, delta gating with hard-block/review bands, judge ×3 median with spread flag, pairwise shadow runner, `Import(sessionID)` through the `Redactor`, online sampler; plus tolerance-band assertions (`>= baseline - tolerance`), `pass@k` and `pass^k`, trajectory assertions on tool sequences, structured-field checks; LLM-backed judges are `Decider`s with temperature 0, enum/boolean output through constrained decoding, pinned `Version`, and a sampled human-label comparison job.
- Conformance suites: `conformance.Runtime`, `conformance.Chain`, `conformance.Flow`, `conformance.Model`, `storetest.*`; adapters run them in their module tests. `conformance.Model(t, newModel, fixtures)` replays a fixture set every provider adapter ships (recorded responses for 429 with `Retry-After`, 5xx, 401, 400, context overflow, retired model, no-usage response, `max_tokens` truncation mid tool call, unmodelled block, explicit cache markers) and asserts the provider adapter contract in `openspec/specs/model/`: error classes, `RetryAfter`, `Usage` incl. cached/cache-write tokens, `ModelVersion` and `Estimated`, delta ordering, complete `ToolUse`, truncation marking, cancellation within 100 ms, `Idle` timeout, `Raw` round-trip, declared fidelity honoured, iterator contract.
- Round-trip property tests for all message converters.
- Scenario tests for S1–S4 in `examples/excursions` are the acceptance suite of the spec.
- Real-provider integration tests behind a build tag; load test for S3 in a separate pipeline.
- Scenario binding: each scenario ID in `openspec/scenarios.json` is the exact name of the subtest that covers it (`t.Run("flow.plain-invoke", …)`). `task spec:coverage` reports IDs with no matching subtest and subtests named like IDs that are not in the registry. It reads concatenated `go test -json` streams from all three modules (root, `adapter/otel`, `examples`). `task spec:gate` gates the same matching at milestone `M0.5` by default (`GATE=` overrides). No comments or tags carry IDs.
- `t.Parallel()` and `t.Cleanup()` throughout; `synctest` for harness-time tests (budgets, timeouts, heartbeats). For memory store-time tests, use `stores.WithMemoryRunClock` and `stores.WithMemoryCheckpointClock`; `postgres.WithClock` overrides the default PostgreSQL `clock_timestamp()` store clock for tests. Store-time tests are separate from harness-time tests (`stores` *Two clocks*); goroutine-leak profile in conformance; hand-written fakes in `gohantest`, no mock generators.
- Recovery contract tests cover an empty consumed `Continue()`, obsolete tokens, pending human approval, and run stores without `CheckpointRuns` (`TestRecoveryContract`). `TestRecoveryEventTail` checks a second conversation's `Attach` and completion writes after reaper cancellation. `TestRuntimeFinishError` checks that a failed terminal write emits an error, not `Done`; `TestRuntimeResumeToolBudget` checks cumulative admissions and prepaid calls across resume and recovery.
- Provider composition acceptance uses controlled `httptest` HTTP SSE through `Build` and `NewNativeConversation` for provider scenarios. The Eino identity scenario uses `Build` and `NewForeignConversation` with a controlled model. Where conversation success cannot expose iterator state, public `Generate` tests cover finality and cancellation residuals, including `synctest` silent cancellation and response-body close counters.
- Shipped-fixture wire-boundary tests assert raw fixture boundaries independently of the provider decoder, so a decoder false positive cannot validate its own input.
- The admitted full-workspace race sweep uses `-p=2`, `-parallel=2` and `GOMAXPROCS=2`, including all four affected adapter packages: `adapter/anthropic`, `adapter/openai`, `adapter/eino` and `adapter/eino/einoflow`.
- Network acceptance runs real forwarding peers under controlled divergent DNS: the client and the proxy resolve hostnames independently, a raw wire observer records the absolute request target and `Host` header before `net/http` parses them, and HTTPS leaves use independent proxy and destination certificate authorities in bounded subprocesses. The network-global fixtures mutate package-level resolution seams, so the network tests run serially (`-p=2 -parallel=2`) with bounded, verified cleanup. `go test ./std/egress -run '^TestNetworkPinningScenarios$'` covers the `tools.cgnat-destination-denied`, `tools.proxy-destination-pinned` and `tools.proxy-tls-authority-preserved` leaves, and `go test ./std/egress -run '^TestEgressCGNATPolicy$'` covers the shared-address policy unit contract. `TestEgressProxyPinning`, `TestEgressProxyAuthority` and `TestEgressProxyLifetime` land with the proxy implementation chunk and pin the same contract at unit level: pinned dial to the validated address, CONNECT carrying the checked authority with original-host certificate verification, and lifetime release unchanged by the proxied path. Sandbox evidence stays in the `adapter/docker` acceptance suite; the network tests do not prove sandbox isolation.
- Package-level green results, fixture repairs and documentation changes are workspace verification, not independent release verification. `task api:check` and the spec gate belong to the parent milestone.


## Testkit shapes (illustrative tier — testkit is not a capability; names may change in task 29)

```go
// gohantest
func NewScriptedModel(profile gohan.ModelProfile, turns ...Turn) *ScriptedModel
func Text(s string) Turn
func ToolCall(name string, args any) Turn
func Fail(class gohan.ErrorClass) Turn
func Refuse() Turn
func Delay(d time.Duration) Turn
func WithUsage(u gohan.Usage) Turn
func (m *ScriptedModel) Requests() []gohan.ModelRequest

func Record(t *testing.T, real gohan.Model) gohan.Model
func Replay(t *testing.T) gohan.Model
// cassette: testdata/cassettes/<TestName>.json — {"version": "<profile version>", "calls": [{"key": "<sha256 of assembled request>", "chunks": [{"at_ms": 12, "chunk": {...}}, …], "usage": {...}}]}
// mode from GOHAN_CASSETTES = strict | byturn | rerecord (default strict)

func Flaky(m gohan.Model, plan FaultPlan) gohan.Model
type FaultPlan struct{ Every int; Class gohan.ErrorClass; AfterChunks int }
func LeakCheck(t *testing.T)

// conformance
type Harness interface {
	Run(ctx context.Context, input []types.Message) ([]types.Event, error)
	Observed() Observations
}
type Scenario string     // ScenarioPlainAnswer, ScenarioForeignTool, …
type Observations struct{ Steps, ToolCalls []string }
type Fixtures []Fixture
func DefaultFixtures() Fixtures

func Runtime(t *testing.T, newHarness func(Scenario) Harness)                // runtime.* scenarios
func Chain(t *testing.T, newHarness func(Scenario) Harness)                  // chains.* ordering scenarios
func Flow(t *testing.T)                                                     // flow.*, suspension.* scenarios
func Model(t *testing.T, newModel func(Fixture) types.Model, fixtures Fixtures) // model.status-to-class … model.raw-round-trip

// storetest — each suite takes a factory, not a store, so a fresh instance
// per subtest is the suite's own business
func SessionLog(t *testing.T, factory SessionLogFactory)
func Checkpoints(t *testing.T, factory CheckpointsFactory)
func CheckpointsResumer(t *testing.T, factory CheckpointResumerFactory)
func Journal(t *testing.T, newJournal JournalFactory)
func Runs(t *testing.T, newRuns RunFactory) // RunFactory returns (RunStore, RunClock)
func AuditLog(t *testing.T, newLog AuditFactory)
func EventLog(t *testing.T, newLog EventLogFactory)
func Schemas(t *testing.T, factory SchemasFactory)
```

`storetest.Bloat` (1 M reserve/complete cycles under autovacuum, p99 `Reserve` latency and dead-tuple bounds) is **not shipped**: it is an M3 deliverable that lands with the Postgres partitioning work (`docs/design/adapters.md`; `docs/overview.md` M3 row).

Every suite runs the scenarios listed against its capability in `openspec/scenarios.json`, using subtest names equal to the IDs, so `task spec:gate` counts adapters' conformance runs in the `adapter/otel` stream.

Performance gates are specified in `openspec/specs/performance/spec.md`.
