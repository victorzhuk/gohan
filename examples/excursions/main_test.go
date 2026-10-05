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
		if len(trip.Batch.Results) != 2 {
			t.Fatalf("batch settled %d results, want 2: %+v", len(trip.Batch.Results), trip.Batch.Results)
		}
		if trip.Batch.Suspend != nil {
			t.Fatalf("batch suspended at %+v, want no ask", trip.Batch.Suspend)
		}
		denied := trip.Batch.Results[1]
		if denied.Call.Name != "book_cabin" {
			t.Fatalf("second call %q, want book_cabin", denied.Call.Name)
		}
		res := denied.Result
		if res.Outcome != types.Failed || res.Error == nil || res.Error.Kind != types.Permanent {
			t.Fatalf("booking result %+v, want Failed(Permanent)", res)
		}
		if res.Error.Message != "not_executed: denied by policy" {
			t.Fatalf("denial message %q, want not_executed: denied by policy", res.Error.Message)
		}
		lookup := trip.Batch.Results[0]
		if lookup.Call.Name != "search_trails" || lookup.Result.Outcome != types.Succeeded {
			t.Fatalf("lookup result %+v, want an executed search_trails", lookup)
		}
	})

	t.Run("batch history lands in call order", func(t *testing.T) {
		trip := runTrip(t)
		var pending, results *types.Message
		for i := range trip.Appended {
			m := &trip.Appended[i]
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
			t.Fatalf("appended %v, want a pending message and a results message", trip.Appended)
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
		var historyToolUses, historyToolResults int
		for _, m := range trip.History {
			for _, b := range m.Blocks {
				switch b.(type) {
				case types.ToolUse:
					historyToolUses++
				case types.ToolResult:
					historyToolResults++
				}
			}
		}
		if historyToolUses != 2 || historyToolResults != 2 {
			t.Fatalf("history holds %d tool uses and %d results, want 2 and 2", historyToolUses, historyToolResults)
		}
	})
}
