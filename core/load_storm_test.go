package gohan

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/loadtest"
)

// stormModel is run-aware: while the run's history holds a tool use with
// no matching result it re-emits exactly that call, so a recovered drive
// presents the same call key to the journal; otherwise it issues the
// session's next call and finally a plain text turn. The first Generate
// call arms the outage schedule, which runs before any event relay can
// consume a faulted operation.
type stormModel struct {
	faults *loadtest.Faults
	arm    sync.Once

	mu     sync.Mutex
	calls  map[string]int
	runIDs map[string]string
	plain  map[string]bool
}

func newStormModel(faults *loadtest.Faults) *stormModel {
	return &stormModel{
		faults: faults,
		calls:  map[string]int{},
		runIDs: map[string]string{},
		plain:  map[string]bool{},
	}
}

func (m *stormModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "native", Caps: types.Caps{Tools: true}}
}

func (m *stormModel) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		m.arm.Do(func() { m.faults.FailNext(loadtest.PortEventLog, stormK) })
		info, _ := types.RunInfoFrom(ctx)
		m.mu.Lock()
		m.calls[info.RunID]++
		if info.SessionID != "" {
			m.runIDs[info.SessionID] = info.RunID
		}
		m.mu.Unlock()

		var uses []types.ToolUse
		answered := map[string]bool{}
		for _, msg := range req.Messages {
			for _, b := range msg.Blocks {
				switch v := b.(type) {
				case types.ToolUse:
					uses = append(uses, v)
				case types.ToolResult:
					answered[v.ID] = true
				}
			}
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if u, ok := m.unresolved(uses, answered); ok {
			yield(types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: &u}, nil)
			yield(types.ModelChunk{Finish: types.FinishToolUse}, nil)
			return
		}
		if len(uses) > 0 {
			// The session's call was already answered: the drive is done.
			yield(types.ModelChunk{Kind: types.DeltaText, Delta: "done"}, nil)
			yield(types.ModelChunk{Finish: types.FinishStop}, nil)
			return
		}
		if m.plain[info.SessionID] {
			yield(types.ModelChunk{Kind: types.DeltaText, Delta: "done"}, nil)
			yield(types.ModelChunk{Finish: types.FinishStop}, nil)
			return
		}
		call := types.ToolUse{
			ID:   fmt.Sprintf("call-%s-0", info.SessionID),
			Name: "note",
			Args: jsontext.Value(fmt.Sprintf(`{"id":"%s-0"}`, info.SessionID)),
		}
		yield(types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: &call}, nil)
		yield(types.ModelChunk{Finish: types.FinishToolUse}, nil)
	}
}

// unresolved returns the history's tool call that still lacks its result,
// so a recovered drive presents the same call key to the journal.
func (m *stormModel) unresolved(uses []types.ToolUse, answered map[string]bool) (types.ToolUse, bool) {
	for _, u := range uses {
		if !answered[u.ID] {
			return u, true
		}
	}
	return types.ToolUse{}, false
}

func (m *stormModel) callsByRun(runID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[runID]
}

func (m *stormModel) runIDForSession(session string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runIDs[session]
}

// stormTool records every execution as a (session, call id, args) triple.
type stormTool struct {
	mu    sync.Mutex
	known map[string]stormExec
}

type stormExec struct {
	Session string
	ID      string
	Args    string
}

func newStormTool() *stormTool {
	return &stormTool{known: map[string]stormExec{}}
}

func (t *stormTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "note", Effect: types.SideEffect}
}

func (t *stormTool) Call(ctx context.Context, args jsontext.Value) (types.ToolResult, error) {
	var in struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(args, &in)
	session := ""
	if info, ok := types.RunInfoFrom(ctx); ok {
		session = info.SessionID
	}
	t.mu.Lock()
	t.known[session+"/"+in.ID] = stormExec{Session: session, ID: in.ID, Args: string(args)}
	t.mu.Unlock()
	return types.ToolResult{
		ID:      in.ID,
		Content: []types.Block{types.Text{Text: "noted " + in.ID}},
		Outcome: types.Succeeded,
	}, nil
}

func (t *stormTool) execs() []stormExec {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]stormExec, 0, len(t.known))
	for _, e := range t.known {
		out = append(out, e)
	}
	return out
}

// stormJournalMW journals every side-effect call through the fixture
// journal: Reserve mints or replays the intent, the effect runs under the
// reserved key, and the settled result completes the entry. The fingerprint
// matches ToolFingerprint so the committed assertions find the entries.
func stormJournalMW(j stores.Journal, lookup func(string) (types.ToolSpec, bool)) chains.ToolMiddleware {
	return func(next types.ToolFunc) types.ToolFunc {
		return func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
			spec, ok := lookup(call.Name)
			if !ok {
				spec = types.ToolSpec{Name: call.Name}
			}
			if spec.Effect == types.ReadOnly {
				return next(ctx, call)
			}
			info, _ := types.RunInfoFrom(ctx)
			args := json.RawMessage(call.Args)
			var id struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(args, &id); err != nil {
				return types.ToolResult{}, fmt.Errorf("journal intent %s: %w", call.Name, err)
			}
			key := types.CallKey{SessionID: info.SessionID, CallID: id.ID}
			fp := ToolFingerprint(spec, args)
			entry, created, err := j.Reserve(ctx, key, call.Name, fp, args)
			if err != nil {
				return types.ToolResult{}, fmt.Errorf("reserve %s: %w", call.Name, err)
			}
			key.CallID = entry.Key
			if !created && entry.State == stores.Completed && entry.Result.Outcome != types.Unknown {
				res := entry.Result
				res.ID = call.ID
				return res, nil
			}
			res, err := next(ctx, call)
			if err != nil {
				return res, err
			}
			res.ID = entry.Key
			if err := j.Complete(ctx, key, res); err != nil {
				res.Outcome = types.Unknown
			}
			return res, nil
		}
	}
}

type stormClock struct {
	mu  sync.Mutex
	now time.Time
}

func newStormClock() *stormClock {
	return &stormClock{now: time.Unix(0, 0)}
}

func (c *stormClock) tick() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *stormClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

const stormK = 6

// TestLoadDisconnectStorm drives six concurrent runs through a mid-run
// event-record outage, then reaps the stale runs and proves the storm and
// the recovery together executed every call exactly once.
func TestLoadDisconnectStorm(t *testing.T) {
	t.Run("performance.disconnect-storm-exactly-once", func(t *testing.T) {
		clock := newStormClock()
		faults := loadtest.NewFaults()
		model := newStormModel(faults)
		tool := newStormTool()

		runs := stores.NewMemoryRuns(
			stores.WithMemoryRunClock(clock.tick),
			stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
				p, ok := types.PrincipalFrom(ctx)
				return types.RunInfo{Principal: p}, ok
			}),
		)
		cp := stores.NewMemoryCheckpoints(
			stores.WithMemoryCheckpointClock(clock.tick),
			stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom),
			stores.WithMemoryCheckpointRuns(runs),
		)
		journal := stores.NewMemoryJournal(stores.WithMemoryJournalClock(clock.tick))
		base := stores.Stores{
			SessionLog:  stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
			Runs:        runs,
			Checkpoints: cp,
			Journal:     journal,
			EventLog:    stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now)),
		}
		jmw := stormJournalMW(journal, func(name string) (types.ToolSpec, bool) {
			if name == tool.Spec().Name {
				return tool.Spec(), true
			}
			return types.ToolSpec{}, false
		})
		jchain := chains.ToolChain{{Name: "journal", Kind: chains.KindJournal, Use: jmw}}
		stack, err := Build(
			WithStores(loadtest.Wrap(base, faults, loadtest.PortEventLog)),
			WithModels(model),
			WithNativeAgent(NativeSpec{
				Request:   FlowRequest{Name: "load"},
				Profile:   "native",
				Tools:     []types.Tool{tool},
				ToolChain: jchain,
				Decider:   &allowDecider{},
				Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
					return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
				},
			}),
			WithRecoveryRuntime("load", runtime.NewNative()),
			WithApprovalPolicySource(nrrAllowPolicy{}),
		)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		evlog := stack.stores.EventLog

		runSession := func(i int) string { return fmt.Sprintf("s%d", i) }
		results := loadtest.Run(context.Background(), stormK, func(ctx context.Context, i int) error {
			conv, err := NewNativeConversation(stack, "load",
				WithConversationRuns(runs),
				WithConversationEventLog(evlog),
				WithConversationApprovalPolicy(nrrAllowPolicy{}),
			)
			if err != nil {
				return err
			}
			sctx := WithIdempotencyKey(
				WithPrincipal(ctx, types.Principal{Subject: "load", Tenant: "t1"}),
				fmt.Sprintf("op-%d", i),
			)
			var last error
			for _, err := range conv.Send(sctx, runSession(i), stormUser("go")) {
				if err != nil {
					last = err
				}
			}
			return last
		})

		failed := 0
		for _, r := range results {
			if r.Err != nil && errors.Is(r.Err, loadtest.ErrUnavailable) {
				failed++
			}
		}
		if failed < 1 {
			t.Fatalf("runs failed=%d evFail=%d evOps=%d results=%v", failed, faults.Failures(loadtest.PortEventLog), faults.Operations(loadtest.PortEventLog), results)
		}
		if got := faults.Failures(loadtest.PortEventLog); got < 1 {
			t.Fatalf("event log failures = %d, want at least 1", got)
		}
		if faults.MethodFailures(loadtest.PortEventLog, "Append") < 1 {
			t.Fatalf("append failures = %d, want at least 1 (operations=%d)", faults.MethodFailures(loadtest.PortEventLog, "Append"), faults.Operations(loadtest.PortEventLog))
		}
		retry := func(i int) error {
			conv, err := NewNativeConversation(stack, "load",
				WithConversationRuns(runs),
				WithConversationEventLog(evlog),
				WithConversationApprovalPolicy(nrrAllowPolicy{}),
			)
			if err != nil {
				return err
			}
			sctx := WithIdempotencyKey(
				WithPrincipal(context.Background(), types.Principal{Subject: "load", Tenant: "t1"}),
				fmt.Sprintf("op-%d", i),
			)
			var last error
			for _, err := range conv.Send(sctx, runSession(i), stormUser("go")) {
				if err != nil {
					last = err
				}
			}
			return last
		}
		for i := 1; i < stormK; i += 2 {
			faults.FailNext(loadtest.PortEventLog, 1)
			_ = retry(i)
		}
		if faults.MethodFailures(loadtest.PortEventLog, "Read") < 1 {
			t.Fatalf("read failures = %d, want at least 1 (operations=%d)", faults.MethodFailures(loadtest.PortEventLog, "Read"), faults.Operations(loadtest.PortEventLog))
		}
		faults.Clear(loadtest.PortEventLog)

		clock.advance(2 * stores.LeaseTTL)
		rctx := WithPrincipal(context.Background(), types.Principal{Subject: "load", Tenant: "t1"})
		if err := stack.Recover(rctx, 64); err != nil {
			t.Fatalf("recover: %v", err)
		}

		var runStore stores.Runs = runs
		finder, ok := runStore.(runFinder)
		if !ok {
			t.Fatal("runs store has no by-id reader")
		}
		for i := range stormK {
			session := runSession(i)
			runID := model.runIDForSession(session)
			if runID == "" {
				t.Fatalf("session %s never recorded a run id", session)
			}
			row, err := finder.ByID(rctx, runID)
			if err != nil {
				t.Fatalf("run by id %s: %v", runID, err)
			}
			if row.State != stores.Finished {
				t.Fatalf("run %s row = %+v, want Finished", runID, row)
			}
			if done := stormDoneCount(t, evlog, rctx, runID); done != 1 {
				t.Fatalf("run %s recorded %d Done events, want exactly 1", runID, done)
			}
			hist, err := base.SessionLog.Load(rctx, session)
			if err != nil {
				t.Fatalf("load history %s: %v", session, err)
			}
			resultsInHistory := 0
			for _, msg := range hist.Messages {
				for _, b := range msg.Blocks {
					if _, ok := b.(types.ToolResult); ok {
						resultsInHistory++
					}
				}
			}
			// A recovered run presents the history's unresolved call
			// once before its result turn, so the bound is results + 2.
			if calls := model.callsByRun(runID); calls > resultsInHistory+2 {
				t.Fatalf("run %s model calls = %d, want at most %d", runID, calls, resultsInHistory+2)
			}
		}

		execs := tool.execs()
		if len(execs) != stormK {
			t.Fatalf("tool executions = %d, want %d", len(execs), stormK)
		}
		for _, e := range execs {
			if e.Session == "" || e.ID == "" {
				t.Fatalf("execution without identity: %+v", e)
			}
			fp := ToolFingerprint(types.ToolSpec{Name: "note", Effect: types.SideEffect}, json.RawMessage(e.Args))
			entries, err := journal.ByFingerprint(rctx, e.Session, fp)
			if err != nil {
				t.Fatalf("journal by fingerprint %s/%s: %v", e.Session, e.ID, err)
			}
			if len(entries) != 1 {
				unk, _ := journal.Unknown(rctx, e.Session)
				t.Fatalf("journal entries for %s/%s = %d (fp %q), want exactly 1; unknown=%+v", e.Session, e.ID, len(entries), fp, unk)
			}
			if entries[0].State != stores.Completed {
				t.Fatalf("journal entry %s/%s state = %v, want Completed", e.Session, e.ID, entries[0].State)
			}
			if entries[0].Fingerprint != fp {
				t.Fatalf("journal entry %s/%s fingerprint = %q, want %q", e.Session, e.ID, entries[0].Fingerprint, fp)
			}
			if entries[0].Key != e.ID {
				t.Fatalf("journal entry %s key = %q, want %q", e.Session, entries[0].Key, e.ID)
			}
		}

		dup := stores.Run{SessionID: "dup", RunID: "dup-run", Flow: "load", OperationID: "op-0"}
		dctx := WithPrincipal(context.Background(), types.Principal{Subject: "dup", Tenant: "t1"})
		modelCallsBeforeDup := stormTotalModelCalls(model)
		toolExecsBeforeDup := len(tool.execs())
		if _, err := runs.Start(dctx, dup, stores.LeaseTTL); !errors.Is(err, types.ErrOperationExists) {
			t.Fatalf("duplicate operation start err = %v, want ErrOperationExists", err)
		}
		if got := stormTotalModelCalls(model); got != modelCallsBeforeDup {
			t.Fatalf("model calls after duplicate start = %d, want %d", got, modelCallsBeforeDup)
		}
		if got := len(tool.execs()); got != toolExecsBeforeDup {
			t.Fatalf("tool executions after duplicate start = %d, want %d", got, toolExecsBeforeDup)
		}
	})

	t.Run("performance.disconnect-storm-recovery", func(t *testing.T) {
		clock := newStormClock()
		faults := loadtest.NewFaults()
		model := newStormModel(faults)
		tool := newStormTool()

		runs := stores.NewMemoryRuns(
			stores.WithMemoryRunClock(clock.tick),
			stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
				p, ok := types.PrincipalFrom(ctx)
				return types.RunInfo{Principal: p}, ok
			}),
		)
		fence := newStormFenceRuns(runs)
		probe := newStormCpProbe(stores.NewMemoryCheckpoints(
			stores.WithMemoryCheckpointClock(clock.tick),
			stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom),
			stores.WithMemoryCheckpointRuns(fence),
		))
		journal := stores.NewMemoryJournal(stores.WithMemoryJournalClock(clock.tick))
		base := stores.Stores{
			SessionLog:  stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
			Runs:        fence,
			Checkpoints: probe,
			Journal:     journal,
			EventLog:    stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now)),
		}
		jmw := stormJournalMW(journal, func(name string) (types.ToolSpec, bool) {
			if name == tool.Spec().Name {
				return tool.Spec(), true
			}
			return types.ToolSpec{}, false
		})
		jchain := chains.ToolChain{{Name: "journal", Kind: chains.KindJournal, Use: jmw}}
		stack, err := Build(
			WithStores(loadtest.Wrap(base, faults, loadtest.PortEventLog)),
			WithModels(model),
			WithNativeAgent(NativeSpec{
				Request: FlowRequest{Name: "load"},
				Profile: "native",
				Tools:   []types.Tool{tool},
				// No Decider: the side-effect gate asks, and the ask
				// suspends the run with a resume token.
				ToolChain: jchain,
				Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
					return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
				},
			}),
			WithRecoveryRuntime("load", runtime.NewNative()),
			WithApprovalPolicySource(nrrAllowPolicy{}),
		)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		evlog := stack.stores.EventLog
		owner := types.Principal{Subject: "load", Tenant: "t1"}

		newConv := func() (Conversation, error) {
			return NewNativeConversation(stack, "load",
				WithConversationRuns(fence),
				WithConversationEventLog(evlog),
				WithConversationApprovalPolicy(nrrAllowPolicy{}),
			)
		}
		suspend := func(session string) (types.ResumeToken, error) {
			conv, err := newConv()
			if err != nil {
				return "", err
			}
			sctx := WithIdempotencyKey(
				WithPrincipal(context.Background(), owner),
				"op-"+session,
			)
			var tok types.ResumeToken
			var last error
			for ev, err := range conv.Send(sctx, session, stormUser("go")) {
				if err != nil {
					last = err
					continue
				}
				if s, ok := ev.(types.Suspended); ok {
					tok = s.Token
				}
			}
			return tok, last
		}
		row := func(t *testing.T, runID string) stores.Run {
			t.Helper()
			got, err := fence.ByID(context.Background(), runID)
			if err != nil {
				t.Fatalf("run by id %s: %v", runID, err)
			}
			return got
		}

		// A run interrupted by the event-record outage mid-storm: the
		// drive dies with a live-then-abandoned lease and no suspension.
		model.plain["s-int"] = true
		if _, err := suspend("s-int"); !errors.Is(err, loadtest.ErrUnavailable) {
			t.Fatalf("interrupted send err = %v, want ErrUnavailable", err)
		}
		faults.Clear(loadtest.PortEventLog)
		if got := row(t, model.runIDForSession("s-int")).State; got != stores.Running {
			t.Fatalf("interrupted run state = %v, want Running", got)
		}

		tok0, err := suspend("s0")
		if err != nil || tok0 == "" {
			t.Fatalf("s0 suspend: token=%q err=%v", tok0, err)
		}
		run0 := model.runIDForSession("s0")
		if got := row(t, run0).State; got != stores.Suspended {
			t.Fatalf("s0 run state = %v, want Suspended", got)
		}
		tok1, err := suspend("s1")
		if err != nil || tok1 == "" {
			t.Fatalf("s1 suspend: token=%q err=%v", tok1, err)
		}
		run1 := model.runIDForSession("s1")

		// Consume the decision, then kill the lease: the resume fails at
		// the token-fenced acquisition and leaves the run suspended with
		// a consumed token — the crash interval recovery completes.
		conv0, err := newConv()
		if err != nil {
			t.Fatalf("conversation: %v", err)
		}
		var rerr error
		for _, err := range conv0.Resume(
			WithPrincipal(context.Background(), owner), tok0, Approve(),
		) {
			if err != nil {
				rerr = err
			}
		}
		if !errors.Is(rerr, loadtest.ErrUnavailable) {
			t.Fatalf("resume err = %v, want ErrUnavailable", rerr)
		}
		if got := fence.fenceCalls(); got != 1 || got != fence.fenceFailures() {
			t.Fatalf("fence calls=%d failures=%d, want the single failed resume acquisition", got, fence.fenceFailures())
		}
		if got := row(t, run0).State; got != stores.Suspended {
			t.Fatalf("s0 run state after failed resume = %v, want Suspended", got)
		}
		if got, err := fence.SuspensionToken(context.Background(), run0); err != nil || got != tok0 {
			t.Fatalf("s0 suspension token = %q (%v), want the consumed token", got, err)
		}

		clock.advance(2 * stores.LeaseTTL)
		rctx := WithPrincipal(context.Background(), owner)
		if err := stack.Recover(rctx, 64); err != nil {
			t.Fatalf("recover: %v", err)
		}

		// Recovery discovery: ResumeReady listed exactly the consumed run
		// and the second fenced acquisition — the first failed in the
		// client resume — claimed it exactly once.
		if ready := probe.readyCalls(); len(ready) != 1 || len(ready[0]) != 1 || ready[0][0] != run0 {
			t.Fatalf("ResumeReady calls = %+v, want one listing exactly %s", ready, run0)
		}
		if got := fence.fenceCalls(); got != 2 || fence.fenceFailures() != 1 {
			t.Fatalf("fence calls=%d failures=%d, want one failed resume plus one recovery acquisition", got, fence.fenceFailures())
		}
		if got := probe.pendingCalls(run0); got != 1 {
			t.Fatalf("PendingInput calls for %s = %d, want exactly 1", run0, got)
		}
		if got := row(t, run0).State; got != stores.Finished {
			t.Fatalf("s0 run state = %+v, want Finished", row(t, run0))
		}
		if done := stormDoneCount(t, evlog, rctx, run0); done != 1 {
			t.Fatalf("run %s recorded %d Done events, want exactly 1", run0, done)
		}
		execsFor := func(session string) int {
			n := 0
			for _, e := range tool.execs() {
				if e.Session == session {
					n++
				}
			}
			return n
		}
		if got := execsFor("s0"); got != 1 {
			t.Fatalf("s0 tool executions = %d, want exactly 1", got)
		}
		if got := execsFor("s1"); got != 0 {
			t.Fatalf("s1 tool executions = %d, want 0", got)
		}
		if got := execsFor("s-int"); got != 0 {
			t.Fatalf("s-int tool executions = %d, want 0", got)
		}

		// The untouched suspension keeps its parked row and never shows up
		// in a reclaim; the interrupted run is reclaimed exactly once and
		// finishes.
		if got := row(t, run1).State; got != stores.Suspended {
			t.Fatalf("s1 run state = %v, want Suspended", got)
		}
		reclaims := fence.reclaimCounts()
		runInt := model.runIDForSession("s-int")
		if got := reclaims[runInt]; got != 1 {
			t.Fatalf("interrupted run reclaims = %d, want exactly 1", got)
		}
		if got := row(t, runInt).State; got != stores.Finished {
			t.Fatalf("interrupted run state = %v, want Finished", got)
		}
		for id, n := range reclaims {
			if id == runInt {
				continue
			}
			t.Fatalf("run %s reclaimed %d times, want no reclaim outside the interrupted run", id, n)
		}
	})
}

func stormUser(text string) types.Message {
	return types.Message{ID: "storm-in", Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: text}}}
}

func stormTotalModelCalls(m *stormModel) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, c := range m.calls {
		n += c
	}
	return n
}

func stormDoneCount(t *testing.T, log stores.EventLog, ctx context.Context, runID string) int {
	t.Helper()
	n := 0
	for ev, err := range log.Read(ctx, runID, 0) {
		if err != nil {
			t.Fatalf("read events %s: %v", runID, err)
		}
		if _, ok := ev.Payload.(types.Done); ok {
			n++
		}
	}
	return n
}

// stormFenceRuns fails the first token-fenced resume lease with an outage
// and forwards every later call, recording reclaims as recovery evidence.
type stormFenceRuns struct {
	stores.Runs

	mu         sync.Mutex
	fences     int
	fenceFails int
	reclaims   map[string]int
}

func newStormFenceRuns(r stores.Runs) *stormFenceRuns {
	return &stormFenceRuns{Runs: r, reclaims: map[string]int{}}
}

func (r *stormFenceRuns) SuspensionToken(ctx context.Context, runID string) (types.ResumeToken, error) {
	return r.Runs.(stores.CheckpointRuns).SuspensionToken(ctx, runID)
}

func (r *stormFenceRuns) ResumingCheckpoint(ctx context.Context, runID string, t types.ResumeToken, ttl time.Duration) (stores.Lease, error) {
	r.mu.Lock()
	r.fences++
	fail := r.fences == 1
	r.mu.Unlock()
	if fail {
		r.mu.Lock()
		r.fenceFails++
		r.mu.Unlock()
		return stores.Lease{}, loadtest.ErrUnavailable
	}
	return r.Runs.(stores.CheckpointRuns).ResumingCheckpoint(ctx, runID, t, ttl)
}

func (r *stormFenceRuns) Reclaim(ctx context.Context, run stores.Run, ttl time.Duration) (stores.Lease, error) {
	r.mu.Lock()
	r.reclaims[run.RunID]++
	r.mu.Unlock()
	return r.Runs.Reclaim(ctx, run, ttl)
}

func (r *stormFenceRuns) ByID(ctx context.Context, runID string) (stores.Run, error) {
	return r.Runs.(interface {
		ByID(ctx context.Context, runID string) (stores.Run, error)
	}).ByID(ctx, runID)
}

func (r *stormFenceRuns) fenceCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fences
}

func (r *stormFenceRuns) fenceFailures() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fenceFails
}

func (r *stormFenceRuns) reclaimCounts() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int, len(r.reclaims))
	for id, n := range r.reclaims {
		out[id] = n
	}
	return out
}

// stormCpProbe records the recovery-discovery calls the checkpoint store
// served: which runs ResumeReady listed and how often PendingInput read a
// run's recorded decision.
type stormCpProbe struct {
	stores.Checkpoints

	mu    sync.Mutex
	ready [][]string
	pend  map[string]int
}

func newStormCpProbe(cps stores.Checkpoints) *stormCpProbe {
	return &stormCpProbe{Checkpoints: cps, pend: map[string]int{}}
}

func (p *stormCpProbe) ResumeReady(ctx context.Context, limit int) ([]stores.Checkpoint, error) {
	out, err := p.Checkpoints.(stores.ResumeReadyLister).ResumeReady(ctx, limit)
	if err != nil {
		return out, err
	}
	ids := make([]string, 0, len(out))
	for _, cp := range out {
		ids = append(ids, cp.RunID)
	}
	p.mu.Lock()
	p.ready = append(p.ready, ids)
	p.mu.Unlock()
	return out, nil
}

func (p *stormCpProbe) PendingInput(ctx context.Context, runID string) (stores.Checkpoint, stores.ResumeInput, error) {
	p.mu.Lock()
	p.pend[runID]++
	p.mu.Unlock()
	return p.Checkpoints.PendingInput(ctx, runID)
}

func (p *stormCpProbe) Peek(ctx context.Context, t types.ResumeToken) (stores.Checkpoint, error) {
	return p.Checkpoints.(stores.CheckpointResumer).Peek(ctx, t)
}

func (p *stormCpProbe) ConsumeIf(ctx context.Context, t types.ResumeToken, expected stores.Checkpoint, in stores.ResumeInput) (stores.Checkpoint, error) {
	return p.Checkpoints.(stores.CheckpointResumer).ConsumeIf(ctx, t, expected, in)
}

func (p *stormCpProbe) UpdatePending(ctx context.Context, t types.ResumeToken, expected, next stores.Checkpoint) error {
	return p.Checkpoints.(stores.CheckpointResumer).UpdatePending(ctx, t, expected, next)
}

func (p *stormCpProbe) readyCalls() [][]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]string(nil), p.ready...)
}

func (p *stormCpProbe) pendingCalls(runID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pend[runID]
}
