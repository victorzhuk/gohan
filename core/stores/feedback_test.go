package stores

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func feedbackTestStore() (*MemoryFeedback, map[string]types.SessionOwner) {
	owners := map[string]types.SessionOwner{
		"s1": {Tenant: "t-a", Subject: "u1"},
	}
	return NewMemoryFeedback(
		WithMemoryFeedbackPrincipals(principalFromTestCtx),
		WithFeedbackSessionOwner(func(_ context.Context, sessionID string) (types.SessionOwner, error) {
			owner, ok := owners[sessionID]
			if !ok {
				return types.SessionOwner{}, errors.New("no such session")
			}
			return owner, nil
		}),
	), owners
}

func feedbackText(text string) types.Block {
	return types.Text{BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginUser}}, Text: text}
}

func TestFeedbackStore(t *testing.T) {
	t.Run("flow.feedback-owner-checked", func(t *testing.T) {
		s, _ := feedbackTestStore()
		ctx := withTestPrincipal(context.Background(), ownerTenantA)
		if _, err := s.PutFeedback(ctx, Feedback{Name: "thumbs", Target: types.FeedbackTarget{SessionID: "s1"}}); err != nil {
			t.Fatalf("put by owner: %v", err)
		}

		_, err := s.PutFeedback(withTestPrincipal(context.Background(), peerTenantA), Feedback{Name: "thumbs", Target: types.FeedbackTarget{SessionID: "s1"}})
		if !errors.Is(err, types.ErrSessionForbidden) {
			t.Fatalf("put by peer: %v, want ErrSessionForbidden", err)
		}
		if rows, err := s.Feedback(ctx, "s1"); err != nil || len(rows) != 1 {
			t.Fatalf("rows after peer put: %d rows, err %v", len(rows), err)
		}

		if _, err := s.Feedback(withTestPrincipal(context.Background(), peerTenantA), "s1"); !errors.Is(err, types.ErrSessionForbidden) {
			t.Fatalf("list by peer: %v, want ErrSessionForbidden", err)
		}
	})

	t.Run("flow.feedback-idempotent-per-name", func(t *testing.T) {
		s, _ := feedbackTestStore()
		ctx := withTestPrincipal(context.Background(), ownerTenantA)

		target := types.FeedbackTarget{SessionID: "s1", RunID: "r1", MessageID: "m3"}
		if v, err := s.PutFeedback(ctx, Feedback{Target: target, Name: "thumbs", Value: false, Source: types.Explicit}); err != nil || v != 1 {
			t.Fatalf("first put: version %d, err %v", v, err)
		}
		if _, err := s.PutFeedback(ctx, Feedback{Target: target, Name: "flags", Value: "x", Source: types.Implicit}); err != nil {
			t.Fatalf("other name put: %v", err)
		}

		v, err := s.PutFeedback(ctx, Feedback{Target: target, Name: "thumbs", Value: true, Source: types.Explicit})
		if err != nil || v != 2 {
			t.Fatalf("second put: version %d, err %v", v, err)
		}

		rows, err := s.Feedback(ctx, "s1")
		if err != nil {
			t.Fatalf("feedback: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("rows = %d, want 2 (one per name)", len(rows))
		}
		thumbs := rows[0]
		if thumbs.Name != "flags" || rows[1].Name != "thumbs" {
			t.Fatalf("rows not in name order: %q, %q", rows[0].Name, rows[1].Name)
		}
		thumbs = rows[1]
		if thumbs.Version != 2 || thumbs.Value != true {
			t.Fatalf("thumbs = version %d value %v, want version 2 value true", thumbs.Version, thumbs.Value)
		}
	})

	t.Run("flow.feedback-never-in-context", func(t *testing.T) {
		s, _ := feedbackTestStore()
		ctx := withTestPrincipal(context.Background(), ownerTenantA)

		comment := "missed the invoice number"
		correction := []types.Block{feedbackText("use INV-42")}
		if _, err := s.PutFeedback(ctx, Feedback{
			Target:     types.FeedbackTarget{SessionID: "s1", MessageID: "m3"},
			Name:       "thumbs",
			Value:      true,
			Comment:    comment,
			Correction: correction,
			Source:     types.Explicit,
		}); err != nil {
			t.Fatalf("put: %v", err)
		}

		rows, err := s.Feedback(ctx, "s1")
		if err != nil || len(rows) != 1 {
			t.Fatalf("feedback: %d rows, err %v", len(rows), err)
		}
		if rows[0].Comment != comment || !slices.Equal(rows[0].Correction, correction) {
			t.Fatalf("stored feedback lost comment or correction: %+v", rows[0])
		}

		// The store's only sink is this port; returned entries are copies,
		// so no consumer mutation can leak back into later reads either.
		rows[0].Comment = "tampered"
		rows[0].Correction[0] = feedbackText("tampered")
		again, err := s.Feedback(ctx, "s1")
		if err != nil {
			t.Fatalf("re-read: %v", err)
		}
		if again[0].Comment != comment || !slices.Equal(again[0].Correction, correction) {
			t.Fatalf("mutation leaked into the store: %+v", again[0])
		}
	})
}
