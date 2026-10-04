package gohan

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

func stackTestStores(t *testing.T) (*Stack, *stores.MemorySessionLog) {
	t.Helper()
	s, err := Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	log := stores.NewMemorySessionLog(
		stores.WithMemorySessionClock(func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) }),
		stores.WithSessionPrincipals(types.PrincipalFrom),
	)
	s.stores = stores.Stores{SessionLog: log}
	return s, log
}

func TestStackStores(t *testing.T) {
	t.Run("accessors return the injected ports", func(t *testing.T) {
		s, log := stackTestStores(t)
		if s.Stores().SessionLog != log {
			t.Fatal("Stores does not carry the injected session log")
		}
	})

	t.Run("owner mismatch is refused", func(t *testing.T) {
		s, _ := stackTestStores(t)
		ctx := WithPrincipal(context.Background(), types.Principal{Tenant: "t-a", Subject: "u1"})
		if _, err := s.Stores().SessionLog.Append(ctx, "sess-1", 0, types.Message{}); err != nil {
			t.Fatalf("append: %v", err)
		}
		other := WithPrincipal(ctx, types.Principal{Tenant: "t-a", Subject: "u2"})
		got, _, err := s.Sessions(other, stores.SessionQuery{})
		if err != nil {
			t.Fatalf("sessions: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("u2 saw %d of u1's sessions", len(got))
		}
		own, _, err := s.Sessions(ctx, stores.SessionQuery{})
		if err != nil || len(own) != 1 {
			t.Fatalf("owner listing: %d rows, err %v", len(own), err)
		}
	})

	t.Run("fork is an optional interface", func(t *testing.T) {
		if _, ok := any(stores.NewMemorySessionLog()).(stores.SessionForker); !ok {
			t.Fatal("memory session log does not satisfy SessionForker")
		}
		var bare stores.SessionLog = bareSessionLog{}
		if _, ok := bare.(stores.SessionForker); ok {
			t.Fatal("a log without Fork must not satisfy SessionForker")
		}
		s, log := stackTestStores(t)
		ctx := WithPrincipal(context.Background(), types.Principal{Tenant: "t-a", Subject: "u1"})
		if _, err := log.Append(ctx, "sess-1", 0, types.Message{}); err != nil {
			t.Fatalf("append: %v", err)
		}
		hist, err := log.Load(ctx, "sess-1")
		if err != nil || len(hist.Messages) == 0 {
			t.Fatalf("load: %v", err)
		}
		forker, ok := s.Stores().SessionLog.(stores.SessionForker)
		if !ok {
			t.Fatal("injected log does not satisfy SessionForker")
		}
		child, err := forker.Fork(ctx, "sess-1", hist.Messages[0].ID)
		if err != nil {
			t.Fatalf("fork: %v", err)
		}
		childHist, err := log.Load(ctx, child)
		if err != nil {
			t.Fatalf("load fork: %v", err)
		}
		if childHist.ForkedFrom == nil || childHist.ForkedFrom.SessionID != "sess-1" {
			t.Fatalf("fork provenance: %+v", childHist.ForkedFrom)
		}
	})

	t.Run("sessions without an index are refused", func(t *testing.T) {
		s, _ := stackTestStores(t)
		s.stores = stores.Stores{SessionLog: bareSessionLog{}}
		_, _, err := s.Sessions(WithPrincipal(context.Background(), types.Principal{Tenant: "t-a", Subject: "u1"}), stores.SessionQuery{})
		if !errors.Is(err, types.ErrSessionIndexRequired) {
			t.Fatalf("missing index: %v", err)
		}
	})
}

type bareSessionLog struct{}

func (bareSessionLog) Load(context.Context, string) (stores.History, error) {
	return stores.History{}, nil
}

func (bareSessionLog) Append(context.Context, string, int64, ...types.Message) (int64, error) {
	return 0, nil
}

func (bareSessionLog) Purge(context.Context, time.Time) (int, error) { return 0, nil }

func (bareSessionLog) Delete(context.Context, string) error { return nil }
