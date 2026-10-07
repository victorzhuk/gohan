package gohan_test

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// interceptingLog wraps a SessionLog and records every Append while
// delegating Load, so a test can prove a projection stage never persisted.
type interceptingLog struct {
	mu      sync.Mutex
	inner   stores.SessionLog
	appends [][]types.Message
}

func newInterceptingLog(inner stores.SessionLog) *interceptingLog {
	return &interceptingLog{inner: inner}
}

func (l *interceptingLog) Load(ctx context.Context, sessionID string) (stores.History, error) {
	return l.inner.Load(ctx, sessionID)
}

func (l *interceptingLog) Append(ctx context.Context, sessionID string, expectedVersion int64, msgs ...types.Message) (int64, error) {
	l.mu.Lock()
	l.appends = append(l.appends, slices.Clone(msgs))
	l.mu.Unlock()
	return l.inner.Append(ctx, sessionID, expectedVersion, msgs...)
}

func (l *interceptingLog) Purge(ctx context.Context, olderThan time.Time) (int, error) {
	return l.inner.Purge(ctx, olderThan)
}

func (l *interceptingLog) Delete(ctx context.Context, sessionID string) error {
	return l.inner.Delete(ctx, sessionID)
}

func (l *interceptingLog) recorded() [][]types.Message {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([][]types.Message, len(l.appends))
	copy(out, l.appends)
	return out
}

// seededHistory seeds log with one user turn and two completed tool calls
// whose results the tests treat as the recorded truth: a notes_read result
// and a notes_write result. It returns the loaded history with its store
// version.
func seededHistory(t *testing.T, log stores.SessionLog, sessionID string) stores.History {
	t.Helper()
	ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"})
	msgs := []types.Message{
		{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "seed"}}},
		{Role: types.RoleAssistant, Blocks: []types.Block{types.ToolUse{ID: "use-read", Name: "notes_read"}}},
		{Role: types.RoleUser, Blocks: []types.Block{types.ToolResult{ID: "use-read", Content: []types.Block{types.Text{Text: "read result"}}, Outcome: types.Succeeded}}},
		{Role: types.RoleAssistant, Blocks: []types.Block{types.ToolUse{ID: "use-write", Name: "notes_write"}}},
		{Role: types.RoleUser, Blocks: []types.Block{types.ToolResult{ID: "use-write", Content: []types.Block{types.Text{Text: "write result"}}, Outcome: types.Succeeded}}},
	}
	h, err := log.Load(ctx, sessionID)
	if err != nil {
		h = stores.History{}
	}
	if _, err := log.Append(ctx, sessionID, h.Version, msgs...); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	h, err = log.Load(ctx, sessionID)
	if err != nil {
		t.Fatalf("load seeded session: %v", err)
	}
	return h
}

// fixedEstimator answers every count with its fixed value, so a
// projection's firing decision is reproducible.
type fixedEstimator struct{ n int }

func (e fixedEstimator) Count(context.Context, types.ModelRequest) (int, error) {
	return e.n, nil
}

var _ types.TokenCounter = fixedEstimator{}

// clearingProjection replaces every tool result's content with its marker,
// the observable effect a no-write assertion looks for in the store.
type clearingProjection struct {
	marker string
}

func (p clearingProjection) Project(_ context.Context, h stores.History, _ types.ModelProfile) (stores.History, error) {
	out := make([]types.Message, len(h.Messages))
	copy(out, h.Messages)
	for i := range out {
		blocks := make([]types.Block, len(out[i].Blocks))
		copy(blocks, out[i].Blocks)
		for j, b := range blocks {
			if r, ok := b.(types.ToolResult); ok {
				r.Content = []types.Block{types.Text{Text: p.marker}}
				blocks[j] = r
			}
		}
		out[i].Blocks = blocks
	}
	return stores.History{Messages: out, Version: h.Version}, nil
}

// markerProjection appends one marker message and records the marker of an
// earlier projection when it sees one, so declared order is observable.
type markerProjection struct {
	name string
	mu   sync.Mutex
	saw  []string
}

func (p *markerProjection) Project(_ context.Context, h stores.History, _ types.ModelProfile) (stores.History, error) {
	for _, msg := range h.Messages {
		for _, b := range msg.Blocks {
			if txt, ok := b.(types.Text); ok && txt.Text == "proj-a" {
				p.mu.Lock()
				p.saw = append(p.saw, txt.Text)
				p.mu.Unlock()
			}
		}
	}
	out := append(slices.Clone(h.Messages), types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: p.name}}})
	return stores.History{Messages: out, Version: h.Version}, nil
}

func projectionStack(t *testing.T, model *recordingModel, log stores.SessionLog, policy stores.ContextPolicy) (*gohan.Stack, gohan.Conversation) {
	t.Helper()
	runs := stores.NewMemoryRuns(
		stores.WithMemoryRunClock(time.Now),
		stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
			p, ok := types.PrincipalFrom(ctx)
			return types.RunInfo{Principal: p}, ok
		}),
	)
	stack, err := gohan.Build(
		gohan.WithStores(stores.Stores{SessionLog: log}),
		gohan.WithModels(model),
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request:  gohan.FlowRequest{Name: "chat"},
			Profile:  "native",
			Assemble: passthroughAssemble,
		}),
		gohan.WithContextPolicy(policy),
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	conv, err := gohan.NewNativeConversation(stack, "chat",
		gohan.WithConversationRuns(runs),
		gohan.WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
	)
	if err != nil {
		t.Fatalf("new native conversation: %v", err)
	}
	return stack, conv
}

func requestBytes(t *testing.T, m *recordingModel) []byte {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.requests) == 0 {
		t.Fatal("model received no request")
	}
	raw, err := json.Marshal(m.requests[0].Messages)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return raw
}

func lastText(msg types.Message) string {
	for i := len(msg.Blocks) - 1; i >= 0; i-- {
		if txt, ok := msg.Blocks[i].(types.Text); ok {
			return txt.Text
		}
	}
	return ""
}

func TestProjectionAssemblyOrder(t *testing.T) {
	model := &recordingModel{}
	first := &markerProjection{name: "proj-a"}
	second := &markerProjection{name: "proj-b"}
	_, conv := projectionStack(t, model,
		newInterceptingLog(stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))),
		stores.ContextPolicy{Projections: []stores.Projection{first, second}})
	sendOne(t, conv, "turn")

	model.mu.Lock()
	req := model.requests[0]
	model.mu.Unlock()
	var texts []string
	for _, msg := range req.Messages {
		texts = append(texts, lastText(msg))
	}
	if want := []string{"turn", "proj-a", "proj-b"}; !slices.Equal(texts, want) {
		t.Fatalf("request message order = %v, want %v: history first, then the projections in declared order", texts, want)
	}
	second.mu.Lock()
	defer second.mu.Unlock()
	if len(second.saw) != 1 || second.saw[0] != "proj-a" {
		t.Fatalf("second projection saw %v, want the first projection's output", second.saw)
	}
	first.mu.Lock()
	defer first.mu.Unlock()
	if len(first.saw) != 0 {
		t.Fatalf("first projection saw %v, want no later projection output", first.saw)
	}
}

func TestProjectionAssembly(t *testing.T) {
	t.Run("context.projection-deterministic", func(t *testing.T) {
		build := func() *recordingModel {
			model := &recordingModel{}
			log := newInterceptingLog(stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)))
			_, conv := projectionStack(t, model, log,
				stores.ContextPolicy{Projections: []stores.Projection{clearingProjection{marker: "[tool result cleared]"}}})
			seededHistory(t, log, "s1")
			sendOne(t, conv, "turn")
			return model
		}
		if !bytes.Equal(requestBytes(t, build()), requestBytes(t, build())) {
			t.Fatal("two assemblies of the same history version and profile produced different request bytes")
		}
	})

	t.Run("context.projection-does-not-write", func(t *testing.T) {
		const marker = "[projection fired]"
		model := &recordingModel{}
		log := newInterceptingLog(stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)))
		_, conv := projectionStack(t, model, log,
			stores.ContextPolicy{Projections: []stores.Projection{clearingProjection{marker: marker}}})
		h := seededHistory(t, log, "s1")
		sendOne(t, conv, "turn")

		ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"})
		got, err := log.Load(ctx, "s1")
		if err != nil {
			t.Fatalf("load after send: %v", err)
		}
		if got.Version != h.Version+2 {
			t.Fatalf("version after send = %d, want seeded %d plus the turn's own user and assistant messages", got.Version, h.Version)
		}
		results := map[string]string{}
		for _, msg := range got.Messages {
			for _, b := range msg.Blocks {
				if txt, ok := b.(types.Text); ok && txt.Text == marker {
					t.Fatal("projection output reached the session log")
				}
				if r, ok := b.(types.ToolResult); ok && len(r.Content) > 0 {
					if txt, ok := r.Content[0].(types.Text); ok {
						results[r.ID] = txt.Text
					}
				}
			}
		}
		if results["use-read"] != "read result" || results["use-write"] != "write result" {
			t.Fatalf("stored tool results = %v, want the full recorded results", results)
		}
		for _, msgs := range log.recorded() {
			for _, msg := range msgs {
				if lastText(msg) == marker {
					t.Fatal("an Append carried a projected message")
				}
			}
		}
	})
}
