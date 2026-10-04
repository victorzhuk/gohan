package gohan

import (
	"context"
	"errors"
	"iter"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// Continue runs one assistant turn on the session's existing history
// without appending input. It follows the same lifecycle, lease and
// idempotency rules as Send: a live lease refuses before anything starts,
// a handed-off session refuses with ErrSessionHandedOff, and a session
// without messages refuses with ErrEmptyHistory.
func (c *conversation) Continue(ctx context.Context, sessionID string) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		p, ok := PrincipalFrom(ctx)
		if !ok {
			yield(nil, types.ErrNoPrincipal)
			return
		}
		if h, ok := c.runs.(stores.SessionLeaseHolder); ok && h.SessionLeaseActive(ctx, sessionID) {
			yield(nil, types.ErrRunActive)
			return
		}
		ctrl := stores.ControlAgent
		if r, ok := c.log.(SessionControlReader); ok {
			v, err := r.Control(ctx, sessionID)
			if err != nil {
				yield(nil, err)
				return
			}
			ctrl = v
		}
		if ctrl == stores.ControlHuman {
			yield(nil, types.ErrSessionHandedOff)
			return
		}
		hist, err := c.load(ctx, sessionID)
		if err != nil {
			yield(nil, err)
			return
		}
		if len(hist.Messages) == 0 {
			yield(nil, types.ErrEmptyHistory)
			return
		}
		key, _ := IdempotencyKey(ctx)
		if key != "" {
			if run, berr := c.runs.ByOperation(ctx, p.Tenant, key); berr == nil {
				c.reattach(ctx, run.RunID, yield)
				return
			} else if !errors.Is(berr, stores.ErrRunNotFound) {
				yield(nil, berr)
				return
			}
		}
		runID := c.nextRunID()
		lease, serr := c.runs.Start(ctx, stores.Run{
			SessionID:   sessionID,
			RunID:       runID,
			Flow:        c.spec,
			OperationID: key,
		}, stores.LeaseTTL)
		if serr != nil {
			var exists stores.OperationExistsError
			if errors.As(serr, &exists) {
				c.reattach(ctx, exists.RunID, yield)
				return
			}
			yield(nil, serr)
			return
		}
		c.track(sessionID, runID)
		defer c.untrack(sessionID)
		c.stream(ctx, lease, sessionID, nil, yield)
	}
}
