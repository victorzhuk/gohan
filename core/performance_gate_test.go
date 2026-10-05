package gohan

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPerformanceGate drives tools/performance_gate.go against throwaway git
// repositories, exercising the offline measure-and-compare path and the
// commit-pair verdict cache. The fixture runs 1000 iterations per op so a
// stray runtime allocation cannot pollute the exact per-op counts: allocs/op
// is a process-wide delta over the measurement window, and at one iteration a
// single stray allocation reads as a false regression.
func TestPerformanceGate(t *testing.T) {
	t.Run("performance.regression-gate", func(t *testing.T) {
		fixture := benchFixtureRepo(t, false)
		cache := t.TempDir()

		out, code := runGate(t, fixture, cache, "1000x")
		if code != 1 {
			t.Fatalf("gate exit code = %d, want 1\n%s", code, out)
		}
		if !strings.Contains(out, "BenchmarkFixture") || !strings.Contains(out, "allocations increased") {
			t.Fatalf("output must name the benchmark and the allocation regression\n%s", out)
		}

		// A cached verdict is reused without measuring again: the invalid
		// benchtime would fail any real measurement.
		out, code = runGate(t, fixture, cache, "garbage")
		if code != 1 {
			t.Fatalf("cached gate exit code = %d, want 1\n%s", code, out)
		}
		if !strings.Contains(out, "cache hit") {
			t.Fatalf("second run must reuse the cached verdict\n%s", out)
		}
	})

	t.Run("pass within tolerance", func(t *testing.T) {
		fixture := benchFixtureRepo(t, true)
		out, code := runGate(t, fixture, t.TempDir(), "1000x")
		if code != 0 {
			t.Fatalf("gate exit code = %d, want 0\n%s", code, out)
		}
		if !strings.Contains(out, "verdict: PASS") {
			t.Fatalf("output must report a pass\n%s", out)
		}
	})
}

// benchFixtureRepo builds a two-commit git repo holding one benchmark. When
// same is true both commits are identical; otherwise the head commit adds one
// allocation per op, a deterministic exact-axis regression.
func benchFixtureRepo(t *testing.T, same bool) string {
	t.Helper()
	dir := t.TempDir()
	gitDir(t, dir, "init", "-q", ".")
	gitDir(t, dir, "config", "user.email", "gate@example.com")
	gitDir(t, dir, "config", "user.name", "gate")
	writeBench(t, dir, false)
	gitDir(t, dir, "add", "-A")
	gitDir(t, dir, "commit", "-qm", "chore: fixture base")
	if same {
		gitDir(t, dir, "commit", "--allow-empty", "-qm", "chore: fixture head")
	} else {
		writeBench(t, dir, true)
		gitDir(t, dir, "add", "-A")
		gitDir(t, dir, "commit", "-qm", "chore: fixture head")
	}
	return dir
}

func writeBench(t *testing.T, dir string, slow bool) {
	t.Helper()
	body := "package benchfixture\n\nimport \"testing\"\n\nvar sink []byte\n\nfunc BenchmarkFixture(b *testing.B) {\n\tfor b.Loop() {\n"
	if slow {
		body += "\t\tsink = make([]byte, 8)\n"
	}
	body += "\t}\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "bench_test.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	gomod := "module benchfixture\n\ngo 1.25\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitDir(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, buf.String())
	}
}

// runGate invokes the gate through the module at the repo root, mirroring the
// task bench:gate target.
func runGate(t *testing.T, repo, cache, benchtime string) (string, int) {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repo
	head, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("git", "rev-parse", "HEAD~1")
	cmd.Dir = repo
	base, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("go", "run", "./tools/performance_gate.go",
		"--repo", repo, "--cache", cache, "--benchtime", benchtime,
		"--base", strings.TrimSpace(string(base)), "--head", strings.TrimSpace(string(head)))
	cmd.Dir = root
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err = cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("gate run: %v\n%s", err, buf.String())
		}
		code = ee.ExitCode()
	}
	return buf.String(), code
}
