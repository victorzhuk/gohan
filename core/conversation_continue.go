package gohan

import (
	"context"
	"errors"
	"iter"

	"github.com/victorzhuk/gohan/core/runtime"
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
		if err := requirePrincipal(ctx, c.allowAnonymous); err != nil {
			yield(nil, err)
			return
		}
		if err := c.checkAnonymousSession(ctx, sessionID); err != nil {
			yield(nil, err)
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
		key, _ := IdempotencyKey(ctx)
		runID, rerr := newRunID()
		if rerr != nil {
			yield(nil, rerr)
			return
		}
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
		ctx = c.runIdentityCtx(ctx, runID, sessionID)
		hist, err := c.load(ctx, sessionID)
		if err == nil && len(hist.Messages) == 0 {
			err = types.ErrEmptyHistory
		}
		if err != nil {
			lc := NewLifecycle(WithLifecycleRuns(c.runs, lease), WithLifecycleApprovalPolicy(c.policySrc), WithLifecycleToolSpecs(c.toolSpecs))
			if ferr := lc.finishRun(ctx, runtime.State{}, stores.Failed, nil); ferr != nil {
				yield(nil, ferr)
				return
			}
			c.untrack(sessionID)
			yield(nil, err)
			return
		}
		defer c.untrack(sessionID)
		c.stream(ctx, lease, sessionID, nil, yield)
	}
}
