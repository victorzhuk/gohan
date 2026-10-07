package gohan

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/types"
)

type captureFlow[In, Out any] struct {
	see func(ctx context.Context, in In) Out
}

const (
	callerTok = "caller-token"
	staleTok  = "stale-token"
)

func (f captureFlow[In, Out]) Invoke(ctx context.Context, in In) (Out, error) {
	return f.see(ctx, in), nil
}

func subflowStrip(t *testing.T) context.Context {
	t.Helper()
	ctx := context.Background()
	ctx = WithPrincipal(ctx, types.Principal{Tenant: "t-a", Subject: "u1"})
	ctx = WithCredential(ctx, types.Credential{Token: callerTok})
	ctx = WithIdempotencyKey(ctx, "op-42")
	ctx = types.WithApproval(ctx, types.Approval{Approver: types.Principal{Tenant: "t-a", Subject: "ops"}})
	ctx = types.WithRunInfo(ctx, types.RunInfo{RunID: "root", RootRunID: "root"})

	var gotKey, gotApproval bool
	child := FlowTool("child", captureFlow[string, string]{see: func(ctx context.Context, in string) string {
		_, gotKey = IdempotencyKey(ctx)
		_, gotApproval = types.ApprovalFrom(ctx)
		return in
	}})
	out, err := child.Invoke(ctx, "x")
	if err != nil {
		t.Fatalf("sub-flow invoke: %v", err)
	}
	if out != "x" {
		t.Fatalf("out = %q, want x", out)
	}
	if gotKey {
		t.Fatal("sub-flow inherited the caller's idempotency key")
	}
	if gotApproval {
		t.Fatal("sub-flow inherited the caller's approval")
	}
	return ctx
}

func TestFlowToolDropsKey(t *testing.T) {
	ctx := subflowStrip(t)
	if _, ok := IdempotencyKey(ctx); !ok {
		t.Fatal("strip mutated the caller's context")
	}
}

type reissueCreds struct {
	mu    sync.Mutex
	calls []types.Principal
	tok   string
}

func (c *reissueCreds) Credentials(_ context.Context, p types.Principal) (types.Credential, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, p)
	c.tok = "fresh-" + p.Subject
	return types.Credential{Token: c.tok, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (c *reissueCreds) issued() ([]types.Principal, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls, c.tok
}

type identityRT struct {
	mu      sync.Mutex
	entered chan struct{}
	gate    chan struct{}
	observe func(ctx context.Context)
	calls   int
}

func (r *identityRT) Name() string                         { return "identity.test" }
func (r *identityRT) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

func (r *identityRT) Start(context.Context, runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}

func (r *identityRT) Step(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	r.mu.Lock()
	r.calls++
	call := r.calls
	r.mu.Unlock()
	if call == 1 {
		if r.observe != nil {
			r.observe(ctx)
		}
		if r.entered != nil {
			close(r.entered)
		}
		return st, []types.Event{types.TextDelta{Delta: "d0"}}, runtime.Continue, nil
	}
	if r.gate != nil {
		<-r.gate
	}
	return st, nil, runtime.DoneStatus, nil
}

type detachedObs struct {
	principal   types.Principal
	principalOK bool
	cred        types.Credential
	credOK      bool
	key         string
	keyOK       bool
	info        types.RunInfo
	infoOK      bool
}

func runDetachedReissue(t *testing.T) (*reissueCreds, detachedObs) {
	t.Helper()
	src := &reissueCreds{}
	rt := &identityRT{entered: make(chan struct{}), gate: make(chan struct{})}
	var obs detachedObs
	rt.observe = func(ctx context.Context) {
		obs.principal, obs.principalOK = PrincipalFrom(ctx)
		obs.cred, obs.credOK = CredentialFrom(ctx)
		obs.key, obs.keyOK = IdempotencyKey(ctx)
		obs.info, obs.infoOK = RunInfoFrom(ctx)
	}
	conv := streamSetup(t, rt, Detached(), WithConversationCredentialSource(src))
	caller := principalCtx(context.Background())
	caller = WithCredential(caller, types.Credential{Token: staleTok, ExpiresAt: time.Now().Add(-time.Minute)})
	caller = WithIdempotencyKey(caller, "op-42")

	start := make(chan streamResult, 1)
	go func() { start <- collectStream(conv.Send(caller, "s1", userMsg("hi"))) }()
	got := <-start
	if got.err != nil || len(got.evs) != 0 {
		t.Fatalf("detached send = %+v, want no events and no error", got)
	}
	<-rt.entered
	close(rt.gate)
	waitFor(t, "detached run finished", func() bool {
		return lenEvents(t, conv, liveRunID(conv, "s1")) >= 1
	})

	calls, tok := src.issued()
	want := types.Principal{Tenant: "t-a", Subject: "u1"}
	if len(calls) != 1 || calls[0].Tenant != want.Tenant || calls[0].Subject != want.Subject {
		t.Fatalf("credential source calls = %+v, want exactly one for %+v", calls, want)
	}
	if !obs.credOK || obs.cred.Token != tok || tok == staleTok {
		t.Fatalf("detached credential = %+v, %v; want the freshly issued %q", obs.cred, obs.credOK, tok)
	}
	if !obs.principalOK || obs.principal.Tenant != want.Tenant || obs.principal.Subject != want.Subject {
		t.Fatalf("detached principal = %+v, %v; want %+v", obs.principal, obs.principalOK, want)
	}
	if !obs.keyOK || obs.key != "op-42" {
		t.Fatalf("detached idempotency key = %q, %v; want op-42", obs.key, obs.keyOK)
	}
	if !obs.infoOK {
		t.Fatal("detached run info missing")
	}
	return src, obs
}

func TestDetachedCredentialReissue(t *testing.T) {
	_, obs := runDetachedReissue(t)
	if obs.info.ParentRunID != "" || obs.info.RootRunID != obs.info.RunID {
		t.Fatalf("detached run info = %+v, want an independent root", obs.info)
	}
}

func TestIdentityDetach(t *testing.T) {
	t.Run("identity.idempotency-key-transport-only", func(t *testing.T) {
		if _, err := Build(); err != nil {
			t.Fatalf("build: %v", err)
		}
		subflowStrip(t)
	})

	t.Run("identity.detached-reissues-credential", func(t *testing.T) {
		runDetachedReissue(t)
	})
}
