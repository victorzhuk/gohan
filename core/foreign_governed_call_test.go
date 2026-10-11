package gohan_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
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
	stdperm "github.com/victorzhuk/gohan/std/permission"
)

var (
	govOwner = types.Principal{Tenant: "acme", Subject: "alice"}
	govOp    = types.Principal{
		Tenant: "acme", Subject: "ops",
		Scopes: []string{types.ScopeSessionRead, types.ScopeSessionWrite, "approve:tool"},
	}
)

// govTool is the flow's side-effect tool: it counts the executions the
// governed path lets through. A non-empty required makes the gate hard
// deny every call whose run principal lacks those scopes.
type govTool struct {
	mu       sync.Mutex
	calls    int
	required []string
}

func (t *govTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "confirm", Risk: types.RiskHigh, Effect: types.SideEffect, RequiredScopes: t.required}
}

func (t *govTool) Call(context.Context, json.RawMessage) (types.ToolResult, error) {
	t.mu.Lock()
	t.calls++
	t.mu.Unlock()
	return types.ToolResult{Outcome: types.Succeeded}, nil
}

func (t *govTool) ran() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.calls
}

// govDecider decides every call the same way.
type govDecider struct{ verdict permission.Verdict }

func (d govDecider) Decide(context.Context, *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	return types.Decision[permission.Verdict]{Value: d.verdict, Confidence: 1}, nil
}

// govPolicy approves every approver.
type govPolicy struct{}

func (govPolicy) ApprovalPolicy(context.Context, types.RiskTier, string, bool) (permission.ApprovalPolicy, error) {
	return permission.ApprovalPolicy{}, nil
}

// govCreds re-issues the transport credential on every resume.
type govCreds struct{}

func (govCreds) Credentials(context.Context, types.Principal) (types.Credential, error) {
	return types.Credential{Token: "fresh"}, nil
}

// govRT drives the flow's one tool call per run and records the results
// the governed path hands back. A SuspendError from the governed path
// crosses as the step error, the way the shipped adapter parks it.
type govRT struct {
	mu      sync.Mutex
	ag      runtime.AgentRun
	args    json.RawMessage
	results []types.ToolResult
	drives  int
}

func (r *govRT) Name() string                         { return "gov.test" }
func (r *govRT) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

func (r *govRT) Start(_ context.Context, ag runtime.AgentRun) (runtime.State, error) {
	r.mu.Lock()
	r.ag = ag
	r.mu.Unlock()
	return runtime.State{}, nil
}

// Step drives one turn. A resumed state carries the parked call the
// suspension recorded, and the backend replays that call under its own
// recorded id and arguments before it generates anything new: an approved
// call never reissues its identity, and the grant the approver holds still
// matches the parked fingerprint. A state with no pending call is a fresh
// drive and takes a new call id over the arguments the stack holds.
func (r *govRT) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	r.mu.Lock()
	ag := r.ag
	args := r.args
	var call types.ToolUse
	if len(st.Pending) > 0 {
		call = st.Pending[0]
		args = call.Args
	} else {
		r.drives++
		call = types.ToolUse{ID: fmt.Sprintf("call-%d", r.drives), Name: "confirm", Args: args}
	}
	r.mu.Unlock()
	for _, t := range ag.Tools {
		if t.Spec().Name != "confirm" {
			continue
		}
		res, err := t.Call(types.WithToolUse(ctx, call), args)
		r.mu.Lock()
		r.results = append(r.results, res)
		r.mu.Unlock()
		if err != nil {
			var se *types.SuspendError
			if errors.As(err, &se) {
				// The shipped adapter records the parked calls in the
				// state's pending queue before it parks.
				if calls, ok := se.Payload.([]types.ToolUse); ok && len(calls) > 0 {
					st.Pending = calls
				}
			}
			return st, nil, runtime.Continue, err
		}
	}
	// The step advances the supplied state instead of returning a literal,
	// so typed metadata the decorator carried in survives the boundary.
	nst := st
	nst.Turn = st.Turn + 1
	nst.HistoryVersion = st.HistoryVersion + 1
	return nst, nil, runtime.DoneStatus, nil
}

func (r *govRT) seen() []types.ToolResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]types.ToolResult(nil), r.results...)
}

// govCalls returns the results the governed call produced, keeping only
// the ones carrying a call id: the suspension crosses the runtime
// boundary with an empty id and is not a tool result.
func (r *govRT) govCalls() []types.ToolResult {
	var out []types.ToolResult
	for _, res := range r.seen() {
		if res.ID != "" {
			out = append(out, res)
		}
	}
	return out
}

// govStack carries the fixtures one governed foreign flow shares across
// its sends, resumes and recoveries.
type govStack struct {
	conv   gohan.Conversation
	tool   *govTool
	rt     *govRT
	jr     *stores.MemoryJournal
	runs   *stores.MemoryRuns
	cps    *stores.MemoryCheckpoints
	stack  *gohan.Stack
	lookup func(string) (types.ToolSpec, bool)
	grants *stdperm.MemoryGrantStore
	audit  stores.AuditLog
	now    func() time.Time
}

func newGovStack(t *testing.T, jr *stores.MemoryJournal, withChain bool, dec types.Decider[*permission.ToolInvocation, permission.Verdict]) *govStack {
	t.Helper()
	f := newGovFixture(t, jr)
	f.grants = stdperm.NewMemoryGrantStore(func() time.Time { return f.now() })
	f.build(t, jr, withChain, dec, gohan.ApprovalGrants(f.grants))
	return f
}

// newGovStackNoGrants binds no grant store, so a scoped approval has no
// store to land its grant in.
func newGovStackNoGrants(t *testing.T, jr *stores.MemoryJournal, withChain bool, dec types.Decider[*permission.ToolInvocation, permission.Verdict]) *govStack {
	t.Helper()
	f := newGovFixture(t, jr)
	f.build(t, jr, withChain, dec)
	return f
}

func newGovFixture(t *testing.T, jr *stores.MemoryJournal) *govStack {
	t.Helper()
	return &govStack{
		tool: &govTool{},
		rt:   &govRT{args: json.RawMessage(`{"n":1}`)},
		jr:   jr,
		now:  time.Now,
	}
}

func (f *govStack) build(t *testing.T, jr *stores.MemoryJournal, withChain bool, dec types.Decider[*permission.ToolInvocation, permission.Verdict], extra ...gohan.Option) {
	t.Helper()
	f.audit = stores.NewMemoryAuditLog()
	f.lookup = func(string) (types.ToolSpec, bool) { return f.tool.Spec(), true }
	var chain chains.ToolChain
	if jr != nil && withChain {
		chain = chains.ToolChain{
			{Name: "shield", Kind: chains.KindGuard, Use: std.Shield(f.lookup)},
			{Name: "journal", Kind: chains.KindJournal, Use: std.Journal(jr, std.WithJournalSpecs(f.lookup))},
		}
	}
	log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
	f.runs = stores.NewMemoryRuns(stores.WithMemoryRunInfo(types.RunInfoFrom))
	f.cps = stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom))
	opts := []gohan.Option{
		gohan.WithModels(govModel{}),
		gohan.WithStores(stores.Stores{SessionLog: log, Runs: f.runs, Checkpoints: f.cps, Journal: jr, Audit: f.audit}),
		gohan.WithLimits("chat", types.RunLimits{
			MaxTurns: 8, MaxToolCalls: 8, MaxWallClock: time.Minute,
			SoftRatio: 0.8, MaxDepth: 4, MaxParallelChildren: 2, MaxParallelTools: 2,
		}),
		gohan.WithApprovalPolicySource(govPolicy{}),
		gohan.WithCredentialSource(govCreds{}),
		gohan.WithForeignAgent(gohan.ForeignSpec{
			Request:    gohan.FlowRequest{Name: "chat"},
			Profile:    "gov-test",
			NewRuntime: func() runtime.Runtime { return f.rt },
			Tools:      []types.Tool{f.tool},
			ToolChain:  chain,
			Decider:    dec,
		}),
	}
	stack, err := gohan.Build(append(opts, extra...)...)
	if err != nil {
		t.Fatal(err)
	}
	f.stack = stack
	conv, err := gohan.NewForeignConversation(stack, "chat",
		gohan.WithConversationRuns(f.runs),
		gohan.WithConversationEventLog(stores.NewMemoryEventLog()),
	)
	if err != nil {
		t.Fatal(err)
	}
	f.conv = conv
}

// govModel is the profile-backed model the flow's governed invocations
// route through.
type govModel struct{}

func (govModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "gov-test", Caps: types.Caps{Tools: true}}
}

func (govModel) Generate(context.Context, types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return nil
}

func (f *govStack) send(t *testing.T) (types.ResumeToken, any, error) {
	t.Helper()
	ctx := types.WithPrincipal(context.Background(), govOwner)
	var token types.ResumeToken
	var payload any
	var sendErr error
	for ev, err := range f.conv.Send(ctx, "s1", types.Message{
		Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "go"}},
	}) {
		if s, ok := ev.(types.Suspended); ok {
			token, payload = s.Token, s.Payload
		}
		if err != nil {
			sendErr = err
		}
	}
	return token, payload, sendErr
}

// setArgs re-points the runtime's next drive at different arguments, so a
// changed fingerprint meets the stored grant.
func (f *govStack) setArgs(args string) {
	f.rt.mu.Lock()
	f.rt.args = json.RawMessage(args)
	f.rt.mu.Unlock()
}

func (f *govStack) sendApprover(t *testing.T) (types.ResumeToken, any, error) {
	t.Helper()
	ctx := types.WithPrincipal(context.Background(), govOp)
	var token types.ResumeToken
	var payload any
	var sendErr error
	for ev, err := range f.conv.Send(ctx, "s1", types.Message{
		Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "again"}},
	}) {
		if s, ok := ev.(types.Suspended); ok {
			token, payload = s.Token, s.Payload
		}
		if err != nil {
			sendErr = err
		}
	}
	return token, payload, sendErr
}

func (f *govStack) resume(t *testing.T, token types.ResumeToken) error {
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

func (f *govStack) resumeScope(t *testing.T, token types.ResumeToken, ttl time.Duration) error {
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

func (f *govStack) entries(t *testing.T, args string) []stores.Entry {
	t.Helper()
	entries, err := f.jr.ByFingerprint(context.Background(), "s1", std.CanonicalFingerprint("confirm", json.RawMessage(args)))
	if err != nil {
		t.Fatalf("ByFingerprint: %v", err)
	}
	return entries
}

func TestGovernedForeignCall(t *testing.T) {
	args := `{"n":1}`

	// runtime.foreign-call-allowed: the gate allows, the chain journals one
	// Completed entry under the call's own id, and the implementation runs
	// once. A chain without a journal step records nothing.
	t.Run("runtime.foreign-call-allowed", func(t *testing.T) {
		f := newGovStack(t, stores.NewMemoryJournal(), true, govDecider{verdict: permission.Allow})
		if _, _, err := f.send(t); err != nil {
			t.Fatalf("Send: %v", err)
		}
		if n := f.tool.ran(); n != 1 {
			t.Fatalf("tool ran %d times, want 1", n)
		}
		entries := f.entries(t, args)
		if len(entries) != 1 || entries[0].State != stores.Completed || entries[0].Key != "call-1" {
			t.Fatalf("entries = %+v, want one Completed entry under call-1", entries)
		}

		// A flow whose chain declares no journal step holds no entry: the
		// journal store is wired, but the governed chain records nothing.
		f2 := newGovStack(t, stores.NewMemoryJournal(), false, govDecider{verdict: permission.Allow})
		if _, _, err := f2.send(t); err != nil {
			t.Fatalf("Send without journal chain: %v", err)
		}
		if entries := f2.entries(t, args); len(entries) != 0 {
			t.Fatalf("entries = %+v, want none without a journal step", entries)
		}
	})

	// runtime.foreign-call-denied: the denial renders the not_executed
	// permanent result, the implementation never runs and the journal holds
	// no entry.
	t.Run("runtime.foreign-call-denied", func(t *testing.T) {
		f := newGovStack(t, stores.NewMemoryJournal(), true, govDecider{verdict: permission.DenyVerdict})
		if _, _, err := f.send(t); err != nil {
			t.Fatalf("Send: %v", err)
		}
		seen := f.rt.seen()
		if len(seen) != 1 || seen[0].Outcome != types.Failed || seen[0].Error == nil ||
			!strings.HasPrefix(seen[0].Error.Message, runtime.NotExecutedPrefix) {
			t.Fatalf("result = %+v, want one Failed result under %q", seen, runtime.NotExecutedPrefix)
		}
		if n := f.tool.ran(); n != 0 {
			t.Fatalf("tool ran %d times, want 0 for a denied call", n)
		}
		if entries := f.entries(t, args); len(entries) != 0 {
			t.Fatalf("entries = %+v, want none for a denied call", entries)
		}
	})

	// permission.foreign-deny: the policy refuses the foreign run's call
	// through the hard scope block the decider cannot widen — the run
	// principal holds none of the tool's required scopes while the decider
	// would allow. The implementation never runs, no journal entry lands,
	// and the model side receives the not_executed permanent result.
	t.Run("permission.foreign-deny", func(t *testing.T) {
		f := newGovFixture(t, stores.NewMemoryJournal())
		// The scopes are on the spec before Build resolves the toolset,
		// so the gate's snapshot carries them.
		f.tool.required = []string{"tool:confirm"}
		f.build(t, f.jr, true, govDecider{verdict: permission.Allow})
		if _, _, err := f.send(t); err != nil {
			t.Fatalf("Send: %v", err)
		}
		if n := f.tool.ran(); n != 0 {
			t.Fatalf("tool ran %d times, want 0 behind the hard deny", n)
		}
		if entries := f.entries(t, args); len(entries) != 0 {
			t.Fatalf("entries = %+v, want none for the denied call", entries)
		}
		seen := f.rt.govCalls()
		if len(seen) != 1 || seen[0].ID != "call-1" {
			t.Fatalf("results = %+v, want one result under call-1", seen)
		}
		got := seen[0]
		if got.Outcome != types.Failed || got.Error == nil ||
			!strings.HasPrefix(got.Error.Message, runtime.NotExecutedPrefix) {
			t.Fatalf("result = %+v, want one Failed result under %q", got, runtime.NotExecutedPrefix)
		}
	})

	// runtime.foreign-mixed-stack-invoke: with a native flow and the
	// foreign flow registered over the same driver profile, the foreign
	// run's governed call still passes the gate, journals exactly once for
	// its call key, and the tool result reaches the model side.
	t.Run("runtime.foreign-mixed-stack-invoke", func(t *testing.T) {
		f := newGovFixture(t, stores.NewMemoryJournal())
		dec := &govCountDecider{}
		f.build(t, f.jr, true, dec,
			gohan.WithNativeAgent(gohan.NativeSpec{Request: gohan.FlowRequest{Name: "native-flow"}, Profile: "gov-test"}))
		if _, _, err := f.send(t); err != nil {
			t.Fatalf("Send: %v", err)
		}
		saw := dec.invocations()
		if len(saw) != 1 || saw[0].Spec.Name != "confirm" || saw[0].Call.ID != "call-1" {
			t.Fatalf("gate invocations = %+v, want one for confirm under call-1", saw)
		}
		if n := f.tool.ran(); n != 1 {
			t.Fatalf("tool ran %d times, want exactly the governed call", n)
		}
		entries := f.entries(t, args)
		if len(entries) != 1 || entries[0].State != stores.Completed || entries[0].Key != "call-1" {
			t.Fatalf("entries = %+v, want one Completed entry under call-1", entries)
		}
		results := f.rt.govCalls()
		if len(results) != 1 || results[0].ID != "call-1" || results[0].Outcome != types.Succeeded {
			t.Fatalf("results = %+v, want one Succeeded result under call-1 reaching the model side", results)
		}
	})

	t.Run("permission.foreign-ask", func(t *testing.T) {
		f := newGovStack(t, stores.NewMemoryJournal(), true, nil)
		token, payload, err := f.send(t)
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if token == "" {
			t.Fatal("Send did not suspend, want the approval token")
		}
		req, ok := payload.(permission.ApprovalRequest)
		if !ok || req.Call.ID != "call-1" {
			t.Fatalf("payload = %+v, want the ApprovalRequest naming call-1", payload)
		}
		if n := f.tool.ran(); n != 0 {
			t.Fatalf("tool ran %d times before approval, want 0", n)
		}
		if err := f.resume(t, token); err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if n := f.tool.ran(); n != 1 {
			t.Fatalf("tool ran %d times after approval, want exactly the approved call", n)
		}
		entries := f.entries(t, args)
		if len(entries) != 1 || entries[0].State != stores.Completed {
			t.Fatalf("entries = %+v, want one Completed entry after approval", entries)
		}
	})

	// permission.foreign-grant-reused: the scoped resume settles the
	// originator's parked call under the approving subject, the grant and
	// its audit record name that subject, and the same fingerprint reissued
	// under a fresh call id runs on the stored grant without suspending
	// again. A changed fingerprint is outside the grant and asks again.
	t.Run("permission.foreign-grant-reused", func(t *testing.T) {
		f := newGovStack(t, stores.NewMemoryJournal(), true, nil)
		token, _, err := f.send(t)
		if err != nil || token == "" {
			t.Fatalf("Send: token=%q err=%v, want a suspension", token, err)
		}
		if err := f.resumeScope(t, token, time.Hour); err != nil {
			t.Fatalf("Resume with scope: %v", err)
		}
		if n := f.tool.ran(); n != 1 {
			t.Fatalf("tool ran %d times, want exactly the approved call", n)
		}
		fp := gohan.ToolFingerprint(f.tool.Spec(), json.RawMessage(args))
		if _, ok, err := f.grants.Find(context.Background(), "s1", "confirm", fp, govOp.Subject); err != nil || !ok {
			t.Fatalf("grant for (s1, confirm, ops) not stored: ok=%v err=%v", ok, err)
		}
		found := 0
		for rec, err := range f.audit.Read(context.Background(), "s1") {
			if err != nil {
				t.Fatalf("audit read: %v", err)
			}
			if stores.AuditKind("granted_by_scope") == rec.Kind {
				found++
				if rec.Tool != "confirm" || rec.Approver != govOp.Subject {
					t.Fatalf("audit record = %+v, want tool confirm approver ops", rec)
				}
			}
		}
		if found != 1 {
			t.Fatalf("audit carries %d granted_by_scope records, want 1", found)
		}
		token2, _, err := f.sendApprover(t)
		if err != nil {
			t.Fatalf("second Send: %v", err)
		}
		if token2 != "" {
			t.Fatal("second call under the granted fingerprint suspended again inside the TTL")
		}
		if n := f.tool.ran(); n != 2 {
			t.Fatalf("tool ran %d times, want the granted call to execute twice", n)
		}
		f.setArgs(`{"n":2}`)
		token3, _, err := f.sendApprover(t)
		if err != nil {
			t.Fatalf("third Send: %v", err)
		}
		if token3 == "" {
			t.Fatal("a changed fingerprint executed on the granted fingerprint's grant")
		}
		if n := f.tool.ran(); n != 2 {
			t.Fatalf("tool ran %d times, want no execution for the changed fingerprint", n)
		}
	})

	// permission.foreign-grant-store-required: a scoped approval with no
	// bound grant store fails closed before the approved call runs.
	t.Run("permission.foreign-grant-store-required", func(t *testing.T) {
		f := newGovStackNoGrants(t, stores.NewMemoryJournal(), true, nil)
		token, _, err := f.send(t)
		if err != nil || token == "" {
			t.Fatalf("Send: token=%q err=%v, want a suspension", token, err)
		}
		if err := f.resumeScope(t, token, time.Hour); !errors.Is(err, types.ErrInputInvalid) {
			t.Fatalf("scoped resume without a grant store: err = %v, want %v", err, types.ErrInputInvalid)
		}
		if n := f.tool.ran(); n != 0 {
			t.Fatalf("tool ran %d times, want 0 when the scoped resume failed closed", n)
		}
		if entries := f.entries(t, args); len(entries) != 0 {
			t.Fatalf("entries = %+v, want none when the scoped resume failed closed", entries)
		}
	})

	t.Run("scope-invalid-input-keeps-token-pending", func(t *testing.T) {
		f := newGovStack(t, stores.NewMemoryJournal(), true, nil)
		token, _, err := f.send(t)
		if err != nil || token == "" {
			t.Fatalf("Send: token=%q err=%v, want a suspension", token, err)
		}
		if err := f.resumeScope(t, token, -time.Minute); err == nil {
			t.Fatal("negative scope TTL accepted")
		}
		if err := f.resume(t, token); err != nil {
			t.Fatalf("Resume after refused scope: %v", err)
		}
		if n := f.tool.ran(); n != 1 {
			t.Fatalf("tool ran %d times, want the plain approval to execute once", n)
		}
	})

	t.Run("scope-deny-wins-over-grant", func(t *testing.T) {
		dec := &govAskThenDeny{}
		f := newGovStack(t, stores.NewMemoryJournal(), true, dec)
		token, _, err := f.send(t)
		if err != nil || token == "" {
			t.Fatalf("Send: token=%q err=%v, want a suspension", token, err)
		}
		dec.deny = true
		if err := f.resumeScope(t, token, time.Hour); err != nil {
			t.Fatalf("Resume with scope: %v", err)
		}
		if n := f.tool.ran(); n != 0 {
			t.Fatalf("tool ran %d times, want 0 under a live deny", n)
		}
		// The resumed drive replays the parked call under its recorded
		// identity, so the denial settles call-1, never a reissued id.
		seen := f.rt.govCalls()
		if len(seen) != 1 || seen[0].ID != "call-1" {
			t.Fatalf("results = %+v, want one result under the parked call's own id", seen)
		}
		got := seen[0]
		if got.Outcome != types.Failed || got.Error == nil ||
			!strings.HasPrefix(got.Error.Message, runtime.NotExecutedPrefix) {
			t.Fatalf("result = %+v, want one Failed result under %q", got, runtime.NotExecutedPrefix)
		}
		fp := gohan.ToolFingerprint(f.tool.Spec(), json.RawMessage(args2))
		if _, ok, err := f.grants.Find(context.Background(), "s1", "confirm", fp, govOp.Subject); err != nil || !ok {
			t.Fatalf("grant not stored before the refused replay: ok=%v err=%v", ok, err)
		}
	})
}

// args2 is the governed call's fingerprinted arguments.
const args2 = `{"n":1}`

// govAskThenDeny asks on the first decision and denies afterwards, so a
// resumed replay meets a live deny while a grant is on file.
type govAskThenDeny struct {
	deny bool
}

func (d *govAskThenDeny) Decide(_ context.Context, inv *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	v := permission.Ask
	if d.deny {
		v = permission.DenyVerdict
	}
	return types.Decision[permission.Verdict]{Value: v, Confidence: 1}, nil
}

// govCountDecider records every invocation the gate consults and allows
// the call.
type govCountDecider struct {
	mu   sync.Mutex
	seen []permission.ToolInvocation
}

func (d *govCountDecider) Decide(_ context.Context, inv *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	d.mu.Lock()
	d.seen = append(d.seen, *inv)
	d.mu.Unlock()
	return types.Decision[permission.Verdict]{Value: permission.Allow, Confidence: 1}, nil
}

func (d *govCountDecider) invocations() []permission.ToolInvocation {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]permission.ToolInvocation(nil), d.seen...)
}
