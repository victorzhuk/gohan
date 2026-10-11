package outputs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type countTel struct {
	mu     sync.Mutex
	counts map[string]int64
}

func (t *countTel) Count(_ context.Context, name string, n int64, _ ...types.Attr) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.counts == nil {
		t.counts = map[string]int64{}
	}
	t.counts[name] += n
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

type failingStore struct{ err error }

func (s failingStore) Put(context.Context, types.RunInfo, []types.Block) (string, error) {
	return "", s.err
}

func (s failingStore) Get(context.Context, string) ([]byte, error) {
	return nil, stores.ErrOutputNotFound
}

func storeRunInfo() types.RunInfo {
	return types.RunInfo{SessionID: "s1", RunID: "r1", RootRunID: "r1"}
}

func storedText(t *testing.T, store stores.OutputStore, ref string) string {
	t.Helper()
	data, err := store.Get(context.Background(), ref)
	if err != nil {
		t.Fatalf("Get(%q): %v", ref, err)
	}
	var msg types.Message
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("decode stored blocks: %v", err)
	}
	var sb strings.Builder
	for _, b := range msg.Blocks {
		if txt, ok := b.(types.Text); ok {
			sb.WriteString(txt.Text)
		}
	}
	return sb.String()
}

func TestStoreOutputCapBoundary(t *testing.T) {
	store := stores.NewMemoryOutputs()
	tel := &countTel{}
	ctx := types.WithTelemetry(context.Background(), tel)

	blocks, ref, err := StoreOutput(ctx, OutputStoreParams{
		Store: store, Run: storeRunInfo(), Tool: "sh", Max: 8, Stream: "stdout",
		Data: []byte("exactly8"), Tel: tel,
	})
	if err != nil {
		t.Fatalf("StoreOutput: %v", err)
	}
	if ref != "" {
		t.Fatalf("fitting output ref = %q, want empty", ref)
	}
	if len(blocks) != 1 || blocks[0].(types.Text).Text != "exactly8" {
		t.Fatalf("fitting output blocks = %v, want the text unchanged", blocks)
	}
	if got := tel.total("gohan.output.stored"); got != 0 {
		t.Fatalf("gohan.output.stored = %d, want 0", got)
	}

	blocks, ref, err = StoreOutput(ctx, OutputStoreParams{
		Store: store, Run: storeRunInfo(), Tool: "sh", Max: 8, Stream: "stdout",
		Data: []byte("exactly8!"), Tel: tel,
	})
	if err != nil {
		t.Fatalf("StoreOutput: %v", err)
	}
	if ref == "" {
		t.Fatal("one byte over the cap produced no ref")
	}
	head := blocks[0].(types.Text).Text
	if !strings.HasPrefix(head, "exactly8") || !strings.Contains(head,
		"[stdout truncated at 8 bytes, 1 bytes dropped; full output ref: "+ref+"]") {
		t.Fatalf("overflow head = %q, want excerpt plus marker", head)
	}
	if got := tel.total("gohan.output.stored"); got != 1 {
		t.Fatalf("gohan.output.stored = %d, want 1", got)
	}
	if stored := storedText(t, store, ref); stored != "exactly8!" {
		t.Fatalf("stored document = %q, want the whole output", stored)
	}
}

func TestStoreOutputNoStore(t *testing.T) {
	blocks, ref, err := StoreOutput(context.Background(), OutputStoreParams{
		Max: 8, Stream: "stdout", Data: []byte("exactly8!"),
	})
	if err != nil {
		t.Fatalf("StoreOutput: %v", err)
	}
	if ref != "" {
		t.Fatalf("no-store ref = %q, want empty", ref)
	}
	head := blocks[0].(types.Text).Text
	if head != "exactly8\n[stdout truncated at 8 bytes, 1 bytes dropped; no output store configured]" {
		t.Fatalf("no-store head = %q, want bounded excerpt plus missing-store marker", head)
	}
}

func TestStoreOutputEqualBytesOneRef(t *testing.T) {
	store := stores.NewMemoryOutputs()
	params := OutputStoreParams{
		Store: store, Run: storeRunInfo(), Tool: "sh", Max: 4, Stream: "stdout",
		Data: []byte("same-bytes"),
	}
	_, ref1, err := StoreOutput(context.Background(), params)
	if err != nil {
		t.Fatalf("first StoreOutput: %v", err)
	}
	_, ref2, err := StoreOutput(context.Background(), params)
	if err != nil {
		t.Fatalf("second StoreOutput: %v", err)
	}
	if ref1 == "" || ref1 != ref2 {
		t.Fatalf("equal bytes produced refs %q and %q, want one ref", ref1, ref2)
	}
}

func TestStoreOutputForceStoreFittingBytes(t *testing.T) {
	store := stores.NewMemoryOutputs()
	tel := &countTel{}
	blocks, ref, err := StoreOutput(types.WithTelemetry(context.Background(), tel), OutputStoreParams{
		Store: store, Run: storeRunInfo(), Tool: "sh", Max: 64, Stream: "stdout",
		Data: []byte("fits"), ForceStore: true, Tel: tel,
	})
	if err != nil {
		t.Fatalf("StoreOutput: %v", err)
	}
	if ref == "" {
		t.Fatal("forced store returned no ref")
	}
	if text := blocks[0].(types.Text).Text; text != "fits" || strings.Contains(text, "truncated") {
		t.Fatalf("fitting forced store blocks = %v, want the whole text and no marker", blocks)
	}
	if stored := storedText(t, store, ref); stored != "fits" {
		t.Fatalf("stored document = %q, want the whole output", stored)
	}
	if got := tel.total("gohan.output.stored"); got != 1 {
		t.Fatalf("gohan.output.stored = %d, want 1", got)
	}
}

func TestStoreOutputSanitizerOwnership(t *testing.T) {
	store := stores.NewMemoryOutputs()
	tel := &countTel{}
	ctx := types.WithTelemetry(context.Background(), tel)
	sanitized := SanitizeFunc(func(_ context.Context, _ string, data []byte) ([]byte, int, error) {
		return []byte("clean"), 2, nil
	})

	blocks, ref, err := StoreOutput(ctx, OutputStoreParams{
		Store: store, Run: storeRunInfo(), Tool: "sh", Max: 64, Stream: "stdout",
		Data: []byte("raw-secret"), Sanitize: sanitized, ForceStore: true, Tel: tel,
	})
	if err != nil {
		t.Fatalf("StoreOutput: %v", err)
	}
	if got := tel.total("gohan.sandbox.secret_leak"); got != 2 {
		t.Fatalf("gohan.sandbox.secret_leak = %d, want 2", got)
	}
	if text := blocks[0].(types.Text).Text; text != "clean" || strings.Contains(text, "raw-secret") {
		t.Fatalf("blocks = %v, want sanitized bytes with no raw spill", blocks)
	}
	if stored := storedText(t, store, ref); strings.Contains(stored, "raw-secret") || stored != "clean" {
		t.Fatalf("stored document = %q, want sanitized bytes only", stored)
	}

	// A second pass over already-sanitized bytes contributes zero leaks, so
	// each occurrence is counted once.
	noop := SanitizeFunc(func(_ context.Context, _ string, data []byte) ([]byte, int, error) {
		return data, 0, nil
	})
	_, _, err = StoreOutput(ctx, OutputStoreParams{
		Store: store, Run: storeRunInfo(), Tool: "sh", Max: 64, Stream: "stdout",
		Data: []byte("clean"), Sanitize: noop, ForceStore: true, Tel: tel,
	})
	if err != nil {
		t.Fatalf("second StoreOutput: %v", err)
	}
	if got := tel.total("gohan.sandbox.secret_leak"); got != 2 {
		t.Fatalf("gohan.sandbox.secret_leak after second pass = %d, want 2", got)
	}
}

func TestStoreOutputSanitizeError(t *testing.T) {
	store := stores.NewMemoryOutputs()
	sanErr := errors.New("sanitize failed")
	sanitized := SanitizeFunc(func(context.Context, string, []byte) ([]byte, int, error) {
		return nil, 0, sanErr
	})
	blocks, ref, err := StoreOutput(context.Background(), OutputStoreParams{
		Store: store, Run: storeRunInfo(), Tool: "sh", Max: 4, Stream: "stdout",
		Data: []byte("raw"), Sanitize: sanitized,
	})
	if !errors.Is(err, sanErr) {
		t.Fatalf("StoreOutput err = %v, want %v", err, sanErr)
	}
	if blocks != nil || ref != "" {
		t.Fatalf("sanitize error returned blocks %v ref %q, want none", blocks, ref)
	}
}

func TestStoreOutputPutError(t *testing.T) {
	putErr := errors.New("put failed")
	blocks, ref, err := StoreOutput(context.Background(), OutputStoreParams{
		Store: failingStore{err: putErr}, Run: storeRunInfo(), Tool: "sh", Max: 4, Stream: "stdout",
		Data: []byte("too many bytes"),
	})
	if !errors.Is(err, putErr) {
		t.Fatalf("StoreOutput err = %v, want %v", err, putErr)
	}
	if blocks != nil || ref != "" {
		t.Fatalf("Put error returned blocks %v ref %q, want none", blocks, ref)
	}
}

func TestStoreOutputDefaultCap(t *testing.T) {
	if DefaultMaxOutput != 64*1024 {
		t.Fatalf("DefaultMaxOutput = %d, want %d", DefaultMaxOutput, 64*1024)
	}
	data := strings.Repeat("a", DefaultMaxOutput+1)
	blocks, _, err := StoreOutput(context.Background(), OutputStoreParams{
		Stream: "stdout", Data: []byte(data),
	})
	if err != nil {
		t.Fatalf("StoreOutput: %v", err)
	}
	if !strings.Contains(blocks[0].(types.Text).Text,
		fmt.Sprintf("[stdout truncated at %d bytes", DefaultMaxOutput)) {
		t.Fatalf("zero Max did not resolve to DefaultMaxOutput: %q", blocks[0].(types.Text).Text[:80])
	}
}
