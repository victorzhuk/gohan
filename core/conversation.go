package gohan

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"iter"
	"sync"
	"time"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/permission"
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
	Attach(ctx context.Context, runID string, afterSeq int64) iter.Seq2[Event, error]
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
	spec string
	rt   runtime.Runtime
	// newRun builds one run's native runtime and effect pair after the
	// run's lease is acquired; nil keeps the fixed foreign runtime.
	newRun func(ctx context.Context, sessionID string, lease stores.Lease, input []Message) (context.Context, runtime.AgentRun, error)
	// newResumeRun builds the same governed pair for a resumed run: a
	// fresh runtime over the resolved configuration, the restored
	// history and a ledger seeded from what the run record reports as
	// spent. nil keeps the fixed foreign runtime.
	newResumeRun func(ctx context.Context, sessionID string, hist stores.History, seed float64) (context.Context, runtime.Runtime, runtime.AgentRun, *chains.LimitsState, error)
	log          stores.SessionLog
	runs         stores.Runs
	events       stores.EventLog
	cps          stores.Checkpoints
	creds        types.CredentialSource
	policySrc    permission.ApprovalPolicySource
	toolSpecs    func(name string) (types.ToolSpec, bool)
	detached     bool
	wall         time.Duration
	// stall bounds how long the attached consumer may take no event
	// before the run preempts at its next safe point; zero disables
	// detection.
	stall time.Duration
	// stallAction selects what a run does when the consumer stall guard
	// fires; the zero action preempts.
	stallAction StallAction

	allowAnonymous bool

	mu      sync.Mutex
	live    map[string]string
	waiters map[string]map[chan struct{}]struct{}
	ended   map[string]bool
}

// markRunEnded records that the run produced its last event and wakes any
// Attach still waiting on it, so a late subscriber cannot sleep past the
// run's end.
func (c *conversation) markRunEnded(runID string) {
	c.mu.Lock()
	if c.ended == nil {
		c.ended = map[string]bool{}
	}
	c.ended[runID] = true
	c.mu.Unlock()
	c.notify(runID)
}

// runEnded reports whether the run has finished producing events.
func (c *conversation) runEnded(runID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ended[runID]
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

// OnStall selects what a run does when its consumer takes no event for
// RunLimits.ConsumerStall. The zero action preempts; StallDetach stops
// attached delivery and continues the run detached, so it needs an event
// log a reattaching client can read.
func OnStall(action StallAction) ConversationOption {
	return func(c *conversation) { c.stallAction = action }
}

// WithConversationToolSpecs binds the registered tool-spec lookup a
// HumanApproval suspension resolves each pending call's declaration
// through.
func WithConversationToolSpecs(lookup func(name string) (types.ToolSpec, bool)) ConversationOption {
	return func(c *conversation) { c.toolSpecs = lookup }
}

// NewConversation builds the streaming conversation for spec over rt.
// The flow spec names this constructor agent.NewConversation; no agent
// package exists in the M0 package table, so the driver freezes the name.
func NewConversation(stack *Stack, spec string, rt runtime.Runtime, opts ...ConversationOption) (Conversation, error) {
	c := &conversation{spec: spec, rt: rt, live: map[string]string{}}
	if stack != nil {
		c.log = stack.stores.SessionLog
		c.creds = stack.credentials
		c.policySrc = stack.approvalPolicy
		c.allowAnonymous = stack.allowAnonymous
		if l, ok := stack.Limits(spec); ok {
			c.wall = l.MaxWallClock
			c.stall = l.ConsumerStall
		}
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.runs == nil {
		return nil, errConversationRuns
	}
	if c.detached && c.events == nil {
		return nil, errDetachedNoLog
	}
	if c.stallAction == StallDetach && c.events == nil {
		return nil, errDetachedNoLog
	}
	if c.events == nil {
		return nil, errConversationEvents
	}
	return c, nil
}

// Send appends the user message and streams one run. The run's lease is
// acquired before history loads or the input is appended, so a refused
// send costs no append and a duplicate operation id reattaches to the
// recorded run's events. A session under human control appends without
// acquiring a run.
func (c *conversation) Send(ctx context.Context, sessionID string, msg Message) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		if err := requirePrincipal(ctx, c.allowAnonymous); err != nil {
			yield(nil, err)
			return
		}
		if err := c.checkAnonymousSession(ctx, sessionID); err != nil {
			yield(nil, err)
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
		if ctrl == stores.ControlHuman {
			hist, err := c.load(ctx, sessionID)
			if err != nil {
				yield(nil, err)
				return
			}
			if _, aerr := c.log.Append(ctx, sessionID, hist.Version, msg); aerr != nil {
				yield(nil, aerr)
				return
			}
			yield(types.Done{Reason: types.StopHandedOff}, nil)
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
		if !c.appendInput(ctx, lease, sessionID, msg, yield) {
			return
		}
		if c.detached {
			runCtx, cancel := detachedContext(ctx, c.wall)
			go func() {
				defer cancel()
				defer c.untrack(sessionID)
				c.stream(runCtx, lease, sessionID, []Message{msg}, func(Event, error) bool { return true })
			}()
			return
		}
		defer c.untrack(sessionID)
		c.stream(ctx, lease, sessionID, []Message{msg}, yield)
	}
}

// appendInput loads the history and appends the acquired run's input under
// the lease it holds. A load or append failure closes the acquired run as
// Failed, so the caller never leaks an active lease, and reports false
// once the terminal error tuple is delivered.
func (c *conversation) appendInput(ctx context.Context, lease stores.Lease, sessionID string, msg Message, yield func(Event, error) bool) bool {
	hist, err := c.load(ctx, sessionID)
	if err == nil {
		_, err = c.log.Append(ctx, sessionID, hist.Version, msg)
	}
	if err != nil {
		lc := NewLifecycle(WithLifecycleRuns(c.runs, lease), WithLifecycleApprovalPolicy(c.policySrc), WithLifecycleToolSpecs(c.toolSpecs))
		if ferr := lc.finishRun(ctx, runtime.State{}, stores.Failed, nil); ferr != nil {
			yield(nil, ferr)
			return false
		}
		c.untrack(sessionID)
		yield(nil, err)
		return false
	}
	return true
}

// checkAnonymousSession keeps anonymous invocation off the session face:
// spec rule 1 permits it only for unowned function flows.
func (c *conversation) checkAnonymousSession(ctx context.Context, sessionID string) error {
	if _, ok := PrincipalFrom(ctx); ok {
		return nil
	}
	return fmt.Errorf("anonymous invocation: session %s: %w", sessionID, types.ErrSessionForbidden)
}

// Cancel posts SignalCancel to the session's run and returns once the runs
// store shows the run finished or the lease TTL elapsed.
func (c *conversation) Cancel(ctx context.Context, sessionID string) error {
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
// event in the run's log as it is delivered.
func (c *conversation) stream(ctx context.Context, lease stores.Lease, sessionID string, input []Message, yield func(Event, error) bool) {
	for ev, err := range c.run(ctx, lease, sessionID, input) {
		if !yield(ev, err) {
			return
		}
	}
}

// relay records one event in the run's log before it is delivered. A record
// failure ends the stream with one terminal error tuple: a caller
// reattaching later would otherwise miss the event, and an error tuple is
// never followed by another tuple.
func (c *conversation) relay(ctx context.Context, runID string, ev Event, yield func(Event, error) bool) bool {
	if err := c.events.Append(ctx, runID, stores.Event{Payload: ev, Meta: types.EventMeta{RunID: runID}}); err != nil {
		yield(nil, fmt.Errorf("record event: %w", err))
		return false
	}
	c.notify(runID)
	return true
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

// newRunID mints a run id collision-resistant across processes: a
// process-local counter collides between pods over one runs store.
func newRunID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("mint run id: %w", err)
	}
	return "run-" + hex.EncodeToString(b[:]), nil
}
