# gohan — overview

The capability specs under `openspec/specs/` each carry their own version — from the `v1.0 baseline` specs restructured from gohan-spec v0.13 on 2026-09-29 to `stores` at v1.10 — and evolve independently through their ADRs. This file is the map; it is not normative.

## 1. Purpose

gohan fixes the **structure and flow** of AI features in Go services — boundaries, governance, identity, durability, telemetry — and delegates **loop execution** and **capabilities** (models, tools, decisions, graphs) to adapters: a native runtime, cloudwego/eino, google adk-go, and others.

The design target is evolution without rewrites: a service starts as a small, clean-architecture Go service with mocks and a plain API call, grows into enterprise integrations and highload production, and later adopts eino graphs or adk-go workflows — while domain, use cases, tool implementations and transport stay untouched. Only infrastructure wiring changes.

### Goals

- G1. Business code depends on its own ports; gohan provides one stable seam behind them: `Flow[In, Out]` and `Conversation`.
- G2. Governance (permissions, scopes, guards, budgets, limits, journaling, telemetry) is a property of components and applies under any runtime or graph.
- G3. Mix components across adapters: e.g. eino graph + adk-go model + Jev decider in a guard.
- G4. Production semantics out of the box: exactly-once tool effects, single-use resume tokens, session concurrency safety, verified identity, fail-fast configuration.
- G5. Never defeat inference-layer optimizations (prefix caching, batching, constrained decoding); expose their knobs.
- G6. Deterministic testing: scripted models, conformance suites for runtimes, stores and chains.

### Non-goals

- Realtime bidirectional audio / voice (use adk-go live APIs directly).
- Document ingestion / indexing pipelines (retrieval is a Tool or a graph node).
- An ACL engine (gohan carries identity; downstream services enforce).
- A scheduler or queue (gohan exposes a `Waker` port).
- Inference engine features (gohan configures them, never implements them).
- Prompt management UI; transfer-of-control handoffs (sub-flows are typed `FlowAsTool` calls, see `subflows`).


## 2. Principles

1. Domain and use cases never import gohan.
2. One seam (`Flow` / `Conversation`), many implementations.
3. Governed = passed through gohan. Governance travels with the component, not with the loop.
4. One middleware primitive per component, one canonical order.
5. Concurrency guarantees live in store port contracts, not in documentation.
6. Identity is ambient and verified; the model never supplies it.
7. Configuration is data (profiles) plus strategies at real variation points only; validated at startup.
8. Every seam that would be a breaking change later exists now, even with one implementation.
9. **No hidden behavior.** Core adds no middleware and no prompt text on its own. Every step in a chain is a named value you can print, remove or reorder; every harness-authored string is an exported value you can read and replace. `Explain` shows exactly what will run and what the model will see.
10. Presets are ordinary code. `std` ships the recommended chains as short functions meant to be read and copied, not trusted.
11. Pay only for what a call needs: `ReadOnly` tools skip journaling and shielding; persistence is one write per turn.


## Where things are

| Path | Holds |
|---|---|
| `openspec/specs/<capability>/spec.md` | Normative contract and requirements per capability (below) |
| `docs/adr/` | 154 architecture decision records, numbered 0001–0154 (D1–D89 from the monolith, 0090+ from later grill rounds) |
| `docs/design/types.md` | generated index of every normative type, function and sentinel error with its defining capability |
| `docs/design/` | Architecture, module layout, Go baseline, compatibility policy (normative), adapter mappings, inference layer, reference scenarios, testing, evidence, risks |
| `openspec/changes/` | Change proposals with task lists and design notes; `m0-core` is fully tasked, `m1`–`m4` are scope stubs |
| `AGENTS.md`, `openspec/project.md` | Entry point and conventions for implementing agents: reading order, rules, commands, definition of done |
| `docs/archive/gohan-spec-v0.13.md` | The pre-split monolith, kept for history only |

## Capabilities

| Capability | Title | Scenarios |
|---|---|---|
| `flow` | Flow and Conversation | 25 |
| `messages` | Messages and blocks | 15 |
| `suspension` | Suspension and resume | 7 |
| `identity` | Identity, approval and run trees | 23 |
| `model` | Model and profiles | 30 |
| `tools` | Tools | 41 |
| `decider` | Decider | 4 |
| `chains` | Chains, prompts and Explain | 17 |
| `cache` | Cache contract | 3 |
| `flags` | Feature flags | 5 |
| `redaction` | Redaction, erasure and residency | 9 |
| `guards` | Guards and provenance | 10 |
| `permission` | Permission gate and approvals | 15 |
| `assembly` | Context assembly | 3 |
| `context` | Context management: projections and compaction | 15 |
| `subflows` | Sub-flows: isolation, effects, nested suspension, limits | 16 |
| `interop` | MCP client and server (2026-07-28), `AwaitingInput` | 16 |
| `sandbox` | Sandboxed execution: port, policy, workspace lifecycle | 15 |
| `skills` | Skills: SKILL.md progressive disclosure under tool controls | 12 |
| `agui` | Client transport (AG-UI): message ids, reasoning deltas, shared state | 14 |
| `release` | Release safety: shadow runs, release identity, evals | 15 |
| `taint` | Information-flow control: taint, trifecta check, quarantine | 14 |

Capability freeze: no new capability specs until M0 ships; candidates go to `docs/backlog.md` (ADR-0098).
| `build` | Strategies and Build | 6 |
| `stores` | Store ports | 60 |
| `recovery` | Crash recovery | 6 |
| `limits` | Run limits and uncertainty | 7 |
| `runtime` | Runtimes and stepper | 27 |
| `streams` | Events, cancellation and streams | 21 |
| `working-state` | Durable working state | 15 |
| `telemetry` | Telemetry and prompt sources | 11 |
| `structured-output` | Structured output | 9 |
| `engines` | External engines, dedup and run states | 11 |
| `languages` | Embedded languages and definitions | 8 |
| `lifecycle` | Retirement, schema evolution and cost | 7 |
| `performance` | Harness performance budgets | 3 |

Total: 515 scenarios, each with a stable ID that is the name of the subtest covering it. `task spec:coverage` reports scenarios without tests and tests without scenarios.

Each scenario also carries an `origin` label: a requirement heading of the archived v0.13 monolith (`R1`–`R14`, `R5a`–`R5y` — the archive is the only place those are written down) or the ADR that added the scenario (`ADR-NNNN`). Labels from later review rounds (`R35`–`R60`, `R52`, `RPERF`) name no written requirement list and are provenance only. Acceptance is `deferred_to` plus `task spec:coverage`; never the label.

## 12. Milestones

| M | Scope | Exit |
|---|---|---|
| M0 | **core**: types incl. `Origin`, `Seq`, error classes, `PromptSet`, chain-as-data + ordering validation, `Explain`, `StepError`, Flow/Conversation, native runtime, NewTool, scopes, Principal, run trees, suspension + Replay resume, `Runs` + `Recover` + `Inspect`, RunLimits, uncertainty surfacing, memory stores + EventLog, Build + profiles + fallback validation + manifest + tool policy, `ToolFilter`, OTel, scripted model, conformance (incl. leak checks) + storetest. **std**: canonical chains with `Applies` gating, gate, journal, shield, guards, StablePrefix + fencing + Truncate, ToolSchema + ValidateRepair, DefaultPrompts, presets | `task spec:coverage` clean at M0 — every scenario without a test carries `deferred_to` a later milestone; S1 green on the native runtime; performance baselines frozen (ADR-0135) |
| M0.5 | stability review gated by the three acceptance processes' offline scenarios against memory stores (ADR-0083, ADR-0136): core types/ports declared v1-candidate; ports and handles tagged; `task api:check` in CI; v1.0.0 at M2 exit (`docs/design/compatibility.md`) | API review doc merged; `kafka-refunds`, `temporal-travel`, `camunda-invoice` offline green |
| M1 | `taint` (`Capabilities`, `std/taint`, trifecta check) and `Verify`; `quickstart` and `excursions` S1–S2 examples; `std/redact` rules + HMAC `Redactor` + `EraseSubject`; sunset/successor validation; schema versions + upcaster registry + `storetest.Schemas`; cost tags; `std/cache` key builder + exact-match cache; structured-output hardening; adapter/openai (+vLLM), adapter/anthropic with error-class normalization and version reporting, adapter/postgres (all ports incl. AuditLog) + CompleteTx, limit.Local + breaker, class-based retry/fallback, router Static/ByLatencyClass, guards (all four stages) + output modes + fallback, tool/exec, Notes, OutputStore, Waker port, `Detached` + Attach, `std.ExplainHandler` | `tool/exec` and `std/redact` scenarios green; `conformance.Model` green for both provider adapters; S2 green incl. pod-kill test |
| M2 | adapter/eino: runtime, bridges, graph flow, Native resume; `sandbox` (port, `std/sandbox`, adapter/docker); `context` (projections, persisted compaction) | `conformance.*` green on eino; S4 (graph) green |
| M3 | `subflows`; `interop` (adapter/mcp client + server on 2026-07-28); `release` (shadow mode, `ReleaseManifest`); adapter/adkgo: runtime, bridges, workflow flows; postgres partitioning + lint + `storetest.Bloat`; adapter/redis (quota pools, admission, EventLog); Constrained structured output; S3 load test incl. disconnect storm and noisy-neighbor test | `conformance.*` green on all three backends; S3 green |
| M4 | `skills`, `agui` (adapter/agui), evals module (`release`); adapter/jev, adapter/langfuse, adapter/openfeature, `core/flowdef` + `std/flow.Compile` + YAML front-end, adapter/cel, adapter/lispico (front-end + ExprLang where declared), `catalog-enrichment`, live-engine tests for the three processes, evals module, FlowAsTool, Summarize policy, `std/flow` recipes (`Extract`, `Classify`, `Route`, `MapReduce`, `Pipeline`, `RAG`, `Judge`/`Refine`), remaining examples (`support-triage`, `invoice-extraction`, `research-report`, `ops-agent`, `pr-review`, `data-analyst`) | all eight examples green offline from cassettes and once live in the scheduled workflow |


## 13. Open questions

| Q | Question | Default |
|---|---|---|
| Q1 | Jev transport: official API vs OpenAI-compatible surface | official API once documented |
| Q2 | JSON schema derivation library for `NewTool` | resolved by ADR-0106: reflection walker in `core`, tag vocabulary `json`/`desc`/`enum`/`min`/`max`/`pattern`, no third-party library |
| Q3 | adk-go v2: exact constructor/config names, graph API, pause/resume payloads, session service and tool interfaces (source fetches disagree on the current v1 tag; the v2 module path and feature set are consistent) | target v2; `Replay` fallback; verify against the v2 tag pinned in `adapter/adkgo/go.mod` |
| Q4 | eino v0.9: adapter targets the `TypedChatModelAgent[*schema.AgenticMessage]` path (block-ordered, matches D57), not legacy `schema.Message`; calling `StatefulInterrupt` from inside a tool; `CheckPointStore` methods; graph introspection for `Check` | AgenticMessage path; Replay fallback; node builders without `Check` |
| Q5 | vLLM priority parameter and affinity header conventions in the target gateway | configurable per profile |
| Q6 | Default output guard window size and latency budget per class | 64 tokens; measure in S3 |
| Q7 | Parallel tool execution in native runtime | resolved by ADR-0114: batch protocol — gate all first, `ReadOnly` concurrent under `MaxParallelTools`, effects sequential, asks last, one result per call |
| Q8 | Session history retention defaults and purge scheduling | caller-driven `Purge` |
| Q9 | `PromptSource` selector for A/B: sticky by session hash vs per-request random; where the variant weight lives | sticky by session; weights in the source's label config |
| Q10 | Handoff semantics beyond `FlowAsTool` | none planned; `FlowAsTool` is the surviving pattern upstream |
| Q26 | CaMeL-style planner: privileged model emits programs run by the `flowdef`/`ExprLang` runtime with variable-level capabilities | after the script tier (Q21); taint policy covers the agent loop until then |
| Q25 | Detached children with push-based completion (parent suspends `AwaitingExternal` keyed by child `RunID`; child outlives parent's request) | after v1; synchronous children only until then |
| Q19 | Whether `examples/` should be one module or one per example (build time vs isolation) | one module until CI time forces a split |
| Q20 | Engine-driven loops (engine calls `Step` inside activities): which engine first, and whether `State.Backend` bytes are acceptable in workflow history | later; Temporal first; keep `State` small, history in `SessionLog` |
| Q21 | Script tier: which route per language — deterministic replay (needs `Hermetic + Deterministic`), VM snapshot (`Snapshot`; WASM engines are adding host-driven suspension), or serializable continuations (`Continuations`; natural for a Lisp) — and which go-lispico changes each needs | later; declare capabilities honestly, admit nothing to `ScriptLang` until conformance exists |
| Q23 | NER/LLM detectors for the `Redactor`: which to ship as reference `Decider`s and their latency budget on the `Interactive` path | rules only in std; NER via adapter; measure in S3 |
| Q24 | Deprecation signal sources per provider (headers vs error bodies vs published schedules) | normalize per adapter; fixtures in conformance |
| Q22 | Whether go-lispico can expose a restricted evaluation mode with a step limit and no host bindings (needed for `ExprLang` admission) | if not, CEL only for expressions; Lisp stays a front-end |
| Q11 | Module path and license | resolved by ADR-0101: `github.com/victorzhuk/gohan`, Apache-2.0 |
| Q12 | Fingerprint canonicalization: which arg fields are "intent" (e.g. exclude free-text comments)? | full canonical JSON; `WithFingerprintFields` opt-in to narrow; the same fields define grant scope |
| Q13 | Attached-mode reconnect while the original run is alive: which pod serves `Attach` (affinity vs shared EventLog)? | shared `EventLog` (Redis) in S3; affinity optional |
| Q17 | Fencing format per provider (XML tags vs markdown) and its effect on prefix caching | XML-style tags; fence markers are static so the prefix stays stable |
| Q18 | Description guard: rules only or LLM/Jev decider at build time? | rules in core; decider optional; both logged in manifest |
| Q14 | Lease TTL and reaper cadence defaults | resolved by ADR-0102: constants in `stores` (30 s / 10 s / 60 s) |
| Q15 | Policy manifest export (`Stack.Manifest()`) for diff-able review in CI | resolved by ADR-0096: `ReleaseManifest` |
| Q16 | Fault-injecting test wrappers (`gohantest.Flaky`) | evals module, M4; shadow runner resolved by ADR-0096 |
