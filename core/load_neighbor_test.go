package gohan

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/json/jsontext"
	"fmt"
	"iter"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

const (
	neighborSteps     = 8
	neighborK         = 5
	neighborBurnIters = 4000
)

// sink keeps the burn workload observable so the compiler cannot elide it.
var sink [32]byte

type neighborModel struct {
	steps   int
	entered chan struct{}
}

func newNeighborModel(steps int, entered chan struct{}) *neighborModel {
	return &neighborModel{steps: steps, entered: entered}
}

func (m *neighborModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "native", Caps: types.Caps{Tools: true}}
}

func (m *neighborModel) Generate(_ context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		if lastUserText(req) == "saturate" {
			neighborBurn()
			if m.entered != nil {
				select {
				case m.entered <- struct{}{}:
				default:
				}
			}
			yield(types.ModelChunk{Kind: types.DeltaText, Delta: "ok", Finish: types.FinishStop}, nil)
			return
		}
		answered := 0
		for _, msg := range req.Messages {
			for _, b := range msg.Blocks {
				if _, ok := b.(types.ToolResult); ok {
					answered++
				}
			}
		}
		if answered >= m.steps {
			yield(types.ModelChunk{Kind: types.DeltaText, Delta: "done", Finish: types.FinishStop}, nil)
			return
		}
		call := types.ToolUse{
			ID:   fmt.Sprintf("call-%d", answered),
			Name: "noop",
			Args: jsontext.Value(`{}`),
		}
		yield(types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: &call}, nil)
		yield(types.ModelChunk{Finish: types.FinishToolUse}, nil)
	}
}

// neighborBurn performs a fixed CPU workload of roughly one turn-millisecond.
func neighborBurn() {
	buf := make([]byte, 64)
	var local [32]byte
	for range neighborBurnIters {
		local = sha256.Sum256(buf)
	}
	if local[0] == 0xff {
		sink = local
	}
}

func lastUserText(req types.ModelRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if msg.Role != types.RoleUser {
			continue
		}
		for _, b := range msg.Blocks {
			if t, ok := b.(types.Text); ok {
				return t.Text
			}
		}
	}
	return ""
}

func neighborUser(text string) types.Message {
	return types.Message{ID: "neighbor-in", Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: text}}}
}

// neighborNoopTool is a trivial read-only tool so no approval suspends the run.
type neighborNoopTool struct {
	mu      sync.Mutex
	execs   int
	samples []time.Time
}

func newNoopTool() *neighborNoopTool { return &neighborNoopTool{} }

func (t *neighborNoopTool) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "noop", Effect: types.ReadOnly}
}

func (t *neighborNoopTool) Call(_ context.Context, args jsontext.Value) (types.ToolResult, error) {
	var in struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(args, &in)
	t.mu.Lock()
	t.execs++
	t.samples = append(t.samples, time.Now())
	t.mu.Unlock()
	return types.ToolResult{
		ID:      in.ID,
		Content: []types.Block{types.Text{Text: "ok"}},
		Outcome: types.Succeeded,
	}, nil
}

func (t *neighborNoopTool) execCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.execs
}

func (t *neighborNoopTool) stepSamples() []time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]time.Time(nil), t.samples...)
}

func neighborStack(t *testing.T, model types.Model, tools []types.Tool) (*Stack, *stores.MemoryEventLog, stores.Runs) {
	t.Helper()
	runs := stores.NewMemoryRuns(
		stores.WithMemoryRunClock(time.Now),
		stores.WithMemoryRunInfo(func(ctx context.Context) (types.RunInfo, bool) {
			p, ok := types.PrincipalFrom(ctx)
			return types.RunInfo{Principal: p}, ok
		}),
	)
	evlog := stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))
	stack, err := Build(
		WithStores(stores.Stores{
			SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
			Runs:       runs,
			Checkpoints: stores.NewMemoryCheckpoints(
				stores.WithMemoryCheckpointClock(time.Now),
				stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom),
				stores.WithMemoryCheckpointRuns(runs),
			),
			EventLog: evlog,
		}),
		WithModels(model),
		WithNativeAgent(NativeSpec{
			Request: FlowRequest{Name: "load"},
			Profile: "native",
			Tools:   tools,
			Assemble: func(_ context.Context, in types.AssembleInput) (types.ModelRequest, error) {
				return types.ModelRequest{Messages: append(append([]types.Message{}, in.History...), in.Input...)}, nil
			},
		}),
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return stack, evlog, runs
}

func neighborConversation(stack *Stack, evlog *stores.MemoryEventLog, runs stores.Runs) (Conversation, error) {
	conv, err := NewNativeConversation(stack, "load",
		WithConversationRuns(runs),
		WithConversationEventLog(evlog),
		WithConversationApprovalPolicy(nrrAllowPolicy{}),
	)
	if err != nil {
		return nil, fmt.Errorf("new native conversation: %w", err)
	}
	return conv, nil
}

func neighborCtx() context.Context {
	return types.WithPrincipal(context.Background(), types.Principal{Subject: "load", Tenant: "t1"})
}

// neighborRun executes the single-run tool-loop workload and returns the wall time.
func neighborRun(t *testing.T, conv Conversation, session string) time.Duration {
	t.Helper()
	start := time.Now()
	for _, err := range conv.Send(neighborCtx(), session, neighborUser("steady")) {
		if err != nil {
			t.Fatalf("send on %s: %v", session, err)
		}
	}
	return time.Since(start)
}

func TestLoadNoisyNeighbor(t *testing.T) {
	t.Run("performance.noisy-neighbor-isolation", func(t *testing.T) {
		raw, err := baselineFS.ReadFile("performance_baselines.json")
		if err != nil {
			t.Fatalf("read baselines: %v", err)
		}
		var baselines struct {
			NoisyNeighbor struct {
				MaxRatio       float64 `json:"max_ratio"`
				ReferenceSteps int     `json:"reference_steps"`
			} `json:"noisy_neighbor"`
		}
		if err := json.Unmarshal(raw, &baselines); err != nil {
			t.Fatalf("parse baselines: %v", err)
		}
		if baselines.NoisyNeighbor.MaxRatio <= 0 {
			t.Fatalf("noisy_neighbor.max_ratio = %g, want > 0", baselines.NoisyNeighbor.MaxRatio)
		}
		if baselines.NoisyNeighbor.ReferenceSteps != neighborSteps {
			t.Fatalf("noisy_neighbor.reference_steps = %d, want %d", baselines.NoisyNeighbor.ReferenceSteps, neighborSteps)
		}

		// Isolated baseline: the same single-run workload alone on a fresh stack.
		isoTool := newNoopTool()
		isoStack, isoLog, isoRuns := neighborStack(t, newNeighborModel(neighborSteps, nil), []types.Tool{isoTool})
		isoConv, err := neighborConversation(isoStack, isoLog, isoRuns)
		if err != nil {
			t.Fatalf("conversation: %v", err)
		}
		isolatedWall := neighborRun(t, isoConv, "nb-iso")
		if got := isoTool.execCount(); got != neighborSteps {
			t.Fatalf("isolated tool execs = %d, want %d", got, neighborSteps)
		}
		isolatedSamples := append(isoTool.stepSamples(), time.Now())

		// Loaded: K-1 saturators loop CPU-bound turns on the shared stack
		// while the neighbor runs its workload once.
		stop := make(chan struct{})
		var closeOnce sync.Once
		closeStop := func() {}
		defer closeStop()
		closeStop = func() {
			closeOnce.Do(func() { close(stop) })
		}

		entered := make(chan struct{}, neighborK)
		tool := newNoopTool()
		stack, evlog, runs := neighborStack(t, newNeighborModel(neighborSteps, entered), []types.Tool{tool})

		var wg sync.WaitGroup
		turnCounts := make([]int, neighborK)
		satErrs := make([]error, neighborK)
		for i := range neighborK {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				conv, err := neighborConversation(stack, evlog, runs)
				if err != nil {
					satErrs[i] = err
					return
				}
				for {
					select {
					case <-stop:
						return
					default:
					}
					for _, err := range conv.Send(neighborCtx(), fmt.Sprintf("sat-%d", i), neighborUser("saturate")) {
						if err != nil {
							satErrs[i] = err
							return
						}
					}
					turnCounts[i]++
				}
			}(i)
		}
		for range neighborK {
			<-entered
		}

		loadedConv, err := neighborConversation(stack, evlog, runs)
		if err != nil {
			t.Fatalf("conversation: %v", err)
		}
		loadedWall := neighborRun(t, loadedConv, "nb-loaded")
		loadedSamples := append(tool.stepSamples(), time.Now())
		closeStop()
		wg.Wait()

		for i := range neighborK {
			if satErrs[i] != nil {
				t.Fatalf("saturating run %d: %v", i, satErrs[i])
			}
			if turnCounts[i] <= 0 {
				t.Fatalf("saturating run %d completed %d turns, want > 0", i, turnCounts[i])
			}
		}
		if got := tool.execCount(); got != neighborSteps {
			t.Fatalf("neighbor tool execs = %d, want %d", got, neighborSteps)
		}

		isolatedNs := int(isolatedWall.Nanoseconds())
		loadedNs := int(loadedWall.Nanoseconds())
		if isolatedNs <= 0 || loadedNs <= 0 {
			t.Fatalf("measurements isolated=%d loaded=%d, want both positive", isolatedNs, loadedNs)
		}
		if len(isolatedSamples) != neighborSteps+1 || len(loadedSamples) != neighborSteps+1 {
			t.Fatalf("step samples isolated=%d loaded=%d, want %d each", len(isolatedSamples), len(loadedSamples), neighborSteps+1)
		}
		isolatedPerStep := float64(isolatedNs) / float64(neighborSteps)
		loadedPerStep := float64(loadedNs) / float64(neighborSteps)
		ratio := loadedPerStep / isolatedPerStep
		if ratio <= 0 {
			t.Fatalf("ratio = %g, want > 0", ratio)
		}
		maxStepRatio := 0.0
		maxStep := -1
		for i := range neighborSteps {
			isoStep := float64(isolatedSamples[i+1].Sub(isolatedSamples[i]).Nanoseconds())
			loadedStep := float64(loadedSamples[i+1].Sub(loadedSamples[i]).Nanoseconds())
			if isoStep <= 0 || loadedStep <= 0 {
				t.Fatalf("step %d latency isolated=%d loaded=%d, want both positive", i, int(isoStep), int(loadedStep))
			}
			if r := loadedStep / isoStep; r > maxStepRatio {
				maxStepRatio = r
				maxStep = i
			}
		}
		t.Logf(`LOADRATIO {"run":"neighbor","isolated_ns":%d,"loaded_ns":%d,"ratio":%.4f,"max_step_ratio":%.4f,"limit":%g,"steps":%d}`,
			isolatedNs, loadedNs, ratio, maxStepRatio, baselines.NoisyNeighbor.MaxRatio, neighborSteps)

		if os.Getenv("GOHAN_PERF_REFERENCE") == "1" && ratio > baselines.NoisyNeighbor.MaxRatio {
			t.Fatalf("neighbor mean ratio = %.4f (worst step %.4f, step %d), want <= %g", ratio, maxStepRatio, maxStep, baselines.NoisyNeighbor.MaxRatio)
		}
	})
}
