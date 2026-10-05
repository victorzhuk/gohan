package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree materialises a module pair as two directory trees. The comparison
// runs on the trees themselves, so the break is constructed by editing the
// exported surface between the two, not by asserting on this tool's own
// output.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDiffTrees(t *testing.T) {
	base := map[string]string{
		"go.mod":       "module example.com/m\n\ngo 1.27\n",
		"api.go":       "package m\n\ntype Store interface {\n\tGet(key string) (string, error)\n}\n\ntype Options struct {\n\tSize int\n}\n\nfunc New(opts Options) (*Store, error) { return nil, nil }\n\nfunc (s *Store) Flush(n int) error { return nil }\n\nconst MaxSize = 10\n",
		"util/util.go": "package util\n\nfunc Help() {}\n",
	}
	write := func(m map[string]string, name, body string) map[string]string {
		next := make(map[string]string, len(m)+1)
		for k, v := range m {
			next[k] = v
		}
		next[name] = body
		return next
	}

	drop := func(m map[string]string, name string) map[string]string {
		next := make(map[string]string, len(m)-1)
		for k, v := range m {
			if k != name {
				next[k] = v
			}
		}
		return next
	}

	cases := []struct {
		name string
		head map[string]string
		want []string
	}{
		{
			name: "identical surfaces",
			head: base,
		},
		{
			name: "additive growth",
			head: write(base, "extra.go", "package m\n\ntype Client struct{}\n\nfunc (c *Client) Ping() {}\n\nconst MinSize = 1\n"),
		},
		{
			name: "func signature changed",
			head: write(base, "api.go", strings.Replace(base["api.go"], "func New(opts Options)", "func New(size int, opts Options)", 1)),
			want: []string{"func New: changed from func(opts Options)(*Store, error) to func(size int, opts Options)(*Store, error)"},
		},
		{
			name: "exported func removed",
			head: write(base, "api.go", strings.Replace(base["api.go"], "func New(opts Options) (*Store, error) { return nil, nil }\n\n", "", 1)),
			want: []string{"func New: removed"},
		},
		{
			name: "method removed from interface",
			head: write(base, "api.go", strings.Replace(base["api.go"], "\tGet(key string) (string, error)\n", "", 1)),
			want: []string{"type Store: changed from interface{Get func(key string)(string, error)}"},
		},
		{
			name: "struct field type changed",
			head: write(base, "api.go", strings.Replace(base["api.go"], "Size int", "Size int64", 1)),
			want: []string{"type Options: changed from struct{Size int}"},
		},
		{
			name: "method receiver widened to pointer",
			head: write(base, "api.go", strings.Replace(base["api.go"], "func (s *Store) Flush", "func (s Store) Flush", 1)),
			want: []string{"method (*Store) Flush: removed"},
		},
		{
			name: "package removed",
			head: drop(base, "util/util.go"),
			want: []string{"util: func Help: package removed"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			head := writeTree(t, tc.head)
			got, err := diffTrees(writeTree(t, base), head)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("diffTrees() = %v, want %d break(s) %v", got, len(tc.want), tc.want)
			}
			for i, want := range tc.want {
				if !strings.Contains(string(got[i]), want) {
					t.Errorf("break %q does not mention %q", got[i], want)
				}
			}
		})
	}
}

func TestDiffTreesUnexportedChangeCompatible(t *testing.T) {
	base := writeTree(t, map[string]string{
		"api.go": "package m\n\ntype Options struct {\n\tsize int\n\tSize int\n}\n",
	})
	head := writeTree(t, map[string]string{
		"api.go": "package m\n\ntype Options struct {\n\tsize int64\n\tSize int\n}\n",
	})
	got, err := diffTrees(base, head)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("unexported field change reported: %v", got)
	}
}
