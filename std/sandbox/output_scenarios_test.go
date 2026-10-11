package sandbox

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

const outputSecret = "s3cret-out-4e7dval"

// outWorkspace serves a queue of exec results, so each call sees the
// stdout the subtest scripts.
type outWorkspace struct {
	*fakeWorkspace
	results []types.ExecResult
}

func (w *outWorkspace) Exec(ctx context.Context, req types.ExecRequest) (types.ExecResult, error) {
	w.execReqs = append(w.execReqs, req)
	res := w.results[0]
	w.results = w.results[1:]
	return res, nil
}

func outPolicy() types.SandboxPolicy {
	return types.SandboxPolicy{
		Lifecycle: types.PerCall,
		Secrets:   []types.SecretRef{{Name: "warehouse", Env: "PGPASSWORD"}},
	}
}

// outCall runs run_code once through the public tool surface with the
// resolver and telemetry wired into the call context, as the harness does.
func outCall(t *testing.T, sb *fakeSandbox, p types.SandboxPolicy, opts ...Option) ([]types.Block, *countTelemetry) {
	t.Helper()
	resolver := &fakeResolver{values: map[string]string{"warehouse": outputSecret}}
	ts, err := New(sb, p, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tel := newCountTelemetry()
	ctx := types.WithSecrets(context.Background(), resolver)
	ctx = types.WithTelemetry(ctx, tel)
	res, err := ts.Tools()[0].Call(ctx, args(t, runCodeArgs{Code: "echo $PGPASSWORD"}))
	if err != nil {
		t.Fatalf("run_code: %v", err)
	}
	return res.Content, tel
}

func fileRef(t *testing.T, blocks []types.Block) string {
	t.Helper()
	for _, b := range blocks {
		if f, ok := b.(types.File); ok {
			return f.Blob.Ref
		}
	}
	return ""
}

func textBlocks(t *testing.T, blocks []types.Block) []string {
	t.Helper()
	var out []string
	for _, b := range blocks {
		if txt, ok := b.(types.Text); ok {
			out = append(out, txt.Text)
		}
	}
	return out
}

func joinTexts(texts []string) string {
	return strings.Join(texts, "")
}

func TestSandboxOutput(t *testing.T) {
	t.Run("sandbox.output-stored-redacted", func(t *testing.T) {
		w := &outWorkspace{fakeWorkspace: &fakeWorkspace{}}
		sb := &fakeSandbox{ws: w}
		w.results = []types.ExecResult{
			{Stdout: []byte("pw=" + outputSecret + strings.Repeat("x", 24))},
		}
		store := stores.NewMemoryOutputs()
		blocks, tel := outCall(t, sb, outPolicy(), WithOutputStore(store), WithMaxOutput(8))

		if ref := fileRef(t, blocks); ref == "" {
			t.Fatalf("content = %#v, want a File block with a ref", blocks)
		}
		token := secretToken(outputSecret)
		for i, txt := range textBlocks(t, blocks) {
			if strings.Contains(txt, outputSecret) {
				t.Errorf("inline text[%d] carries the secret value", i)
			}
		}
		if !strings.Contains(joinTexts(textBlocks(t, blocks)), "truncated at 8 bytes") {
			t.Errorf("inline text = %v, want the truncation marker", textBlocks(t, blocks))
		}
		stored := readBack(t, store, fileRef(t, blocks))
		if !strings.Contains(stored, token) {
			t.Errorf("stored doc = %q, want the redaction marker", stored)
		}
		if got := tel.counts["gohan.sandbox.secret_leak"]; got != 1 {
			t.Errorf("gohan.sandbox.secret_leak = %d, want 1", got)
		}
	})

	t.Run("sandbox.output-ref-sanitized", func(t *testing.T) {
		w := &outWorkspace{fakeWorkspace: &fakeWorkspace{}}
		sb := &fakeSandbox{ws: w}
		w.results = []types.ExecResult{
			{Stdout: []byte("pw=" + outputSecret + strings.Repeat("x", 24))},
		}
		store := stores.NewMemoryOutputs()
		blocks, _ := outCall(t, sb, outPolicy(), WithOutputStore(store), WithMaxOutput(8))

		ref := fileRef(t, blocks)
		if ref == "" {
			t.Fatalf("content = %#v, want a File block with a ref", blocks)
		}
		stored := readBack(t, store, ref)
		if strings.Contains(stored, outputSecret) {
			t.Errorf("read_output = %q, want no secret value in the stored document", stored)
		}
		if !strings.Contains(stored, secretToken(outputSecret)) {
			t.Errorf("read_output = %q, want the redaction marker", stored)
		}
	})

	t.Run("sandbox.output-redaction-deterministic", func(t *testing.T) {
		stdout := "pw=" + outputSecret + strings.Repeat("x", 24)
		store := stores.NewMemoryOutputs()
		var refs [2]string
		var excerpts [2]string
		for i := range refs {
			w := &outWorkspace{fakeWorkspace: &fakeWorkspace{}}
			sb := &fakeSandbox{ws: w}
			w.results = []types.ExecResult{{Stdout: []byte(stdout)}}
			blocks, _ := outCall(t, sb, outPolicy(), WithOutputStore(store), WithMaxOutput(8))
			refs[i] = fileRef(t, blocks)
			excerpts[i] = joinTexts(textBlocks(t, blocks))
			if refs[i] == "" {
				t.Fatalf("call %d: content = %#v, want a ref", i, blocks)
			}
		}
		if refs[0] != refs[1] {
			t.Errorf("refs = %q vs %q, want one content-addressed ref for equal redacted bytes", refs[0], refs[1])
		}
		if excerpts[0] != excerpts[1] {
			t.Errorf("excerpts differ: %q vs %q", excerpts[0], excerpts[1])
		}
	})

	t.Run("sandbox.leak-count-route-independent", func(t *testing.T) {
		w := &outWorkspace{fakeWorkspace: &fakeWorkspace{}}
		sb := &fakeSandbox{ws: w}
		w.results = []types.ExecResult{
			{Stdout: []byte("a" + outputSecret + "b" + outputSecret + "c")},
			{Stdout: []byte("pw=" + outputSecret + strings.Repeat("x", 24))},
		}
		store := stores.NewMemoryOutputs()
		resolver := &fakeResolver{values: map[string]string{"warehouse": outputSecret}}
		ts, err := New(sb, outPolicy(), WithOutputStore(store), WithMaxOutput(8))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		tel := newCountTelemetry()
		ctx := types.WithSecrets(context.Background(), resolver)
		ctx = types.WithTelemetry(ctx, tel)
		for i := range w.results {
			if _, err := ts.Tools()[0].Call(ctx, args(t, runCodeArgs{Code: "echo $PGPASSWORD"})); err != nil {
				t.Fatalf("call %d: run_code: %v", i, err)
			}
		}
		// Two inline occurrences plus one spilled occurrence, each counted
		// once; the safety pass over already-redacted blocks adds none.
		if got := tel.counts["gohan.sandbox.secret_leak"]; got != 3 {
			t.Errorf("gohan.sandbox.secret_leak = %d, want 3 across both routes", got)
		}
	})

	t.Run("sandbox.excerpt-cap-boundary", func(t *testing.T) {
		const max = 16
		w := &outWorkspace{fakeWorkspace: &fakeWorkspace{}}
		sb := &fakeSandbox{ws: w}
		// exactly Max after sanitization, then one byte more
		w.results = []types.ExecResult{
			{Stdout: []byte("0123456789abcdef")},
			{Stdout: []byte("0123456789abcdefg")},
		}
		store := stores.NewMemoryOutputs()
		resolver := &fakeResolver{values: map[string]string{"warehouse": outputSecret}}
		ts, err := New(sb, types.SandboxPolicy{
			Lifecycle: types.PerCall,
			Secrets:   []types.SecretRef{{Name: "warehouse", Env: "PGPASSWORD"}},
		}, WithOutputStore(store), WithMaxOutput(max))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		tel := newCountTelemetry()
		ctx := types.WithSecrets(context.Background(), resolver)
		ctx = types.WithTelemetry(ctx, tel)

		res, err := ts.Tools()[0].Call(ctx, args(t, runCodeArgs{Code: "x"}))
		if err != nil {
			t.Fatalf("run_code: %v", err)
		}
		if fileRef(t, res.Content) != "" {
			t.Errorf("content = %#v, want no ref at the cap boundary", res.Content)
		}
		texts := textBlocks(t, res.Content)
		if len(texts) != 1 || texts[0] != "0123456789abcdef" {
			t.Errorf("inline text = %v, want the unaltered stream", texts)
		}

		res, err = ts.Tools()[0].Call(ctx, args(t, runCodeArgs{Code: "x"}))
		if err != nil {
			t.Fatalf("run_code: %v", err)
		}
		texts = textBlocks(t, res.Content)
		if len(texts) != 1 {
			t.Fatalf("content texts = %v, want one head excerpt", texts)
		}
		markerLen := len(texts[0]) - max
		if markerLen <= 0 {
			t.Errorf("excerpt = %q, want the marker appended to a %d-byte head", texts[0], max)
		}
		if !strings.HasPrefix(texts[0], "0123456789abcdef") || !strings.Contains(texts[0], "truncated at 16 bytes, 1 bytes dropped") {
			t.Errorf("excerpt = %q, want a %d-byte head plus the truncation marker", texts[0], max)
		}
		if ref := fileRef(t, res.Content); ref == "" {
			t.Errorf("content = %#v, want a File block with a ref", res.Content)
		}
		if got := tel.counts["gohan.sandbox.secret_leak"]; got != 0 {
			t.Errorf("gohan.sandbox.secret_leak = %d, want 0: the value never appears", got)
		}
	})

	t.Run("sandbox.no-store-overflow", func(t *testing.T) {
		w := &outWorkspace{fakeWorkspace: &fakeWorkspace{}}
		sb := &fakeSandbox{ws: w}
		w.results = []types.ExecResult{
			{Stdout: []byte("pw=" + outputSecret + strings.Repeat("x", 40))},
		}
		blocks, tel := outCall(t, sb, outPolicy(), WithMaxOutput(8))

		if ref := fileRef(t, blocks); ref != "" {
			t.Errorf("content = %#v, want no ref without a store", blocks)
		}
		texts := textBlocks(t, blocks)
		if len(texts) != 1 {
			t.Fatalf("content texts = %v, want one bounded excerpt", texts)
		}
		if !strings.Contains(texts[0], "no output store configured") {
			t.Errorf("excerpt = %q, want the missing-store marker", texts[0])
		}
		sanitizedLen := len("pw=") + 40 + len(secretToken(outputSecret))
		dropped := sanitizedLen - 8
		if !strings.Contains(texts[0], fmt.Sprintf("%d bytes dropped", dropped)) {
			t.Errorf("excerpt = %q, want the dropped byte count %d", texts[0], dropped)
		}
		if strings.Contains(texts[0], outputSecret) {
			t.Errorf("excerpt = %q, want no secret value", texts[0])
		}
		if got := tel.counts["gohan.output.stored"]; got != 0 {
			t.Errorf("gohan.output.stored = %d, want 0 without a store", got)
		}
	})

	t.Run("TestSandboxOutputPublicPath", func(t *testing.T) {
		w := &scenarioWorkspace{fakeWorkspace: &fakeWorkspace{}}
		sb := &fakeSandbox{ws: w}
		resolver := &fakeResolver{values: map[string]string{"warehouse": outputSecret}}
		model := &scenarioModel{steps: []types.ToolUse{scenarioRunCode("call-out", "echo $PGPASSWORD")}}
		w.results = []types.ExecResult{
			{Stdout: []byte("pw=" + outputSecret + strings.Repeat("x", 24))},
		}
		outs := stores.NewMemoryOutputs()
		tools := mustTools(t, sb, outPolicy(), WithOutputStore(outs), WithMaxOutput(8))
		log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))
		runs := stores.NewMemoryRuns(
			stores.WithMemoryRunClock(time.Now),
			stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
				p, ok := types.PrincipalFrom(ctx)
				return types.RunInfo{Principal: p}, ok
			}),
		)
		stack, err := gohan.Build(
			gohan.WithStores(stores.Stores{
				SessionLog:  log,
				Runs:        runs,
				OutputStore: outs,
				Checkpoints: stores.NewMemoryCheckpoints(stores.WithMemoryCheckpointClock(time.Now), stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom)),
			}),
			gohan.WithModels(model),
			gohan.WithNativeAgent(gohan.NativeSpec{
				Request: gohan.FlowRequest{Name: "chat"},
				Profile: "native",
				Tools:   tools,
				Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
					return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
				},
			}),
			gohan.WithEgressClient(func(types.EgressPolicy) *http.Client { return http.DefaultClient }),
			gohan.WithSecretResolver(resolver),
		)
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
		tel := &scenarioTelemetry{countTelemetry: newCountTelemetry()}
		scenarioSend(t, conv, tel, "turn")

		blocks := scenarioResultBlocks(scenarioHistory(t, log))
		ref := fileRef(t, blocks)
		if ref == "" {
			t.Fatalf("history blocks = %#v, want a File block with a ref", blocks)
		}
		stored := readBack(t, outs, ref)
		if strings.Contains(stored, outputSecret) {
			t.Errorf("read_output = %q, want no secret value", stored)
		}
		if !strings.Contains(stored, secretToken(outputSecret)) {
			t.Errorf("read_output = %q, want the redaction marker", stored)
		}
		scenarioAssertAbsent(t, outputSecret, "history", scenarioJSON(t, blocks))
		if got := tel.counts["gohan.sandbox.secret_leak"]; got != 1 {
			t.Errorf("gohan.sandbox.secret_leak = %d, want 1", got)
		}
	})
}
