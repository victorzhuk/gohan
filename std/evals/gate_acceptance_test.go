package evals

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestReleaseGate(t *testing.T) {
	t.Run("release.dataset-pinned", func(t *testing.T) {
		cases := []Case{{ID: "case-a", Input: "book a flight"}, {ID: "case-b", Input: "cancel it"}}
		pinned, err := Shadow(context.Background(), func() ShadowRun {
			r := shadowScriptedRun(
				&shadowFlowDouble{name: "champion", tools: []string{"book"}},
				&shadowFlowDouble{name: "challenger", tools: []string{"book"}},
			)
			r.Inputs = cases
			return r
		}())
		if err != nil {
			t.Fatalf("pin-free Shadow: %v", err)
		}
		want := pinned.DatasetHash

		champ := &shadowFlowDouble{name: "champion", tools: []string{"book"}}
		chall := &shadowFlowDouble{name: "challenger", tools: []string{"book"}}
		run := shadowScriptedRun(champ, chall)
		run.Inputs = cases
		run.DatasetHash = want

		mutated := []Case{{ID: "case-a", Input: "book a flight"}, {ID: "case-b", Input: "cancel it now"}}
		runMutated := run
		runMutated.Inputs = mutated
		_, err = Shadow(context.Background(), runMutated)
		if !errors.Is(err, ErrDatasetPinned) {
			t.Fatalf("err = %v, want ErrDatasetPinned", err)
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name the recorded hash %s", err, want)
		}
		if champ.calls() != 0 || chall.calls() != 0 {
			t.Errorf("flows ran despite pin mismatch: champion=%d challenger=%d, want 0/0", champ.calls(), chall.calls())
		}

		if _, err := Shadow(context.Background(), run); err != nil {
			t.Fatalf("matching pin refused: %v", err)
		}
		if champ.calls() != len(cases) || chall.calls() != len(cases) {
			t.Errorf("flows ran champion=%d challenger=%d, want %d each", champ.calls(), chall.calls(), len(cases))
		}
	})

	t.Run("release.delta-gate", func(t *testing.T) {
		gate := DefaultGate(0.90)
		for _, tc := range []struct {
			candidate float64
			want      GateResult
		}{
			{0.84, GateFail},
			{0.87, GateNeedsReview},
			{0.89, GatePass},
		} {
			if got := gate.Decide(tc.candidate); got != tc.want {
				t.Errorf("Decide(%v) = %q, want %q", tc.candidate, got, tc.want)
			}
		}
	})

	t.Run("release.judge-median", func(t *testing.T) {
		median, spread := medianSpread([]float64{0.6, 0.9, 0.85})
		if median != 0.85 {
			t.Errorf("median = %v, want 0.85", median)
		}
		if spread <= UnstableSpread {
			t.Errorf("spread = %v, not above UnstableSpread %v: case not flagged Unstable", spread, UnstableSpread)
		}
	})
}
