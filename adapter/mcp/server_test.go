package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

const (
	testPrincipalSubject = "user-1"
	testToolName         = "create_booking"
)

func testPrincipal(*http.Request) (types.Principal, error) {
	return types.Principal{Subject: testPrincipalSubject, Tenant: "tenant-1"}, nil
}

type resumeRecord struct {
	token types.ResumeToken
	input stores.ResumeInput
}

// scriptedFlow is a Flow whose Invoke and Resume are test closures.
type scriptedFlow struct {
	invokeFn func(ctx context.Context, in map[string]any) (map[string]any, error)
	resumeFn func(ctx context.Context, token types.ResumeToken, in stores.ResumeInput) (map[string]any, error)
}

func (f *scriptedFlow) Invoke(ctx context.Context, in map[string]any) (map[string]any, error) {
	return f.invokeFn(ctx, in)
}

func (f *scriptedFlow) Resume(ctx context.Context, token types.ResumeToken, in stores.ResumeInput) (map[string]any, error) {
	return f.resumeFn(ctx, token, in)
}

func startServer(t *testing.T, opts ...ServerOption) *sdk.ClientSession {
	t.Helper()
	base := []ServerOption{
		WithPrincipalSource(testPrincipal),
		WithStores(stores.NewMemoryRuns(), stores.NewMemoryEventLog()),
	}
	handler, err := NewHandler(append(base, opts...)...)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, &sdk.ClientOptions{
		MultiRoundTrip: &sdk.MultiRoundTripOptions{Disabled: true},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	cs, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: server.URL}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func callTool(t *testing.T, cs *sdk.ClientSession, params *sdk.CallToolParams) *sdk.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, params)
	if err != nil {
		t.Fatalf("tools/call %s: %v", params.Name, err)
	}
	return res
}

func gohanMeta(t *testing.T, res *sdk.CallToolResult) map[string]any {
	t.Helper()
	raw, err := json.Marshal(res.Meta)
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}
	g, _ := meta[MetaKeyGohan].(map[string]any)
	if g == nil {
		t.Fatalf("no %s key in meta: %v", MetaKeyGohan, meta)
	}
	return g
}

func TestServerSuspensionTokenError(t *testing.T) {
	t.Run("interop.server-suspension-token-error", func(t *testing.T) {
		flow := &scriptedFlow{
			invokeFn: func(ctx context.Context, in map[string]any) (map[string]any, error) {
				return nil, &types.SuspendError{
					Token:  "cp_test",
					Reason: types.HumanApproval,
					Payload: permission.ApprovalRequest{
						Call: types.ToolUse{Name: testToolName},
					},
				}
			},
		}
		for range 2 {
			cs := startServer(t, Expose(testToolName, flow))
			res := callTool(t, cs, &sdk.CallToolParams{Name: testToolName})
			if res.IsError {
				t.Fatalf("suspension became an error result: %+v", res)
			}
			if len(res.InputRequests) == 0 {
				t.Fatalf("no input requests in reply: %+v", res)
			}
			if got := gohanMeta(t, res)[MetaKeyToken]; got != "cp_test" {
				t.Fatalf("token = %v, want cp_test", got)
			}
			if res.RequestState == "" {
				t.Fatal("empty request state")
			}
		}
	})
}

func TestServerInputRequired(t *testing.T) {
	t.Run("interop.server-input-required", func(t *testing.T) {
		flow := &scriptedFlow{
			invokeFn: func(ctx context.Context, in map[string]any) (map[string]any, error) {
				return nil, &types.SuspendError{
					Token:  "cp_input",
					Reason: types.HumanApproval,
					Payload: permission.ApprovalRequest{
						Call:        types.ToolUse{Name: testToolName},
						Consequence: "deletes the booking",
					},
				}
			},
		}
		cs := startServer(t, Expose(testToolName, flow))
		res := callTool(t, cs, &sdk.CallToolParams{Name: testToolName})
		if res.IsError {
			t.Fatalf("suspension became an error result: %+v", res)
		}
		if len(res.InputRequests) != 1 {
			t.Fatalf("input requests = %d, want 1", len(res.InputRequests))
		}
		ep, ok := res.InputRequests["0"].(*sdk.ElicitParams)
		if !ok {
			t.Fatalf("input request is not ElicitParams: %T", res.InputRequests["0"])
		}
		schemaRaw, err := json.Marshal(ep.RequestedSchema)
		if err != nil {
			t.Fatalf("marshal requested schema: %v", err)
		}
		var schema map[string]any
		if err := json.Unmarshal(schemaRaw, &schema); err != nil || schema["type"] != "boolean" {
			t.Fatalf("requested schema = %s, want boolean type", schemaRaw)
		}
		if ep.Message != "deletes the booking" {
			t.Fatalf("message = %q, want the approval consequence", ep.Message)
		}
	})
}

func TestServerStatelessResume(t *testing.T) {
	t.Run("interop.server-stateless-resume", func(t *testing.T) {
		var records []resumeRecord
		suspending := &scriptedFlow{
			invokeFn: func(ctx context.Context, in map[string]any) (map[string]any, error) {
				return nil, &types.SuspendError{Token: "cp_resume", Reason: types.HumanApproval}
			},
		}
		csA := startServer(t, Expose(testToolName, suspending))
		first := callTool(t, csA, &sdk.CallToolParams{Name: testToolName})
		token := first.RequestState

		resuming := &scriptedFlow{
			resumeFn: func(ctx context.Context, token types.ResumeToken, in stores.ResumeInput) (map[string]any, error) {
				records = append(records, resumeRecord{token: token, input: in})
				return map[string]any{"status": "booked"}, nil
			},
		}
		csB := startServer(t, Expose(testToolName, resuming))
		second := callTool(t, csB, &sdk.CallToolParams{
			Name:         testToolName,
			RequestState: token,
			InputResponses: sdk.InputResponseMap{
				"0": &sdk.ElicitResult{Action: "accept"},
			},
		})
		if second.IsError {
			t.Fatalf("resume failed: %+v", second)
		}
		if len(records) != 1 {
			t.Fatalf("resume calls = %d, want 1", len(records))
		}
		rec := records[0]
		if rec.input.Approver == nil {
			t.Fatal("no approver on resume input")
		}
		if rec.input.Approver.Subject != testPrincipalSubject {
			t.Fatalf("approver subject = %q, want %q", rec.input.Approver.Subject, testPrincipalSubject)
		}
		if string(rec.token) != "cp_resume" {
			t.Fatalf("resume token = %q, want cp_resume", rec.token)
		}
		if second.StructuredContent == nil {
			t.Fatal("no structured content in resume reply")
		}
	})
}

func TestServerSuspensionsNeverProtocolErrors(t *testing.T) {
	t.Run("interop.server-suspensions-never-protocol-errors", func(t *testing.T) {
		wake := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
		flow := &scriptedFlow{
			invokeFn: func(ctx context.Context, in map[string]any) (map[string]any, error) {
				return nil, &types.SuspendError{
					Token:  "cp_task",
					Reason: types.AwaitingExternal,
					WakeAt: wake,
				}
			},
		}
		cs := startServer(t, Expose(testToolName, flow, Tasks()))
		res := callTool(t, cs, &sdk.CallToolParams{Name: testToolName})
		if res.IsError {
			t.Fatalf("task suspension became an error result: %+v", res)
		}
		g := gohanMeta(t, res)
		if _, ok := g[MetaKeyTask]; !ok {
			t.Fatalf("no task ref in meta: %v", g)
		}
		wakeAt, _ := g[MetaKeyWakeAt].(string)
		if wakeAt == "" {
			t.Fatalf("no wakeAt in meta: %v", g)
		}
		parsed, err := time.Parse(time.RFC3339, wakeAt)
		if err != nil || !parsed.Equal(wake) {
			t.Fatalf("wakeAt = %q, want %s", wakeAt, wake)
		}
	})
}

func TestServerAwaitingInputToken(t *testing.T) {
	t.Run("interop.server-awaiting-input-token", func(t *testing.T) {
		const choiceSchema = `{"type":"object","properties":{"choice":{"type":"string"}},"required":["choice"]}`
		var records []resumeRecord
		suspending := &scriptedFlow{
			invokeFn: func(ctx context.Context, in map[string]any) (map[string]any, error) {
				return nil, &types.SuspendError{
					Token:   "cp_choice",
					Reason:  types.AwaitingInput,
					Payload: types.InputRequest{Prompt: "pick a choice", Schema: json.RawMessage(choiceSchema)},
				}
			},
		}
		resumed := false
		delivering := &scriptedFlow{
			resumeFn: func(ctx context.Context, token types.ResumeToken, in stores.ResumeInput) (map[string]any, error) {
				resumed = true
				records = append(records, resumeRecord{token: token, input: in})
				return map[string]any{"ok": true}, nil
			},
		}
		csA := startServer(t, Expose(testToolName, suspending))
		res := callTool(t, csA, &sdk.CallToolParams{Name: testToolName})
		if res.IsError {
			t.Fatalf("suspension became an error result: %+v", res)
		}

		csB := startServer(t, Expose(testToolName, delivering, InputSchema(json.RawMessage(choiceSchema))))
		retry := callTool(t, csB, &sdk.CallToolParams{
			Name:         testToolName,
			RequestState: res.RequestState,
			InputResponses: sdk.InputResponseMap{
				"0": &sdk.ElicitResult{Action: "accept", Content: map[string]any{"choice": "a"}},
			},
		})
		if retry.IsError {
			t.Fatalf("resume failed: %+v", retry)
		}
		if len(records) != 1 || !resumed {
			t.Fatalf("resume calls = %d resumed = %v, want 1 true", len(records), resumed)
		}
		in := records[0].input
		if in.Approver == nil || in.Approver.Subject != testPrincipalSubject {
			t.Fatalf("approver = %+v, want principal %q", in.Approver, testPrincipalSubject)
		}
		if string(in.Data) != `{"choice":"a"}` {
			t.Fatalf("delivered data = %s", in.Data)
		}

		resumed = false
		records = nil
		csC := startServer(t, Expose(testToolName, delivering, InputSchema(json.RawMessage(choiceSchema))))
		bad := callTool(t, csC, &sdk.CallToolParams{
			Name:         testToolName,
			RequestState: res.RequestState,
			InputResponses: sdk.InputResponseMap{
				"0": &sdk.ElicitResult{Action: "accept", Content: map[string]any{"choice": 5}},
			},
		})
		if !bad.IsError {
			t.Fatalf("bad content accepted: %+v", bad)
		}
		if resumed || len(records) != 0 {
			t.Fatal("Flow.Resume ran on invalid content")
		}
	})
}
