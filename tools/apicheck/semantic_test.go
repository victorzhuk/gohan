package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// compileTree type-checks a fixture tree as a standalone module before any
// compatibility assertion runs. A compatibility miss must never be
// confounded with a fixture that fails to build: this gate turns a broken
// fixture into a fixture error, not into a misleading break report.
func compileTree(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("fixture tree does not compile: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
}

func mentionsBreak(breaks []breakage, substr string) bool {
	for _, b := range breaks {
		if strings.Contains(string(b), substr) {
			return true
		}
	}
	return false
}

// TestSemanticCompatibility pins the required semantic behavior of the
// compatibility checker over legal Go source pairs: identity and inferred
// types matter, textual qualified-name matches alone must not, and keyed
// struct growth is compatible.
func TestSemanticCompatibility(t *testing.T) {
	const mod = "module example.com/m\n\ngo 1.27\n"

	t.Run("generic method removal is breaking", func(t *testing.T) {
		base := writeTree(t, map[string]string{
			"go.mod": mod,
			"api.go": "package m\n\ntype Box[T any] struct {\n\tvalue T\n}\n\nfunc (b *Box[T]) Get() T { return b.value }\n\nfunc (b *Box[T]) Put(v T) { b.value = v }\n",
		})
		head := writeTree(t, map[string]string{
			"go.mod": mod,
			"api.go": "package m\n\ntype Box[T any] struct {\n\tvalue T\n}\n\nfunc (b *Box[T]) Put(v T) { b.value = v }\n",
		})
		compileTree(t, base)
		compileTree(t, head)
		got, err := diffTrees(base, head)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 {
			t.Fatalf("removal of generic method (Box[T]) Get reported as compatible; want a break naming method Get on the generic receiver")
		}
		if !mentionsBreak(got, "Get") {
			t.Fatalf("break for removal of method Get on generic receiver Box[T] not reported; got %v", got)
		}
	})

	t.Run("inferred variable type change is breaking", func(t *testing.T) {
		base := writeTree(t, map[string]string{
			"go.mod": mod,
			"api.go": "package m\n\nfunc floor() int { return 1 }\n\nvar Threshold = floor()\n",
		})
		head := writeTree(t, map[string]string{
			"go.mod": mod,
			"api.go": "package m\n\nfunc floor() int64 { return 1 }\n\nvar Threshold = floor()\n",
		})
		compileTree(t, base)
		compileTree(t, head)
		got, err := diffTrees(base, head)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 {
			t.Fatalf("exported var Threshold inferring int changed to inferring int64 reported as compatible; want a break naming var Threshold and its inferred type change")
		}
		if !mentionsBreak(got, "Threshold") {
			t.Fatalf("break for inferred type change of var Threshold not reported; got %v", got)
		}
	})

	t.Run("imported type identity change is breaking", func(t *testing.T) {
		base := writeTree(t, map[string]string{
			"go.mod":         mod,
			"api.go":         "package m\n\nimport \"example.com/m/alpha\"\n\nfunc Make() alpha.Coder { return alpha.Coder{Name: \"x\"} }\n",
			"alpha/alpha.go": "package alpha\n\ntype Coder struct {\n\tName string\n}\n",
			"beta/beta.go":   "package beta\n\ntype Coder struct {\n\tTitle string\n}\n",
		})
		head := writeTree(t, map[string]string{
			"go.mod":         mod,
			"api.go":         "package m\n\nimport \"example.com/m/beta\"\n\nfunc Make() beta.Coder { return beta.Coder{Title: \"x\"} }\n",
			"alpha/alpha.go": "package alpha\n\ntype Coder struct {\n\tName string\n}\n",
			"beta/beta.go":   "package beta\n\ntype Coder struct {\n\tTitle string\n}\n",
		})
		compileTree(t, base)
		compileTree(t, head)
		got, err := diffTrees(base, head)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 {
			t.Fatalf("func Make result type identity changed from alpha.Coder to beta.Coder reported as compatible; want a break naming both identities")
		}
		if !mentionsBreak(got, "Make") || !mentionsBreak(got, "alpha.Coder") || !mentionsBreak(got, "beta.Coder") {
			t.Fatalf("break naming func Make identity change alpha.Coder -> beta.Coder not reported; got %v", got)
		}
	})

	t.Run("additive keyed struct growth is compatible", func(t *testing.T) {
		base := writeTree(t, map[string]string{
			"go.mod": mod,
			"api.go": "package m\n\ntype Event struct {\n\tKind string\n}\n\nfunc Emit(e Event) {}\n",
		})
		head := writeTree(t, map[string]string{
			"go.mod": mod,
			"api.go": "package m\n\ntype Event struct {\n\tKind string\n\tAt   string\n}\n\nfunc Emit(e Event) {}\n",
		})
		compileTree(t, base)
		compileTree(t, head)
		got, err := diffTrees(base, head)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("addition of keyed field At to struct Event reported as breaking: %v; want keyed additive field growth accepted", got)
		}
	})
}
