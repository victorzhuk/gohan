package stores

import (
	"context"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func feedbackLifecycleStore() (*MemoryFeedback, context.Context) {
	owners := map[string]types.SessionOwner{
		"s1": {Tenant: "t-a", Subject: "u1"},
		"s2": {Tenant: "t-a", Subject: "u1"},
	}
	s := NewMemoryFeedback(
		WithMemoryFeedbackPrincipals(principalFromTestCtx),
		WithFeedbackSessionOwner(func(_ context.Context, sessionID string) (types.SessionOwner, error) {
			owner, ok := owners[sessionID]
			if !ok {
				return types.SessionOwner{}, errors.New("no such session")
			}
			return owner, nil
		}),
	)
	return s, withTestPrincipal(context.Background(), ownerTenantA)
}

func TestFeedbackLifecycle(t *testing.T) {
	t.Run("stores.feedback-cascades-on-erase", func(t *testing.T) {
		s, ctx := feedbackLifecycleStore()

		for _, target := range []types.FeedbackTarget{
			{SessionID: "s1", RunID: "r1", MessageID: "m1"},
			{SessionID: "s1", RunID: "r2", MessageID: "m2"},
			{SessionID: "s2", RunID: "r3", MessageID: "m3"},
		} {
			if _, err := s.PutFeedback(ctx, Feedback{Target: target, Name: "thumbs", Source: types.Explicit}); err != nil {
				t.Fatalf("put for %s: %v", target.SessionID, err)
			}
		}

		if err := s.DeleteSessionDependents(ctx, "s1"); err != nil {
			t.Fatalf("cascade delete: %v", err)
		}

		if rows, err := s.Feedback(ctx, "s1"); err != nil || len(rows) != 0 {
			t.Fatalf("feedback after erase: %d rows, err %v", len(rows), err)
		}
		if rows, err := s.Feedback(ctx, "s2"); err != nil || len(rows) != 1 {
			t.Fatalf("sibling session touched by cascade: %d rows, err %v", len(rows), err)
		}

		// A copy carries nothing: feedback about the parent never
		// reaches the forked or child session.
		if err := s.CopySessionDependents(ctx, "s2", "s1"); err != nil {
			t.Fatalf("copy: %v", err)
		}
		if rows, err := s.Feedback(ctx, "s1"); err != nil || len(rows) != 0 {
			t.Fatalf("copy carried rows: %d rows, err %v", len(rows), err)
		}
		if rows, err := s.Feedback(ctx, "s2"); err != nil || len(rows) != 1 {
			t.Fatalf("source rows changed by copy: %d rows, err %v", len(rows), err)
		}
	})

	t.Run("streams.feedback-recorded-event", func(t *testing.T) {
		log := NewMemoryEventLog()
		ctx := context.Background()
		if err := log.Append(ctx, "r1", Event{Payload: types.Done{Reason: types.StopCompleted, Seq: 3}}); err != nil {
			t.Fatalf("append done: %v", err)
		}

		target := types.FeedbackTarget{SessionID: "s1", RunID: "r1", MessageID: "m1"}
		f := Feedback{
			Target:     target,
			Name:       "thumbs",
			Value:      false,
			Comment:    "should not travel",
			Correction: []types.Block{feedbackText("nor this")},
			Source:     types.Explicit,
		}
		if err := RecordFeedback(ctx, log, f); err != nil {
			t.Fatalf("record: %v", err)
		}

		var got []Event
		for e, err := range log.Read(ctx, "r1", 0) {
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			got = append(got, e)
		}
		if len(got) != 2 {
			t.Fatalf("events = %d, want 2", len(got))
		}
		if _, ok := got[0].Payload.(types.Done); !ok {
			t.Fatalf("first event is %T, want Done", got[0].Payload)
		}
		if got[1].Meta.Seq != got[0].Meta.Seq+1 {
			t.Fatalf("feedback Seq %d, want %d", got[1].Meta.Seq, got[0].Meta.Seq+1)
		}
		if got[1].Meta.RunID != "r1" {
			t.Fatalf("feedback RunID %q, want r1", got[1].Meta.RunID)
		}
		rec, ok := got[1].Payload.(types.FeedbackRecorded)
		if !ok {
			t.Fatalf("second event is %T, want FeedbackRecorded", got[1].Payload)
		}
		if rec.Target != target || rec.Name != "thumbs" || rec.Value != false || rec.Source != types.Explicit {
			t.Fatalf("payload mismatch: %+v", rec)
		}

		if err := RecordFeedback(ctx, nil, f); err == nil {
			t.Fatal("record without event log: nil error")
		}
	})
}
