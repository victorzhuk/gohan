package gohan

import (
	"context"
	"errors"
	"time"
)

// errDetachedNoLog is the constructor error for a Detached flow without an
// event log: a client could never reattach, so the run would be unreadable.
var errDetachedNoLog = errors.New("gohan: detached flow needs an event log")

// Detached switches the conversation's runs to detached lifetime: Send
// returns once the run is started, the run executes under a harness-owned
// context bounded by the flow's MaxWallClock, and clients consume events
// through Attach. A detached conversation needs an event log.
func Detached() ConversationOption {
	return func(c *conversation) { c.detached = true }
}

// detachedContext derives the context a detached run executes under. It
// keeps values from the caller's context but not its cancellation: the run
// outlives the client that started it. The wall-clock bound comes from the
// flow's limits; zero leaves the run to the runs store's lease expiry.
func detachedContext(ctx context.Context, wall time.Duration) (context.Context, context.CancelFunc) {
	base := context.WithoutCancel(ctx)
	if wall <= 0 {
		return context.WithCancel(base)
	}
	return context.WithTimeout(base, wall)
}
