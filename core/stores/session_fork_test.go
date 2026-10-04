package stores

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func forkTestStore(t *testing.T, opts ...MemorySessionOption) *MemorySessionLog {
	t.Helper()
	base := []MemorySessionOption{WithSessionPrincipals(func(ctx context.Context) (types.Principal, bool) {
		return types.Principal{
			Subject: "user-1",
			Tenant:  "acme",
			Scopes:  []string{scopeSessionRead, scopeSessionWrite},
		}, true
	})}
	return NewMemorySessionLog(append(base, opts...)...)
}

func forkMsgs(n int) []types.Message {
	msgs := make([]types.Message, 0, n)
	for i := range n {
		var blocks []types.Block
		if i == 2 {
			origin := types.Origin{Kind: types.OriginSystem, Name: "compact"}
			blocks = []types.Block{types.Compaction{
				BlockBase:  types.BlockBase{Origin: origin, Seq: int64(i)},
				CoversUpTo: 2,
				Kind:       types.CompactionText,
				Summary:    []types.Block{types.Text{BlockBase: types.BlockBase{Origin: origin}, Text: "so far"}},
			}}
			msgs = append(msgs, types.Message{Role: types.RoleAssistant, Blocks: blocks})
			continue
		}
		okind, orole := types.OriginUser, types.RoleUser
		if i%2 == 1 {
			okind, orole = types.OriginModel, types.RoleAssistant
		}
		blocks = []types.Block{types.Text{
			BlockBase: types.BlockBase{Origin: types.Origin{Kind: okind, Name: "n" + string(rune('a'+i))}, Seq: int64(i)},
			Text:      "msg",
		}}
		msgs = append(msgs, types.Message{Role: orole, Blocks: blocks, Meta: map[string]any{"i": i}})
	}
	return msgs
}

func TestSessionFork(t *testing.T) {
	ctx := context.Background()

	t.Run("stores.fork-prefix", func(t *testing.T) {
		s := forkTestStore(t)
		if _, err := s.Append(ctx, "parent", 0, forkMsgs(10)...); err != nil {
			t.Fatalf("append: %v", err)
		}
		parent, err := s.Load(ctx, "parent")
		if err != nil {
			t.Fatalf("load parent: %v", err)
		}
		upTo := parent.Messages[6].ID

		childID, err := s.Fork(ctx, "parent", upTo)
		if err != nil {
			t.Fatalf("fork: %v", err)
		}
		if childID == "" || childID == "parent" {
			t.Fatalf("fork returned id %q", childID)
		}

		child, err := s.Load(ctx, childID)
		if err != nil {
			t.Fatalf("load child: %v", err)
		}
		if len(child.Messages) != 7 {
			t.Fatalf("child has %d messages, want 7", len(child.Messages))
		}
		if child.Version != 7 {
			t.Fatalf("child version %d, want 7", child.Version)
		}
		for i, m := range child.Messages {
			want := parent.Messages[i]
			if m.ID != want.ID {
				t.Errorf("child[%d].ID = %q, want %q", i, m.ID, want.ID)
			}
			if !reflect.DeepEqual(m.Blocks, want.Blocks) {
				t.Errorf("child[%d] blocks differ from parent", i)
			}
			if !reflect.DeepEqual(m.Meta, want.Meta) {
				t.Errorf("child[%d] meta differs from parent", i)
			}
		}
		if _, ok := child.Messages[2].Blocks[0].(types.Compaction); !ok {
			t.Errorf("compaction block not preserved as Compaction, got %T", child.Messages[2].Blocks[0])
		}
		if child.ForkedFrom == nil || child.ForkedFrom.SessionID != "parent" || child.ForkedFrom.UpTo != upTo {
			t.Errorf("ForkedFrom = %+v, want {parent %s}", child.ForkedFrom, upTo)
		}
		if child.Owner != parent.Owner {
			t.Errorf("child owner %+v, want parent owner %+v", child.Owner, parent.Owner)
		}
	})

	t.Run("stores.fork-requires-no-lease", func(t *testing.T) {
		s := forkTestStore(t, WithSessionLeases(leaseHolderFunc(func(ctx context.Context, sessionID string) bool {
			return sessionID == "parent"
		})))
		if _, err := s.Append(ctx, "parent", 0, forkMsgs(3)...); err != nil {
			t.Fatalf("append: %v", err)
		}
		_, err := s.Fork(ctx, "parent", "")
		if !errors.Is(err, types.ErrRunActive) {
			t.Fatalf("fork with live lease: err = %v, want ErrRunActive", err)
		}
		metas, _, err := s.Sessions(ctx, types.SessionOwner{Tenant: "acme", Subject: "user-1"}, SessionQuery{Kinds: []SessionKind{SessionFork}})
		if err != nil {
			t.Fatalf("sessions: %v", err)
		}
		if len(metas) != 0 {
			t.Fatalf("fork created %d sessions despite lease refusal", len(metas))
		}
	})

	t.Run("stores.fork-survives-parent-delete", func(t *testing.T) {
		s := forkTestStore(t)
		if _, err := s.Append(ctx, "parent", 0, forkMsgs(4)...); err != nil {
			t.Fatalf("append: %v", err)
		}
		parent, err := s.Load(ctx, "parent")
		if err != nil {
			t.Fatalf("load parent: %v", err)
		}
		childID, err := s.Fork(ctx, "parent", parent.Messages[3].ID)
		if err != nil {
			t.Fatalf("fork: %v", err)
		}
		if _, err := s.Append(ctx, childID, 4, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "after fork"}}}); err != nil {
			t.Fatalf("append to child: %v", err)
		}
		if err := s.Delete(ctx, "parent"); err != nil {
			t.Fatalf("delete parent: %v", err)
		}
		child, err := s.Load(ctx, childID)
		if err != nil {
			t.Fatalf("load fork after parent delete: %v", err)
		}
		if len(child.Messages) != 5 {
			t.Fatalf("fork has %d messages after parent delete, want 5", len(child.Messages))
		}
		for i := 0; i < 4; i++ {
			if child.Messages[i].ID != parent.Messages[i].ID {
				t.Errorf("fork[%d].ID = %q, want %q", i, child.Messages[i].ID, parent.Messages[i].ID)
			}
		}
	})

	t.Run("stores.reconstruct-crosses-fork", func(t *testing.T) {
		s := forkTestStore(t)
		if _, err := s.Append(ctx, "parent", 0, forkMsgs(6)...); err != nil {
			t.Fatalf("append: %v", err)
		}
		parent, err := s.Load(ctx, "parent")
		if err != nil {
			t.Fatalf("load parent: %v", err)
		}
		upTo := parent.Messages[3].ID
		childID, err := s.Fork(ctx, "parent", upTo)
		if err != nil {
			t.Fatalf("fork: %v", err)
		}
		if _, err := s.Append(ctx, childID, 4, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "own"}}}); err != nil {
			t.Fatalf("append to child: %v", err)
		}
		child, err := s.Load(ctx, childID)
		if err != nil {
			t.Fatalf("load child: %v", err)
		}
		if child.ForkedFrom == nil || child.ForkedFrom.SessionID != "parent" || child.ForkedFrom.UpTo != upTo {
			t.Fatalf("ForkedFrom = %+v, want {parent %s}", child.ForkedFrom, upTo)
		}
		for i := 0; i < 4; i++ {
			if child.Messages[i].ID != parent.Messages[i].ID {
				t.Errorf("trail[%d].ID = %q, want parent prefix %q", i, child.Messages[i].ID, parent.Messages[i].ID)
			}
		}
		if child.Messages[4].ID == "" || child.Messages[4].ID == parent.Messages[3].ID {
			t.Errorf("trail[4] is not a fork-owned message: %q", child.Messages[4].ID)
		}
	})
}

type leaseHolderFunc func(ctx context.Context, sessionID string) bool

func (f leaseHolderFunc) SessionLeaseActive(ctx context.Context, sessionID string) bool {
	return f(ctx, sessionID)
}
