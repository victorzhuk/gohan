package gohan

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// A steer received while Send drives the run is appended to the session
// history before SteerApplied acknowledges it, and the turn after the safe
// point is built from a history that carries the steer.
func TestConversationSteerPersisted(t *testing.T) {
	ctx := principalCtx(context.Background())
	sid := "sess-steer-persist"
	log := steerLog(t)
	runs := steerNewRuns()
	events := stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))
	stack, err := Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	stack.stores = stores.Stores{SessionLog: log}
	rt := &steerRT{log: log, sess: sid, gate: make(chan struct{}), entered: make(chan struct{})}
	conv, err := NewConversation(stack, "chat", rt,
		WithConversationRuns(runs),
		WithConversationEventLog(events),
	)
	if err != nil {
		t.Fatalf("new conversation: %v", err)
	}

	res := make(chan streamResult, 1)
	go func() { res <- collectStream(conv.Send(ctx, sid, userMsg("hi"))) }()
	<-rt.entered
	if err := conv.Steer(ctx, sid, steerMsg("use the cheaper carrier", "steer-1")); err != nil {
		t.Fatalf("steer: %v", err)
	}
	close(rt.gate)
	got := <-res
	if got.err != nil {
		t.Fatalf("send: %v", got.err)
	}

	applied := false
	for _, ev := range got.evs {
		if sa, ok := ev.(types.SteerApplied); ok && sa.MessageID == "steer-1" {
			applied = true
		}
	}
	if !applied {
		t.Fatalf("no SteerApplied for steer-1 in %d events", len(got.evs))
	}

	h, err := log.Load(ctx, sid)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	sig := historySig(h)
	steers := 0
	for _, s := range sig {
		if s == "user:use the cheaper carrier" {
			steers++
		}
	}
	if steers != 1 {
		t.Fatalf("steer appended %d times in %v, want once", steers, sig)
	}
	// The steer sits after the first turn's results and before the final
	// assistant reply, so the request the next turn assembled carried it.
	lastAssistant := -1
	for i, s := range sig {
		if strings.HasPrefix(s, "assistant:") {
			lastAssistant = i
		}
	}
	steerAt := -1
	for i, s := range sig {
		if s == "user:use the cheaper carrier" {
			steerAt = i
		}
	}
	if steerAt < 0 || lastAssistant < 0 || steerAt >= lastAssistant {
		t.Fatalf("steer at %d not before the final assistant at %d in %v", steerAt, lastAssistant, sig)
	}
}
