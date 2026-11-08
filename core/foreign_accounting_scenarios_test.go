package gohan_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std"
)

// foreignAccountArgs is the fingerprinted argument body every governed
// call in these fixtures issues.
const foreignAccountArgs = `{"n":1}`

// foreignAdmissionMetaKey is the reserved session-history metadata key a
// core-written foreign admission record carries.
const foreignAdmissionMetaKey = "gohan.foreign.admission"

// foreignAccountAdmission is the durable shape of one admission record,
// decoded from session-history metadata.
type foreignAccountAdmission struct {
	RunID         string `json:"run_id"`
	AdmittedTotal int    `json:"admitted_total"`
	CallID        string `json:"call_id"`
}

// foreignAccountCommitTool is the flow's SideEffect tool. In blocking
// mode it signals started, waits for release, and only then settles: the
// shield must let the committed execution finish under cancellation.
type foreignAccountCommitTool struct {
	mu      sync.Mutex
	ran     int
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (t *foreignAccountCommitTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "commit", Risk: types.RiskHigh, Effect: types.SideEffect}
}

func (t *foreignAccountCommitTool) Call(context.Context, json.RawMessage) (types.ToolResult, error) {
	if t.started != nil {
		t.once.Do(func() { close(t.started) })
		<-t.release
	}
	t.mu.Lock()
	t.ran++
	t.mu.Unlock()
	return types.ToolResult{Outcome: types.Succeeded}, nil
}

func (t *foreignAccountCommitTool) runs() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ran
}

// foreignAccountPeekTool is an unshielded ReadOnly tool: it signals
// started, then aborts on the caller's cancellation.
type foreignAccountPeekTool struct {
	mu      sync.Mutex
	ran     int
	aborted error
	started chan struct{}
	once    sync.Once
}

func (t *foreignAccountPeekTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "peek", Risk: types.RiskLow, Effect: types.ReadOnly}
}

func (t *foreignAccountPeekTool) Call(ctx context.Context, _ json.RawMessage) (types.ToolResult, error) {
	if t.started != nil {
		t.once.Do(func() { close(t.started) })
	}
	t.mu.Lock()
	t.ran++
	t.mu.Unlock()
	<-ctx.Done()
	t.mu.Lock()
	t.aborted = ctx.Err()
	t.mu.Unlock()
	return types.ToolResult{}, ctx.Err()
}

func (t *foreignAccountPeekTool) abort() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.aborted
}

// foreignAccountAfterTool marks the component after the cancelled one:
// cancellation must stop it from ever running.
type foreignAccountAfterTool struct {
	mu  sync.Mutex
	ran int
}

func (t *foreignAccountAfterTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "after", Risk: types.RiskLow, Effect: types.ReadOnly}
}

func (t *foreignAccountAfterTool) Call(context.Context, json.RawMessage) (types.ToolResult, error) {
	t.mu.Lock()
	t.ran++
	t.mu.Unlock()
	return types.ToolResult{Outcome: types.Succeeded}, nil
}

func (t *foreignAccountAfterTool) runs() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ran
}

// foreignAccountPlanRT issues one governed call per step until its plan
// is exhausted, replaying a parked pending call under its own identity.
type foreignAccountPlanRT struct {
	mu      sync.Mutex
	ag      runtime.AgentRun
	plan    int
	issued  int
	results []types.ToolResult
}

func (r *foreignAccountPlanRT) Name() string                         { return "acct.test" }
func (r *foreignAccountPlanRT) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

func (r *foreignAccountPlanRT) Start(_ context.Context, ag runtime.AgentRun) (runtime.State, error) {
	r.mu.Lock()
	r.ag = ag
	r.mu.Unlock()
	return runtime.State{}, nil
}

func (r *foreignAccountPlanRT) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	var call types.ToolUse
	if len(st.Pending) > 0 {
		call = st.Pending[0]
	} else {
		r.mu.Lock()
		if r.issued >= r.plan {
			r.mu.Unlock()
			nst := st
			nst.Turn = st.Turn + 1
			return nst, nil, runtime.DoneStatus, nil
		}
		r.issued++
		call = types.ToolUse{ID: fmt.Sprintf("call-%d", r.issued), Name: "commit", Args: json.RawMessage(foreignAccountArgs)}
		r.mu.Unlock()
	}
	r.mu.Lock()
	ag := r.ag
	r.mu.Unlock()
	for _, t := range ag.Tools {
		if t.Spec().Name != "commit" {
			continue
		}
		res, err := t.Call(types.WithToolUse(ctx, call), call.Args)
		r.mu.Lock()
		r.results = append(r.results, res)
		r.mu.Unlock()
		if err != nil {
			var se *types.SuspendError
			if errors.As(err, &se) {
				if calls, ok := se.Payload.([]types.ToolUse); ok && len(calls) > 0 {
					st.Pending = calls
				}
			}
			return st, nil, runtime.Continue, err
		}
	}
	r.mu.Lock()
	done := r.issued >= r.plan
	r.mu.Unlock()
	nst := st
	nst.Pending = nil
	nst.Turn = st.Turn + 1
	nst.HistoryVersion = st.HistoryVersion + 1
	if done {
		return nst, nil, runtime.DoneStatus, nil
	}
	return nst, nil, runtime.Continue, nil
}

func (r *foreignAccountPlanRT) seen() []types.ToolResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]types.ToolResult(nil), r.results...)
}

// foreignAccountCancelRT drives a two-component turn: the first
// component is the named governed call, the second runs only if the
// first settled without error.
type foreignAccountCancelRT struct {
	mu     sync.Mutex
	ag     runtime.AgentRun
	first  string
	second string
}

func (r *foreignAccountCancelRT) Name() string { return "acct.test" }
func (r *foreignAccountCancelRT) Granularity() runtime.StepGranularity {
	return runtime.GranularityTurn
}

func (r *foreignAccountCancelRT) Start(_ context.Context, ag runtime.AgentRun) (runtime.State, error) {
	r.mu.Lock()
	r.ag = ag
	r.mu.Unlock()
	return runtime.State{}, nil
}

func (r *foreignAccountCancelRT) call(ctx context.Context, name, id string) (types.ToolResult, error) {
	r.mu.Lock()
	ag := r.ag
	r.mu.Unlock()
	call := types.ToolUse{ID: id, Name: name, Args: json.RawMessage(foreignAccountArgs)}
	for _, t := range ag.Tools {
		if t.Spec().Name != name {
			continue
		}
		return t.Call(types.WithToolUse(ctx, call), call.Args)
	}
	return types.ToolResult{}, fmt.Errorf("acct.test: tool %q not found", name)
}

func (r *foreignAccountCancelRT) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	_, err := r.call(ctx, r.first, "call-1")
	if err != nil {
		return st, nil, runtime.Continue, err
	}
	_, err2 := r.call(ctx, r.second, "call-2")
	if err2 != nil {
		return st, nil, runtime.Continue, err2
	}
	nst := st
	nst.Turn = st.Turn + 1
	nst.HistoryVersion = st.HistoryVersion + 1
	return nst, nil, runtime.DoneStatus, nil
}

// foreignAccountDecider allows the first allow calls and asks after.
type foreignAccountDecider struct {
	allow int
}

func (d *foreignAccountDecider) Decide(context.Context, *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	if d.allow > 0 {
		d.allow--
		return types.Decision[permission.Verdict]{Value: permission.Allow, Confidence: 1}, nil
	}
	return types.Decision[permission.Verdict]{Value: permission.Ask, Confidence: 1}, nil
}

// foreignAccountAskOnceDecider asks on the first decision and allows after.
type foreignAccountAskOnceDecider struct {
	mu    sync.Mutex
	asked bool
}

func (d *foreignAccountAskOnceDecider) Decide(context.Context, *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.asked {
		d.asked = true
		return types.Decision[permission.Verdict]{Value: permission.Ask, Confidence: 1}, nil
	}
	return types.Decision[permission.Verdict]{Value: permission.Allow, Confidence: 1}, nil
}

// foreignAccountStack carries one foreign flow's fixture state.
type foreignAccountStack struct {
	conv  gohan.Conversation
	stack *gohan.Stack
	runs  *stores.MemoryRuns
	log   stores.SessionLog
}

func foreignAccountLookup(tools []types.Tool) func(string) (types.ToolSpec, bool) {
	return func(name string) (types.ToolSpec, bool) {
		for _, t := range tools {
			if t.Spec().Name == name {
				return t.Spec(), true
			}
		}
		return types.ToolSpec{}, false
	}
}

func foreignAccountBuild(
	t *testing.T,
	jr *stores.MemoryJournal,
	runs *stores.MemoryRuns,
	log stores.SessionLog,
	maxToolCalls int,
	dec types.Decider[*permission.ToolInvocation, permission.Verdict],
	rt runtime.Runtime,
	recovery runtime.Runtime,
	tools []types.Tool,
	extra ...gohan.Option,
) *foreignAccountStack {
	t.Helper()
	lookup := foreignAccountLookup(tools)
	chain := chains.ToolChain{
		{Name: "shield", Kind: chains.KindGuard, Use: std.Shield(lookup)},
		{Name: "journal", Kind: chains.KindJournal, Use: std.Journal(jr, std.WithJournalSpecs(lookup))},
	}
	opts := []gohan.Option{
		gohan.WithModels(govModel{}),
		gohan.WithStores(stores.Stores{
			SessionLog:  log,
			Runs:        runs,
			Checkpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom), stores.WithMemoryCheckpointRuns(runs)),
			Journal:     jr,
			Audit:       stores.NewMemoryAuditLog(),
		}),
		gohan.WithLimits("chat", types.RunLimits{
			MaxTurns: 8, MaxToolCalls: maxToolCalls, MaxWallClock: time.Minute,
			SoftRatio: 0.8, MaxDepth: 4, MaxParallelChildren: 2, MaxParallelTools: 2,
		}),
		gohan.WithApprovalPolicySource(govPolicy{}),
		gohan.WithCredentialSource(govCreds{}),
		gohan.WithForeignAgent(gohan.ForeignSpec{
			Request:    gohan.FlowRequest{Name: "chat"},
			Profile:    "gov-test",
			NewRuntime: func() runtime.Runtime { return rt },
			Tools:      tools,
			ToolChain:  chain,
			Decider:    dec,
		}),
	}
	if recovery != nil {
		opts = append(opts, gohan.WithRecoveryRuntime("chat", recovery))
	}
	stack, err := gohan.Build(append(opts, extra...)...)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := gohan.NewForeignConversation(stack, "chat",
		gohan.WithConversationRuns(runs),
		gohan.WithConversationEventLog(stores.NewMemoryEventLog()),
	)
	if err != nil {
		t.Fatal(err)
	}
	return &foreignAccountStack{conv: conv, stack: stack, runs: runs, log: log}
}

func (f *foreignAccountStack) sendCtx(t *testing.T, ctx context.Context) (types.ResumeToken, error) {
	t.Helper()
	var token types.ResumeToken
	var sendErr error
	for ev, err := range f.conv.Send(ctx, "s1", types.Message{
		Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "go"}},
	}) {
		if s, ok := ev.(types.Suspended); ok {
			token = s.Token
		}
		if err != nil {
			sendErr = err
		}
	}
	return token, sendErr
}

func (f *foreignAccountStack) send(t *testing.T) (types.ResumeToken, error) {
	t.Helper()
	return f.sendCtx(t, types.WithPrincipal(context.Background(), govOwner))
}

func (f *foreignAccountStack) resume(t *testing.T, token types.ResumeToken) error {
	t.Helper()
	ctx := types.WithPrincipal(context.Background(), govOp)
	var resumeErr error
	for _, err := range f.conv.Resume(ctx, token, gohan.Approve()) {
		if err != nil {
			resumeErr = err
		}
	}
	return resumeErr
}

func (f *foreignAccountStack) resumeScope(t *testing.T, token types.ResumeToken, ttl time.Duration) error {
	t.Helper()
	ctx := types.WithPrincipal(context.Background(), govOp)
	var resumeErr error
	for _, err := range f.conv.Resume(ctx, token, gohan.ApproveScope(ttl)) {
		if err != nil {
			resumeErr = err
		}
	}
	return resumeErr
}

// foreignAccountAdmissions decodes the durable admission records the
// session history carries, in append order.
func foreignAccountAdmissions(t *testing.T, log stores.SessionLog, session string) []foreignAccountAdmission {
	t.Helper()
	h, err := log.Load(types.WithPrincipal(context.Background(), govOwner), session)
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	var out []foreignAccountAdmission
	for _, msg := range h.Messages {
		raw, ok := msg.Meta[foreignAdmissionMetaKey]
		if !ok {
			continue
		}
		b, err := json.Marshal(raw)
		if err != nil {
			t.Fatalf("marshal admission record: %v", err)
		}
		var rec foreignAccountAdmission
		if err := json.Unmarshal(b, &rec); err != nil {
			t.Fatalf("unmarshal admission record: %v", err)
		}
		out = append(out, rec)
	}
	return out
}

func foreignAccountEntries(t *testing.T, jr *stores.MemoryJournal, tool string) []stores.Entry {
	t.Helper()
	entries, err := jr.ByFingerprint(context.Background(), "s1", std.CanonicalFingerprint(tool, json.RawMessage(foreignAccountArgs)))
	if err != nil {
		t.Fatalf("ByFingerprint: %v", err)
	}
	return entries
}

// foreignAccountFailed returns the recorded results that failed closed
// with the not_executed prefix, keeping their call ids.
func foreignAccountFailed(seen []types.ToolResult) []types.ToolResult {
	var out []types.ToolResult
	for _, res := range seen {
		if res.Outcome == types.Failed && res.Error != nil &&
			strings.HasPrefix(res.Error.Message, runtime.NotExecutedPrefix) {
			out = append(out, res)
		}
	}
	return out
}

// TestForeignAccountCancellation covers runtime.foreign-cancellation:
// cancellation stops the foreign turn by effect. A committed SideEffect
// call settles and journals before the run stops; an unshielded
// ReadOnly call aborts and no later component of the turn runs.
func TestForeignAccountCancellation(t *testing.T) {
	t.Run("runtime.foreign-cancellation", func(t *testing.T) {
		jr := stores.NewMemoryJournal()
		runs := stores.NewMemoryRuns(stores.WithMemoryRunInfo(types.RunInfoFrom))
		log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		commit := &foreignAccountCommitTool{started: make(chan struct{}), release: make(chan struct{})}
		after := &foreignAccountAfterTool{}
		rt := &foreignAccountCancelRT{first: "commit", second: "after"}
		f := foreignAccountBuild(t, jr, runs, log, 8, govDecider{verdict: permission.Allow}, rt, nil,
			[]types.Tool{commit, after})

		ctx, cancel := context.WithCancel(types.WithPrincipal(context.Background(), govOwner))
		go func() {
			<-commit.started
			cancel()
			close(commit.release)
		}()
		if _, err := f.sendCtx(t, ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Send: %v", err)
		}
		if n := commit.runs(); n != 1 {
			t.Fatalf("committed effect ran %d times, want the shield to settle it exactly once", n)
		}
		entries := foreignAccountEntries(t, jr, "commit")
		if len(entries) != 1 || entries[0].State != stores.Completed || entries[0].Key != "call-1" {
			t.Fatalf("entries = %+v, want one Completed journal entry under call-1 before the run stopped", entries)
		}
		if n := after.runs(); n != 0 {
			t.Fatalf("component after the cancelled call ran %d times, want 0", n)
		}
	})

	t.Run("runtime.foreign-cancellation/read-only-aborts", func(t *testing.T) {
		jr := stores.NewMemoryJournal()
		runs := stores.NewMemoryRuns(stores.WithMemoryRunInfo(types.RunInfoFrom))
		log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		peek := &foreignAccountPeekTool{started: make(chan struct{})}
		after := &foreignAccountAfterTool{}
		rt := &foreignAccountCancelRT{first: "peek", second: "after"}
		f := foreignAccountBuild(t, jr, runs, log, 8, govDecider{verdict: permission.Allow}, rt, nil,
			[]types.Tool{peek, after})

		ctx, cancel := context.WithCancel(types.WithPrincipal(context.Background(), govOwner))
		go func() {
			<-peek.started
			cancel()
		}()
		if _, err := f.sendCtx(t, ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Send: %v", err)
		}
		if err := peek.abort(); !errors.Is(err, context.Canceled) {
			t.Fatalf("read-only call aborted with %v, want context.Canceled", err)
		}
		if entries := foreignAccountEntries(t, jr, "peek"); len(entries) != 0 {
			t.Fatalf("entries = %+v, want none: a ReadOnly call gains no journal requirement", entries)
		}
		if n := after.runs(); n != 0 {
			t.Fatalf("component after the aborted call ran %d times, want 0", n)
		}
	})
}

// TestForeignAccountCompletedCallReplay covers
// runtime.foreign-completed-call-replayed: a foreign run that crashes
// after a SideEffect call completed and journaled recovers by replaying
// the journal entry, never re-executing the tool.
// foreignAccountSwitchRT is the flow's factory runtime: it delegates to
// whichever backend it currently holds, so the recovery drive runs a
// fresh backend the way a restarted process would.
type foreignAccountSwitchRT struct {
	mu  sync.Mutex
	cur runtime.Runtime
}

func (s *foreignAccountSwitchRT) swap(rt runtime.Runtime) {
	s.mu.Lock()
	s.cur = rt
	s.mu.Unlock()
}

func (s *foreignAccountSwitchRT) Name() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur.Name()
}

func (s *foreignAccountSwitchRT) Granularity() runtime.StepGranularity {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur.Granularity()
}

func (s *foreignAccountSwitchRT) Start(ctx context.Context, ag runtime.AgentRun) (runtime.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur.Start(ctx, ag)
}

func (s *foreignAccountSwitchRT) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur.Step(ctx, st)
}

// foreignAccountCrashFixture runs one live governed Send whose SideEffect
// call really executes and journals, then leaves a stale Running run for
// the recovery drive. The recovery runtime the stack registers is fresh,
// so the recovery drive re-issues the same call identity and the journal
// must answer it without execution.
func foreignAccountCrashFixture(t *testing.T, recoverRT runtime.Runtime) (*foreignAccountStack, *foreignAccountSwitchRT, *foreignAccountCommitTool, *stores.MemoryJournal, *stores.MemoryRuns, stores.SessionLog, func()) {
	t.Helper()
	jr := stores.NewMemoryJournal()
	now := time.Unix(0, 0)
	tick := func() time.Time { return now }
	runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(tick), stores.WithMemoryRunInfo(types.RunInfoFrom))
	log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	live := &foreignAccountPlanRT{plan: 1}
	sw := &foreignAccountSwitchRT{cur: live}
	commit := &foreignAccountCommitTool{}
	age := func() { now = now.Add(2 * stores.LeaseTTL) }
	f := foreignAccountBuild(t, jr, runs, log, 8, govDecider{verdict: permission.Allow}, sw, recoverRT,
		[]types.Tool{commit})
	if _, err := f.send(t); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if n := commit.runs(); n != 1 {
		t.Fatalf("live call ran %d times, want 1", n)
	}
	entries := foreignAccountEntries(t, jr, "commit")
	if len(entries) != 1 || entries[0].State != stores.Completed {
		t.Fatalf("entries = %+v, want one Completed entry from the live run", entries)
	}
	h, err := log.Load(types.WithPrincipal(context.Background(), govOwner), "s1")
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if _, err := runs.Start(context.Background(), stores.Run{
		SessionID: "s1", RunID: "r2", Flow: "chat", State: stores.Running, Turn: 1, Seq: h.Version,
	}, stores.LeaseTTL); err != nil {
		t.Fatal(err)
	}
	return f, sw, commit, jr, runs, log, age
}

func TestForeignAccountCompletedCallReplay(t *testing.T) {
	recoverCtx := func() context.Context {
		return types.WithPrincipal(context.Background(), govOwner)
	}

	t.Run("runtime.foreign-completed-call-replayed", func(t *testing.T) {
		recoverRT := &foreignAccountPlanRT{plan: 1}
		f, sw, commit, jr, _, _, age := foreignAccountCrashFixture(t, recoverRT)
		sw.swap(recoverRT)
		age()
		if err := f.stack.Recover(recoverCtx(), 10); err != nil {
			t.Fatalf("Recover: %v", err)
		}
		if n := commit.runs(); n != 1 {
			t.Fatalf("tool ran %d times after recovery, want the journal replay to keep it at 1", n)
		}
		seen := recoverRT.seen()
		if len(seen) != 1 || seen[0].ID != "call-1" || seen[0].Outcome != types.Succeeded {
			t.Fatalf("recovered results = %+v, want the replayed Completed result under call-1", seen)
		}
		entries := foreignAccountEntries(t, jr, "commit")
		if len(entries) != 1 || entries[0].State != stores.Completed {
			t.Fatalf("entries = %+v, want the single Completed entry the recovery replayed", entries)
		}
	})

	t.Run("runtime.foreign-completed-call-replayed/fresh-stack", func(t *testing.T) {
		// The crashing stack registers no recovery runtime; a fresh
		// stack built over the same durable stores recovers the run.
		_, _, commit, jr, runs, log, age := foreignAccountCrashFixture(t, nil)
		recoverRT := &foreignAccountPlanRT{plan: 1}
		fresh := foreignAccountBuild(t, jr, runs, log, 8, govDecider{verdict: permission.Allow}, recoverRT, recoverRT,
			[]types.Tool{commit})
		age()
		if err := fresh.stack.Recover(recoverCtx(), 10); err != nil {
			t.Fatalf("Recover: %v", err)
		}
		if n := commit.runs(); n != 1 {
			t.Fatalf("tool ran %d times after fresh-stack recovery, want 1", n)
		}
		seen := recoverRT.seen()
		if len(seen) != 1 || seen[0].ID != "call-1" || seen[0].Outcome != types.Succeeded {
			t.Fatalf("recovered results = %+v, want the replayed Completed result under call-1", seen)
		}
		entries := foreignAccountEntries(t, jr, "commit")
		if len(entries) != 1 || entries[0].State != stores.Completed {
			t.Fatalf("entries = %+v, want the single Completed entry the recovery replayed", entries)
		}
	})

	t.Run("runtime.foreign-completed-call-replayed/live-lease-wins", func(t *testing.T) {
		recoverRT := &foreignAccountPlanRT{plan: 1}
		f, sw, commit, _, _, _, age := foreignAccountCrashFixture(t, recoverRT)
		sw.swap(recoverRT)
		// The stale run still holds a live lease: recovery must not
		// drive it, exactly as a live pod would.
		if err := f.stack.Recover(recoverCtx(), 10); err != nil {
			t.Fatalf("Recover under a live lease: %v", err)
		}
		if seen := recoverRT.seen(); len(seen) != 0 {
			t.Fatalf("recovery drove %d results under a live lease, want none", len(seen))
		}
		age()
		if err := f.stack.Recover(recoverCtx(), 10); err != nil {
			t.Fatalf("Recover after lease expiry: %v", err)
		}
		if n := commit.runs(); n != 1 {
			t.Fatalf("tool ran %d times, want the expired-lease recovery to replay without execution", n)
		}
		seen := recoverRT.seen()
		if len(seen) != 1 || seen[0].ID != "call-1" || seen[0].Outcome != types.Succeeded {
			t.Fatalf("recovered results = %+v, want the replayed Completed result under call-1", seen)
		}
	})
}

// TestForeignAccountAdmissionBudget covers the numeric admission
// accounting: a prepaid call replays without a second charge, a fresh
// identity past MaxToolCalls fails closed without a durable admission,
// and repeated resumes never inflate the admitted total.
func TestForeignAccountAdmissionBudget(t *testing.T) {
	build := func(t *testing.T, maxTools int, plan int, dec types.Decider[*permission.ToolInvocation, permission.Verdict]) (*foreignAccountStack, *foreignAccountPlanRT, *foreignAccountCommitTool, stores.SessionLog) {
		t.Helper()
		jr := stores.NewMemoryJournal()
		runs := stores.NewMemoryRuns(stores.WithMemoryRunInfo(types.RunInfoFrom))
		log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		rt := &foreignAccountPlanRT{plan: plan}
		commit := &foreignAccountCommitTool{}
		f := foreignAccountBuild(t, jr, runs, log, maxTools, dec, rt, nil, []types.Tool{commit})
		return f, rt, commit, log
	}
	wantAdmissions := func(t *testing.T, got []foreignAccountAdmission, ids ...string) {
		t.Helper()
		if len(got) != len(ids) {
			t.Fatalf("admissions = %+v, want %d records", got, len(ids))
		}
		for i, id := range ids {
			if got[i].CallID != id || got[i].AdmittedTotal != i+1 {
				t.Fatalf("admission[%d] = %+v, want call %s with admitted_total %d", i, got[i], id, i+1)
			}
		}
	}

	t.Run("max-tool-calls-1-prepaid-once-then-refused", func(t *testing.T) {
		f, rt, commit, log := build(t, 1, 2, nil)
		token, err := f.send(t)
		if err != nil || token == "" {
			t.Fatalf("Send: token=%q err=%v, want the ask suspension", token, err)
		}
		if n := commit.runs(); n != 0 {
			t.Fatalf("tool ran %d times before approval, want 0", n)
		}
		if err := f.resume(t, token); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if n := commit.runs(); n != 1 {
			t.Fatalf("tool ran %d times, want the prepaid approved call exactly once", n)
		}
		failed := foreignAccountFailed(rt.seen())
		if len(failed) != 1 || failed[0].ID != "call-2" {
			t.Fatalf("failed results = %+v, want exactly call-2 refused over the limit", failed)
		}
		wantAdmissions(t, foreignAccountAdmissions(t, log, "s1"), "call-1")
	})

	t.Run("max-tool-calls-2-completed-ask-approve-third-refused", func(t *testing.T) {
		f, rt, commit, log := build(t, 2, 3, &foreignAccountDecider{allow: 1})
		token, err := f.send(t)
		if err != nil || token == "" {
			t.Fatalf("Send: token=%q err=%v, want the completed call then the ask suspension", token, err)
		}
		if n := commit.runs(); n != 1 {
			t.Fatalf("tool ran %d times after Send, want the allowed call-1 only", n)
		}
		if err := f.resume(t, token); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if n := commit.runs(); n != 2 {
			t.Fatalf("tool ran %d times after approval, want the prepaid call-2 to complete the budget", n)
		}
		failed := foreignAccountFailed(rt.seen())
		if len(failed) != 1 || failed[0].ID != "call-3" {
			t.Fatalf("failed results = %+v, want exactly call-3 refused over the limit", failed)
		}
		wantAdmissions(t, foreignAccountAdmissions(t, log, "s1"), "call-1", "call-2")
	})

	t.Run("repeated-resume-keeps-admitted-total", func(t *testing.T) {
		f, _, commit, log := build(t, 2, 2, &foreignAccountAskOnceDecider{})
		token, err := f.send(t)
		if err != nil || token == "" {
			t.Fatalf("Send: token=%q err=%v, want the ask suspension", token, err)
		}
		if err := f.resumeScope(t, token, -time.Minute); err == nil {
			t.Fatal("negative scope TTL accepted, want the refused resume to leave the token pending")
		}
		// The refused resume changed no accounting: the one approval
		// that follows settles the parked prepaid call and drives the
		// turn to its budget without re-admitting either identity.
		if err := f.resume(t, token); err != nil {
			t.Fatalf("Resume after refused scope: %v", err)
		}
		if n := commit.runs(); n != 2 {
			t.Fatalf("tool ran %d times, want the prepaid call and the approved follow-up exactly once each", n)
		}
		wantAdmissions(t, foreignAccountAdmissions(t, log, "s1"), "call-1", "call-2")
	})
}
