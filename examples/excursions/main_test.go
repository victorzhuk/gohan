package main

import (
	"context"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

func runTrip(t *testing.T) Trip {
	t.Helper()
	var trip Trip
	gohantest.LeakCheck(t, func() {
		var err error
		trip, err = Plan(context.Background(), "Plan a weekend in the Dolomites: find trails and book a cabin.")
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
	})
	return trip
}

func fakeTool(t *testing.T, tools []types.Tool, name string) *gohantest.FakeTool {
	t.Helper()
	for _, tool := range tools {
		if f, ok := tool.(*gohantest.FakeTool); ok && f.Spec().Name == name {
			return f
		}
	}
	t.Fatalf("no fixture tool %q", name)
	return nil
}

func TestExcursionsS1Offline(t *testing.T) {
	t.Run("offline excursion plan", func(t *testing.T) {
		trip := runTrip(t)
		var text strings.Builder
		sawAssistant, sawDone := false, false
		for _, ev := range trip.Events {
			switch e := ev.(type) {
			case types.TextDelta:
				text.WriteString(e.Delta)
			case types.AssistantMessage:
				sawAssistant = true
			case types.Done:
				sawDone = true
			}
		}
		if !sawAssistant || !sawDone {
			t.Fatalf("events %v, want an assistant message and a Done", trip.Events)
		}
		if got := text.String(); got != "Cabin denied by policy; pick from the 3 trails found instead." {
			t.Fatalf("streamed %q, want the scripted reply", got)
		}
	})

	t.Run("fixture policy denies the booking", func(t *testing.T) {
		trip := runTrip(t)
		if calls := fakeTool(t, trip.Tools, "book_cabin").Calls(); len(calls) != 0 {
			t.Fatalf("book_cabin executed %d times, want 0: %v", len(calls), calls)
		}
		if calls := fakeTool(t, trip.Tools, "search_trails").Calls(); len(calls) != 1 {
			t.Fatalf("search_trails executed %d times, want 1", len(calls))
		}
		var denied *types.ToolResult
		for i := range trip.History {
			m := &trip.History[i]
			for _, b := range m.Blocks {
				res, ok := b.(types.ToolResult)
				if ok && res.ID == "call_book_cabin" {
					denied = &res
				}
			}
		}
		if denied == nil {
			t.Fatalf("history %v, want a persisted result for call_book_cabin", trip.History)
		}
		if denied.Outcome != types.Failed || denied.Error == nil || denied.Error.Kind != types.Permanent {
			t.Fatalf("booking result %+v, want Failed(Permanent)", denied)
		}
		if denied.Error.Message != "not_executed: denied by policy" {
			t.Fatalf("denial message %q, want not_executed: denied by policy", denied.Error.Message)
		}
	})

	t.Run("batch history lands in call order", func(t *testing.T) {
		trip := runTrip(t)
		var pending, results *types.Message
		for i := range trip.History {
			m := &trip.History[i]
			for _, b := range m.Blocks {
				switch b.(type) {
				case types.ToolUse:
					pending = m
				case types.ToolResult:
					results = m
				}
			}
		}
		if pending == nil || results == nil {
			t.Fatalf("history %v, want a pending message and a results message", trip.History)
		}
		calls := []string{"search_trails", "book_cabin"}
		for i, want := range calls {
			use, ok := pending.Blocks[i].(types.ToolUse)
			if !ok || use.Name != want {
				t.Fatalf("pending block %d is %v, want ToolUse %q", i, pending.Blocks[i], want)
			}
			res, ok := results.Blocks[i].(types.ToolResult)
			if !ok || res.ID != "call_"+want {
				t.Fatalf("results block %d is %v, want ToolResult for call_%s", i, results.Blocks[i], want)
			}
		}
	})
}
