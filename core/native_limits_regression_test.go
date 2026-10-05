package gohan

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"iter"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// batchSeqModel emits one whole batch of tool calls per model turn, in the
// order its steps list them, and answers with plain text once the steps run
// out. Unlike nrrModel it can put several calls in one response.
type batchSeqModel struct {
	mu      sync.Mutex
	emitted int
	steps   [][]types.ToolUse
}

func (m *batchSeqModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "native", Caps: types.Caps{Tools: true}}
}

func (m *batchSeqModel) Generate(_ context.Context, _ types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.emitted < len(m.steps) {
			for _, call := range m.steps[m.emitted] {
				cu := call
				yield(types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: &cu}, nil)
			}
			m.emitted++
			yield(types.ModelChunk{Finish: types.FinishToolUse}, nil)
			return
		}
		yield(types.ModelChunk{Kind: types.DeltaText, Delta: "done"}, nil)
		yield(types.ModelChunk{Finish: types.FinishStop}, nil)
	}
}

// driveApproval resumes the current token with an approval and reports the
// next suspension token plus the last stream error.
func driveApproval(t *testing.T, stack *Stack, token ResumeToken) (ResumeToken, error) {
	t.Helper()
	conv, err := NewNativeConversation(stack, "chat",
		WithConversationRuns(stack.stores.Runs),
		WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
		WithConversationApprovalPolicy(nrrAllowPolicy{}),
	)
	if err != nil {
		t.Fatalf("approval conversation: %v", err)
	}
	var next ResumeToken
	var lastErr error
	for ev, err := range conv.Resume(nrrCtx(), token, Approve()) {
		if s, ok := ev.(types.Suspended); ok {
			next = s.Token
		}
		if err != nil {
			lastErr = err
		}
	}
	return next, lastErr
}

// TestNativeMaxToolCallsPermitsPrepaidAsks drives a three-ask batch across
// fresh conversations: every prepaid ask executes exactly once, and a later
// fresh batch in the re-entered drive is refused against the restored total
// instead of drawing a fresh budget.
func TestNativeMaxToolCallsPermitsPrepaidAsks(t *testing.T) {
	model := &batchSeqModel{steps: [][]types.ToolUse{
		{nrrBook("c1"), nrrBook("c2"), nrrBook("c3")},
		{nrrBook("c4")},
	}}
	book := &bookTool{}
	note := &nrrNoteTool{}
	stack, conv := nrrStack(t, model, book, note, nil, nil,
		WithLimits("chat", nativeConvLimits(3)))

	var token ResumeToken
	for ev, err := range conv.Send(nrrCtx(), "s1", nlUser("go")) {
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if s, ok := ev.(types.Suspended); ok {
			token = s.Token
		}
	}
	if token == "" {
		t.Fatal("Send: no suspension")
	}
	if ran := book.ran(); len(ran) != 0 {
		t.Fatalf("tools executed before approval: %v", ran)
	}

	var overrun error
	for range 3 {
		next, err := driveApproval(t, stack, token)
		if err != nil {
			overrun = err
		}
		if next == "" {
			break
		}
		token = next
	}
	if !errors.Is(overrun, types.ErrBatchOverrun) {
		t.Fatalf("fourth call err = %v, want the batch overrun refusal", overrun)
	}
	if ran := book.ran(); len(ran) != 3 || ran[0] != "c1" || ran[1] != "c2" || ran[2] != "c3" {
		t.Fatalf("book executions = %v, want [c1 c2 c3] exactly once each", ran)
	}
}

// TestNativeRestoredToolTotalIsNotReservedTwice runs two fresh batches in
// one re-entered drive: the historical total restores on the first
// reservation only, so both batches stay inside MaxToolCalls instead of
// paying the restored slots twice.
func TestNativeRestoredToolTotalIsNotReservedTwice(t *testing.T) {
	model := &batchSeqModel{steps: [][]types.ToolUse{
		{nrrBook("c1")},
		{nrrNote("n1"), nrrNote("n2")},
		{nrrNote("n3")},
	}}
	book := &bookTool{}
	note := &nrrNoteTool{}
	stack, conv := nrrStack(t, model, book, note, nil, nil,
		WithLimits("chat", nativeConvLimits(4)))

	var token ResumeToken
	for ev, err := range conv.Send(nrrCtx(), "s1", nlUser("go")) {
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if s, ok := ev.(types.Suspended); ok {
			token = s.Token
		}
	}
	if token == "" {
		t.Fatal("Send: no suspension")
	}

	var lastErr error
	for {
		next, err := driveApproval(t, stack, token)
		if err != nil {
			lastErr = err
		}
		if next == "" {
			break
		}
		token = next
	}
	if lastErr != nil {
		t.Fatalf("drive err = %v, want both batches admitted", lastErr)
	}
	if ran := book.ran(); len(ran) != 1 || ran[0] != "c1" {
		t.Fatalf("book executions = %v, want [c1] once", ran)
	}
	if ran := note.ran(); len(ran) != 3 {
		t.Fatalf("note executions = %v, want [n1 n2 n3]", ran)
	}
}

// TestNativeConsumerStallConfigured pins the resolved ConsumerStall onto the
// native conversation's stall guard, so a configured value reaches the
// shipped entry instead of leaving detection disabled.
func TestNativeConsumerStallConfigured(t *testing.T) {
	model := &batchSeqModel{}
	limits := nativeConvLimits(5)
	limits.ConsumerStall = 250 * time.Millisecond
	stack, conv := nrrStack(t, model, &bookTool{}, &nrrNoteTool{}, nil, nil,
		WithLimits("chat", limits))
	if _, ok := stack.Limits("chat"); !ok {
		t.Fatal("stack lost the chat limits")
	}
	c, ok := conv.(*conversation)
	if !ok {
		t.Fatalf("conversation type %T, want *conversation", conv)
	}
	if c.stall != 250*time.Millisecond {
		t.Fatalf("stall guard limit = %v, want the configured 250ms", c.stall)
	}
}
