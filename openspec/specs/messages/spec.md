# Messages and blocks

Capability: `messages` · Spec v1.3 (ADR-0127) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `messages` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.


## Contract

### 6.1 Messages (ordered blocks)

```go
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type Message struct {
	ID     string
	Role   Role
	Blocks []Block
	Meta   map[string]any
}

type OriginKind int

const (
	OriginSystem OriginKind = iota
	OriginUser
	OriginModel
	OriginTool
	OriginProvider
	OriginOperator
)

type Origin struct {
	Kind OriginKind
	Name string
}

type Block interface {
	isBlock()
	BlockOrigin() Origin
}

// BlockBase is embedded by every block; Origin and Seq live here.
type BlockBase struct {
	Origin Origin
	Seq    int64
}

func (b BlockBase) BlockOrigin() Origin { return b.Origin }

type Text struct {
	BlockBase
	Text string
}
type Reasoning struct {
	BlockBase
	Text      string
	Signature []byte
	Provider  string
}
type Blob struct {
	Ref    string
	SHA256 string
	Bytes  int64
}
type Image struct {
	BlockBase
	MIME string
	Data []byte
	URL  string
	Blob Blob
}
type Audio struct {
	BlockBase
	MIME string
	Data []byte
	URL  string
	Blob Blob
}
type File struct {
	BlockBase
	MIME string
	Name string
	Data []byte
	URL  string
	Blob Blob
}

var ErrBlobTooLarge = errors.New("gohan: block exceeds the profile's blob limit")
type Document struct {
	BlockBase
	Content []Block
	Source  string
	Meta    map[string]string
}
type ToolUse struct {
	BlockBase
	ID   string
	Name string
	Args json.RawMessage
}
type ToolResult struct {
	BlockBase
	ID      string
	Content []Block
	Outcome Outcome
	Error   *ToolError
	Ref     string
}
type CacheBreak struct{ BlockBase }
type Raw struct {
	BlockBase
	Provider string
	Value    any
}
type Compaction struct {
	BlockBase
	CoversUpTo int64
	Kind       CompactionKind
	Summary    []Block
	Opaque     Raw
	Model      string
	Tokens     int
}

type Outcome int

const (
	Succeeded Outcome = iota
	Failed
	Unknown
)

type ErrorKind int

const (
	Permanent ErrorKind = iota
	Retryable
	OutcomeUnknown
)

type ToolError struct {
	Kind    ErrorKind
	Message string
}

type ToolArgsError struct {
	Reason  string
	Message string
}

func ValidateToolArgs(args json.RawMessage) error
func ArgsErrorResult(err error) ToolResult

type ErrorCode string

type Problem struct {
	Code       ErrorCode
	Kind       ErrorKind
	Status     int
	RetryAfter time.Duration
	Title      string
	Detail     string
	Instance   string
	Fields     map[string]string
}

func ProblemOf(err error) Problem

type BlockKind string

const (
	KindText       BlockKind = "text"
	KindReasoning  BlockKind = "reasoning"
	KindImage      BlockKind = "image"
	KindAudio      BlockKind = "audio"
	KindFile       BlockKind = "file"
	KindDocument   BlockKind = "document"
	KindToolUse    BlockKind = "tool_use"
	KindToolResult BlockKind = "tool_result"
	KindCacheBreak BlockKind = "cache_break"
	KindRaw        BlockKind = "raw"
	KindCompaction BlockKind = "compaction"
)

type Fidelity int

const (
	Preserved Fidelity = iota
	Degraded
	Dropped
)
```

Rules:

- A message is an **ordered** sequence of blocks; adapters preserve order in both directions. There is no tool-call side field and no tool role: an assistant turn that calls tools is `[Reasoning?, Text?, ToolUse, ToolUse…]`; results come back as a `RoleUser` message of `ToolResult` blocks, the shape every current provider accepts.
- `Reasoning` is opaque: it round-trips only to the provider that produced it (`Provider` + `Signature`) and is `Dropped` for any other. The assembler never edits it.
- `Origin` is assigned by the chain, never by callers: user input → `OriginUser`, tool results → `OriginTool{Name}`, provider output → `OriginProvider{Name}`, model output → `OriginModel`, instructions → `OriginSystem`, a human operator's reply during takeover → `OriginOperator{Subject}` (`flow` *Takeover*), which is trusted for taint like `OriginUser` but never counted as model-authored by audit, evals or feedback. Converters preserve it via `Message.Meta` where the foreign type has no slot. Guards and the assembler read it through `BlockOrigin()`.
- `ValidateToolArgs` rejects arguments that are not a syntactically valid JSON object with unique keys and valid UTF-8; `ToolArgsError.Reason` is `duplicate_key`, `invalid_utf8` or `syntax`, and `ArgsErrorResult` renders any such error as a `Failed`/`Permanent` `ToolResult` the model reads.
- `Outcome == Unknown` means the side effect may or may not have happened; the model sees a structured "outcome unknown" result and, if the tool declares `ReadBack`, an instruction to verify. `Ref` points into the output store when content was truncated (§6.15b).
- `Document` carries retrieved chunks with metadata into guards, spans and evals. `CacheBreak` marks provider cache breakpoints. `Raw` is the escape hatch for provider blocks gohan does not model (server-side tools, citations, …); it round-trips through its own adapter only. `Compaction` replaces every history message with version ≤ `CoversUpTo` at assembly time; it is persisted in `SessionLog` and owned by the `context` capability.
- **Blobs live once.** An `Image`, `Audio` or `File` whose `Data` exceeds `InlineBlobBytes` (harness constant, 64 KiB; spill vs reject order in *Limits* below) is written to `OutputStore` when it enters the harness (`Send`, tool result, provider result); the persisted block carries `Blob{Ref, SHA256, Bytes}` and `Data` is nil in `SessionLog`. Storage is content-addressed: equal bytes yield one `Ref`. `DeleteSession` and `EraseSubject` cascade to the refs the session or subject owns. Assembly loads bytes from the store only when the adapter has no provider handle: an adapter implementing the optional `BlobUploader` uploads a blob once per profile, records `ref → provider id` in session metadata, and sends the id on later turns (`messages.blob-provider-id-reused`); otherwise base64 from the store. Visual cost goes through `TokenEstimator` using `Caps.Blobs`.
- **URLs are never forwarded on the model's behalf.** A `URL` in a block whose origin is `OriginUser` or `OriginSystem` may be passed to a provider that fetches URLs. A `URL` in a block with any other origin is fetched by the harness with `std/egress.Client` under the flow's `EgressPolicy` (`tools` *Egress*) and stored as a blob; it is never sent to the provider as a URL.
- **Limits.** `Caps.Blobs{MaxBytes, MaxPerRequest, MaxPixels, Formats}` per profile; the harness constant `InlineBlobBytes` (64 KiB) is the spill threshold and `MaxBytes` the hard per-block cap, so a block between the two is stored by ref and only a block over `MaxBytes` is rejected; `Send` and tool results reject a block over `MaxBytes` or outside `Formats` with `ErrBlobTooLarge` (`Permanent`); `Build` fails when a flow's declared block kinds exceed the profile; `RunLimits.MaxBlobBytes` caps a session's total (default 256 MiB, `*LimitExceededError`).
- Every adapter declares a **fidelity matrix**: for each block type, `Preserved`, `Degraded` (e.g. `File` sent as extracted text) or `Dropped`. `Explain` prints it per profile; the round-trip suite asserts it. `Build` fails when a flow can emit a block its model would `Drop` unless the flow opts in (`AllowDrop(File)`). The opt-in is the `std/flow` option `AllowDrop(kinds ...BlockKind)`, resolved like every other flow option (`build` §Strategies); the gate is scenario `messages.fidelity-gate-at-build`.
- `ModelChunk` streams typed deltas (`DeltaText`, `DeltaReasoning`, `DeltaToolArgs` as preview only — `streams`); `ToolUse` blocks are emitted complete. A `ToolUse` produced under `Finish == max_tokens` is marked truncated and is never executed: the model receives a `Failed(Permanent)` result naming truncation and the turn is retried once with a larger `MaxTokens` (metric `gohan.tool.truncated_args`). Schema-valid free-text fields carrying refusal signatures are classified `ModelError{Class: ContentPolicy}` (`gohan.model.refusal_as_json`).


## Requirements

### Requirement: Error catalog

`ProblemOf` is the only place a Go error becomes client-facing. The catalog is closed and versioned with the spec; every sentinel and typed error a spec defines has a row, and `spec:types` fails when one does not — except the two that never reach a transport: `*SuspendError`, which is the suspension signal, and `ToolError`, which is tool-result data the model reads. `Title` is the code's fixed English title; `Detail` is rendered from a fixed template per code with allow-listed `Fields` (`tool`, `limit`, `retry_after`, `run_id`, `stage`, `reason`, `key_id`) and never contains provider bodies, prompts, arguments, tenant or subject ids; `Instance` is the run id. Unknown errors become `gohan.internal` with `Detail: "internal error"` and the cause logged with the run id. Transports: HTTP responds `application/problem+json` with `type: https://gohan.dev/problems/<code>`, `status`, and `Retry-After` when `RetryAfter > 0`; a stream that has already started sends one terminal `error` event `{code, kind, retry_after, instance}` and closes, and a close without `done` or `error` is `gohan.stream_interrupted` (client-side, reattach by `Seq`); AG-UI sends `RUN_ERROR{code, message: Title}`; the MCP server sets JSON-RPC `error.data.code`. `Explain` prints the catalog.

| Code | Kind | HTTP | Source |
|---|---|---|---|
| `gohan.no_principal` | Permanent | 401 | `ErrNoPrincipal` |
| `gohan.session_forbidden` | Permanent | 403 | `ErrSessionForbidden` |
| `gohan.approver_not_eligible` | Permanent | 403 | `ErrApproverNotEligible` |
| `gohan.run_active` | Retryable (`RetryAfter` = lease remaining) | 409 | `ErrRunActive` |
| `gohan.run_not_active` | Permanent | 409 | `ErrRunNotActive` |
| `gohan.session_held` | Permanent | 409 | `ErrSessionHeld` |
| `gohan.mailbox_full` | Retryable (`RetryAfter` 1 s) | 429 | `ErrMailboxFull` |
| `gohan.resume_inside_run` | Permanent | 409 | `ErrResumeInsideRun` |
| `gohan.operation_exists` | Permanent (carries `run_id`) | 409 | `ErrOperationExists` |
| `gohan.token_consumed` | Permanent | 410 | `ErrTokenConsumed` |
| `gohan.token_expired` | Permanent | 410 | `ErrTokenExpired` |
| `gohan.token_mismatch` | Permanent | 422 | `ErrTokenMismatch` |
| `gohan.input_invalid` | Permanent | 422 | `ErrInputInvalid`, `ErrNotSuspendable` |
| `gohan.empty_history` | Permanent | 422 | `ErrEmptyHistory` |
| `gohan.session_handed_off` | Permanent | 409 | `ErrSessionHandedOff` |
| `gohan.blob_too_large` | Permanent | 413 | `ErrBlobTooLarge` |
| `gohan.limit_exceeded` | Permanent (carries `limit`) | 429 for quota pools, else 422 | `*LimitExceededError` |
| `gohan.shutting_down` | Retryable (`RetryAfter` 1 s) | 503 | `ErrShuttingDown` |
| `gohan.checkpoint_incompatible` | Permanent | 409 | `ErrCheckpointIncompatible` |
| `gohan.tool_denied` | Permanent (carries `tool`) | 403 | gate `DenyVerdict`, `TaintDenied`, `EgressDenied`, `ShadowSuppressed` |
| `gohan.guard_blocked` | Permanent (carries `stage`) | 422 | `GuardBlockedError` |
| `gohan.provider_key_missing` | Permanent | 422 | `ErrNoProviderKey` |
| `gohan.provider_key_rejected` | Permanent (carries `key_id`) | 422 | `ModelError{ClassAuth}` on a tenant key |
| `gohan.model_rate_limited` | Retryable (`RetryAfter` from provider) | 429 | `ModelError{ClassRateLimited}` after the chain gave up |
| `gohan.model_unavailable` | Retryable | 503 | `ModelError{ClassTransient}`, breaker open |
| `gohan.model_version_drift` | Permanent | 409 | `ModelError{ClassVersionDrift}` after every endpoint drifted |
| `gohan.context_overflow` | Permanent | 422 | `ModelError{ClassContextOverflow}` after the backstop |
| `gohan.content_policy` | Permanent | 422 | `ModelError{ClassContentPolicy}` |
| `gohan.aborted` | Permanent (carries `reason`) | 422 | `*AbortError` |
| `gohan.stream_interrupted` | Retryable | — (client-side) | stream closed without `done` or `error` |
| `gohan.configuration` | Permanent | 500 | build- and startup-time errors that reach a request only through misconfiguration: `ErrToolName`, `ErrToolCollision`, `ErrToolDescription`, `ErrManifestDrift`, `ErrToolSetDrift`, `ErrMemoryStoreRequired`, `ErrNoTokenEstimator`, `ErrSessionIndexRequired`, `ErrSchemaTooOld`, `ErrPartitionMissing`, `ErrShutdownIncomplete`, `ErrEgressPolicyRequired` |
| `gohan.version_conflict` | Permanent | 409 | `ErrVersionConflict` |
| `gohan.structured_output` | Permanent | 422 | `ErrStructuredOutput` |
| `gohan.chain_step` | Permanent | 500 | `StepError` |
| `gohan.subflow_partial` | Permanent | 422 | `PartialError` |
| `gohan.outcome_unknown` | Retryable | 409 | `UncertainOutcomeError` |
| `gohan.internal` | Permanent | 500 | any other error, including `ErrSignalsPending` escaping the runtime (a bug) |

#### Scenario: every sentinel has a code
ID: `errors.every-sentinel-has-code`
- WHEN `spec:types` runs
- THEN every sentinel and typed error declared in a spec appears in the catalog, and a missing row fails the check

#### Scenario: unknown error is internal
ID: `errors.problem-of-unknown-is-internal`
- WHEN `ProblemOf` receives an error no catalog row matches
- THEN the problem is `gohan.internal`, status 500, `Detail` is `internal error` and the cause is logged with the run id

#### Scenario: detail never carries provider body
ID: `errors.detail-never-carries-provider-body`
- WHEN a provider returns a 500 with a body naming the request payload
- THEN the client sees `gohan.model_unavailable` whose `Detail` contains none of the provider body

#### Scenario: retry-after on retryable
ID: `errors.retry-after-on-retryable`
- WHEN `Send` fails with `ErrRunActive` over HTTP
- THEN the response is 409 `application/problem+json` with `Retry-After` set to the lease's remaining seconds

### Requirement: Blobs

#### Scenario: blob stored by reference
ID: `messages.blob-stored-by-ref`
- WHEN `Send` carries a 3 MB `Image`
- THEN `SessionLog` holds the block with `Blob{Ref, SHA256, Bytes}` and nil `Data`, and `OutputStore.Get(Ref)` returns the bytes

#### Scenario: blob content-addressed
ID: `messages.blob-content-addressed`
- WHEN the same image is sent in two messages
- THEN both blocks carry the same `Ref` and the store holds one copy

#### Scenario: provider id reused
ID: `messages.blob-provider-id-reused`
- WHEN the adapter implements `BlobUploader` and a conversation runs three turns after an image was attached
- THEN the image bytes are uploaded once and turns two and three send the provider id

#### Scenario: url only from user
ID: `messages.url-only-from-user`
- WHEN a tool result contains an `Image` with a `URL`
- THEN the harness fetches it under the egress policy and stores a blob, and the request to the provider carries no URL

#### Scenario: blob too large
ID: `messages.blob-too-large`
- WHEN `Send` carries a `File` over `Caps.Blobs.MaxBytes`
- THEN `ErrBlobTooLarge` is returned before any store write and no run starts

### Requirement: Block model and fidelity

#### Scenario: order preserved
ID: `messages.order-preserved`
- WHEN an assistant message `[Reasoning, Text, ToolUse, ToolUse]` is stored, resumed and re-sent to the same provider
- THEN the provider receives the blocks in that order with the reasoning signature intact

#### Scenario: reasoning dropped across providers
ID: `messages.reasoning-dropped-across-providers`
- WHEN a history containing Anthropic `Reasoning` blocks is routed to a vLLM profile by fallback
- THEN the reasoning blocks are dropped, `gohan.block.dropped{kind=reasoning}` increments, and no request error occurs

#### Scenario: fidelity gate at build
ID: `messages.fidelity-gate-at-build`
- WHEN a flow's tools can return `File` blocks and its profile declares `File: Dropped`
- THEN `Build` fails unless the flow sets `AllowDrop(File)`

#### Scenario: typed deltas
ID: `messages.typed-deltas`
- WHEN a model streams thinking then text
- THEN `ModelChunk.Kind` is `DeltaReasoning` for the first chunks and `DeltaText` after; `Windowed` output guards only `DeltaText`

#### Scenario: duplicate keys in tool args
ID: `messages.duplicate-keys-in-tool-args`
- WHEN the model emits tool args with a duplicate JSON key
- THEN validation via `json/v2` rejects them as `Failed(Permanent)` and `gohan.tool.invalid_args{reason=duplicate_key}` increments

#### Scenario: deterministic tool filter on replay
ID: `messages.deterministic-tool-filter-on-replay`
- WHEN a run suspended with filtered tool set S and `Replay` recomputes the filter
- THEN the recomputed set equals S; a mismatch fails resume with `ErrToolSetDrift` before any model call
