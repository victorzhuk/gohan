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
	SessionID      string
	Flow           string
	Backend        string
	BackendVersion string
	Reason         SuspendReason
	Originator     Principal
	Data           []byte
	Child          ResumeToken
	Workspace      WorkspaceRef
	ExpiresAt      time.Time
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
	RunID   string
	Expires time.Time
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
}

type SessionIndex interface {
	Sessions(ctx context.Context, owner SessionOwner, q SessionQuery) ([]SessionMeta, string, error)
	UpdateSession(ctx context.Context, sessionID string, p SessionPatch) error
}

func (s *Stack) Sessions(ctx context.Context, q SessionQuery) ([]SessionMeta, string, error)
func (s *Stack) UpdateSession(ctx context.Context, sessionID string, p SessionPatch) error

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
	Confidence   float64
	Approver     string
	Verdict      string
	Outcome      Outcome
	ResultSHA    string
	ResultBytes  int
	Stage        GuardStage
	Model        string
	ModelVersion string
	ManifestHash string
	PrevHash     string
	Hash         string
}

type AuditLog interface {
	Append(ctx context.Context, r AuditRecord) error
	Read(ctx context.Context, sessionID string) iter.Seq2[AuditRecord, error]
	Purge(ctx context.Context, tenant string, olderThan time.Time) (int, error)
}
```

Contracts:

- `Append` with a stale version returns `ErrVersionConflict`; the harness reloads and retries once for pure appends, otherwise surfaces the error.
- `Consume` is atomic; the second caller gets `ErrTokenConsumed`; expired checkpoints return `ErrTokenExpired`.
- `Reserve` returns `created=true` for a new reservation. Existing `Completed` → decorator returns the recorded result without executing (`ToolFinished.Replayed=true`). Existing `Reserved` → outcome unknown; the tool re-executes with the same pinned key.
- `Fingerprint = hash(tool name, canonical JSON of args)`. Before creating a new entry the decorator calls `ByFingerprint`; if an entry whose `Result.Outcome` is `Unknown` (state `Reserved`) exists in the same session, the new entry **inherits its `Key`** (key pinning per intent). If a `Completed`/`Succeeded` entry exists for a `SideEffect` fingerprint, the call proceeds with a fresh key but `gohan.tool.repeat_intent` increments and the loop detector counts it.
- `AuditLog.Append` is called only from chain steps and the harness (gate, scope check, journal, model step, guards, suspend/resume, limits, run start/finish). Tools, models, context providers and user code have no handle to it. Records carry checksums and sizes of args and results, never content. `PrevHash`/`Hash` form an optional per-session hash chain (postgres implementation on by default). Retention is per tenant; default 6 months; `Purge` is the only delete.
- `Journal` results are needed for replay during a run's lifetime only: after `Runs.Finish` they expire (default 24 h); the audit record keeps `ResultSHA` and `ResultBytes`.
- `stack.Reconstruct(ctx, sessionID)` joins `AuditLog` and `SessionLog` into an ordered decision trail: who ran what, for whom, which tools were allowed/denied/asked, who approved, what version of model/prompts/toolset was in force.
- `Runs.Start` with a non-empty `OperationID` is unique per tenant; a duplicate returns `ErrOperationExists{RunID}` and the caller reads the stored result via `ByOperation` + `ResultRef` (an `OutputStore` ref; it is retained by the Conversation tier of the owning session). A deliberate re-execution needs a new operation ID.
- `Suspend` releases the lease and records the token; the run is not recoverable while `Suspended`; its retention is the checkpoint's `ExpiresAt`. `Consume(t, in)` stores the resume input atomically with consumption and `Resuming(runID)` takes a fresh lease; a crash after that point leaves a `Resuming` run whose input `Recover` reads back with `PendingInput` and re-drives. Only `Running` and `Resuming` runs are ever reclaimed.
- `Runs.Start` fails with `ErrRunActive` if the session has an unexpired lease. Leases are refreshed by the harness at least every `HeartbeatEvery`, through the run's heartbeat goroutine and, where the store supports it, the per-turn writes (`recovery`). `Stale` lists runs whose heartbeat is older than `staleAfter` by the store's clock and state `Running` or `Resuming`, so a run left `Resuming` by a crash after `Resuming(runID)` is reclaimed like a stale `Running` one. `PreemptedLister` is an optional interface on `Runs` (`docs/design/compatibility.md`): when the store implements it, `Recover` resumes preempted runs first; when it does not, preempted runs wait for a client `Continue()` or, if none comes, expire with their checkpoint. `Preempted` lists `Suspended` runs whose checkpoint reason is `Preempted`, oldest first. The core memory store and `adapter/postgres` implement it. `Reclaim` atomically takes over a stale run (exactly one reaper wins).
- **Forking.** `Stores.ForkSession(from, upTo)` creates a new session whose history is the parent's prefix through message `upTo` (message IDs, origins and compaction blocks preserved), with the same `SessionOwner`, `History.ForkedFrom` set, an audit record `session_fork` and `gohan.session.forked{tenant}`. It fails with `ErrRunActive` while the parent has a live lease; a suspended parent forks (its token stays with the parent). Inheritance: owner yes; session grants no (the child starts with none); notes and shared state copied as of fork time (shared state at version 1 with a full-snapshot `StateChanged`); journal, checkpoints and runs no; subject memory is owner-scoped and unchanged; taint is computed over the child's own history. A store may implement copy-on-write with a parent pointer, but a fork is not a dependent record: `DeleteSession(parent)` leaves it readable, and `EraseSubject` covers parent and forks alike. `stack.Reconstruct(child)` prepends the parent's trail through `UpTo` with a fork marker. Assembly is prefix-stable, so a fork's first model call reuses the parent's provider cache.
- **Session index.** `SessionIndex` is an optional interface on `SessionLog` (`docs/design/compatibility.md`; the core memory store and `adapter/postgres` implement it). `Append` maintains `Messages` and `LastActivity` (store clock); `ForkSession`, `subflows` and `release` set `Kind`; `Sessions` lists by `LastActivity` descending with cursor pagination (`Limit` default 25, max 200) and, with an empty `Kinds`, returns only `SessionPrimary` and `SessionFork`; `Archived == nil` means not archived. `Stack.Sessions` scopes the query to `PrincipalFrom(ctx)`'s owner (or a tenant with `session:read`); `Stack.UpdateSession` is owner-checked like `Send`, and a title set by a user sets `TitleLocked`. Without the index, `Stack.Sessions` returns `ErrSessionIndexRequired`. Retention: `Purge` skips `Pinned` sessions and applies `ArchivedRetention` (default 180 days) to archived ones; `DeleteSession` and `EraseSubject` remove index rows; `Reconstruct` reads archived sessions.
- **Two clocks.** Store time is authoritative for every instant that is persisted and later compared: lease `Expires`, staleness, `Checkpoint.ExpiresAt` (enforced at `Consume`), journal expiry, operation retention, catalog `ttlMs`. Stores compute and compare these from their own clock (`now()` in Postgres, server `TIME` in Redis, the process clock in `core/memory`); callers pass durations (`Start(ttl)`, `Reclaim(ttl)`, `Stale(staleAfter)`), never instants, so a pod with a skewed clock cannot shorten or extend anyone's lease. `Lease.Expires` is store time and informational: the harness heartbeats on `ttl/3` and never compares it. Retention sweeps (`Purge`, `Expire(olderThan)`) keep instants because they are coarse and operator-driven. Harness time is monotonic-only, for budgets and timeouts (`RunLimits.MaxWallClock`, `ToolSpec.Timeout`, `ModelProfile.Timeout`, `HeartbeatEvery`, `ReaperEvery`) via `time.Since` and `context.WithTimeout`; no rule compares a harness instant to a stored one. `EventMeta.Time` and audit timestamps are harness wall time, informational; `Seq` orders. `WakeAt` is UTC read by the scheduler; `Sunset` is UTC read at `Build`. `memory.New(memory.WithNow(func() time.Time))` lets tests move the memory stores' clock; there is no clock port in core.
- Every stored shape (`Message`, `State`, `Checkpoint`, `AuditRecord`, `Event`, journal `Entry`, `Run`, `RedactionMap`) carries `SchemaVersion`. Core keeps a registry of pure upcasters N→N+1 (no clocks, lookups or randomness) applied on read; stores never rewrite records in place; additive changes need no upcaster. `storetest.Schemas` replays recorded fixtures from every released schema version through the current readers.
- `Checkpoint.Originator` is a `Principal` and therefore carries no token by type: the originator's token stays in `ctx` (`Credential`) and is re-issued on resume by `CredentialSource` (`identity`).
- Keys are tenant-scoped by implementations (tenant from `RunInfo`).
- `adapter/postgres` adds `func (j *Journal) CompleteTx(ctx context.Context, tx pgx.Tx, k gohan.CallKey, res gohan.ToolResult) error`.
- **Postgres schema.** Every table lives in schema `gohan` (or the name given by `postgres.WithSchema`, so two stacks may share one database) as `<store>_<record>`; users never alter these tables and extend by their own tables keyed on gohan ids; `sqlc` queries are the only SQL in the adapter. Migrations are embedded goose SQL files applied by `postgres.Migrate(ctx, pool, MigrateOptions{Schema, TargetVersion})` under an advisory lock (concurrent pods serialise) and by the `gohan-postgres migrate` command (JSON output when stdout is not a TTY). The intended place is the deploy pipeline or a pre-install hook with a DDL role; `postgres.WithAutoMigrate()` is for development and `Build` refuses it when `CostTags.Environment` is `production`. DDL versions are separate from record `SchemaVersion`s. **Skew rule:** each release runs against the previous release's schema and the next one — added columns are nullable or defaulted and not read until the following release, drops happen two releases after the last reader; `postgres.New` reads `gohan.schema_version` and fails with `ErrSchemaTooOld` below its `MinSchema`, and `Build` warns `gohan.postgres.schema_ahead` when the schema is newer than the code. Time-partitioned tables (`journal`, `events`, `audit`, `outputs`, `session_log`) have partitions created 14 days ahead by `Migrate` and thereafter by `postgres.Maintain(ctx)` on the reaper cadence; a write into a missing partition fails with `ErrPartitionMissing`, never a default partition. `storetest.Migrations` runs every store suite on a fresh install at head and on an install migrated step by step from the first version, and requires both schemas to be identical.


## Requirements

### Requirement: Store contracts

`storetest.SessionLog`, `storetest.Checkpoints`, `storetest.Journal`, `storetest.Runs`, `storetest.AuditLog` SHALL pass for the core memory implementations and `adapter/postgres` (testcontainers).

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

### Requirement: Optional interfaces

#### Scenario: session index optional
ID: `stores.session-index-optional`
- WHEN a `SessionLog` implementation does not implement `SessionIndex`
- THEN `Stack.Sessions` returns `ErrSessionIndexRequired` and every other operation works unchanged

#### Scenario: preempted lister fallback
ID: `stores.optional-preempted-lister-fallback`
- WHEN a `Runs` implementation does not implement `PreemptedLister` and a run was preempted
- THEN `Recover` completes without error, the run stays `Suspended` and `Resume(token, Continue())` still works

### Requirement: Notice outbox

`Finish` and `Suspend` append a `RunNotice` (`streams`) in the same transaction as the state change; a crash after the transaction never loses the notice and a crash before it never emits one. `Notices` claims up to `limit` pending notices for the calling dispatcher for `LeaseTTL` (another pod's `Notices` skips them until the claim expires), `AckNotice` removes one; an unacknowledged claim returns to pending. Notices belong to the session's Conversation tier and are purged and deleted with it. Postgres: `run_notices`; memory: an in-process queue.

#### Scenario: notice written with finish
ID: `stores.notice-written-with-finish`
- WHEN the pod crashes right after `Runs.Finish` commits and before any delivery
- THEN another pod's dispatcher claims the notice through `Notices` and delivers it; an unacknowledged claim returns to pending and a redelivery carries the same notice id, which is the consumer's idempotency key (`streams`)

### Requirement: Run mailbox

Every run has a mailbox in `Runs`. `Signal` appends for a run whose state is `Running` and fails with `ErrRunNotActive` otherwise, and with `ErrMailboxFull` past `MaxPendingSignals`; `Drain` returns and removes the pending signals in arrival order for the lease holder only. `Cancel` is `Signal{SignalCancel}`. `Finish` is atomic with the mailbox: it fails with `ErrSignalsPending` when `SignalSteer` entries remain, and the runtime drains them and runs one more turn, which counts against `MaxTurns`; when that limit is already reached the run ends with `Done(StopLimit)` instead; pending `SignalCancel` never blocks `Finish`. Signals are not journal entries; a steer becomes part of history only when the runtime appends it (`runtime`). The memory store and Postgres (`run_signals`, purged with the run's session under the Conversation tier) implement the mailbox; `Signal` from any pod reaches the holder on its next `Drain`.

#### Scenario: cancel signal crosses pods
ID: `stores.signal-cancel-cross-pod`
- WHEN pod A calls `Signal(runID, SignalCancel)` while pod B holds the lease
- THEN pod B's next `Drain` returns the cancel and the run stops at that safe point

#### Scenario: mailbox full
ID: `stores.mailbox-full`
- WHEN eleven steers are posted before the holder drains
- THEN the eleventh returns `ErrMailboxFull` and the first ten are delivered in order

### Requirement: Session index

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

#### Scenario: feedback cascades on erase
ID: `stores.feedback-cascades-on-erase`
- WHEN `EraseSubject` or `DeleteSession` runs for a session with feedback rows
- THEN `FeedbackStore.Feedback(sessionID)` returns nothing and audit keeps the `feedback` records with checksums only

### Requirement: Retention and legal hold

Retention is one `RetentionPolicy` per stack (`agent.WithRetention`; default 90 days / 180 days / 2 years), overridable per tenant through the optional `RetentionSource`. Three tiers, each with its own clock by the store's time: **Conversation** covers everything keyed by a session — `SessionLog`, `EventLog`, `OutputStore` and its blobs, `Checkpoints`, `Journal`, the run mailbox, `FeedbackStore` and `RedactionMap` — measured from `SessionMeta.LastActivity`; a session with a `Running` run never ages, and a `Suspended` one until its checkpoint's `ExpiresAt` passes; a session whose suspended checkpoint has expired ages normally, `Recover` no longer resumes that run and `Consume` returns `ErrTokenExpired`. `Pinned` sessions never age, and an archived session ages by `ArchivedRetention` (default 180 days, `SessionIndex`) instead of the Conversation tier. **Working** covers notes and subject memory, measured from the entry's last write. **Audit** covers `AuditLog` and is never shorter than `Conversation`; `Build` fails on such a policy. `Stack.Maintain` runs every store's purge tenant by tenant under the tenant's policy, skips `Pinned`, `Archived` (their own threshold, `stores.purge-respects-pinned-and-archived`) and held sessions, calls `postgres.Maintain`, and appends one `purge` audit record per tenant with per-store counts; `gohan-maintain` (JSON when stdout is not a TTY) is the cron entry point. `Conversation: 0` is zero retention: the session's Conversation-tier records are deleted at `Finish` and only audit checksums remain; `agent.Ephemeral()` sets it for a flow.

**Legal hold.** `SessionMeta.Hold` is a hold id (empty = none) set through `UpdateSession` by a principal of the owner's tenant with `session:hold`; each change is audited (`hold_set`, `hold_cleared`). While set, `Maintain` skips the session and counts it in `Held`, `DeleteSession` returns `ErrSessionHeld`, zero retention does not apply, and `EraseSubject` erases everything else and lists the session in `EraseReport.Held`. Holds live in session metadata; a host that needs a hold registry drives `UpdateSession` from it.

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

#### Scenario: delete cascades
ID: `stores.delete-cascades`
- WHEN `Stores.DeleteSession` is called for a session with checkpoints, journal entries, outputs, notes and events
- THEN the session, its child and shadow sessions, and every dependent record are removed from all stores; audit records remain with content checksums only; a session under a hold fails with `ErrSessionHeld` first, and otherwise a live lease makes the call fail with `ErrRunActive`

### Requirement: Audit trail

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
