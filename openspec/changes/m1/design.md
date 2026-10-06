# Design: m1

The milestone record. `proposal.md` holds the scope; this file holds the handoff from M0, the acceptance matrix that assigns every M1 obligation to one change, and the shared contract decisions the first changes implement. The capability specs remain the contract; this file decides only what the specs leave open between two changes.

## 1. Handoff from M0

M0 and its hardening and closure changes are archived under `openspec/changes/archive/2026-10-05-*`. The preceding review is closed on the current tree, re-established rather than quoted:

```
HEAD 63598e47f52fa71ddc348c86952f3720263fbe31, working tree clean
task tools:test     27 tests, OK
task lint           0 issues
task test           green (root, adapter/otel, examples)
task spec           registered=515 subtests=1164 covered=294 missing=221
                    missing_in_scope=0 unregistered=0
task api:check      exit 0; 13 pre-v1 differences reported; adapter/otel compatible with its tag
task examples:run   exit 0; all five mains printed their documented output
```

`docs/design/api-review-m0-5.md:85` records an earlier `missing_in_scope=9` verdict. That verdict describes the pre-repair tree; the run above supersedes it and the review document is not a current defect list. One item it records is still real and unreleased: `adapter/otel/v0.1.0` was tagged from a `go.mod` carrying `replace github.com/victorzhuk/gohan => ../..`. The working tree holds the repair, and a later adapter patch release carries it. No release is cut in M1 authoring.

Two recorded limitations stay limitations, not blockers:

- The `api:check` HTTP clause (`api/gohan.yaml` against a generated `adapter/httpapi`) is unexercisable until M4, when both artifacts exist (`docs/design/compatibility.md:45`).
- The `spec:types` port-method-set clause is unimplemented: the generator reads `openspec/specs/*/spec.md`, so it records declared intent and observes no shipped method set (`docs/design/compatibility.md:46`). It must exist before the v1 freeze at M2's exit; it does not block M1.

Root module stays `v0.x` through M2. Breaking changes inside M1 are listed under *Breaking* in `CHANGELOG.md` and each one that changes a normative spec carries an ADR.

## 2. Acceptance matrix

Every entry of `openspec/scenarios.json` with `deferred_to: M1` — 85 IDs — appears exactly once below, plus the M0-ranked IDs whose implementation M1 finishes and the explicit milestones obligations that carry no ID. Owner is a change name; every change named here is authored under `openspec/changes/<name>/`. Status: `missing` (no implementation), `integration gap` (the declaration or a partial implementation ships, the public path does not use it), `shipped` (behaviour exists on the public path, verification is recorded).

### 2.1 taint (14)

| ID | Owner | Depends on | Status | Evidence |
|---|---|---|---|---|
| `taint.substring-match` | `m1-taint-enforcement` | — | done | `core/taint_computation_scenarios_test.go`; shipped in `archive/2026-10-06-m1-taint-enforcement` |
| `taint.short-untainted` | `m1-taint-enforcement` | — | done | as above |
| `taint.user-untainted` | `m1-taint-enforcement` | — | done | as above |
| `taint.window-since-user-turn` | `m1-taint-enforcement` | — | done | as above |
| `taint.exfil-denied` | `m1-taint-enforcement` | `m1-egress-policy` (Exfil derivation) | done | `core/taint_enforcement_scenarios_test.go`, `core/taint_wiring_test.go` |
| `taint.side-effect-asks` | `m1-taint-enforcement` | `m1-egress-policy` | done | as above |
| `taint.read-only-allowed` | `m1-taint-enforcement` | `m1-egress-policy` | done | as above |
| `taint.gate-order` | `m1-taint-enforcement` | — | done | as above |
| `taint.trifecta-fails-build` | `m1-taint-enforcement` | — | done | `core/taint_trifecta_scenarios_test.go` |
| `taint.trifecta-with-policy` | `m1-taint-enforcement` | — | done | as above |
| `taint.no-exfil` | `m1-taint-enforcement` | — | done | as above |
| `taint.quarantine-carries-origin` | `m1-extraction-provenance` | `m1-taint-enforcement` | missing | no `std/flow.Quarantine` exists; `std/flow` ships `Classify` and `Extract` only |
| `taint.subflow-carries-origin` | moved to M3 (2.3) | — | moved | `FlowAsTool` belongs to `subflows` (M3); the registry now reads `deferred_to: M3` and the M3 `subflows` change owns it |
| `taint.provider-tool-post-hoc` | `m1-provider-tool-governance` | `m1-taint-enforcement`, provider contract | missing | no provider tool path exists |

### 2.2 tools (9 M1-ranked)

| ID | Owner | Depends on | Status | Evidence |
|---|---|---|---|---|
| `tools.egress-metadata-blocked` | `m1-egress-policy` | — | done | `core/tool_call_egress_test.go`; shipped in `archive/2026-10-06-m1-egress-policy` |
| `tools.egress-redirect-revalidated` | `m1-egress-policy` | — | done | as above |
| `tools.egress-rebinding-safe` | `m1-egress-policy` | — | done | as above |
| `tools.egress-size-limit` | `m1-egress-policy` | — | done | as above |
| `tools.egress-audited` | `m1-egress-policy` | — | done | as above |
| `tools.provider-tool-registered` | `m1-provider-tool-governance` | provider contract | missing | no provider-executed tool path exists |
| `tools.provider-tool-gate-before-call` | `m1-provider-tool-governance` | provider contract | missing | as above |
| `tools.provider-approval-as-suspension` | `m1-provider-tool-governance` | provider contract | missing | as above |
| `tools.provider-result-origin` | `m1-provider-tool-governance` | `m1-taint-enforcement` | missing | as above |

M0-ranked, finished by M1:

| ID | Owner | Status | Evidence |
|---|---|---|---|
| `tools.egress-policy-required` | `m1-egress-policy` | done | `core/egress_build_test.go`, `core/tool_egress_test.go`; `Build` wires `CheckEgress`/`RequiresEgress` and the egress policy option |
| `tools.verify-reconciles-unknown` | `m1-verify-public-paths` | done | `core/verify_reconciliation_scenarios_test.go`, `core/verify_resume_done_scenarios_test.go`; shipped in `archive/2026-10-06-m1-verify-public-paths` |

### 2.3 Milestone conflict to settle in `m1-extraction-provenance`

`taint.subflow-carries-origin` is settled: **moved to M3**. The scenario names a `FlowAsTool` child, and `FlowAsTool` is a `subflows` deliverable — isolation, effects, nested suspension and limits all belong to M3 (`docs/overview.md`). Landing only the provenance carrier in M1 would build a partial seam inside an M3 capability and would still be unprovable on a public path, because no public child-flow entry point exists in M1.

Recorded action, applied here:

1. `openspec/scenarios.json` reads `deferred_to: M3` for the ID, so `task spec:gate GATE=M1` no longer requires it and the M3 gate does.
2. The M3 `subflows` change owns the scenario; its design must state that a child's output value carries the child's source origin rather than `OriginTool` of the child alone, and that the taint hook of the parent reads it. That obligation is recorded here so it cannot be lost with the mover.
3. `m1-extraction-provenance` keeps `taint.quarantine-carries-origin` only, and its design cites this section as the reason the subflow case is absent.

This is a scope reduction, approved explicitly; it is the one place in this matrix where a scenario changed milestone rather than owner.

### 2.4 model (11 IDs: 10 M1-ranked plus one M0-ranked named by the proposal)

| ID | Owner | Depends on | Status | Evidence |
|---|---|---|---|---|
| `model.status-to-class` | `m1-provider-conformance-contract` | — | missing | no provider adapter exists; `adapter/openai` and `adapter/anthropic` are not directories |
| `model.retry-after-parsed` | `m1-provider-conformance-contract` | — | missing | as above |
| `model.cache-explicit-breakpoints` | `m1-provider-conformance-contract` | — | missing | as above |
| `model.schema-never-ignored` | `m1-provider-conformance-contract` | — | missing | as above |
| `model.usage-estimated` | `m1-provider-conformance-contract` | — | missing | as above |
| `model.extra-collision` | `m1-provider-conformance-contract` | — | missing | as above |
| `model.raw-round-trip` | `m1-provider-conformance-contract` | — | missing | as above |
| `model.isolation-per-key` | `m1-provider-conformance-contract` | — | missing | provider-key selection ships in core; no adapter consumes it |
| `model.key-never-in-telemetry` | `m1-provider-conformance-contract` | — | missing | as above |
| `model.provider-tool-usage` | `m1-provider-tool-governance` | provider contract | missing | `Usage.ProviderToolCalls` ships as a field; nothing increments it |
| `model.undeclared-fidelity` | `m1-provider-conformance-contract` | — | integration gap | M0-ranked; a subtest exists, and the proposal names the ID as an adapter conformance obligation. The change re-verifies it against a real adapter without re-claiming the ID |

The eight IDs the `m1` proposal names for the two adapters map onto the rows above. The two adapters (`m1-openai-provider`, `m1-anthropic-provider`) run the shared `conformance.Model` suite that `m1-provider-conformance-contract` extends; an ID is claimed once, by the suite, and both adapters must run it. An adapter-specific requirement — error-class normalization, version reporting, cache markers — lands in that adapter's own design without claiming a second ID for the same behaviour.

### 2.5 working-state (7)

| ID | Owner | Depends on | Status | Evidence |
|---|---|---|---|---|
| `working-state.subject-memory-isolated` | `m1-subject-memory` | — | integration gap | `stores.MemoryStore` and `ErrMemoryStoreRequired` ship; no memory tool is registered by any preset |
| `working-state.memory-write-untrusted-denied` | `m1-subject-memory` | `m1-taint-enforcement` | integration gap | as above; the untrusted-turn guard is `std/guard`'s `StageContext` |
| `working-state.memory-write-schema` | `m1-subject-memory` | — | integration gap | as above |
| `working-state.memory-read-carries-origin` | `m1-subject-memory` | `m1-taint-enforcement` | integration gap | as above |
| `working-state.memory-consolidate-at-done` | `m1-subject-memory` | — | integration gap | as above |
| `working-state.memory-forget` | `m1-subject-memory` | — | integration gap | as above |
| `working-state.memory-ttl-eviction` | `m1-subject-memory` | — | missing | `MemoryEntry` carries no timestamps and the memory store enforces no TTL |

### 2.6 redaction (8) and guards (1)

| ID | Owner | Depends on | Status | Evidence |
|---|---|---|---|---|
| `redaction.pseudonym-stable-within-session` | `m1-redaction-pipeline` | — | done | `core/redaction_scenarios_test.go`; shipped in `archive/2026-10-06-m1-redaction-pipeline` |
| `redaction.stores-never-see-raw-entities` | `m1-redaction-pipeline` | — | done | `core/redaction_sinks_test.go`, `core/redaction_native_wiring_test.go` |
| `redaction.streaming-rehydration` | `m1-redaction-pipeline` | — | done | `core/redaction_scenarios_test.go` |
| `redaction.hallucinated-token-dropped` | `m1-redaction-pipeline` | — | done | `core/redaction_scenarios_test.go` |
| `redaction.residency-enforced-at-build` | `m1-redaction-pipeline` | — | done | `core/residency_build_test.go` |
| `guards.redaction` | `m1-redaction-pipeline` | — | done | `core/redaction_scenarios_test.go` |
| `redaction.erasure` | `m1-subject-erasure-residency` | redaction, subject memory | done early | `core/erasure_test.go`; `Stack.EraseSubject` landed inside the redaction change, so `m1-subject-erasure-residency` records evidence and keeps any remaining scope (blob store, memory store) |
| `redaction.erase-removes-memory` | `m1-subject-erasure-residency` | `m1-subject-memory` | done early | `core/erasure_test.go`, through the `Notes` port |
| `redaction.erase-removes-blobs` | `m1-subject-erasure-residency` | blob retention | done early | `core/erasure_test.go`; `EraseReport.BlobRefs` reports refs for a later blob-store erasure |

### 2.7 flags (5)

| ID | Owner | Depends on | Status | Evidence |
|---|---|---|---|---|
| `flags.frozen-snapshot` | `m1-flags-control` | — | missing | no `std/flags` package exists |
| `flags.never-widen` | `m1-flags-control` | `m1-taint-enforcement` (hard-block ordering) | missing | as above |
| `flags.stale-then-deny` | `m1-flags-control` | as above | missing | as above |
| `flags.from-default-suspends` | `m1-flags-control` | as above | missing | as above |
| `flags.replay-asserts-snapshot` | `m1-flags-control` | — | missing | as above |

### 2.8 chains (6)

| ID | Owner | Depends on | Status | Evidence |
|---|---|---|---|---|
| `chains.hedge-fires-on-ttft` | `m1-hedged-model-calls` | provider contract | missing | no hedging exists; `std/route` ships routing and breakers |
| `chains.hedge-never-same-endpoint` | `m1-hedged-model-calls` | provider contract | missing | as above |
| `chains.hedge-loser-cancelled-and-charged` | `m1-hedged-model-calls` | provider contract | missing | as above |
| `chains.hedge-budget-cap` | `m1-hedged-model-calls` | `m1-model-retirement-cost` (cost tags) | missing | as above |
| `chains.hedge-single-journal-step` | `m1-hedged-model-calls` | journal contract | missing | as above |
| `chains.hedge-refused-with-provider-tools` | `m1-hedged-model-calls` | `m1-provider-tool-governance` | missing | as above |

`std/route/route.go:97-105` calls `Breaker.Allow()` from `Candidates()`; a hedge must not consume a half-open probe for a request it discards. `m1-hedged-model-calls` decides admission before a probe is consumed and records the choice.

### 2.9 cache (3)

| ID | Owner | Depends on | Status | Evidence |
|---|---|---|---|---|
| `cache.key-includes-manifest` | `m1-exact-match-cache` | final guard acceptance | missing | no `std/cache` package exists |
| `cache.never-cache-failures` | `m1-exact-match-cache` | final guard acceptance | missing | as above |
| `cache.refused-on-conversation` | `m1-exact-match-cache` | — | missing | as above |

### 2.10 lifecycle (7)

| ID | Owner | Depends on | Status | Evidence |
|---|---|---|---|---|
| `lifecycle.sunset-inside-notice-window` | `m1-model-retirement-cost` | provider metadata contract | missing | no retirement logic exists |
| `lifecycle.deprecated-never-silent` | `m1-model-retirement-cost` | as above | missing | as above |
| `lifecycle.successor-params-validated` | `m1-model-retirement-cost` | as above | missing | as above |
| `lifecycle.cost-tags-forwarded` | `m1-model-retirement-cost` | provider contract | integration gap | `CostTags` ships on `RunInfo`; no adapter forwards them |
| `lifecycle.cache-write-socialized` | `m1-model-retirement-cost` | `m1-exact-match-cache` | missing | as above |
| `lifecycle.upcast-on-read` | `m1-schema-read-paths` | — | integration gap | `core/stores/schema.go` declares the upcaster registry and `testkit/storetest/schemas.go` the suite; no reader calls the registry |
| `lifecycle.historical-fixtures` | `m1-schema-read-paths` | — | integration gap | as above |

### 2.11 stores (1), limits (1), messages (1)

| ID | Owner | Depends on | Status | Evidence |
|---|---|---|---|---|
| `stores.same-transaction-journal` | `m1-postgres-stores` | schema-read, erasure contracts | missing | no `adapter/postgres` module exists; `CompleteTx` is unimplemented |
| `limits.provider-calls-count` | `m1-provider-tool-governance` | provider contract | integration gap | `RunLimits` ships; the provider-call counter has no increment site |
| `messages.blob-provider-id-reused` | `m1-s1-s2-acceptance` | decomposition (below) | missing | `messages` spec declares the provider-id rule; no adapter exercises it |

### 2.12 streams (2), suspension (1), identity (4), telemetry (1), flow (4)

| ID | Owner | Depends on | Status | Evidence |
|---|---|---|---|---|
| `streams.notice-retry-and-dead-letter` | `m1-run-notifications` | `m1-egress-policy` | integration gap | the `Runs` notice outbox ships (`core/stores/runs_notices.go`); no dispatcher retries or dead-letters |
| `streams.webhook-standard-signed` | `m1-run-notifications` | `m1-egress-policy` | missing | no webhook transport exists |
| `suspension.approval-notice-delivered` | `m1-run-notifications` | as above | integration gap | the outbox ships; notices have no delivery path |
| `identity.notice-endpoint-per-tenant` | `m1-run-notifications` | as above | missing | as above |
| `flow.title-generated-after-first-done` | `m1-session-metadata-handoff` | — | missing | no title generation exists |
| `flow.title-locked-by-rename` | `m1-session-metadata-handoff` | — | missing | as above |
| `flow.request-human-ends-run` | `m1-session-metadata-handoff` | — | missing | as above |
| `flow.handback-fences-operator-turns` | `m1-session-metadata-handoff` | — | missing | as above |
| `telemetry.feedback-metric-labels` | `m1-session-metadata-handoff` | — | integration gap | `stores.FeedbackStore` and the `streams.feedback-recorded-event` path ship; the metric labels do not |
| `identity.idempotency-key-transport-only` | `m1-s1-s2-acceptance` | decomposition (below) | integration gap | `WithIdempotencyKey` ships in `core/identity.go`; nothing enforces transport-only |
| `identity.values-survive-shield` | `m1-s1-s2-acceptance` | decomposition (below) | integration gap | `std/shield.go` ships; the identity-value contract is unverified |
| `identity.detached-reissues-credential` | `m1-s1-s2-acceptance` | decomposition (below) | integration gap | detach ships (`core/attach.go`, `core/stream_stall.go`); credential re-issue is unverified |

### 2.13 Obligations without a scenario ID

These come from the `m1` proposal scope and the M1 row of `docs/overview.md` and are not in the registry. Each needs a task and an acceptance statement; none may be dropped because it has no ID.

| Obligation | Owner | Status | Evidence |
|---|---|---|---|
| `ToolSpec.Trust` and the manifest hash's `trust` input, declared by the `tools` spec and missing from the shipped struct and `std/manifest.go` | `m1-taint-enforcement` | integration gap | discovered while wiring the trifecta's untrusted leg; the `tools` ToolSpec block and the *Tool spec pinning* requirement both name the field, and `ToolManifestHash` does not hash it |
| `Verify` end-to-end (Failed reconciliation, audit, lifecycle execution) | `m1-verify-public-paths` | integration gap | `std/verify.go` has no `Failed` reconciliation and discards journal errors |
| `std/redact` rules + HMAC `Redactor` + `EraseSubject` | `m1-redaction-pipeline`, `m1-subject-erasure-residency` | missing | neither package exists |
| `std/cache` key builder + exact-match cache | `m1-exact-match-cache` | missing | no `std/cache` |
| `std/hedge` | `m1-hedged-model-calls` | missing | no `std/` hedging package |
| `std/notify` + `std/notify/webhook` | `m1-run-notifications` | missing | no `std/notify` |
| `std/egress` + `std/tool/http.Fetch` | `m1-egress-policy` | missing | `std/tool` is empty |
| `std/flags`; adapter/openfeature later | `m1-flags-control` | missing | no `std/flags`; openfeature stays out of M1 |
| sunset/successor validation, schema versions, upcaster registry, cost tags | `m1-model-retirement-cost`, `m1-schema-read-paths` | integration gap | registry and suite types ship; readers do not |
| adapter/openai (+vLLM), adapter/anthropic with error normalization and version reporting | `m1-provider-conformance-contract`, `m1-openai-provider`, `m1-anthropic-provider` | missing | `adapter/` holds `otel` only |
| adapter/postgres, all ports incl. `AuditLog`, plus `CompleteTx` | `m1-postgres-stores` | missing | no `adapter/postgres` |
| limit.Local + breaker, class-based retry/fallback, router Static/ByLatencyClass | `m1-s1-s2-acceptance` | shipped, integration gap for `ByLatencyClass` | `std/limit/{model,run}.go`, `std/retry/retry.go`, `std/route/{route,breaker,fallback}.go` ship; `ByLatencyClass` has no implementation |
| guards: all four stages + output modes + fallback | `m1-s1-s2-acceptance` | shipped | `std/guard` ships six files; the acceptance package verifies the composed path rather than rebuilding it |
| tool/exec | `m1-s1-s2-acceptance` | integration gap | `core/tool.go` ships `WithEffect`/`WithScopes`; the exec runner and `AllowHostExec` warning are unverified against the spec |
| Notes, OutputStore, Waker port, `Detached` + Attach | `m1-s1-s2-acceptance` | shipped | `std/notes`, `std/outputs/read_output.go`, `core/suspension_delivery.go`, `core/attach.go` |
| structured-output hardening | `m1-s1-s2-acceptance` | shipped | `std/structured` ships six files |
| `std.ExplainHandler` | `m1-s1-s2-acceptance` | missing | no HTTP handler exists; must not grow a transport module |
| `quickstart` and `excursions` S1–S2 complete | `m1-s1-s2-acceptance` | integration gap | both examples run offline; S2's transaction and cross-pod approval require Postgres |

### 2.14 Decomposition bucket

`m1-s1-s2-acceptance` owns four scenario IDs (`messages.blob-provider-id-reused`, `identity.idempotency-key-transport-only`, `identity.values-survive-shield`, `identity.detached-reissues-credential`) that belong to capabilities no other M1 change covers. Its first task is to decompose them into their own bounded change (`m1-messages-blobs`, `m1-identity-detach`) before either can enter implementation. Until that decomposition exists, those IDs have an owner but no implementation plan.

### 2.15 Lane status

`m1-egress-policy` and `m1-taint-enforcement` are implemented and their scenario ids pass on the public path. `m1-verify-public-paths`, `m1-schema-read-paths`, `m1-redaction-pipeline` and `m1-provider-conformance-contract` are authored and validate with `openspec validate --strict`; their seams are sealed in §3.1 (ADR-0157) and enter implementation once their designs cite them and the package review closes. Every other change named above is the recorded M1 owner of its obligations and is authored after its dependencies settle, each with its own contract review. No implementation starts before its change package is authored, its seams are sealed and the package is reviewed.

## 3. Shared contract decisions

Sealed. The first changes implement these; they do not re-decide them. ADR-0155 is the record.

- **D1 — `TaintPolicy.MaxWindowBytes`.** The normative struct gains `MaxWindowBytes int`; `0` means the 256 KiB default; truncation drops the oldest blocks first and counts `gohan.taint.window_truncated`. Rule 1 is the authority; the struct catches up. (`taint` spec Contract and rule 1, ADR-0155 §1.)
- **D2 — Ownership.** `std/taint` owns `TaintPolicy`, its matcher and `Default()`; core owns the hook slot only. No policy value or matcher in `core` (`docs/design/architecture.md` §4.2a, ADR-0155 §2).
- **D3 — Registration seam.** `NativeSpec` gains a `TaintPolicy` field holding the flow's `types.TaintHook`; `Build` resolves it like `Decider` and wires the gate with it. A flow with no hook keeps today's behaviour. `agent.WithTaintPolicy` is not implemented and no second builder is added (ADR-0155 §3).
- **D4 — Trifecta.** `Build` computes the three legs per flow and fails with `ErrTaintTrifecta{Flow, Exfil, PrivateRead, Untrusted}` unless the flow declared a policy; counts `gohan.build.trifecta{flow}` (ADR-0155 §4).
- **D5 — Exfil derivation has one owner.** Egress owns it: `Build` calls `RequiresEgress`, `CheckEgress` and `DeriveExfil` per registered tool and writes the derived `Capabilities.Exfil` into the resolved spec. The taint hook reads `ToolSpec.Capabilities.Exfil` and never re-derives it (ADR-0155 §5).
- **D6 — Permission order.** Hard and live denies → taint hook → session grants → decider. A live deny beats a taint `Ask` and requests no approval (`taint.gate-order`, ADR-0155 §6).
- **D7 — Taint window input.** The window is computed over the run's raw blocks since the last `OriginUser` message, taken from the turn's own messages at the gate invocation. Projection and compaction never launder taint: the computation never reads a projected view, and it is capped by D1.

- **D8 — Network declaration and egress audit.** `ToolSpec` gains `Network bool`, the reach fact `Build` consumes; `WithHTTP()` ships in the egress change because the Egress requirement names it; `stores.AuditRecord` gains `Host` and `Reason` and `AuditEgressDenied` joins the kind list, carrying host and reason only. Reach never implies `Capabilities.Exfil` (ADR-0156).

### 3.1 Lane-3 seams (ADR-0157)

`m1-verify-public-paths`, `m1-schema-read-paths`, `m1-redaction-pipeline` and `m1-provider-conformance-contract` were authored against the live code and each stopped on a port that did not exist or did not carry the data its requirement is written against. ADR-0157 seals them:

- **Redaction.** `core` declares `Redactor` and the policy value types, `std/redact` implements them, and registration is the `WithRedactor` option the `build` contract already lists — no second builder, no `std` import in core. The map is a store of its own, `Stores.RedactionMaps`, tenant-keyed and retained apart from run state.
- **Residency.** A store serving a residency-bound tenant implements `RegionProvider`; `Build` is fail-closed when the interface is missing, the region is empty, or it differs from the tenant's.
- **Verification.** The journal retains each call's canonical arguments and lists the session's `Unknown` entries with them; `WithLifecycleVerify` stays the lifecycle carrier; a `Failed` reconciliation preserves the recorded `ToolError`; `std.UncertainOutcomeError` becomes an alias of the `types` type so a run that ends uncertain is classified as uncertain rather than internal.
- **Schema.** A stored record is persisted in a `RecordEnvelope` naming the shape and the version it was written at; one generic decoder upcasts a copy on read and emits `gohan.schema.upcast{from,to}` per successful chain; domain types stay version-free.
- **Provider options.** `NativeSpec.ModelOptions` carries the flow's static `Extra`, and `Build` refuses a governed-key collision; a per-request entry stays the adapter's call-time duty.

Coverage honesty for `m1-provider-conformance-contract`: this lane earns `model.undeclared-fidelity` and `model.extra-collision`, both `Build`-side. The other eight `model.*` ids name what an adapter returns and stay unearned until `m1-openai-provider` and `m1-anthropic-provider` run the shared `conformance.Model` suite. The reference double proves the suite fails a non-conforming implementation; it is not counted as scenario coverage.

## 4. What the first changes must not do

- They add no HTTP transport: `api/gohan.yaml`, `adapter/httpapi` and `std.ExplainHandler`'s handler land in M4 except where 2.13 assigns them.
- They cut no release and add no tag.
- They do not edit a capability spec outside their own change package; the main-spec edit, the ADR citation and the `docs/design/types.md` regeneration happen when the change is applied (`openspec/project.md`, *Change workflow* step 5).
- They do not rebuild shipped behaviour. `std/route`, `std/limit`, `std/retry`, `std/guard`, `std/structured`, `std/notes`, `std/outputs`, detach and `Attach` exist; a change that believes one of them is wrong must show the public-path gap first.
