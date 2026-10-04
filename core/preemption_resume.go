package gohan

import (
	"context"
	"iter"
)

// ResumePreempted resumes a run suspended with Preempted through the landed
// Conversation.Resume seam with Continue(): the single-use token is
// consumed, the checkpointed state re-drives and no approver is required
// (runtime.preempted-resume-continue).
func ResumePreempted(ctx context.Context, c Conversation, t ResumeToken) iter.Seq2[Event, error] {
	return c.Resume(ctx, t, Continue())
}
