package stores

import (
	"context"
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
}

// SessionIndex is the optional listing surface on a SessionLog.
type SessionIndex interface {
	Sessions(ctx context.Context, owner types.SessionOwner, q SessionQuery) ([]SessionMeta, string, error)
	UpdateSession(ctx context.Context, sessionID string, p SessionPatch) error
}
