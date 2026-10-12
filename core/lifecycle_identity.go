package gohan

import (
	"fmt"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// WithLifecycleRuns binds the runs store and the lease Drive holds. Both
// are required for suspension and Finish; without them the driver only
// orders events.
func WithLifecycleRuns(r stores.Runs, l stores.Lease) LifecycleOption {
	return func(lc *Lifecycle) { lc.runs, lc.lease = r, l }
}

// WithLifecycleResumeState drives the state a checkpoint carried instead of
// the position Runtime.Start returns. Start still runs once to install the
// per-run wiring; the saved state then replaces its initial position, so
// initial effects never replay.
func WithLifecycleResumeState(st runtime.State) LifecycleOption {
	return func(lc *Lifecycle) { lc.resumeState = &st }
}

// WithLifecycleLease sets the externally acquired lease the lifecycle
// holds: every Drain, Suspend, Finish and heartbeat refresh runs under it.
// A later option replaces an earlier lease binding.
func WithLifecycleLease(l stores.Lease) LifecycleOption {
	return func(lc *Lifecycle) { lc.lease = l }
}

// WithLifecycleFlow sets the flow spec a suspended checkpoint records.
func WithLifecycleFlow(flow string) LifecycleOption {
	return func(lc *Lifecycle) { lc.flow = flow }
}

// WithLifecycleResidency sets the residency a suspended checkpoint
// records in its run identity.
func WithLifecycleResidency(region string) LifecycleOption {
	return func(lc *Lifecycle) { lc.residency = region }
}

// WithLifecycleOriginator sets the principal a suspended checkpoint
// restores its credentials for.
func WithLifecycleOriginator(p types.Principal) LifecycleOption {
	return func(lc *Lifecycle) { lc.originator = p }
}

// WithLifecycleSession sets the session id a checkpoint records.
func WithLifecycleSession(id string) LifecycleOption {
	return func(lc *Lifecycle) { lc.sessionID = id }
}

// WithLifecycleTelemetry binds the emission port the run span and the
// governed counters record through. A lifecycle without one emits nothing.
func WithLifecycleTelemetry(t types.Telemetry) LifecycleOption {
	return func(lc *Lifecycle) { lc.telemetry = t }
}

// heartbeatErr reports a lost lease: the driver starts no further effect.
func (lc *Lifecycle) heartbeatErr() error {
	if lc.hb == nil {
		return nil
	}
	if err := lc.hb.Err(); err != nil {
		return fmt.Errorf("run lease lost: %w", err)
	}
	return nil
}

// stopHeartbeat joins the heartbeat helper and adopts the last lease it
// held. Every terminal store transition runs after it.
func (lc *Lifecycle) stopHeartbeat() {
	if lc.hb == nil {
		return
	}
	lc.lease = lc.hb.Stop()
	lc.hb = nil
}
