// Command performance_gate compares a base commit's benchmarks against a head
// commit's and fails when the head regresses beyond the tolerance. It is the
// CI regression gate for the budgets in openspec/specs/performance/spec.md.
//
// Measurement alternates base and head at the round level, three rounds per
// side, and takes the fastest round per benchmark: -count repeats one side's
// cells back to back, so machine drift over the run would be confounded with
// the side. Every gated benchmark must produce a nonempty sample in each
// round on both sides; a missing benchmark or a zero ns/op sample fails the
// run and names the benchmark.
//
// Allocation counts and bytes are exact per-op numbers and are judged first;
// they are authoritative on any machine. Latency is a sampled statistic:
// the chain-overhead budgets from the baselines file (fastest chain minus
// fastest raw comparator against the frozen µs budgets) and the relative
// regression are enforced only in strict mode (-latency, the reference
// runner); a local run reports them as advisory and can never satisfy a
// strict verdict, because the latency mode is part of the cache identity.
//
// Verdicts are cached under a versioned identity (schema2): full commit
// SHAs, benchmark selector, effective benchtime, inner timeout, round count,
// tolerance, latency mode, SHA256 digest of the validated baselines file,
// Go version, GOOS/GOARCH/GOMAXPROCS, and runner identity. A cache entry is
// read back only when its identity matches exactly; entries from earlier
// schemas are ignored. The baselines file is loaded and validated before the
// cache is consulted. The tool never touches the network: it checks out both
// commits into temporary worktrees and builds them with the local toolchain.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

const (
	rounds         = 3
	identitySchema = "schema2"
)

type sample struct {
	ns     float64
	bytes  float64
	allocs float64
}

type benchResult map[string][]sample

// baselines mirrors the gated subset of core/performance_baselines.json.
type baselines struct {
	Gated   []string `json:"gated"`
	Budgets struct {
		ToolChainOverheadUS  float64 `json:"tool_chain_overhead_us"`
		ModelChainOverheadUS float64 `json:"model_chain_overhead_us"`
	} `json:"budgets"`
}

// budgetPair freezes the pairing between each chain benchmark and its raw
// comparator; the µs budget itself comes from the baselines file.
type budgetPair struct {
	chain, raw, label string
	budgetUS          float64
}

func budgetPairs(b baselines) []budgetPair {
	return []budgetPair{
		{"BenchmarkToolChain_ReadOnly", "BenchmarkToolCall_Raw", "tool-chain overhead", b.Budgets.ToolChainOverheadUS},
		{"BenchmarkModelChain", "BenchmarkModelCall_Raw", "model-chain overhead", b.Budgets.ModelChainOverheadUS},
	}
}

// gateIdentity is the schema2 cache identity: a cached verdict is reused only
// when every field matches the current run exactly.
type gateIdentity struct {
	Schema          string  `json:"schema"`
	Base            string  `json:"base_sha"`
	Head            string  `json:"head_sha"`
	Selector        string  `json:"bench_selector"`
	Benchtime       string  `json:"benchtime"`
	Timeout         string  `json:"test_timeout"`
	Rounds          int     `json:"rounds"`
	Tolerance       float64 `json:"tolerance"`
	LatencyEnforced bool    `json:"latency_enforced"`
	BaselinesSHA    string  `json:"baselines_sha256"`
	GoVersion       string  `json:"go_version"`
	GOOS            string  `json:"goos"`
	GOARCH          string  `json:"goarch"`
	GOMAXPROCS      int     `json:"gomaxprocs"`
	Runner          string  `json:"runner"`
}

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
	Identity gateIdentity `json:"identity"`
	Passed   bool         `json:"passed"`
	Rows     []verdictRow `json:"rows"`
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
	benchtime := fs.String("benchtime", "1000x", "benchtime passed to go test (the gated chain benchmarks carry a MaxToolCalls limit, so a fixed iteration count is required)")
	innerTimeout := fs.String("test-timeout", "10m", "timeout for each inner go test run")
	tolerance := fs.Float64("tolerance", 5.0, "allowed regression percent")
	latency := fs.Bool("latency", false, "fail on latency regressions (quiet fixed runner only)")
	cacheDir := fs.String("cache", defaultCacheDir(), "verdict cache directory")
	baselinesPath := fs.String("baselines", "", "JSON baselines file naming the gated benchmarks and the latency budgets (required)")
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
	if *baselinesPath == "" {
		fs.Usage()
		return errors.New("-baselines is required")
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

	// The baselines file is loaded and validated before the cache is
	// consulted: the validated file's digest is part of the cache identity,
	// so a stale or invalid budget set can never bless a cached verdict.
	bl, baselinesSHA, err := loadBaselines(*baselinesPath)
	if err != nil {
		return err
	}
	fmt.Printf("[gate] gating %d benchmarks from %s (sha256 %s)\n", len(bl.Gated), *baselinesPath, baselinesSHA)

	benchtimeEff := *benchtime
	if benchtimeEff == "" {
		benchtimeEff = "default"
	}
	id := buildIdentity(baseSHA, headSHA, *bench, benchtimeEff, *innerTimeout, *tolerance, *latency, baselinesSHA)

	cacheFile := filepath.Join(*cacheDir, cacheName(id))
	if v, ok, err := loadVerdict(cacheFile, id); err != nil {
		return err
	} else if ok {
		fmt.Printf("[gate] cache hit: %s\n", cacheFile)
		report(v)
		return exitCode(v)
	}

	work, err := os.MkdirTemp("", "performance-gate-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	baseWT, err := worktree(root, "base", baseSHA, work)
	if err != nil {
		return err
	}
	headWT, err := worktree(root, "head", headSHA, work)
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

	v := compare(id, baseRes, headRes, *bl, *tolerance, *latency)
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

func buildIdentity(baseSHA, headSHA, selector, benchtime, timeout string, tolerance float64, latency bool, baselinesSHA string) gateIdentity {
	runner, err := os.Hostname()
	if err != nil || runner == "" {
		runner = "unknown"
	}
	if r := os.Getenv("RUNNER_NAME"); r != "" {
		runner = r
	}
	if img := os.Getenv("ImageOS"); img != "" {
		runner += "/" + img
	}
	return gateIdentity{
		Schema:          identitySchema,
		Base:            baseSHA,
		Head:            headSHA,
		Selector:        selector,
		Benchtime:       benchtime,
		Timeout:         timeout,
		Rounds:          rounds,
		Tolerance:       tolerance,
		LatencyEnforced: latency,
		BaselinesSHA:    baselinesSHA,
		GoVersion:       runtime.Version(),
		GOOS:            runtime.GOOS,
		GOARCH:          runtime.GOARCH,
		GOMAXPROCS:      runtime.GOMAXPROCS(0),
		Runner:          runner,
	}
}

func cacheName(id gateIdentity) string {
	blob, err := json.Marshal(id)
	if err != nil {
		panic(err) // struct of comparable builtin fields cannot fail to marshal
	}
	sum := sha256.Sum256(blob)
	return hex.EncodeToString(sum[:]) + ".json"
}

func loadVerdict(path string, want gateIdentity) (verdict, bool, error) {
	blob, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return verdict{}, false, nil
	}
	if err != nil {
		return verdict{}, false, err
	}
	var v verdict
	if err := json.Unmarshal(blob, &v); err != nil {
		fmt.Printf("[gate] ignoring unreadable cache entry %s: %v\n", path, err)
		return verdict{}, false, nil
	}
	if v.Identity.Schema != identitySchema {
		fmt.Printf("[gate] ignoring cache entry %s: schema %q, want %q\n", path, v.Identity.Schema, identitySchema)
		return verdict{}, false, nil
	}
	if v.Identity != want {
		fmt.Printf("[gate] ignoring cache entry %s: identity mismatch\n", path)
		return verdict{}, false, nil
	}
	return v, true, nil
}

// loadBaselines reads the baselines file, validates that the gated set is
// nonempty and both latency budgets are positive, and returns the file's
// SHA256 digest over its exact bytes.
func loadBaselines(path string) (*baselines, string, error) {
	blob, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf("baselines file %s not found", path)
	}
	if err != nil {
		return nil, "", err
	}
	var bl baselines
	if err := json.Unmarshal(blob, &bl); err != nil {
		return nil, "", fmt.Errorf("baselines %s: %w", path, err)
	}
	switch {
	case len(bl.Gated) == 0:
		return nil, "", fmt.Errorf("baselines %s: gated set is empty", path)
	case bl.Budgets.ToolChainOverheadUS <= 0:
		return nil, "", fmt.Errorf("baselines %s: tool_chain_overhead_us must be > 0", path)
	case bl.Budgets.ModelChainOverheadUS <= 0:
		return nil, "", fmt.Errorf("baselines %s: model_chain_overhead_us must be > 0", path)
	}
	sum := sha256.Sum256(blob)
	return &bl, hex.EncodeToString(sum[:]), nil
}

func worktree(root, label, sha, work string) (string, error) {
	dir := filepath.Join(work, label+"-"+sha[:12])
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
		if m[3] == "" || m[4] == "" {
			return nil, fmt.Errorf("benchmark line %q: missing B/op or allocs/op measurement; gated lines must come from a -benchmem run", m[0])
		}
		if s.bytes, err = strconv.ParseFloat(m[3], 64); err != nil {
			return nil, fmt.Errorf("parse %q: %w", m[0], err)
		}
		if s.allocs, err = strconv.ParseFloat(m[4], 64); err != nil {
			return nil, fmt.Errorf("parse %q: %w", m[0], err)
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

func hasZeroSample(ss []sample) bool {
	for _, s := range ss {
		if s.ns <= 0 {
			return true
		}
	}
	return false
}

func compare(id gateIdentity, baseRes, headRes benchResult, bl baselines, tolerance float64, latencyEnforced bool) verdict {
	names := append([]string(nil), bl.Gated...)
	sort.Strings(names)
	v := verdict{Identity: id, Passed: true}
	for _, name := range names {
		row := verdictRow{Name: name}
		bs, hs := baseRes[name], headRes[name]
		switch {
		case len(bs) == 0:
			row.Reason = "missing in base: no samples"
		case len(hs) == 0:
			row.Reason = "missing in head: no samples"
		case len(bs) < rounds:
			row.Reason = fmt.Sprintf("only %d of %d rounds produced base samples", len(bs), rounds)
		case len(hs) < rounds:
			row.Reason = fmt.Sprintf("only %d of %d rounds produced head samples", len(hs), rounds)
		case hasZeroSample(bs):
			row.Reason = "zero ns/op sample in base rounds"
		case hasZeroSample(hs):
			row.Reason = "zero ns/op sample in head rounds"
		default:
			b, h := fastest(bs), fastest(hs)
			delta := 0.0
			if b.ns > 0 {
				delta = (h.ns - b.ns) / b.ns * 100
			}
			row.BaseNS, row.HeadNS, row.DeltaPct = b.ns, h.ns, delta
			row.BaseAllocs, row.HeadAllocs = b.allocs, h.allocs
			// Exact per-op counts are locally authoritative, so they are
			// judged before the sampled latency axis: an allocation or byte
			// rise fails deterministically, while a latency miss on a noisy
			// machine defers to the reference runner unless -latency.
			switch {
			case h.allocs > b.allocs:
				row.Reason = fmt.Sprintf("allocations increased %g -> %g per op", b.allocs, h.allocs)
			case h.bytes > b.bytes:
				row.Reason = fmt.Sprintf("bytes per op increased %g -> %g", b.bytes, h.bytes)
			case delta > tolerance && latencyEnforced:
				row.Reason = fmt.Sprintf("latency regressed %+.2f%% (tolerance %.1f%%)", delta, tolerance)
			case delta > tolerance:
				fmt.Printf("[gate] %s: latency %+.2f%% exceeds tolerance %.1f%%: deferred to reference runner\n",
					name, delta, tolerance)
			}
		}
		if row.Reason != "" {
			v.Passed = false
		}
		v.Rows = append(v.Rows, row)
	}

	// Chain-overhead budgets: fastest chain minus fastest raw comparator on
	// head, against the frozen µs budgets from the baselines file. Enforced
	// only in strict mode; a local miss is advisory and never fails the run.
	for _, p := range budgetPairs(bl) {
		hc, hr := headRes[p.chain], headRes[p.raw]
		if len(hc) == 0 || len(hr) == 0 || hasZeroSample(hc) || hasZeroSample(hr) {
			continue // already failed per benchmark above
		}
		overheadUS := (fastest(hc).ns - fastest(hr).ns) / 1000
		if overheadUS > p.budgetUS {
			if latencyEnforced {
				v.Rows = append(v.Rows, verdictRow{
					Name:   fmt.Sprintf("overhead %s: %s - %s", p.label, p.chain, p.raw),
					Reason: fmt.Sprintf("%s %.1f µs exceeds budget %.1f µs", p.label, overheadUS, p.budgetUS),
				})
				v.Passed = false
			} else {
				fmt.Printf("[gate] %s: %.1f µs exceeds budget %.1f µs: advisory only, deferred to reference runner\n",
					p.label, overheadUS, p.budgetUS)
			}
			continue
		}
		if latencyEnforced {
			v.Rows = append(v.Rows, verdictRow{
				Name: fmt.Sprintf("overhead %s: %s - %s", p.label, p.chain, p.raw),
			})
		}
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
