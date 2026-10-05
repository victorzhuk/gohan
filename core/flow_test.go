package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"reflect"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type itinerary struct {
	City    string   `json:"city"`
	Carrier string   `json:"carrier"`
	Stops   []string `json:"stops"`
}

func collectFlowEvents(t *testing.T, seq iter.Seq2[types.Event, error]) []types.Event {
	t.Helper()
	var evs []types.Event
	for e, err := range seq {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		evs = append(evs, e)
	}
	return evs
}

func TestFlowContract(t *testing.T) {
	t.Run("flow.plain-invoke", func(t *testing.T) {
		f := FlowFunc[string, string]("echo", func(_ context.Context, in string) (string, error) {
			return "x:" + in, nil
		})
		out, err := f.Invoke(WithPrincipal(context.Background(), types.Principal{Subject: "u"}), "in")
		if err != nil {
			t.Fatalf("Invoke: %v", err)
		}
		if out != "x:in" {
			t.Fatalf("got %q, want %q", out, "x:in")
		}
	})

	t.Run("flow.not-suspendable", func(t *testing.T) {
		f := FlowFunc[string, string]("plain", func(_ context.Context, in string) (string, error) {
			return in, nil
		})
		_, err := f.Resume(context.Background(), "tok", stores.ResumeInput{})
		if !errors.Is(err, types.ErrNotSuspendable) {
			t.Fatalf("got %v, want types.ErrNotSuspendable", err)
		}
	})

	t.Run("flow.typed-result-on-conversation", func(t *testing.T) {
		want := itinerary{City: "Kyiv", Carrier: "rail", Stops: []string{"Lviv", "Odesa"}}
		step := &flowStep[string, itinerary]{fn: func(_ context.Context, _ string) (itinerary, error) {
			return want, nil
		}, in: "in"}
		evs := collectFlowEvents(t, driveFlow[string, itinerary](context.Background(), step))
		if len(evs) != 2 {
			t.Fatalf("got %d events, want 2", len(evs))
		}
		am, ok := evs[0].(types.AssistantMessage)
		if !ok {
			t.Fatalf("event 0 is %T, want types.AssistantMessage", evs[0])
		}
		if am.Message.Role != types.RoleAssistant || len(am.Message.Blocks) != 1 {
			t.Fatalf("assistant message: role %v, %d blocks", am.Message.Role, len(am.Message.Blocks))
		}
		tb, ok := am.Message.Blocks[0].(types.Text)
		if !ok {
			t.Fatalf("block is %T, want types.Text", am.Message.Blocks[0])
		}
		d, ok := evs[1].(types.Done)
		if !ok {
			t.Fatalf("event 1 is %T, want types.Done", evs[1])
		}
		var got itinerary
		if err := json.Unmarshal(d.Result, &got); err != nil {
			t.Fatalf("Done.Result: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Done.Result decodes to %+v, want %+v", got, want)
		}
		var fromMsg itinerary
		if err := json.Unmarshal([]byte(tb.Text), &fromMsg); err != nil {
			t.Fatalf("message text: %v", err)
		}
		if !reflect.DeepEqual(fromMsg, want) {
			t.Fatalf("message text decodes to %+v, want %+v", fromMsg, want)
		}
	})

	t.Run("message-json-matches-result-bytes", func(t *testing.T) {
		want := itinerary{City: "Lviv", Carrier: "bus"}
		step := &flowStep[string, itinerary]{fn: func(_ context.Context, _ string) (itinerary, error) {
			return want, nil
		}, in: "in"}
		evs := collectFlowEvents(t, driveFlow[string, itinerary](context.Background(), step))
		am := evs[0].(types.AssistantMessage)
		tb := am.Message.Blocks[0].(types.Text)
		if tb.Text != string(step.raw) {
			t.Fatal("message text and Done.Result carry different bytes")
		}
		var round itinerary
		if err := json.Unmarshal([]byte(tb.Text), &round); err != nil {
			t.Fatalf("canonical JSON: %v", err)
		}
		if !reflect.DeepEqual(round, want) {
			t.Fatalf("canonical JSON decodes to %+v, want %+v", round, want)
		}
	})

	t.Run("string-result-has-no-json-block", func(t *testing.T) {
		step := &flowStep[string, string]{fn: func(_ context.Context, _ string) (string, error) {
			return "plain text", nil
		}, in: "in"}
		evs := collectFlowEvents(t, driveFlow[string, string](context.Background(), step))
		if len(evs) != 1 {
			t.Fatalf("got %d events, want 1 (Done only)", len(evs))
		}
		d, ok := evs[0].(types.Done)
		if !ok {
			t.Fatalf("event is %T, want types.Done", evs[0])
		}
		if d.Result != nil {
			t.Fatalf("string flow carried Result %s, want none", d.Result)
		}
	})

	t.Run("error-yielded-once", func(t *testing.T) {
		boom := errors.New("boom")
		step := &flowStep[string, string]{fn: func(_ context.Context, _ string) (string, error) {
			return "", boom
		}, in: "in"}
		errCount := 0
		doneCount := 0
		var lastErr error
		for ev, err := range driveFlow[string, string](context.Background(), step) {
			if err != nil {
				errCount++
				lastErr = err
				continue
			}
			if _, ok := ev.(types.Done); ok {
				doneCount++
			}
		}
		if errCount != 1 {
			t.Fatalf("error yielded %d times, want 1", errCount)
		}
		if doneCount != 0 {
			t.Fatalf("Done emitted %d times on failure, want 0", doneCount)
		}
		if !errors.Is(lastErr, boom) {
			t.Fatalf("got %v, want boom", lastErr)
		}
		f := FlowFunc[string, string]("fail", step.fn)
		_, ierr := f.Invoke(WithPrincipal(context.Background(), types.Principal{Subject: "u"}), "in")
		if !errors.Is(ierr, boom) {
			t.Fatalf("Invoke got %v, want boom", ierr)
		}
	})
}
