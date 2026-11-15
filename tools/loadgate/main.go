// Command loadgate gates the noisy-neighbor load bound beside the benchmark
// gate. It loads core/performance_baselines.json, requires a positive
// noisy_neighbor.max_ratio, and obtains measurements either from --input or
// by running the frozen TestLoadNoisyNeighbor suite in ./core. The suite logs
// one machine-readable LOADRATIO <json> line per measurement; every line is
// parsed and judged against the frozen max_ratio. Over-limit ratios fail in
// strict mode (--latency or the reference runner) and report advisory
// otherwise. Missing or invalid measurements always fail. No network, no
// writes.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"
)

const (
	testName   = "TestLoadNoisyNeighbor"
	ratioToken = "LOADRATIO "
)

type baselines struct {
	NoisyNeighbor struct {
		MaxRatio float64 `json:"max_ratio"`
	} `json:"noisy_neighbor"`
}

type measurement struct {
	Run          string  `json:"run"`
	IsolatedNS   int64   `json:"isolated_ns"`
	LoadedNS     int64   `json:"loaded_ns"`
	Ratio        float64 `json:"ratio"`
	MaxStepRatio float64 `json:"max_step_ratio"`
	Limit        float64 `json:"limit"`
	Steps        int     `json:"steps"`
}

type config struct {
	baselinesPath string
	input         string
	latency       bool
	timeout       string
	require       string
	runSuite      func() (string, error)
}

func main() {
	fs := flag.NewFlagSet("loadgate", flag.ContinueOnError)
	cfg := &config{}
	fs.StringVar(&cfg.baselinesPath, "baselines", "core/performance_baselines.json", "baselines file path")
	fs.StringVar(&cfg.input, "input", "", "measurement file instead of running the suite")
	fs.BoolVar(&cfg.latency, "latency", false, "strict mode: fail on over-limit ratios")
	fs.StringVar(&cfg.timeout, "timeout", "10m", "go test timeout for the suite run")
	fs.StringVar(&cfg.require, "require", "neighbor", "comma-separated run names the measurements must cover exactly")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	cfg.latency = cfg.latency || os.Getenv("GOHAN_PERF_REFERENCE") == "1"
	if err := run(os.Stdout, cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(out io.Writer, cfg *config) error {
	bl, err := loadBaselines(cfg.baselinesPath)
	if err != nil {
		return err
	}
	meas, err := collect(cfg)
	if err != nil {
		return err
	}
	if len(meas) == 0 {
		return fmt.Errorf("missing measurement: %s produced no %sline", testName, ratioToken)
	}
	names, err := requireSet(cfg.require)
	if err != nil {
		return err
	}
	want := map[string]bool{}
	for _, n := range names {
		if want[n] {
			return fmt.Errorf("--require: duplicate run %q", n)
		}
		want[n] = true
	}
	seen := map[string]int{}
	for _, m := range meas {
		if err := m.validate(bl.NoisyNeighbor.MaxRatio); err != nil {
			return err
		}
		seen[m.Run]++
	}
	for _, m := range meas {
		if !want[m.Run] {
			return fmt.Errorf("measurement %q: run not in --require %q", m.Run, cfg.require)
		}
	}
	for _, n := range names {
		switch seen[n] {
		case 0:
			return fmt.Errorf("missing measurement for required run %q", n)
		case 1:
		default:
			return fmt.Errorf("duplicate measurement for run %q", n)
		}
	}
	verdict := true
	for _, m := range meas {
		if m.Ratio <= bl.NoisyNeighbor.MaxRatio {
			continue
		}
		delta := (m.Ratio/bl.NoisyNeighbor.MaxRatio - 1) * 100
		if cfg.latency {
			_, _ = fmt.Fprintf(out, "load gate: %s: ratio %g > limit %g (+%.2f%%) (worst step %g)\n", m.Run, m.Ratio, bl.NoisyNeighbor.MaxRatio, delta, m.MaxStepRatio)
			verdict = false
		} else {
			_, _ = fmt.Fprintf(out, "load gate: advisory: %s: ratio %g > limit %g (+%.2f%%) (worst step %g)\n", m.Run, m.Ratio, bl.NoisyNeighbor.MaxRatio, delta, m.MaxStepRatio)
		}
	}
	if !verdict {
		return errors.New("load gate: over-limit measurement in strict mode")
	}
	return nil
}

func loadBaselines(path string) (*baselines, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading baselines %q: %w", path, err)
	}
	var bl baselines
	if err := json.Unmarshal(data, &bl); err != nil {
		return nil, fmt.Errorf("parsing baselines %q: %w", path, err)
	}
	if bl.NoisyNeighbor.MaxRatio <= 0 {
		return nil, fmt.Errorf("baselines %q: noisy_neighbor.max_ratio must be > 0, got %g", path, bl.NoisyNeighbor.MaxRatio)
	}
	return &bl, nil
}

func collect(cfg *config) ([]measurement, error) {
	var text string
	if cfg.input != "" {
		data, err := os.ReadFile(cfg.input)
		if err != nil {
			return nil, fmt.Errorf("reading input %q: %w", cfg.input, err)
		}
		text = string(data)
	} else {
		var err error
		if cfg.runSuite != nil {
			text, err = cfg.runSuite()
		} else {
			cmd := exec.Command("go", "test", "-run", "^"+testName+"$", "-v", "-count=1", "-timeout", cfg.timeout, "./core/")
			var out []byte
			out, err = cmd.CombinedOutput()
			text = string(out)
		}
		if err != nil {
			subset, perr := parse(text)
			if perr == nil && len(subset) > 0 {
				return nil, fmt.Errorf("running %s: %w: %d measurements parsed before failure: %s",
					testName, err, len(subset), tail(text, 400))
			}
			return nil, fmt.Errorf("running %s: %w: %s", testName, err, tail(text, 400))
		}
	}
	return parse(text)
}

// tail returns the last n bytes of s for failure reports.
func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "... " + s[len(s)-n:]
}

func requireSet(spec string) ([]string, error) {
	var names []string
	for _, p := range strings.Split(spec, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, fmt.Errorf("--require: empty run name in %q", spec)
		}
		names = append(names, p)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("--require: no run names in %q", spec)
	}
	return names, nil
}

func parse(text string) ([]measurement, error) {
	var meas []measurement
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		i := strings.Index(sc.Text(), ratioToken)
		if i < 0 {
			continue
		}
		var m measurement
		if err := json.Unmarshal([]byte(sc.Text()[i+len(ratioToken):]), &m); err != nil {
			return nil, fmt.Errorf("parsing LOADRATIO line: %w", err)
		}
		meas = append(meas, m)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scanning output: %w", err)
	}
	return meas, nil
}

func (m measurement) validate(frozenLimit float64) error {
	if m.Run == "" {
		return errors.New("measurement: run must be non-empty")
	}
	if m.Ratio <= 0 {
		return fmt.Errorf("measurement %q: ratio must be > 0, got %g", m.Run, m.Ratio)
	}
	if m.MaxStepRatio <= 0 {
		return fmt.Errorf("measurement %q: max_step_ratio must be > 0, got %g", m.Run, m.MaxStepRatio)
	}
	if m.IsolatedNS <= 0 {
		return fmt.Errorf("measurement %q: isolated_ns must be > 0, got %d", m.Run, m.IsolatedNS)
	}
	if m.LoadedNS <= 0 {
		return fmt.Errorf("measurement %q: loaded_ns must be > 0, got %d", m.Run, m.LoadedNS)
	}
	if m.Steps <= 0 {
		return fmt.Errorf("measurement %q: steps must be > 0, got %d", m.Run, m.Steps)
	}
	if m.Limit != frozenLimit {
		return fmt.Errorf("measurement %q: limit %g does not match frozen noisy_neighbor.max_ratio %g; measurement came from a different baselines revision", m.Run, m.Limit, frozenLimit)
	}
	computed := float64(m.LoadedNS) / float64(m.IsolatedNS)
	if math.Abs(m.Ratio-computed)/computed > 0.01 {
		return fmt.Errorf("measurement %q: ratio %g does not match loaded_ns/isolated_ns %g", m.Run, m.Ratio, computed)
	}
	return nil
}
