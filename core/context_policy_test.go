package gohan_test

import (
	"context"
	"iter"
	"sync"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type recordingModel struct {
	mu       sync.Mutex
	requests []types.ModelRequest
}

func (m *recordingModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "native", Caps: types.Caps{Tools: true}}
}

func (m *recordingModel) Generate(_ context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	m.mu.Unlock()
	return func(yield func(types.ModelChunk, error) bool) {
		yield(types.ModelChunk{Kind: types.DeltaText, Delta: "ok"}, nil)
		yield(types.ModelChunk{Finish: types.FinishStop}, nil)
	}
}

type countingProjection struct {
	mu    sync.Mutex
	calls int
}

func (p *countingProjection) Project(_ context.Context, h stores.History, _ types.ModelProfile) (stores.History, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return h, nil
}

func policyTestStack(t *testing.T, model *recordingModel, opts ...gohan.Option) (*gohan.Stack, gohan.Conversation) {
	t.Helper()
	runs := stores.NewMemoryRuns(
		stores.WithMemoryRunClock(time.Now),
		stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
			p, ok := types.PrincipalFrom(ctx)
			return types.RunInfo{Principal: p}, ok
		}),
	)
	opts = append([]gohan.Option{
		gohan.WithStores(stores.Stores{SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))}),
		gohan.WithModels(model),
	}, opts...)
	stack, err := gohan.Build(opts...)
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

func sendOne(t *testing.T, conv gohan.Conversation, text string) {
	t.Helper()
	ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"})
	for ev, err := range conv.Send(ctx, "s1", types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: text}}}) {
		if err != nil {
			t.Fatalf("send: %v", err)
		}
		_ = ev
	}
}

func passthroughAssemble(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
	return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
}

func TestWithContextPolicy(t *testing.T) {
	model := &recordingModel{}
	proj := &countingProjection{}
	policy := stores.ContextPolicy{Projections: []stores.Projection{proj}}
	stack, conv := policyTestStack(t, model,
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request:  gohan.FlowRequest{Name: "chat"},
			Profile:  "native",
			Assemble: passthroughAssemble,
		}),
		gohan.WithContextPolicy(policy),
	)
	got, ok := stack.ContextPolicy("chat")
	if !ok {
		t.Fatal("chat is not a registered native flow")
	}
	if got.Compactor != nil {
		t.Fatalf("Compactor = %v, want nil", got.Compactor)
	}
	if len(got.Projections) != 1 || got.Projections[0] != stores.Projection(proj) {
		t.Fatalf("Projections = %v, want the bound projection", got.Projections)
	}
	sendOne(t, conv, "hi")
	if len(model.requests) != 1 {
		t.Fatalf("model calls = %d, want 1", len(model.requests))
	}
}

func TestNoContextPolicyUnchanged(t *testing.T) {
	model := &recordingModel{}
	stack, conv := policyTestStack(t, model,
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request:  gohan.FlowRequest{Name: "chat"},
			Profile:  "native",
			Assemble: passthroughAssemble,
		}),
	)
	got, ok := stack.ContextPolicy("chat")
	if !ok {
		t.Fatal("chat is not a registered native flow")
	}
	if got.Compactor != nil || len(got.Projections) != 0 {
		t.Fatalf("policy = %+v, want the zero policy", got)
	}
	sendOne(t, conv, "hi")
	if len(model.requests) != 1 {
		t.Fatalf("model calls = %d, want 1", len(model.requests))
	}
	msgs := model.requests[0].Messages
	if len(msgs) != 1 || msgs[0].Role != types.RoleUser || len(msgs[0].Blocks) != 1 {
		t.Fatalf("assembled messages = %+v, want the plain input path", msgs)
	}
	if text, ok := msgs[0].Blocks[0].(types.Text); !ok || text.Text != "hi" {
		t.Fatalf("assembled block = %#v, want the user text", msgs[0].Blocks[0])
	}
}
