package gohan

import (
	"context"
	"fmt"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// MetricFeedback counts and records recorded feedback signals.
const MetricFeedback = "gohan.feedback"

var errFeedbackStoreMissing = fmt.Errorf("gohan: feedback store is not wired")

// auditFeedback classifies the feedback audit record.
const auditFeedback stores.AuditKind = "feedback"

// Feedback records one feedback signal for a session: the store write is
// owner-checked and idempotent per name, the audit trail gains a feedback
// record, a run with an event log gains the FeedbackRecorded event, and
// gohan.feedback emits with the name and flow labels. A numeric value
// records a histogram; any other value counts.
func (s *Stack) Feedback(ctx context.Context, sessionID string, f stores.Feedback) error {
	fb := s.stores.Feedback
	if fb == nil {
		return errFeedbackStoreMissing
	}
	if f.Target.SessionID == "" {
		f.Target.SessionID = sessionID
	}
	version, err := fb.PutFeedback(ctx, f)
	if err != nil {
		return err
	}
	f.Version = version
	if a, ok := decisionAuditFrom(ctx); ok {
		if err := a.Append(ctx, stores.AuditRecord{
			Kind:      auditFeedback,
			SessionID: sessionID,
			RunID:     f.Target.RunID,
			Tenant:    s.tenant.ID,
			Decision:  f.Name,
		}); err != nil {
			return err
		}
	}
	if s.stores.EventLog != nil {
		if err := stores.RecordFeedback(ctx, s.stores.EventLog, f); err != nil {
			return err
		}
	}
	s.emitFeedback(ctx, f)
	return nil
}

func (s *Stack) emitFeedback(ctx context.Context, f stores.Feedback) {
	if s.telemetry == nil {
		return
	}
	ri, _ := types.RunInfoFrom(ctx)
	attrs := []types.Attr{
		types.String("name", f.Name),
	}
	if ri.Flow != "" {
		attrs = append(attrs, types.String(types.KeyFlow, ri.Flow))
	}
	switch v := f.Value.(type) {
	case int:
		s.telemetry.Record(ctx, MetricFeedback, float64(v), attrs...)
	case int64:
		s.telemetry.Record(ctx, MetricFeedback, float64(v), attrs...)
	case float64:
		s.telemetry.Record(ctx, MetricFeedback, v, attrs...)
	case float32:
		s.telemetry.Record(ctx, MetricFeedback, float64(v), attrs...)
	default:
		s.telemetry.Count(ctx, MetricFeedback, 1, attrs...)
	}
}
