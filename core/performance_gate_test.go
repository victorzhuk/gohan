package gohan

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPerformanceGate drives tools/performance_gate.go against throwaway git
// repositories, exercising the offline measure-and-compare path and the
// identity-keyed verdict cache. Benchmarks use 1000 iterations per op where
// allocation counts are judged so a stray runtime allocation cannot pollute
// the exact per-op numbers; latency fixtures use few iterations of a sleep so
// the chain-overhead gap dominates the frozen 20 µs budget deterministically.
func TestPerformanceGate(t *testing.T) {
	t.Run("performance.regression-gate", func(t *testing.T) {
		fixture := benchFixtureRepo(t, "regress")
		cache := t.TempDir()
		bl := writeFixtureBaselines(t, fixture, "gated-regress")

		out, code := runGate(t, fixture, cache, "1000x", false, bl)
		if code != 1 {
			t.Fatalf("gate exit code = %d, want 1\n%s", code, out)
		}
		if !strings.Contains(out, "BenchmarkToolChain_ReadOnly") || !strings.Contains(out, "allocations increased") {
			t.Fatalf("output must name the benchmark and the allocation regression\n%s", out)
		}

		// The cache identity includes the effective benchtime, so a second
		// run with a garbage benchtime must re-measure (and fail on the
		// measurement), never silently reuse the cached verdict.
		out, code = runGate(t, fixture, cache, "garbage", false, bl)
		if code == 0 {
			t.Fatalf("garbage benchtime run must fail\n%s", out)
		}
		if strings.Contains(out, "cache hit") {
			t.Fatalf("different benchtime must not reuse the cached verdict\n%s", out)
		}
	})

	t.Run("pass within tolerance", func(t *testing.T) {
		fixture := benchFixtureRepo(t, "same")
		bl := writeFixtureBaselines(t, fixture, "gated-same")
		out, code := runGate(t, fixture, t.TempDir(), "1000x", false, bl)
		if code != 0 {
			t.Fatalf("gate exit code = %d, want 0\n%s", code, out)
		}
		if !strings.Contains(out, "verdict: PASS") {
			t.Fatalf("output must report a pass\n%s", out)
		}
	})

	// Stable fixture whose chain overhead is far above the frozen 20 µs
	// budget on every axis the budget touches: advisory mode must PASS,
	// strict mode must FAIL, and the strict run must not reuse the
	// advisory verdict cached for the same commit pair.
	t.Run("overbudget stable advisory then strict", func(t *testing.T) {
		fixture := benchFixtureRepo(t, "slowchain")
		cache := t.TempDir()
		bl := writeFixtureBaselines(t, fixture, "gated-slow")

		out, code := runGate(t, fixture, cache, "1000x", false, bl)
		if code != 0 || !strings.Contains(out, "verdict: PASS") {
			t.Fatalf("advisory run must pass on an overbudget stable fixture\ncode=%d\n%s", code, out)
		}

		out, code = runGate(t, fixture, cache, "1000x", true, bl)
		if code != 1 {
			t.Fatalf("strict run exit code = %d, want 1\n%s", code, out)
		}
		if strings.Contains(out, "cache hit") {
			t.Fatalf("advisory PASS must never satisfy a strict latency run\n%s", out)
		}
		if !strings.Contains(out, "tool-chain overhead") || !strings.Contains(out, "exceeds budget") {
			t.Fatalf("strict run must name the overhead budget failure\n%s", out)
		}
	})

	t.Run("missing side fails naming benchmark", func(t *testing.T) {
		fixture := benchFixtureRepo(t, "skipchain")
		bl := writeFixtureBaselines(t, fixture, "gated-skip")
		out, code := runGate(t, fixture, t.TempDir(), "10x", false, bl)
		if code != 1 {
			t.Fatalf("gate exit code = %d, want 1\n%s", code, out)
		}
		if !strings.Contains(out, "BenchmarkToolChain_ReadOnly") || !strings.Contains(out, "missing in head") {
			t.Fatalf("output must name the benchmark missing from head\n%s", out)
		}
	})

	t.Run("zero sample fails naming benchmark", func(t *testing.T) {
		fixture := benchFixtureRepo(t, "zerosample")
		bl := writeFixtureBaselines(t, fixture, "gated-zero")
		out, code := runGate(t, fixture, t.TempDir(), "10x", false, bl)
		if code != 1 {
			t.Fatalf("gate exit code = %d, want 1\n%s", code, out)
		}
		if !strings.Contains(out, "BenchmarkToolCall_Raw") || !strings.Contains(out, "zero ns/op") {
			t.Fatalf("output must name the benchmark with a zero sample\n%s", out)
		}
	})

	t.Run("empty or invalid baselines rejected", func(t *testing.T) {
		fixture := benchFixtureRepo(t, "same")
		bl := writeFixtureBaselines(t, fixture, "gated-empty")
		if out, code := runGate(t, fixture, t.TempDir(), "5x", false, bl); code == 0 {
			t.Fatalf("empty gated set must fail\n%s", out)
		}
	})
}

const fixtureBenchBase = `package benchfixture

import (
	"fmt"
	"testing"
	"time"
)

var sink int

var zeroSample = %ZEROSAMPLE%

var _ = time.Second

// burnChain spins for d so the chain op's overhead is far above the frozen
// budget without any allocation jitter from runtime timers.
func burnChain(d time.Duration) {
	start := time.Now()
	for time.Since(start) < d {
		sink++
	}
}

func BenchmarkToolCall_Raw(b *testing.B) {
	if zeroSample {
		fmt.Println("BenchmarkToolCall_Raw-8  100  0.00 ns/op  0 B/op  0 allocs/op")
	}
	for b.Loop() {
		sink = 1
	}
}

func BenchmarkToolChain_ReadOnly(b *testing.B) {
	for b.Loop() {
%s
	}
}
`

// benchFixtureRepo builds a two-commit git repo holding the two gated
// benchmarks. The head commit applies the named variant:
//
//	same        identical commits
//	regress     one extra allocation per chain op, an exact-axis regression
//	slowchain   chain op sleeps 200 µs: stable, far above the 20 µs budget
//	skipchain   head skips BenchmarkToolChain_ReadOnly: no samples
//	zerosample  head prints a 0.00 ns/op line for BenchmarkToolCall_Raw
func benchFixtureRepo(t *testing.T, variant string) string {
	t.Helper()
	dir := t.TempDir()
	gitDir(t, dir, "init", "-q", ".")
	gitDir(t, dir, "config", "user.email", "gate@example.com")
	gitDir(t, dir, "config", "user.name", "gate")
	writeFixtureBench(t, dir, "base", variant)
	gitDir(t, dir, "add", "-A")
	gitDir(t, dir, "commit", "-qm", "chore: fixture base")
	writeFixtureBench(t, dir, "head", variant)
	gitDir(t, dir, "add", "-A")
	gitDir(t, dir, "commit", "--allow-empty", "-qm", "chore: fixture head")
	return dir
}

func writeFixtureBench(t *testing.T, dir, phase, variant string) {
	t.Helper()
	head := phase == "head"
	zero := head && variant == "zerosample"
	body := strings.Replace(fixtureBenchBase, "%ZEROSAMPLE%", map[bool]string{true: "true", false: "false"}[zero], 1)
	switch {
	case head && variant == "regress":
		body = strings.Replace(body, "\t\tsink = 1\n", "\t\tsink = 1\n\t\tsinkBytes = make([]byte, 8)\n", 1)
		body = strings.Replace(body, "var sink int\n", "var sink int\n\nvar sinkBytes []byte\n", 1)
		body = strings.Replace(body, "%s", "\t\tsink = 1\n", 1)
	case variant == "slowchain":
		body = strings.Replace(body, "%s", "\t\tburnChain(200 * time.Microsecond)\n", 1)
	case head && variant == "skipchain":
		body = strings.Replace(body, "%s", "\t\tb.Skip(\"fixture: no chain samples\")\n", 1)
	default:
		body = strings.Replace(body, "%s", "\t\tsink = 1\n", 1)
	}
	if err := os.WriteFile(filepath.Join(dir, "bench_test.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	gomod := "module benchfixture\n\ngo 1.25\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeFixtureBaselines writes a baselines file into the fixture and returns
// its absolute path for the gate's --baselines flag. Variants shrinking the
// gated set exist only to prove validation rejects them.
func writeFixtureBaselines(t *testing.T, dir, variant string) string {
	t.Helper()
	gated := `["BenchmarkToolChain_ReadOnly", "BenchmarkToolCall_Raw"]`
	switch variant {
	case "gated-empty":
		gated = `[]`
	}
	body := `{
  "version": 1,
  "budgets": {"tool_chain_overhead_us": 20, "model_chain_overhead_us": 50},
  "gated": ` + gated + `
}`
	path := filepath.Join(dir, "performance_baselines.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func gitDir(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var buf strings.Builder
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, buf.String())
	}
}

// runGate invokes the gate through the module at the repo root. latency
// selects strict mode (-latency); otherwise the run is advisory.
func runGate(t *testing.T, repo, cache, benchtime string, latency bool, baselines string) (string, int) {
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
	args := []string{"run", "./tools/performance_gate.go",
		"--repo", repo, "--cache", cache, "--benchtime", benchtime,
		"--baselines", baselines,
		"--base", strings.TrimSpace(string(base)), "--head", strings.TrimSpace(string(head))}
	if latency {
		args = append(args, "--latency")
	}
	cmd = exec.Command("go", args...)
	cmd.Dir = root
	var buf strings.Builder
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
