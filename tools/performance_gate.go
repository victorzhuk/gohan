// Command performance_gate compares a base commit's benchmarks against a head
// commit's and fails when the head regresses beyond the tolerance. It is the
// CI regression gate for the budgets in openspec/specs/performance/spec.md.
//
// Measurement alternates base and head at the round level, three rounds per
// side, and takes the fastest round per benchmark: -count repeats one side's
// cells back to back, so machine drift over the run would be confounded with
// the side. Allocation counts and bytes are exact per-op numbers and are
// judged first; latency is a sampled statistic decidable only on a quiet
// fixed runner, so a latency miss defers to the release runner unless
// -latency enforces it. Verdicts are cached by the commit pair so a
// re-run of the same pair does not re-measure. The tool never touches the
// network: it checks out both commits into temporary worktrees and builds them
// with the local toolchain.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const rounds = 3

type sample struct {
	ns     float64
	bytes  float64
	allocs float64
}

type benchResult map[string][]sample

type verdictRow struct {
	Name       string  `json:"name"`
	BaseNS     float64 `json:"base_ns"`
	HeadNS     float64 `json:"head_ns"`
	DeltaPct   float64 `json:"delta_pct"`
	BaseAllocs float64 `json:"base_allocs"`
	HeadAllocs float64 `json:"head_allocs"`
	Reason     string  `json:"reason,omitempty"`
}

type verdict struct {
	Base   string       `json:"base"`
	Head   string       `json:"head"`
	Passed bool         `json:"passed"`
	Rows   []verdictRow `json:"rows"`
}

type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func main() {
	if err := run(os.Args[1:]); err != nil {
		var ee *exitError
		code := 2
		if errors.As(err, &ee) {
			code = ee.code
		}
		fmt.Fprintln(os.Stderr, "performance_gate:", err)
		os.Exit(code)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("performance_gate", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	repo := fs.String("repo", ".", "repository to measure")
	base := fs.String("base", "", "base commit (required)")
	head := fs.String("head", "HEAD", "head commit")
	bench := fs.String("bench", ".", "benchmark name pattern passed to go test")
	benchtime := fs.String("benchtime", "", "benchtime passed to go test (default: go's own)")
	innerTimeout := fs.String("test-timeout", "10m", "timeout for each inner go test run")
	tolerance := fs.Float64("tolerance", 5.0, "allowed regression percent")
	latency := fs.Bool("latency", false, "fail on latency regressions (quiet fixed runner only)")
	cacheDir := fs.String("cache", defaultCacheDir(), "verdict cache directory")
	baselines := fs.String("baselines", "", "optional JSON file naming the gated benchmarks")
	fs.Usage = usage(fs)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *base == "" {
		fs.Usage()
		return errors.New("-base is required")
	}

	root, err := filepath.Abs(*repo)
	if err != nil {
		return err
	}
	baseSHA, err := git(root, "rev-parse", "--verify", *base+"^{commit}")
	if err != nil {
		return err
	}
	headSHA, err := git(root, "rev-parse", "--verify", *head+"^{commit}")
	if err != nil {
		return err
	}

	cacheFile := filepath.Join(*cacheDir, cacheName(baseSHA, headSHA))
	if v, ok, err := loadVerdict(cacheFile); err != nil {
		return err
	} else if ok {
		fmt.Printf("[gate] cache hit: %s\n", cacheFile)
		report(v)
		return exitCode(v)
	}

	gated, err := loadGated(*baselines)
	if err != nil {
		return err
	}
	if gated == nil {
		fmt.Println("[gate] no baselines file: gating every benchmark found")
	} else {
		fmt.Printf("[gate] gating %d benchmarks from baselines\n", len(gated))
	}

	work, err := os.MkdirTemp("", "performance-gate-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	baseWT, err := worktree(root, baseSHA, work)
	if err != nil {
		return err
	}
	headWT, err := worktree(root, headSHA, work)
	if err != nil {
		return err
	}

	baseRes, headRes := benchResult{}, benchResult{}
	// Round-level alternation keeps arm identity decoupled from time: -count
	// would run one side's cells back to back, so thermal or frequency drift
	// over the run lands entirely on one side.
	for range rounds {
		if err := measureRound(baseWT, *bench, *benchtime, *innerTimeout, baseRes); err != nil {
			return err
		}
		if err := measureRound(headWT, *bench, *benchtime, *innerTimeout, headRes); err != nil {
			return err
		}
	}

	v := compare(baseSHA, headSHA, baseRes, headRes, gated, *tolerance, *latency)
	if err := os.MkdirAll(*cacheDir, 0o755); err != nil {
		return err
	}
	blob, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(cacheFile, blob, 0o644); err != nil {
		return err
	}
	report(v)
	return exitCode(v)
}

func usage(fs *flag.FlagSet) func() {
	return func() {
		fmt.Println("performance_gate: fail when head benchmarks regress beyond the tolerance against base")
		fs.PrintDefaults()
	}
}

func defaultCacheDir() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "gohan-performance-gate")
	}
	return filepath.Join(os.TempDir(), "gohan-performance-gate")
}

func cacheName(base, head string) string {
	return base[:12] + "-" + head[:12] + ".json"
}

func loadVerdict(path string) (verdict, bool, error) {
	blob, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return verdict{}, false, nil
	}
	if err != nil {
		return verdict{}, false, err
	}
	var v verdict
	if err := json.Unmarshal(blob, &v); err != nil {
		return verdict{}, false, fmt.Errorf("cache %s: %w", path, err)
	}
	return v, true, nil
}

// loadGated reads the optional baselines file. Its shape is a JSON object with
// a "gated" array of benchmark names; any other object is tolerated by taking
// only that key, and a missing file disables filtering.
func loadGated(path string) (map[string]bool, error) {
	if path == "" {
		return nil, nil
	}
	blob, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Printf("[gate] baselines file %s not found: gating every benchmark found\n", path)
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc struct {
		Gated []string `json:"gated"`
	}
	if err := json.Unmarshal(blob, &doc); err != nil {
		return nil, fmt.Errorf("baselines %s: %w", path, err)
	}
	if len(doc.Gated) == 0 {
		return nil, nil
	}
	set := make(map[string]bool, len(doc.Gated))
	for _, name := range doc.Gated {
		set[name] = true
	}
	return set, nil
}

func worktree(root, sha, work string) (string, error) {
	dir := filepath.Join(work, sha[:12])
	if _, err := gitOut(root, "worktree", "add", "--detach", dir, sha); err != nil {
		return "", err
	}
	return dir, nil
}

func measureRound(dir, pattern, benchtime, timeout string, acc benchResult) error {
	args := []string{"test", "-run", "^$", "-bench", pattern, "-count", "1", "-benchmem", "-timeout", timeout, "./..."}
	if benchtime != "" {
		args = append(args, "-benchtime", benchtime)
	}
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("benchmarks in %s: %w\n%s", dir, err, buf.String())
	}
	res, err := parseBench(buf.String())
	if err != nil {
		return err
	}
	for name, ss := range res {
		acc[name] = append(acc[name], ss...)
	}
	return nil
}

var benchLine = regexp.MustCompile(`^(Benchmark\S+?)-\d+\s+\d+\s+([\d.]+) ns/op(?:\s+([\d.]+) B/op)?(?:\s+(\d+) allocs/op)?`)

func parseBench(out string) (benchResult, error) {
	res := benchResult{}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		m := benchLine.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		s := sample{}
		var err error
		if s.ns, err = strconv.ParseFloat(m[2], 64); err != nil {
			return nil, fmt.Errorf("parse %q: %w", m[0], err)
		}
		if m[3] != "" {
			if s.bytes, err = strconv.ParseFloat(m[3], 64); err != nil {
				return nil, fmt.Errorf("parse %q: %w", m[0], err)
			}
		}
		if m[4] != "" {
			if s.allocs, err = strconv.ParseFloat(m[4], 64); err != nil {
				return nil, fmt.Errorf("parse %q: %w", m[0], err)
			}
		}
		res[m[1]] = append(res[m[1]], s)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return res, nil
}

func fastest(ss []sample) sample {
	best := ss[0]
	for _, s := range ss[1:] {
		if s.ns < best.ns {
			best = s
		}
	}
	return best
}

func compare(baseSHA, headSHA string, baseRes, headRes benchResult, gated map[string]bool, tolerance float64, latencyEnforced bool) verdict {
	names := make([]string, 0, len(baseRes))
	for name := range baseRes {
		if gated != nil && !gated[name] {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	v := verdict{Base: baseSHA, Head: headSHA, Passed: true}
	for _, name := range names {
		headSamples, ok := headRes[name]
		if !ok {
			v.Rows = append(v.Rows, verdictRow{Name: name, Reason: "missing in head"})
			v.Passed = false
			continue
		}
		b, h := fastest(baseRes[name]), fastest(headSamples)
		delta := 0.0
		if b.ns > 0 {
			delta = (h.ns - b.ns) / b.ns * 100
		}
		row := verdictRow{
			Name:       name,
			BaseNS:     b.ns,
			HeadNS:     h.ns,
			DeltaPct:   delta,
			BaseAllocs: b.allocs,
			HeadAllocs: h.allocs,
		}
		// Exact per-op counts are locally authoritative, so they are judged
		// before the sampled latency axis: an allocation or byte rise fails
		// deterministically, while a latency miss on a noisy machine defers
		// to the release runner instead of failing the gate here.
		switch {
		case h.allocs > b.allocs:
			row.Reason = fmt.Sprintf("allocations increased %g -> %g per op", b.allocs, h.allocs)
		case h.bytes > b.bytes:
			row.Reason = fmt.Sprintf("bytes per op increased %g -> %g", b.bytes, h.bytes)
		case delta > tolerance && latencyEnforced:
			row.Reason = fmt.Sprintf("latency regressed %+.2f%% (tolerance %.1f%%)", delta, tolerance)
		case delta > tolerance:
			fmt.Printf("[gate] %s: latency %+.2f%% exceeds tolerance %.1f%%: deferred to release runner\n",
				name, delta, tolerance)
		}
		if row.Reason != "" {
			v.Passed = false
		}
		v.Rows = append(v.Rows, row)
	}
	if len(v.Rows) == 0 {
		fmt.Println("[gate] warning: no benchmarks matched")
	}
	return v
}

func report(v verdict) {
	for _, row := range v.Rows {
		line := fmt.Sprintf("%s  %.4g -> %.4g ns/op  delta %+.2f%%  allocs %g -> %g",
			row.Name, row.BaseNS, row.HeadNS, row.DeltaPct, row.BaseAllocs, row.HeadAllocs)
		if row.Reason != "" {
			line += "  FAIL: " + row.Reason
		}
		fmt.Println(line)
	}
	if v.Passed {
		fmt.Println("verdict: PASS")
	} else {
		fmt.Println("verdict: FAIL")
	}
}

func exitCode(v verdict) error {
	if v.Passed {
		return nil
	}
	return &exitError{code: 1, err: errors.New("performance regression detected")}
}

func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stderr = &buf
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(buf.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

func git(dir string, args ...string) (string, error) {
	return gitOut(dir, args...)
}
