package gohan

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type funcModel struct {
	fn types.ModelFunc
}

func (m *funcModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "native", Caps: types.Caps{Tools: true}}
}

func (m *funcModel) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return m.fn(ctx, req)
}

type countingMW struct {
	mu    sync.Mutex
	calls int
}

func (c *countingMW) wrap(next types.ModelFunc) types.ModelFunc {
	return func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
		c.mu.Lock()
		c.calls++
		c.mu.Unlock()
		return next(ctx, req)
	}
}

func (c *countingMW) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func batchThenDone() types.ModelFunc {
	return func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
		return func(yield func(types.ModelChunk, error) bool) {
			for _, m := range req.Messages {
				for _, b := range m.Blocks {
					if _, ok := b.(types.ToolResult); ok {
						yield(types.ModelChunk{Kind: types.DeltaText, Delta: "done"}, nil)
						yield(types.ModelChunk{Finish: types.FinishStop}, nil)
						return
					}
				}
			}
			yield(types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: &types.ToolUse{ID: "c1", Name: "echo", Args: jsontext.Value(`{}`)}}, nil)
			yield(types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: &types.ToolUse{ID: "c2", Name: "echo", Args: jsontext.Value(`{}`)}}, nil)
			yield(types.ModelChunk{Finish: types.FinishToolUse}, nil)
		}
	}
}

// noopTool is a ReadOnly tool with no state, safe under concurrent
// conversations sharing one stack.
type noopTool struct{}

func (noopTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "echo", Effect: types.ReadOnly}
}

func (noopTool) Call(context.Context, jsontext.Value) (types.ToolResult, error) {
	return types.ToolResult{Content: []types.Block{types.Text{Text: "echoed"}}, Outcome: types.Succeeded}, nil
}

func nativeConvLimits(maxToolCalls int) types.RunLimits {
	return types.RunLimits{
		MaxTurns:            8,
		MaxToolCalls:        maxToolCalls,
		MaxWallClock:        time.Minute,
		SoftRatio:           0.8,
		MaxDepth:            4,
		MaxParallelChildren: 2,
		MaxParallelTools:    2,
	}
}

func nativeConvStack(t *testing.T, gen types.ModelFunc, mw types.ModelMiddleware, limits types.RunLimits) *Stack {
	t.Helper()
	opts := []Option{
		WithStores(stores.Stores{SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))}),
		WithModels(&funcModel{fn: gen}),
		WithNativeAgent(NativeSpec{
			Request: FlowRequest{Name: "chat"},
			Profile: "native",
			Tools:   []types.Tool{noopTool{}},
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
			},
		}),
	}
	if mw != nil {
		opts = append(opts, WithModelMiddleware(mw))
	}
	if limits.MaxToolCalls > 0 {
		opts = append(opts, WithLimits("chat", limits))
	}
	stack, err := Build(opts...)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return stack
}

func nativeConv(t *testing.T, stack *Stack) Conversation {
	t.Helper()
	conv, err := NewNativeConversation(stack, "chat",
		WithConversationRuns(stores.NewMemoryRuns(
			stores.WithMemoryRunClock(time.Now),
			stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
				p, ok := types.PrincipalFrom(ctx)
				return types.RunInfo{Principal: p}, ok
			}),
		)),
		WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
	)
	if err != nil {
		t.Fatalf("new native conversation: %v", err)
	}
	return conv
}

func TestNewNativeConversation(t *testing.T) {
	textTurn := func(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
		return func(yield func(types.ModelChunk, error) bool) {
			yield(types.ModelChunk{Kind: types.DeltaText, Delta: "hi"}, nil)
			yield(types.ModelChunk{Finish: types.FinishStop}, nil)
		}
	}

	t.Run("build middleware runs in send", func(t *testing.T) {
		mw := &countingMW{}
		conv := nativeConv(t, nativeConvStack(t, textTurn, mw.wrap, types.RunLimits{}))
		res := collectStream(conv.Send(principalCtx(context.Background()), "s1", userMsg("hello")))
		if res.err != nil {
			t.Fatalf("send: %v", res.err)
		}
		if mw.count() != 1 {
			t.Fatalf("middleware calls %d, want 1", mw.count())
		}
		var text string
		for _, ev := range res.evs {
			if am, ok := ev.(types.AssistantMessage); ok {
				for _, b := range am.Message.Blocks {
					if txt, ok := b.(types.Text); ok {
						text += txt.Text
					}
				}
			}
		}
		if !strings.Contains(text, "hi") {
			t.Fatalf("assistant text %q, want hi", text)
		}
	})

	t.Run("unknown name is refused", func(t *testing.T) {
		mw := &countingMW{}
		stack := nativeConvStack(t, textTurn, mw.wrap, types.RunLimits{})
		if _, err := NewNativeConversation(stack, "nope"); err == nil {
			t.Fatal("unknown flow name must be refused")
		}
		if _, err := NewNativeConversation(nil, "chat"); err == nil {
			t.Fatal("nil stack must be refused")
		}
		if mw.count() != 0 {
			t.Fatalf("refusal reached the model %d times", mw.count())
		}
	})

	t.Run("concurrent conversations stay isolated", func(t *testing.T) {
		stack := nativeConvStack(t, batchThenDone(), nil, nativeConvLimits(2))
		var wg sync.WaitGroup
		type outcome struct {
			evs []types.Event
			err error
		}
		out := make([]outcome, 2)
		for i := range out {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				conv := nativeConv(t, stack)
				res := collectStream(conv.Send(principalCtx(context.Background()), fmt.Sprintf("s%d", i), userMsg("go")))
				out[i] = outcome(res)
			}(i)
		}
		wg.Wait()
		for i, o := range out {
			if o.err != nil {
				t.Fatalf("conversation %d: %v", i, o.err)
			}
			var finished, done int
			for _, ev := range o.evs {
				switch ev.(type) {
				case types.ToolFinished:
					finished++
				case types.Done:
					done++
				}
			}
			if finished != 2 || done == 0 {
				t.Fatalf("conversation %d: tool results %d, done %d, want 2 tool results and a done", i, finished, done)
			}
		}
	})
}
