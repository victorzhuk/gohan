package telemetry_test

import (
	"context"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
	telemetry "github.com/victorzhuk/gohan/std/telemetry"
)

type callCountingDest struct {
	name  string
	caps  types.DestinationCapabilities
	calls int
}

func (d *callCountingDest) Name() string                                { return d.name }
func (d *callCountingDest) Capabilities() types.DestinationCapabilities { return d.caps }

func (d *callCountingDest) Export(ctx context.Context, recs []types.ExportRecord) ([]types.RecordOutcome, error) {
	d.calls++
	res := make([]types.RecordOutcome, 0, len(recs))
	for _, rec := range recs {
		res = append(res, types.RecordOutcome{ID: rec.ID, Status: types.OutcomeAccepted})
	}
	return res, nil
}

func TestExportSignalRejection(t *testing.T) {
	t.Run("telemetry.export-signal-fidelity", func(t *testing.T) {
		t.Run("span-only destination under default signals", func(t *testing.T) {
			dest := &callCountingDest{
				name: "spans-only",
				caps: types.DestinationCapabilities{
					Signals:     []types.ExportSignalKind{types.SignalSpan},
					Replay:      types.ReplayDedupByID,
					Temporality: types.TemporalityCumulative,
				},
			}
			p, err := telemetry.NewPipeline([]types.ExportDestination{dest})
			if !errors.Is(err, types.ErrExportUnsupportedSignal) {
				t.Fatalf("NewPipeline err = %v, want ErrExportUnsupportedSignal", err)
			}
			if p != nil {
				t.Fatal("pipeline constructed over an incompatible destination")
			}
			if dest.calls != 0 {
				t.Fatalf("Export called %d times, want 0", dest.calls)
			}
		})

		t.Run("destination replay below requirement", func(t *testing.T) {
			dest := &callCountingDest{
				name: "no-replay",
				caps: types.DestinationCapabilities{
					Signals:     []types.ExportSignalKind{types.SignalSpan, types.SignalMetric},
					Replay:      types.ReplayNone,
					Temporality: types.TemporalityCumulative,
				},
			}
			p, err := telemetry.NewPipeline([]types.ExportDestination{dest},
				telemetry.WithRequiredReplay(types.ReplayDedupByID),
			)
			if !errors.Is(err, types.ErrIncompatibleReplay) {
				t.Fatalf("NewPipeline err = %v, want ErrIncompatibleReplay", err)
			}
			if p != nil {
				t.Fatal("pipeline constructed over an incompatible destination")
			}
			if dest.calls != 0 {
				t.Fatalf("Export called %d times, want 0", dest.calls)
			}
		})
	})
}
