package mcp

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// ResumeChild is the chain-resume seam core resolves on the registered
// tool; the tests reach it through the structural interface.
type resumeSeam interface {
	ResumeChild(ctx context.Context, t types.ResumeToken, r stores.ResumeInput) (json.RawMessage, error)
}

type callRecord struct {
	Name           string
	Arguments      json.RawMessage
	InputResponses mcp.InputResponseMap
	RequestState   string
}

type scriptTool struct {
	name string
	fn   func(p *mcp.CallToolParamsRaw) (*mcp.CallToolResult, error)
}

type scriptFixture struct {
	mu    sync.Mutex
	calls []callRecord
	tools []*scriptTool
}

func (f *scriptFixture) record(p *mcp.CallToolParamsRaw) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, callRecord{
		Name:           p.Name,
		Arguments:      p.Arguments,
		InputResponses: p.InputResponses,
		RequestState:   p.RequestState,
	})
}

func (f *scriptFixture) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *scriptFixture) last() callRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

func (f *scriptFixture) serve(t *testing.T) string {
	t.Helper()
	ts := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		srv := mcp.NewServer(&mcp.Implementation{Name: "script", Version: "0"}, nil)
		for _, st := range f.tools {
			tool := &mcp.Tool{Name: st.name, InputSchema: map[string]any{"type": "object"}}
			srv.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				f.record(req.Params)
				return st.fn(req.Params)
			})
		}
		return srv
	}, &mcp.StreamableHTTPOptions{Stateless: true})
	httpSrv := httptest.NewServer(ts)
	t.Cleanup(httpSrv.Close)
	return httpSrv.URL
}

func mrtrInputRequired(schema any) *mcp.CallToolResult {
	return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{
		"0": &mcp.ElicitParams{Mode: "form", Message: "need input", RequestedSchema: schema},
	}}
}

func mrtrSet(t *testing.T, f *scriptFixture) (types.Tool, resumeSeam) {
	t.Helper()
	cps := stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom))
	set := connect(t, f.serve(t), Namespace("ns"), WithCheckpoints(cps))
	tools, err := set.Tools(context.Background())
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	for _, tool := range tools {
		if tool.Spec().Name == "ns__ask" {
			seam, ok := tool.(resumeSeam)
			if !ok {
				t.Fatal("tool does not expose ResumeChild")
			}
			return tool, seam
		}
	}
	t.Fatal("tool ns__ask not found")
	return nil, nil
}

func mrtrCtx() context.Context {
	ctx := types.WithRunInfo(context.Background(), types.RunInfo{SessionID: "s1", RunID: "r1"})
	return types.WithToolUse(ctx, types.ToolUse{ID: "call-1", Name: "ns__ask", Args: jsontext.Value(`{"q":"x"}`)})
}

func TestMRTR(t *testing.T) {
	t.Run("interop.mrtr-confirmation", func(t *testing.T) {
		f := &scriptFixture{tools: []*scriptTool{{name: "ask", fn: func(*mcp.CallToolParamsRaw) (*mcp.CallToolResult, error) {
			return mrtrInputRequired(map[string]any{"type": "boolean"}), nil
		}}}}
		tool, _ := mrtrSet(t, f)
		_, err := tool.Call(mrtrCtx(), json.RawMessage(`{"q":"x"}`))
		var susp *types.SuspendError
		if !errors.As(err, &susp) {
			t.Fatalf("Call: want SuspendError, got %v", err)
		}
		if susp.Reason != types.HumanApproval {
			t.Errorf("Reason = %v, want HumanApproval", susp.Reason)
		}
		if susp.Token == "" {
			t.Error("Token is empty")
		}
		req, ok := susp.Payload.(permission.ApprovalRequest)
		if !ok {
			t.Fatalf("Payload is %T, want permission.ApprovalRequest", susp.Payload)
		}
		if req.Call.Name != "ns__ask" {
			t.Errorf("Call.Name = %q, want ns__ask", req.Call.Name)
		}
		if req.Call.ID != "call-1" {
			t.Errorf("Call.ID = %q, want call-1", req.Call.ID)
		}
		if string(req.Call.Args) != `{"q":"x"}` {
			t.Errorf("Call.Args = %s", req.Call.Args)
		}
		if req.Risk != req.Tool.Risk {
			t.Errorf("Risk = %v, want %v", req.Risk, req.Tool.Risk)
		}
	})

	t.Run("interop.mrtr-replay", func(t *testing.T) {
		f := &scriptFixture{tools: []*scriptTool{{name: "ask", fn: func(p *mcp.CallToolParamsRaw) (*mcp.CallToolResult, error) {
			if len(p.InputResponses) == 0 {
				res := mrtrInputRequired(map[string]any{"type": "boolean"})
				res.RequestState = "st-1"
				return res, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "approved"}}}, nil
		}}}}
		tool, seam := mrtrSet(t, f)
		_, err := tool.Call(mrtrCtx(), json.RawMessage(`{"q":"x"}`))
		var susp *types.SuspendError
		if !errors.As(err, &susp) {
			t.Fatalf("Call: want SuspendError, got %v", err)
		}
		out, err := seam.ResumeChild(mrtrCtx(), susp.Token, stores.ResumeInput{Verdict: stores.VerdictApprove})
		if err != nil {
			t.Fatalf("ResumeChild: %v", err)
		}
		if f.count() != 2 {
			t.Fatalf("tools/call count = %d, want 2", f.count())
		}
		rec := f.last()
		if rec.Name != "ask" {
			t.Errorf("replayed tool = %q, want ask", rec.Name)
		}
		resp, ok := rec.InputResponses["0"]
		if !ok {
			t.Fatal("replay carries no InputResponses[0]")
		}
		er, ok := resp.(*mcp.ElicitResult)
		if !ok {
			t.Fatalf("response is %T", resp)
		}
		if er.Action != "accept" {
			t.Errorf("Action = %q, want accept", er.Action)
		}
		if string(rec.Arguments) != `{"q":"x"}` {
			t.Errorf("replayed Arguments = %s", rec.Arguments)
		}
		if rec.RequestState != "st-1" {
			t.Errorf("RequestState = %q, want st-1", rec.RequestState)
		}
		if string(out) != `"approved"` {
			t.Errorf("ResumeChild = %s, want \"approved\"", out)
		}
	})

	t.Run("interop.client-awaiting-input-schema", func(t *testing.T) {
		f := &scriptFixture{tools: []*scriptTool{{name: "ask", fn: func(p *mcp.CallToolParamsRaw) (*mcp.CallToolResult, error) {
			if len(p.InputResponses) == 0 {
				res := mrtrInputRequired(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"choice": map[string]any{"type": "string"},
					},
					"required": []string{"choice"},
				})
				res.RequestState = "st-2"
				return res, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "done"}}}, nil
		}}}}
		tool, seam := mrtrSet(t, f)
		_, err := tool.Call(mrtrCtx(), json.RawMessage(`{"q":"x"}`))
		var susp *types.SuspendError
		if !errors.As(err, &susp) {
			t.Fatalf("Call: want SuspendError, got %v", err)
		}
		if susp.Reason != types.AwaitingInput {
			t.Fatalf("Reason = %v, want AwaitingInput", susp.Reason)
		}
		req, ok := susp.Payload.(types.InputRequest)
		if !ok {
			t.Fatalf("Payload is %T, want types.InputRequest", susp.Payload)
		}
		var schema map[string]any
		if err := json.Unmarshal(req.Schema, &schema); err != nil {
			t.Fatalf("Schema: %v", err)
		}
		if _, ok := schema["properties"]; !ok {
			t.Errorf("Schema missing properties: %s", req.Schema)
		}
		bad, err := seam.ResumeChild(mrtrCtx(), susp.Token, stores.ResumeInput{Data: json.RawMessage(`{"choice":5}`)})
		if !errors.Is(err, types.ErrInputInvalid) {
			t.Fatalf("ResumeChild bad payload: want ErrInputInvalid, got %v", err)
		}
		if bad != nil {
			t.Errorf("bad payload returned bytes %s", bad)
		}
		if f.count() != 1 {
			t.Fatalf("tools/call count = %d after bad delivery, want 1", f.count())
		}
		good, err := seam.ResumeChild(mrtrCtx(), susp.Token, stores.ResumeInput{Data: json.RawMessage(`{"choice":"a"}`)})
		if err != nil {
			t.Fatalf("ResumeChild good payload: %v", err)
		}
		if f.count() != 2 {
			t.Fatalf("tools/call count = %d, want 2", f.count())
		}
		resp := f.last().InputResponses["0"].(*mcp.ElicitResult)
		if got, _ := resp.Content["choice"].(string); got != "a" {
			t.Errorf("Content.choice = %v, want a", resp.Content["choice"])
		}
		if string(good) != `"done"` {
			t.Errorf("ResumeChild = %s, want \"done\"", good)
		}
	})

	t.Run("interop.task-handle", func(t *testing.T) {
		wake := time.Now().UTC().Add(time.Minute).Truncate(time.Second)
		f := &scriptFixture{tools: []*scriptTool{{name: "ask", fn: func(*mcp.CallToolParamsRaw) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Meta: mcp.Meta{
				MetaKeyGohan: map[string]any{
					MetaKeyTask:   map[string]any{"taskId": "t1", "runId": "r9"},
					MetaKeyWakeAt: wake.Format(time.RFC3339),
				},
			}}, nil
		}}}}
		tool, _ := mrtrSet(t, f)
		_, err := tool.Call(mrtrCtx(), json.RawMessage(`{"q":"x"}`))
		var susp *types.SuspendError
		if !errors.As(err, &susp) {
			t.Fatalf("Call: want SuspendError, got %v", err)
		}
		if susp.Reason != types.AwaitingTool {
			t.Errorf("Reason = %v, want AwaitingTool", susp.Reason)
		}
		h, ok := susp.Payload.(TaskHandle)
		if !ok {
			t.Fatalf("Payload is %T, want TaskHandle", susp.Payload)
		}
		if h.TaskID != "t1" || h.RunID != "r9" || h.ServerTool != "ask" {
			t.Errorf("handle = %+v", h)
		}
		if !susp.WakeAt.Equal(wake) {
			t.Errorf("WakeAt = %v, want %v", susp.WakeAt, wake)
		}
	})
}

func TestMRTRRisks(t *testing.T) {
	t.Run("UnsupportedMultipleInputRequests", func(t *testing.T) {
		f := &scriptFixture{tools: []*scriptTool{{name: "ask", fn: func(*mcp.CallToolParamsRaw) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{
				"0": &mcp.ElicitParams{Message: "a"},
				"1": &mcp.ElicitParams{Message: "b"},
			}}, nil
		}}}}
		tool, _ := mrtrSet(t, f)
		_, err := tool.Call(mrtrCtx(), json.RawMessage(`{}`))
		if !errors.Is(err, ErrUnsupportedInputRequests) {
			t.Fatalf("want ErrUnsupportedInputRequests, got %v", err)
		}
	})

	t.Run("ResumeChildRejectsSecondResume", func(t *testing.T) {
		f := &scriptFixture{tools: []*scriptTool{{name: "ask", fn: func(p *mcp.CallToolParamsRaw) (*mcp.CallToolResult, error) {
			if len(p.InputResponses) == 0 {
				return mrtrInputRequired(map[string]any{"type": "boolean"}), nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
		}}}}
		tool, seam := mrtrSet(t, f)
		_, err := tool.Call(mrtrCtx(), json.RawMessage(`{}`))
		var susp *types.SuspendError
		if !errors.As(err, &susp) {
			t.Fatalf("Call: want SuspendError, got %v", err)
		}
		if _, err := seam.ResumeChild(mrtrCtx(), susp.Token, stores.ResumeInput{Verdict: stores.VerdictApprove}); err != nil {
			t.Fatalf("first ResumeChild: %v", err)
		}
		_, err = seam.ResumeChild(mrtrCtx(), susp.Token, stores.ResumeInput{Verdict: stores.VerdictApprove})
		if !errors.Is(err, ErrReplayStateLost) {
			t.Fatalf("second ResumeChild: want ErrReplayStateLost, got %v", err)
		}
		if f.count() != 2 {
			t.Errorf("tools/call count = %d, want 2", f.count())
		}
	})

	t.Run("ClientRequiresCheckpoints", func(t *testing.T) {
		ts := httptest.NewServer(http.NotFoundHandler())
		defer ts.Close()
		if _, err := Client(ts.URL); !errors.Is(err, ErrCheckpointsRequired) {
			t.Fatalf("want ErrCheckpointsRequired, got %v", err)
		}
	})
}
