package stores

import (
	"context"
	"encoding/json"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// History is the full recorded conversation of one session.
type History struct {
	Owner      types.SessionOwner
	Messages   []types.Message
	Version    int64
	ForkedFrom *ForkPoint
}

// ForkPoint names the parent session and the last parent message a fork
// carries.
type ForkPoint struct {
	SessionID string
	UpTo      string
}

// SessionLog persists message history per session. The owner is recorded at
// the first append and never changes; Message.ID values are assigned here and
// stay stable across replay, compaction and forks.
type SessionLog interface {
	Load(ctx context.Context, sessionID string) (History, error)
	Append(ctx context.Context, sessionID string, expectedVersion int64, msgs ...types.Message) (int64, error)
	Purge(ctx context.Context, olderThan time.Time) (int, error)
	Delete(ctx context.Context, sessionID string) error
}

// SessionForker is the optional fork surface on a SessionLog. A port grows
// only through an optional interface discovered by type assertion; a log
// without it cannot fork.
type SessionForker interface {
	Fork(ctx context.Context, from, upTo string) (string, error)
}

// SessionDependent is a store keyed by session id that must follow the
// session log's cascade and fork operations. Consumer-owned: each dependent
// store adapter satisfies it and registers through an option.
type SessionDependent interface {
	DeleteSessionDependents(ctx context.Context, sessionID string) error
	CopySessionDependents(ctx context.Context, from, to string) error
}

// SessionLeaseHolder answers whether a session currently holds a live run
// lease. Consumer-owned: the runs store adapter satisfies it.
type SessionLeaseHolder interface {
	SessionLeaseActive(ctx context.Context, sessionID string) bool
}

type SessionKind int

const (
	SessionPrimary SessionKind = iota
	SessionFork
	SessionChild
	SessionShadow
)

// SessionMeta is one row of the session index.
type SessionMeta struct {
	ID           string
	Owner        types.SessionOwner
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
	Handoff      types.HandoffContext
}

type SessionControl int

const (
	ControlAgent SessionControl = iota
	ControlHandoffRequested
	ControlHuman
)

// SessionQuery selects a page of the index. Archived == nil means not
// archived; an empty Kinds list means the default kinds (primary and fork).
// After is the opaque cursor from the previous page.
type SessionQuery struct {
	Archived *bool
	Control  *SessionControl
	Kinds    []SessionKind
	After    string
	Limit    int
}

// SessionPatch carries optional metadata updates; a nil field leaves the
// value unchanged.
type SessionPatch struct {
	Title    *string
	Archived *bool
	Pinned   *bool
	Hold     *string
	Handoff  *types.HandoffContext
}

// SessionIndex is the optional listing surface on a SessionLog.
type SessionIndex interface {
	Sessions(ctx context.Context, owner types.SessionOwner, q SessionQuery) ([]SessionMeta, string, error)
	UpdateSession(ctx context.Context, sessionID string, p SessionPatch) error
}

// SessionStateMeta is the optional shared-state metadata surface on a
// SessionLog. The shared state lives outside the message history so a
// resume on another pod reads it from the session row alone; the version
// is the state's own monotonic counter, independent of history versions.
type SessionStateMeta interface {
	SharedStateMeta(ctx context.Context, sessionID string) (json.RawMessage, int64, error)
	SetSharedStateMeta(ctx context.Context, sessionID string, expectedVersion int64, value json.RawMessage) (int64, error)
}

// WorkspaceMeta is the durable per-session workspace pointer. Generation is
// the persisted compare-and-set token; zero denotes absent metadata. Owner
// records the run lease the current snapshot was published under.
type WorkspaceMeta struct {
	Ref        types.WorkspaceRef
	Generation int64
	Owner      types.WorkspaceOwner
}

// SessionWorkspaceMeta is the optional durable workspace metadata surface on
// a SessionLog. Placement follows the optional-interface rule: the surface
// lives here, the coordinator implementation in std/sandbox, and the store
// implementations beside their SessionLog. A log without the surface cannot
// persist a durable workspace pointer and a PerSession build wiring it is
// refused.
//
// SetWorkspaceMeta applies the refusal precedence in one atomic step:
// authorize the session write, compare the generation token (and the ref
// when the incoming generation equals the stored one), validate the owner
// lease proof, then assign. A token mismatch reports
// types.ErrVersionConflict; a failed owner proof reports
// types.ErrRunNotActive; a store with no wired owner guard refuses every
// write.
type SessionWorkspaceMeta interface {
	WorkspaceMeta(ctx context.Context, sessionID string) (WorkspaceMeta, bool, error)
	SetWorkspaceMeta(ctx context.Context, sessionID string, expectedGeneration int64, m WorkspaceMeta) (int64, error)
	ImportWorkspaceMeta(ctx context.Context, sessionID string, owner types.WorkspaceOwner, legacy types.WorkspaceSnapshot) (types.WorkspaceSnapshot, error)
}

// WorkspaceCleanupRecord is the durable ownership association for one
// acquired live sandbox resource. ResourceRef identifies the exact live
// resource Open returned, not the ref Snapshot published. A session holds
// one record per acquired resource: a successor never overwrites its
// predecessor's record. The logical key is the tenant scope, SessionID,
// Owner.RunID, Owner.LeaseGeneration and ResourceRef together.
type WorkspaceCleanupRecord struct {
	SessionID   string
	Owner       types.WorkspaceOwner
	ResourceRef types.WorkspaceRef
}

// SessionWorkspaceCleanup is the optional cleanup-record surface on a
// SessionLog. It carries the ownership records the sandbox coordinator
// sweeps and the atomic claim that excludes a cleanup from a live session:
// a claim is refused while the session holds a live lease, and an accepted
// claim refuses a concurrent acquisition. Claim bookkeeping never becomes
// a second snapshot version.
type SessionWorkspaceCleanup interface {
	// RecordWorkspaceCleanup persists the ownership record for one
	// acquired live resource. The owner proof must validate.
	RecordWorkspaceCleanup(ctx context.Context, sessionID string, owner types.WorkspaceOwner, ref types.WorkspaceRef) error
	// ValidateWorkspaceAcquire atomically validates the owner proof and
	// refuses while a cleanup claim blocks the session's acquisition.
	ValidateWorkspaceAcquire(ctx context.Context, sessionID string, owner types.WorkspaceOwner) error
	// CleanupCandidates reads at most limit durable records.
	CleanupCandidates(ctx context.Context, limit int) ([]WorkspaceCleanupRecord, error)
	// ClaimWorkspaceCleanup atomically claims the exact record: a live
	// session lease or an existing claim reports claimed == false and
	// changes nothing.
	ClaimWorkspaceCleanup(ctx context.Context, rec WorkspaceCleanupRecord) (bool, error)
	// CompleteWorkspaceCleanup removes the exact record and its claim after
	// a successful close. Snapshot metadata is retained.
	CompleteWorkspaceCleanup(ctx context.Context, rec WorkspaceCleanupRecord) error
	// AbortWorkspaceCleanup releases the claim after a failed close and
	// keeps the exact record for the next sweep.
	AbortWorkspaceCleanup(ctx context.Context, rec WorkspaceCleanupRecord) error
}

// SessionBlobs is the optional blob-ledger metadata surface on a SessionLog.
// It maps a blob ref to the provider that issued an id for it, outside the
// message history, so a resume on another pod reads the ledger from the
// session row alone. A log without the surface cannot persist ids and
// assembly re-uploads each turn.
type SessionBlobs interface {
	BlobProviderID(ctx context.Context, sessionID, ref string) (provider, id string, ok bool)
	SetBlobProviderID(ctx context.Context, sessionID, ref, provider, id string) error
}
