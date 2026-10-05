package gohan

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// Two Conversation instances over one runs store and one event log must
// not mint colliding run ids: a per-process counter names two runs alike,
// the second Start overwrites the first row and both sessions' events land
// in one log under one id.
func TestConversationRunIdentity(t *testing.T) {
	ctx := principalCtx(context.Background())
	log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	runs := &runsFixture{
		MemoryRuns: stores.NewMemoryRuns(
			stores.WithMemoryRunClock(time.Now),
			stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
				p, ok := types.PrincipalFrom(ctx)
				return types.RunInfo{Principal: p}, ok
			}),
		),
		onSess: map[string]string{},
	}
	events := stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))
	stack, err := Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	stack.stores = stores.Stores{SessionLog: log}
	newConv := func(rt runtime.Runtime) Conversation {
		t.Helper()
		conv, err := NewConversation(stack, "chat", rt,
			WithConversationRuns(runs),
			WithConversationEventLog(events),
		)
		if err != nil {
			t.Fatalf("new conversation: %v", err)
		}
		return conv
	}

	res1 := collectStream(newConv(&convRT{steps: 1}).Send(ctx, "s1", userMsg("hi")))
	res2 := collectStream(newConv(&convRT{steps: 1}).Send(ctx, "s2", userMsg("hi")))
	if res1.err != nil {
		t.Fatalf("first send: %v", res1.err)
	}
	if res2.err != nil {
		t.Fatalf("second send: %v", res2.err)
	}

	id1, ok1 := runs.onSess["s1"]
	id2, ok2 := runs.onSess["s2"]
	if !ok1 || !ok2 {
		t.Fatalf("run rows missing: s1=%q ok=%v s2=%q ok=%v", id1, ok1, id2, ok2)
	}
	if id1 == id2 {
		t.Fatalf("both sessions recorded run id %q", id1)
	}
	for _, id := range []string{id1, id2} {
		if !strings.HasPrefix(id, "run-") || len(id) != len("run-")+32 {
			t.Errorf("run id %q: want run- plus 32 hex characters", id)
		}
	}

	for _, id := range []string{id1, id2} {
		var done bool
		n := int64(0)
		for e, err := range events.Read(ctx, id, 0) {
			if err != nil {
				t.Fatalf("read %s: %v", id, err)
			}
			n++
			if e.Meta.RunID != id {
				t.Errorf("event %d of %s carries run id %q", n, id, e.Meta.RunID)
			}
			if _, ok := e.Payload.(types.Done); ok {
				done = true
			}
		}
		if n == 0 {
			t.Errorf("run %s recorded no events", id)
		}
		if !done {
			t.Errorf("run %s recorded no Done event", id)
		}
	}
}
