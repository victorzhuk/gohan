package evals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// shadowFlowDouble records the RunInfo each call observed and replays
// scripted tool sequences, costs and sleeps.
type shadowFlowDouble struct {
	name      string
	tools     []string
	cost      float64
	sleep     time.Duration
	err       error
	mu        sync.Mutex
	infos     []types.RunInfo
	callCount int
}

func (d *shadowFlowDouble) flow(ctx context.Context, input string) (ShadowOutcome, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.callCount++
	if info, ok := types.RunInfoFrom(ctx); ok {
		d.infos = append(d.infos, info)
	}
	if d.sleep > 0 {
		time.Sleep(d.sleep)
	}
	if d.err != nil {
		return ShadowOutcome{}, d.err
	}
	msg := types.Message{ID: d.name + "-1", Role: types.RoleAssistant}
	for _, name := range d.tools {
		msg.Blocks = append(msg.Blocks, types.ToolUse{Name: name})
	}
	return ShadowOutcome{
		Output:  d.name + " output for " + input,
		History: stores.History{Messages: []types.Message{msg}},
		Cost:    d.cost,
	}, nil
}

func (d *shadowFlowDouble) calls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.callCount
}

// shadowScorerDouble scores 0.8 when the history shows "book", 0.5 when
// it shows "refund"; anything else is unscored.
type shadowScorerDouble struct{}

func (shadowScorerDouble) Dimension() string { return types.EvalQuality }
func (shadowScorerDouble) Version() string   { return "shadow-test-v1" }

func (shadowScorerDouble) Evaluate(ev Evidence) types.EvalDimensionResult {
	for _, name := range toolSequence(ev.History) {
		switch name {
		case "book":
			return types.EvalDimensionResult{Dimension: types.EvalQuality, Status: types.EvalScored, Score: 0.8, Scale: types.ScaleUnit}
		case "refund":
			return types.EvalDimensionResult{Dimension: types.EvalQuality, Status: types.EvalScored, Score: 0.5, Scale: types.ScaleUnit}
		}
	}
	return types.EvalDimensionResult{Dimension: types.EvalQuality, Status: types.EvalUnscored, Reason: "no known tool"}
}

// shadowTransportDouble records whether Serve was ever called.
type shadowTransportDouble struct {
	served bool
}

func (t *shadowTransportDouble) Serve(ctx context.Context) error {
	t.served = true
	return nil
}

func shadowScriptedRun(champ, chall *shadowFlowDouble) ShadowRun {
	return ShadowRun{
		Champion:   Side{Flow: champ.flow, ReleaseID: "rel-champ", Variant: "v1"},
		Challenger: Side{Flow: chall.flow, ReleaseID: "rel-chall", Variant: "v2"},
	}
}

func TestReleaseShadow(t *testing.T) {
	t.Run("release.shadow-traffic-replay", func(t *testing.T) {
		path := t.TempDir() + "/cassette.json"
		cases := []Case{
			{ID: "case-a", Input: "book a flight to Lisbon"},
			{ID: "case-b", Input: "cancel my booking"},
			{ID: "case-c", Input: "what time does the office open"},
		}
		data, err := json.Marshal(cases)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, err := LoadCases(path)
		if err != nil {
			t.Fatalf("LoadCases: %v", err)
		}

		champ := &shadowFlowDouble{name: "champion", tools: []string{"search", "book"}, cost: 0.01}
		chall := &shadowFlowDouble{name: "challenger", tools: []string{"search", "book"}, cost: 0.02}
		run := shadowScriptedRun(champ, chall)
		run.Inputs = loaded
		run.Source = SourceCassette
		report, err := Shadow(context.Background(), run)
		if err != nil {
			t.Fatalf("Shadow: %v", err)
		}
		if len(report.Rows) != 3 {
			t.Fatalf("rows = %d, want 3", len(report.Rows))
		}
		ids := make(map[string]bool, 3)
		for _, row := range report.Rows {
			ids[row.CaseID] = true
			if row.CaseID == "" {
				t.Error("empty CaseID in a row")
			}
		}
		if len(ids) != 3 {
			t.Fatalf("CaseIDs not unique: %v", ids)
		}
		if len(champ.infos) != 3 || len(chall.infos) != 3 {
			t.Fatalf("observed infos champion=%d challenger=%d, want 3/3", len(champ.infos), len(chall.infos))
		}
		for _, info := range champ.infos {
			if info.Mode != types.Primary {
				t.Errorf("champion mode = %v, want Primary", info.Mode)
			}
			if info.ReleaseID != "rel-champ" {
				t.Errorf("champion release = %q, want rel-champ", info.ReleaseID)
			}
		}
		for _, info := range chall.infos {
			if info.Mode != types.Shadow {
				t.Errorf("challenger mode = %v, want Shadow", info.Mode)
			}
			if info.ReleaseID != "rel-chall" || info.Variant != "v2" {
				t.Errorf("challenger identity = %q/%q, want rel-chall/v2", info.ReleaseID, info.Variant)
			}
		}
		if report.Source != SourceCassette {
			t.Errorf("source = %q, want %q", report.Source, SourceCassette)
		}
		if report.DatasetHash == "" {
			t.Error("empty DatasetHash")
		}
		if report.ChampionReleaseID == report.ChallengerReleaseID {
			t.Error("sides share a ReleaseID")
		}
		if report.Identity() == "" {
			t.Error("empty Identity")
		}

		rerun := shadowScriptedRun(&shadowFlowDouble{name: "champion"}, &shadowFlowDouble{name: "challenger"})
		rerun.Inputs = loaded
		rerun.Source = SourceCassette
		second, err := Shadow(context.Background(), rerun)
		if err != nil {
			t.Fatalf("second Shadow: %v", err)
		}
		if second.DatasetHash != report.DatasetHash {
			t.Errorf("DatasetHash differs across identical inputs: %q vs %q", report.DatasetHash, second.DatasetHash)
		}
	})

	t.Run("release.shadow-scripted-suite", func(t *testing.T) {
		champ := &shadowFlowDouble{name: "champion", tools: []string{"search", "book"}, cost: 0.10, sleep: 2 * time.Millisecond}
		chall := &shadowFlowDouble{name: "challenger", tools: []string{"search", "refund"}, cost: 0.15, sleep: 3 * time.Millisecond}
		run := shadowScriptedRun(champ, chall)
		run.Inputs = []Case{{ID: "case-1", Input: "rebook my trip", Expected: "rebooked with refund"}}
		run.Scorer = shadowScorerDouble{}

		var report Report
		synctest.Test(t, func(t *testing.T) {
			var err error
			report, err = Shadow(context.Background(), run)
			if err != nil {
				t.Fatalf("Shadow: %v", err)
			}
		})

		if report.Source != SourceScripted {
			t.Errorf("source = %q, want %q", report.Source, SourceScripted)
		}
		if len(report.Rows) != 1 {
			t.Fatalf("rows = %d, want 1", len(report.Rows))
		}
		row := report.Rows[0]
		if diff := row.ToolSequenceDiff; !slices.Equal(diff, []string{"-book", "+refund"}) {
			t.Errorf("ToolSequenceDiff = %v, want [-book +refund]", diff)
		}
		if row.ChampionCost != 0.10 || row.ChallengerCost != 0.15 {
			t.Errorf("costs = %v/%v, want 0.10/0.15", row.ChampionCost, row.ChallengerCost)
		}
		if row.ChampionLatency != 2*time.Millisecond {
			t.Errorf("champion latency = %v, want 2ms", row.ChampionLatency)
		}
		if row.ChallengerLatency != 3*time.Millisecond {
			t.Errorf("challenger latency = %v, want 3ms", row.ChallengerLatency)
		}
		if row.ScoreStatus != types.EvalScored {
			t.Fatalf("score status = %q (%s), want scored", row.ScoreStatus, row.ScoreReason)
		}
		if row.ScoreReason != "" {
			t.Errorf("unexpected score reason %q", row.ScoreReason)
		}
		if delta := row.ScoreDelta; delta < -0.3-1e-9 || delta > -0.3+1e-9 {
			t.Errorf("score delta = %v, want -0.3", delta)
		}
		want := Judge{Dimension: shadowScorerDouble{}.Dimension(), Version: shadowScorerDouble{}.Version()}.Fingerprint()
		if !slices.Equal(report.JudgeFingerprints, []string{want}) {
			t.Errorf("JudgeFingerprints = %v, want [%s]", report.JudgeFingerprints, want)
		}
	})

	t.Run("release.pairwise-report", func(t *testing.T) {
		const n = 50
		cases := make([]Case, 0, n)
		for i := range n {
			cases = append(cases, Case{ID: fmt.Sprintf("case-%02d", i), Input: fmt.Sprintf("request %d", i)})
		}
		champ := &shadowFlowDouble{name: "champion", tools: []string{"search", "book"}, cost: 0.01}
		chall := &shadowFlowDouble{name: "challenger", tools: []string{"search", "refund"}, cost: 0.02}
		run := shadowScriptedRun(champ, chall)
		run.Inputs = cases
		run.Scorer = shadowScorerDouble{}

		report, err := Shadow(context.Background(), run)
		if err != nil {
			t.Fatalf("Shadow: %v", err)
		}
		if len(report.Rows) != n {
			t.Fatalf("rows = %d, want %d", len(report.Rows), n)
		}
		for _, row := range report.Rows {
			if row.ScoreStatus != types.EvalScored {
				t.Errorf("row %s score status = %q (%s), want scored", row.CaseID, row.ScoreStatus, row.ScoreReason)
				continue
			}
			if row.ScoreDelta < -0.3-1e-9 || row.ScoreDelta > -0.3+1e-9 {
				t.Errorf("row %s score delta = %v, want -0.3", row.CaseID, row.ScoreDelta)
			}
			if !slices.Equal(row.ToolSequenceDiff, []string{"-book", "+refund"}) {
				t.Errorf("row %s tool diff = %v, want [-book +refund]", row.CaseID, row.ToolSequenceDiff)
			}
			if row.ChampionCost != 0.01 || row.ChallengerCost != 0.02 {
				t.Errorf("row %s costs = %v/%v, want 0.01/0.02", row.CaseID, row.ChampionCost, row.ChallengerCost)
			}
			if row.ChampionLatency <= 0 || row.ChallengerLatency <= 0 {
				t.Errorf("row %s latencies = %v/%v, want both measured", row.CaseID, row.ChampionLatency, row.ChallengerLatency)
			}
		}
	})

	t.Run("release.shadow-no-caller-serving", func(t *testing.T) {
		transport := &shadowTransportDouble{}
		champ := &shadowFlowDouble{name: "champion"}
		chall := &shadowFlowDouble{name: "challenger"}
		run := shadowScriptedRun(champ, chall)
		run.Inputs = []Case{{ID: "case-x", Input: "hello"}}
		run.CallerTransport = transport

		report, err := Shadow(context.Background(), run)
		if !errors.Is(err, ErrCallerServing) {
			t.Fatalf("err = %v, want ErrCallerServing", err)
		}
		if champ.calls() != 0 || chall.calls() != 0 {
			t.Errorf("flows ran: champion=%d challenger=%d, want 0/0", champ.calls(), chall.calls())
		}
		if transport.served {
			t.Error("Serve was called")
		}
		if report.Rows != nil || report.Source != "" || report.DatasetHash != "" {
			t.Errorf("report = %+v, want zero value", report)
		}
	})
}

func TestReleaseShadowHelpers(t *testing.T) {
	if diff := diffToolSeq([]string{"search", "book"}, []string{"search", "book"}); len(diff) != 0 {
		t.Errorf("equal sequences diff = %v, want empty", diff)
	}
	if diff := diffToolSeq(nil, nil); len(diff) != 0 {
		t.Errorf("empty diff = %v, want empty", diff)
	}
	if diff := diffToolSeq([]string{"a"}, []string{"b"}); !slices.Equal(diff, []string{"-a", "+b"}) {
		t.Errorf("disjoint diff = %v, want [-a +b]", diff)
	}
	if got := toolSequence(stores.History{}); len(got) != 0 {
		t.Errorf("empty history sequence = %v", got)
	}
}
