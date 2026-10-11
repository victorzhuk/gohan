package eino

import (
	"context"
	"sync"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/victorzhuk/gohan/core/types"
)

// scriptedSchemaModel plays the backend model: it replays scripted
// messages, then answers done.
type scriptedSchemaModel struct {
	msgs []*schema.Message
	i    int
}

func (m *scriptedSchemaModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	if m.i < len(m.msgs) {
		msg := m.msgs[m.i]
		m.i++
		return msg, nil
	}
	return &schema.Message{Role: schema.Assistant, Content: "done"}, nil
}

func (m *scriptedSchemaModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, in, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

// recordUseTool records the tool-use carrier the context reaches it with.
type recordUseTool struct {
	mu  sync.Mutex
	got types.ToolUse
	ok  bool
}

func (r *recordUseTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "rec"}, nil
}

func (r *recordUseTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	r.mu.Lock()
	r.got, r.ok = types.ToolUseFrom(ctx)
	r.mu.Unlock()
	return `{"text":"ok"}`, nil
}

func TestEinoToolCallIDHandler(t *testing.T) {
	t.Run("call-1_reaches_tool_use_id", func(t *testing.T) {
		rec := &recordUseTool{}
		m := &scriptedSchemaModel{msgs: []*schema.Message{{
			Role: schema.Assistant,
			ToolCalls: []schema.ToolCall{{
				ID:       "call-1",
				Function: schema.FunctionCall{Name: "rec", Arguments: "{}"},
			}},
		}}}
		ag, err := adk.NewChatModelAgent(context.Background(), &adk.ChatModelAgentConfig{
			Model:    m,
			Handlers: []adk.ChatModelAgentMiddleware{NewToolCallIDHandler()},
			ToolsConfig: adk.ToolsConfig{
				ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []tool.BaseTool{rec}},
			},
		})
		if err != nil {
			t.Fatalf("NewChatModelAgent: %v", err)
		}
		ctx := context.Background()
		runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: ag})
		it := runner.Run(ctx, []*schema.Message{{Role: schema.User, Content: "go"}})
		for {
			ev, ok := it.Next()
			if !ok {
				break
			}
			if ev.Err != nil {
				t.Fatalf("runner: %v", ev.Err)
			}
		}
		rec.mu.Lock()
		defer rec.mu.Unlock()
		if !rec.ok {
			t.Fatal("ToolUseFrom inside the tool: ok = false, want the carried call")
		}
		if rec.got.ID != "call-1" {
			t.Fatalf("carried ID = %q, want call-1", rec.got.ID)
		}
	})
}
