package gohan

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// Conversation is the streaming face of an agent session. Send and Cancel
// carry the run lifecycle; Continue, Resume and Steer extend it in rows
// 23.3 and 23.4.
type Conversation interface {
	Send(ctx context.Context, sessionID string, msg Message) iter.Seq2[Event, error]
	Continue(ctx context.Context, sessionID string) iter.Seq2[Event, error]
	Resume(ctx context.Context, t ResumeToken, r stores.ResumeInput) iter.Seq2[Event, error]
	Cancel(ctx context.Context, sessionID string) error
	Steer(ctx context.Context, sessionID string, msg Message) error
}

// SessionControlReader is the optional per-session control read on a
// session log. A log without it reports ControlAgent.
type SessionControlReader interface {
	Control(ctx context.Context, sessionID string) (stores.SessionControl, error)
}

// SessionRunFinder is the optional per-session run lookup on a runs store.
// It lets Cancel reach a run another pod started; without it only runs
// this process started are cancellable.
type SessionRunFinder interface {
	RunForSession(ctx context.Context, sessionID string) (stores.Run, error)
}

var (
	errConversationRuns   = errors.New("gohan: conversation needs a runs store")
	errConversationEvents = errors.New("gohan: conversation needs an event log")
)

type conversation struct {
	spec   string
	rt     runtime.Runtime
	log    stores.SessionLog
	runs   stores.Runs
	events stores.EventLog
	cps    stores.Checkpoints
	creds  types.CredentialSource

	mu    sync.Mutex
	live  map[string]string
	runID int
}

// ConversationOption configures a Conversation built by NewConversation.
type ConversationOption func(*conversation)

// WithConversationRuns binds the runs store the conversation starts runs
// and posts signals against.
func WithConversationRuns(r stores.Runs) ConversationOption {
	return func(c *conversation) { c.runs = r }
}

// WithConversationEventLog binds the event log Send records the run's
// events into and reattachment reads from.
func WithConversationEventLog(l stores.EventLog) ConversationOption {
	return func(c *conversation) { c.events = l }
}

// NewConversation builds the streaming conversation for spec over rt.
// The flow spec names this constructor agent.NewConversation; no agent
// package exists in the M0 package table, so the driver freezes the name.
func NewConversation(stack *Stack, spec string, rt runtime.Runtime, opts ...ConversationOption) (Conversation, error) {
	c := &conversation{spec: spec, rt: rt, live: map[string]string{}}
	if stack != nil {
		c.log = stack.stores.SessionLog
		c.creds = stack.credentials
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.runs == nil {
		return nil, errConversationRuns
	}
	if c.events == nil {
		return nil, errConversationEvents
	}
	return c, nil
}

// Send appends the user message and streams one run. A live lease refuses
// before anything is appended, a session under human control costs no run,
// and an idempotency key reattaches to the recorded run's events.
func (c *conversation) Send(ctx context.Context, sessionID string, msg Message) iter.Seq2[Event, error] {
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
		// The control state is read before Runs.Start, or a session under
		// human control would record a run it must not.
		ctrl := stores.ControlAgent
		if r, ok := c.log.(SessionControlReader); ok {
			v, err := r.Control(ctx, sessionID)
			if err != nil {
				yield(nil, err)
				return
			}
			ctrl = v
		}
		hist, err := c.load(ctx, sessionID)
		if err != nil {
			yield(nil, err)
			return
		}
		if ctrl == stores.ControlHuman {
			if _, aerr := c.log.Append(ctx, sessionID, hist.Version, msg); aerr != nil {
				yield(nil, aerr)
				return
			}
			yield(types.Done{Reason: types.StopHandedOff}, nil)
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
		if _, aerr := c.log.Append(ctx, sessionID, hist.Version, msg); aerr != nil {
			yield(nil, aerr)
			return
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
		c.stream(ctx, lease, sessionID, []Message{msg}, yield)
	}
}

// Cancel posts SignalCancel to the session's run and returns once the runs
// store shows the run finished or the lease TTL elapsed.
func (c *conversation) Cancel(ctx context.Context, sessionID string) error {
	if _, ok := PrincipalFrom(ctx); !ok {
		return types.ErrNoPrincipal
	}
	run, live := c.find(ctx, sessionID)
	if !live {
		return types.ErrRunNotActive
	}
	if err := c.runs.Signal(ctx, run.RunID, stores.Signal{Kind: stores.SignalCancel}); err != nil {
		return err
	}
	h, ok := c.runs.(stores.SessionLeaseHolder)
	if !ok {
		return nil
	}
	deadline := time.Now().Add(stores.LeaseTTL)
	for h.SessionLeaseActive(ctx, sessionID) {
		if !time.Now().Before(deadline) {
			return nil
		}
		time.Sleep(time.Millisecond)
	}
	return nil
}

// find resolves the session's live run: one this process started first,
// then one the runs store can name for another pod.
func (c *conversation) find(ctx context.Context, sessionID string) (stores.Run, bool) {
	c.mu.Lock()
	runID, ok := c.live[sessionID]
	c.mu.Unlock()
	if ok {
		return stores.Run{SessionID: sessionID, RunID: runID}, true
	}
	if f, ok := c.runs.(SessionRunFinder); ok {
		r, err := f.RunForSession(ctx, sessionID)
		if err == nil {
			return r, true
		}
	}
	return stores.Run{}, false
}

// stream drives the run under the lifecycle ordering, recording every
// event in the run's log as it is yielded.
func (c *conversation) stream(ctx context.Context, lease stores.Lease, sessionID string, input []Message, yield func(Event, error) bool) {
	lc := NewLifecycle(
		WithLifecycleRuns(c.runs, lease),
		WithLifecycleSession(sessionID),
	)
	ag := runtime.AgentRun{Input: input}
	if c.cps != nil {
		// Suspension persists through the conversation's checkpoints
		// store; without one the runtime cannot suspend.
		ag.Save = c.cps.Put
	}
	for ev, err := range DriveLifecycle(ctx, lc, c.rt, ag) {
		if err != nil {
			var gb *types.GuardBlockedError
			if errors.As(err, &gb) {
				gbEv := types.GuardBlocked{Stage: gb.Stage, Reason: gb.Reason}
				c.relay(ctx, lease.RunID, gbEv, yield)
				if !yield(gbEv, nil) {
					return
				}
				doneEv := types.Done{Reason: types.StopGuardBlocked}
				c.relay(ctx, lease.RunID, doneEv, yield)
				if !yield(doneEv, nil) {
					return
				}
				c.finishQuietly(ctx, lease)
				return
			}
			yield(nil, err)
			c.finishQuietly(ctx, lease)
			return
		}
		c.relay(ctx, lease.RunID, ev, yield)
		if !yield(ev, nil) {
			return
		}
	}
}

// relay records one event in the run's log; a record failure ends the
// stream, since a caller reattaching later would otherwise miss it.
func (c *conversation) relay(ctx context.Context, runID string, ev Event, yield func(Event, error) bool) {
	if err := c.events.Append(ctx, runID, stores.Event{Payload: ev, Meta: types.EventMeta{RunID: runID}}); err != nil {
		yield(nil, fmt.Errorf("record event: %w", err))
	}
}

func (c *conversation) finishQuietly(ctx context.Context, lease stores.Lease) {
	_ = c.runs.Finish(ctx, lease, stores.Failed, nil, "")
}

// reattach replays the recorded run's events from sequence 1.
func (c *conversation) reattach(ctx context.Context, runID string, yield func(Event, error) bool) {
	for e, err := range c.events.Read(ctx, runID, 0) {
		if err != nil {
			yield(nil, err)
			return
		}
		if !yield(e.Payload, nil) {
			return
		}
	}
}

func (c *conversation) load(ctx context.Context, sessionID string) (stores.History, error) {
	if c.log == nil {
		return stores.History{}, types.ErrSessionIndexRequired
	}
	h, err := c.log.Load(ctx, sessionID)
	if errors.Is(err, stores.ErrSessionNotFound) {
		// Send starts a session that does not exist yet; Append with
		// expectedVersion 0 creates it.
		return stores.History{}, nil
	}
	return h, err
}

func (c *conversation) track(sessionID, runID string) {
	c.mu.Lock()
	c.live[sessionID] = runID
	c.mu.Unlock()
}

func (c *conversation) untrack(sessionID string) {
	c.mu.Lock()
	delete(c.live, sessionID)
	c.mu.Unlock()
}

func (c *conversation) nextRunID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.runID++
	return fmt.Sprintf("run-%08x", c.runID)
}
