package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/permission"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

var (
	authOwner = types.Principal{Tenant: "acme", Subject: "alice"}
	authOp    = types.Principal{
		Tenant: "acme", Subject: "op",
		Scopes: []string{types.ScopeSessionRead, types.ScopeSessionWrite, "approve:tool"},
	}
	authForeign = types.Principal{Tenant: "other", Subject: "mallory"}
)

type authPolicySource struct {
	scope    string
	quorum   int
	separate bool
	err      error
	mu       sync.Mutex
	calls    int
}

func (s *authPolicySource) ApprovalPolicy(context.Context, types.RiskTier, string, bool) (permission.ApprovalPolicy, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	if s.err != nil {
		return permission.ApprovalPolicy{}, s.err
	}
	q := s.quorum
	if q == 0 {
		q = 1
	}
	scope := s.scope
	if scope == "" {
		scope = "approve:tool"
	}
	return permission.ApprovalPolicy{Scope: scope, Quorum: q, SeparateFromOriginator: s.separate}, nil
}

type authCreds struct{ err error }

func (c *authCreds) Credentials(context.Context, types.Principal) (types.Credential, error) {
	if c.err != nil {
		return types.Credential{}, c.err
	}
	return types.Credential{Token: "fresh"}, nil
}

type authFailsafeCreds struct{ calls int }

func (c *authFailsafeCreds) Credentials(context.Context, types.Principal) (types.Credential, error) {
	c.calls++
	return types.Credential{Token: "fresh"}, nil
}

// authHarness seeds a suspended HumanApproval run through public store
// operations: a session owned by alice, a run Start/Suspend pair and a
// versioned approval checkpoint.
type authHarness struct {
	cps  *stores.MemoryCheckpoints
	log  *stores.MemorySessionLog
	runs *stores.MemoryRuns
	rt   *resumeRT
}

func newAuthHarness(t *testing.T) *authHarness {
	t.Helper()
	h := &authHarness{
		cps:  stores.NewMemoryCheckpoints(),
		log:  stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
		runs: stores.NewMemoryRuns(),
		rt:   &resumeRT{},
	}
	h.rt.steps = []func(context.Context, runtime.State) (runtime.State, []types.Event, runtime.Status, error){
		func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
			return st, nil, runtime.DoneStatus, nil
		},
	}
	if _, err := h.log.Append(types.WithPrincipal(context.Background(), authOwner), "s1", 0, types.Message{
		Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "book it"}},
	}); err != nil {
		t.Fatal(err)
	}
	return h
}

func defaultEligibility(quorum int) permission.Eligibility {
	return permission.Eligibility{Scopes: []string{"approve:tool"}, Quorum: quorum}
}

func (h *authHarness) seed(t *testing.T, runID string, reason types.SuspendReason, st runtime.State, approvals []checkpointApproval) types.ResumeToken {
	t.Helper()
	env := checkpointEnvelope{
		Run:        types.RunInfo{Flow: "flights", SessionID: "s1", RunID: runID},
		Generation: 1,
		State:      st,
		Approvals:  approvals,
	}
	data, err := encodeCheckpoint(env)
	if err != nil {
		t.Fatal(err)
	}
	token, err := h.cps.Put(types.WithPrincipal(context.Background(), authOwner), stores.Checkpoint{
		RunID:         runID,
		SchemaVersion: stores.CurrentSchemaVersion,
		SessionID:     "s1",
		Flow:          "flights",
		Backend:       h.rt.Name(),
		Reason:        reason,
		Originator:    authOwner,
		Data:          data,
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := h.runs.Start(types.WithPrincipal(context.Background(), authOwner), stores.Run{
		SessionID: "s1", RunID: runID, Flow: "flights", Backend: h.rt.Name(),
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.runs.Suspend(types.WithPrincipal(context.Background(), authOwner), lease, token); err != nil {
		t.Fatal(err)
	}
	return token
}

func (h *authHarness) token(t *testing.T, quorum int) types.ResumeToken {
	t.Helper()
	approval := checkpointApproval{
		Call:        types.ToolUse{ID: "call-1", Name: "book", Args: json.RawMessage(`{"n":1}`)},
		Risk:        types.RiskHigh,
		Fingerprint: "fp-call-1",
		Eligible:    permission.Eligibility{Scopes: []string{"approve:tool"}, Quorum: quorum},
	}
	st := runtime.State{Turn: 1, HistoryVersion: 1, Pending: []types.ToolUse{approval.Call}}
	return h.seed(t, "run-1", types.HumanApproval, st, []checkpointApproval{approval})
}

func (h *authHarness) conv(t *testing.T, src permission.ApprovalPolicySource, creds types.CredentialSource) Conversation {
	t.Helper()
	stack := &Stack{stores: stores.Stores{SessionLog: h.log}}
	opts := []ConversationOption{
		WithConversationRuns(h.runs),
		WithConversationEventLog(stores.NewMemoryEventLog()),
		WithConversationCheckpoints(h.cps),
		WithConversationCredentialSource(creds),
	}
	if src != nil {
		opts = append(opts, WithConversationApprovalPolicy(src))
	}
	conv, err := NewConversation(stack, "flights", h.rt, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return conv
}

func (h *authHarness) errors(t *testing.T, seq iter.Seq2[Event, error]) []error {
	t.Helper()
	var errs []error
	for ev, err := range seq {
		if err != nil {
			errs = append(errs, err)
		}
		_ = ev
	}
	return errs
}

func TestResumeAuthority(t *testing.T) {
	ctx := context.Background()

	t.Run("session owner mismatch is refused", func(t *testing.T) {
		h := newAuthHarness(t)
		token := h.token(t, 1)
		src := &authPolicySource{}
		conv := h.conv(t, src, &authCreds{})
		errs := h.errors(t, conv.Resume(types.WithPrincipal(ctx, authForeign), token, Approve()))
		if len(errs) == 0 || !errors.Is(errs[0], types.ErrSessionForbidden) {
			t.Fatalf("cross-tenant resume: got %v, want ErrSessionForbidden", errs)
		}
		if _, err := h.cps.Peek(ctx, token); err != nil {
			t.Fatalf("refused resume consumed the token: %v", err)
		}
		errs = h.errors(t, conv.Resume(types.WithPrincipal(ctx, authOp), token, Approve()))
		if len(errs) != 0 {
			t.Fatalf("owner-approved resume after refusal: got %v", errs)
		}
		if h.rt.i != 1 {
			t.Errorf("tool executions: got %d, want 1", h.rt.i)
		}
	})

	t.Run("read scope alone is refused", func(t *testing.T) {
		h := newAuthHarness(t)
		token := h.token(t, 1)
		conv := h.conv(t, &authPolicySource{}, &authCreds{})
		reader := types.Principal{
			Tenant: "acme", Subject: "peer", Scopes: []string{types.ScopeSessionRead},
		}
		errs := h.errors(t, conv.Resume(types.WithPrincipal(ctx, reader), token, Approve()))
		if len(errs) == 0 || !errors.Is(errs[0], types.ErrSessionForbidden) {
			t.Fatalf("read-only resume: got %v, want ErrSessionForbidden", errs)
		}
		if _, err := h.cps.Peek(ctx, token); err != nil {
			t.Fatalf("refused resume consumed the token: %v", err)
		}
	})

	t.Run("permission.ineligible-keeps-token", func(t *testing.T) {
		h := newAuthHarness(t)
		token := h.token(t, 1)
		conv := h.conv(t, &authPolicySource{}, &authCreds{})
		unscoped := authOp
		unscoped.Scopes = []string{types.ScopeSessionRead, types.ScopeSessionWrite}
		errs := h.errors(t, conv.Resume(types.WithPrincipal(ctx, unscoped), token, Approve()))
		if len(errs) == 0 || !errors.Is(errs[0], types.ErrApproverNotEligible) {
			t.Fatalf("unscoped approver: got %v, want ErrApproverNotEligible", errs)
		}
		if _, err := h.cps.Peek(ctx, token); err != nil {
			t.Fatalf("refused approval consumed the token: %v", err)
		}
		errs = h.errors(t, conv.Resume(types.WithPrincipal(ctx, authOp), token, Approve()))
		if len(errs) != 0 {
			t.Fatalf("eligible approval after refusal: got %v", errs)
		}
	})

	t.Run("missing policy source is refused", func(t *testing.T) {
		h := newAuthHarness(t)
		token := h.token(t, 1)
		conv := h.conv(t, nil, &authCreds{})
		errs := h.errors(t, conv.Resume(types.WithPrincipal(ctx, authOp), token, Approve()))
		if len(errs) == 0 || !errors.Is(errs[0], types.ErrApproverNotEligible) {
			t.Fatalf("missing policy: got %v, want ErrApproverNotEligible", errs)
		}
		if _, err := h.cps.Peek(ctx, token); err != nil {
			t.Fatalf("refused approval consumed the token: %v", err)
		}
	})

	t.Run("permission.self-approval-refused-high-risk", func(t *testing.T) {
		h := newAuthHarness(t)
		token := h.token(t, 1)
		src := &authPolicySource{separate: true}
		conv := h.conv(t, src, &authCreds{})
		errs := h.errors(t, conv.Resume(types.WithPrincipal(ctx, authOwner), token, Approve()))
		if len(errs) == 0 || !errors.Is(errs[0], types.ErrApproverNotEligible) {
			t.Fatalf("self approval: got %v, want ErrApproverNotEligible", errs)
		}
		if _, err := h.cps.Peek(ctx, token); err != nil {
			t.Fatalf("refused approval consumed the token: %v", err)
		}
	})

	t.Run("policy error is refused", func(t *testing.T) {
		h := newAuthHarness(t)
		token := h.token(t, 1)
		src := &authPolicySource{err: errors.New("policy store down")}
		conv := h.conv(t, src, &authCreds{})
		errs := h.errors(t, conv.Resume(types.WithPrincipal(ctx, authOp), token, Approve()))
		if len(errs) == 0 || !errors.Is(errs[0], types.ErrApproverNotEligible) {
			t.Fatalf("policy error: got %v, want ErrApproverNotEligible", errs)
		}
		if _, err := h.cps.Peek(ctx, token); err != nil {
			t.Fatalf("refused approval consumed the token: %v", err)
		}
	})

	t.Run("suspension.approve-on-another-pod", func(t *testing.T) {
		h := newAuthHarness(t)
		token := h.token(t, 2)
		src := &authPolicySource{quorum: 2}
		creds := &authFailsafeCreds{}
		conv := h.conv(t, src, creds)
		errs := h.errors(t, conv.Resume(types.WithPrincipal(ctx, authOp), token, Approve()))
		if len(errs) != 0 {
			t.Fatalf("partial approval: got %v, want a recorded pending approval", errs)
		}
		if h.rt.i != 0 {
			t.Errorf("partial approval executed the run: %d steps", h.rt.i)
		}
		cp, err := h.cps.Peek(ctx, token)
		if err != nil {
			t.Fatalf("pending token after partial approval: %v", err)
		}
		env, _, err := decodeCheckpoint(cp, h.rt, "flights")
		if err != nil {
			t.Fatal(err)
		}
		if len(env.Approvals) != 1 || len(env.Approvals[0].ApprovedBy) != 1 ||
			env.Approvals[0].ApprovedBy[0].Subject != authOp.Subject {
			t.Fatalf("recorded approvers: %+v, want op", env.Approvals)
		}

		// The second approval arrives on a fresh Conversation: the quorum
		// completes, the run executes and the receipt records both subjects.
		op2 := types.Principal{
			Tenant: "acme", Subject: "op2",
			Scopes: []string{types.ScopeSessionRead, types.ScopeSessionWrite, "approve:tool"},
		}
		errs = h.errors(t, h.conv(t, src, creds).Resume(types.WithPrincipal(ctx, op2), token, Approve()))
		if len(errs) != 0 {
			t.Fatalf("second approval: got %v", errs)
		}
		if h.rt.i != 1 {
			t.Errorf("tool executions after quorum: got %d, want 1", h.rt.i)
		}
		hst, err := h.log.Load(types.WithPrincipal(ctx, authOp), "s1")
		if err != nil {
			t.Fatal(err)
		}
		receipts := 0
		for _, msg := range hst.Messages {
			if raw, ok := msg.Meta[ApprovalReceiptKey]; ok {
				rc, err := decodeReceipt(raw)
				if err != nil {
					t.Fatal(err)
				}
				receipts++
				if len(rc.Approvers) != 2 {
					t.Errorf("receipt approvers: %+v, want op and op2", rc.Approvers)
				}
			}
		}
		if receipts != 1 {
			t.Errorf("receipts appended: %d, want 1", receipts)
		}
	})

	t.Run("duplicate subject is refused", func(t *testing.T) {
		h := newAuthHarness(t)
		token := h.token(t, 2)
		src := &authPolicySource{quorum: 2}
		conv := h.conv(t, src, &authCreds{})
		if errs := h.errors(t, conv.Resume(types.WithPrincipal(ctx, authOp), token, Approve())); len(errs) != 0 {
			t.Fatalf("first approval: got %v", errs)
		}
		errs := h.errors(t, conv.Resume(types.WithPrincipal(ctx, authOp), token, Approve()))
		if len(errs) == 0 || !errors.Is(errs[0], types.ErrApproverNotEligible) {
			t.Fatalf("duplicate subject: got %v, want ErrApproverNotEligible", errs)
		}
		cp, err := h.cps.Peek(ctx, token)
		if err != nil {
			t.Fatal(err)
		}
		env, _, err := decodeCheckpoint(cp, h.rt, "flights")
		if err != nil {
			t.Fatal(err)
		}
		if len(env.Approvals[0].ApprovedBy) != 1 {
			t.Fatalf("approvers after duplicate: %+v, want one entry", env.Approvals[0].ApprovedBy)
		}
	})

	t.Run("rejection renders the exact marker", func(t *testing.T) {
		h := newAuthHarness(t)
		token := h.token(t, 1)
		conv := h.conv(t, &authPolicySource{}, &authCreds{})
		if errs := h.errors(t, conv.Resume(types.WithPrincipal(ctx, authOp), token, Reject("too expensive"))); len(errs) != 0 {
			t.Fatalf("reject: got %v", errs)
		}
		hst, err := h.log.Load(types.WithPrincipal(ctx, authOp), "s1")
		if err != nil {
			t.Fatal(err)
		}
		last := hst.Messages[len(hst.Messages)-1]
		res, ok := last.Blocks[len(last.Blocks)-1].(types.ToolResult)
		if !ok || res.Outcome != types.Failed || res.Error == nil {
			t.Fatalf("reject result: got %+v, want a failed tool result", last.Blocks)
		}
		if res.Error.Kind != types.Permanent {
			t.Errorf("reject kind: got %v, want Permanent", res.Error.Kind)
		}
		want := runtime.NotExecutedPrefix + "rejected by " + authOp.Subject
		if res.Error.Message != want {
			t.Errorf("reject message: got %q, want %q", res.Error.Message, want)
		}
	})

	t.Run("credential error causes no effects", func(t *testing.T) {
		h := newAuthHarness(t)
		token := h.token(t, 1)
		conv := h.conv(t, &authPolicySource{}, &authCreds{err: errors.New("idp down")})
		errs := h.errors(t, conv.Resume(types.WithPrincipal(ctx, authOp), token, Approve()))
		if len(errs) == 0 {
			t.Fatal("credential failure: got no error")
		}
		if _, err := h.cps.Peek(ctx, token); err != nil {
			t.Fatalf("failed resume consumed the token: %v", err)
		}
		if h.rt.i != 0 {
			t.Errorf("credential failure executed %d steps", h.rt.i)
		}
		hst, err := h.log.Load(types.WithPrincipal(ctx, authOp), "s1")
		if err != nil {
			t.Fatal(err)
		}
		if len(hst.Messages) != 1 {
			t.Errorf("credential failure appended history: %d messages, want 1", len(hst.Messages))
		}
	})

	t.Run("concurrent resume has one winner", func(t *testing.T) {
		h := newAuthHarness(t)
		token := h.token(t, 1)
		conv := h.conv(t, &authPolicySource{}, &authCreds{})
		var wg sync.WaitGroup
		results := make([]error, 2)
		for i := range 2 {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				for _, err := range conv.Resume(types.WithPrincipal(ctx, authOp), token, Approve()) {
					if err != nil {
						results[i] = err
					}
				}
			}(i)
		}
		wg.Wait()
		winners := 0
		for i := range results {
			if results[i] == nil {
				winners++
			} else if !errors.Is(results[i], types.ErrTokenConsumed) {
				t.Errorf("loser %d: got %v, want ErrTokenConsumed", i, results[i])
			}
		}
		if winners != 1 {
			t.Errorf("winners: %d, want 1", winners)
		}
		if h.rt.i != 1 {
			t.Errorf("tool executions: got %d, want 1", h.rt.i)
		}
	})

	t.Run("identity.approver-from-transport-only", func(t *testing.T) {
		h := newAuthHarness(t)
		token := h.token(t, 1)
		conv := h.conv(t, &authPolicySource{}, &authCreds{})
		in := Approve()
		in.Approver = &authForeign
		errs := h.errors(t, conv.Resume(types.WithPrincipal(ctx, authOp), token, in))
		if len(errs) != 0 {
			t.Fatalf("resume with forged approver: got %v", errs)
		}
		hst, err := h.log.Load(types.WithPrincipal(ctx, authOp), "s1")
		if err != nil {
			t.Fatal(err)
		}
		for _, msg := range hst.Messages {
			if raw, ok := msg.Meta[ApprovalReceiptKey]; ok {
				rc, err := decodeReceipt(raw)
				if err != nil {
					t.Fatal(err)
				}
				if len(rc.Approvers) != 1 || rc.Approvers[0].Subject != authOp.Subject {
					t.Errorf("receipt approvers: %+v, want the transport principal", rc.Approvers)
				}
			}
		}
	})
}

// TestApprovalEnvelopeForged drives a HumanApproval checkpoint whose envelope
// carries no approvals: every approval verdict is refused before any effect
// and the token stays pending.
func TestApprovalEnvelopeForged(t *testing.T) {
	ctx := context.Background()

	t.Run("suspension.approve-on-another-pod", func(t *testing.T) {
		h := newAuthHarness(t)
		st := runtime.State{Turn: 1, HistoryVersion: 1,
			Pending: []types.ToolUse{{ID: "call-1", Name: "book", Args: json.RawMessage(`{"n":1}`)}}}
		token := h.seed(t, "run-1", types.HumanApproval, st, nil)
		conv := h.conv(t, &authPolicySource{}, &authCreds{})
		errs := h.errors(t, conv.Resume(types.WithPrincipal(ctx, authOp), token, Approve()))
		if len(errs) == 0 || !errors.Is(errs[0], types.ErrCheckpointIncompatible) {
			t.Fatalf("forged envelope: got %v, want ErrCheckpointIncompatible", errs)
		}
		if h.rt.i != 0 {
			t.Errorf("forged envelope executed %d steps", h.rt.i)
		}
		if _, err := h.cps.Peek(ctx, token); err != nil {
			t.Fatalf("refused resume consumed the token: %v", err)
		}
	})
}
