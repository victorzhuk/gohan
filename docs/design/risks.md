## 15. Risks

| Risk | Mitigation |
|---|---|
| Message conversion loss between eino / genai / gohan types | `Raw`, round-trip tests, loss matrix |
| Upstream API churn (eino v0.x, adk-go minors) | adapters in separate modules, pinned versions, conformance on dependency bumps |
| Ungoverned components slipping into graphs | governed node builders, `Check`, docs; ungoverned count metric where detectable |
| Chain ordering regressions | `conformance.Chain` in CI; order defined in one file |
| Windowed guard latency on interactive flows | fast deciders (rules/Jev) on output stage; TTFT metric per flow |
| Journal growth | TTL per tenant; purge with sessions |
| Replay resume divergence if tools are non-deterministic in args | args come from persisted history, not regenerated; model is not re-asked for completed turns |
| Core API bloat | new core types require a scenario that cannot be built without them; strategies need ≥ 2 shipped implementations |
| Key pinning collapses two legitimate identical intents (e.g. book the same slot twice on purpose) | pinning applies only while the earlier entry is `Unknown`/`Reserved`; a `Succeeded` entry yields a fresh key plus `repeat_intent` signal; `FingerprintFields` narrows |
| Reaper re-executes a side effect the downstream API does not dedupe | `Reserved` re-execution is logged with `key_pinned`; teams without key support at the API must use `CompleteTx` or accept at-least-once and mark the tool `Idempotent` only if true |
| Recovery replays a run whose inputs are now stale (prices changed) | recovery honours original limits; tools re-read source of truth; business layer receives `UncertainOutcomeError` when applicable |
| Fencing and provenance instructions consume prefix tokens and may still be ignored by weaker models | static markers keep cache hits; guards, not fencing, are the enforcement layer; evals include injection suites |
| Cancel shield keeps paying for a tool after the user left | bounded by `ToolSpec.Timeout`; only `SideEffect` calls past the gate are shielded |
| Pinned manifest blocks legitimate upstream spec updates | drift is a startup error with the diff in the message; bump the manifest in the same PR as the dependency |
| `std` presets drift from the spec's canonical order as they are copied and edited | ordering constraints live in core validation, not in `std`; a copied chain in the wrong order fails `Build` |
| Teams bypass `std` and lose safety they did not know they had | `Explain` lists absent step kinds against the flow's latency class as warnings (e.g. "no Gate step; SideEffect tools will execute unguarded") |
| Adapter modules lag core releases | additive-only core after M2; conformance suite is the contract; an adapter that fails it is marked unsupported in the README rather than blocking a release |
| Audit append on the critical path adds a write per decision | postgres implementation batches within a turn's transaction; memory implementation for dev; append failure is fatal by design because a silent gap is worse than a failed step |
| Quota pool store (Redis) becomes a single point of failure for all model calls | limiter degrades to local token bucket at a configured fraction of the pool when Redis is unreachable; `gohan.pool.degraded` gauge |

## 16. Spec consistency debt (2026-10-04 review)

The 2026-10-04 review reconciled 27 cross-spec contradictions (ADR-0138) and found about fifty more that it did not reconcile: five lenses read the corpus slice by slice, and the unreconciled items are recorded with their evidence in `docs/design/review-2026-10-04-findings.md`. They cluster in four places — telemetry's metric-label allow-list against its own metric table, the stored `Run`/`Journal` shapes against the states and outcomes the specs assert on them, the model-call path (429 retry versus failover, strict-version drift, ineligible `RetryAfter`), and the approval/token state machine (quorum against single-use tokens, the `Ask` payload). Reconcile them the way ADR-0138 does — resolve to the side the scenarios assert — before the capability they touch is implemented, and record the pass in a new ADR. Until then, `task spec:coverage` stays the gate and these items are read as risk, not as contract.
