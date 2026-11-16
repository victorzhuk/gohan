package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

const loopPrincipalSubject = "user-loop"

type childResumer interface {
	ResumeChild(ctx context.Context, t types.ResumeToken, in stores.ResumeInput) (json.RawMessage, error)
}

type rtIn struct {
	Q string `json:"q"`
}

type rtOut struct {
	A string `json:"a"`
}

type loopFlow struct {
	mu      sync.Mutex
	invokes int
	resumes int

	resumeToken  types.ResumeToken
	resumeInput  stores.ResumeInput
	invokeResult *types.SuspendError
	resumeOut    rtOut
}

func (f *loopFlow) Invoke(context.Context, rtIn) (rtOut, error) {
	f.mu.Lock()
	f.invokes++
	f.mu.Unlock()
	return rtOut{}, f.invokeResult
}

func (f *loopFlow) Resume(_ context.Context, t types.ResumeToken, in stores.ResumeInput) (rtOut, error) {
	f.mu.Lock()
	f.resumes++
	f.resumeToken = t
	f.resumeInput = in
	out := f.resumeOut
	f.mu.Unlock()
	return out, nil
}

func (f *loopFlow) snapshot() (invokes, resumes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.invokes, f.resumes
}

func loopContext() context.Context {
	ctx := types.WithRunInfo(context.Background(), types.RunInfo{SessionID: "s-loop", RunID: "r-loop"})
	return types.WithToolUse(ctx, types.ToolUse{ID: "call-1", Name: "loop__roundtrip"})
}

func loopServer(t *testing.T, opts ...ServerOption) *httptest.Server {
	t.Helper()
	handler, err := NewHandler(append([]ServerOption{
		WithStores(stores.NewMemoryRuns(), stores.NewMemoryEventLog()),
		WithPrincipalSource(func(*http.Request) (types.Principal, error) {
			return types.Principal{Subject: loopPrincipalSubject, Tenant: "acme"}, nil
		}),
	}, opts...)...)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

func loopClient(t *testing.T, url string) (*Set, types.Tool, childResumer) {
	t.Helper()
	set, err := Client(url,
		Namespace("loop"),
		WithCheckpoints(stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom))),
		WithRefreshInterval(50*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tools, err := set.Tools(ctx)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	for _, tool := range tools {
		name := tool.Spec().Name
		seam, ok := tool.(childResumer)
		if !ok {
			t.Fatalf("tool %s does not expose ResumeChild", name)
		}
		if name == "loop__roundtrip" || name == "loop__approve" {
			return set, tool, seam
		}
	}
	t.Fatalf("imported tools: got %d, want the loop tool", len(tools))
	return nil, nil, nil
}

func requireSuspend(t *testing.T, err error, want types.SuspendReason) *types.SuspendError {
	t.Helper()
	var susp *types.SuspendError
	if !errors.As(err, &susp) {
		t.Fatalf("err %v: want suspend with %s", err, want)
	}
	if susp.Reason != want {
		t.Fatalf("reason %q: want %q", susp.Reason, want)
	}
	if susp.Token == "" {
		t.Fatal("suspension carries no token")
	}
	return susp
}

func loopContextTimeout() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(loopContext(), 10*time.Second)
	return ctx, cancel
}

func TestLoopback(t *testing.T) {
	t.Run("interop.awaiting-input-roundtrip", func(t *testing.T) {
		flow := &loopFlow{
			invokeResult: &types.SuspendError{
				Token:  "cp_rt",
				Reason: types.AwaitingInput,
				Payload: types.InputRequest{
					Prompt: "pick one",
					Schema: json.RawMessage(`{"type":"object","properties":{"choice":{"type":"string"}},"required":["choice"]}`),
				},
			},
			resumeOut: rtOut{A: "answered"},
		}
		ts := loopServer(t, Expose("roundtrip", flow,
			InputSchema(json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)),
			OutputSchema(json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"}}}`)),
		))
		_, tool, seam := loopClient(t, ts.URL)

		_, err := tool.Call(loopContext(), json.RawMessage(`{"q":"hi"}`))
		susp := requireSuspend(t, err, types.AwaitingInput)

		ctx, cancel := loopContextTimeout()
		defer cancel()
		out, err := seam.ResumeChild(ctx, susp.Token, gohan.Deliver(json.RawMessage(`{"choice":"a"}`)))
		if err != nil {
			t.Fatalf("ResumeChild: %v", err)
		}
		var res map[string]any
		if json.Unmarshal(out, &res) != nil {
			t.Fatalf("resume bytes %s: want the flow output", out)
		}
		if res["a"] != "answered" {
			t.Fatalf("resume = %s, want the delivered choice", out)
		}

		invokes, resumes := flow.snapshot()
		if resumes != 1 {
			t.Fatalf("flow resumes = %d, want 1", resumes)
		}
		if invokes != 1 {
			t.Fatalf("flow invokes = %d, want 1", invokes)
		}
		if flow.resumeToken != "cp_rt" {
			t.Fatalf("resume token %q, want cp_rt", flow.resumeToken)
		}
		if string(flow.resumeInput.Data) != `{"choice":"a"}` {
			t.Fatalf("delivered data %s, want the choice payload", flow.resumeInput.Data)
		}
		if flow.resumeInput.Approver == nil {
			t.Fatal("resume carries no approver")
		}
		if flow.resumeInput.Approver.Subject != loopPrincipalSubject {
			t.Fatalf("approver subject %q, want %q", flow.resumeInput.Approver.Subject, loopPrincipalSubject)
		}

		if _, err := seam.ResumeChild(ctx, "cp_rt", gohan.Deliver(json.RawMessage(`{"choice":"b"}`))); !errors.Is(err, ErrReplayStateLost) {
			t.Fatalf("second resume: %v, want ErrReplayStateLost", err)
		}
	})

	t.Run("interop.awaiting-input-invalid", func(t *testing.T) {
		flow := &loopFlow{
			invokeResult: &types.SuspendError{
				Token:  "cp_invalid",
				Reason: types.AwaitingInput,
				Payload: types.InputRequest{
					Prompt: "pick one",
					Schema: json.RawMessage(`{"type":"object","properties":{"choice":{"type":"string"}},"required":["choice"]}`),
				},
			},
			resumeOut: rtOut{A: "answered"},
		}
		ts := loopServer(t, Expose("roundtrip", flow,
			InputSchema(json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)),
			OutputSchema(json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"}}}`)),
		))
		_, tool, seam := loopClient(t, ts.URL)

		_, err := tool.Call(loopContext(), json.RawMessage(`{"q":"hi"}`))
		susp := requireSuspend(t, err, types.AwaitingInput)

		ctx, cancel := loopContextTimeout()
		defer cancel()
		_, err = seam.ResumeChild(ctx, susp.Token, gohan.Deliver(json.RawMessage(`{"choice":123}`)))
		if !errors.Is(err, types.ErrInputInvalid) {
			t.Fatalf("ResumeChild with non-conforming data: %v, want ErrInputInvalid", err)
		}

		invokes, resumes := flow.snapshot()
		if resumes != 0 {
			t.Fatalf("flow resumes = %d, want 0 after rejected delivery", resumes)
		}

		out, err := seam.ResumeChild(ctx, susp.Token, gohan.Deliver(json.RawMessage(`{"choice":"a"}`)))
		if err != nil {
			t.Fatalf("ResumeChild after rejection: %v", err)
		}
		var res map[string]any
		if json.Unmarshal(out, &res) != nil || res["a"] != "answered" {
			t.Fatalf("resume = %s, want the flow output", out)
		}
		invokes, resumes = flow.snapshot()
		if invokes != 1 || resumes != 1 {
			t.Fatalf("flow invokes %d resumes %d, want 1 and 1", invokes, resumes)
		}
	})
}

func TestLoopbackApproval(t *testing.T) {
	newFlow := func() *loopFlow {
		return &loopFlow{
			invokeResult: &types.SuspendError{
				Token:  "cp_approve",
				Reason: types.HumanApproval,
				Payload: permission.ApprovalRequest{
					Call: types.ToolUse{ID: "c1", Name: "create_booking"},
				},
			},
			resumeOut: rtOut{A: "approved"},
		}
	}

	t.Run("approve", func(t *testing.T) {
		flow := newFlow()
		ts := loopServer(t, Expose("approve", flow))
		_, tool, seam := loopClient(t, ts.URL)

		ctx, cancel := loopContextTimeout()
		defer cancel()
		_, err := tool.Call(loopContext(), json.RawMessage(`{"q":"hi"}`))
		susp := requireSuspend(t, err, types.HumanApproval)
		req, ok := susp.Payload.(permission.ApprovalRequest)
		if !ok {
			t.Fatalf("payload %T, want permission.ApprovalRequest", susp.Payload)
		}
		if req.Call.Name != "loop__approve" {
			t.Fatalf("pending call %q, want loop__approve", req.Call.Name)
		}

		out, err := seam.ResumeChild(ctx, susp.Token, gohan.Approve())
		if err != nil {
			t.Fatalf("ResumeChild approve: %v", err)
		}
		if string(out) == "" {
			t.Fatal("resume carries no bytes")
		}
		invokes, resumes := flow.snapshot()
		if invokes != 1 || resumes != 1 {
			t.Fatalf("tools/call requests: invoke %d resume %d, want 1 and 1", invokes, resumes)
		}
		if flow.resumeInput.Verdict != stores.VerdictApprove {
			t.Fatalf("verdict %v, want approve", flow.resumeInput.Verdict)
		}
		if flow.resumeInput.Approver == nil || flow.resumeInput.Approver.Subject != loopPrincipalSubject {
			t.Fatalf("approver %+v, want subject %q", flow.resumeInput.Approver, loopPrincipalSubject)
		}
	})

	t.Run("reject", func(t *testing.T) {
		flow := newFlow()
		ts := loopServer(t, Expose("approve", flow))
		_, tool, seam := loopClient(t, ts.URL)

		ctx, cancel := loopContextTimeout()
		defer cancel()
		_, err := tool.Call(loopContext(), json.RawMessage(`{"q":"hi"}`))
		susp := requireSuspend(t, err, types.HumanApproval)

		if _, err := seam.ResumeChild(ctx, susp.Token, gohan.Reject("no")); err != nil {
			t.Fatalf("ResumeChild reject: %v", err)
		}
		if flow.resumeInput.Verdict != stores.VerdictReject {
			t.Fatalf("verdict %v, want reject", flow.resumeInput.Verdict)
		}
		if flow.resumeInput.Reason != "no" {
			t.Fatalf("reason %q, want \"no\"", flow.resumeInput.Reason)
		}
	})
}
