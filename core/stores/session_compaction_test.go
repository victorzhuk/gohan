package stores

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func compactionPrincipalSource(t *testing.T, principal types.Principal, ok bool) MemorySessionOption {
	t.Helper()
	return WithSessionPrincipals(func(context.Context) (types.Principal, bool) {
		return principal, ok
	})
}

func compactionTestPrincipal(tenant, subject string) types.Principal {
	return types.Principal{
		Tenant:  tenant,
		Subject: subject,
		Scopes:  []string{scopeSessionRead, scopeSessionWrite},
	}
}

func compactionPublication(sessionID, runID string, fromVersion int64, summary []types.Block) (types.Message, CompactionPublication, error) {
	checksum, err := CompactionSummarySHA(summary)
	if err != nil {
		return types.Message{}, CompactionPublication{}, err
	}
	payload := CompactionPublication{
		SessionID:   sessionID,
		RunID:       runID,
		RootRunID:   "root-1",
		Flow:        "flow-1",
		Tenant:      "t1",
		Subject:     "u1",
		FromVersion: fromVersion,
		ToVersion:   fromVersion + 1,
		Policy:      "policy-1",
		SummarySHA:  checksum,
	}
	payload.PublicationID = CompactionPublicationID(sessionID, runID, fromVersion)
	message := types.Message{
		ID:   payload.PublicationID,
		Role: types.RoleSystem,
		Blocks: []types.Block{
			types.Compaction{
				Kind:       types.CompactionText,
				CoversUpTo: fromVersion,
				Summary:    summary,
			},
		},
		Meta: map[string]any{MetadataPayloadKey: payload},
	}
	return message, payload, nil
}

func appendCompactionSeed(t *testing.T, ctx context.Context, s *MemorySessionLog, sessionID string) int64 {
	t.Helper()
	version, err := s.Append(ctx, sessionID, 0, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "seed"}}})
	if err != nil {
		t.Fatalf("seed append: %v", err)
	}
	return version
}

func TestMemorySessionAppendCompaction(t *testing.T) {
	ctx := context.Background()
	principal := compactionTestPrincipal("t1", "u1")

	t.Run("context.compaction-publication-identity", func(t *testing.T) {
		s := NewMemorySessionLog(compactionPrincipalSource(t, principal, true))
		version := appendCompactionSeed(t, ctx, s, "s1")
		message, payload, err := compactionPublication("s1", "run-1", version, []types.Block{types.Text{Text: "summary"}})
		if err != nil {
			t.Fatal(err)
		}
		wantID := CompactionPublicationID("s1", "run-1", version)
		if payload.PublicationID != wantID {
			t.Fatalf("publication id %s: want %s", payload.PublicationID, wantID)
		}
		got, err := s.AppendCompaction(ctx, "s1", version, message)
		if err != nil {
			t.Fatal(err)
		}
		if got != version+1 {
			t.Fatalf("append version %d: want %d", got, version+1)
		}
		h, err := s.Load(ctx, "s1")
		if err != nil {
			t.Fatal(err)
		}
		if h.Version != version+1 {
			t.Fatalf("history version %d: want %d", h.Version, version+1)
		}
		last := h.Messages[len(h.Messages)-1]
		if last.ID != wantID {
			t.Fatalf("stored message id %s: want %s", last.ID, wantID)
		}
	})
	t.Run("context.compaction-append-duplicate-noop", func(t *testing.T) {
		s := NewMemorySessionLog(compactionPrincipalSource(t, principal, true))
		version := appendCompactionSeed(t, ctx, s, "s1")
		message, _, err := compactionPublication("s1", "run-1", version, nil)
		if err != nil {
			t.Fatal(err)
		}
		committed, err := s.AppendCompaction(ctx, "s1", version, message)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Append(ctx, "s1", committed, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "later"}}}); err != nil {
			t.Fatal(err)
		}
		h, err := s.Load(ctx, "s1")
		if err != nil {
			t.Fatal(err)
		}
		// Identical retry with a stale expectedVersion still returns the
		// original committed append version.
		retry, err := s.AppendCompaction(ctx, "s1", h.Version, message)
		if err != nil {
			t.Fatal(err)
		}
		if retry != committed {
			t.Fatalf("duplicate version %d: want original %d", retry, committed)
		}
		after, err := s.Load(ctx, "s1")
		if err != nil {
			t.Fatal(err)
		}
		if after.Version != h.Version || len(after.Messages) != len(h.Messages) {
			t.Fatalf("duplicate mutated history: version %d messages %d, want %d/%d",
				after.Version, len(after.Messages), h.Version, len(h.Messages))
		}
	})
	t.Run("context.compaction-append-conflict-refused", func(t *testing.T) {
		s := NewMemorySessionLog(compactionPrincipalSource(t, principal, true))
		version := appendCompactionSeed(t, ctx, s, "s1")
		message, payload, err := compactionPublication("s1", "run-1", version, []types.Block{types.Text{Text: "summary"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.AppendCompaction(ctx, "s1", version, message); err != nil {
			t.Fatal(err)
		}
		conflicting := message
		conflicting.Meta = map[string]any{MetadataPayloadKey: payload}
		conflicting.Meta[MetadataPayloadKey] = func() any {
			p := payload
			p.TokensAfter = p.TokensAfter + 7
			return p
		}()
		before, err := s.Load(ctx, "s1")
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.AppendCompaction(ctx, "s1", version, conflicting)
		if !errors.Is(err, errCompactionConflict) {
			t.Fatalf("conflict error %v: want errCompactionConflict", err)
		}
		if errors.Is(err, types.ErrVersionConflict) {
			t.Fatalf("conflict error %v must not wrap types.ErrVersionConflict", err)
		}
		after, err := s.Load(ctx, "s1")
		if err != nil {
			t.Fatal(err)
		}
		if after.Version != before.Version || len(after.Messages) != len(before.Messages) {
			t.Fatal("conflicting append mutated history")
		}
	})
	t.Run("context.compaction-append-stale-refused", func(t *testing.T) {
		s := NewMemorySessionLog(compactionPrincipalSource(t, principal, true))
		version := appendCompactionSeed(t, ctx, s, "s1")
		message, _, err := compactionPublication("s1", "run-1", version, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Append(ctx, "s1", version, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "advance"}}}); err != nil {
			t.Fatal(err)
		}
		before, err := s.Load(ctx, "s1")
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.AppendCompaction(ctx, "s1", version, message)
		if !errors.Is(err, types.ErrVersionConflict) {
			t.Fatalf("stale error %v: want types.ErrVersionConflict", err)
		}
		after, err := s.Load(ctx, "s1")
		if err != nil {
			t.Fatal(err)
		}
		if after.Version != before.Version || len(after.Messages) != len(before.Messages) {
			t.Fatal("stale append mutated history")
		}
	})
	t.Run("unauthorized duplicate refused before lookup", func(t *testing.T) {
		current := principal
		present := true
		s := NewMemorySessionLog(WithSessionPrincipals(func(context.Context) (types.Principal, bool) {
			return current, present
		}))
		version := appendCompactionSeed(t, ctx, s, "s1")
		message, _, err := compactionPublication("s1", "run-1", version, nil)
		if err != nil {
			t.Fatal(err)
		}
		committed, err := s.AppendCompaction(ctx, "s1", version, message)
		if err != nil {
			t.Fatal(err)
		}
		current = types.Principal{Tenant: "other", Subject: "intruder", Scopes: []string{scopeSessionWrite}}
		// The duplicate arrives with a stale expectedVersion under a
		// forbidden principal: the refusal must not reveal the stored
		// version and must not be a conflict or version error.
		got, err := s.AppendCompaction(ctx, "s1", version-1, message)
		if !errors.Is(err, types.ErrSessionForbidden) {
			t.Fatalf("unauthorized duplicate error %v: want types.ErrSessionForbidden", err)
		}
		if got != 0 || got == committed {
			t.Fatalf("unauthorized duplicate leaked version %d", got)
		}
		present = false
		if _, err := s.AppendCompaction(ctx, "s1", version, message); !errors.Is(err, types.ErrNoPrincipal) {
			t.Fatalf("missing principal error %v: want types.ErrNoPrincipal", err)
		}
	})
	t.Run("invalid payload refused before lookup", func(t *testing.T) {
		s := NewMemorySessionLog(compactionPrincipalSource(t, principal, true))
		version := appendCompactionSeed(t, ctx, s, "s1")
		message, payload, err := compactionPublication("s1", "run-1", version, []types.Block{types.Text{Text: "summary"}})
		if err != nil {
			t.Fatal(err)
		}
		cases := []struct {
			name  string
			mutae func(m *types.Message)
		}{
			{"missing metadata", func(m *types.Message) { m.Meta = nil }},
			{"wrong message id", func(m *types.Message) { m.ID = "m-bogus" }},
			{"wrong payload publication", func(m *types.Message) {
				p := payload
				p.PublicationID = "deadbeef"
				m.Meta = map[string]any{MetadataPayloadKey: p}
			}},
			{"wrong session", func(m *types.Message) {
				p := payload
				p.SessionID = "s2"
				m.Meta = map[string]any{MetadataPayloadKey: p}
			}},
			{"version gap", func(m *types.Message) {
				p := payload
				p.ToVersion = p.FromVersion + 2
				m.Meta = map[string]any{MetadataPayloadKey: p}
			}},
			{"summary sha mismatch", func(m *types.Message) {
				p := payload
				p.SummarySHA = "0000"
				m.Meta = map[string]any{MetadataPayloadKey: p}
			}},
			{"no compaction block", func(m *types.Message) { m.Blocks = nil }},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				bad := message
				tc.mutae(&bad)
				_, err := s.AppendCompaction(ctx, "s1", version, bad)
				if !errors.Is(err, errCompactionPayload) {
					t.Fatalf("error %v: want errCompactionPayload", err)
				}
			})
		}
	})
	t.Run("caller metadata mutation cannot reach stored state", func(t *testing.T) {
		s := NewMemorySessionLog(compactionPrincipalSource(t, principal, true))
		version := appendCompactionSeed(t, ctx, s, "s1")
		message, payload, err := compactionPublication("s1", "run-1", version, []types.Block{types.Text{Text: "summary"}})
		if err != nil {
			t.Fatal(err)
		}
		committed, err := s.AppendCompaction(ctx, "s1", version, message)
		if err != nil {
			t.Fatal(err)
		}
		message.Meta["intruder"] = "yes"
		p := payload
		p.Policy = "mutated"
		message.Meta[MetadataPayloadKey] = p
		h, err := s.Load(ctx, "s1")
		if err != nil {
			t.Fatal(err)
		}
		stored := h.Messages[len(h.Messages)-1]
		if _, ok := stored.Meta["intruder"]; ok {
			t.Fatal("caller Meta mutation reached stored metadata")
		}
		// The stored payload still carries the committed policy, and a fresh
		// identical retry still dedupes to the original version.
		retryMessage, retryPayload, err := compactionPublication("s1", "run-1", version, []types.Block{types.Text{Text: "summary"}})
		if err != nil {
			t.Fatal(err)
		}
		if retryPayload.Policy != "policy-1" {
			t.Fatalf("rebuilt payload policy %s: want policy-1", retryPayload.Policy)
		}
		retry, err := s.AppendCompaction(ctx, "s1", committed, retryMessage)
		if err != nil {
			t.Fatal(err)
		}
		if retry != committed {
			t.Fatalf("retry after caller mutation returned %d: want %d", retry, committed)
		}
	})
	t.Run("concurrent identical duplicates insert once", func(t *testing.T) {
		s := NewMemorySessionLog(compactionPrincipalSource(t, principal, true))
		version := appendCompactionSeed(t, ctx, s, "s1")
		message, _, err := compactionPublication("s1", "run-1", version, nil)
		if err != nil {
			t.Fatal(err)
		}
		const writers = 2
		results := make([]int64, writers)
		errs := make([]error, writers)
		var wg sync.WaitGroup
		for i := range writers {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				results[i], errs[i] = s.AppendCompaction(ctx, "s1", version, message)
			}(i)
		}
		wg.Wait()
		for i := range writers {
			if errs[i] != nil {
				t.Fatalf("writer %d: %v", i, errs[i])
			}
			if results[i] != version+1 {
				t.Fatalf("writer %d version %d: want %d", i, results[i], version+1)
			}
		}
		h, err := s.Load(ctx, "s1")
		if err != nil {
			t.Fatal(err)
		}
		if h.Version != version+1 {
			t.Fatalf("history version %d: want %d", h.Version, version+1)
		}
		if len(h.Messages) != 2 {
			t.Fatalf("message count %d: want 2", len(h.Messages))
		}
	})
	t.Run("session delete removes identity records", func(t *testing.T) {
		s := NewMemorySessionLog(compactionPrincipalSource(t, principal, true))
		version := appendCompactionSeed(t, ctx, s, "s1")
		message, _, err := compactionPublication("s1", "run-1", version, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.AppendCompaction(ctx, "s1", version, message); err != nil {
			t.Fatal(err)
		}
		if err := s.Delete(ctx, "s1"); err != nil {
			t.Fatal(err)
		}
		if rows := s.publications["s1"]; len(rows) != 0 {
			t.Fatalf("identity records survived delete: %v", rows)
		}
	})
}
