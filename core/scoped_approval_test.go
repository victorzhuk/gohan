package gohan

import (
	"context"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	std "github.com/victorzhuk/gohan/std/permission"
)

// scopedApprover holds the session-write and approval scope the resume
// authorization and the eligibility check ask for.
var scopedApprover = types.Principal{
	Tenant: "t1", Subject: "ops",
	Scopes: []string{types.ScopeSessionRead, types.ScopeSessionWrite, "pay"},
}

func scopedCtx() context.Context {
	return types.WithPrincipal(context.Background(), scopedApprover)
}

// scopedStack builds the native ask flow with a grant store and an audit
// log wired, so an ApproveScope settlement is observable end to end.
func scopedStack(t *testing.T, model types.Model, probe *naraProbeTool, now func() time.Time) (*Stack, Conversation, *std.MemoryGrantStore, stores.AuditLog) {
	t.Helper()
	runs := stores.NewMemoryRuns(
		stores.WithMemoryRunClock(now),
		stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
			p, ok := types.PrincipalFrom(ctx)
			return types.RunInfo{Principal: p}, ok
		}),
	)
	grants := std.NewMemoryGrantStore(now)
	audit := stores.NewMemoryAuditLog()
	stack, err := Build(
		WithStores(stores.Stores{
			SessionLog:  stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
			Runs:        runs,
			Checkpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
			Audit:       audit,
		}),
		WithModels(model),
		ApprovalGrants(grants),
		WithNativeAgent(NativeSpec{
			Request: FlowRequest{Name: "chat"},
			Profile: "native",
			Tools:   []types.Tool{probe},
			Decider: naraAskDecider{},
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
			},
		}),
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	conv, err := NewNativeConversation(stack, "chat",
		WithConversationRuns(runs),
		WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(now))),
		WithConversationApprovalPolicy(nrrAllowPolicy{}),
	)
	if err != nil {
		t.Fatalf("new native conversation: %v", err)
	}
	return stack, conv, grants, audit
}

func scopedSuspend(t *testing.T, conv Conversation, ctx context.Context) types.ResumeToken {
	t.Helper()
	var token types.ResumeToken
	for ev, err := range conv.Send(ctx, "s1", nlUser("go")) {
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if s, ok := ev.(types.Suspended); ok {
			token = s.Token
		}
	}
	return token
}

func scopedResume(t *testing.T, conv Conversation, token types.ResumeToken, in stores.ResumeInput) error {
	t.Helper()
	var resumeErr error
	for _, err := range conv.Resume(scopedCtx(), token, in) {
		if err != nil {
			resumeErr = errors.Join(resumeErr, err)
		}
	}
	return resumeErr
}

func TestNativeScopedApprovalStoresGrantAndAudit(t *testing.T) {
	model := &nrrModel{}
	probe := &naraProbeTool{}
	now := time.Now
	stack, conv, grants, audit := scopedStack(t, model, probe, now)
	model.reset(types.ToolUse{ID: "p1", Name: "probe", Args: jsontextValue(`{"id":"p1"}`)})

	token := scopedSuspend(t, conv, nrrCtx())
	if token == "" {
		t.Fatal("Send: no suspension")
	}
	if err := scopedResume(t, conv, token, ApproveScope(time.Hour)); err != nil {
		t.Fatalf("Resume with scope: %v", err)
	}
	if ran := probe.ran(); len(ran) != 1 {
		t.Fatalf("probe executions = %v, want exactly the approved call", ran)
	}
	fp := ToolFingerprint(probe.Spec(), json.RawMessage(`{"id":"p1"}`))
	if _, ok, err := grants.Find(context.Background(), "s1", "probe", fp, scopedApprover.Subject); err != nil || !ok {
		t.Fatalf("grant for (s1, probe, ops) not stored: ok=%v err=%v", ok, err)
	}
	found := 0
	for rec, err := range audit.Read(context.Background(), "s1") {
		if err != nil {
			t.Fatalf("audit read: %v", err)
		}
		if stores.AuditKind("granted_by_scope") == rec.Kind {
			found++
			if rec.Tool != "probe" || rec.Approver != scopedApprover.Subject {
				t.Fatalf("audit record = %+v, want tool probe approver ops", rec)
			}
		}
	}
	if found != 1 {
		t.Fatalf("audit carries %d granted_by_scope records, want 1", found)
	}

	// The scoped receipt grants nothing on its own; the store does. The
	// same approving subject, the granted fingerprint and a fresh call
	// id under the granted TTL execute without a second ask. nrrModel
	// emits one scripted call per tool result the history already
	// carries, so the already-answered first call stays in the script.
	model.reset(
		types.ToolUse{ID: "p1", Name: "probe", Args: jsontextValue(`{"id":"p1"}`)},
		types.ToolUse{ID: "p2", Name: "probe", Args: jsontextValue(`{"id":"p1"}`)},
	)
	if token := scopedSuspend(t, conv, scopedCtx()); token != "" {
		t.Fatal("second call under the approving subject asked again inside the granted TTL")
	}
	if ran := probe.ran(); len(ran) != 2 {
		t.Fatalf("probe executions = %v, want the granted call to execute again", ran)
	}

	// A different subject still asks: the grant is keyed on the approver.
	model.reset(
		types.ToolUse{ID: "p1", Name: "probe", Args: jsontextValue(`{"id":"p1"}`)},
		types.ToolUse{ID: "p2", Name: "probe", Args: jsontextValue(`{"id":"p1"}`)},
		types.ToolUse{ID: "p3", Name: "probe", Args: jsontextValue(`{"id":"p1"}`)},
	)
	if token := scopedSuspend(t, conv, nrrCtx()); token == "" {
		t.Fatal("a call under another subject executed on the approver's grant")
	}

	// A changed fingerprint still asks: the grant pins the call identity.
	model.reset(
		types.ToolUse{ID: "p1", Name: "probe", Args: jsontextValue(`{"id":"p1"}`)},
		types.ToolUse{ID: "p2", Name: "probe", Args: jsontextValue(`{"id":"p1"}`)},
		types.ToolUse{ID: "p3", Name: "probe", Args: jsontextValue(`{"id":"p1"}`)},
		types.ToolUse{ID: "p4", Name: "probe", Args: jsontextValue(`{"id":"p1"}`)},
		types.ToolUse{ID: "p5", Name: "probe", Args: jsontextValue(`{"id":"p9"}`)},
	)
	if token := scopedSuspend(t, conv, scopedCtx()); token == "" {
		t.Fatal("a changed fingerprint executed on the granted fingerprint's grant")
	}

	// The scoped receipt never grants through the receipt path.
	h, err := stack.stores.SessionLog.Load(scopedCtx(), "s1")
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	args, err := latestApprovedArgs(h, "probe")
	if err != nil {
		t.Fatalf("latestApprovedArgs: %v", err)
	}
	if args != nil {
		t.Fatalf("scoped receipt granted through the receipt path: args=%s", args)
	}
}

// scopedAskDecider asks for every side-effect call, so the resumed turn's
// second call reaches the gate as a fresh one.
type scopedAskDecider struct{}

func (scopedAskDecider) Decide(_ context.Context, inv *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	if inv.Spec.Effect == types.ReadOnly {
		return types.Decision[permission.Verdict]{Value: permission.Allow, Confidence: 1}, nil
	}
	return types.Decision[permission.Verdict]{Value: permission.Ask, Confidence: 1}, nil
}

// resumedScopedStack builds the native flow over a side-effect tool, so the
// resumed turn's second call is one the gate must decide afresh.
func resumedScopedStack(t *testing.T, model types.Model, book *bookTool) (*Stack, Conversation, *std.MemoryGrantStore) {
	t.Helper()
	runs := stores.NewMemoryRuns(
		stores.WithMemoryRunClock(time.Now),
		stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
			p, ok := types.PrincipalFrom(ctx)
			return types.RunInfo{Principal: p}, ok
		}),
	)
	grants := std.NewMemoryGrantStore(time.Now)
	stack, err := Build(
		WithStores(stores.Stores{
			SessionLog:  stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
			Runs:        runs,
			Checkpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
			Audit:       stores.NewMemoryAuditLog(),
		}),
		WithModels(model),
		ApprovalGrants(grants),
		WithNativeAgent(NativeSpec{
			Request: FlowRequest{Name: "chat"},
			Profile: "native",
			Tools:   []types.Tool{book},
			Decider: scopedAskDecider{},
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
			},
		}),
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	conv, err := NewNativeConversation(stack, "chat",
		WithConversationRuns(runs),
		WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
		WithConversationApprovalPolicy(nrrAllowPolicy{}),
	)
	if err != nil {
		t.Fatalf("new native conversation: %v", err)
	}
	return stack, conv, grants
}

// TestNativeResumedScopeServesOnlyTheParkedCall pins the subject boundary
// of a scoped approval: the resume executes the parked call the approver
// decided, and the second fresh call the same resumed turn issues asks
// again — the ambient approval speaks for the parked call only, never for
// the rest of the turn.
func TestNativeResumedScopeServesOnlyTheParkedCall(t *testing.T) {
	model := &nrrModel{}
	book := &bookTool{}
	_, conv, grants := resumedScopedStack(t, model, book)
	// The second call repeats the approved call's arguments under a fresh
	// call id, so the fingerprint is the granted one and only the subject
	// boundary can decide it.
	again := types.ToolUse{ID: "c2", Name: "book", Args: jsontextValue(`{"id":"c1"}`)}
	model.reset(nrrBook("c1"), again)

	var token types.ResumeToken
	for ev, err := range conv.Send(nrrCtx(), "s1", nlUser("go")) {
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if s, ok := ev.(types.Suspended); ok {
			token = s.Token
		}
	}
	if token == "" {
		t.Fatal("Send: no suspension")
	}

	// The approver op decides the parked call and scopes the grant; the
	// resumed turn then issues a second call of its own.
	var suspAgain bool
	var resumeErr error
	for ev, err := range conv.Resume(scopedCtx(), token, ApproveScope(time.Hour)) {
		if err != nil {
			resumeErr = errors.Join(resumeErr, err)
		}
		if _, ok := ev.(types.Suspended); ok {
			suspAgain = true
		}
	}
	if resumeErr != nil {
		t.Fatalf("Resume: %v", resumeErr)
	}
	if ran := book.ran(); len(ran) != 1 || ran[0] != "c1" {
		t.Fatalf("tool executions = %v, want only the approved call c1", ran)
	}
	if !suspAgain {
		t.Fatal("the fresh call in the resumed turn executed on the approver's grant")
	}
	// The grant is keyed on the approving subject, and it survived.
	fp := ToolFingerprint(book.Spec(), again.Args)
	if _, ok, err := grants.Find(context.Background(), "s1", "book", fp, scopedApprover.Subject); err != nil || !ok {
		t.Fatalf("grant for (s1, book, ops) not stored: ok=%v err=%v", ok, err)
	}

	// The originator's own grant does authorize its fresh call.
	if err := grants.Put(context.Background(), "s1", permission.Grant{
		Tool: "book", Fingerprint: fp, Subject: nrrOwner.Subject,
		Approver: scopedApprover.Subject, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("Put originator grant: %v", err)
	}
	// The model scripts one call per result the history already carries, so
	// the granted call sits at the index the answered results reach.
	model.reset(nrrBook("c9"), again)
	suspAgain = false
	for ev, err := range conv.Send(nrrCtx(), "s1", nlUser("again")) {
		if err != nil {
			t.Fatalf("Send under the originator's grant: %v", err)
		}
		if _, ok := ev.(types.Suspended); ok {
			suspAgain = true
		}
	}
	if suspAgain {
		t.Fatal("the originator's own grant did not authorize its fresh call")
	}
	if ran := book.ran(); len(ran) != 2 || ran[1] != "c1" {
		t.Fatalf("tool executions = %v, want the originator's fresh call to execute once", ran)
	}
}

func TestNativeScopedApprovalRefusesInvalidInput(t *testing.T) {
	model := &nrrModel{}
	probe := &naraProbeTool{}
	_, conv, _, _ := scopedStack(t, model, probe, time.Now)
	model.reset(types.ToolUse{ID: "p1", Name: "probe", Args: jsontextValue(`{"id":"p1"}`)})
	token := scopedSuspend(t, conv, nrrCtx())

	if err := scopedResume(t, conv, token, ApproveScope(-time.Minute)); err == nil {
		t.Fatal("negative scope TTL accepted")
	}
	// The refusal consumed nothing: the plain approval still settles.
	if err := scopedResume(t, conv, token, Approve()); err != nil {
		t.Fatalf("Resume after refused scope: %v", err)
	}
	if ran := probe.ran(); len(ran) != 1 {
		t.Fatalf("probe executions = %v, want the plain approval to execute once", ran)
	}
}

func TestScopeGrantCheckEnforcesSubjectAndExpiry(t *testing.T) {
	spec := types.ToolSpec{Name: "probe", Effect: types.ReadOnly}
	fp := ToolFingerprint(spec, json.RawMessage(`{"id":"p1"}`))
	inv := &permission.ToolInvocation{Spec: spec, Call: types.ToolUse{ID: "p1", Name: "probe", Args: jsontext.Value(`{"id":"p1"}`)}}

	at := time.Now()
	store := std.NewMemoryGrantStore(func() time.Time { return at })
	// The approving subject serves the parked call the resume approved,
	// and only that one: the marker is the persisted decision, matched on
	// call id, fingerprint and the complete argument value together.
	ctx := withApprovedCalls(types.WithApproval(types.WithRunInfo(types.WithPrincipal(context.Background(), scopedApprover),
		types.RunInfo{SessionID: "s1"}), types.Approval{Approver: scopedApprover}),
		map[approvedCall]bool{{id: "p1", fingerprint: fp, fullArgs: fullArgsFingerprint("probe", json.RawMessage(`{"id":"p1"}`)), generation: 1}: true})
	check := scopeGrantCheck(store)
	grant := func(expires time.Time) {
		if err := store.Put(context.Background(), "s1", permission.Grant{
			Tool: "probe", Fingerprint: fp, Subject: scopedApprover.Subject,
			Approver: scopedApprover.Subject, ExpiresAt: expires,
		}); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}

	grant(at.Add(time.Hour))
	if !check(ctx, inv) {
		t.Fatal("live grant for the approving subject did not pass the check")
	}
	other := types.WithApproval(ctx, types.Approval{Approver: types.Principal{Tenant: "t1", Subject: "mallory"}})
	if check(other, inv) {
		t.Fatal("a grant keyed to another subject passed the check")
	}
	changed := &permission.ToolInvocation{Spec: spec, Call: types.ToolUse{ID: "p1", Name: "probe", Args: jsontext.Value(`{"id":"p2"}`)}}
	if check(ctx, changed) {
		t.Fatal("a grant for another fingerprint passed the check")
	}
	// permission.grant-does-not-cross-sessions-or-principals: the ambient
	// approval left in a resumed context speaks for the parked call only.
	// The resumed turn drives as the originator, so a fresh call under the
	// originator's principal must not ride the approver's grant.
	fresh := &permission.ToolInvocation{
		Spec: spec, Call: types.ToolUse{ID: "p2", Name: "probe", Args: jsontext.Value(`{"id":"p1"}`)},
		Run: types.RunInfo{SessionID: "s1", Principal: types.Principal{Tenant: "t1", Subject: "user"}},
	}
	if check(ctx, fresh) {
		t.Fatal("the approving subject's grant served a fresh call in the resumed turn")
	}
	// The originator's own grant does serve it, when one exists.
	if err := store.Put(context.Background(), "s1", permission.Grant{
		Tool: "probe", Fingerprint: fp, Subject: "user",
		Approver: scopedApprover.Subject, ExpiresAt: at.Add(time.Hour),
	}); err != nil {
		t.Fatalf("Put originator grant: %v", err)
	}
	if !check(ctx, fresh) {
		t.Fatal("the originator's own grant did not serve its fresh call")
	}
	at = at.Add(2 * time.Hour)
	if check(ctx, inv) {
		t.Fatal("an expired grant passed the check")
	}
	bare := types.WithRunInfo(context.Background(), types.RunInfo{SessionID: "s1"})
	if check(bare, inv) {
		t.Fatal("a context without the approving approval passed the check")
	}
}

func TestGrantCheckNeverOverridesHardRefusals(t *testing.T) {
	spec := types.ToolSpec{Name: "probe", Effect: types.SideEffect}
	fp := ToolFingerprint(spec, json.RawMessage(`{"id":"p1"}`))
	at := time.Now()
	store := std.NewMemoryGrantStore(func() time.Time { return at })
	if err := store.Put(context.Background(), "s1", permission.Grant{
		Tool: "probe", Fingerprint: fp, Subject: scopedApprover.Subject,
		Approver: scopedApprover.Subject, ExpiresAt: at.Add(time.Hour),
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	ctx := types.WithApproval(types.WithRunInfo(context.Background(), types.RunInfo{SessionID: "s1"}),
		types.Approval{Approver: scopedApprover})
	inv := &permission.ToolInvocation{Spec: spec, Call: types.ToolUse{ID: "p1", Name: "probe", Args: jsontext.Value(`{"id":"p1"}`)}}
	check := scopeGrantCheck(store)

	dec := permission.DecideCall(ctx, scopedAllowDecider{}, inv,
		permission.WithGrantCheck(check),
		permission.WithTaintHook(func(context.Context, types.ToolSpec, jsontext.Value) (types.TaintAction, []types.ArgTaint) {
			return types.TaintDeny, nil
		}),
	)
	if dec.Verdict != permission.TaintDenied {
		t.Fatalf("forced taint = %v, want TaintDenied over a live grant", dec.Verdict)
	}
	dec = permission.DecideCall(ctx, scopedDenyDecider{}, inv, permission.WithGrantCheck(check))
	if dec.Verdict != permission.DenyVerdict {
		t.Fatalf("live deny = %v, want DenyVerdict over a live grant", dec.Verdict)
	}
}

// scopedAllowDecider and scopedDenyDecider pin the decider verdicts the
// ordering test needs.
type scopedAllowDecider struct{}

func (scopedAllowDecider) Decide(context.Context, *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	return types.Decision[permission.Verdict]{Value: permission.Allow, Confidence: 1}, nil
}

type scopedDenyDecider struct{}

func (scopedDenyDecider) Decide(context.Context, *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	return types.Decision[permission.Verdict]{Value: permission.DenyVerdict, Confidence: 1}, nil
}

// jsontextValue hides the json/v2 import the tests need for one helper.
func jsontextValue(s string) json.RawMessage {
	return json.RawMessage(s)
}
