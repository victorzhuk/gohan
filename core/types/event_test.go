package types

import (
	"context"
	stdjson "encoding/json"
	"encoding/json/v2"
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestEventTypes(t *testing.T) {
	t.Run("notice kinds render in spec order", func(t *testing.T) {
		kinds := []NoticeKind{NoticeFinished, NoticeFailed, NoticeCancelled, NoticeSuspended}
		want := []string{"finished", "failed", "cancelled", "suspended"}
		got := make([]string, len(kinds))
		for i, k := range kinds {
			got[i] = k.String()
		}
		if !slices.Equal(got, want) {
			t.Fatalf("NoticeKind strings = %v, want %v", got, want)
		}
	})

	t.Run("run notice marshals exactly the seven body fields", func(t *testing.T) {
		at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
		n := RunNotice{
			ID:        "n-1",
			Kind:      NoticeCancelled,
			Tenant:    "acme",
			SessionID: "s-1",
			RunID:     "r-1",
			Reason:    "user cancelled",
			At:        at,
		}
		raw, err := json.Marshal(n)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		var body map[string]stdjson.RawMessage
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		wantKeys := []string{"at", "id", "kind", "reason", "run_id", "session_id", "tenant"}
		keys := make([]string, 0, len(body))
		for k := range body {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if !slices.Equal(keys, wantKeys) {
			t.Fatalf("notice fields = %v, want %v", keys, wantKeys)
		}
		if string(body["kind"]) != `"cancelled"` {
			t.Fatalf("kind = %s, want %q", body["kind"], "cancelled")
		}

		var back RunNotice
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		if back != n {
			t.Fatalf("round trip = %+v, want %+v", back, n)
		}
	})

	t.Run("payloads satisfy Event", func(t *testing.T) {
		payloads := []Event{
			TextDelta{},
			ReasoningDelta{},
			AssistantMessage{},
			ToolArgsDelta{},
			ResultDelta{},
			ToolStarted{},
			ToolFinished{},
			Suspended{},
			GuardBlocked{},
			LimitWarning{},
			Compacted{},
			StateChanged{},
			FeedbackRecorded{},
			SteerApplied{},
		}
		if len(payloads) != 14 {
			t.Fatalf("payload count = %d, want 14", len(payloads))
		}
	})

	t.Run("stop reasons equal spec strings", func(t *testing.T) {
		want := map[StopReason]string{
			StopCompleted:       "completed",
			StopSuspended:       "suspended",
			StopLimit:           "limit",
			StopGuardBlocked:    "guard_blocked",
			StopCancelled:       "cancelled",
			StopFailed:          "failed",
			StopShadowSuspended: "shadow_suspended",
			StopHandedOff:       "handed_off",
		}
		for reason, s := range want {
			if string(reason) != s {
				t.Fatalf("StopReason %q != %q", reason, s)
			}
		}
	})

	t.Run("done with uncertain and raw result round trips", func(t *testing.T) {
		done := Done{
			Reason:    StopFailed,
			Seq:       7,
			Usage:     Usage{InputTokens: 10, OutputTokens: 4},
			Cost:      0.25,
			Uncertain: []CallKey{{SessionID: "s-1", CallID: "call-1"}},
			Result:    stdjson.RawMessage(`{"ok":false,"err":"boom"}`),
		}
		raw, err := json.Marshal(done)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		var back Done
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		if back.Reason != done.Reason || back.Seq != done.Seq || back.Cost != done.Cost ||
			back.Usage.InputTokens != done.Usage.InputTokens ||
			back.Usage.CachedInputTokens != done.Usage.CachedInputTokens ||
			back.Usage.OutputTokens != done.Usage.OutputTokens ||
			!reflect.DeepEqual(back.Uncertain, done.Uncertain) ||
			string(back.Result) != string(done.Result) {
			t.Fatalf("round trip back=%#v want=%#v", back, done)
		}
	})
}

var _ Notifier = notifierFunc(nil)

type notifierFunc func(context.Context, RunNotice) error

func (f notifierFunc) Notify(ctx context.Context, n RunNotice) error { return f(ctx, n) }
