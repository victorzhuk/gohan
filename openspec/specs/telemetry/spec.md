# Telemetry and prompt sources

Capability: `telemetry` · Spec v1.1 (ADR-0123) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `telemetry` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.


## Contract

### 6.16 Telemetry

**A dependency-free port with std policy.** Core declares a small `Telemetry` port (span start/end with attributes, counters and recordings); `std/telemetry` owns the conventions, the metric label policy and a `Decorate(sink, convention)` wrapper that applies `Convention.ApplyAttrs` to initial span attributes, final span attributes, counters and recordings; `adapter/otel` owns the vendor translation. The root module stays dependency-free (ADR-0145 supersedes the tracing-seam clause of ADR-0066).

The core port and its attribute value type (shipped in `core/types`; `Attr` values come from the `String`, `Int`, `Float` and `Bool` constructors, so an adapter maps them without reflection):

```go
type Telemetry interface {
	StartSpan(ctx context.Context, name string, attrs ...Attr) (context.Context, func(attrs ...Attr))
	Count(ctx context.Context, name string, n int64, attrs ...Attr)
	Record(ctx context.Context, name string, v float64, attrs ...Attr)
}

type Attr struct {
	Key   string
	Value any
}
```

gohan emits spans and metrics with `gohan.*` attributes as the source of truth and maps them through a `Convention`:

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

### 6.16a Portable telemetry export

Instrumentation stays as declared above. Export is a separate, opt-in contract: dependency-free records and ports in `core/types`, delivery policy in `std/telemetry`, SDK translation in `adapter/otel` (ADR-0174). The frozen `Telemetry` methods never change; export attaches through new ports and optional interfaces discovered by type assertion. Borrowed providers (`otel.New`) stay owned by their caller and are never closed by gohan. Owned pipelines report context-bounded flush and shutdown outcomes with unresolved-work counts. Remote acceptance never implies local durability; stable IDs permit deduplication, never exactly-once aggregation.

Records (version, stable ID, resource identity, signal kind, typed payload; completed spans cross SDK export boundaries, never replayed `StartSpan`):

```go
type ExportSignalKind string

const (
	SignalSpan   ExportSignalKind = "span"
	SignalMetric ExportSignalKind = "metric"
)

type RecordID string

const CurrentExportSchemaVersion = 1

type ResourceIdentity struct {
	Name  string `json:"name"`
	Attrs []Attr `json:"attrs,omitempty"`
}

type Source struct {
	Name      string `json:"name"`
	Version   string `json:"version,omitempty"`
	SchemaURL string `json:"schema_url,omitempty"`
}

type SpanOutcome string

const (
	SpanUnset SpanOutcome = "unset"
	SpanOK    SpanOutcome = "ok"
	SpanError SpanOutcome = "error"
)

type SpanRecord struct {
	TraceID  string      `json:"trace_id"`
	SpanID   string      `json:"span_id"`
	ParentID string      `json:"parent_id,omitempty"`
	Name     string      `json:"name"`
	Start    time.Time   `json:"start"`
	End      time.Time   `json:"end"`
	Attrs    []Attr      `json:"attrs,omitempty"`
	Outcome  SpanOutcome `json:"outcome"`
	Source   Source      `json:"source"`
}

type InstrumentKind string

const (
	InstrumentCounter   InstrumentKind = "counter"
	InstrumentGauge     InstrumentKind = "gauge"
	InstrumentHistogram InstrumentKind = "histogram"
)

type Temporality string

const (
	TemporalityCumulative Temporality = "cumulative"
	TemporalityDelta      Temporality = "delta"
)

type Instrument struct {
	Name        string         `json:"name"`
	Kind        InstrumentKind `json:"kind"`
	Unit        string         `json:"unit,omitempty"`
	Description string         `json:"description,omitempty"`
	Boundaries  []float64      `json:"boundaries,omitempty"`
}

type HistogramValue struct {
	Count        uint64    `json:"count"`
	Bounds       []float64 `json:"bounds"`
	BucketCounts []uint64  `json:"bucket_counts"`
	Sum          float64   `json:"sum"`
	Min          *float64  `json:"min,omitempty"`
	Max          *float64  `json:"max,omitempty"`
}

type MetricValue struct {
	Int       *int64          `json:"int,omitempty"`
	Float     *float64        `json:"float,omitempty"`
	Histogram *HistogramValue `json:"histogram,omitempty"`
}

type MetricRecord struct {
	Instrument  Instrument  `json:"instrument"`
	Source      Source      `json:"source"`
	Time        time.Time   `json:"time"`
	Start       time.Time   `json:"start,omitempty"`
	Temporality Temporality `json:"temporality"`
	Dimensions  []Attr      `json:"dimensions,omitempty"`
	Value       MetricValue `json:"value"`
}

type ExportRecord struct {
	SchemaVersion int              `json:"schema_version"`
	ID            RecordID         `json:"id"`
	Resource      ResourceIdentity `json:"resource"`
	Signal        ExportSignalKind `json:"signal"`
	Span          *SpanRecord      `json:"span,omitempty"`
	Metric        *MetricRecord    `json:"metric,omitempty"`
}

func (a Attr) MarshalJSON() ([]byte, error)
func (a *Attr) UnmarshalJSON(data []byte) error
```

`Attr` serializes as `{"key":...,"kind":...,"value":...}` with frozen kind literals `string`, `int64`, `float64`, `bool`; decoding validates kind against the value's JSON type. Wire field names are snake_case; times are RFC 3339 with nanoseconds; readers reject unknown `schema_version` values.

Per-record outcomes and ports. A record with no outcome row when a delivery attempt returns is unresolved, not ambiguous, and is retried with its ID unchanged:

```go
type OutcomeStatus string

const (
	OutcomeAccepted  OutcomeStatus = "accepted"
	OutcomeRetryable OutcomeStatus = "retryable"
	OutcomePermanent OutcomeStatus = "permanent"
	OutcomeAmbiguous OutcomeStatus = "ambiguous"
)

type RecordOutcome struct {
	ID      RecordID      `json:"id"`
	Status  OutcomeStatus `json:"status"`
	Message string        `json:"message,omitempty"`
}

type ReplaySemantics string

const (
	ReplayNone      ReplaySemantics = "none"
	ReplayDedupByID ReplaySemantics = "dedup_by_id"
)

type DestinationCapabilities struct {
	Signals     []ExportSignalKind `json:"signals"`
	Replay      ReplaySemantics    `json:"replay"`
	Temporality Temporality        `json:"temporality"`
}

type ExportDestination interface {
	Name() string
	Capabilities() DestinationCapabilities
	Export(ctx context.Context, recs []ExportRecord) ([]RecordOutcome, error)
}

type RecordSink interface {
	Ingest(ctx context.Context, dest string, recs []ExportRecord) error
	Flush(ctx context.Context) (UnresolvedWork, error)
	Shutdown(ctx context.Context) (UnresolvedWork, error)
}

type TelemetryLifecycle interface {
	Flush(ctx context.Context) (UnresolvedWork, error)
	Shutdown(ctx context.Context) (UnresolvedWork, error)
}

type GaugeTelemetry interface {
	Gauge(ctx context.Context, name string, v float64, attrs ...Attr)
}

type UnresolvedWork struct {
	Records       int64            `json:"records"`
	ByDestination map[string]int64 `json:"by_destination,omitempty"`
}

type ExportLimits struct {
	MaxQueueDepth      int
	MaxInstruments     int
	MaxDimensionValues int
	MaxSeries          int
}

const (
	DefaultMaxQueueDepth      = 2048
	DefaultMaxInstruments     = 256
	DefaultMaxDimensionValues = 1024
	DefaultMaxSeries          = 10000
)
```

Delivery state machine per record and destination: `accepted` advances; `permanent` advances and reports a drop; `ambiguous` advances and reports possible remote duplication; `retryable` retains the record; an absent outcome row or a cancelled attempt retains the record unresolved with its ID unchanged. Redaction and metric filtering run before serialization and before queue admission: the content-key policy follows `ContentMapping` (off by default), an optional `Redactor` round-trips surviving string attribute values, forbidden identity labels drop from metric dimensions under either spelling, and the bounds below refuse the whole admission atomically. Diagnostics take an independent path and never re-enter the pipeline as records; each `(kind, name)` key is reported at most once per cycle plus one final growth report.

```go
type DiagnosticKind string

const (
	DiagInstrumentRejected DiagnosticKind = "instrument_rejected"
	DiagGaugeUnsupported   DiagnosticKind = "gauge_unsupported"
	DiagAdmissionRejected  DiagnosticKind = "admission_rejected"
	DiagDeliveryDropped    DiagnosticKind = "delivery_dropped"
	DiagDeliveryAmbiguous  DiagnosticKind = "delivery_ambiguous"
)

type ExportDiagnostic struct {
	Kind    DiagnosticKind
	Name    string
	Err     error
	Dropped int64
}

type Diagnostics interface {
	Report(ctx context.Context, d ExportDiagnostic)
}

func BoundedDiagnostics(inner Diagnostics) Diagnostics

var (
	ErrExportUnsupportedSignal = errors.New("gohan: destination does not support this signal")
	ErrIncompatibleReplay      = errors.New("gohan: destination replay semantics below the requirement")
	ErrUnknownDestination      = errors.New("gohan: export destination not configured")
	ErrExportQueueFull         = errors.New("gohan: export queue is full")
	ErrInstrumentLimit         = errors.New("gohan: instrument limit reached")
	ErrInstrumentConflict      = errors.New("gohan: conflicting instrument declarations")
	ErrDimensionLimit          = errors.New("gohan: dimension value limit reached")
	ErrSeriesLimit             = errors.New("gohan: series limit reached")
	ErrExportShutdown          = errors.New("gohan: export pipeline is shut down")
)

type ExportError struct {
	Destination string
	Err         error
}

func (e *ExportError) Error() string
func (e *ExportError) Unwrap() error
```

`std/telemetry` ships the delivery engine: one bounded queue and one worker per destination, configuration validated before admission (unsupported signals, replay below the requirement, duplicate destination names), delivery attempts bounded by the caller's context at flush and shutdown:

```go
type Pipeline struct{ /* unexported */ }

type ExportOption func(*exportConfig)

func NewPipeline(dests []ExportDestination, opts ...ExportOption) (*Pipeline, error)

func (p *Pipeline) Ingest(ctx context.Context, dest string, recs []ExportRecord) error
func (p *Pipeline) Flush(ctx context.Context) (UnresolvedWork, error)
func (p *Pipeline) Shutdown(ctx context.Context) (UnresolvedWork, error)

func WithExportSignals(sigs ...ExportSignalKind) ExportOption
func WithExportBounds(l ExportLimits) ExportOption
func WithExportDiagnostics(d Diagnostics) ExportOption
func WithRequiredReplay(r ReplaySemantics) ExportOption
func WithRedactor(r Redactor, p RedactionPolicy) ExportOption
func WithContentCapture(c ContentMapping) ExportOption
func DirectDelivery() ExportOption
func WithDiagnostics(d Diagnostics) Option
```

The `Metrics` forwarder forwards gauge observations when the wrapped sink implements `GaugeTelemetry`; without that support it drops the observation and reports `DiagGaugeUnsupported` through the configured diagnostics (default: bounded reports over the run logger at warn level, never error). The `Decorate` wrapper applies the convention and forwards when its inner sink implements `GaugeTelemetry`; without that support it drops the observation silently, and the diagnostic belongs to the layer holding the diagnostics.

`adapter/otel` translates completed SDK signals once per destination: completed spans fan out to every span-capable destination through one span exporter; each metric-capable destination gets its own reader honoring the destination's declared temporality; declared instruments apply unit, description and histogram boundaries at instrument creation (boundaries must be strictly increasing; conflicting duplicate descriptors fail construction; the SDK default aggregation applies otherwise):

```go
func WithDiagnostics(d Diagnostics) Option
func WithExportBounds(b ExportLimits) Option
func WithSignals(sigs ...ExportSignalKind) Option
func WithInstruments(insts ...Instrument) Option
func WithResourceName(name string) Option

type ExportPipeline struct{ /* unexported */ }

func NewExportPipeline(sink RecordSink, dests []ExportDestination, opts ...Option) (*ExportPipeline, error)

func (p *ExportPipeline) StartSpan(ctx context.Context, name string, attrs ...Attr) (context.Context, func(...Attr))
func (p *ExportPipeline) Count(ctx context.Context, name string, n int64, attrs ...Attr)
func (p *ExportPipeline) Record(ctx context.Context, name string, v float64, attrs ...Attr)
func (p *ExportPipeline) Gauge(ctx context.Context, name string, v float64, attrs ...Attr)
func (p *ExportPipeline) Flush(ctx context.Context) (UnresolvedWork, error)
func (p *ExportPipeline) Shutdown(ctx context.Context) (UnresolvedWork, error)
```

Instrument creation failures on any path report `DiagInstrumentRejected` instead of dropping silently; the distinct-name count is bounded by `ExportLimits.MaxInstruments`.

### 6.16b Durable local telemetry

Export (6.16a) gains an opt-in durable local spool: `std/telemetry`, standard library only, Linux local filesystems only. The spool owns delivery itself — it never wraps `Pipeline`, and `NewPipeline` behavior for non-WAL users never changes. `adapter/otel` drives the spool through `SpoolSink`, the void instrumentation bridge, which promises bounded admission only; critical callers use the explicit durable append and wait for sync. Records persist in the 6.16a wire form with their stable IDs; delivery is at-least-once, so a destination may see duplicates after a crash, never silent loss.

One process owns each spool. `OpenSpool` creates the directory with mode `0o700` and every file with mode `0o600`, takes an exclusive OS lock (flock) on a lock file for the spool's lifetime, and fails with `ErrSpoolLocked` when another writer holds it; `Shutdown` releases the lock and is idempotent. Every limit is required at construction: a zero or negative `ByteQuota`, `SegmentBytes`, `MaxPendingAge`, `RetryPolicy` field, `Workers` or `RequestDeadline` fails with `ErrSpoolConfig` naming the field — there are no hidden defaults for the six required limits. Destination names must be unique and non-nil at open; per-record admission mirrors 6.16a (content capture off by default through `ContentNone`, an optional `Redactor` round-tripping surviving string values, forbidden identity dimensions dropped under both spellings, record validation) and runs before any byte is queued; a record whose signal the destination does not support refuses with `ErrExportUnsupportedSignal`, an unconfigured destination with `ErrUnknownDestination`. Admission may redact or drop attributes in place through the caller's record pointers, so a caller retaining records must copy first.

Acceptance has two explicit levels. `Append` is the durable append: it returns `nil` only after the record bytes and the segment creation and rotation metadata (file data fsync, then directory fsync when a segment is created or rolled) are synced; concurrent appends share one fsync to amortize cost, and no caller is acknowledged before that sync completes. `Admit` is queue acceptance: bounded by the admission depth (`DefaultSpoolAdmissionDepth`, adjustable), refused with `ErrExportQueueFull` when full, never a durability promise — only synced records replay after a restart. After `Shutdown` both refuse with `ErrExportShutdown`. Capacity refusal is atomic and never evicts: appending past `ByteQuota` fails with `ErrSpoolQuota`, a full disk fails with `ErrSpoolDiskFull` wrapping the write error, and in both cases the whole call refuses, no record is persisted partially, and pending records are never deleted to make room.

Storage is one ordered stream per destination, split into segments that roll at `SegmentBytes` (a record larger than the threshold occupies its own segment). Each record is length-framed, little-endian: a header of `u32` version (`CurrentSpoolRecordVersion`), `u64` sequence, `u32` body length and `u32` CRC-32C over the body, followed by the body in the 6.16a wire form. Sequence numbers are minted at append, survive compaction unchanged and are never reused. Recovery at open verifies every record. An incomplete final record — header or body cut off by end of file — is repaired: the stream truncates to the last complete boundary and fsyncs, preceding records are preserved, and the repair is reported as `DiagSpoolRepaired` and in health. Any interior checksum or framing failure, and any complete record whose envelope version or body `schema_version` is unsupported, fails the open with a `*CorruptionError` carrying kind, path and offset, reported as `DiagSpoolCorrupt` — corruption is never silently skipped.

Replay and delivery: a destination's pending records are exactly the sequence range above its durable cursor. At most `Workers` delivery workers run concurrently, one owning each destination's stream; batches carry at most `DefaultSpoolBatchRecords` records in strict sequence order, and every `Export` attempt runs under a context bounded by `RequestDeadline`. Outcomes follow the 6.16a state machine with two durable-mode amendments: `ambiguous` retains the record unresolved — a later attempt may duplicate it, which the at-least-once contract permits — and `permanent` parks the stream at that record instead of dropping it. `retryable`, an absent outcome row and a cancelled attempt retain the record with its ID unchanged. The cursor advances only past acknowledged or explicitly discarded records; remote acknowledgements persist (cursor fsync) before any acknowledged prefix is reclaimed, so a crash between remote acceptance and cursor persistence replays the record — duplicates, never loss. `Retry` bounds attempts per record within a spool session (exponential backoff from `Backoff` to `MaxBackoff`; exhausted attempts park the record); `MaxPendingAge` is the durable backstop: a pending record older than the limit expires, delivery for its destination stops at that record, and only explicit discard resumes the stream.

Two decisions stay with the owner. `Discard` removes named pending records from a destination stream through a synced compaction, returns the count removed and unblocks the stream — the required disposition for parked and expired records. `RemoveDestination` refuses to guess: `BacklogDiscard` compacts the stream away, clears its cursor and removes retired files before returning; `BacklogRetain` keeps the records durable and quota-counted, undelivered, and the stream resumes if the same name is registered again.

Persisted content obeys erasure. Credentials never persist: redaction precedes append and content capture is off by default; cursor, lock and health state carry no record content. `EraseSubjects` runs crash-safely: it fsyncs a tombstone of the erasure rule first; from that moment every read path blocks matching records (string equality on `Attr` across resource attrs, span attrs and metric dimensions); each stream compacts without the matching records while surviving records keep their sequence numbers and order; cursor state compacts with them; retired files are unlinked and the directory fsynced before the call reports completion; only then is the tombstone cleared. An erasure interrupted at any point completes at the next open before the spool serves, and removed subject content never replays — not even between crash and completed compaction, because the tombstone filters first. Erasure does not promise physical media sanitization; deployments combine it with filesystem encryption.

Health is independent of the record path: `Health` takes a read-only local snapshot, never enqueues records and never routes failures back through the WAL. It reports pending bytes, admitted-but-unsynced count, oldest pending age, per-destination cursor, pending count, lag (age of the oldest undelivered record), retries, parked IDs, expired count, cumulative rejected appends and corruption repairs. `Flush` distinguishes locally synced from remotely delivered work: it syncs every admitted record, waits for one bounded delivery pass within `ctx`, and returns how many records became durable (`Synced`), were acknowledged remotely per destination (`Delivered`) and remain durable but undelivered (`Undelivered`). `Shutdown` performs one final bounded drain, persists cursors and state, releases the lock and leaves every undelivered record durable for the next start; draining or retaining is the rollback path and a spool is never deleted as rollback.

```go
const CurrentSpoolRecordVersion = 1

const (
	DefaultSpoolAdmissionDepth = 2048
	DefaultSpoolBatchRecords   = 128
)

type RetryPolicy struct {
	MaxAttempts int           // attempts per record per spool session; exhaustion parks the record
	Backoff     time.Duration // base delay between attempts
	MaxBackoff  time.Duration // exponential backoff ceiling
}

type SpoolConfig struct {
	ByteQuota       int64         // durable pending bytes; refusal beyond it never evicts records
	SegmentBytes    int64         // segment rollover threshold
	MaxPendingAge   time.Duration // retention limit; expired pending parks delivery until discard
	Retry           RetryPolicy   // delivery retry bounds
	Workers         int           // concurrent delivery worker cap
	RequestDeadline time.Duration // per Export attempt deadline
}

type SpoolOption func(*spoolConfig)

func WithSpoolDiagnostics(d Diagnostics) SpoolOption
func WithSpoolRedactor(r Redactor, p RedactionPolicy) SpoolOption
func WithSpoolContentCapture(c ContentMapping) SpoolOption
func WithSpoolAdmissionDepth(n int) SpoolOption

// OpenSpool opens (or creates) the spool at dir, owned exclusively by this
// process for its lifetime, recovers streams and cursors, completes any
// interrupted erasure, and validates cfg and dests before serving.
func OpenSpool(dir string, dests []ExportDestination, cfg SpoolConfig, opts ...SpoolOption) (*Spool, error)

type Spool struct{ /* unexported */ }

// Append is the explicit durable append: it returns nil only after the
// record data and the segment creation and rotation metadata are synced.
func (s *Spool) Append(ctx context.Context, dest string, recs []ExportRecord) error

// Admit is bounded queue acceptance: no sync promise, refused with
// ErrExportQueueFull past the admission depth. Only synced records replay.
func (s *Spool) Admit(ctx context.Context, dest string, recs []ExportRecord) error

// Flush syncs admitted records, waits for one bounded delivery pass within
// ctx and reports synced, delivered and undelivered work separately.
func (s *Spool) Flush(ctx context.Context) (SpoolFlush, error)

// Shutdown runs one final bounded drain, persists cursors and state,
// releases the exclusive lock and is idempotent. Undelivered records stay
// durable for the next open.
func (s *Spool) Shutdown(ctx context.Context) (UnresolvedWork, error)

// Health snapshots the spool locally; it never enqueues records.
func (s *Spool) Health() SpoolHealth

// Discard removes named pending records from dest through a synced
// compaction and returns how many were removed. It is the required
// disposition for parked and expired records.
func (s *Spool) Discard(ctx context.Context, dest string, ids ...RecordID) (int64, error)

// RemoveDestination deregisters dest after an explicit disposition of its
// backlog.
func (s *Spool) RemoveDestination(ctx context.Context, dest string, disposition BacklogDisposition) error

// EraseSubjects removes every record matching the erasure rule from all
// streams, crash-safely, and never lets matching content replay again.
func (s *Spool) EraseSubjects(ctx context.Context, subjects SubjectErasure) (ErasureResult, error)

// SpoolSink adapts a Spool to RecordSink for adapter/otel: Ingest admits
// with bounded queue acceptance, Flush syncs and drains, Shutdown shuts
// down. The bridge promises bounded admission only.
func SpoolSink(s *Spool) RecordSink

type SpoolFlush struct {
	Synced      int64            `json:"synced"`              // records made durable by this flush
	Delivered   map[string]int64 `json:"delivered,omitempty"` // records acknowledged remotely per destination
	Undelivered UnresolvedWork   `json:"undelivered"`         // durable records awaiting remote acknowledgement
}

type DestinationCursor struct {
	Name  string `json:"name"`
	Acked int64  `json:"acked"` // highest sequence acknowledged or discarded
}

type DestinationHealth struct {
	Name    string            `json:"name"`
	Cursor  DestinationCursor `json:"cursor"`
	Pending int64             `json:"pending"` // undelivered records
	Lag     time.Duration     `json:"lag"`     // age of the oldest undelivered record
	Retries int64             `json:"retries"` // cumulative retryable outcomes since open
	Parked  []RecordID        `json:"parked,omitempty"` // permanent rejections and exhausted retries
	Expired int64             `json:"expired"` // pending records past the retention limit
}

type CorruptionReport struct {
	RepairedTails int64 `json:"repaired_tails"` // incomplete final records truncated at recovery
	RepairedBytes int64 `json:"repaired_bytes"`
}

type SpoolHealth struct {
	PendingBytes     int64               `json:"pending_bytes"`
	AdmissionQueued  int64               `json:"admission_queued"` // accepted, not yet synced
	OldestPendingAge time.Duration       `json:"oldest_pending_age"`
	RejectedAppends  int64               `json:"rejected_appends"` // cumulative refused Append and Admit calls
	Corruption       CorruptionReport    `json:"corruption"`
	Destinations     []DestinationHealth `json:"destinations"`
}

type BacklogDisposition string

const (
	BacklogDiscard BacklogDisposition = "discard" // compact the stream away and remove retired files
	BacklogRetain  BacklogDisposition = "retain"  // keep records durable and quota-counted; resume on re-registration
)

type SubjectErasure struct {
	Attr   string   `json:"attr"`   // attribute key identifying the subject
	Values []string `json:"values"` // matching string values are erased
}

type ErasureResult struct {
	Records int64 `json:"records"`
	Bytes   int64 `json:"bytes"`
}

type CorruptionKind string

const (
	CorruptionTail     CorruptionKind = "incomplete_tail" // repairable torn final record
	CorruptionChecksum CorruptionKind = "checksum"        // interior checksum mismatch
	CorruptionFraming  CorruptionKind = "framing"         // interior length or header violation
	CorruptionVersion  CorruptionKind = "version"         // unsupported envelope version or schema_version
)

type CorruptionError struct {
	Kind   CorruptionKind
	Path   string
	Offset int64
	Err    error
}

func (e *CorruptionError) Error() string
func (e *CorruptionError) Unwrap() error

// Declared on the core DiagnosticKind type; core/types itself is unchanged.
const (
	DiagSpoolCorrupt  DiagnosticKind = "spool_corrupt"  // rejected corruption with a *CorruptionError
	DiagSpoolRepaired DiagnosticKind = "spool_repaired" // tail repair; Dropped carries lost bytes
)

var (
	ErrSpoolLocked   = errors.New("gohan: spool is owned by another writer")
	ErrSpoolConfig   = errors.New("gohan: spool configuration is invalid")
	ErrSpoolQuota    = errors.New("gohan: spool byte quota exceeded")
	ErrSpoolDiskFull = errors.New("gohan: spool device is out of space")
)
```

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

`Stack.Feedback` MUST record each signal through `stores.FeedbackStore`, append the `feedback` audit record and the `FeedbackRecorded` event, and emit `gohan.feedback`. The metric MUST be registered with the labels `name` and `flow` only; the metric layer stamps `release` and `variant`, so the observed series is exactly `gohan.feedback{name, flow, release, variant}`. A numeric `Value` MUST record a histogram and any other `Value` MUST count. No label MAY carry the subject, session, run, target, comment or correction: the allow-list admits none of them and forbids the identity names. The cardinality is bounded by the declared `name` set (the tool-name grammar `^[a-z][a-z0-9_]{0,63}$`), the registered flow names, the release and the variant — a fixed four-label product with no unbounded dimension.

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

### Requirement: Export acceptance is explicit
Exporters MUST distinguish accepted, retryable, permanent and ambiguous results per record. Cancellation MUST leave unacknowledged records unresolved.

#### Scenario: Partial export acknowledgement
ID: `telemetry.export-acceptance`
- **WHEN** a batch is partially accepted before its response is interrupted
- **THEN** only acknowledged records advance and unresolved records retain their IDs

### Requirement: Telemetry lifecycle has an owner
Pipelines MUST distinguish borrowed and owned resources. Flush and shutdown MUST honor context deadlines and report unresolved work.

#### Scenario: Bounded pipeline closure
ID: `telemetry.export-lifecycle`
- **WHEN** pipeline closure reaches its deadline with a blocked exporter and borrowed providers
- **THEN** it reports unresolved work without closing borrowed providers or leaking workers

### Requirement: Export preserves signal semantics
Export MUST preserve ancestry, timestamps, schema identity, metric kind, unit and temporality. Unsupported signals MUST fail configuration.

#### Scenario: Equivalent destinations
ID: `telemetry.export-signal-fidelity`
- **WHEN** completed spans and gauge, counter and histogram observations reach two supported destinations
- **THEN** original ancestry and timestamps match and instrument semantics remain equivalent

### Requirement: Export admission is bounded and private
Pipelines MUST bound queues, instruments, dimension values and total series. Redaction MUST precede serialization. Diagnostics MUST avoid recursive export.

#### Scenario: Admission rejects unsafe growth
ID: `telemetry.export-bounds-privacy`
- **WHEN** producers submit secret-bearing content, unbounded dimensions and a failing instrument registration
- **THEN** forbidden content reaches no serialized output and bounded independent diagnostics report rejection

### Requirement: Durable local acceptance survives restart
Durable append MUST acknowledge only synced records. Restart MUST replay these records with their original IDs until remote acknowledgement.

#### Scenario: Crash after local acceptance
ID: `telemetry.wal-durable-restart`
- **WHEN** the process is killed after synced acknowledgement and before remote delivery
- **THEN** restart exports every locally acknowledged pending record with unchanged IDs

### Requirement: WAL corruption is explicit
Recovery MUST reject interior corruption and unknown versions. Recovery MUST preserve preceding records when repairing an incomplete final record.

#### Scenario: Corrupt spool recovery
ID: `telemetry.wal-corruption`
- **WHEN** a spool has an incomplete tail, interior checksum failure or unsupported version
- **THEN** only the incomplete tail is repaired and other corruption fails with actionable diagnostics

### Requirement: Destinations retain independent progress
Each destination MUST persist its acknowledgement progress. Ambiguous delivery MUST retain unresolved records and permit duplicate replay.

#### Scenario: Restart with divergent destinations
ID: `telemetry.wal-destination-cursors`
- **WHEN** one destination acknowledges and another fails before a restart
- **THEN** acknowledged progress survives and unresolved records replay with stable IDs

### Requirement: Spool limits preserve durable records
The spool MUST reject concurrent writers and appends beyond capacity. It MUST NOT automatically evict unacknowledged records.

#### Scenario: Ownership and disk failure
ID: `telemetry.wal-capacity-ownership`
- **WHEN** a second writer opens the spool and its owner encounters quota or disk exhaustion
- **THEN** the second writer fails and existing durable records remain recoverable

### Requirement: Persisted content obeys erasure
Subject erasure MUST remove matching captured content before reporting completion and MUST prevent its subsequent replay.

#### Scenario: Erasure survives interruption
ID: `telemetry.wal-content-erasure`
- **WHEN** erasure is interrupted during segment replacement and retried after restart
- **THEN** recovery completes erasure without replaying removed subject content
