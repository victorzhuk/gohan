package stores

import (
	"context"
	"fmt"

	"github.com/victorzhuk/gohan/core/types"
)

// MemoryFeedback follows the session log's cascade: deleting or erasing a
// session removes its feedback rows with everything else keyed by it.
var _ SessionDependent = (*MemoryFeedback)(nil)

// DeleteSessionDependents drops every feedback row for the session. The
// caller (the session log's cascade) treats an already gone session as done,
// so a missing session is not an error.
func (s *MemoryFeedback) DeleteSessionDependents(_ context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, sessionID)
	return nil
}

// CopySessionDependents carries nothing: feedback is quality telemetry about
// the parent conversation and is never inherited by a fork or child.
func (s *MemoryFeedback) CopySessionDependents(_ context.Context, from, to string) error {
	return nil
}

// RecordFeedback appends the FeedbackRecorded event for f to the run's
// EventLog. The log assigns the next Seq for f.Target.RunID, so the event
// lands after Done, outside the finished run's event stream. The payload
// carries target, name, value and source only: comment and correction text
// never leaves the feedback store.
func RecordFeedback(ctx context.Context, log EventLog, f Feedback) error {
	if log == nil {
		return fmt.Errorf("feedback for run %s: event log is not wired", f.Target.RunID)
	}
	return log.Append(ctx, f.Target.RunID, Event{
		Payload: types.FeedbackRecorded{
			Target: f.Target,
			Name:   f.Name,
			Value:  f.Value,
			Source: f.Source,
		},
	})
}
