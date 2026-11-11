package evals

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// Evidence is the redacted snapshot one evaluation runs on. Judges and
// evaluators receive this value only: untrusted data, no execution tools,
// no production credentials, no contexts.
type Evidence struct {
	Info    types.RunInfo
	Done    types.Done
	History stores.History
}

// Evaluator deterministically scores one dimension from evidence, in
// process, with no model and no network. An unevaluable input returns an
// Unscored types.EvalDimensionResult; an error means the evaluator itself
// failed and the dimension settles unscored with judge_failed.
type Evaluator interface {
	Dimension() string
	Version() string
	Evaluate(ev Evidence) types.EvalDimensionResult
}

// Rubric is the versioned scoring instruction a model judge runs under.
type Rubric struct {
	Version     string
	Instruction string
}

// Fingerprint hashes the rubric version and instruction.
func (r Rubric) Fingerprint() string {
	sum := sha256.Sum256([]byte(r.Version + "\x00" + r.Instruction))
	return hex.EncodeToString(sum[:8])
}

// Judge is one evaluation unit: a named dimension scored by a
// deterministic Evaluator or by an external judge adapter over a
// caller-supplied Model with a versioned Rubric. Exactly one of
// Evaluator, Model is set. Temperature zero does not establish
// determinism: the judge repeats Repeat independent calls, takes the
// median and flags spread above UnstableSpread as Unstable.
type Judge struct {
	Dimension string
	Evaluator Evaluator
	Model     types.Model
	Version   string
	Rubric    Rubric
	Repeat    int // DefaultJudgeRepeat when zero
}

// Fingerprint is the judge identity a calibration approval covers:
// dimension, evaluator version or model version, and rubric fingerprint
// (spec: profile version + instruction hash).
func (j Judge) Fingerprint() string {
	version := j.Version
	if version == "" && j.Model != nil {
		version = j.Model.Profile().Version
	}
	sum := sha256.Sum256([]byte(j.Dimension + "\x00" + version + "\x00" + j.Rubric.Fingerprint()))
	return hex.EncodeToString(sum[:8])
}

const (
	DefaultJudgeRepeat = 3
	UnstableSpread     = 0.10
)

type JudgeOption func(*Judge)

// WithRepeat sets the repeated-judgment count.
func WithRepeat(n int) JudgeOption {
	return func(j *Judge) { j.Repeat = n }
}

// NewJudge builds a model-backed judge for dimension. No model SDK enters
// core or std: the caller supplies the Model adapter.
func NewJudge(dimension string, m types.Model, version string, r Rubric, opts ...JudgeOption) Judge {
	j := Judge{Dimension: dimension, Model: m, Version: version, Rubric: r}
	for _, opt := range opts {
		opt(&j)
	}
	return j
}
