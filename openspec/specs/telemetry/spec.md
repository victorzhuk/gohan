# Telemetry and prompt sources

Capability: `telemetry` · Spec v1.1 (ADR-0123) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `telemetry` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.


## Contract

### 6.16 Telemetry

**A dependency-free port with std policy.** Core declares a small `Telemetry` port (span start/end with attributes, counters and recordings); `std/telemetry` owns the conventions, the metric label policy and a `Decorate(sink, convention)` wrapper that applies `Convention.ApplyAttrs` to initial span attributes, final span attributes, counters and recordings; `adapter/otel` owns the vendor translation. The root module stays dependency-free (ADR-0145 supersedes the tracing-seam clause of ADR-0066). gohan emits spans and metrics with `gohan.*` attributes as the source of truth and maps them through a `Convention`:

```go
type Convention struct {
	SchemaURL string
	Map       func(attr string) []string
	Content   ContentMapping
}

type ContentMapping int

const (
	ContentNone ContentMapping = iota
	ContentHashes
	ContentRedacted
	ContentFull
}
```

`std/telemetry` ships `GenAI` (pinned to the semantic-conventions-genai commit validated at release; emits `gen_ai.operation.name`, `gen_ai.provider.name`, `gen_ai.request.model`, `gen_ai.response.model`, `gen_ai.usage.input_tokens`/`output_tokens`, opt-in `gen_ai.input.messages`/`output.messages`) and `Langfuse` (adds `langfuse.session.id`, `langfuse.trace.tags`, `langfuse.release` = manifest hash, `langfuse.observation.type`, `langfuse.prompt.name`/`version`, and content under the names Langfuse reads; `langfuse.user.id` only when the redactor policy allows). A rename upstream is a one-line change in `std`. Operation names: `invoke_agent` (agent flow), `invoke_workflow` (graph/workflow flow), `chat`, `execute_tool`, `retrieval` (tools returning `Document` blocks), `plan` (router/decider spans).

```go
type PromptSource interface {
	Prompt(ctx context.Context, name, label string) (Prompt, error)
}

type Prompt struct {
	Name     string
	Label    string
	Version  string
	Text     string
}
```

`AgentSpec.Instruction` may be a literal or `gohan.PromptRef{Name, Label}` resolved through the `PromptSource` at run start (cached with TTL; embedded fallback text is required so an unavailable source never blocks a request). The resolved `Version` is stamped on the run span, the audit record and, for A/B, chosen by a sticky selector (`Decider` over session hash). `adapter/langfuse` implements `PromptSource` over the prompt-management API, an `evals.Sink` for scores via the ingestion API, and a dataset provider.

Spans (OTel GenAI semantic conventions where defined, *verify* current names): `invoke_agent` / `gohan.flow` (run), `invoke_workflow` (graph/workflow flow), `chat` (model call), `plan` (router/decider), `execute_tool` (tool call), `retrieval` (tool call returning `Document` blocks), `gohan.guard` (guard evaluation), `gohan.suspend`, `gohan.resume`.

Canonical attribute keys (core emits these; the `Convention` layer maps them to `gen_ai.*` where a semantic convention exists):

| Key | On | GenAI mapping |
|---|---|---|
| `gohan.flow`, `gohan.session_id`, `gohan.run_id`, `gohan.root_run_id`, `gohan.parent_run_id`, `gohan.turn` | run, model, tool spans | `gen_ai.conversation.id` for session |
| `gohan.tenant`, `gohan.subject` | run span | — |
| `gohan.release`, `gohan.variant`, `gohan.mode` | all spans, all metrics | — |
| `gohan.model.profile`, `gohan.model.version`, `gohan.model.endpoint`, `gohan.model.key_id` | model span | `gen_ai.request.model`, `gen_ai.response.model`, `gen_ai.provider.name` |
| `gohan.usage.input`, `gohan.usage.cached_input`, `gohan.usage.cache_write`, `gohan.usage.output`, `gohan.usage.hedge_loser`, `gohan.cost` | model span, run span (sums) | `gen_ai.usage.input_tokens`, `gen_ai.usage.output_tokens` |
| `gohan.tool.name`, `gohan.tool.effect`, `gohan.tool.outcome`, `gohan.journal.replayed` | tool span | `gen_ai.tool.name` |
| `gohan.guard.stage`, `gohan.guard.verdict` | guard span | — |
| `gohan.router.decision`, `gohan.router.confidence` | model span | — |
| `gohan.approver`, `gohan.suspend.reason` | suspend/resume spans | — |
| `gohan.notice.kind` | notice delivery span | — |

Metric labels are an allow-list enforced by `std/telemetry` at registration **and at emission**: `flow`, `tool`, `profile`, `class`, `release`, `variant`, `mode`, `stage`, `reason`, `kind`, `limit`, `lifecycle`, `source`, `judge`, `name`, `target`, `provider`, `notes`, `from`, `to`; `tenant` only with `WithTenantLabel()`; `session_id`, `run_id`, `subject`, `approver` never. A metric registered with any other label fails `Build`. Emission is filtered by the registration: an unregistered metric is not emitted, a label that is not registered for that metric is omitted, a forbidden identity attribute is omitted even when a caller passes a canonical `gohan.*` spelling, `tenant` is forwarded only with `WithTenantLabel()`, duplicate normalized labels keep the last admissible caller value, and the harness's own release/variant stamps override caller spellings. Emission methods stay void and never panic for an invalid attribute.

Logging contract: core takes `WithLogger(*slog.Logger)` (default `slog.Default()`) and derives a per-run logger with the attribute keys above as `slog.Attr`s. Levels: run start, finish, suspend, resume and `Recover` actions at `Info`; per-step and per-tool records at `Debug`; `Build` warnings at `Warn`; model and tool errors at `Debug` (they are events, metrics and spans, not log noise); nothing at `Error` except store failures that abort a run. Message content, tool arguments and results, prompts, credentials and `Raw` values are never logged at any level; the only content-bearing sink is span content capture behind the `Redactor`.

Content capture (prompts, outputs, tool args/results, retrieved `Document`s) is off by default; when enabled it passes through the configured `Redactor` (placement 3 in §6.8d). `CostTags` and `Residency` are span attributes and are forwarded as provider request metadata where the provider supports it.

Metrics:

| Metric | Why |
|---|---|
| `gohan.model.ttft` histogram | prefill-bound detection; interactive UX |
| `gohan.model.tpot` histogram | decode-bound detection; agentic UX |
| `gohan.model.inflight` gauge, `gohan.model.queue_wait` histogram | bulkhead saturation vs engine throughput |
| `gohan.model.tokens` counter (input, cached_input, output) | prefix-cache effectiveness, cost |
| `gohan.cost` counter by flow (a `tenant` label needs `WithTenantLabel()`) | budgets, billing |
| `gohan.tool.calls` / `gohan.tool.errors` by tool | tool scoping decisions |
| `gohan.loop.detected`, `gohan.max_turns.reached` | non-terminating agents |
| `gohan.guard.blocked` by stage, `gohan.fallback.used` | guardrail health |
| `gohan.router.decisions` by target, `gohan.router.escalations` | routing quality |
| `gohan.suspend` by reason, `gohan.resume.latency` | HITL and async health |
| `gohan.journal.replayed`, `gohan.journal.unknown_outcome`, `gohan.journal.key_pinned`, `gohan.tool.repeat_intent` | exactly-once health |
| `gohan.tool.unknown`, `gohan.tool.invalid_args` | hallucinated tools/args |
| `gohan.run.recovered`, `gohan.run.abandoned`, `gohan.run.stale` gauge | crash recovery health |
| `gohan.shutdown.preempted`, `gohan.shutdown.incomplete`, `gohan.shutdown.duration` | deploy safety |
| `gohan.feedback{name, flow, release, variant}` (histogram for numeric, counter otherwise) | online quality signal from users |
| `gohan.limit.exceeded`, `gohan.limit.warning` by limit | runaway runs |
| `gohan.output.stored` by tool | context pressure from large outputs |
| `gohan.guard.context_rejected` by provider/notes | memory poisoning attempts |
| `gohan.sandbox.seconds` by lifecycle, `gohan.sandbox.opens`, `gohan.sandbox.cold_start`, `gohan.sandbox.secret_leak`, `gohan.sandbox.egress_denied` | sandbox cost, warm-pool health, containment |
| `gohan.taint.denied{tool}`, `gohan.taint.asked{tool}`, `gohan.taint.post_hoc{tool}`, `gohan.taint.window_truncated`, `gohan.build.trifecta{flow}` | information-flow enforcement, exposure surface |
| `gohan.eval.score{flow, variant, judge}`, `gohan.eval.unstable`, `gohan.shadow.runs`, `gohan.shadow.suppressed{tool}`; every `gohan.*` metric carries `release` and `variant` | release gating, canary slicing |
| `gohan.skill.loaded{name}`, `gohan.skill.rejected{reason}`, `gohan.skill.resource_reads` | skill usage and containment |
| `gohan.tool.manifest_drift` by source | imported tool definition changed under a pinned manifest (rug pull) |
| `gohan.context.compactions` by kind, `gohan.context.tokens_cleared`, `gohan.context.tokens_before`/`tokens_after`, `gohan.context.cache_invalidated` | context pressure; cost of compaction vs cache churn |
| `gohan.tool.effect_capped` by source | untrusted tools hitting `MaxEffect` |
| `gohan.run.cancelled_shielded` | side effects completed under client cancellation |
| `gohan.stream.attached`, `gohan.stream.replayed_events` | reconnect health |
| `gohan.audit.appended`, `gohan.audit.append_failed` | audit trail health (append failure aborts the step) |
| `gohan.block.dropped{kind}`, `gohan.block.degraded{kind}` | cross-provider fidelity loss |
| `gohan.approval.*` (requested, approved, rejected, expired, granted_by_scope, review_time, queue_age, rate, rubber_stamp_suspected) | human-in-the-loop health |
| `gohan.tool.deferred_activated`, `gohan.tool.context_share` | tool-catalog pressure |
| `gohan.prompt.fallback`, `gohan.prompt.version` attr | prompt source health and A/B attribution |
| `gohan.cache.hit`, `gohan.cache.miss` | cache effectiveness under the contract |
| `gohan.operation.duplicate`, `gohan.run.resuming_recovered`, `gohan.flag.denied`, `gohan.flag.stale_suspended`, `gohan.definition.version` attr | engine composition, dedup, flags, definitions |
| `gohan.redact.entities{kind}`, `gohan.redact.unknown_token`, `gohan.erasure.completed` | PII pipeline health |
| `gohan.model.deprecated`, `gohan.model.sunset_days` gauge | retirement readiness |
| `gohan.cost.per_run{flow}` histogram, `gohan.cost.cache_write`, `gohan.cost.batch_item` | unit cost and shared-cost attribution |
| `gohan.schema.upcast{from,to}` | stored-data evolution |
| `gohan.model.refusal_as_json`, `gohan.tool.truncated_args`, `gohan.structured.validation_failed` | structured output health |
| `gohan.resume.fallback_replay`, `gohan.checkpoint.incompatible` | checkpoint versioning |
| `gohan.pool.queue_wait`, `gohan.pool.deferred{class}`, `gohan.cost.anomaly` | shared-quota fairness, cost |

Spans and metrics come from the governed chains, so all backends produce the same tree.


## Requirements

### Requirement: Telemetry

#### Scenario: same tree on all backends
ID: `telemetry.same-tree-on-all-backends`
- WHEN a run with 2 model calls and 1 tool call executes on native, eino and adk-go
- THEN each produces 1 run span, 2 `chat` children, 1 `execute_tool` child with identical attribute keys

#### Scenario: TTFT and TPOT
ID: `telemetry.ttft-and-tpot`
- WHEN a scripted model streams 10 chunks with fixed delays
- THEN `gohan.model.ttft` and `gohan.model.tpot` record values within tolerance

#### Scenario: loop detection
ID: `telemetry.loop-detection`
- WHEN the model calls the same tool with identical args 4 times and the threshold is 3
- THEN the 4th call is denied and `gohan.loop.detected` increments

### Requirement: Attributes, labels and logging

#### Scenario: canonical keys on spans
ID: `telemetry.canonical-keys`
- WHEN a run executes one model call and one tool call
- THEN every span carries the keys the attribute table places on it, the run, model and tool spans carry `gohan.flow`, `gohan.session_id`, `gohan.run_id`, `gohan.root_run_id`, `gohan.parent_run_id` and `gohan.turn`, and the `GenAI` convention maps `gohan.model.profile` to `gen_ai.request.model`

#### Scenario: forbidden label rejected
ID: `telemetry.forbidden-label`
- WHEN a metric is registered with a `session_id` label
- THEN `Build` fails naming the metric and label

#### Scenario: no content in logs
ID: `telemetry.no-content-in-logs`
- WHEN a run logs at `Debug` with content capture enabled
- THEN no log record contains message text, tool arguments, tool results or credential values

### Requirement: Telemetry conventions and prompts

#### Scenario: rename is config
ID: `telemetry.rename-is-config`
- WHEN the `GenAI` convention maps `gohan.model.version` to a new attribute name
- THEN no core package changes and existing spans carry the new name after redeploy

#### Scenario: Langfuse preset
ID: `telemetry.langfuse-preset`
- WHEN the `Langfuse` convention is active and content capture is off
- THEN spans carry `langfuse.session.id`, `langfuse.release` and `langfuse.prompt.version`, and no `langfuse.observation.input`

#### Scenario: prompt source outage
ID: `telemetry.prompt-source-outage`
- WHEN the `PromptSource` is unreachable and the cache is empty
- THEN the run uses the embedded fallback text, stamps `version=fallback`, and `gohan.prompt.fallback` increments

#### Scenario: feedback metric labels
ID: `telemetry.feedback-metric-labels`
- WHEN feedback `csat: 4` is recorded for a run under release `r7`, variant `b`
- THEN `gohan.feedback{name=csat, release=r7, variant=b}` observes 4 and no label carries the subject or comment

#### Scenario: replay strictness
ID: `telemetry.replay-strictness`
- WHEN a cassette was recorded with `Version: v1` and the profile now pins `v2`
- THEN `Strict` replay fails naming the version mismatch; `ByTurn` replays with a warning
