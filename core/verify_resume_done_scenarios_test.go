package gohan_test

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"strings"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

func staleClock() (func() time.Time, func()) {
	now := time.Unix(0, 0)
	tick := func() time.Time { return now }
	return tick, func() { now = now.Add(2 * stores.LeaseTTL) }
}

// verifyEffectTool records its executions and always reports an Unknown
// outcome, so the journal holds a durable entry the drivers must reconcile.
type verifyEffectTool struct {
	execs  int
	verify func(context.Context, json.RawMessage, types.ToolResult) (types.Outcome, error)
}

func (t *verifyEffectTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "book", Effect: types.SideEffect, Verify: t.verify}
}

func (t *verifyEffectTool) Call(context.Context, json.RawMessage) (types.ToolResult, error) {
	t.execs++
	return types.ToolResult{Outcome: types.Unknown, Error: &types.ToolError{Kind: types.OutcomeUnknown, Message: "timeout"}}, nil
}

// verifyAskTool is the second call in the script. In ask mode the decider
// sends it to human approval, so the Unknown book entry outlives the Send
// that made it and the approved replay executes the gate once. In recover
// mode the tool itself suspends the first execution and settles on the
// re-drive, which needs no consumed checkpoint.
type verifyAskTool struct {
	execs       int
	suspendOnce bool
}

func (t *verifyAskTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "gate", Effect: types.SideEffect}
}

func (t *verifyAskTool) Call(context.Context, json.RawMessage) (types.ToolResult, error) {
	t.execs++
	if t.suspendOnce && t.execs == 1 {
		return types.ToolResult{}, &types.SuspendError{Reason: types.HumanApproval}
	}
	return types.ToolResult{Outcome: types.Succeeded}, nil
}

// verifyAskDecider allows the effect and asks for the gate call.
type verifyAskDecider struct{}

func (verifyAskDecider) Decide(_ context.Context, inv *permission.ToolInvocation) (types.Decision[permission.Verdict], error) {
	if inv.Spec.Name == "gate" {
		return types.Decision[permission.Verdict]{Value: permission.Ask, Confidence: 1}, nil
	}
	return types.Decision[permission.Verdict]{Value: permission.Allow, Confidence: 1}, nil
}

type verifyAllowPolicy struct{}

func (verifyAllowPolicy) ApprovalPolicy(context.Context, types.RiskTier, string, bool) (permission.ApprovalPolicy, error) {
	return permission.ApprovalPolicy{}, nil
}

type recordingAudit struct {
	records []stores.AuditRecord
}

func (a *recordingAudit) Append(_ context.Context, r stores.AuditRecord) error {
	a.records = append(a.records, r)
	return nil
}

func (a *recordingAudit) Purge(context.Context, string, time.Time) (int, error) { return 0, nil }

func (a *recordingAudit) Read(_ context.Context, _ string) iter.Seq2[stores.AuditRecord, error] {
	return func(yield func(stores.AuditRecord, error) bool) {
		for _, r := range a.records {
			if !yield(r, nil) {
				return
			}
		}
	}
}

// flakyJournal fails Journal.Unknown on the calls its predicate picks.
type flakyJournal struct {
	stores.Journal
	calls   int
	failsOn func(call int) bool
}

func (j *flakyJournal) Unknown(ctx context.Context, sessionID string) ([]stores.Entry, error) {
	j.calls++
	if j.failsOn != nil && j.failsOn(j.calls) {
		return nil, errors.New("journal unavailable")
	}
	return j.Journal.Unknown(ctx, sessionID)
}

type verifyOutcome func(context.Context, json.RawMessage, types.ToolResult) (types.Outcome, error)

func settleWith(outcome types.Outcome, calls *int) verifyOutcome {
	return func(context.Context, json.RawMessage, types.ToolResult) (types.Outcome, error) {
		if calls != nil {
			*calls++
		}
		return outcome, nil
	}
}

type verifyResumeFixture struct {
	journal stores.Journal
	audit   *recordingAudit
	book    *verifyEffectTool
	gate    *verifyAskTool
	model   *gohantest.ScriptedModel
	runs    *stores.MemoryRuns
	stack   *gohan.Stack
	expire  func()
}

func newVerifyResumeFixture(t *testing.T, gateAsks bool, journal stores.Journal, bookVerify, gateVerify verifyOutcome, verifier func(journal stores.Journal, lookup func(string) (types.ToolSpec, bool)) types.Verifier) *verifyResumeFixture {
	t.Helper()
	audit := &recordingAudit{}
	book := &verifyEffectTool{verify: bookVerify}
	gate := &verifyAskTool{suspendOnce: !gateAsks}
	tick, expire := staleClock()
	lookup := func(name string) (types.ToolSpec, bool) {
		switch name {
		case "book":
			return book.Spec(), true
		case "gate":
			spec := gate.Spec()
			spec.Verify = gateVerify
			return spec, true
		}
		return types.ToolSpec{}, false
	}
	model := gohantest.NewScriptedModel(types.ModelProfile{Name: "native", Caps: types.Caps{Tools: true}},
		gohantest.ToolCall("book", map[string]any{"guest": "ada"}),
		gohantest.ToolCall("gate", map[string]any{"ok": true}),
		gohantest.Text("done"),
	)
	sessionID := func(context.Context) string { return "session" }
	runs := stores.NewMemoryRuns(stores.WithMemoryRunClock(tick), stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
		principal, ok := types.PrincipalFrom(ctx)
		return types.RunInfo{Principal: principal}, ok
	}))
	decider := types.Decider[*permission.ToolInvocation, permission.Verdict](verifyAllowDecider{})
	if gateAsks {
		decider = verifyAskDecider{}
	}
	buildOpts := []gohan.Option{
		gohan.AllowAnonymous(),
		gohan.WithStores(stores.Stores{
			SessionLog:  stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
			Checkpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(tick), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
			Journal:     journal,
			Runs:        runs,
		}),
		gohan.WithModels(model),
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request: gohan.FlowRequest{Name: "chat"},
			Profile: "native",
			Tools:   []types.Tool{book, gate},
			ToolChain: chains.ToolChain{{
				Name: "journal",
				Kind: chains.KindJournal,
				Use: std.Journal(journal,
					std.WithJournalSpecs(lookup),
					std.WithJournalSessionID(sessionID),
				),
			}},
			Decider: decider,
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
			},
		}),
	}
	if verifier != nil {
		buildOpts = append(buildOpts, gohan.WithVerifier(verifier(journal, lookup)))
	}
	stack, err := gohan.Build(buildOpts...)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return &verifyResumeFixture{journal: journal, audit: audit, book: book, gate: gate, model: model, runs: runs, stack: stack, expire: expire}
}

func (f *verifyResumeFixture) conversation(t *testing.T) gohan.Conversation {
	t.Helper()
	conv, err := gohan.NewNativeConversation(f.stack, "chat",
		gohan.WithConversationRuns(f.runs),
		gohan.WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
		gohan.WithConversationApprovalPolicy(verifyAllowPolicy{}),
	)
	if err != nil {
		t.Fatalf("NewNativeConversation: %v", err)
	}
	return conv
}

func verifyResumeCtx(audit stores.AuditLog) context.Context {
	ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "owner"})
	return gohan.WithDecisionAudit(ctx, audit)
}

func (f *verifyResumeFixture) sendUntilAsk(t *testing.T, conv gohan.Conversation, ctx context.Context) types.ResumeToken {
	t.Helper()
	var token types.ResumeToken
	for ev, err := range conv.Send(ctx, "session", types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "book ada"}}}) {
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
		if s, ok := ev.(types.Suspended); ok {
			token = s.Token
		}
	}
	if token == "" {
		t.Fatal("Send: no Suspended token")
	}
	if f.book.execs != 1 {
		t.Fatalf("book executed %d times before the suspension, want 1", f.book.execs)
	}
	if entry := f.bookEntry(t, ctx); entry.Result.Outcome != types.Unknown {
		t.Fatalf("journal outcome after Send = %v, want the Unknown entry preserved", entry.Result.Outcome)
	}
	return token
}

func (f *verifyResumeFixture) bookEntry(t *testing.T, ctx context.Context) stores.Entry {
	t.Helper()
	args, err := json.Marshal(map[string]any{"guest": "ada"})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	entries, err := f.journal.ByFingerprint(ctx, "session", std.CanonicalFingerprint("book", args))
	if err != nil {
		t.Fatalf("ByFingerprint: %v", err)
	}
	for _, entry := range entries {
		if entry.Tool == "book" {
			return entry
		}
	}
	t.Fatalf("journal holds no book entry: %+v", entries)
	return stores.Entry{}
}

func (f *verifyResumeFixture) bookCallID(t *testing.T) types.CallKey {
	t.Helper()
	for _, req := range f.model.Requests() {
		for _, message := range req.Messages {
			for _, block := range message.Blocks {
				if use, ok := block.(types.ToolUse); ok && use.Name == "book" {
					return types.CallKey{SessionID: "session", CallID: use.ID}
				}
			}
		}
	}
	t.Fatal("model never requested the book call")
	return types.CallKey{}
}

func (f *verifyResumeFixture) modelSawOutcome(want types.Outcome) bool {
	for _, req := range f.model.Requests() {
		for _, message := range req.Messages {
			for _, block := range message.Blocks {
				if result, ok := block.(types.ToolResult); ok && result.ID != "" && result.Outcome == want {
					return true
				}
			}
		}
	}
	return false
}

func (f *verifyResumeFixture) auditHasBookOutcome(outcome types.Outcome) bool {
	for _, r := range f.audit.records {
		if r.Kind == stores.AuditToolOutcome && r.Tool == "book" && r.Outcome == outcome {
			return true
		}
	}
	return false
}

func resumeCollect(t *testing.T, conv gohan.Conversation, ctx context.Context, token types.ResumeToken) (types.Done, error) {
	t.Helper()
	var done types.Done
	var termErr error
	for ev, err := range conv.Resume(ctx, token, gohan.Approve()) {
		if err != nil {
			termErr = err
			continue
		}
		if d, ok := ev.(types.Done); ok {
			done = d
		}
	}
	return done, termErr
}

func uncertainHas(keys []types.CallKey, want types.CallKey) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}

func stdVerifier() func(stores.Journal, func(string) (types.ToolSpec, bool)) types.Verifier {
	return func(journal stores.Journal, lookup func(string) (types.ToolSpec, bool)) types.Verifier {
		return &std.Verifier{Journal: journal, Specs: lookup}
	}
}

func TestVerifyResume(t *testing.T) {
	ctx := func(audit stores.AuditLog) context.Context {
		return verifyResumeCtx(audit)
	}

	t.Run("resume reconciles unknown entries", func(t *testing.T) {
		var verifyCalls int
		f := newVerifyResumeFixture(t, true, stores.NewMemoryJournal(), nil,
			settleWith(types.Succeeded, nil), stdVerifier())
		conv := f.conversation(t)
		runCtx := ctx(f.audit)
		token := f.sendUntilAsk(t, conv, runCtx)
		f.book.verify = settleWith(types.Succeeded, &verifyCalls)
		done, rerr := resumeCollect(t, conv, runCtx, token)
		if rerr != nil {
			t.Fatalf("Resume: %v", rerr)
		}
		if len(done.Uncertain) != 0 {
			t.Fatalf("Done.Uncertain = %v, want empty", done.Uncertain)
		}
		if f.book.execs != 1 {
			t.Fatalf("book executed %d times, want 1", f.book.execs)
		}
		if f.gate.execs != 1 {
			t.Fatalf("gate executed %d times, want 1", f.gate.execs)
		}
		if entry := f.bookEntry(t, runCtx); entry.Result.Outcome != types.Succeeded {
			t.Fatalf("journal outcome = %v, want Succeeded", entry.Result.Outcome)
		}
		if verifyCalls == 0 {
			t.Fatal("Verify never ran on resume")
		}
		if !f.modelSawOutcome(types.Succeeded) {
			t.Fatal("model never saw the reconciled Succeeded result")
		}
		if !f.auditHasBookOutcome(types.Succeeded) {
			t.Fatalf("audit records = %+v, want a reconciled book outcome", f.audit.records)
		}
	})

	t.Run("resume keeps uncertainty when verify errors", func(t *testing.T) {
		f := newVerifyResumeFixture(t, true, stores.NewMemoryJournal(), nil,
			settleWith(types.Succeeded, nil), stdVerifier())
		conv := f.conversation(t)
		runCtx := ctx(f.audit)
		token := f.sendUntilAsk(t, conv, runCtx)
		f.book.verify = func(context.Context, json.RawMessage, types.ToolResult) (types.Outcome, error) {
			return types.Unknown, errors.New("verifier down")
		}
		done, rerr := resumeCollect(t, conv, runCtx, token)
		if rerr != nil {
			t.Fatalf("Resume: %v", rerr)
		}
		if !uncertainHas(done.Uncertain, f.bookCallID(t)) {
			t.Fatalf("Done.Uncertain = %v, want the book key", done.Uncertain)
		}
		if entry := f.bookEntry(t, runCtx); entry.Result.Outcome != types.Unknown {
			t.Fatalf("journal outcome = %v, want the entry preserved Unknown", entry.Result.Outcome)
		}
		if f.book.execs != 1 {
			t.Fatalf("book executed %d times, want 1", f.book.execs)
		}
	})

	t.Run("resume keeps uncertainty when verify is absent", func(t *testing.T) {
		f := newVerifyResumeFixture(t, true, stores.NewMemoryJournal(), nil, nil, stdVerifier())
		conv := f.conversation(t)
		runCtx := ctx(f.audit)
		token := f.sendUntilAsk(t, conv, runCtx)
		done, rerr := resumeCollect(t, conv, runCtx, token)
		if rerr != nil {
			t.Fatalf("Resume: %v", rerr)
		}
		if !uncertainHas(done.Uncertain, f.bookCallID(t)) {
			t.Fatalf("Done.Uncertain = %v, want the book key", done.Uncertain)
		}
		if entry := f.bookEntry(t, runCtx); entry.Result.Outcome != types.Unknown {
			t.Fatalf("journal outcome = %v, want the entry preserved Unknown", entry.Result.Outcome)
		}
	})

	t.Run("resume fails when journal enumeration fails", func(t *testing.T) {
		journal := &flakyJournal{Journal: stores.NewMemoryJournal(), failsOn: func(call int) bool { return call == 2 }}
		f := newVerifyResumeFixture(t, true, journal, nil,
			settleWith(types.Succeeded, nil), stdVerifier())
		conv := f.conversation(t)
		runCtx := ctx(f.audit)
		token := f.sendUntilAsk(t, conv, runCtx)
		_, rerr := resumeCollect(t, conv, runCtx, token)
		if rerr == nil || !strings.Contains(rerr.Error(), "enumerate unknown journal entries") {
			t.Fatalf("Resume error = %v, want the journal enumeration failure", rerr)
		}
	})

}

func TestVerifyDone(t *testing.T) {
	ctx := func(audit stores.AuditLog) context.Context {
		return verifyResumeCtx(audit)
	}

	t.Run("done clears durably reconciled keys", func(t *testing.T) {
		var verifyCalls int
		f := newVerifyResumeFixture(t, true, stores.NewMemoryJournal(), nil,
			settleWith(types.Succeeded, nil), stdVerifier())
		conv := f.conversation(t)
		runCtx := ctx(f.audit)
		token := f.sendUntilAsk(t, conv, runCtx)
		f.book.verify = func(context.Context, json.RawMessage, types.ToolResult) (types.Outcome, error) {
			verifyCalls++
			if verifyCalls == 1 {
				return types.Unknown, errors.New("verify not ready")
			}
			return types.Succeeded, nil
		}
		done, rerr := resumeCollect(t, conv, runCtx, token)
		if rerr != nil {
			t.Fatalf("Resume: %v", rerr)
		}
		if len(done.Uncertain) != 0 {
			t.Fatalf("Done.Uncertain = %v, want the settled key cleared", done.Uncertain)
		}
		if entry := f.bookEntry(t, runCtx); entry.Result.Outcome != types.Succeeded {
			t.Fatalf("journal outcome = %v, want Succeeded", entry.Result.Outcome)
		}
		if !f.auditHasBookOutcome(types.Succeeded) {
			t.Fatalf("audit records = %+v, want a reconciled book outcome", f.audit.records)
		}
		if f.book.execs != 1 {
			t.Fatalf("book executed %d times, want 1", f.book.execs)
		}
	})

	t.Run("done keeps unsettled keys when verify is absent", func(t *testing.T) {
		f := newVerifyResumeFixture(t, true, stores.NewMemoryJournal(), nil, nil, stdVerifier())
		conv := f.conversation(t)
		runCtx := ctx(f.audit)
		token := f.sendUntilAsk(t, conv, runCtx)
		done, rerr := resumeCollect(t, conv, runCtx, token)
		if rerr != nil {
			t.Fatalf("Resume: %v", rerr)
		}
		if !uncertainHas(done.Uncertain, f.bookCallID(t)) {
			t.Fatalf("Done.Uncertain = %v, want the unsettled book key", done.Uncertain)
		}
		if entry := f.bookEntry(t, runCtx); entry.Result.Outcome != types.Unknown {
			t.Fatalf("journal outcome = %v, want the entry preserved Unknown", entry.Result.Outcome)
		}
	})

	t.Run("done fails when journal enumeration fails", func(t *testing.T) {
		journal := &flakyJournal{Journal: stores.NewMemoryJournal(), failsOn: func(call int) bool { return call == 3 }}
		f := newVerifyResumeFixture(t, true, journal, nil, nil, stdVerifier())
		conv := f.conversation(t)
		runCtx := ctx(f.audit)
		token := f.sendUntilAsk(t, conv, runCtx)
		_, rerr := resumeCollect(t, conv, runCtx, token)
		if rerr == nil || !strings.Contains(rerr.Error(), "enumerate unknown journal entries") {
			t.Fatalf("Resume error = %v, want the before-Done enumeration failure", rerr)
		}
	})
}
