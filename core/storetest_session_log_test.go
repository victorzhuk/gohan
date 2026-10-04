package gohan

import (
	"context"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/storetest"
)

// sessionLogAdapter narrows the memory store to the conformance suite's
// port shape. The suite itself must not know the concrete store.
type sessionLogAdapter struct {
	inner *stores.MemorySessionLog
}

func (a sessionLogAdapter) Load(ctx context.Context, sessionID string) (storetest.SessionHistory, error) {
	h, err := a.inner.Load(ctx, sessionID)
	if err != nil {
		return storetest.SessionHistory{}, err
	}
	return storetest.SessionHistory{Messages: h.Messages, Version: h.Version}, nil
}

func (a sessionLogAdapter) Append(ctx context.Context, sessionID string, expectedVersion int64, msgs ...types.Message) (int64, error) {
	return a.inner.Append(ctx, sessionID, expectedVersion, msgs...)
}

func (a sessionLogAdapter) Purge(ctx context.Context, olderThan time.Time) (int, error) {
	return a.inner.Purge(ctx, olderThan)
}

func (a sessionLogAdapter) Delete(ctx context.Context, sessionID string) error {
	return a.inner.Delete(ctx, sessionID)
}

func TestStoretestSessionLogBind(t *testing.T) {
	storetest.SessionLog(t, func(ctx context.Context) (storetest.SessionLogStore, error) {
		p := types.Principal{Subject: "alice", Tenant: "t1"}
		s := stores.NewMemorySessionLog(stores.WithSessionPrincipals(func(context.Context) (types.Principal, bool) {
			return p, true
		}))
		return sessionLogAdapter{inner: s}, nil
	})
}
