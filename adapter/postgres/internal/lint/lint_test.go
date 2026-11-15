package lint

import (
	"go/token"
	"testing"
)

func TestLint(t *testing.T) {
	t.Run("stores.transaction-across-model-call", func(t *testing.T) {
		ok, err := Check("testdata/ok")
		if err != nil {
			t.Fatalf("Check(testdata/ok): %v", err)
		}
		if len(ok) != 0 {
			t.Fatalf("expected no findings in testdata/ok, got %+v", ok)
		}

		bad, err := Check("testdata/bad")
		if err != nil {
			t.Fatalf("Check(testdata/bad): %v", err)
		}
		want := []Finding{
			{Pos: mkpos("testdata/bad/deferred_rollback.go", 9), Binding: "tx", Boundary: Invoke},
			{Pos: mkpos("testdata/bad/holding_bad.go", 25), Binding: "tx", Boundary: Invoke},
			{Pos: mkpos("testdata/bad/holding_bad.go", 34), Binding: "tx", Boundary: Call},
		}
		if len(bad) != len(want) {
			t.Fatalf("expected %d findings in testdata/bad, got %d: %+v", len(want), len(bad), bad)
		}
		for i, w := range want {
			got := bad[i]
			if got.Binding != w.Binding || got.Boundary != w.Boundary || got.Pos.Line != w.Pos.Line || got.Pos.Filename != w.Pos.Filename {
				t.Errorf("finding[%d]: got %+v, want %+v", i, got, w)
			}
		}
		// Same code path as cmd/pgtxlint: the command maps any finding
		// count > 0 to exit code 1.
		if len(bad) == 0 {
			t.Fatal("pgtxlint would exit 0, expected exit 1 on findings")
		}
	})
}

func mkpos(filename string, line int) token.Position {
	return token.Position{Filename: filename, Line: line}
}
