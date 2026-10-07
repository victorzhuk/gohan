package gohan

import (
	"context"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	stdtelemetry "github.com/victorzhuk/gohan/std/telemetry"
)

type feedbackObservation struct {
	name  string
	n     int64
	v     float64
	attrs []types.Attr
}

type feedbackSink struct {
	counts  []feedbackObservation
	records []feedbackObservation
}

func (s *feedbackSink) StartSpan(ctx context.Context, _ string, _ ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}

func (s *feedbackSink) Count(_ context.Context, name string, n int64, attrs ...types.Attr) {
	s.counts = append(s.counts, feedbackObservation{name: name, n: n, attrs: attrs})
}

func (s *feedbackSink) Record(_ context.Context, name string, v float64, attrs ...types.Attr) {
	s.records = append(s.records, feedbackObservation{name: name, v: v, attrs: attrs})
}

func feedbackLabels(attrs []types.Attr) map[string]string {
	out := make(map[string]string, len(attrs))
	for _, a := range attrs {
		out[a.Key] = a.Value.(string)
	}
	return out
}

// feedbackFixture wires a Stack whose feedback store resolves "s1" to the
// t-a/u1 owner and whose audit trail and event log are fresh memory stores.
func feedbackFixture(t *testing.T, sink types.Telemetry) (*Stack, *stores.MemoryFeedback, *stores.MemoryAuditLog, *stores.MemoryEventLog) {
	t.Helper()
	fb := stores.NewMemoryFeedback(
		stores.WithMemoryFeedbackPrincipals(types.PrincipalFrom),
		stores.WithFeedbackSessionOwner(func(_ context.Context, id string) (types.SessionOwner, error) {
			if id != "s1" {
				return types.SessionOwner{}, errors.New("no such session")
			}
			return types.SessionOwner{Tenant: "t-a", Subject: "u1"}, nil
		}),
	)
	audit := stores.NewMemoryAuditLog()
	events := stores.NewMemoryEventLog()
	s, err := Build(
		WithTenant(types.Tenant{ID: "t-a"}),
		WithStores(stores.Stores{Feedback: fb, Audit: audit, EventLog: events}),
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if sink != nil {
		s.telemetry = sink
	}
	return s, fb, audit, events
}

func feedbackOwnerCtx() context.Context {
	return types.WithPrincipal(context.Background(), types.Principal{Tenant: "t-a", Subject: "u1"})
}

func feedbackCtx(audit stores.AuditLog) context.Context {
	ctx := types.WithRunInfo(feedbackOwnerCtx(), types.RunInfo{Flow: "support", SessionID: "s1", RunID: "r1"})
	return WithDecisionAudit(ctx, audit)
}

func feedbackSignal() stores.Feedback {
	return stores.Feedback{
		Target: types.FeedbackTarget{SessionID: "s1", RunID: "r1", MessageID: "m3"},
		Name:   "thumbs",
		Value:  false,
		Source: types.Explicit,
	}
}

func feedbackRows(t *testing.T, s stores.FeedbackStore) []stores.Feedback {
	t.Helper()
	rows, err := s.Feedback(feedbackOwnerCtx(), "s1")
	if err != nil {
		t.Fatalf("feedback rows: %v", err)
	}
	return rows
}

func auditRecords(t *testing.T, a stores.AuditLog, sessionID string) []stores.AuditRecord {
	t.Helper()
	var out []stores.AuditRecord
	for rec, err := range a.Read(context.Background(), sessionID) {
		if err != nil {
			t.Fatalf("audit read: %v", err)
		}
		out = append(out, rec)
	}
	return out
}

func eventRecords(t *testing.T, log stores.EventLog, runID string) []stores.Event {
	t.Helper()
	var out []stores.Event
	for ev, err := range log.Read(context.Background(), runID, 0) {
		if err != nil {
			t.Fatalf("event read: %v", err)
		}
		out = append(out, ev)
	}
	return out
}

func TestStackFeedback(t *testing.T) {
	t.Run("streams.feedback-recorded-event", func(t *testing.T) {
		s, fb, audit, events := feedbackFixture(t, nil)
		if err := s.Feedback(feedbackCtx(audit), "s1", feedbackSignal()); err != nil {
			t.Fatalf("feedback: %v", err)
		}

		rows := feedbackRows(t, fb)
		if len(rows) != 1 || rows[0].Name != "thumbs" || rows[0].Value != false {
			t.Fatalf("store rows = %+v, want one thumbs=false row", rows)
		}

		recs := auditRecords(t, audit, "s1")
		var recorded *stores.AuditRecord
		for i := range recs {
			if recs[i].Kind == auditFeedback {
				recorded = &recs[i]
			}
		}
		if recorded == nil {
			t.Fatalf("no feedback audit record in %+v", recs)
		}
		if recorded.SessionID != "s1" || recorded.RunID != "r1" || recorded.Decision != "thumbs" || recorded.Tenant != "t-a" {
			t.Fatalf("audit record = %+v, want the feedback signal's identity", *recorded)
		}

		var event *types.FeedbackRecorded
		for _, ev := range eventRecords(t, events, "r1") {
			if p, ok := ev.Payload.(types.FeedbackRecorded); ok {
				event = &p
			}
		}
		if event == nil {
			t.Fatalf("no FeedbackRecorded event in %+v", eventRecords(t, events, "r1"))
		}
		if event.Name != "thumbs" || event.Target.MessageID != "m3" || event.Source != types.Explicit {
			t.Fatalf("event = %+v, want the recorded signal", *event)
		}
	})

	t.Run("flow.feedback-idempotent-per-name", func(t *testing.T) {
		s, fb, audit, _ := feedbackFixture(t, nil)
		ctx := feedbackCtx(audit)
		if err := s.Feedback(ctx, "s1", feedbackSignal()); err != nil {
			t.Fatalf("first feedback: %v", err)
		}
		second := feedbackSignal()
		second.Value = true
		if err := s.Feedback(ctx, "s1", second); err != nil {
			t.Fatalf("second feedback: %v", err)
		}

		rows := feedbackRows(t, fb)
		if len(rows) != 1 {
			t.Fatalf("rows after two writes = %d, want one per name", len(rows))
		}
		if rows[0].Value != true || rows[0].Version != 2 {
			t.Fatalf("row = %+v, want value true at version 2", rows[0])
		}

		if err := s.Feedback(ctx, "s1", second); err != nil {
			t.Fatalf("repeat feedback: %v", err)
		}
		if rows := feedbackRows(t, fb); len(rows) != 1 {
			t.Fatalf("rows after an identical repeat = %d, want one", len(rows))
		}
	})

	t.Run("flow.feedback-owner-checked", func(t *testing.T) {
		s, fb, audit, events := feedbackFixture(t, nil)
		foreign := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t-b", Subject: "u2"})
		err := s.Feedback(foreign, "s1", feedbackSignal())
		if !errors.Is(err, types.ErrSessionForbidden) {
			t.Fatalf("foreign tenant: %v, want ErrSessionForbidden", err)
		}
		if rows := feedbackRows(t, fb); len(rows) != 0 {
			t.Fatalf("rows after refused call = %+v, want none", rows)
		}
		if recs := auditRecords(t, audit, "s1"); len(recs) != 0 {
			t.Fatalf("audit records after refused call = %+v, want none", recs)
		}
		if evs := eventRecords(t, events, "r1"); len(evs) != 0 {
			t.Fatalf("events after refused call = %+v, want none", evs)
		}
	})
}

func TestFeedbackMetricEmit(t *testing.T) {
	t.Run("telemetry.feedback-metric-labels", func(t *testing.T) {
		sink := &feedbackSink{}
		m, err := stdtelemetry.Build(sink, stdtelemetry.DefaultMetrics, stdtelemetry.WithRelease("r7"), stdtelemetry.WithVariant("b"))
		if err != nil {
			t.Fatalf("build metrics: %v", err)
		}
		s, _, audit, _ := feedbackFixture(t, m)
		ctx := feedbackCtx(audit)

		numeric := feedbackSignal()
		numeric.Name, numeric.Value = "csat", 4
		if err := s.Feedback(ctx, "s1", numeric); err != nil {
			t.Fatalf("numeric feedback: %v", err)
		}

		t.Run("numeric value records", func(t *testing.T) {
			if len(sink.counts) != 0 {
				t.Fatalf("numeric value counted %+v, want a histogram record", sink.counts)
			}
			if len(sink.records) != 1 {
				t.Fatalf("records = %+v, want exactly one", sink.records)
			}
			obs := sink.records[0]
			if obs.name != MetricFeedback || obs.v != 4 {
				t.Fatalf("observation = %+v, want %s observing 4", obs, MetricFeedback)
			}
			got := feedbackLabels(obs.attrs)
			want := map[string]string{"name": "csat", "flow": "support", types.KeyRelease: "r7", types.KeyVariant: "b"}
			if len(got) != len(want) {
				t.Fatalf("labels = %v, want exactly %v", got, want)
			}
			for k, v := range want {
				if got[k] != v {
					t.Fatalf("label %s = %q, want %q", k, got[k], v)
				}
			}
			for _, banned := range []string{"subject", "session_id", "run_id", "target", "comment", "correction", types.KeySubject, types.KeySessionID, types.KeyRunID} {
				if _, ok := got[banned]; ok {
					t.Fatalf("label %s must not reach the feedback metric", banned)
				}
			}
		})

		t.Run("other value counts", func(t *testing.T) {
			other := feedbackSignal()
			other.Name, other.Value = "csat", "great"
			if err := s.Feedback(ctx, "s1", other); err != nil {
				t.Fatalf("text feedback: %v", err)
			}
			if len(sink.records) != 1 {
				t.Fatalf("records = %+v, want the numeric record only", sink.records)
			}
			if len(sink.counts) != 1 {
				t.Fatalf("counts = %+v, want exactly one", sink.counts)
			}
			obs := sink.counts[0]
			if obs.name != MetricFeedback || obs.n != 1 {
				t.Fatalf("observation = %+v, want %s counting 1", obs, MetricFeedback)
			}
			got := feedbackLabels(obs.attrs)
			want := map[string]string{"name": "csat", "flow": "support", types.KeyRelease: "r7", types.KeyVariant: "b"}
			if len(got) != len(want) {
				t.Fatalf("labels = %v, want exactly %v", got, want)
			}
			for k, v := range want {
				if got[k] != v {
					t.Fatalf("label %s = %q, want %q", k, got[k], v)
				}
			}
			for _, banned := range []string{"subject", "session_id", "run_id", "target", "comment", "correction"} {
				if _, ok := got[banned]; ok {
					t.Fatalf("label %s must not reach the feedback metric", banned)
				}
			}
		})
	})
}
