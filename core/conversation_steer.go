package gohan

import (
	"context"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// Steer posts the message to the live run's mailbox. The run appends it to
// history at its next safe point and takes one more turn when it had
// already produced its final reply. A session without a Running run
// refuses with ErrRunNotActive and the client falls back to Send; a
// mailbox at MaxPendingSignals refuses with ErrMailboxFull.
func (c *conversation) Steer(ctx context.Context, sessionID string, msg Message) error {
	if err := requirePrincipal(ctx, c.allowAnonymous); err != nil {
		return err
	}
	if err := c.checkSteerOwner(ctx, sessionID); err != nil {
		return err
	}
	run, live := c.find(ctx, sessionID)
	if !live {
		return types.ErrRunNotActive
	}
	return c.runs.Signal(ctx, run.RunID, stores.Signal{Kind: stores.SignalSteer, Message: msg})
}
