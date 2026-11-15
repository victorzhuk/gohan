package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixtures(t *testing.T, baselines, input string) (basePath, inputPath string) {
	t.Helper()
	basePath = filepath.Join(t.TempDir(), "baselines.json")
	if err := os.WriteFile(basePath, []byte(baselines), 0o600); err != nil {
		t.Fatal(err)
	}
	inputPath = filepath.Join(t.TempDir(), "input.txt")
	if err := os.WriteFile(inputPath, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	return basePath, inputPath
}

const goodBaselines = `{"noisy_neighbor": {"max_ratio": 2.5}}`

const goodInput = `some log noise
--- LOADRATIO {"run":"neighbor","isolated_ns":100,"loaded_ns":190,"ratio":1.9,"max_step_ratio":2.1,"limit":2.5,"steps":4}
more noise
`

const overLimitInput = "LOADRATIO " + `{"run":"neighbor","isolated_ns":100,"loaded_ns":320,"ratio":3.2,"max_step_ratio":3.2,"limit":2.5,"steps":4}` + "\n"

func TestRun(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		baselines string
		input     string
		latency   bool
		require   string
		suite     func() (string, error)
		wantErr   string
		wantErr2  string
		wantOut   string
	}{
		{
			name:      "within limit strict passes",
			baselines: goodBaselines,
			input:     goodInput,
			latency:   true,
		},
		{
			name:      "worst step over limit but mean within passes strict",
			baselines: goodBaselines,
			input:     "LOADRATIO " + `{"run":"neighbor","isolated_ns":100,"loaded_ns":190,"ratio":1.9,"max_step_ratio":4.0,"limit":2.5,"steps":4}` + "\n",
			latency:   true,
		},
		{
			name:      "mean over limit strict fails naming run and delta",
			baselines: goodBaselines,
			input:     "LOADRATIO " + `{"run":"neighbor","isolated_ns":100,"loaded_ns":300,"ratio":3.0,"max_step_ratio":2.4,"limit":2.5,"steps":4}` + "\n",
			latency:   true,
			wantErr:   "load gate: over-limit measurement in strict mode",
			wantOut:   "load gate: neighbor: ratio 3 > limit 2.5 (+20.00%) (worst step 2.4)",
		},
		{
			name:      "ratio over limit strict fails naming run and delta",
			baselines: goodBaselines,
			input:     overLimitInput,
			latency:   true,
			wantErr:   "load gate: over-limit measurement in strict mode",
			wantOut:   "load gate: neighbor: ratio 3.2 > limit 2.5 (+28.00%) (worst step 3.2)",
		},
		{
			name:      "ratio over limit advisory passes",
			baselines: goodBaselines,
			input:     overLimitInput,
			wantOut:   "load gate: advisory: neighbor: ratio 3.2 > limit 2.5 (+28.00%) (worst step 3.2)",
		},
		{
			name:      "missing max_step_ratio fails",
			baselines: goodBaselines,
			input:     "LOADRATIO " + `{"run":"neighbor","isolated_ns":100,"loaded_ns":190,"ratio":1.9,"limit":2.5,"steps":4}` + "\n",
			latency:   true,
			wantErr:   "max_step_ratio must be > 0",
		},
		{
			name:      "missing key fails",
			baselines: `{"noisy_neighbor": {"max_ratio": 0}}`,
			input:     goodInput,
			latency:   true,
			wantErr:   `baselines`,
			wantErr2:  "noisy_neighbor.max_ratio",
		},
		{
			name:      "missing line fails",
			baselines: goodBaselines,
			input:     "no measurements here\n",
			latency:   true,
			wantErr:   "missing measurement",
		},
		{
			name:      "malformed JSON fails",
			baselines: goodBaselines,
			input:     "LOADRATIO {not json}\n",
			latency:   true,
			wantErr:   "parsing LOADRATIO line",
		},
		{
			name:      "empty run fails",
			baselines: goodBaselines,
			input:     `LOADRATIO {"run":"","isolated_ns":100,"loaded_ns":190,"ratio":1.9,"max_step_ratio":2.1,"limit":2.5,"steps":4}` + "\n",
			latency:   true,
			wantErr:   "run must be non-empty",
		},
		{
			name:      "zero ratio fails",
			baselines: goodBaselines,
			input:     `LOADRATIO {"run":"neighbor","isolated_ns":100,"loaded_ns":200,"ratio":0,"max_step_ratio":2.1,"limit":2.5,"steps":4}`,
			latency:   true,
			wantErr:   "ratio must be > 0",
		},
		{
			name:      "zero isolated fails",
			baselines: goodBaselines,
			input:     `LOADRATIO {"run":"neighbor","isolated_ns":0,"loaded_ns":200,"ratio":2.0,"max_step_ratio":2.1,"limit":2.5,"steps":4}`,
			latency:   true,
			wantErr:   "isolated_ns must be > 0",
		},
		{
			name:      "zero loaded fails",
			baselines: goodBaselines,
			input:     `LOADRATIO {"run":"neighbor","isolated_ns":100,"loaded_ns":0,"ratio":1.0,"max_step_ratio":2.1,"limit":2.5,"steps":4}`,
			latency:   true,
			wantErr:   "loaded_ns must be > 0",
		},
		{
			name:      "zero steps fails",
			baselines: goodBaselines,
			input:     `LOADRATIO {"run":"neighbor","isolated_ns":100,"loaded_ns":200,"ratio":2.0,"max_step_ratio":2.1,"limit":2.5,"steps":0}`,
			latency:   true,
			wantErr:   "steps must be > 0",
		},
		{
			name:      "limit mismatch fails naming run and limits",
			baselines: goodBaselines,
			input:     "LOADRATIO " + `{"run":"neighbor","isolated_ns":100,"loaded_ns":190,"ratio":1.9,"max_step_ratio":2.1,"limit":3.0,"steps":4}` + "\n",
			latency:   true,
			wantErr:   "limit 3 does not match frozen noisy_neighbor.max_ratio 2.5",
		},
		{
			name:      "ratio inconsistent with fields fails",
			baselines: goodBaselines,
			input:     "LOADRATIO " + `{"run":"neighbor","isolated_ns":100,"loaded_ns":150,"ratio":1.9,"max_step_ratio":2.1,"limit":2.5,"steps":4}` + "\n",
			latency:   true,
			wantErr:   "does not match loaded_ns/isolated_ns 1.5",
		},
		{
			name:      "unknown run name fails",
			baselines: goodBaselines,
			input:     "LOADRATIO " + `{"run":"stranger","isolated_ns":100,"loaded_ns":190,"ratio":1.9,"max_step_ratio":2.1,"limit":2.5,"steps":4}` + "\n",
			latency:   true,
			wantErr:   `run not in --require "neighbor"`,
		},
		{
			name:      "missing required run fails",
			baselines: goodBaselines,
			input:     "",
			latency:   true,
			wantErr:   "missing measurement",
		},
		{
			name:      "duplicate run names fail",
			baselines: goodBaselines,
			input:     goodInput + goodInput,
			latency:   true,
			wantErr:   `duplicate measurement for run "neighbor"`,
		},
		{
			name:      "multiple required runs each judged",
			baselines: `{"noisy_neighbor": {"max_ratio": 1.5}}`,
			input: `LOADRATIO {"run":"a","isolated_ns":100,"loaded_ns":120,"ratio":1.2,"max_step_ratio":1.3,"limit":1.5,"steps":3}
LOADRATIO {"run":"b","isolated_ns":100,"loaded_ns":160,"ratio":1.6,"max_step_ratio":1.8,"limit":1.5,"steps":3}
`,
			latency: true,
			require: "a,b",
			wantErr: "load gate: over-limit measurement in strict mode",
			wantOut: "load gate: b: ratio 1.6 > limit 1.5 (+6.67%) (worst step 1.8)",
		},
		{
			name:      "suite non-zero exit fails despite parsed line",
			baselines: goodBaselines,
			input:     "",
			latency:   true,
			suite: func() (string, error) {
				return "LOADRATIO " + `{"run":"neighbor","isolated_ns":100,"loaded_ns":190,"ratio":1.9,"max_step_ratio":2.1,"limit":2.5,"steps":4}` + "\n", errors.New("exit status 1")
			},
			wantErr:  "running TestLoadNoisyNeighbor",
			wantErr2: "1 measurements parsed before failure",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			basePath, inputPath := writeFixtures(t, tt.baselines, tt.input)
			cfg := &config{
				baselinesPath: basePath,
				latency:       tt.latency,
				require:       tt.require,
				runSuite:      tt.suite,
			}
			if tt.suite == nil {
				cfg.input = inputPath
			}
			if cfg.require == "" {
				cfg.require = "neighbor"
			}
			var out strings.Builder
			err := run(&out, cfg)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("run() = nil, want error containing %q", tt.wantErr)
				}
				for _, want := range []string{tt.wantErr, tt.wantErr2} {
					if want != "" && !strings.Contains(err.Error(), want) {
						t.Errorf("error %q missing %q", err, want)
					}
				}
			} else if err != nil {
				t.Fatalf("run() = %v, want nil", err)
			}
			if got := out.String(); !strings.Contains(got, tt.wantOut) {
				t.Errorf("output %q missing %q", got, tt.wantOut)
			}
		})
	}
}

func TestInputPathUsed(t *testing.T) {
	t.Parallel()
	basePath, inputPath := writeFixtures(t, goodBaselines, goodInput)
	cfg := &config{baselinesPath: basePath, input: inputPath, latency: true, require: "neighbor"}
	var out strings.Builder
	if err := run(&out, cfg); err != nil {
		t.Fatalf("run() = %v, want nil", err)
	}
	if strings.Contains(out.String(), "advisory") {
		t.Errorf("strict run produced advisory output: %q", out.String())
	}
}

func TestParseEmptyRatioToken(t *testing.T) {
	t.Parallel()
	if _, err := parse(""); !errors.Is(err, nil) {
		t.Fatalf("parse(\"\") = %v, want nil", err)
	}
}
