package gohan

import (
	"context"
	"encoding/json/jsontext"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// queueModel scripts one model turn that emits every scripted call in a
// single response, then answers with text once the history carries a tool
// result per call. It counts the requests, so a test can prove no model
// call runs between asks.
type queueModel struct {
	mu       sync.Mutex
	calls    []types.ToolUse
	requests int
}

func (m *queueModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "native", Caps: types.Caps{Tools: true}}
}

func (m *queueModel) Generate(_ context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		m.mu.Lock()
		m.requests++
		answered := 0
		for _, msg := range req.Messages {
			for _, b := range msg.Blocks {
				if _, ok := b.(types.ToolResult); ok {
					answered++
				}
			}
		}
		steps := append([]types.ToolUse(nil), m.calls...)
		m.mu.Unlock()
		if answered >= len(steps) {
			yield(types.ModelChunk{Kind: types.DeltaText, Delta: "done"}, nil)
			yield(types.ModelChunk{Finish: types.FinishStop}, nil)
			return
		}
		for i := range steps {
			call := steps[i]
			yield(types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: &call}, nil)
		}
		yield(types.ModelChunk{Finish: types.FinishToolUse}, nil)
	}
}

func (m *queueModel) requestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.requests
}

func seqResumeConversation(t *testing.T, stack *Stack) Conversation {
	t.Helper()
	conv, err := NewNativeConversation(stack, "chat",
		WithConversationRuns(stack.stores.Runs),
		WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
		WithConversationApprovalPolicy(nrrAllowPolicy{}),
	)
	if err != nil {
		t.Fatalf("second conversation: %v", err)
	}
	return conv
}

func seqSend(t *testing.T, conv Conversation) []Event {
	t.Helper()
	var evs []Event
	for ev, err := range conv.Send(nrrCtx(), "s1", nlUser("go")) {
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		evs = append(evs, ev)
	}
	return evs
}

func seqSuspend(t *testing.T, evs []Event) types.Suspended {
	t.Helper()
	var susp types.Suspended
	for _, ev := range evs {
		if s, ok := ev.(types.Suspended); ok {
			susp = s
		}
	}
	if susp.Token == "" {
		t.Fatal("no suspension")
	}
	return susp
}

func seqCollect(t *testing.T, conv Conversation, token ResumeToken, in stores.ResumeInput) (types.Suspended, bool) {
	t.Helper()
	var susp types.Suspended
	done := false
	for ev, err := range conv.Resume(nrrCtx(), token, in) {
		if err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if s, ok := ev.(types.Suspended); ok {
			susp = s
		}
		if _, ok := ev.(types.Done); ok {
			done = true
		}
	}
	return susp, done
}

func seqResultCount(t *testing.T, stack *Stack, sessionID, callID string) int {
	t.Helper()
	h, err := stack.stores.SessionLog.Load(nrrCtx(), sessionID)
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	n := 0
	for _, msg := range h.Messages {
		for _, b := range msg.Blocks {
			if res, ok := b.(types.ToolResult); ok && res.ID == callID {
				n++
			}
		}
	}
	return n
}

func TestNativeSequentialAsksOneRequestEach(t *testing.T) {
	model := &queueModel{calls: []types.ToolUse{nrrBook("c1"), nrrBook("c2"), nrrBook("c3")}}
	book := &bookTool{}
	note := &nrrNoteTool{}
	stack, conv := nrrStack(t, model, book, note, nil, nil)

	susp := seqSuspend(t, seqSend(t, conv))
	if susp.Reason != types.HumanApproval {
		t.Fatalf("reason = %v, want HumanApproval", susp.Reason)
	}
	req, ok := susp.Payload.(permission.ApprovalRequest)
	if !ok {
		t.Fatalf("payload %T, want permission.ApprovalRequest", susp.Payload)
	}
	if req.Call.ID != "c1" {
		t.Fatalf("payload call = %+v, want the head ask c1", req.Call)
	}
	env, st := persistedApproval(t, stack, susp.Token)
	if len(env.Approvals) != 1 || env.Approvals[0].Call.ID != "c1" {
		t.Fatalf("persisted approvals = %+v, want one for c1", env.Approvals)
	}
	if len(st.Pending) != 3 || st.Pending[0].ID != "c1" || st.Pending[1].ID != "c2" || st.Pending[2].ID != "c3" {
		t.Fatalf("pending queue = %+v, want [c1 c2 c3] in call order", st.Pending)
	}
	if ran := book.ran(); len(ran) != 0 {
		t.Fatalf("tool executed before any approval: %v", ran)
	}

	conv = seqResumeConversation(t, stack)
	rounds := 0
	for rounds < 3 {
		next, done := seqCollect(t, conv, susp.Token, Approve())
		rounds++
		if done {
			break
		}
		if next.Token == "" || next.Reason != types.HumanApproval {
			t.Fatalf("round %d: no follow-up HumanApproval suspension", rounds)
		}
		susp = next
	}
	if rounds != 3 {
		t.Fatalf("approval rounds = %d, want 3", rounds)
	}
	if ran := book.ran(); len(ran) != 3 {
		t.Fatalf("book executions = %v, want three calls run once each", ran)
	}
	if model.requestCount() != 2 {
		t.Fatalf("model requests = %d, want the initial batch turn and the final answer only", model.requestCount())
	}
}

func TestNativeQueuedAsksNeedDistinctApprovals(t *testing.T) {
	args := jsontext.Value(`{"id":"same"}`)
	model := &queueModel{calls: []types.ToolUse{
		{ID: "c1", Name: "book", Args: args},
		{ID: "c2", Name: "book", Args: args},
	}}
	book := &bookTool{}
	stack, conv := nrrStack(t, model, book, &nrrNoteTool{}, nil, nil)

	susp := seqSuspend(t, seqSend(t, conv))
	conv = seqResumeConversation(t, stack)
	next, done := seqCollect(t, conv, susp.Token, Approve())
	if done {
		t.Fatal("first approval ended the run: the second ask never happened")
	}
	if ran := book.ran(); len(ran) != 1 {
		t.Fatalf("book executions after first approval = %v, want exactly one", ran)
	}
	req, ok := next.Payload.(permission.ApprovalRequest)
	if !ok {
		t.Fatalf("payload %T, want permission.ApprovalRequest", next.Payload)
	}
	if req.Call.ID != "c2" {
		t.Fatalf("second payload call = %+v, want the queued c2", req.Call)
	}
	_, done = seqCollect(t, conv, next.Token, Approve())
	if !done {
		t.Fatal("second approval did not finish the run")
	}
	if ran := book.ran(); len(ran) != 2 {
		t.Fatalf("book executions = %v, want both identical asks executed once", ran)
	}
}

func TestNativeAskDecisionsDoNotRerunSettledAsks(t *testing.T) {
	model := &queueModel{calls: []types.ToolUse{nrrBook("c1"), nrrBook("c2")}}
	book := &bookTool{}
	stack, conv := nrrStack(t, model, book, &nrrNoteTool{}, nil, nil)

	susp := seqSuspend(t, seqSend(t, conv))
	conv = seqResumeConversation(t, stack)
	next, _ := seqCollect(t, conv, susp.Token, Approve())
	if next.Token == "" {
		t.Fatal("second ask never suspended")
	}
	if n := seqResultCount(t, stack, "s1", "c1"); n != 1 {
		t.Fatalf("c1 results in the session log = %d, want exactly one", n)
	}
	if ran := book.ran(); len(ran) != 1 {
		t.Fatalf("book executions = %v, want only the settled first ask", ran)
	}
	_, done := seqCollect(t, conv, next.Token, Approve())
	if !done {
		t.Fatal("second approval did not finish the run")
	}
	if n := seqResultCount(t, stack, "s1", "c1"); n != 1 {
		t.Fatalf("c1 results after the second approval = %d, want still exactly one", n)
	}
}

func TestNativeRejectSettlesOnlyTheActiveAsk(t *testing.T) {
	model := &queueModel{calls: []types.ToolUse{nrrBook("c1"), nrrBook("c2")}}
	book := &bookTool{}
	stack, conv := nrrStack(t, model, book, &nrrNoteTool{}, nil, nil)

	susp := seqSuspend(t, seqSend(t, conv))
	conv = seqResumeConversation(t, stack)
	next, done := seqCollect(t, conv, susp.Token, Reject("no"))
	if done {
		t.Fatal("first rejection ended the run: the second ask never happened")
	}
	if next.Reason != types.HumanApproval {
		t.Fatalf("reason after rejection = %v, want HumanApproval for the next ask", next.Reason)
	}
	req, ok := next.Payload.(permission.ApprovalRequest)
	if !ok {
		t.Fatalf("payload %T, want permission.ApprovalRequest", next.Payload)
	}
	if req.Call.ID != "c2" {
		t.Fatalf("payload after rejection = %+v, want the still-queued c2", req.Call)
	}
	if ran := book.ran(); len(ran) != 0 {
		t.Fatalf("book executions = %v, want none", ran)
	}
	if model.requestCount() != 1 {
		t.Fatalf("model requests = %d, want no model call between asks", model.requestCount())
	}
	h, err := stack.stores.SessionLog.Load(nrrCtx(), "s1")
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	var rejected *types.ToolResult
	for _, msg := range h.Messages {
		for _, b := range msg.Blocks {
			if res, ok := b.(types.ToolResult); ok && res.ID == "c1" {
				rejected = &res
			}
		}
	}
	if rejected == nil || rejected.Outcome != types.Failed || rejected.Error == nil ||
		rejected.Error.Kind != types.Permanent || rejected.Error.Message != runtime.NotExecutedPrefix+"rejected by owner" {
		t.Fatalf("c1 result = %+v, want one permanent not_executed rejection", rejected)
	}
	_, done = seqCollect(t, conv, next.Token, Approve())
	if !done {
		t.Fatal("second approval did not finish the run")
	}
	if ran := book.ran(); len(ran) != 1 {
		t.Fatalf("book executions = %v, want only the second ask", ran)
	}
}
