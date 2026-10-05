package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// recordingCreds captures the principals credential resolution runs for.
type recordingCreds struct {
	mu  sync.Mutex
	got []types.Principal
	err error
}

func (r *recordingCreds) Credentials(_ context.Context, p types.Principal) (types.Credential, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, p)
	if r.err != nil {
		return types.Credential{}, r.err
	}
	return types.Credential{}, nil
}

func (r *recordingCreds) principals() []types.Principal {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]types.Principal(nil), r.got...)
}

func TestRecoveryAuthority(t *testing.T) {
	// reaperReadCtx scopes the reaper with session read, so the owner
	// lookup can resolve a session it does not own.
	reaperReadCtx := func() context.Context {
		return WithPrincipal(context.Background(), types.Principal{
			Subject: "reaper", Tenant: "t1", Scopes: []string{types.ScopeSessionRead},
		})
	}
	seedSession := func(t *testing.T, f *recoverFixture, owner types.Principal) {
		t.Helper()
		ownerCtx := WithPrincipal(context.Background(), owner)
		if _, err := f.log.Append(ownerCtx, "s1", 0, types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "m"}}}); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("stale run executes as the session owner", func(t *testing.T) {
		f := newRecoverFixture(t, "agent")
		creds := &recordingCreds{}
		st, err := Build(
			WithStores(stores.Stores{SessionLog: f.log, Runs: f.runs, Checkpoints: f.cps, Journal: f.jnl}),
			WithRecoveryRuntime("agent", f.rt),
			WithCredentialSource(creds),
		)
		if err != nil {
			t.Fatal(err)
		}
		seedSession(t, f, types.Principal{Subject: "u1", Tenant: "t1"})
		f.seedRun(t, stores.Run{SessionID: "s1", RunID: "r1", Flow: "agent", State: stores.Running, Turn: 1, Seq: 1})
		f.expire()

		if err := st.Recover(reaperReadCtx(), 10); err != nil {
			t.Fatal(err)
		}
		owner := types.Principal{Tenant: "t1", Subject: "u1"}
		got := creds.principals()
		if len(got) != 1 || got[0].Subject != owner.Subject || got[0].Tenant != owner.Tenant {
			t.Fatalf("credential source saw %+v, want the session owner %v", got, owner)
		}
		infos := f.rt.infos()
		if len(infos) != 1 || infos[0].Principal.Subject != owner.Subject || infos[0].Principal.Tenant != owner.Tenant {
			t.Fatalf("run principal = %+v, want the session owner %v", infos, owner)
		}
	})

	t.Run("checkpoint originator outranks the session owner", func(t *testing.T) {
		f := newRecoverFixture(t, "agent")
		creds := &recordingCreds{}
		st, err := Build(
			WithStores(stores.Stores{SessionLog: f.log, Runs: f.runs, Checkpoints: f.cps, Journal: f.jnl}),
			WithRecoveryRuntime("agent", f.rt),
			WithCredentialSource(creds),
		)
		if err != nil {
			t.Fatal(err)
		}
		// The session belongs to u2; only the checkpoint's originator u1
		// (holding session write so the re-drive may append) may win.
		seedSession(t, f, types.Principal{Subject: "u2", Tenant: "t1"})
		lease, err := f.runs.Start(t.Context(), stores.Run{
			SessionID: "s1", RunID: "pre1", Flow: "agent", State: stores.Running, Turn: 1, Seq: 1,
		}, stores.LeaseTTL)
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(runtime.State{Turn: 1, HistoryVersion: 1})
		if err != nil {
			t.Fatal(err)
		}
		tokCtx := types.WithRunInfo(t.Context(), types.RunInfo{SessionID: "s1", RunID: "pre1", Flow: "agent"})
		tok, err := f.cps.Put(tokCtx, stores.Checkpoint{
			RunID: "pre1", SessionID: "s1", Flow: "agent", Reason: types.Preempted,
			Originator: types.Principal{Subject: "u1", Tenant: "t1", Scopes: []string{types.ScopeSessionWrite}},
			Data:       data,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.runs.Suspend(t.Context(), lease, tok); err != nil {
			t.Fatal(err)
		}

		if err := st.Recover(reaperReadCtx(), 10); err != nil {
			t.Fatal(err)
		}
		want := types.Principal{Tenant: "t1", Subject: "u1"}
		got := creds.principals()
		if len(got) != 1 || got[0].Subject != want.Subject || got[0].Tenant != want.Tenant {
			t.Fatalf("credential source saw %+v, want the checkpoint originator %v", got, want)
		}
		infos := f.rt.infos()
		if len(infos) != 1 || infos[0].Principal.Subject != want.Subject || infos[0].Principal.Tenant != want.Tenant {
			t.Fatalf("run principal = %+v, want the checkpoint originator %v", infos, want)
		}
	})

	t.Run("credential failure fails the run without calls", func(t *testing.T) {
		f := newRecoverFixture(t, "agent")
		sealed := errors.New("vault sealed")
		creds := &recordingCreds{err: sealed}
		st, err := Build(
			WithStores(stores.Stores{SessionLog: f.log, Runs: f.runs, Checkpoints: f.cps, Journal: f.jnl}),
			WithRecoveryRuntime("agent", f.rt),
			WithCredentialSource(creds),
		)
		if err != nil {
			t.Fatal(err)
		}
		f.seedHistory(t, 1)
		f.seedRun(t, stores.Run{SessionID: "s1", RunID: "r1", Flow: "agent", State: stores.Running, Turn: 1, Seq: 1})
		f.expire()

		if err := st.Recover(f.reaperCtx(), 10); !errors.Is(err, sealed) {
			t.Fatalf("recover error = %v, want the credential failure", err)
		}
		if f.rt.modelCalls() != 0 {
			t.Fatalf("model calls = %d, want 0", f.rt.modelCalls())
		}
		exec, _ := f.rt.calls()
		if len(exec) != 0 {
			t.Fatalf("tool calls executed = %v, want none", exec)
		}
		final, err := f.runs.ByID(t.Context(), "r1")
		if err != nil {
			t.Fatal(err)
		}
		if final.State != stores.Failed {
			t.Fatalf("run state = %v, want Failed", final.State)
		}
	})
}
