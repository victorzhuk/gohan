package gohantest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	jsonv2 "encoding/json/v2"

	"github.com/victorzhuk/gohan/core/types"
)

// Mode selects how a cassette replay resolves a model call.
type Mode string

const (
	// ModeStrict fails the call when a request matches no recorded key.
	ModeStrict Mode = "strict"
	// ModeByTurn matches calls by turn index, for tests that edit prompts
	// deliberately and so break the request hash.
	ModeByTurn Mode = "byturn"
	// ModeRerecord overwrites the cassette from a live model.
	ModeRerecord Mode = "rerecord"
)

const cassetteEnv = "GOHAN_CASSETTES"

// ModeFromEnv reads the cassette mode from GOHAN_CASSETTES. An unset or
// unrecognised value selects ModeStrict.
func ModeFromEnv() Mode {
	switch Mode(os.Getenv(cassetteEnv)) {
	case ModeByTurn:
		return ModeByTurn
	case ModeRerecord:
		return ModeRerecord
	default:
		return ModeStrict
	}
}

// cassetteFile is the on-disk cassette: the profile version it was recorded
// with and one entry per model call, keyed by the sha256 of the assembled
// request. Chunk offsets are milliseconds from the start of the call.
type cassetteFile struct {
	Version string         `json:"version"`
	Calls   []cassetteCall `json:"calls"`
}

type cassetteCall struct {
	Key    string          `json:"key"`
	Chunks []cassetteChunk `json:"chunks"`
	Usage  *types.Usage    `json:"usage"`
}

type cassetteChunk struct {
	AtMs  int64            `json:"at_ms"`
	Chunk types.ModelChunk `json:"chunk"`
}

// Record wraps real so every call is captured into the cassette at path,
// overwriting any existing file. The file is rewritten after each call, so
// a Replay in the same test sees it without waiting for cleanup.
func Record(t *testing.T, real types.Model, path string) *CassetteModel {
	t.Helper()
	m := &CassetteModel{
		t:       t,
		mode:    ModeRerecord,
		path:    path,
		profile: real.Profile(),
		live:    real,
		file:    cassetteFile{Version: real.Profile().Version},
	}
	return m
}

// Replay loads the cassette at path and returns a model that answers calls
// from it. The mode comes from GOHAN_CASSETTES and defaults to strict. A
// missing or malformed cassette is an error, never a silent live call.
func Replay(t *testing.T, profile types.ModelProfile, path string) (*CassetteModel, error) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("gohantest: cassette: %w", err)
	}
	var cf cassetteFile
	if err := json.Unmarshal(b, &cf); err != nil {
		return nil, fmt.Errorf("gohantest: cassette %s: %w", path, err)
	}
	mode := ModeFromEnv()
	if cf.Version != profile.Version && mode != ModeRerecord {
		if mode == ModeByTurn {
			t.Logf("gohantest: cassette recorded with version %q, profile pins %q; replaying by turn", cf.Version, profile.Version)
		} else {
			return nil, fmt.Errorf("gohantest: cassette recorded with version %q, profile pins %q", cf.Version, profile.Version)
		}
	}
	return &CassetteModel{
		t:       t,
		mode:    mode,
		path:    path,
		profile: profile,
		file:    cf,
	}, nil
}

// CassetteModel is the model Record and Replay return. In rerecord mode it
// captures a live model's streams; otherwise it replays recorded chunks
// back to back with no wall-clock wait, and Timings exposes each call's
// recorded offsets as data.
type CassetteModel struct {
	t       *testing.T
	mode    Mode
	path    string
	profile types.ModelProfile
	live    types.Model

	mu      sync.Mutex
	file    cassetteFile
	turn    int
	timings [][]time.Duration
}

// Profile returns the profile the cassette was recorded with or replayed
// against.
func (m *CassetteModel) Profile() types.ModelProfile { return m.profile }

// Timings returns, per call, the millisecond offset of each chunk from the
// start of that call.
func (m *CassetteModel) Timings() [][]time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]time.Duration, len(m.timings))
	for i, ts := range m.timings {
		out[i] = append([]time.Duration(nil), ts...)
	}
	return out
}

// Generate answers one model call from the cassette, or captures one from
// the live model in rerecord mode.
func (m *CassetteModel) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		if m.mode == ModeRerecord {
			m.record(ctx, req, yield)
			return
		}
		m.replay(req, yield)
	}
}

func (m *CassetteModel) record(ctx context.Context, req types.ModelRequest, yield func(types.ModelChunk, error) bool) {
	call := cassetteCall{Key: requestKey(req)}
	start := time.Now()
	var offsets []time.Duration
	for chunk, err := range m.live.Generate(ctx, req) {
		if err != nil {
			yield(types.ModelChunk{}, err)
			return
		}
		at := time.Since(start)
		offsets = append(offsets, at)
		u := chunk.Usage
		call.Chunks = append(call.Chunks, cassetteChunk{AtMs: at.Milliseconds(), Chunk: chunk})
		if u != nil {
			call.Usage = u
		}
		if !yield(chunk, nil) {
			return
		}
	}
	m.mu.Lock()
	m.file.Calls = append(m.file.Calls, call)
	m.timings = append(m.timings, offsets)
	err := m.save()
	m.mu.Unlock()
	if err != nil {
		m.t.Errorf("gohantest: cassette %s: %v", m.path, err)
	}
}

func (m *CassetteModel) replay(req types.ModelRequest, yield func(types.ModelChunk, error) bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := requestKey(req)
	m.turn++
	call, _ := m.callByKey(key)
	if call == nil {
		turn := m.turn - 1
		if m.mode == ModeByTurn {
			if turn >= len(m.file.Calls) {
				yield(types.ModelChunk{}, fmt.Errorf("gohantest: cassette has no call for turn %d", turn))
				return
			}
			call = &m.file.Calls[turn]
		} else {
			first := "(none)"
			if len(m.file.Calls) > 0 {
				first = m.file.Calls[0].Key
			}
			yield(types.ModelChunk{}, fmt.Errorf("gohantest: strict replay: request on turn %d (key %s) matches no recorded call; first recorded key %s", turn, key[:12], first[:min(12, len(first))]))
			return
		}
	}
	var offsets []time.Duration
	for _, c := range call.Chunks {
		offsets = append(offsets, time.Duration(c.AtMs)*time.Millisecond)
		if !yield(c.Chunk, nil) {
			return
		}
	}
	m.timings = append(m.timings, offsets)
}

func (m *CassetteModel) callByKey(key string) (*cassetteCall, int) {
	for i := range m.file.Calls {
		if m.file.Calls[i].Key == key {
			return &m.file.Calls[i], i
		}
	}
	return nil, len(m.file.Calls)
}

func (m *CassetteModel) save() error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(m.file)
	if err != nil {
		return err
	}
	return os.WriteFile(m.path, b, 0o644)
}

// requestKey hashes the assembled request through its wire form, so the key
// is stable across unrelated edits to in-memory-only fields.
func requestKey(req types.ModelRequest) string {
	b, err := jsonv2.Marshal(req)
	if err != nil {
		return "unmarshalable:" + err.Error()
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
