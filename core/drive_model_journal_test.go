package gohan

import (
	"context"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/hedge"
)

type journalSlowModel struct {
	profile types.ModelProfile
	delay   time.Duration

	mu    sync.Mutex
	calls int
}

func (m *journalSlowModel) Profile() types.ModelProfile { return m.profile }

func (m *journalSlowModel) callsMade() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func (m *journalSlowModel) Generate(ctx context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		m.mu.Lock()
		m.calls++
		m.mu.Unlock()
		if !yield(types.ModelChunk{Kind: types.DeltaReasoning, Delta: "thinking"}, nil) {
			return
		}
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			yield(types.ModelChunk{}, ctx.Err())
			return
		}
		yield(types.ModelChunk{Kind: types.DeltaText, Delta: "slow answer"}, nil)
	}
}

type journalFastModel struct {
	profile types.ModelProfile

	mu    sync.Mutex
	calls int
}

func (m *journalFastModel) Profile() types.ModelProfile { return m.profile }

func (m *journalFastModel) callsMade() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func (m *journalFastModel) Generate(ctx context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		m.mu.Lock()
		m.calls++
		m.mu.Unlock()
		yield(types.ModelChunk{Kind: types.DeltaText, Delta: "fast answer"}, nil)
	}
}

func streamingProfile(name string) types.ModelProfile {
	return types.ModelProfile{
		Name: name,
		Caps: types.Caps{Streaming: true},
	}
}

func TestModelStepJournal(t *testing.T) {
	t.Run("chains.hedge-single-journal-step", func(t *testing.T) {
		slow := &journalSlowModel{profile: streamingProfile("primary"), delay: 150 * time.Millisecond}
		fast := &journalFastModel{profile: streamingProfile("fast")}
		journal := stores.NewMemoryJournal()
		spec := NativeSpec{
			Request: FlowRequest{Name: "chat"},
			Profile: "primary",
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
			},
		}
		opts := []Option{
			AllowAnonymous(),
			WithModels(slow, fast),
			WithNativeAgent(spec),
			WithModelMiddleware(hedge.New(hedge.HedgePolicy{
				After: 20 * time.Millisecond,
				Targets: func() []types.Model {
					return []types.Model{slow, fast}
				},
			})),
			WithStores(stores.Stores{
				SessionLog:  stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
				Runs:        stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom)),
				Checkpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
				Journal:     journal,
			}),
		}
		stack, err := Build(opts...)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		conv, err := NewNativeConversation(stack, "chat",
			WithConversationRuns(stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now), stores.WithMemoryRunInfo(types.RunInfoFrom))),
			WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
		)
		if err != nil {
			t.Fatalf("new conversation: %v", err)
		}
		ctx := types.WithRunInfo(
			types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"}),
			types.RunInfo{Flow: "chat", SessionID: "s1", RunID: "r1", RootRunID: "r1"},
		)
		for ev, serr := range conv.Send(ctx, "s1", types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "hi"}}}) {
			_ = ev
			if serr != nil {
				t.Fatalf("send: %v", serr)
			}
		}
		if got := slow.callsMade(); got != 1 {
			t.Fatalf("slow calls = %d, want 1", got)
		}
		if got := fast.callsMade(); got != 1 {
			t.Fatalf("fast calls = %d, want 1", got)
		}
		entries, err := journal.ByFingerprint(ctx, "s1", "model:1")
		if err != nil {
			t.Fatalf("by fingerprint: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("journal entries = %d, want one model step", len(entries))
		}
		e := entries[0]
		if e.State != stores.Completed {
			t.Fatalf("entry state = %v, want completed", e.State)
		}
		if e.Attempts != 2 {
			t.Fatalf("attempts = %d, want 2 on a hedged pair", e.Attempts)
		}
		if e.Endpoint != "fast" {
			t.Fatalf("endpoint = %q, want the winner fast", e.Endpoint)
		}

		// A re-drive that finds the completed entry replays the winner's
		// response instead of re-issuing the pair.
		for ev, rerr := range conv.Send(ctx, "s1", types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "again"}}}) {
			_ = ev
			if rerr != nil {
				t.Fatalf("re-send: %v", rerr)
			}
		}
		if got := slow.callsMade(); got != 1 {
			t.Fatalf("slow calls after replay = %d, want still 1", got)
		}
		if got := fast.callsMade(); got != 1 {
			t.Fatalf("fast calls after replay = %d, want still 1", got)
		}
	})
}
