package exec

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type countTel struct {
	mu     sync.Mutex
	counts map[string]int64
	attrs  map[string][]types.Attr
}

func newCountTel() *countTel {
	return &countTel{counts: map[string]int64{}, attrs: map[string][]types.Attr{}}
}

func (t *countTel) Count(_ context.Context, name string, n int64, attrs ...types.Attr) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.counts[name] += n
	t.attrs[name] = append(t.attrs[name], attrs...)
}

func (t *countTel) StartSpan(ctx context.Context, _ string, _ ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}

func (t *countTel) Record(context.Context, string, float64, ...types.Attr) {}

func (t *countTel) total(name string) int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.counts[name]
}

func outputRunInfo() types.RunInfo {
	return types.RunInfo{SessionID: "s1", RunID: "r1", RootRunID: "r1"}
}

func smallCapper(tel types.Telemetry, store stores.OutputStore) OutputCapper {
	return OutputCapper{Max: 1024, Store: store, Tel: tel, Run: outputRunInfo(), Tool: "sh"}
}

func TestExecOutput(t *testing.T) {
	ctx := context.Background()

	t.Run("tools.output-cap", func(t *testing.T) {
		tel := newCountTel()
		c := smallCapper(tel, stores.NewMemoryOutputs())
		data := strings.Repeat("a", 2048)
		blocks, ref, err := c.Cap(ctx, "stdout", []byte(data))
		if err != nil {
			t.Fatalf("Cap: %v", err)
		}
		if ref == "" {
			t.Fatal("output over the cap must be stored and referenced")
		}
		text, ok := blocks[0].(types.Text)
		if !ok {
			t.Fatalf("expected a Text block, got %T", blocks[0])
		}
		if !strings.Contains(text.Text, "truncated at 1024 bytes, 1024 bytes dropped") {
			t.Fatalf("marker missing the cap and the dropped count: %q", text.Text[len(text.Text)-120:])
		}
		if len(text.Text) > 1024+200 {
			t.Fatalf("inline text not capped: %d bytes", len(text.Text))
		}
	})

	t.Run("tools.large-output-stored", func(t *testing.T) {
		tel := newCountTel()
		store := stores.NewMemoryOutputs()
		c := smallCapper(tel, store)
		data := strings.Repeat("x", 200*1024)
		blocks, ref, err := c.Cap(ctx, "stdout", []byte(data))
		if err != nil {
			t.Fatalf("Cap: %v", err)
		}
		if ref == "" {
			t.Fatal("expected a stored ref for output over the cap")
		}
		text := blocks[0].(types.Text)
		if !strings.Contains(text.Text, data[:64]) {
			t.Fatal("head excerpt missing")
		}
		if !strings.Contains(text.Text, ref) {
			t.Fatal("marker does not name the stored ref")
		}
		full, err := store.Get(ctx, ref)
		if err != nil {
			t.Fatalf("store.Get: %v", err)
		}
		var msg types.Message
		if err := json.Unmarshal(full, &msg); err != nil {
			t.Fatalf("decode stored blocks: %v", err)
		}
		stored := msg.Blocks[0].(types.Text).Text
		if stored != data {
			t.Fatalf("stored content differs: %d bytes, want %d", len(stored), len(data))
		}
		if got := tel.total(OutputStoredMetric); got != 1 {
			t.Fatalf("%s = %d, want 1", OutputStoredMetric, got)
		}
	})

	t.Run("stderr and stdout cap independently", func(t *testing.T) {
		tel := newCountTel()
		c := smallCapper(tel, stores.NewMemoryOutputs())
		data := strings.Repeat("e", 4096)
		outBlocks, _, err := c.Cap(ctx, "stdout", []byte(data))
		if err != nil {
			t.Fatalf("Cap stdout: %v", err)
		}
		errBlocks, _, err := c.Cap(ctx, "stderr", []byte(data))
		if err != nil {
			t.Fatalf("Cap stderr: %v", err)
		}
		outText := outBlocks[0].(types.Text).Text
		errText := errBlocks[0].(types.Text).Text
		if !strings.Contains(outText, "[stdout truncated") || !strings.Contains(errText, "[stderr truncated") {
			t.Fatal("each stream must carry its own truncation marker")
		}
		if len(outText) > 1024+200 || len(errText) > 1024+200 {
			t.Fatalf("caps not independent: stdout %d, stderr %d inline bytes", len(outText), len(errText))
		}
	})

	t.Run("under the cap carries no marker", func(t *testing.T) {
		tel := newCountTel()
		c := smallCapper(tel, stores.NewMemoryOutputs())
		blocks, ref, err := c.Cap(ctx, "stdout", []byte("hello"))
		if err != nil {
			t.Fatalf("Cap: %v", err)
		}
		if ref != "" {
			t.Fatalf("under-cap output must not be stored, got ref %q", ref)
		}
		text := blocks[0].(types.Text)
		if text.Text != "hello" {
			t.Fatalf("under-cap output changed: %q", text.Text)
		}
		if strings.Contains(text.Text, "truncated") {
			t.Fatal("marker on output under the cap")
		}
		if got := tel.total(OutputStoredMetric); got != 0 {
			t.Fatalf("%s = %d, want 0", OutputStoredMetric, got)
		}
	})
}
