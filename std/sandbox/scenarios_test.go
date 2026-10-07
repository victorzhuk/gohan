package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"iter"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/limit"
)

const scenarioSecret = "s3cret-pg-pass-9f2c"

type scenarioWorkspace struct {
	*fakeWorkspace
	results     []types.ExecResult
	envs        []map[string]string
	file        []byte
	panicOnExec bool
}

func (w *scenarioWorkspace) Exec(ctx context.Context, req types.ExecRequest) (types.ExecResult, error) {
	w.execReqs = append(w.execReqs, req)
	return w.nextResult(), nil
}

func (w *scenarioWorkspace) nextResult() types.ExecResult {
	if w.panicOnExec {
		panic("sandbox exec panic")
	}
	if len(w.results) == 0 {
		return types.ExecResult{}
	}
	res := w.results[0]
	w.results = w.results[1:]
	return res
}

func (w *scenarioWorkspace) SetEnv(ctx context.Context, env map[string]string) error {
	w.envs = append(w.envs, env)
	return nil
}

func (w *scenarioWorkspace) Get(ctx context.Context, path string) (io.ReadCloser, error) {
	w.reads = append(w.reads, path)
	return io.NopCloser(bytes.NewReader(w.file)), nil
}

type scenarioModel struct {
	mu       sync.Mutex
	requests []types.ModelRequest
	steps    []types.ToolUse
	emitted  int
}

func (m *scenarioModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "native", Caps: types.Caps{Tools: true}}
}

func (m *scenarioModel) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	var step *types.ToolUse
	if m.emitted < len(m.steps) {
		s := m.steps[m.emitted]
		step = &s
		m.emitted++
	}
	m.mu.Unlock()
	return func(yield func(types.ModelChunk, error) bool) {
		if step != nil {
			yield(types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: step}, nil)
			yield(types.ModelChunk{Finish: types.FinishToolUse}, nil)
			return
		}
		yield(types.ModelChunk{Kind: types.DeltaText, Delta: "done"}, nil)
		yield(types.ModelChunk{Finish: types.FinishStop}, nil)
	}
}

func (m *scenarioModel) recorded() []types.ModelRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]types.ModelRequest(nil), m.requests...)
}

type scenarioTelemetry struct {
	*countTelemetry
	mu    sync.Mutex
	spans []string
}

func (t *scenarioTelemetry) StartSpan(ctx context.Context, name string, attrs ...types.Attr) (context.Context, func(...types.Attr)) {
	raw, _ := json.Marshal(attrs)
	t.mu.Lock()
	t.spans = append(t.spans, name+" "+string(raw))
	t.mu.Unlock()
	return ctx, func(...types.Attr) {}
}

func (t *scenarioTelemetry) recordedSpans() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.spans...)
}

func scenarioJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

func scenarioResultBlocks(h stores.History) []types.Block {
	var blocks []types.Block
	for _, msg := range h.Messages {
		for _, b := range msg.Blocks {
			if r, ok := b.(types.ToolResult); ok {
				blocks = append(blocks, r.Content...)
			}
		}
	}
	return blocks
}

func scenarioResultText(h stores.History) string {
	var sb strings.Builder
	for _, b := range scenarioResultBlocks(h) {
		if txt, ok := b.(types.Text); ok {
			sb.WriteString(txt.Text)
		}
	}
	return sb.String()
}

func scenarioAssertAbsent(t *testing.T, secret, where string, texts ...string) {
	t.Helper()
	for i, text := range texts {
		if strings.Contains(text, secret) {
			t.Fatalf("%s[%d] carries the secret value", where, i)
		}
	}
}

func scenarioBuild(t *testing.T, model *scenarioModel, log stores.SessionLog, tools []types.Tool, chain chains.ToolChain, opts ...gohan.Option) gohan.Conversation {
	t.Helper()
	runs := stores.NewMemoryRuns(
		stores.WithMemoryRunClock(time.Now),
		stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
			p, ok := types.PrincipalFrom(ctx)
			return types.RunInfo{Principal: p}, ok
		}),
	)
	buildOpts := append([]gohan.Option{
		gohan.WithStores(stores.Stores{
			SessionLog:  log,
			Runs:        runs,
			Checkpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
		}),
		gohan.WithModels(model),
		gohan.WithNativeAgent(gohan.NativeSpec{
			Request:   gohan.FlowRequest{Name: "chat"},
			Profile:   "native",
			Tools:     tools,
			ToolChain: chain,
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
			},
		}),
		gohan.WithEgressClient(func(types.EgressPolicy) *http.Client { return http.DefaultClient }),
	}, opts...)
	stack, err := gohan.Build(buildOpts...)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	conv, err := gohan.NewNativeConversation(stack, "chat",
		gohan.WithConversationRuns(runs),
		gohan.WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
	)
	if err != nil {
		t.Fatalf("new native conversation: %v", err)
	}
	return conv
}

func scenarioSend(t *testing.T, conv gohan.Conversation, tel types.Telemetry, text string) {
	t.Helper()
	ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"})
	if tel != nil {
		ctx = types.WithTelemetry(ctx, tel)
	}
	for ev, err := range conv.Send(ctx, "s1", types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: text}}}) {
		if err != nil {
			t.Fatalf("send: %v", err)
		}
		_ = ev
	}
}

func scenarioRunCode(id, code string) types.ToolUse {
	raw, err := json.Marshal(runCodeArgs{Code: code})
	if err != nil {
		panic(err)
	}
	return types.ToolUse{ID: id, Name: "run_code", Args: raw}
}

func scenarioHistory(t *testing.T, log stores.SessionLog) stores.History {
	t.Helper()
	h, err := log.Load(types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"}), "s1")
	if err != nil {
		t.Fatalf("load session: %v", err)
	}
	return h
}

func TestSandboxEffectDerived(t *testing.T) {
	t.Run("sandbox.effect-derived", func(t *testing.T) {
		idempotent, err := New(&fakeSandbox{ws: &fakeWorkspace{}}, types.SandboxPolicy{Lifecycle: types.PerSession})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		scenarioSend(t, scenarioBuild(t, &scenarioModel{}, log, idempotent.Tools(), nil), nil, "turn")
		if got := idempotent.Tools()[0].Spec().Effect; got != types.Idempotent {
			t.Fatalf("run_code effect = %v, want Idempotent", got)
		}

		side, err := New(&fakeSandbox{ws: &fakeWorkspace{}}, types.SandboxPolicy{
			Lifecycle: types.PerSession,
			Egress:    types.EgressPolicy{Allow: []string{"api.example.com"}},
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		log2 := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		scenarioSend(t, scenarioBuild(t, &scenarioModel{}, log2, side.Tools(), nil), nil, "turn")
		if got := side.Tools()[0].Spec().Effect; got != types.SideEffect {
			t.Fatalf("run_code effect = %v, want SideEffect", got)
		}
	})
}

func TestSandboxSecrets(t *testing.T) {
	t.Run("sandbox.secret-injected", func(t *testing.T) {
		w := &scenarioWorkspace{fakeWorkspace: &fakeWorkspace{}}
		sb := &fakeSandbox{ws: w}
		resolver := &fakeResolver{values: map[string]string{"warehouse": scenarioSecret}}
		model := &scenarioModel{steps: []types.ToolUse{scenarioRunCode("call-pw", "echo ok")}}
		w.results = []types.ExecResult{{Stdout: []byte("ok\n")}}
		log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		tel := &scenarioTelemetry{countTelemetry: newCountTelemetry()}
		conv := scenarioBuild(t, model, log, mustTools(t, sb, types.SandboxPolicy{
			Lifecycle: types.PerCall,
			Secrets:   []types.SecretRef{{Name: "warehouse", Env: "PGPASSWORD"}},
		}), nil, gohan.WithSecretResolver(resolver))
		scenarioSend(t, conv, tel, "turn")

		if resolver.calls != 1 {
			t.Fatalf("resolver calls = %d, want 1", resolver.calls)
		}
		if len(resolver.names) != 1 || resolver.names[0] != "warehouse" {
			t.Fatalf("resolver names = %v, want [warehouse]", resolver.names)
		}
		if len(resolver.principals) != 1 || resolver.principals[0].Tenant != "t1" || resolver.principals[0].Subject != "u1" {
			t.Fatalf("resolver principals = %v, want the run originator", resolver.principals)
		}
		if len(w.envs) != 1 || w.envs[0]["PGPASSWORD"] != scenarioSecret {
			t.Fatalf("SetEnv calls = %v, want exactly one carrying PGPASSWORD", w.envs)
		}
		h := scenarioHistory(t, log)
		scenarioAssertAbsent(t, scenarioSecret, "history", scenarioJSON(t, h.Messages))
		scenarioAssertAbsent(t, scenarioSecret, "model requests", scenarioJSON(t, model.recorded()))
		scenarioAssertAbsent(t, scenarioSecret, "spans", tel.recordedSpans()...)
	})

	t.Run("sandbox.secret-leak-redacted", func(t *testing.T) {
		w := &scenarioWorkspace{fakeWorkspace: &fakeWorkspace{}}
		sb := &fakeSandbox{ws: w}
		resolver := &fakeResolver{values: map[string]string{"warehouse": scenarioSecret}}
		model := &scenarioModel{steps: []types.ToolUse{scenarioRunCode("call-leak", "echo $PGPASSWORD")}}
		w.results = []types.ExecResult{{Stdout: []byte("pw=" + scenarioSecret + "\n")}}
		log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		tel := &scenarioTelemetry{countTelemetry: newCountTelemetry()}
		conv := scenarioBuild(t, model, log, mustTools(t, sb, types.SandboxPolicy{
			Lifecycle: types.PerCall,
			Secrets:   []types.SecretRef{{Name: "warehouse", Env: "PGPASSWORD"}},
		}), nil, gohan.WithSecretResolver(resolver))
		scenarioSend(t, conv, tel, "turn")

		marker := secretToken(scenarioSecret)
		text := scenarioResultText(scenarioHistory(t, log))
		if !strings.Contains(text, marker) {
			t.Fatalf("tool result %q lacks the redaction marker %q", text, marker)
		}
		scenarioAssertAbsent(t, scenarioSecret, "history", text, scenarioJSON(t, model.recorded()))
		if got := tel.counts["gohan.sandbox.secret_leak"]; got != 1 {
			t.Fatalf("gohan.sandbox.secret_leak = %d, want 1", got)
		}
	})
}

func TestSandboxPerCallCloses(t *testing.T) {
	t.Run("sandbox.per-call-closes", func(t *testing.T) {
		w := &scenarioWorkspace{fakeWorkspace: &fakeWorkspace{}}
		sb := &fakeSandbox{ws: w}
		policy := types.SandboxPolicy{Lifecycle: types.PerCall}

		model := &scenarioModel{steps: []types.ToolUse{scenarioRunCode("call-ok", "echo ok")}}
		w.results = []types.ExecResult{{Stdout: []byte("ok\n")}}
		log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		scenarioSend(t, scenarioBuild(t, model, log, mustTools(t, sb, policy), nil), nil, "turn")
		if w.closes != 1 {
			t.Fatalf("closes after normal call = %d, want 1", w.closes)
		}

		w.panicOnExec = true
		model2 := &scenarioModel{steps: []types.ToolUse{scenarioRunCode("call-panic", "false")}}
		log2 := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		scenarioSend(t, scenarioBuild(t, model2, log2, mustTools(t, sb, policy), nil), nil, "turn")
		if w.closes != 2 {
			t.Fatalf("closes after panicking call = %d, want exactly one more (2)", w.closes)
		}
	})
}

func TestSandboxOutputToStore(t *testing.T) {
	t.Run("sandbox.output-to-store", func(t *testing.T) {
		w := &scenarioWorkspace{fakeWorkspace: &fakeWorkspace{}}
		sb := &fakeSandbox{ws: w}
		stdout := strings.Repeat("x", 100)
		fileBody := strings.Repeat("y", 100)
		w.results = []types.ExecResult{{Stdout: []byte(stdout)}}
		w.file = []byte(fileBody)
		readArgs, err := json.Marshal(readFileArgs{Path: "out.bin"})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		model := &scenarioModel{steps: []types.ToolUse{
			scenarioRunCode("call-big", "generate"),
			{ID: "call-read", Name: "read_file", Args: readArgs},
		}}
		log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		store := stores.NewMemoryOutputs()
		conv := scenarioBuild(t, model, log, mustTools(t, sb, types.SandboxPolicy{Lifecycle: types.PerCall}, WithOutputStore(store), WithMaxOutput(16)), nil)
		scenarioSend(t, conv, nil, "turn")

		blocks := scenarioResultBlocks(scenarioHistory(t, log))
		texts, refs := 0, 0
		for _, b := range blocks {
			switch v := b.(type) {
			case types.Text:
				texts++
				if strings.Contains(v.Text, strings.Repeat("x", 17)) || strings.Contains(v.Text, strings.Repeat("y", 17)) {
					t.Fatalf("inline text %q exceeds the MaxOutput cap", v.Text)
				}
			case types.File:
				refs++
				if v.Blob.Ref == "" {
					t.Fatal("File block carries an empty ref")
				}
				data, err := store.Get(context.Background(), v.Blob.Ref)
				if err != nil {
					t.Fatalf("store get %s: %v", v.Blob.Ref, err)
				}
				stored := string(data)
				if !strings.Contains(stored, stdout) && !strings.Contains(stored, fileBody) {
					t.Fatalf("stored content %q matches neither the stdout nor the returned file", stored)
				}
			}
		}
		if texts == 0 || refs != 2 {
			t.Fatalf("result blocks: %d texts, %d file refs, want head excerpts beside 2 refs", texts, refs)
		}
	})
}

func mustTools(t *testing.T, sb *fakeSandbox, p types.SandboxPolicy, opts ...Option) []types.Tool {
	t.Helper()
	ts, err := New(sb, p, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return ts.Tools()
}

func TestSandboxSecondsBudget(t *testing.T) {
	limits := types.InteractiveLimits
	limits.MaxSandboxSeconds = 10

	t.Run("sandbox.seconds-budget", func(t *testing.T) {
		w := &scenarioWorkspace{fakeWorkspace: &fakeWorkspace{}}
		sb := &fakeSandbox{ws: w}
		w.results = []types.ExecResult{
			{Stdout: []byte("one\n"), Seconds: 6},
			{Stdout: []byte("two\n"), Seconds: 6},
		}
		model := &scenarioModel{steps: []types.ToolUse{
			scenarioRunCode("call-one", "one"),
			scenarioRunCode("call-two", "two"),
		}}
		log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		conv := scenarioBuild(t, model, log,
			mustTools(t, sb, types.SandboxPolicy{Lifecycle: types.PerCall}),
			chains.ToolChain{{Name: "limits", Kind: chains.KindLimit, Use: limit.ToolLimits(limits)}},
			gohan.WithLimits("chat", limits),
			gohan.WithModelMiddleware(limit.Limits(limits, types.Pricing{})))

		ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"})
		var limitErr *types.LimitExceededError
		for _, err := range conv.Send(ctx, "s1", types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "run"}}}) {
			if err != nil {
				if !errors.As(err, &limitErr) || limitErr.Limit != "MaxSandboxSeconds" {
					t.Fatalf("send error = %v, want *types.LimitExceededError MaxSandboxSeconds", err)
				}
				if limitErr.Value <= limits.MaxSandboxSeconds {
					t.Fatalf("limit value = %v, want the cumulative spend past %v", limitErr.Value, limits.MaxSandboxSeconds)
				}
			}
		}
		if limitErr == nil {
			t.Fatal("the second run_code past the budget did not fail the send")
		}
		if len(w.execReqs) != 2 {
			t.Fatalf("exec calls = %d, want 2", len(w.execReqs))
		}

		within := &scenarioWorkspace{fakeWorkspace: &fakeWorkspace{}}
		sb2 := &fakeSandbox{ws: within}
		within.results = []types.ExecResult{{Stdout: []byte("ok\n"), Seconds: 3}}
		model2 := &scenarioModel{steps: []types.ToolUse{scenarioRunCode("call-ok", "ok")}}
		log2 := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		conv2 := scenarioBuild(t, model2, log2,
			mustTools(t, sb2, types.SandboxPolicy{Lifecycle: types.PerCall}),
			chains.ToolChain{{Name: "limits", Kind: chains.KindLimit, Use: limit.ToolLimits(limits)}},
			gohan.WithLimits("chat", limits),
			gohan.WithModelMiddleware(limit.Limits(limits, types.Pricing{})))
		var done *types.Done
		for ev, err := range conv2.Send(ctx, "s1", types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "run"}}}) {
			if err != nil {
				t.Fatalf("within-budget send: %v", err)
			}
			if d, ok := ev.(types.Done); ok {
				done = &d
			}
		}
		if done == nil {
			t.Fatal("within-budget run produced no Done")
		}
		if done.Usage.SandboxSeconds != 3 {
			t.Fatalf("Done.Usage.SandboxSeconds = %v, want 3", done.Usage.SandboxSeconds)
		}
	})
}
