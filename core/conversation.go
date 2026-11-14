package gohan

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log/slog"
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
	// ActiveRun reports the id of the run the session currently drives:
	// state Running or Resuming with a live lease. Anything else,
	// including a suspended, terminal or lease-expired run, reports
	// types.ErrRunNotActive without an id.
	ActiveRun(ctx context.Context, sessionID string) (string, error)
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
	spec  string
	rt    runtime.Runtime
	stack *Stack
	// newRun builds one run's native runtime and effect pair after the
	// run's lease is acquired; nil keeps the fixed foreign runtime.
	newRun func(ctx context.Context, sessionID string, lease stores.Lease, input []Message) (context.Context, runtime.AgentRun, error)
	// newResumeRun builds the same governed pair for a resumed run: a
	// fresh runtime over the resolved configuration, the restored
	// history and a ledger seeded from what the run record reports as
	// spent. nil keeps the fixed foreign runtime.
	newResumeRun func(ctx context.Context, sessionID string, hist stores.History, seed float64) (context.Context, runtime.Runtime, runtime.AgentRun, *chains.LimitsState, error)
	// foreignRun builds one run's governed wiring for a registered
	// foreign-runtime flow: the run-scoped ledger, the governed model,
	// tools and assembler, and a fresh backend runtime. A zero
	// hist.Version makes the builder load the session history itself;
	// input rides only a send.
	foreignRun         func(ctx context.Context, sessionID string, hist stores.History, seed float64, input []Message) (context.Context, runtime.Runtime, runtime.AgentRun, *chains.LimitsState, error)
	log                stores.SessionLog
	runs               stores.Runs
	events             stores.EventLog
	cps                stores.Checkpoints
	outputs            stores.OutputStore
	redactor           types.Redactor
	redactionPolicy    types.RedactionPolicy
	redactionMaps      stores.RedactionMaps
	creds              types.CredentialSource
	policySrc          permission.ApprovalPolicySource
	approvalGrants     permission.GrantStore
	toolSpecs          func(name string) (types.ToolSpec, bool)
	verifier           types.Verifier
	journal            stores.Journal
	consolidator       MemoryConsolidator
	consolidationAudit stores.AuditLog
	evaluator          RunEvaluator
	telemetry          types.Telemetry
	detached           bool
	wall               time.Duration
	// stall bounds how long the attached consumer may take no event
	// before the run preempts at its next safe point; zero disables
	// detection.
	stall time.Duration
	// stallAction selects what a run does when the consumer stall guard
	// fires; the zero action preempts.
	stallAction StallAction

	allowAnonymous bool
	tenant         types.Tenant
	titleProfile   string
	recipeModel    func(profile string) (RecipeCall, error)
	titleMu        sync.Mutex
	titled         map[string]bool
	flags          types.Flags
	frozen         map[string]any

	mu      sync.Mutex
	live    map[string]string
	waiters map[string]map[chan struct{}]struct{}
	ended   map[string]bool
}

// bindStack binds the stack-scoped state every constructor shares before
// its option loop: the shutdown admission stack, the tenant, the title
// profile and the frozen flag snapshot. Each constructor calls it before
// reading per-flow configuration, so both flows admit runs against the
// same stack.
func (c *conversation) bindStack(stack *Stack) {
	c.stack = stack
	c.tenant = stack.Tenant()
	c.titleProfile = stack.titleProfile
	c.flags = stack.flags
	c.frozen = stack.frozen
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

// admitRun registers a starting run with the stack's shutdown admission;
// without a stack nothing refuses the run.
func (c *conversation) admitRun(runID string, pre *Preemptor) error {
	if c.stack == nil {
		return nil
	}
	return c.stack.beginRun(runID, pre)
}

// dropRun clears a run that was refused or failed before its drive could
// land at a safe point.
func (c *conversation) dropRun(runID string) {
	if c.stack == nil {
		return
	}
	c.stack.endRun(runID)
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

// verifyOptions appends the verifier and journal the lifecycle's Unknown
// reconciliation needs; each binding is independent, matching the
// resume path's guards.
func (c *conversation) verifyOptions(opts []LifecycleOption) []LifecycleOption {
	if c.verifier != nil {
		opts = append(opts, WithLifecycleVerify(c.verifier))
	}
	if c.journal != nil {
		opts = append(opts, WithLifecycleJournal(c.journal))
	}
	return opts
}

// NewConversation builds the streaming conversation for spec over rt.
// The flow spec names this constructor agent.NewConversation; no agent
// package exists in the M0 package table, so the driver freezes the name.
func NewConversation(stack *Stack, spec string, rt runtime.Runtime, opts ...ConversationOption) (Conversation, error) {
	c := &conversation{spec: spec, rt: rt, live: map[string]string{}}
	if stack != nil {
		c.stack = stack
		c.tenant = stack.Tenant()
		c.log = stack.stores.SessionLog
		c.creds = stack.credentials
		c.policySrc = stack.approvalPolicy
		c.redactionMaps = stack.stores.RedactionMaps
		c.outputs = stack.stores.OutputStore
		if cfg := stack.native[spec]; cfg != nil {
			c.redactor = cfg.redactor
			c.redactionPolicy = cfg.redactionPolicy
		}
		c.consolidator = stack.consolidation(spec)
		c.consolidationAudit = stack.stores.Audit
		c.evaluator = stack.evaluator
		c.allowAnonymous = stack.allowAnonymous
		c.telemetry = stack.telemetry
		c.titleProfile = stack.titleProfile
		c.recipeModel = stack.RecipeModel
		c.flags = stack.flags
		c.frozen = stack.frozen
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
	if c.detached && (stack == nil || stack.Notifier() == nil) {
		stack.logger.Warn("detached flow has no notifier; notices stay in the runs outbox until a dispatcher delivers them", "flow", spec)
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
		if err := rejectReservedMeta([]types.Message{msg}); err != nil {
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
			stored, err := c.redactMessages(ctx, sessionID, []types.Message{msg})
			if err != nil {
				yield(nil, err)
				return
			}
			if _, err := c.log.Append(ctx, sessionID, hist.Version, stored...); err != nil {
				yield(nil, err)
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
		pre := NewPreemptor()
		if err := c.admitRun(runID, pre); err != nil {
			yield(nil, err)
			return
		}
		lease, serr := c.runs.Start(ctx, stores.Run{
			SessionID:   sessionID,
			RunID:       runID,
			Flow:        c.spec,
			OperationID: key,
		}, stores.LeaseTTL)
		if serr != nil {
			c.dropRun(runID)
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
		if !c.appendInput(ctx, lease, sessionID, msg, yield) {
			c.dropRun(runID)
			return
		}
		if c.detached {
			runCtx, cancel := detachedContext(c.detachedDriveCtx(ctx), c.wall)
			go func() {
				defer cancel()
				defer c.untrack(sessionID)
				c.stream(runCtx, lease, sessionID, []Message{msg}, pre, func(Event, error) bool { return true })
			}()
			return
		}
		defer c.untrack(sessionID)
		c.stream(ctx, lease, sessionID, []Message{msg}, pre, yield)
	}
}

// runIdentityCtx replaces the drive context's run identity with the run
// the conversation just acquired: the gate and journal read it from the
// context, and a caller-supplied ambient RunInfo must not survive. Send
// starts an independent root, so RootRunID is the acquired run itself.
func (c *conversation) runIdentityCtx(ctx context.Context, runID, sessionID string) context.Context {
	info := types.RunInfo{
		Flow:      c.spec,
		SessionID: sessionID,
		RunID:     runID,
		RootRunID: runID,
		Mode:      types.Primary,
		Residency: c.tenant.Residency,
	}
	if p, ok := types.PrincipalFrom(ctx); ok {
		info.Principal = p
	}
	if a, ok := types.RunInfoFrom(ctx); ok {
		// A run keeps the tree identity its parent gave it; only a
		// fresh run roots its own tree.
		if a.RunID == runID {
			info.RootRunID = a.RootRunID
			if info.RootRunID == "" {
				info.RootRunID = runID
			}
			info.ParentRunID = a.ParentRunID
			info.Depth = a.Depth
		}
		// Cost attribution follows the caller's tags even though the run
		// identity itself is rebuilt; identity must not survive, tags may.
		info.CostTags = a.CostTags
	}
	if c.flags != nil && len(c.frozen) > 0 {
		snap, err := SnapshotFlags(ctx, c.flags, types.FlagContext{
			Tenant:  info.Principal.Tenant,
			Subject: info.Principal.Subject,
			Session: sessionID,
			Flow:    c.spec,
		}, c.frozen)
		if err != nil {
			slog.WarnContext(ctx, "flag snapshot failed", "session", sessionID, "run", runID, "err", err)
		} else {
			info.Flags = snap
		}
		if c.consolidationAudit != nil {
			rec := stores.AuditRecord{
				Kind:      stores.AuditRunStarted,
				SessionID: sessionID,
				RunID:     runID,
				RootRunID: runID,
				Flow:      c.spec,
				Tenant:    info.Principal.Tenant,
				Subject:   info.Principal.Subject,
			}
			if len(info.Flags) > 0 {
				if b, jerr := json.Marshal(info.Flags); jerr == nil {
					rec.Reason = string(b)
				}
			}
			if aerr := c.consolidationAudit.Append(ctx, rec); aerr != nil {
				slog.WarnContext(ctx, "run-start audit failed", "session", sessionID, "run", runID, "err", aerr)
			}
		}
	}
	return types.WithRunInfo(ctx, info)
}

// appendInput loads the history and appends the acquired run's input under
// the lease it holds. A load or append failure closes the acquired run as
// Failed, so the caller never leaks an active lease, and reports false
// once the terminal error tuple is delivered.
func (c *conversation) appendInput(ctx context.Context, lease stores.Lease, sessionID string, msg Message, yield func(Event, error) bool) bool {
	hist, err := c.load(ctx, sessionID)
	if err == nil {
		var stored []types.Message
		stored, err = c.redactMessages(ctx, sessionID, []types.Message{msg})
		if err == nil {
			err = c.spillInput(ctx, stored)
		}
		if err == nil {
			_, err = c.log.Append(ctx, sessionID, hist.Version, stored...)
		}
	}
	if err != nil {
		lc := NewLifecycle(c.verifyOptions([]LifecycleOption{WithLifecycleRuns(c.runs, lease), WithLifecycleApprovalPolicy(c.policySrc), WithLifecycleToolSpecs(c.toolSpecs)})...)
		if ferr := lc.finishRun(ctx, runtime.State{}, stores.Failed, nil, types.StopFailed); ferr != nil {
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

// ActiveRun reports the id of the run the session currently drives. The
// owner check matches Cancel's, then the in-process live map resolves
// first and the optional SessionRunFinder names a run another pod
// started. A candidate counts only while its state is Running or
// Resuming with a live lease; a stale local entry, a suspended run and a
// terminal run all report types.ErrRunNotActive without an id.
func (c *conversation) ActiveRun(ctx context.Context, sessionID string) (string, error) {
	if err := c.checkSteerOwner(ctx, sessionID); err != nil {
		return "", err
	}
	c.mu.Lock()
	runID, ok := c.live[sessionID]
	c.mu.Unlock()
	if ok && c.runActive(ctx, sessionID, runID) {
		return runID, nil
	}
	if f, ok := c.runs.(SessionRunFinder); ok {
		r, err := f.RunForSession(ctx, sessionID)
		if err == nil {
			if stores.RunActive(r, time.Now()) {
				return r.RunID, nil
			}
			return "", types.ErrRunNotActive
		}
	}
	return "", types.ErrRunNotActive
}

// runActive validates a local live-map entry against the runs store. A
// store with SessionRunFinder judges the lease on its own clock; one
// without it answers through ByID and the shared active rule. A store
// that can report neither keeps the local answer, matching the lookup
// Cancel performs.
func (c *conversation) runActive(ctx context.Context, sessionID, runID string) bool {
	if f, ok := c.runs.(SessionRunFinder); ok {
		r, err := f.RunForSession(ctx, sessionID)
		return err == nil && r.RunID == runID
	}
	b, ok := c.runs.(interface {
		ByID(context.Context, string) (stores.Run, error)
	})
	if !ok {
		return true
	}
	r, err := b.ByID(ctx, runID)
	return err == nil && stores.RunActive(r, time.Now())
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
func (c *conversation) stream(ctx context.Context, lease stores.Lease, sessionID string, input []Message, pre *Preemptor, yield func(Event, error) bool) {
	completed := false
	for ev, err := range c.run(ctx, lease, sessionID, input, pre) {
		if d, ok := ev.(types.Done); ok && err == nil && d.Reason == types.StopCompleted {
			completed = true
		}
		if !yield(ev, err) {
			return
		}
	}
	if completed {
		go c.maybeTitle(ctx, sessionID)
	}
}

// relay records one event in the run's log before it is delivered. A record
// failure ends the stream with one terminal error tuple: a caller
// reattaching later would otherwise miss the event, and an error tuple is
// never followed by another tuple.
func (c *conversation) relay(ctx context.Context, runID string, ev Event, yield func(Event, error) bool) bool {
	stored, err := c.redactEvent(ctx, ev)
	if err != nil {
		yield(nil, fmt.Errorf("redact event: %w", err))
		return false
	}
	if err := c.events.Append(ctx, runID, stores.Event{Payload: stored, Meta: types.EventMeta{RunID: runID}}); err != nil {
		yield(nil, fmt.Errorf("record event: %w", err))
		return false
	}
	if msg, ok := stored.(types.AssistantMessage); ok && c.outputs != nil {
		info, _ := types.RunInfoFrom(ctx)
		info.RunID = runID
		if _, err := c.outputs.Put(ctx, info, msg.Message.Blocks); err != nil {
			yield(nil, fmt.Errorf("store output: %w", err))
			return false
		}
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

// spillInput writes oversized blob bytes into the content store before the
// input is persisted: the stored block keeps only the ref, so history never
// carries the payload and assembly resolves the upload per profile.
func (c *conversation) spillInput(ctx context.Context, msgs []types.Message) error {
	if c.outputs == nil {
		return nil
	}
	info, _ := types.RunInfoFrom(ctx)
	spill := func(b types.Block, data []byte) (types.Blob, error) {
		ref, err := c.outputs.Put(ctx, info, []types.Block{b})
		if err != nil {
			return types.Blob{}, fmt.Errorf("store input blob: %w", err)
		}
		sum := sha256.Sum256(data)
		return types.Blob{Ref: ref, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(data))}, nil
	}
	for i := range msgs {
		for j, b := range msgs[i].Blocks {
			switch v := b.(type) {
			case types.Image:
				if len(v.Data) <= stores.InlineBlobBytes {
					continue
				}
				blob, err := spill(b, v.Data)
				if err != nil {
					return err
				}
				v.Blob, v.Data = blob, nil
				msgs[i].Blocks[j] = v
			case types.Audio:
				if len(v.Data) <= stores.InlineBlobBytes {
					continue
				}
				blob, err := spill(b, v.Data)
				if err != nil {
					return err
				}
				v.Blob, v.Data = blob, nil
				msgs[i].Blocks[j] = v
			case types.File:
				if len(v.Data) <= stores.InlineBlobBytes {
					continue
				}
				blob, err := spill(b, v.Data)
				if err != nil {
					return err
				}
				v.Blob, v.Data = blob, nil
				msgs[i].Blocks[j] = v
			}
		}
	}
	return nil
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
func (c *conversation) redactMessages(ctx context.Context, sessionID string, msgs []types.Message) ([]types.Message, error) {
	return redactMessagesWith(ctx, sessionID, msgs, c.redactor, c.redactionPolicy, c.redactionMaps)
}

// redactMessagesWith redacts message blocks under the redaction policy and
// merges the produced map into the session's stored map.
func redactMessagesWith(ctx context.Context, sessionID string, msgs []types.Message, redactor types.Redactor, policy types.RedactionPolicy, maps stores.RedactionMaps) ([]types.Message, error) {
	if redactor == nil || len(policy.Detectors) == 0 {
		return msgs, nil
	}
	info, _ := types.RunInfoFrom(ctx)
	info.SessionID = sessionID
	ctx = types.WithRunInfo(ctx, info)
	blocks := make([]types.Block, 0)
	for _, msg := range msgs {
		blocks = append(blocks, msg.Blocks...)
	}
	redacted, m, err := redactor.Redact(ctx, blocks, policy)
	if err != nil {
		return nil, fmt.Errorf("redact messages: %w", err)
	}
	if maps == nil {
		return nil, fmt.Errorf("redaction map store missing")
	}
	m, err = mergeRedactionMapWith(ctx, m, maps)
	if err != nil {
		return nil, err
	}
	if err := maps.Put(ctx, info.Principal.Tenant, sessionID, m); err != nil {
		return nil, fmt.Errorf("store redaction map: %w", err)
	}
	out := append([]types.Message(nil), msgs...)
	offset := 0
	for i := range out {
		n := len(out[i].Blocks)
		out[i].Blocks = append([]types.Block(nil), redacted[offset:offset+n]...)
		offset += n
	}
	return out, nil
}

func mergeRedactionMapWith(ctx context.Context, m types.RedactionMap, maps stores.RedactionMaps) (types.RedactionMap, error) {
	info, _ := types.RunInfoFrom(ctx)
	old, err := maps.Get(ctx, info.Principal.Tenant, info.SessionID)
	if err != nil && !errors.Is(err, stores.ErrRedactionMapNotFound) {
		return types.RedactionMap{}, fmt.Errorf("load redaction map: %w", err)
	}
	for token, entity := range old.Entries {
		if m.Entries == nil {
			m.Entries = make(map[string]types.Entity)
		}
		m.Entries[token] = entity
	}
	return m, nil
}

func (c *conversation) redactEvent(ctx context.Context, event Event) (Event, error) {
	return redactEventWith(ctx, event, c.redactor, c.redactionPolicy, c.redactionMaps)
}

// redactEventWith redacts one event's payloads under the redaction policy.
func redactEventWith(ctx context.Context, event Event, redactor types.Redactor, policy types.RedactionPolicy, maps stores.RedactionMaps) (Event, error) {
	if redactor == nil || len(policy.Detectors) == 0 {
		return event, nil
	}
	info, _ := types.RunInfoFrom(ctx)
	switch e := event.(type) {
	case types.TextDelta:
		msgs, err := redactMessagesWith(ctx, info.SessionID, []types.Message{{Blocks: []types.Block{types.Text{Text: e.Delta}}}}, redactor, policy, maps)
		if err != nil {
			return nil, err
		}
		if len(msgs[0].Blocks) > 0 {
			e.Delta = msgs[0].Blocks[0].(types.Text).Text
		}
		return e, nil
	case types.ReasoningDelta:
		msgs, err := redactMessagesWith(ctx, info.SessionID, []types.Message{{Blocks: []types.Block{types.Reasoning{Text: e.Delta}}}}, redactor, policy, maps)
		if err != nil {
			return nil, err
		}
		e.Delta = msgs[0].Blocks[0].(types.Reasoning).Text
		return e, nil
	case types.ResultDelta:
		msgs, err := redactMessagesWith(ctx, info.SessionID, []types.Message{{Blocks: []types.Block{types.Text{Text: e.Delta}}}}, redactor, policy, maps)
		if err != nil {
			return nil, err
		}
		e.Delta = msgs[0].Blocks[0].(types.Text).Text
		return e, nil
	case types.ToolArgsDelta:
		msgs, err := redactMessagesWith(ctx, info.SessionID, []types.Message{{Blocks: []types.Block{types.Text{Text: e.Delta}}}}, redactor, policy, maps)
		if err != nil {
			return nil, err
		}
		e.Delta = msgs[0].Blocks[0].(types.Text).Text
		return e, nil
	case types.AssistantMessage:
		out, err := redactMessagesWith(ctx, info.SessionID, []types.Message{e.Message}, redactor, policy, maps)
		if err != nil {
			return nil, err
		}
		e.Message = out[0]
		return e, nil
	case types.ToolStarted:
		out, err := redactMessagesWith(ctx, info.SessionID, []types.Message{{Blocks: []types.Block{e.Call}}}, redactor, policy, maps)
		if err != nil {
			return nil, err
		}
		e.Call = out[0].Blocks[0].(types.ToolUse)
		return e, nil
	case types.ToolFinished:
		out, err := redactMessagesWith(ctx, info.SessionID, []types.Message{{Blocks: []types.Block{e.Result}}}, redactor, policy, maps)
		if err != nil {
			return nil, err
		}
		e.Result = out[0].Blocks[0].(types.ToolResult)
		return e, nil
	case types.Suspended:
		return e, nil
	default:
		return event, nil
	}
}
