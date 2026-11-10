package telemetry_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/telemetry"
)

func TestExportAcceptance(t *testing.T) {
	t.Run("telemetry.export-acceptance", func(t *testing.T) {
		ctx := t.Context()

		// Assertions 1-3: partial acknowledgement, same-ID resend, zero unresolved.
		dest := newScriptDest("scripted")
		dest.script([]types.RecordOutcome{
			{ID: "a1", Status: types.OutcomeAccepted},
			{ID: "a2", Status: types.OutcomeAccepted},
			{ID: "r1", Status: types.OutcomeRetryable},
			{ID: "p1", Status: types.OutcomePermanent},
		}, errors.New("interrupted"))
		dest.script([]types.RecordOutcome{
			{ID: "r1", Status: types.OutcomeAccepted},
			{ID: "u1", Status: types.OutcomeAccepted},
			{ID: "u2", Status: types.OutcomeAccepted},
		}, nil)

		p := newAcceptancePipeline(t, dest)
		ids := []string{"a1", "a2", "r1", "p1", "u1", "u2"}
		for _, id := range ids {
			mustIngest(t, p, dest.name, spanRecord(id))
		}

		if _, err := p.Flush(ctx); err != nil && !strings.Contains(err.Error(), "interrupted") {
			t.Fatalf("flush 1: %v", err)
		}
		first := dest.attempt(0)
		if len(first) != len(ids) {
			t.Fatalf("attempt 1 sent %d records, want %d", len(first), len(ids))
		}
		for _, id := range ids {
			if !slices.Contains(first, id) {
				t.Fatalf("attempt 1 missing %q: %v", id, first)
			}
		}

		work, err := p.Flush(ctx)
		if err != nil {
			t.Fatalf("flush 2: %v", err)
		}
		second := dest.attempt(1)
		want := []string{"r1", "u1", "u2"}
		slices.Sort(second)
		slices.Sort(want)
		if !slices.Equal(second, want) {
			t.Fatalf("attempt 2 sent %v, want exactly %v", second, want)
		}
		if work.Records != 0 || len(work.ByDestination) != 0 {
			t.Fatalf("unresolved after all accepted = %+v, want zero", work)
		}

		// Assertion 4: cancellation keeps unacknowledged records unresolved
		// with unchanged IDs; the acknowledged one advanced.
		block := newBlockingDest("blocking")
		p2 := newAcceptancePipeline(t, block)
		cancelIDs := []string{"b1", "b2", "b3", "b4", "b5"}
		for _, id := range cancelIDs {
			mustIngest(t, p2, block.name, spanRecord(id))
		}
		cancelCtx, cancel := context.WithCancel(ctx)
		go func() {
			<-block.parked
			cancel()
		}()
		work, err = p2.Flush(cancelCtx)
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled flush err = %v, want context.Canceled", err)
		}
		if work.Records != 4 || work.ByDestination["blocking"] != 4 {
			t.Fatalf("unresolved after cancellation = %+v, want 4 at blocking", work)
		}
		work, err = p2.Flush(ctx)
		if err != nil {
			t.Fatalf("flush after cancellation: %v", err)
		}
		resend := block.sentIDs(1)
		slices.Sort(resend)
		wantResend := []string{"b2", "b3", "b4", "b5"}
		if !slices.Equal(resend, wantResend) {
			t.Fatalf("re-sent %v, want %v with unchanged IDs", resend, wantResend)
		}
		if slices.Contains(resend, "b1") {
			t.Fatal("acknowledged record was re-sent after cancellation")
		}
		if work.Records != 0 {
			t.Fatalf("unresolved = %+v, want zero", work)
		}

		// Assertion 5: ambiguous advances the record and reports once.
		diags := &capturingDiags{}
		amb := newScriptDest("ambiguous-dest")
		amb.script([]types.RecordOutcome{{ID: "am1", Status: types.OutcomeAmbiguous}}, nil)
		p3 := newAcceptancePipeline(t, amb, telemetry.WithExportDiagnostics(diags))
		mustIngest(t, p3, amb.name, spanRecord("am1"))
		if _, err := p3.Flush(ctx); err != nil {
			t.Fatalf("ambiguous flush: %v", err)
		}
		if _, err := p3.Flush(ctx); err != nil {
			t.Fatalf("second flush: %v", err)
		}
		if got := amb.allAttempts(); len(got) != 1 {
			t.Fatalf("ambiguous record re-sent: %d attempts, want 1", len(got))
		}
		ambiguous := diags.ofKind(types.DiagDeliveryAmbiguous)
		if len(ambiguous) != 1 || ambiguous[0].Name != amb.name {
			t.Fatalf("DiagDeliveryAmbiguous reports = %+v, want exactly one naming %q", ambiguous, amb.name)
		}

		// Assertion 6: permanent never re-sent and reports dropped.
		diags2 := &capturingDiags{}
		perm := newScriptDest("permanent-dest")
		perm.script([]types.RecordOutcome{{ID: "pm1", Status: types.OutcomePermanent}}, nil)
		p4 := newAcceptancePipeline(t, perm, telemetry.WithExportDiagnostics(diags2))
		mustIngest(t, p4, perm.name, spanRecord("pm1"))
		if _, err := p4.Flush(ctx); err != nil {
			t.Fatalf("permanent flush: %v", err)
		}
		if _, err := p4.Flush(ctx); err != nil {
			t.Fatalf("second flush: %v", err)
		}
		if got := perm.allAttempts(); len(got) != 1 {
			t.Fatalf("permanent record re-sent: %d attempts, want 1", len(got))
		}
		dropped := diags2.ofKind(types.DiagDeliveryDropped)
		if len(dropped) != 1 || dropped[0].Name != perm.name {
			t.Fatalf("DiagDeliveryDropped reports = %+v, want exactly one naming %q", dropped, perm.name)
		}

		if _, err := p.Shutdown(ctx); err != nil {
			t.Fatalf("shutdown: %v", err)
		}
		if _, err := p2.Shutdown(ctx); err != nil {
			t.Fatalf("shutdown: %v", err)
		}
		if _, err := p3.Shutdown(ctx); err != nil {
			t.Fatalf("shutdown: %v", err)
		}
		if _, err := p4.Shutdown(ctx); err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	})
}

func spanRecord(id string) types.ExportRecord {
	return types.ExportRecord{
		SchemaVersion: types.CurrentExportSchemaVersion,
		ID:            types.RecordID(id),
		Resource:      types.ResourceIdentity{Name: "gohan"},
		Signal:        types.SignalSpan,
		Span:          &types.SpanRecord{Name: "chat", Outcome: types.SpanOK},
	}
}

func mustIngest(t *testing.T, p *telemetry.Pipeline, dest string, rec types.ExportRecord) {
	t.Helper()
	if err := p.Ingest(t.Context(), dest, []types.ExportRecord{rec}); err != nil {
		t.Fatalf("Ingest %s: %v", idOf(rec), err)
	}
}

func idOf(rec types.ExportRecord) string { return string(rec.ID) }

func newAcceptancePipeline(t *testing.T, dest types.ExportDestination, opts ...telemetry.ExportOption) *telemetry.Pipeline {
	t.Helper()
	p, err := telemetry.NewPipeline([]types.ExportDestination{dest}, opts...)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	return p
}

type scriptStep struct {
	outcomes []types.RecordOutcome
	err      error
}

type scriptDest struct {
	name string

	mu      sync.Mutex
	steps   []scriptStep
	idx     int
	sentIDs [][]string
}

func newScriptDest(name string) *scriptDest {
	return &scriptDest{name: name}
}

func (d *scriptDest) script(outcomes []types.RecordOutcome, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.steps = append(d.steps, scriptStep{outcomes: outcomes, err: err})
}

func (d *scriptDest) Name() string { return d.name }

func (d *scriptDest) Capabilities() types.DestinationCapabilities {
	return types.DestinationCapabilities{
		Signals:     []types.ExportSignalKind{types.SignalSpan, types.SignalMetric},
		Replay:      types.ReplayDedupByID,
		Temporality: types.TemporalityCumulative,
	}
}

func (d *scriptDest) Export(ctx context.Context, recs []types.ExportRecord) ([]types.RecordOutcome, error) {
	d.mu.Lock()
	step := d.steps[min(d.idx, len(d.steps)-1)]
	d.idx++
	ids := make([]string, 0, len(recs))
	for _, rec := range recs {
		ids = append(ids, string(rec.ID))
	}
	d.sentIDs = append(d.sentIDs, ids)
	d.mu.Unlock()
	return step.outcomes, step.err
}

func (d *scriptDest) attempt(i int) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if i >= len(d.sentIDs) {
		return nil
	}
	return append([]string(nil), d.sentIDs[i]...)
}

func (d *scriptDest) allAttempts() [][]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([][]string, len(d.sentIDs))
	for i, a := range d.sentIDs {
		out[i] = append([]string(nil), a...)
	}
	return out
}

type blockingDest struct {
	name   string
	mu     sync.Mutex
	sent   [][]string
	parked chan struct{}
}

func newBlockingDest(name string) *blockingDest {
	return &blockingDest{name: name, parked: make(chan struct{})}
}

func (d *blockingDest) Name() string { return d.name }

func (d *blockingDest) Capabilities() types.DestinationCapabilities {
	return types.DestinationCapabilities{
		Signals:     []types.ExportSignalKind{types.SignalSpan, types.SignalMetric},
		Replay:      types.ReplayDedupByID,
		Temporality: types.TemporalityCumulative,
	}
}

func (d *blockingDest) Export(ctx context.Context, recs []types.ExportRecord) ([]types.RecordOutcome, error) {
	d.mu.Lock()
	call := len(d.sent)
	ids := make([]string, 0, len(recs))
	for _, rec := range recs {
		ids = append(ids, string(rec.ID))
	}
	d.sent = append(d.sent, ids)
	d.mu.Unlock()
	if call > 0 {
		out := make([]types.RecordOutcome, 0, len(recs))
		for _, id := range ids {
			out = append(out, types.RecordOutcome{ID: types.RecordID(id), Status: types.OutcomeAccepted})
		}
		return out, nil
	}
	var out []types.RecordOutcome
	if len(recs) > 0 {
		out = append(out, types.RecordOutcome{ID: recs[0].ID, Status: types.OutcomeAccepted})
	}
	close(d.parked)
	<-ctx.Done()
	return out, ctx.Err()
}

func (d *blockingDest) sentIDs(i int) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if i >= len(d.sent) {
		return nil
	}
	return append([]string(nil), d.sent[i]...)
}

type capturingDiags struct {
	mu      sync.Mutex
	reports []types.ExportDiagnostic
}

func (c *capturingDiags) Report(ctx context.Context, d types.ExportDiagnostic) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reports = append(c.reports, d)
}

func (c *capturingDiags) ofKind(k types.DiagnosticKind) []types.ExportDiagnostic {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []types.ExportDiagnostic
	for _, r := range c.reports {
		if r.Kind == k {
			out = append(out, r)
		}
	}
	return out
}
