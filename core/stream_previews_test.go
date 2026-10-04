package gohan

import (
	"context"
	"encoding/json/jsontext"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// argsRecorder is a ReadOnly tool that records the complete arguments it
// was executed with and how many times it ran.
type argsRecorder struct {
	ran  int
	args []jsontext.Value
}

func (t *argsRecorder) Spec() types.ToolSpec {
	return types.ToolSpec{Name: "record", Effect: types.ReadOnly}
}

func (t *argsRecorder) Call(_ context.Context, args jsontext.Value) (types.ToolResult, error) {
	t.ran++
	t.args = append(t.args, args)
	return types.ToolResult{Content: []types.Block{types.Text{Text: "ok"}}, Outcome: types.Succeeded}, nil
}

func allSink(dst *[]types.Event) types.Sink {
	return sinkFunc(func(_ context.Context, e types.Event) { *dst = append(*dst, e) })
}

func noopSink() types.Sink {
	return sinkFunc(func(context.Context, types.Event) {})
}

// argFragments builds n DeltaToolArgs fragment chunks for one call: the
// call identity rides on every fragment, the complete block arrives after.
func argFragments(id, name string, n int) []types.ModelChunk {
	fragments := make([]types.ModelChunk, n)
	for i := range fragments {
		fragments[i] = types.ModelChunk{
			Kind:    types.DeltaToolArgs,
			Delta:   "f" + strconv.Itoa(i),
			ToolUse: &types.ToolUse{ID: id, Name: name},
		}
	}
	return fragments
}

func TestStreamPreviews(t *testing.T) {
	ctx := context.Background()

	t.Run("model.tool-args-delta-kind", func(t *testing.T) {
		m := &scriptTurns{turns: [][]types.ModelChunk{
			append(argFragments("c1", "record", 3),
				types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: &types.ToolUse{ID: "c1", Name: "record", Args: jsontext.Value(`{}`)}, Finish: types.FinishToolUse}),
			{turnTextChunk("done")},
		}}
		var evs []types.Event
		tool := &argsRecorder{}
		cfg := turnConfig{
			model:    m.model(),
			assemble: turnAssemble(nil),
			tools:    turnToolset(tool),
			maxTurns: 4,
		}
		if _, err := driveTurnCollect(func(y func(types.Event, error) bool) {
			driveTurns(types.WithSink(ctx, allSink(&evs)), cfg, y)
		}); err != nil {
			t.Fatalf("drive: %v", err)
		}
		var argDeltas []types.ToolArgsDelta
		startedAt := -1
		for i, ev := range evs {
			switch e := ev.(type) {
			case types.ToolArgsDelta:
				argDeltas = append(argDeltas, e)
			case types.ToolStarted:
				startedAt = i
			}
		}
		if len(argDeltas) != 3 {
			t.Fatalf("ToolArgsDelta events = %d, want 3", len(argDeltas))
		}
		for i, d := range argDeltas {
			if d.CallID != "c1" || d.Name != "record" {
				t.Fatalf("delta %d = %+v, want call c1/record", i, d)
			}
			if d.Delta != "f"+strconv.Itoa(i) {
				t.Fatalf("delta %d = %q, want fragment f%d", i, d.Delta, i)
			}
		}
		if startedAt < 0 {
			t.Fatal("no ToolStarted event")
		}
		for i, ev := range evs {
			if _, ok := ev.(types.ToolArgsDelta); ok && i > startedAt {
				t.Fatalf("ToolArgsDelta at %d after ToolStarted at %d", i, startedAt)
			}
		}
		if tool.ran != 1 {
			t.Fatalf("tool ran %d times, want 1", tool.ran)
		}
	})

	t.Run("streams.tool-args-delta-preview-only", func(t *testing.T) {
		m := &scriptTurns{turns: [][]types.ModelChunk{
			append(argFragments("c1", "record", 3),
				types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: &types.ToolUse{ID: "c1", Name: "record", Args: jsontext.Value(`{"y":2}`)}, Finish: types.FinishToolUse}),
			{turnTextChunk("done")},
		}}
		var evs []types.Event
		tool := &argsRecorder{}
		cfg := turnConfig{
			model:    m.model(),
			assemble: turnAssemble(nil),
			tools:    turnToolset(tool),
			maxTurns: 4,
		}
		if _, err := driveTurnCollect(func(y func(types.Event, error) bool) {
			driveTurns(types.WithSink(ctx, allSink(&evs)), cfg, y)
		}); err != nil {
			t.Fatalf("drive: %v", err)
		}
		var started bool
		for _, ev := range evs {
			if _, ok := ev.(types.ToolStarted); ok {
				started = true
				break
			}
			if d, ok := ev.(types.ToolArgsDelta); ok && d.CallID == "c1" {
				continue
			}
		}
		if !started {
			t.Fatal("no ToolStarted event")
		}
		if tool.ran != 1 {
			t.Fatalf("tool ran %d times, want 1", tool.ran)
		}
		if len(tool.args) != 1 || string(tool.args[0]) != `{"y":2}` {
			t.Fatalf("tool executed with %v, want the complete arguments only", tool.args)
		}
	})

	t.Run("preview-never-executed-as-arguments", func(t *testing.T) {
		m := &scriptTurns{turns: [][]types.ModelChunk{
			append(argFragments("c1", "record", 3),
				types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: &types.ToolUse{ID: "c1", Name: "record", Args: jsontext.Value(`{"complete":true}`)}, Finish: types.FinishToolUse}),
			{turnTextChunk("done")},
		}}
		tool := &argsRecorder{}
		cfg := turnConfig{
			model:    m.model(),
			assemble: turnAssemble(nil),
			tools:    turnToolset(tool),
			maxTurns: 4,
		}
		if _, err := driveTurnCollect(func(y func(types.Event, error) bool) {
			driveTurns(types.WithSink(ctx, noopSink()), cfg, y)
		}); err != nil {
			t.Fatalf("drive: %v", err)
		}
		if tool.ran != 1 {
			t.Fatalf("tool ran %d times, want 1", tool.ran)
		}
		want := jsontext.Value(`{"complete":true}`)
		if len(tool.args) != 1 || string(tool.args[0]) != string(want) {
			t.Fatalf("tool executed with %v, want the complete block %s", tool.args, want)
		}
	})

	t.Run("tools.args-validated-at-completion", func(t *testing.T) {
		m := &scriptTurns{turns: [][]types.ModelChunk{
			append(argFragments("c1", "record", 3),
				types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: &types.ToolUse{ID: "c1", Name: "record", Args: jsontext.Value(`{"a":1,"a":2}`)}, Finish: types.FinishToolUse}),
			{turnTextChunk("ok")},
		}}
		var evs []types.Event
		tool := &argsRecorder{}
		cfg := turnConfig{
			model:    m.model(),
			assemble: turnAssemble(nil),
			tools:    turnToolset(tool),
			maxTurns: 4,
		}
		if _, err := driveTurnCollect(func(y func(types.Event, error) bool) {
			driveTurns(types.WithSink(ctx, allSink(&evs)), cfg, y)
		}); err != nil {
			t.Fatalf("drive: %v", err)
		}
		if tool.ran != 0 {
			t.Fatalf("tool ran %d times, want 0", tool.ran)
		}
		var started, finished int
		var fin types.ToolFinished
		for _, ev := range evs {
			switch e := ev.(type) {
			case types.ToolStarted:
				started++
			case types.ToolFinished:
				finished++
				fin = e
			}
		}
		if started != 0 {
			t.Fatalf("ToolStarted emitted %d times, want 0", started)
		}
		if finished != 1 {
			t.Fatalf("ToolFinished emitted %d times, want 1", finished)
		}
		if fin.Result.Outcome != types.Failed || fin.Result.Error == nil || fin.Result.Error.Kind != types.Permanent {
			t.Fatalf("ToolFinished result = %+v, want Failed(Permanent)", fin.Result)
		}
	})

	t.Run("streams.tool-args-delta-coalesced-in-log", func(t *testing.T) {
		now := time.Unix(0, 0)
		clock := func() time.Time { return now }
		co := NewDeltaCoalescer(DefaultEventLogCoalesce, clock)
		for i := 0; i < 40; i++ {
			if out := co.Add(types.ToolArgsDelta{Turn: 1, CallID: "c1", Name: "record", Delta: "f" + strconv.Itoa(i)}); out != nil {
				t.Fatalf("fragment %d flushed early: %v", i, out)
			}
		}
		out := co.Flush()
		if len(out) != 1 {
			t.Fatalf("flush = %d events, want 1", len(out))
		}
		d, ok := out[0].(types.ToolArgsDelta)
		if !ok {
			t.Fatalf("coalesced record is %T, want ToolArgsDelta", out[0])
		}
		var want strings.Builder
		for i := range 40 {
			want.WriteString("f" + strconv.Itoa(i))
		}
		if d.Delta != want.String() {
			t.Fatalf("coalesced delta = %q, want %q", d.Delta, want.String())
		}

		log := stores.NewMemoryEventLog()
		logSink := sinkFunc(func(_ context.Context, e types.Event) {
			_ = log.Append(context.Background(), "r1", stores.Event{Payload: e})
		})
		m := &scriptTurns{turns: [][]types.ModelChunk{
			append(argFragments("c1", "record", 40),
				types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: &types.ToolUse{ID: "c1", Name: "record", Args: jsontext.Value(`{}`)}, Finish: types.FinishToolUse}),
			{turnTextChunk("done")},
		}}
		tool := &argsRecorder{}
		cfg := turnConfig{
			model:    m.model(),
			assemble: turnAssemble(nil),
			tools:    turnToolset(tool),
			maxTurns: 4,
			coalesce: NewDeltaCoalescer(0, clock),
		}
		if _, err := driveTurnCollect(func(y func(types.Event, error) bool) {
			driveTurns(types.WithSink(ctx, logSink), cfg, y)
		}); err != nil {
			t.Fatalf("drive: %v", err)
		}
		var seqs []int64
		var argRecords []types.ToolArgsDelta
		for ev := range log.Read(context.Background(), "r1", 0) {
			seqs = append(seqs, ev.Meta.Seq)
			if d, ok := ev.Payload.(types.ToolArgsDelta); ok {
				argRecords = append(argRecords, d)
			}
		}
		if len(argRecords) != 1 {
			t.Fatalf("log holds %d ToolArgsDelta records, want 1", len(argRecords))
		}
		if argRecords[0].CallID != "c1" {
			t.Fatalf("coalesced record = %+v, want call c1", argRecords[0])
		}
		for i, s := range seqs {
			if s != int64(i+1) {
				t.Fatalf("Seq at %d = %d, want %d", i, s, i+1)
			}
		}
	})

	t.Run("coalescing-one-record-per-completed-call", func(t *testing.T) {
		clock := func() time.Time { return time.Unix(0, 0) }
		log := stores.NewMemoryEventLog()
		logSink := sinkFunc(func(_ context.Context, e types.Event) {
			_ = log.Append(context.Background(), "r1", stores.Event{Payload: e})
		})
		m := &scriptTurns{turns: [][]types.ModelChunk{
			append(
				append(argFragments("c1", "record", 5), argFragments("c2", "record", 7)...),
				types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: &types.ToolUse{ID: "c1", Name: "record", Args: jsontext.Value(`{}`)}},
				types.ModelChunk{Kind: types.DeltaToolArgs, ToolUse: &types.ToolUse{ID: "c2", Name: "record", Args: jsontext.Value(`{}`)}, Finish: types.FinishToolUse},
			),
			{turnTextChunk("done")},
		}}
		tool := &argsRecorder{}
		cfg := turnConfig{
			model:    m.model(),
			assemble: turnAssemble(nil),
			tools:    turnToolset(tool),
			maxTurns: 4,
			coalesce: NewDeltaCoalescer(0, clock),
		}
		if _, err := driveTurnCollect(func(y func(types.Event, error) bool) {
			driveTurns(types.WithSink(ctx, logSink), cfg, y)
		}); err != nil {
			t.Fatalf("drive: %v", err)
		}
		perCall := map[string]int{}
		for ev := range log.Read(context.Background(), "r1", 0) {
			if d, ok := ev.Payload.(types.ToolArgsDelta); ok {
				perCall[d.CallID]++
			}
		}
		if len(perCall) != 2 || perCall["c1"] != 1 || perCall["c2"] != 1 {
			t.Fatalf("log records per call = %v, want one per completed call", perCall)
		}
	})

	t.Run("result-delta-preview-before-done", func(t *testing.T) {
		m := &scriptTurns{turns: [][]types.ModelChunk{
			{turnTextChunk("hi")},
		}}
		var evs []types.Event
		cfg := turnConfig{
			model:    m.model(),
			assemble: turnAssemble(nil),
			maxTurns: 2,
			resultPreview: func(msg types.Message) []string {
				return []string{`{"text":"`, "hi\"}"}
			},
		}
		doneAt := -1
		if _, err := driveTurnCollect(func(y func(types.Event, error) bool) {
			driveTurns(types.WithSink(ctx, allSink(&evs)), cfg, y)
		}); err != nil {
			t.Fatalf("drive: %v", err)
		}
		var deltas []types.ResultDelta
		for i, ev := range evs {
			switch e := ev.(type) {
			case types.ResultDelta:
				deltas = append(deltas, e)
			case types.Done:
				if doneAt < 0 {
					doneAt = i
				}
			}
		}
		if len(deltas) != 2 {
			t.Fatalf("ResultDelta previews = %d, want 2", len(deltas))
		}
		var sb strings.Builder
		for _, d := range deltas {
			if d.MessageID != "assistant-1" {
				t.Fatalf("ResultDelta message = %q, want assistant-1", d.MessageID)
			}
			sb.WriteString(d.Delta)
		}
		if sb.String() != `{"text":"hi"}` {
			t.Fatalf("preview payload = %q", sb.String())
		}
		if doneAt >= 0 {
			for i, ev := range evs {
				if _, ok := ev.(types.ResultDelta); ok && i > doneAt {
					t.Fatalf("ResultDelta at %d after Done at %d", i, doneAt)
				}
			}
		}
	})
}
