package sandbox

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

type recordingWorkspace struct {
	fakeWorkspace
	stdout []byte
	envs   []map[string]string
	setErr error
}

func (w *recordingWorkspace) Exec(_ context.Context, req types.ExecRequest) (types.ExecResult, error) {
	w.execReqs = append(w.execReqs, req)
	return types.ExecResult{Stdout: w.stdout}, nil
}

func (w *recordingWorkspace) SetEnv(_ context.Context, env map[string]string) error {
	if w.setErr != nil {
		return w.setErr
	}
	w.envs = append(w.envs, env)
	return nil
}

type fakeResolver struct {
	values     map[string]string
	err        error
	calls      int
	names      []string
	principals []types.Principal
}

func (r *fakeResolver) Resolve(_ context.Context, p types.Principal, name string) (string, error) {
	r.calls++
	r.names = append(r.names, name)
	r.principals = append(r.principals, p)
	if r.err != nil {
		return "", r.err
	}
	return r.values[name], nil
}

type countTelemetry struct {
	counts map[string]int64
}

func newCountTelemetry() *countTelemetry {
	return &countTelemetry{counts: map[string]int64{}}
}

func (t *countTelemetry) StartSpan(ctx context.Context, _ string, _ ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}

func (t *countTelemetry) Count(_ context.Context, name string, n int64, _ ...types.Attr) {
	t.counts[name] += n
}

func (t *countTelemetry) Record(_ context.Context, _ string, _ float64, _ ...types.Attr) {}

func TestResolveSecrets(t *testing.T) {
	w := &recordingWorkspace{}
	res := &fakeResolver{values: map[string]string{
		"warehouse": "pg-pass-9f31",
		"queue":     "q-pass-77b2",
	}}
	policy := types.SandboxPolicy{Secrets: []types.SecretRef{
		{Name: "warehouse", Env: "PGPASSWORD"},
		{Name: "queue", Env: "QUEUE_URL"},
	}}
	ctx := types.WithSecrets(context.Background(), res)
	ctx = types.WithPrincipal(ctx, types.Principal{Subject: "u1", Tenant: "acme"})

	env, err := resolveSecrets(ctx, w, policy)
	if err != nil {
		t.Fatalf("resolveSecrets: %v", err)
	}
	want := map[string]string{"PGPASSWORD": "pg-pass-9f31", "QUEUE_URL": "q-pass-77b2"}
	if len(w.envs) != 1 {
		t.Fatalf("SetEnv calls = %d, want 1", len(w.envs))
	}
	if got := w.envs[0]; len(got) != len(want) || got["PGPASSWORD"] != want["PGPASSWORD"] || got["QUEUE_URL"] != want["QUEUE_URL"] {
		t.Errorf("injected env = %v, want %v", got, want)
	}
	if len(env) != 2 || env["PGPASSWORD"] != "pg-pass-9f31" {
		t.Errorf("returned env = %v, want the resolved values", env)
	}
	if res.calls != 2 {
		t.Errorf("resolve calls = %d, want 2", res.calls)
	}
	for i, p := range res.principals {
		if p.Subject != "u1" || p.Tenant != "acme" {
			t.Errorf("resolve %d principal = %+v, want the run originator", i, p)
		}
	}

	if _, err := resolveSecrets(ctx, w, types.SandboxPolicy{}); err != nil {
		t.Errorf("empty policy err = %v, want nil", err)
	}

	res2 := &fakeResolver{err: errors.New("vault down")}
	ctx2 := types.WithSecrets(context.Background(), res2)
	if _, err := resolveSecrets(ctx2, w, policy); err == nil || !strings.Contains(err.Error(), "vault down") {
		t.Errorf("resolver failure err = %v, want it to wrap the resolver error", err)
	}
}

func TestResolveSecretsWithoutResolver(t *testing.T) {
	w := &recordingWorkspace{}
	policy := types.SandboxPolicy{Secrets: []types.SecretRef{{Name: "warehouse", Env: "PGPASSWORD"}}}

	env, err := resolveSecrets(context.Background(), w, policy)
	if !errors.Is(err, errNoSecretResolver) {
		t.Fatalf("err = %v, want errNoSecretResolver", err)
	}
	if env != nil {
		t.Errorf("env = %v, want nil", env)
	}
	if len(w.envs) != 0 {
		t.Errorf("SetEnv calls = %d, want 0", len(w.envs))
	}
}

func TestSecretRedaction(t *testing.T) {
	tel := newCountTelemetry()
	ctx := types.WithTelemetry(context.Background(), tel)
	value := "pg-pass-9f31"
	token := regexp.MustCompile(`<gohan:secret:[0-9a-f]{24}>`)

	t.Run("block text replaced", func(t *testing.T) {
		blocks := []types.Block{types.Text{Text: "out: " + value + " done"}}
		got, err := redactSecrets(ctx, map[string]string{"PGPASSWORD": value}, blocks, nil)
		if err != nil {
			t.Fatalf("redactSecrets: %v", err)
		}
		text := got[0].(types.Text).Text
		if strings.Contains(text, value) {
			t.Errorf("text %q still carries the value", text)
		}
		if !token.MatchString(text) {
			t.Errorf("text %q carries no marker", text)
		}
		if tel.counts["gohan.sandbox.secret_leak"] != 1 {
			t.Errorf("leak counter = %d, want 1", tel.counts["gohan.sandbox.secret_leak"])
		}
	})

	t.Run("error text replaced", func(t *testing.T) {
		tel2 := newCountTelemetry()
		ctx2 := types.WithTelemetry(context.Background(), tel2)
		_, err := redactSecrets(ctx2, map[string]string{"PGPASSWORD": value}, nil, fmt.Errorf("exec: cat: %s: no such file", value))
		if err == nil || strings.Contains(err.Error(), value) {
			t.Fatalf("err = %v, want the value redacted", err)
		}
		if !token.MatchString(err.Error()) {
			t.Errorf("err %q carries no marker", err)
		}
		if tel2.counts["gohan.sandbox.secret_leak"] != 1 {
			t.Errorf("leak counter = %d, want 1", tel2.counts["gohan.sandbox.secret_leak"])
		}
	})

	t.Run("clean output untouched", func(t *testing.T) {
		tel3 := newCountTelemetry()
		ctx3 := types.WithTelemetry(context.Background(), tel3)
		blocks := []types.Block{types.Text{Text: "plain output"}}
		got, err := redactSecrets(ctx3, map[string]string{"PGPASSWORD": value}, blocks, nil)
		if err != nil {
			t.Fatalf("redactSecrets: %v", err)
		}
		if got[0].(types.Text).Text != "plain output" {
			t.Errorf("text = %q, want it untouched", got[0].(types.Text).Text)
		}
		if tel3.counts["gohan.sandbox.secret_leak"] != 0 {
			t.Errorf("leak counter = %d, want 0", tel3.counts["gohan.sandbox.secret_leak"])
		}
	})
}

func TestSecretInjectionAndLeakRedactionThroughTool(t *testing.T) {
	sb, _ := newFake(false)
	w := &recordingWorkspace{}
	sb.ws = w
	res := &fakeResolver{values: map[string]string{"warehouse": "pg-pass-9f31"}}
	policy := types.SandboxPolicy{
		Lifecycle: types.PerCall,
		Secrets:   []types.SecretRef{{Name: "warehouse", Env: "PGPASSWORD"}},
	}
	ts, err := New(sb, policy)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := types.WithSecrets(context.Background(), res)
	ctx = types.WithTelemetry(ctx, newCountTelemetry())
	w.stdout = []byte("connected with pg-pass-9f31\n")

	resBlocks, err := ts.Tools()[0].Call(ctx, args(t, runCodeArgs{Code: `echo "connected with $PGPASSWORD"`}))
	if err != nil {
		t.Fatalf("run_code: %v", err)
	}
	text := resBlocks.Content[0].(types.Text).Text
	if strings.Contains(text, "pg-pass-9f31") {
		t.Errorf("result %q leaks the secret", text)
	}
	if !regexp.MustCompile(`<gohan:secret:[0-9a-f]{24}>`).MatchString(text) {
		t.Errorf("result %q carries no marker", text)
	}
	if len(w.envs) != 1 || w.envs[0]["PGPASSWORD"] != "pg-pass-9f31" {
		t.Errorf("injected env = %v, want PGPASSWORD set once", w.envs)
	}
}

func TestSecretPolicyFailsWithoutResolverThroughTool(t *testing.T) {
	sb, _ := newFake(false)
	policy := types.SandboxPolicy{
		Lifecycle: types.PerCall,
		Secrets:   []types.SecretRef{{Name: "warehouse", Env: "PGPASSWORD"}},
	}
	ts, err := New(sb, policy)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := ts.Tools()[0].Call(context.Background(), args(t, runCodeArgs{Code: "true"})); !errors.Is(err, errNoSecretResolver) {
		t.Fatalf("run_code err = %v, want errNoSecretResolver", err)
	}
}
