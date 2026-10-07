package sandbox

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/outputs"
)

type bigWorkspace struct {
	*fakeWorkspace
	truncated bool
}

func (w bigWorkspace) Exec(ctx context.Context, req types.ExecRequest) (types.ExecResult, error) {
	return types.ExecResult{Stdout: []byte("0123456789ABCDEF"), Truncated: w.truncated}, nil
}

func newBig(listable bool, truncated bool) (*fakeSandbox, *fakeWorkspace) {
	w := &fakeWorkspace{listable: listable}
	sb := &fakeSandbox{ws: bigWorkspace{fakeWorkspace: w, truncated: truncated}}
	return sb, w
}

func readBack(t *testing.T, store stores.OutputStore, ref string) string {
	t.Helper()
	ro, err := newReadOutputForTest(store)
	if err != nil {
		t.Fatalf("read_output tool: %v", err)
	}
	raw, err := json.Marshal(map[string]string{"ref": ref})
	if err != nil {
		t.Fatalf("marshal read_output args: %v", err)
	}
	res, err := ro.Call(context.Background(), raw)
	if err != nil {
		t.Fatalf("read_output: %v", err)
	}
	if len(res.Content) != 1 {
		t.Fatalf("content = %d blocks, want 1", len(res.Content))
	}
	txt, ok := res.Content[0].(types.Text)
	if !ok {
		t.Fatalf("content[0] = %T, want types.Text", res.Content[0])
	}
	return txt.Text
}

func newReadOutputForTest(store stores.OutputStore) (types.Tool, error) {
	return outputs.NewReadOutput(store)
}

func TestOutputSpill(t *testing.T) {
	t.Run("overflow to store", func(t *testing.T) {
		sb, _ := newBig(false, false)
		store := stores.NewMemoryOutputs()
		ts, err := New(sb, types.SandboxPolicy{Lifecycle: types.PerCall}, WithOutputStore(store), WithMaxOutput(8))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		res, err := ts.Tools()[0].Call(context.Background(), args(t, runCodeArgs{Code: "x"}))
		if err != nil {
			t.Fatalf("run_code: %v", err)
		}
		if len(res.Content) != 2 {
			t.Fatalf("content = %d blocks, want head excerpt plus File", len(res.Content))
		}
		head, ok := res.Content[0].(types.Text)
		if !ok || !strings.Contains(head.Text, "01234567") || !strings.Contains(head.Text, "truncated") {
			t.Errorf("head = %#v, want the 8-byte excerpt with a truncation marker", res.Content[0])
		}
		file, ok := res.Content[1].(types.File)
		if !ok || file.Blob.Ref == "" {
			t.Fatalf("content[1] = %#v, want a File block with a ref", res.Content[1])
		}
		if got := readBack(t, store, file.Blob.Ref); got != "0123456789ABCDEF" {
			t.Errorf("read_output = %q, want the full stream", got)
		}
	})
	t.Run("truncated flag spills fitting bytes", func(t *testing.T) {
		sb, _ := newBig(false, true)
		store := stores.NewMemoryOutputs()
		ts, err := New(sb, types.SandboxPolicy{Lifecycle: types.PerCall}, WithOutputStore(store))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		res, err := ts.Tools()[0].Call(context.Background(), args(t, runCodeArgs{Code: "x"}))
		if err != nil {
			t.Fatalf("run_code: %v", err)
		}
		var file types.File
		for _, b := range res.Content {
			if f, ok := b.(types.File); ok {
				file = f
			}
		}
		if file.Blob.Ref == "" {
			t.Fatalf("content = %#v, want a File block: the adapter hit its cap", res.Content)
		}
		if got := readBack(t, store, file.Blob.Ref); got != "0123456789ABCDEF" {
			t.Errorf("read_output = %q, want the delivered bytes", got)
		}
	})
	t.Run("fitting output stays inline", func(t *testing.T) {
		sb, _ := newBig(false, false)
		store := stores.NewMemoryOutputs()
		ts, err := New(sb, types.SandboxPolicy{Lifecycle: types.PerCall}, WithOutputStore(store))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		res, err := ts.Tools()[0].Call(context.Background(), args(t, runCodeArgs{Code: "x"}))
		if err != nil {
			t.Fatalf("run_code: %v", err)
		}
		if len(res.Content) != 1 {
			t.Fatalf("content = %d blocks, want one inline text block", len(res.Content))
		}
	})
}

func TestReturnedFileToStore(t *testing.T) {
	sb, _ := newFake(false)
	store := stores.NewMemoryOutputs()
	ts, err := New(sb, types.SandboxPolicy{Lifecycle: types.PerCall}, WithOutputStore(store), WithMaxOutput(4))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res, err := ts.Tools()[2].Call(context.Background(), args(t, readFileArgs{Path: "data.csv"}))
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	var file types.File
	for _, b := range res.Content {
		if f, ok := b.(types.File); ok {
			file = f
		}
	}
	if file.Blob.Ref == "" {
		t.Fatalf("content = %#v, want a File block with a ref", res.Content)
	}
	if got := readBack(t, store, file.Blob.Ref); got != "file body" {
		t.Errorf("read_output = %q, want the full file body", got)
	}

	sb, _ = newFake(false)
	ts, err = New(sb, types.SandboxPolicy{Lifecycle: types.PerCall})
	if err != nil {
		t.Fatalf("New without store: %v", err)
	}
	res, err = ts.Tools()[2].Call(context.Background(), args(t, readFileArgs{Path: "data.csv"}))
	if err != nil {
		t.Fatalf("read_file without store: %v", err)
	}
	if len(res.Content) != 1 {
		t.Fatalf("content = %d blocks, want one inline text block", len(res.Content))
	}
}
