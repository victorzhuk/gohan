# Store ports

Capability: `stores` · Spec v1.10 (ADR-0133) · Source of truth for this capability.

> **Contract tiers.** Code blocks in *Contract* are **normative** (ports, interfaces, error classes, stored shapes, event types, ordering rules) unless a block is marked `(illustrative)`, in which case names may change during M0 without a spec change. Prose rules in *Contract* are normative. Requirements are normative and each scenario ID is referenced by a test.

## Purpose

See `docs/overview.md` for how `stores` fits the architecture. Out of scope for this capability is anything owned by another capability spec; cross-references are by capability name.

## Contract

### 6.13 Stores

```go
type History struct {
	Owner      SessionOwner
	Messages   []Message
	Version    int64
	ForkedFrom *ForkPoint
}

type ForkPoint struct {
	SessionID string
	UpTo      string
}

type SessionLog interface {
	Load(ctx context.Context, sessionID string) (History, error)
	Append(ctx context.Context, sessionID string, expectedVersion int64, msgs ...Message) (int64, error)
	Purge(ctx context.Context, olderThan time.Time) (int, error)
	Delete(ctx context.Context, sessionID string) error
}

func (s Stores) DeleteSession(ctx context.Context, sessionID string) error

type RetentionPolicy struct {
	Conversation time.Duration
	Working      time.Duration
	Audit        time.Duration
}

type RetentionSource interface {
	Retention(ctx context.Context, tenant string) (RetentionPolicy, bool)
}

type MaintainReport struct {
	Purged map[string]int
	Held   int
}

func (s *Stack) Maintain(ctx context.Context) (MaintainReport, error)
func (s Stores) ForkSession(ctx context.Context, from string, upTo string) (string, error)

type Checkpoint struct {
	SessionID           string
	Flow                string
	Backend             string
	BackendVersion      string
	Reason              SuspendReason
	Originator          Principal
	Data                []byte
	Child               ResumeToken
	Workspace           WorkspaceRef
	WorkspaceGeneration int64
	ExpiresAt           time.Time
}

var (
	ErrSessionIndexRequired   = errors.New("gohan: session listing needs a SessionIndex")
	ErrSchemaTooOld           = errors.New("gohan: postgres schema older than this release supports")
	ErrPartitionMissing       = errors.New("gohan: no partition for this row's time")
	ErrRunActive              = errors.New("gohan: session has an active run lease")
	ErrRunNotActive           = errors.New("gohan: session has no active run")
	ErrSessionHeld            = errors.New("gohan: session is under legal hold")
	ErrMailboxFull            = errors.New("gohan: run mailbox is full")
	ErrSignalsPending         = errors.New("gohan: steer signals arrived after the last drain")
	ErrOperationExists        = errors.New("gohan: operation id already recorded")
	ErrCheckpointIncompatible = errors.New("gohan: checkpoint schema or backend version incompatible")
	ErrVersionConflict        = errors.New("gohan: append with a stale version")
)

type Checkpoints interface {
	Put(ctx context.Context, cp Checkpoint) (ResumeToken, error)
	Consume(ctx context.Context, t ResumeToken, in ResumeInput) (Checkpoint, error)
	PendingInput(ctx context.Context, runID string) (Checkpoint, ResumeInput, error)
}

// SessionRunFinder is the optional per-session active-run lookup on Runs.
type SessionRunFinder interface {
	RunForSession(ctx context.Context, sessionID string) (Run, error)
}

// Optional surfaces on Checkpoints, discovered by type assertion
// (docs/design/compatibility.md). Checkpoint carries the run it belongs to.
type CheckpointResumer interface {
	Peek(ctx context.Context, t ResumeToken) (Checkpoint, error)
	ConsumeIf(ctx context.Context, t ResumeToken, expected Checkpoint, in ResumeInput) (Checkpoint, error)
	UpdatePending(ctx context.Context, t ResumeToken, expected, next Checkpoint) error
}

type ResumeReadyLister interface {
	ResumeReady(ctx context.Context, limit int) ([]Checkpoint, error)
}

type CallKey struct {
	SessionID string
	CallID    string
}

type EntryState int

const (
	Reserved EntryState = iota
	Completed
)

type Fingerprint string

type Entry struct {
	State       EntryState
	Fingerprint Fingerprint
	Key         string
	Result      ToolResult
	At          time.Time
	Attempts    int
	Endpoint    string
}

type Journal interface {
	Reserve(ctx context.Context, k CallKey, fp Fingerprint) (Entry, bool, error)
	Complete(ctx context.Context, k CallKey, res ToolResult) error
	ByFingerprint(ctx context.Context, sessionID string, fp Fingerprint) ([]Entry, error)
}

type RunState int

const (
	Running RunState = iota
	Suspended
	Resuming
	Finished
	Failed
)

type Run struct {
	SessionID   string
	RunID       string
	Flow        string
	Backend     string
	OperationID string
	Mode        RunMode
	State       RunState
	Uncertain   []CallKey
	Turn        int
	Seq         int64
	Pending     []ToolUse
	Cost        float64
	ResultRef   string
	StartedAt   time.Time
	Heartbeat   time.Time
}

const (
	LeaseTTL       = 30 * time.Second
	HeartbeatEvery = 10 * time.Second
	ReaperEvery    = 60 * time.Second
)

type Lease struct {
	RunID      string
	Generation uint64
	Expires    time.Time
}

type Runs interface {
	Start(ctx context.Context, r Run, ttl time.Duration) (Lease, error)
	Heartbeat(ctx context.Context, l Lease) (Lease, error)
	Suspend(ctx context.Context, l Lease, t ResumeToken) error
	Resuming(ctx context.Context, runID string, ttl time.Duration) (Lease, error)
	Finish(ctx context.Context, l Lease, state RunState, uncertain []CallKey, resultRef string) error
	ByOperation(ctx context.Context, tenant, operationID string) (Run, error)
	Stale(ctx context.Context, staleAfter time.Duration, limit int) ([]Run, error)
	Reclaim(ctx context.Context, r Run, ttl time.Duration) (Lease, error)
	Signal(ctx context.Context, runID string, s Signal) error
	Drain(ctx context.Context, l Lease) ([]Signal, error)
	Notices(ctx context.Context, limit int) ([]RunNotice, error)
	AckNotice(ctx context.Context, id string) error
}

type SignalKind int

const (
	SignalCancel SignalKind = iota
	SignalSteer
)

type Signal struct {
	Kind    SignalKind
	Message Message
	At      time.Time
}

const MaxPendingSignals = 10

type SessionKind int

const (
	SessionPrimary SessionKind = iota
	SessionFork
	SessionChild
	SessionShadow
)

type SessionMeta struct {
	ID           string
	Owner        SessionOwner
	Title        string
	TitleLocked  bool
	Archived     bool
	Pinned       bool
	Flow         string
	Kind         SessionKind
	ForkedFrom   *ForkPoint
	Messages     int
	CreatedAt    time.Time
	LastActivity time.Time
	Handoff      HandoffContext
	Control      SessionControl
	Hold         string
}

type SessionControl int

const (
	ControlAgent SessionControl = iota
	ControlHandoffRequested
	ControlHuman
)

type SessionQuery struct {
	Archived *bool
	Control  *SessionControl
	Kinds    []SessionKind
	After    string
	Limit    int
}

type SessionPatch struct {
	Title    *string
	Archived *bool
	Pinned   *bool
	Hold     *string
	Handoff  *HandoffContext
}

type SessionIndex interface {
	Sessions(ctx context.Context, owner SessionOwner, q SessionQuery) ([]SessionMeta, string, error)
	UpdateSession(ctx context.Context, sessionID string, p SessionPatch) error
}

func (s *Stack) Sessions(ctx context.Context, q SessionQuery) ([]SessionMeta, string, error)
func (s *Stack) UpdateSession(ctx context.Context, sessionID string, p SessionPatch) error

// SessionWorkspaceMeta is the optional durable per-session workspace surface on
// SessionLog (docs/design/compatibility.md; the core memory store and
// adapter/postgres implement it). Generation is the sole compare-and-set token;
// zero denotes absent metadata.
type WorkspaceMeta struct {
	Ref        WorkspaceRef
	Generation int64
	Owner      WorkspaceOwner
}

type SessionWorkspaceMeta interface {
	WorkspaceMeta(ctx context.Context, sessionID string) (WorkspaceMeta, bool, error)
	SetWorkspaceMeta(ctx context.Context, sessionID string, expectedGeneration int64, m WorkspaceMeta) (int64, error)
}

// SessionWorkspaceOwnerGuard validates a workspace-metadata write owner while
// its own lock is held. The callback MUST NOT re-enter either store.
type SessionWorkspaceOwnerGuard interface {
	WithWorkspaceOwner(ctx context.Context, sessionID string, owner WorkspaceOwner, write func() error) error
}

func ImportWorkspaceMeta(ctx context.Context, sessionID string, owner WorkspaceOwner, legacy WorkspaceSnapshot) (WorkspaceSnapshot, error)

type FeedbackStore interface {
	PutFeedback(ctx context.Context, f Feedback) (int64, error)
	Feedback(ctx context.Context, sessionID string) ([]Feedback, error)
}

type PreemptedLister interface {
	Preempted(ctx context.Context, limit int) ([]Run, error)
}

type AuditKind string

const (
	AuditRunStarted   AuditKind = "run.started"
	AuditRunFinished  AuditKind = "run.finished"
	AuditModelCall    AuditKind = "model.call"
	AuditToolDecision AuditKind = "tool.decision"
	AuditToolOutcome  AuditKind = "tool.outcome"
	AuditGuard        AuditKind = "guard"
	AuditSuspended    AuditKind = "suspended"
	AuditResumed      AuditKind = "resumed"
	AuditLimit        AuditKind = "limit"
	AuditEgressDenied AuditKind = "egress_denied"
	AuditCompacted    AuditKind = "compaction"
)

type AuditRecord struct {
	Kind         AuditKind
	At           time.Time
	SessionID    string
	RunID        string
	RootRunID    string
	Flow         string
	Subject      string
	Tenant       string
	Tool         string
	Effect       Effect
	ArgsChecksum string
	Decision     string
	Host         string
	Reason       string
	Confidence   float64
	Approver     string
	Verdict      string
	Outcome      Outcome
	ResultSHA    string
	ResultBytes  int
	Compaction   *CompactionAudit
	Stage        GuardStage
	Model        string
	ModelVersion string
	ManifestHash string
	PrevHash     string
	Hash         string
}

// Optional compaction surfaces on SessionLog, AuditLog and EventLog,
// discovered by type assertion (docs/design/compatibility.md). They are the
// only seams a compaction writes through, so each write is atomic and keyed on
// the publication identity (ADR-0163).
type SessionCompactionAppender interface {
	AppendCompaction(ctx context.Context, sessionID string, expectedVersion int64, message Message) (int64, error)
}

type CompactionAuditAppender interface {
	AppendCompactionAudit(ctx context.Context, record AuditRecord) (bool, error)
}

type CompactionEventAppender interface {
	AppendCompactionEvent(ctx context.Context, publicationID string, runID string, event Event) (bool, error)
}

type CompactionFallback interface {
	Projection
	ValidateCompactionFallback(p ModelProfile) error
}

type SummarySource func(context.Context, History, ModelProfile) ([]Block, error)

// CompactionPublication is the durable payload a producer stores under
// Message.Meta[MetadataPayloadKey]. RunID names the original publisher; a
// reconciling run never rewrites it. ToVersion is FromVersion + 1.
type CompactionPublication struct {
	PublicationID string
	SessionID     string
	RunID         string
	RootRunID     string
	Flow          string
	Tenant        string
	Subject       string
	FromVersion   int64
	ToVersion     int64
	TokensBefore  int
	TokensAfter   int
	Policy        string
	SummarySHA    string
}

// CompactionAudit is the producer-owned payload of an AuditCompacted record.
// At, PrevHash and Hash stay sink-owned.
type CompactionAudit struct {
	PublicationID string
	FromVersion   int64
	ToVersion     int64
	TokensBefore  int
	TokensAfter   int
	Policy        string
	SummarySHA    string
}

const MetadataPayloadKey = "gohan.compaction.publication"

func CompactionPublicationID(sessionID string, runID string, fromVersion int64) string
func CompactionSummarySHA(summary []Block) (string, error)
func CompactionAuditHashBytes(p *CompactionAudit) []byte
func CloneCompactionAudit(p *CompactionAudit) *CompactionAudit

type AuditLog interface {
	Append(ctx context.Context, r AuditRecord) error
	Read(ctx context.Context, sessionID string) iter.Seq2[AuditRecord, error]
	Purge(ctx context.Context, tenant string, olderThan time.Time) (int, error)
}
```

Contracts:

- `Append` with a stale version returns `ErrVersionConflict`; the harness reloads and retries once for pure appends, otherwise surfaces the error.
- `Consume` is atomic; the second caller gets `ErrTokenConsumed`; expired checkpoints return `ErrTokenExpired`.
- **Conditional resume.** An external `Resume` inspects a token before consuming it: `Peek` returns an owned snapshot and changes no token state; `ConsumeIf` and `UpdatePending` compare that snapshot against the stored record atomically across every field including `Data` bytes and originator scopes, return `ErrVersionConflict` when the record moved, and never overwrite collected approvals. `ConsumeIf` records the input and consumes the token in one transaction; `UpdatePending` may change only `Data`. Every read path returns copies — `Put`'s result, `Peek`, `Consume`, `ConsumeIf`, `PendingInput`, `ResumeReady` — so no caller can mutate store-owned bytes. A conversation with approvals requires both optional interfaces and is refused with `ErrCheckpointIncompatible` when the store has none, rather than falling back to unconditional `Consume`.
- **Recovery discovery.** `ResumeReady` lists consumed checkpoints whose runs are still `Suspended`. `Recover` invokes it after the stale pass when `ResumeReadyLister` is present, before checking `CheckpointRuns` per candidate. Discovery errors propagate from `Recover`. Unbound memory checkpoint discovery returns an error matching `types.ErrCheckpointIncompatible`. After successful discovery, recovery skips consumed candidates without `CheckpointRuns`; otherwise it takes a `ResumingCheckpoint` lease fenced to the already-consumed token without consuming it again. Exactly one driver wins, and a `Finished` run is never re-executed. For a current `Preempted` checkpoint, recovery uses `ConsumeIf` before taking the token-fenced lease; it skips that path without `CheckpointRuns`. The stale pass does not require `CheckpointRuns`, though later discovery can still fail.
- **Lease generation.** `Lease.Generation` is a nonzero ownership token minted by `Start`, `Resuming` and `Reclaim`; `Heartbeat` changes only `Expires`. `Heartbeat`, `Finish`, `Suspend` and `Drain` require the run id *and* the current generation of an active, unexpired record; a stale generation fails with `ErrRunNotActive` and mutates nothing, so a driver that was reclaimed cannot heartbeat, finish, suspend or drain the new driver's run. `Expires` stays informational and is never an ownership token. Implementations allocate generations that do not repeat for the same run and refuse rather than wrap when the counter is exhausted.
- `Reserve` returns `created=true` for a new reservation. Existing `Completed` → decorator returns the recorded result without executing (`ToolFinished.Replayed=true`). Existing `Reserved` → outcome unknown; the tool re-executes with the same pinned key. A key presented with a fingerprint that differs from the stored entry's is refused with `ErrJournalFingerprintMismatch`, so a reused call id cannot inherit another call's outcome. `Complete` refuses a key with no live reservation — expired or never reserved — with `ErrJournalCompleteMissed`, so a write that outlived its journal entry fails the call instead of a clean run being reported over an unrecorded result (ADR-0149). `Reserve` also records the call's tool name and its canonical argument bytes in the entry, and the journal lists a session's entries whose outcome is still `Unknown` together with both, so a reconciliation resolves the tool and reads the arguments the effect was attempted with rather than reconstructing them from a history that projection and compaction are allowed to change (`tools` Verification).
- `Fingerprint = hash(tool name, canonical JSON of args)`. Before creating a new entry the decorator calls `ByFingerprint`; if an entry whose `Result.Outcome` is `Unknown` (state `Reserved`) exists in the same session, the new entry **inherits its `Key`** (key pinning per intent). If a `Completed`/`Succeeded` entry exists for a `SideEffect` fingerprint, the call proceeds with a fresh key but `gohan.tool.repeat_intent` increments and the loop detector counts it.
- `AuditLog.Append` is called only from chain steps and the harness (gate, scope check, journal, model step, guards, suspend/resume, limits, run start/finish). Tools, models, context providers and user code have no handle to it. Records carry checksums and sizes of args and results, never content. `PrevHash`/`Hash` form an optional per-session hash chain (postgres implementation on by default). Retention is per tenant; default 6 months; `Purge` is the only delete.
- `Journal` results are needed for replay during a run's lifetime only: after `Runs.Finish` they expire (default 24 h); the audit record keeps `ResultSHA` and `ResultBytes`, where `ResultSHA` is the SHA-256 of the canonical encoding of the result's message and `ResultBytes` is that encoding's size, so an auditor can recompute the checksum from the stored output.
- `stack.Reconstruct(ctx, sessionID)` joins `AuditLog` and `SessionLog` into an ordered decision trail: who ran what, for whom, which tools were allowed/denied/asked, who approved, what version of model/prompts/toolset was in force.
- `Runs.Start` with a non-empty `OperationID` is unique per tenant; a duplicate returns `ErrOperationExists{RunID}` and the caller reads the stored result via `ByOperation` + `ResultRef` (an `OutputStore` ref; it is retained by the Conversation tier of the owning session). A deliberate re-execution needs a new operation ID.
- `Suspend` releases the lease and records the token. The checkpoint's retention is its `ExpiresAt`. `ConsumeIf(t, expected, in)` stores the resume input atomically with conditional consumption, and `CheckpointRuns.ResumingCheckpoint` takes the lease fenced to that token. Recovery can acquire a token-fenced lease for a consumed checkpoint while its run remains `Suspended`. A crash after lease acquisition leaves a `Resuming` run whose input `Recover` reads back with `PendingInput` and re-drives. The frozen `Consume` and `Resuming(runID)` methods remain available. Only `Running` and `Resuming` runs are reclaimed by the stale pass.
- `Runs.Start` fails with `ErrRunActive` if the session has an unexpired lease. Leases are refreshed by the harness at least every `HeartbeatEvery`, through the run's heartbeat goroutine and, where the store supports it, the per-turn writes (`recovery`). `Stale` lists runs whose heartbeat is older than `staleAfter` by the store's clock and state `Running` or `Resuming`, so a run left `Resuming` by a crash is reclaimed like a stale `Running` one. `PreemptedLister` is an optional interface on `Runs` (`docs/design/compatibility.md`): when the store implements it, `Recover` discovers preempted runs before stale runs; when it does not, recovery skips preempted discovery. `Preempted` lists `Suspended` runs whose checkpoint reason is `Preempted`, oldest first. The core memory store and `adapter/postgres` implement it. `Reclaim` atomically takes over a stale run (exactly one reaper wins).
- **Forking.** `Stores.ForkSession(from, upTo)` creates a new session whose history is the parent's prefix through message `upTo` (message IDs, origins and compaction blocks preserved), with the same `SessionOwner`, `History.ForkedFrom` set, an audit record `session_fork` and `gohan.session.forked{tenant}`. It fails with `ErrRunActive` while the parent has a live lease; a suspended parent forks (its token stays with the parent). Inheritance: owner yes; session grants no (the child starts with none); notes and shared state copied as of fork time (shared state at version 1 with a full-snapshot `StateChanged`); journal, checkpoints and runs no; subject memory is owner-scoped and unchanged; taint is computed over the child's own history. A store may implement copy-on-write with a parent pointer, but a fork is not a dependent record: `DeleteSession(parent)` leaves it readable, and `EraseSubject` covers parent and forks alike. `stack.Reconstruct(child)` prepends the parent's trail through `UpTo` with a fork marker. Assembly is prefix-stable, so a fork's first model call reuses the parent's provider cache.
- **Session run discovery.** `SessionRunFinder` is an optional interface on `Runs` (`docs/design/compatibility.md`): it returns the session's active run — state `Running` or `Resuming` with a live lease — or `ErrRunNotFound`. `Suspended`, terminal, lease-expired and null-lease runs are excluded, and the store judges the lease on its own clock. A store that does not implement it keeps every other operation unchanged; run discovery over it simply reports no run.
- **Session index.** `SessionIndex` is an optional interface on `SessionLog` (`docs/design/compatibility.md`; the core memory store and `adapter/postgres` implement it). `Append` maintains `Messages` and `LastActivity` (store clock); `ForkSession`, `subflows` and `release` set `Kind`; `Sessions` lists by `LastActivity` descending with cursor pagination (`Limit` default 25, max 200) and, with an empty `Kinds`, returns only `SessionPrimary` and `SessionFork`; `Archived == nil` means not archived. `Stack.Sessions` scopes the query to `PrincipalFrom(ctx)`'s owner (or a tenant with `session:read`); `Stack.UpdateSession` is owner-checked like `Send`, and a title set by a user sets `TitleLocked`. Without the index, `Stack.Sessions` returns `ErrSessionIndexRequired`. Retention: `Purge` skips `Pinned` sessions and applies `ArchivedRetention` (default 180 days) to archived ones; `DeleteSession` and `EraseSubject` remove index rows; `Reconstruct` reads archived sessions.
- **Two clocks.** Store time is authoritative for every instant that is persisted and later compared: lease `Expires`, staleness, `Checkpoint.ExpiresAt` (enforced at `Consume`), journal expiry, operation retention, catalog `ttlMs`. Stores compute and compare these from their own clock (`now()` in Postgres, server `TIME` in Redis, the process clock in `core/memory`); callers pass durations (`Start(ttl)`, `Reclaim(ttl)`, `Stale(staleAfter)`), never instants, so a pod with a skewed clock cannot shorten or extend anyone's lease. `Lease.Expires` is store time and informational: the harness heartbeats on `ttl/3` and never compares it. Retention sweeps (`Purge`, `Expire(olderThan)`) keep instants because they are coarse and operator-driven. Harness time is monotonic-only, for budgets and timeouts (`RunLimits.MaxWallClock`, `ToolSpec.Timeout`, `ModelProfile.Timeout`, `HeartbeatEvery`, `ReaperEvery`) via `time.Since` and `context.WithTimeout`; no rule compares a harness instant to a stored one. `EventMeta.Time` and audit timestamps are harness wall time, informational; `Seq` orders. `WakeAt` is UTC read by the scheduler; `Sunset` is UTC read at `Build`. `memory.New(memory.WithNow(func() time.Time))` lets tests move the memory stores' clock; there is no clock port in core.
- Every stored shape (`Message`, `State`, `Checkpoint`, `AuditRecord`, `Event`, journal `Entry`, `Run`, `RedactionMap`) is persisted in a record envelope that names the shape and carries the `SchemaVersion` the record was written at, with the payload beside it; domain types carry no schema metadata, because a record is versioned where it is stored. Core keeps a registry of pure upcasters N→N+1 (no clocks, lookups or randomness) applied on read: a reader decodes through the envelope, upcasts a copy of the payload to the current version, and emits `gohan.schema.upcast{from,to}` once per successful chain, so a reader that drops the record or fails the chain emits nothing. Stores never rewrite records in place; additive changes need no upcaster. `storetest.Schemas` replays recorded fixtures from every released schema version through the current readers.
- `Checkpoint.Originator` is a `Principal` and therefore carries no token by type: the originator's token stays in `ctx` (`Credential`) and is re-issued on resume by `CredentialSource` (`identity`).
- **Redaction map store.** `Stores.RedactionMaps` holds a session's `RedactionMap` separately from the run it belongs to: `Put(ctx, tenant, session, map)` and `Get(ctx, tenant, session)`, tenant-keyed, with retention independent of checkpoints and journal entries. A checkpoint never carries the map, a resumed run reads it back by tenant and session before it releases output, and erasure deletes it with the subject's other records.
- **Region.** A store that serves a residency-bound tenant states where it lives through an optional `RegionProvider` interface (`Region() string`). `Build` refuses when the tenant's `Residency` is set and a configured store does not implement the interface, states an empty region, or states a region other than the tenant's, naming the tenant, the store and the regions. A store that cannot state its region never serves such a tenant.
- Keys are tenant-scoped by implementations (tenant from `RunInfo`).
- `adapter/postgres` adds `func (j *Journal) CompleteTx(ctx context.Context, tx pgx.Tx, k gohan.CallKey, res gohan.ToolResult) error`.
- **Postgres schema.** Every table lives in schema `gohan` (or the name given by `postgres.WithSchema`, so two stacks may share one database) as `<store>_<record>`; users never alter these tables and extend by their own tables keyed on gohan ids; `sqlc` queries are the only SQL in the adapter. Migrations are embedded goose SQL files applied by `postgres.Migrate(ctx, pool, MigrateOptions{Schema, TargetVersion})` under an advisory lock (concurrent pods serialise) and by the `gohan-postgres migrate` command (JSON output when stdout is not a TTY). The intended place is the deploy pipeline or a pre-install hook with a DDL role; `postgres.WithAutoMigrate()` is for development and `Build` refuses it when `CostTags.Environment` is `production`. DDL versions are separate from record `SchemaVersion`s. **Skew rule:** each release runs against the previous release's schema and the next one — added columns are nullable or defaulted and not read until the following release, drops happen two releases after the last reader; `postgres.New` reads `gohan.schema_version` and fails with `ErrSchemaTooOld` below its `MinSchema`, and `Build` warns `gohan.postgres.schema_ahead` when the schema is newer than the code. Time-partitioned tables (`journal`, `events`, `audit`, `outputs`, `session_log`) have partitions created 14 days ahead by `Migrate` and thereafter by `postgres.Maintain(ctx)` on the reaper cadence; a write into a missing partition fails with `ErrPartitionMissing`, never a default partition. `storetest.Migrations` runs every store suite on a fresh install at head and on an install migrated step by step from the first version, and requires both schemas to be identical.

### Session workspace metadata

`SessionWorkspaceMeta` is such a surface on `SessionLog`: `WorkspaceMeta` reads the durable per-session workspace pointer and reports absence, `SetWorkspaceMeta` compares the expected generation and validates the current lease owner in one atomic write and returns `types.ErrVersionConflict` on a stale token or `types.ErrRunNotActive` on a stale owner, and `ImportWorkspaceMeta` writes one legacy generation-zero ref into absent metadata. A `SessionLog` that does not implement the surface keeps every other operation unchanged, and core MUST refuse a `PerSession` build that wires no durable workspace-metadata surface.

### Notice claims and retention

`Notices` claims up to `limit` pending notices for the calling dispatcher for `LeaseTTL` (another pod's `Notices` skips them until the claim expires), `AckNotice` removes one; an unacknowledged claim returns to pending. Notices belong to the session's Conversation tier and are purged and deleted with it. Postgres: `run_notices`; memory: an in-process queue.

### Finish with pending signals and signal storage

`Finish` is atomic with the mailbox: it fails with `ErrSignalsPending` when `SignalSteer` entries remain, and the runtime drains them and runs one more turn, which counts against `MaxTurns`; when that limit is already reached the run ends with `Done(StopLimit)` instead; pending `SignalCancel` never blocks `Finish`. Signals are not journal entries; a steer becomes part of history only when the runtime appends it (`runtime`). The memory store and Postgres (`run_signals`, purged with the run's session under the Conversation tier) implement the mailbox; `Signal` from any pod reaches the holder on its next `Drain`.

### Retention tiers

Three tiers, each with its own clock by the store's time: **Conversation** covers everything keyed by a session — `SessionLog`, `EventLog`, `OutputStore` and its blobs, `Checkpoints`, `Journal`, the run mailbox, `FeedbackStore` and `RedactionMap` — measured from `SessionMeta.LastActivity`; a session with a `Running` run never ages, and a `Suspended` one until its checkpoint's `ExpiresAt` passes; a session whose suspended checkpoint has expired ages normally, `Recover` no longer resumes that run and `Consume` returns `ErrTokenExpired`. `Pinned` sessions never age, and an archived session ages by `ArchivedRetention` (default 180 days, `SessionIndex`) instead of the Conversation tier. **Working** covers notes and subject memory, measured from the entry's last write. **Audit** covers `AuditLog` and is never shorter than `Conversation`; `Build` fails on such a policy. `Stack.Maintain` runs every store's purge tenant by tenant under the tenant's policy, skips `Pinned`, `Archived` (their own threshold, `stores.purge-respects-pinned-and-archived`) and held sessions, calls `postgres.Maintain`, and appends one `purge` audit record per tenant with per-store counts; `gohan-maintain` (JSON when stdout is not a TTY) is the cron entry point. `Conversation: 0` is zero retention: the session's Conversation-tier records are deleted at `Finish` and only audit checksums remain; `agent.Ephemeral()` sets it for a flow.

### Legal hold

**Legal hold.** `SessionMeta.Hold` is a hold id (empty = none) set through `UpdateSession` by a principal of the owner's tenant with `session:hold`; each change is audited (`hold_set`, `hold_cleared`). While set, `Maintain` skips the session and counts it in `Held`, `DeleteSession` returns `ErrSessionHeld`, zero retention does not apply, and `EraseSubject` erases everything else and lists the session in `EraseReport.Held`. Holds live in session metadata; a host that needs a hold registry drives `UpdateSession` from it.

### Checkpoint backend version compatibility and quota pools

A resume under a different backend version MUST NOT assume compatibility: an agent runtime whose driver state decodes MUST proceed through `Replay`, and a graph checkpoint that cannot be decoded under the current backend MUST be refused with `ErrCheckpointIncompatible` and MUST leave the token unconsumed. A `QuotaPool` on a model profile MUST be enforced across every process sharing that pool, `Batch` admission MUST yield to `Interactive` admission while pool queue-wait is above the threshold, and pool spend MUST be bounded per window so a limit is a `Permanent` `*LimitExceededError` naming the limit.

### Postgres adapter seams and workspace transaction

No port gains a method in core; the adapter-specific seams are methods on the concrete adapter types (ADR-0117). `SessionWorkspaceMeta` MUST run one transaction that takes the session serialization lock, locks `session_record` and then the owning `run_record` last, applies the `stores` refusal precedence, writes `expectedGeneration+1` and commits without releasing the owner row lock; any operation that needs both rows uses the session-before-run order, and a run-only mutation keeps run-only locking.

### Postgres record envelopes

Every stored shape MUST be persisted in the `stores` record envelope and decoded through the schema registry on read.

### Postgres migration reversal

On an empty scratch schema installed at head, the embedded migration down sections MUST support a down-up-down schema-shape check. The check MUST execute down sections newest first and clear migration bookkeeping before each reapplication. Reapplication through `postgres.Migrate` MUST apply every embedded version and reproduce the fresh head install's normalized column and constraint definitions.

### Postgres final migration state

After the final down sweep, the only ordinary table MUST be `schema_migrations`. This check does not guarantee recovery of deleted data or require a public down-migration API.

## Requirements

### Requirement: Store contracts

`storetest.SessionLog`, `storetest.Checkpoints`, `storetest.Journal`, `storetest.Runs`, `storetest.AuditLog` SHALL pass for the core memory implementations and `adapter/postgres` (testcontainers). The journal `Entry` carries `Attempts int` and `Endpoint string` in addition to its tool fields: a model step records the attempts that started and the winner's endpoint, and both fields are zero-valued for a tool entry.

#### Scenario: append conflict
ID: `stores.append-conflict`
- WHEN two writers append with the same `expectedVersion`
- THEN exactly one succeeds; the other gets `ErrVersionConflict`

#### Scenario: concurrent consume
ID: `stores.concurrent-consume`
- WHEN 10 goroutines `Consume` the same token
- THEN exactly one succeeds; 9 get `ErrTokenConsumed`

#### Scenario: concurrent reserve
ID: `stores.concurrent-reserve`
- WHEN 10 goroutines `Reserve` the same `CallKey`
- THEN exactly one gets `created=true`

#### Scenario: no secrets stored
ID: `stores.no-secrets-stored`
- WHEN a checkpoint is stored while the caller's `Credential` holds a token
- THEN the stored `Originator` holds only `Subject`, `Tenant` and `Scopes`, and no token is persisted

#### Scenario: lease exclusivity
ID: `stores.lease-exclusivity`
- WHEN two pods call `Runs.Start` for the same session
- THEN exactly one gets a lease; the other gets `ErrRunActive`

#### Scenario: reclaim race
ID: `stores.reclaim-race`
- WHEN 10 reapers call `Reclaim` on the same stale run
- THEN exactly one succeeds

### Requirement: Two clocks

Stores MUST compute and compare persisted instants using their own clocks.

#### Scenario: staleness by store clock
ID: `stores.stale-by-store-clock`
- WHEN a run's last heartbeat is 20 s old by the store's clock and a reaper calls `Stale(ctx, 30*time.Second, 10)`
- THEN the run is not listed, whatever the reaper's own wall clock says

#### Scenario: skewed caller cannot reclaim
ID: `stores.skewed-caller-cannot-reclaim`
- WHEN a pod whose wall clock is 5 min ahead runs `Recover` while another pod holds a live lease
- THEN `Stale` returns nothing and `Reclaim` is never called

#### Scenario: checkpoint expiry by store clock
ID: `stores.checkpoint-expiry-store-clock`
- WHEN a checkpoint's `ExpiresAt` has passed by the store's clock
- THEN `Consume` returns `ErrTokenExpired` even if the caller's clock is behind

#### Scenario: memory store clock jump
ID: `stores.memstore-clock-jump`
- WHEN `memory.New(memory.WithNow(clock))` is used and `clock` is advanced past `LeaseTTL` without a heartbeat
- THEN `Stale(ctx, LeaseTTL, 10)` lists the run

### Requirement: Exactly-once tool effects

`adapter/postgres` MUST expose `CompleteTx(ctx context.Context, tx pgx.Tx, k gohan.CallKey, res gohan.ToolResult) error` on its concrete `Journal` so a caller-owned `pgx.Tx` carries a tool's own write and the journal completion to one commit; a rollback MUST leave neither. `Complete` and `CompleteTx` MUST share one guarded statement path, so a key with no live reservation is refused with `ErrJournalCompleteMissed` on either, and `CompleteTx` MUST NOT commit — the caller owns the transaction.

#### Scenario: replay returns recorded result
ID: `stores.replay-returns-recorded-result`
- WHEN a completed call is re-invoked during resume
- THEN the recorded result is returned, `ToolFinished.Replayed=true`, and the tool function is not called

#### Scenario: crash window
ID: `stores.crash-window`
- WHEN the process dies after tool execution and before `Complete`
- THEN on retry the tool is called again with the same `IdempotencyKey(ctx)` and `gohan.journal.unknown_outcome` increments

#### Scenario: same-transaction journal
ID: `stores.same-transaction-journal`
- WHEN a postgres-backed tool inserts a booking and calls `CompleteTx` in the same `pgx.Tx`, and the tx rolls back
- THEN neither the booking nor the journal entry exists

### Requirement: Intent-level idempotency and uncertainty

The decorator MUST reuse the pinned key when an entry with the same fingerprint has an `Unknown` outcome in the same session.

#### Scenario: late commit
ID: `stores.late-commit`
- WHEN `create_booking` (SideEffect) times out, then the model re-calls it with identical args and a new call ID
- THEN the first result delivered to the model has `Outcome: Unknown` (not a retryable error), the second call reuses the first entry's pinned key, and `gohan.journal.key_pinned` increments

#### Scenario: read-back offered
ID: `stores.read-back-offered`
- WHEN a tool with `ReadBack: "get_booking"` returns `Unknown`
- THEN the model-visible result names `get_booking` as the verification tool

#### Scenario: uncertainty surfaced
ID: `stores.uncertainty-surfaced`
- WHEN a run ends with one journal entry still `Unknown`
- THEN `Flow.Invoke` returns `*UncertainOutcomeError` carrying `Out` and the `CallKey`; `Conversation` emits `Done{Uncertain: [key]}`

#### Scenario: fingerprint after compaction
ID: `stores.fingerprint-after-compaction`
- WHEN history is truncated so the model no longer sees a previous failed call, and it re-calls the same tool with the same args
- THEN the loop detector counts it via the journal fingerprint, independent of context contents

#### Scenario: reserve refuses a reused key with different arguments
ID: `stores.reserve-fingerprint-mismatch`
- WHEN a call key is reserved again with a fingerprint that differs from the stored entry's
- THEN the reservation is refused with `ErrJournalFingerprintMismatch` and the stored entry is unchanged

#### Scenario: complete refuses a key with no live reservation
ID: `stores.complete-without-reservation`
- WHEN a completion arrives for a key that is expired or was never reserved
- THEN it is refused with `ErrJournalCompleteMissed`, so the caller cannot report a clean run over an unrecorded result

### Requirement: Optional interfaces

An optional surface MUST be discovered by type assertion, and the spec that introduces it MUST state core's behaviour when the assertion fails.

#### Scenario: session index optional
ID: `stores.session-index-optional`
- WHEN a `SessionLog` implementation does not implement `SessionIndex`
- THEN `Stack.Sessions` returns `ErrSessionIndexRequired` and every other operation works unchanged

#### Scenario: preempted lister fallback
ID: `stores.optional-preempted-lister-fallback`
- WHEN a `Runs` implementation does not implement `PreemptedLister` and a run was preempted
- THEN `Recover` completes without error, the run stays `Suspended` and `Resume(token, Continue())` still works

### Requirement: Notice outbox

`Finish` and `Suspend` MUST append a `RunNotice` (`streams`) in the same transaction as the state change; a crash after the transaction never loses the notice and a crash before it never emits one.

#### Scenario: notice written with finish
ID: `stores.notice-written-with-finish`
- WHEN the pod crashes right after `Runs.Finish` commits and before any delivery
- THEN another pod's dispatcher claims the notice through `Notices` and delivers it; an unacknowledged claim returns to pending and a redelivery carries the same notice id, which is the consumer's idempotency key (`streams`)

### Requirement: Run mailbox

Every run MUST have a mailbox in `Runs`. `Signal` appends for a run whose state is `Running` and fails with `ErrRunNotActive` otherwise, and with `ErrMailboxFull` past `MaxPendingSignals`; `Drain` returns and removes the pending signals in arrival order for the lease holder only. `Cancel` is `Signal{SignalCancel}`.

#### Scenario: cancel signal crosses pods
ID: `stores.signal-cancel-cross-pod`
- WHEN pod A calls `Signal(runID, SignalCancel)` while pod B holds the lease
- THEN pod B's next `Drain` returns the cancel and the run stops at that safe point

#### Scenario: mailbox full
ID: `stores.mailbox-full`
- WHEN eleven steers are posted before the holder drains
- THEN the eleventh returns `ErrMailboxFull` and the first ten are delivered in order

### Requirement: Session index

`SessionMeta.Handoff` MUST carry the `HandoffContext` recorded when a run requests a human, and `SessionPatch.Handoff` MUST set it through `UpdateSession`. Both fields default to the zero value, so a stored session row and a request row stay compatible and no data migration is required.

#### Scenario: control in session index
ID: `stores.control-in-session-index`
- WHEN two sessions are `ControlHandoffRequested` and one is `ControlHuman`
- THEN `Sessions` with `Control: ControlHandoffRequested` returns the two, ordered by `LastActivity`, for an operator queue

#### Scenario: sessions listed by owner
ID: `stores.sessions-listed-by-owner`
- WHEN subject `u1` has three sessions and `u2` has two in the same tenant
- THEN `Stack.Sessions` for `u1` returns exactly the three with `Title`, `Messages` and `LastActivity`

#### Scenario: sessions ordered by last activity
ID: `stores.sessions-order-last-activity`
- WHEN a message is appended to the oldest of a subject's sessions
- THEN it is listed first and the cursor from the previous page still resumes without duplicates

#### Scenario: forks and children hidden by default
ID: `stores.sessions-default-excludes-forks-children`
- WHEN a subject has a primary session, a fork of it, a sub-flow child and a shadow session
- THEN the default listing returns the primary and the fork only, and `Kinds: [SessionChild]` returns the child

#### Scenario: purge respects pinned and archived
ID: `stores.purge-respects-pinned-and-archived`
- WHEN `Purge` runs with a 30-day threshold over a pinned session, an archived session 200 days old and an archived one 100 days old
- THEN the pinned session stays, the 200-day archived one is purged and the 100-day one stays

### Requirement: Forking

`Stores.ForkSession(from, upTo)` MUST create a new session with the parent's history prefix through `upTo`, preserving message IDs, origins and compaction blocks.

#### Scenario: fork copies the prefix
ID: `stores.fork-prefix`
- WHEN `ForkSession(s, m7)` is called on a session with ten messages
- THEN the new session loads seven messages with their original IDs, origins and compaction blocks, `ForkedFrom` is `{s, m7}` and an audit record `session_fork` exists

#### Scenario: fork requires no lease
ID: `stores.fork-requires-no-lease`
- WHEN the parent has a live run lease
- THEN `ForkSession` returns `ErrRunActive` and no session is created

#### Scenario: fork survives parent delete
ID: `stores.fork-survives-parent-delete`
- WHEN the parent is deleted with `DeleteSession`
- THEN the fork still loads its full history

#### Scenario: reconstruct crosses fork
ID: `stores.reconstruct-crosses-fork`
- WHEN `Reconstruct` runs on a fork
- THEN the trail starts with the parent's records through `UpTo`, a fork marker, then the fork's own records

### Requirement: Feedback store

Feedback MUST be removed with its session on erasure or deletion.

#### Scenario: feedback cascades on erase
ID: `stores.feedback-cascades-on-erase`
- WHEN `EraseSubject` or `DeleteSession` runs for a session with feedback rows
- THEN `FeedbackStore.Feedback(sessionID)` returns nothing and audit keeps the `feedback` records with checksums only

### Requirement: Retention and legal hold

Retention MUST be one `RetentionPolicy` per stack (`agent.WithRetention`; default 90 days / 180 days / 2 years), overridable per tenant through the optional `RetentionSource`.

#### Scenario: purge by tier
ID: `stores.retention-purge-by-tier`
- WHEN a tenant's policy is `{Conversation: 30d, Working: 60d, Audit: 1y}` and `Maintain` runs over a session idle 40 days with notes written 40 days ago
- THEN the session's messages, events, outputs, checkpoints, journal, feedback and redaction map are gone, the notes remain, the audit records remain, and the `purge` audit record carries the counts

#### Scenario: zero retention deletes at finish
ID: `stores.retention-zero-deletes-at-finish`
- WHEN a flow built with `agent.Ephemeral()` finishes a run
- THEN no message, event, output or checkpoint of the session remains, the run's audit records remain with checksums, and a `Send` on the session starts from an empty history

#### Scenario: hold blocks delete and purge
ID: `stores.hold-blocks-delete-and-purge`
- WHEN a session has `Hold: "case-42"`, is 400 days idle, and `DeleteSession` then `Maintain` run
- THEN `DeleteSession` returns `ErrSessionHeld`, `Maintain` leaves the session intact with `Held: 1`, and `gohan.retention.held` is 1

#### Scenario: purge audited
ID: `stores.purge-audited`
- WHEN `Maintain` purges twelve sessions of tenant `t1`
- THEN one audit record `purge` for `t1` names the tenant, the policy in force and the per-store counts, and no content

### Requirement: Session deletion

`Stores.DeleteSession` MUST delete the session and its dependent records while retaining audit records with content checksums only.

#### Scenario: delete cascades
ID: `stores.delete-cascades`
- WHEN `Stores.DeleteSession` is called for a session with checkpoints, journal entries, outputs, notes and events
- THEN the session, its child and shadow sessions, and every dependent record are removed from all stores; audit records remain with content checksums only; a session under a hold fails with `ErrSessionHeld` first, and otherwise a live lease makes the call fail with `ErrRunActive`

### Requirement: Audit trail

`AuditLog.Append` MUST be called only from chain steps and the harness. Records carry checksums and sizes of args and results, never content.

#### Scenario: chain-written only
ID: `stores.chain-written-only`
- WHEN a tool implementation, a context provider or user middleware attempts to append an audit record
- THEN there is no API path to do so; only steps and the harness hold the writer

#### Scenario: decision trail
ID: `stores.decision-trail`
- WHEN a run denies one tool by scope, asks approval for another, resumes with approver `op-7`, and finishes
- THEN `Reconstruct(sessionID)` yields records for scope denial, gate `Ask`, `Resumed{Approver: "op-7", Verdict: approve}`, tool outcome with `ResultSHA`, and run finish, in order, each carrying `ManifestHash` and `ModelVersion`

#### Scenario: no content
ID: `stores.no-content`
- WHEN a tool returns a 50 KiB result containing an email address
- THEN no audit record contains the address; the record carries `ResultSHA` and `ResultBytes = 51200`

#### Scenario: append failure is fatal to the step
ID: `stores.append-failure-is-fatal-to-the-step`
- WHEN `AuditLog.Append` fails during a gate decision
- THEN the tool does not execute and the run fails with `*StepError{Step: "gate"}`

#### Scenario: journal TTL
ID: `stores.journal-ttl`
- WHEN a run finished 25 h ago (TTL 24 h)
- THEN its journal results are purged and its audit records remain

#### Scenario: hash chain
ID: `stores.hash-chain`
- WHEN a record in the middle of a session is modified in the store
- THEN `Reconstruct` reports a chain break at that record

### Requirement: Checkpoint versioning and quota pools

A checkpoint MUST carry the backend and backend version that wrote it, and the additive `WorkspaceGeneration int64` field names the session workspace generation the checkpoint's ref was published at; a checkpoint written before this field reads it as zero, which restores only a session that holds no durable workspace metadata.

#### Scenario: native checkpoint after adapter upgrade
ID: `stores.native-checkpoint-after-adapter-upgrade`
- WHEN an eino agent flow suspended under adapter version X is resumed under version Y
- THEN resume proceeds via `Replay` and `gohan.resume.fallback_replay` increments

#### Scenario: graph checkpoint incompatible
ID: `stores.graph-checkpoint-incompatible`
- WHEN the same happens to an eino graph flow
- THEN `Resume` returns `ErrCheckpointIncompatible` and the token is not consumed

#### Scenario: shared pool across services
ID: `stores.shared-pool-across-services`
- WHEN two processes use profiles with `QuotaPool: "openai-prod"` backed by `adapter/redis`
- THEN their combined request rate never exceeds the pool limit

#### Scenario: batch yields to interactive
ID: `stores.batch-yields-to-interactive`
- WHEN pool queue-wait for `Interactive` exceeds the threshold while a `Batch` flow is running
- THEN new `Batch` admissions are deferred until it recovers and `gohan.pool.deferred{class=batch}` increments

#### Scenario: daily pool budget
ID: `stores.daily-pool-budget`
- WHEN `pool/day` spend reaches its limit
- THEN new model calls on that pool fail `Permanent` with `*LimitExceededError{Limit: "pool/day"}` until the window resets

### Requirement: Postgres contract

`adapter/postgres` MUST implement every store port the core declares in its own module `github.com/victorzhuk/gohan/adapter/postgres`: `SessionLog` (with `SessionIndex` and `SessionWorkspaceMeta`), `Checkpoints` (with `CheckpointResumer` and `ResumeReadyLister`), `Journal` (with `CompleteTx` and `ModelCompleter`), `Runs` (with `PreemptedLister`, `SuspendReasonRecorder` and `FinishReasonRecorder`), `AuditLog`, `OutputStore`, `EventLog`, `NotesStore` and `RedactionMaps`.

#### Scenario: schema owned by the adapter
ID: `stores.pg-schema-owned`
- WHEN two stacks are built with `WithSchema("a")` and `WithSchema("b")` on one database
- THEN each sees only its own sessions and runs and neither migration touches the other schema

#### Scenario: migrate serialised
ID: `stores.pg-migrate-serialised`
- WHEN five pods call `Migrate` at the same time
- THEN one applies each pending version, the others wait on the advisory lock and finish with nothing to do

#### Scenario: schema too old
ID: `stores.pg-schema-too-old`
- WHEN `postgres.New` runs against a schema below its `MinSchema`
- THEN it fails with `ErrSchemaTooOld` before any query

#### Scenario: schema ahead warns
ID: `stores.pg-schema-ahead-warns`
- WHEN the schema is one version newer than the code knows
- THEN `Build` succeeds, `gohan.postgres.schema_ahead` is 1 and every store suite still passes

#### Scenario: partitions created ahead
ID: `stores.pg-partitions-ahead`
- WHEN `Maintain` runs
- THEN partitions exist for the next 14 days and a write dated inside them succeeds; a write beyond them fails with `ErrPartitionMissing`

#### Scenario: fresh equals upgraded
ID: `stores.pg-fresh-equals-upgraded`
- WHEN `storetest.Migrations` compares a fresh head install with one migrated from version 1
- THEN the schema dumps are identical apart from partition names and the suites pass on both

#### Scenario: migration down-up-down
ID: `stores.pg-migration-down-up-down`
- GIVEN an empty scratch schema installed at head and a separate fresh head install for comparison
- WHEN every embedded down section runs newest first and migration bookkeeping is cleared
- THEN the recorded migration version is zero
- WHEN `postgres.Migrate` reapplies the migrations
- THEN every embedded version is applied and the normalized column and constraint definitions match the fresh head install
- WHEN every down section runs newest first again and migration bookkeeping is cleared
- THEN the only ordinary table remaining is `schema_migrations`

#### Scenario: auto-migrate refused in production
ID: `stores.pg-auto-migrate-refused-in-prod`
- WHEN a stack with `WithAutoMigrate()` is built with `CostTags.Environment: "production"`
- THEN `Build` fails naming the option

#### Scenario: retention by partition
ID: `stores.retention-by-partition`
- WHEN journal retention runs for a day older than TTL
- THEN the adapter drops the partition and no row-level `DELETE` is issued

#### Scenario: transaction across model call
ID: `stores.transaction-across-model-call`
- WHEN example code holds a `pgx.Tx` across `flow.Invoke`
- THEN the lint fails the build

#### Scenario: bloat bound
ID: `stores.bloat-bound`
- WHEN `storetest.Bloat` runs 1 M reserve/complete cycles
- THEN p99 `Reserve` stays under the bound and dead tuples stay bounded

### Requirement: Postgres schema ownership and maintenance

`adapter/postgres` MUST own schema upkeep: `Migrate` applies embedded versions under one advisory lock to `TargetVersion`; `gohan-postgres migrate` drives it offline; `Maintain` creates time partitions, reaps dead runs via `SKIP LOCKED` with coalesced heartbeats and drops aged partitions. Deployments MUST run expand migrations before rollouts and contract migrations only after all readers serve the new shape.

#### Scenario: migrate command drives one path
ID: `stores.pg-migrate-command`
- WHEN an operator runs `gohan-postgres migrate` with a target schema and `TargetVersion` on a schema three versions behind
- THEN exactly the pending versions apply under the advisory lock and a second run reports nothing to do

#### Scenario: maintain is idempotent and concurrent-safe
ID: `stores.pg-maintain-idempotent`
- WHEN two processes call `Maintain` at the same time
- THEN partitions exist once for the rolling window, expired partitions are dropped and both calls finish without error

#### Scenario: expand before contract across versions
ID: `stores.pg-expand-contract-skew`
- WHEN a reader on version N-1 and a writer on version N share one schema during a rolling upgrade
- THEN the writer uses only expanded columns and the contract migration is refused until every reader serves version N

#### Scenario: hot runs table stays small
ID: `stores.pg-hot-runs-reaping`
- WHEN dead runs accumulate while active runs heartbeat every interval and a reaper runs under live workers
- THEN reaping takes bounded `SKIP LOCKED` batches without blocking workers, heartbeats coalesce to one row update per run and the hot table stays within its row bound

### Requirement: Postgres output claim ledger

`adapter/postgres.Outputs.Put` MUST record one claim per `(ref, session)`, so a ref written again by another session keeps a claim for each. A ref MUST live until its last claim drops; subject erasure and the session cascade MUST drop only their own claims and MUST delete a ref only when no claim remains. `CopySessionDependents` MUST give a fork a claim on each ref the parent owns.

#### Scenario: shared ref survives one owner's erasure
ID: `stores.pg-output-shared-ref-survives-erase`
- WHEN two sessions of different subjects `Put` equal bytes yielding one ref and one subject is erased
- THEN the ref still reads for the other subject and the erasure report counts no removal for it

#### Scenario: last claim drops the ref
ID: `stores.pg-output-last-claim-deletes-ref`
- WHEN the last claim on a ref drops through a session delete
- THEN `Get(ref)` returns `stores.ErrOutputNotFound`

### Requirement: Postgres output claim backfill

The migration MUST create session claims for surviving persisted references whose session and owner are reconstructable. When no session attribution survives, it MUST preserve every `output_record` tenant and subject as a legacy owner claim and MUST NOT invent a synthetic session. A ref MUST stay live while a session claim or a legacy owner claim exists, and a claim change MUST run under a ref-keyed advisory lock taken in sorted order.

#### Scenario: reconstructable legacy ownership is preserved
ID: `stores.pg-output-legacy-claims-preserved`
- WHEN an upgraded fixture holds refs whose runs still reference them through the recorded session and owner
- THEN each ref keeps a session claim after the migration and the subject erasure of one owner does not delete it for the other

#### Scenario: Put cannot race last-claim removal
ID: `stores.pg-output-put-erase-race`
- WHEN `Put` for one ref and the removal of that ref's last claim run concurrently
- THEN the ref survives with exactly one claim and `Get(ref)` returns the canonical bytes

### Requirement: Postgres session index pagination

The PostgreSQL session index MUST freeze complete matching membership and LastActivity-descending order for each cursor. Activity changes MUST NOT omit or duplicate surviving members. Filters MUST select membership before pagination. The default page size MUST be 25 and the maximum MUST be 200. Cursors MUST remain owner-scoped and expire after LeaseTTL.

#### Scenario: order and filters inside the query
ID: `stores.pg-sessions-order-and-filters`
- WHEN a subject's sessions mix archived, fork, child and shadow kinds and a message is appended to the oldest primary
- THEN the default page returns primaries and forks only, oldest-by-activity last, and the archived, child and shadow sessions appear only under the matching filters

#### Scenario: activity change between pages
ID: `stores.pg-sessions-cursor-stable`
- WHEN a session's `LastActivity` changes between two page requests on one cursor
- THEN that session is returned at most once and the page boundary stays bound to the cursor's snapshot

#### Scenario: snapshot stays complete past one page cap
ID: `stores.pg-sessions-snapshot-complete`
- WHEN more than 1600 matching sessions exist and unseen sessions change activity between pages
- THEN every surviving snapshot member appears exactly once in the initial order

#### Scenario: cursor expiration refuses continuation
ID: `stores.pg-sessions-cursor-expiration`
- WHEN the store clock reaches the cursor expiration time
- THEN continuation fails without restarting the listing

#### Scenario: expired cursors are pruned
ID: `stores.pg-sessions-cursor-pruned`
- WHEN Sessions or Purge executes after expiration
- THEN expired headers and items are removed

#### Scenario: cursor is bound to its owner
ID: `stores.pg-sessions-cursor-owner-scope`
- WHEN another owner submits the cursor
- THEN the adapter returns `types.ErrSessionForbidden`

#### Scenario: membership stays frozen
ID: `stores.pg-sessions-cursor-membership`
- WHEN a new session appears or existing filters change after materialization
- THEN continuation retains the initial membership

#### Scenario: invalid cursor is refused
ID: `stores.pg-sessions-cursor-invalid`
- WHEN a cursor is malformed, missing, or used with different filters
- THEN the adapter refuses it without restarting

### Requirement: Postgres journal reservation atomicity

`adapter/postgres.Journal.Reserve` and `ReserveTx` MUST make a first reservation atomic: a missing row's `SELECT ... FOR UPDATE` MUST NOT be the only guard. The adapter MUST insert with conflict handling and then read the row under `FOR UPDATE`, so concurrent first reservations of one `CallKey` yield exactly one `created=true` and the others reuse or refresh the winner's entry.

#### Scenario: concurrent first reservation
ID: `stores.pg-journal-concurrent-reserve`
- WHEN ten goroutines `Reserve` the same fresh `CallKey`
- THEN exactly one gets `created=true` and no caller receives a unique-violation error

### Requirement: Postgres operation-id uniqueness

`adapter/postgres.Runs.Start` MUST refuse a duplicate tenant-scoped `OperationID` with `stores.OperationExistsError{RunID}` naming the recorded run and MUST NOT surface a raw unique-violation when two sessions race the same operation id. The per-session advisory lock alone does not serialise this.

#### Scenario: operation id raced across sessions
ID: `stores.pg-operation-id-cross-session-race`
- WHEN two concurrent `Start` calls on different sessions use the same tenant `OperationID`
- THEN exactly one succeeds and the other gets `stores.OperationExistsError{RunID}` naming the recorded run

### Requirement: Postgres notice claim exclusivity

`adapter/postgres.Runs.Notices` MUST claim each pending notice with a guarded claim that succeeds for one dispatcher only, so two concurrent dispatchers never receive the same notice id. An unacknowledged claim MUST expire after `LeaseTTL` and return the notice to pending with its id unchanged.

#### Scenario: two dispatchers never share a notice
ID: `stores.pg-notice-claim-exclusive`
- WHEN two dispatchers call `Notices` at the same time
- THEN no notice id is returned to both, and after the claim expires the notice returns to pending with the same id

### Requirement: Postgres session cascade atomicity

`adapter/postgres.SessionLog` `Delete`, `Purge` and `EraseSessions` MUST delete dependents inside the session-delete transaction through package-local transaction methods on the concrete `Runs`, `Notes`, `Outputs` and `Checkpoints` stores. A rollback MUST preserve the session and all dependent rows, and a commit MUST remove them all.

#### Scenario: cascade commits or rolls back as one
ID: `stores.pg-cascade-in-one-transaction`
- WHEN a session delete removes dependent rows and the transaction rolls back
- THEN no dependent row is removed, and on commit the session and every dependent row are gone

### Requirement: Postgres session run discovery

`adapter/postgres.Runs` MUST implement `gohan.SessionRunFinder` through `RunForSession(ctx context.Context, sessionID string) (stores.Run, error)`. It MUST return the session's most recent run whose state is `Running` or `Resuming` and whose non-null lease expires strictly after the store clock, and MUST return `stores.ErrRunNotFound` when no row is eligible. `Suspended`, terminal, expired and null-lease runs MUST be excluded.

#### Scenario: live run found for another pod
ID: `stores.pg-run-for-session-active`
- WHEN a session holds a live run lease and a second pod looks it up
- THEN `RunForSession` returns that run and `Signal` on its id is accepted

#### Scenario: no live run
ID: `stores.pg-run-for-session-none`
- WHEN the only run for a session has a lease expired by the store clock
- THEN `RunForSession` returns `stores.ErrRunNotFound`

#### Scenario: ineligible run states are excluded
ID: `stores.pg-run-for-session-ineligible`
- WHEN the only run for a session is `Suspended`, terminal, or holds a null lease
- THEN `RunForSession` returns `stores.ErrRunNotFound`

### Requirement: Session run discovery

The core memory `Runs` implementation MUST implement
`SessionRunFinder.RunForSession(ctx, sessionID) (stores.Run, error)`. It MUST
return the session's active run, and MUST NOT return an inactive run. A run
is active only when its state is `Running` or `Resuming` and its lease is
live. It MUST return `stores.ErrRunNotFound` when the session has no active
run. `Cancel`, `Steer` and `ActiveRun` MUST reach a run another pod started
through this interface. The `Runs` port MUST NOT gain a method.

#### Scenario: memory finder reaches a cross-pod run
ID: `stores.run-for-session-cross-instance`
- WHEN a run is `Running` or `Resuming` with a live lease and was not started by this process
- THEN `RunForSession` returns it and `Cancel` reaches it

#### Scenario: finder refuses an absent run
ID: `stores.run-for-session-absent`
- WHEN the session has no active run
- THEN `RunForSession` returns `stores.ErrRunNotFound`

#### Scenario: finder refuses an inactive run
ID: `stores.run-for-session-terminal`
- WHEN the session's run is `Suspended`, terminal, or its lease expired
- THEN `RunForSession` returns `stores.ErrRunNotFound`

### Requirement: Finish reason reporting

A durable finish through the reason-recording path MUST report the run's
real `StopReason`. The terminal notice reason MUST be the recorded
`StopReason` when one was recorded. The store MUST NOT report
`StopCompleted` for a run that ended with `StopLimit`, `StopHandedOff` or
`StopCancelled`.

#### Scenario: notice carries the recorded reason
ID: `stores.finish-notice-recorded-reason`
- WHEN a run finishes through `FinishWithReason` with reason `StopLimit`
- THEN the terminal notice reason is `StopLimit`, not `StopCompleted`

#### Scenario: cancelled finish reports cancelled
ID: `stores.finish-notice-cancelled`
- WHEN a run finishes through `FinishWithReason` with reason `StopCancelled`
- THEN the terminal notice kind is `NoticeCancelled` and the reason word is `StopCancelled`

### Requirement: Every lease acquisition preserves session exclusivity

Start, resume and reclaim MUST atomically refuse another live run in the same session. Every successful acquisition MUST update session ownership consistently. Reclaim MUST accept only an expired stored `Running` or `Resuming` state.

#### Scenario: A suspended run resumes while a newer run owns the session
ID: `stores.resume-session-exclusive`
- WHEN run A is suspended and run B holds a live lease for the same session
- THEN resuming A returns `ErrRunActive` without changing either run

#### Scenario: Reclaim races another acquisition
ID: `stores.reclaim-session-exclusive`
- WHEN an older expired run is reclaimed while a newer run owns the session
- THEN reclaim returns `ErrRunActive` and the newer owner's lease remains usable

#### Scenario: Reclaim keeps the oldest lease and refuses the newer run
ID: `stores.reclaim-oldest-first-exclusive`
- WHEN the oldest expired run is reclaimed and a newer expired run in the same session is then reclaimed
- THEN the newer reclaim returns `ErrRunActive` and the reclaimed oldest run holds a live session lease

#### Scenario: Start is refused while a reclaimed lease is live
ID: `stores.start-refused-reclaimed-lease`
- WHEN `Start` is called for the same session while the reclaimed run holds the session lease
- THEN it returns `ErrRunActive`

#### Scenario: Start is refused while a resumed older run owns the session
ID: `stores.resume-older-owner-blocks-start`
- WHEN the older run resumes and holds the session lease
- THEN a `Start` for the same session returns `ErrRunActive`

#### Scenario: A stored terminal or suspended run is reclaimed
ID: `stores.reclaim-state-refused`
- WHEN a caller attempts to reclaim a stored terminal or suspended run
- THEN reclaim returns `ErrRunNotActive` without minting ownership

### Requirement: PostgreSQL is authoritative for production ownership time

Without an explicit testing clock override, PostgreSQL MUST compute and compare persisted lease and checkpoint expiry times using database time. A process clock difference MUST NOT change ownership, expiry, staleness or active-session discovery.

#### Scenario: Independent adapter instances have skewed process clocks
ID: `stores.postgres-ownership-database-time`
- WHEN independent adapter instances share a database and their process clocks differ
- THEN a live lease remains owned and an unexpired checkpoint remains resumable by database time

### Requirement: Checkpoint lifetime is independent store policy

The driver MUST NOT derive checkpoint expiry from its execution lease. A positive configured checkpoint TTL MUST be measured by the store at creation when explicit expiry is absent. Zero TTL MUST retain no automatic expiry. Explicit expiry MUST remain unchanged. Negative policy MUST be refused before writing.

#### Scenario: A healthy execution outlives its initial lease
ID: `stores.checkpoint-lifetime-independent`
- WHEN an execution heartbeats past its initial lease deadline and then suspends
- THEN the fresh checkpoint remains usable until its own configured expiry

#### Scenario: Explicit and absent checkpoint expiry
ID: `stores.checkpoint-lifetime-policy`
- WHEN checkpoints are written with explicit expiry, a positive store TTL or neither
- THEN explicit expiry is preserved, configured TTL is measured from store time, and the absence of both leaves expiry unset

### Requirement: Optional checkpoint resume port

The optional `CheckpointRuns` port MUST expose this normative method set. Existing frozen `Runs` methods MUST remain unchanged.

```go
type CheckpointRuns interface {
	SuspensionToken(context.Context, string) (types.ResumeToken, error)
	ResumingCheckpoint(context.Context, string, types.ResumeToken, time.Duration) (Lease, error)
}
```

#### Scenario: A token-aware store supports the optional port
ID: `stores.optional-checkpoint-resume-port`
- WHEN the concrete memory or PostgreSQL store is checked for token-aware resume support
- THEN it implements the optional port without changing the frozen run port

### Requirement: Optional checkpoint resume is token-fenced

The optional `CheckpointRuns` surface MUST expose the current suspended token and atomically acquire a resume lease only for that token with exclusive session ownership. Frozen `Runs` methods MUST remain unchanged. A mismatched token MUST leave the current suspension untouched.

#### Scenario: An old token is used after a newer suspension
ID: `stores.resume-current-token-fenced`
- WHEN a run currently waits on token B and a caller requests a lease for token A
- THEN the request returns `ErrTokenMismatch` and B remains current

### Requirement: Recovery discovery limits eligible current checkpoints

Consumed-checkpoint discovery MUST filter for a current unexpired consumed token whose run is still suspended before applying its limit. Preempted discovery MUST filter the actual current checkpoint reason before its limit. Empty resume input MUST NOT determine consumption.

#### Scenario: Completed checkpoints precede a recoverable checkpoint
ID: `stores.resume-ready-filter-before-limit`
- WHEN older consumed checkpoints belong to completed runs and the discovery limit is one
- THEN a newer eligible consumed checkpoint is returned

#### Scenario: Human approval precedes a preempted checkpoint
ID: `stores.preempted-filter-before-limit`
- WHEN an older human approval and a newer preempted checkpoint are suspended and the limit is one
- THEN preempted discovery returns the preempted run only

#### Scenario: Re-entrant run port does not deadlock discovery
ID: `stores.resume-ready-reentrant-run-port`
- WHEN the run port re-enters the checkpoint store during `ResumeReady`
- THEN discovery returns the consumed checkpoint without deadlocking

#### Scenario: Preempted ranking precedes decoding
ID: `stores.preempted-rank-before-decode`
- WHEN a corrupt record is among the preempted candidates
- THEN `Preempted` returns the oldest-first ranking with ties broken by run id when the limit excludes the corrupt record, and returns a decode error when the limit includes it

### Requirement: Per-run cost recording

`Runs` SHALL expose an optional surface that records the spend a run charged before it finishes, so a sub-flow's own cost is readable from its run row. Core MUST discover it by type assertion and MUST NOT fail when a store lacks it: that run keeps `Cost` at zero and the tree total stays authoritative.

```go
type RunCostRecorder interface {
	RecordCost(ctx context.Context, l Lease, cost float64) error
}
```

#### Scenario: run cost recorded when the store supports it
ID: `stores.run-cost-optional`
- WHEN one run finishes on a store implementing `RunCostRecorder` and another on a store without it
- THEN the first run's row reports the recorded spend and the second row keeps zero without an error

### Requirement: Redis contract

`adapter/redis` MUST provide quota pool limiting, class admission, `pool/day` breaker and `EventLog` behind `ModelProfile.QuotaPool` in module `github.com/victorzhuk/gohan/adapter/redis`. Windows, expiry, staleness MUST use Redis server time; callers MUST pass durations. Stores MUST match memory and Postgres port methods and refusals and pass `storetest`. It MUST NOT implement SessionLog/Checkpoints/Journal/Runs/AuditLog/OutputStore/NotesStore/RedactionMaps.

#### Scenario: limiter decided on store clock
ID: `stores.redis-limiter-store-clock`
- WHEN a caller whose wall clock is minutes ahead admits against the pool
- THEN the window boundaries and refill come from Redis server time and the caller cannot extend or shorten its share

#### Scenario: admission class share
ID: `stores.redis-admission-class-share`
- WHEN the pool is near saturation and `Interactive`, `Agentic` and `Batch` requests contend
- THEN `Interactive` is admitted from its reserved share first and `Batch` requests are deferred while queue-wait exceeds the threshold

#### Scenario: pool budget window reset
ID: `stores.redis-pool-budget-window-reset`
- WHEN `pool/day` spend reaches its limit and the UTC day passes on Redis server time
- THEN admission resumes without any caller action and spend restarts from zero for the new window

#### Scenario: shared eventlog reattach
ID: `stores.redis-eventlog-shared-reattach`
- WHEN a client of another service reconnects with `Last-Event-ID` against the shared Redis `EventLog`
- THEN events after that `Seq` replay with no gap and no duplicate, and a cursor older than retained entries is refused instead of served with a gap

#### Scenario: eventlog compaction idempotent
ID: `stores.redis-eventlog-compaction-idempotent`
- WHEN the same compaction publication is appended twice to the Redis `EventLog`
- THEN the first append commits and the second returns the original version appending nothing

#### Scenario: conformance parity
ID: `stores.redis-conformance-parity`
- WHEN the `storetest` suites run against the redis implementations with testcontainers
- THEN the suites pass with the same sentinel refusals and envelope shapes as the memory and Postgres implementations
