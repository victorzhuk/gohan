package gohantest

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

func TestCassetteModes(t *testing.T) {
	t.Run("telemetry.replay-strictness", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "cassette.json")
		req := types.ModelRequest{Options: types.ModelOptions{MaxTokens: 8}}
		writeCassette(t, path, cassetteFile{
			Version: "v1",
			Calls: []cassetteCall{{
				Key:    requestKey(req),
				Chunks: []cassetteChunk{{AtMs: 12, Chunk: types.ModelChunk{Kind: types.DeltaText, Delta: "old"}}, {AtMs: 30, Chunk: types.ModelChunk{Finish: types.FinishStop}}},
			}},
		})

		_, err := Replay(t, types.ModelProfile{Version: "v2"}, path)
		if err == nil {
			t.Fatal("strict replay accepted a version mismatch")
		}
		if !strings.Contains(err.Error(), "v1") || !strings.Contains(err.Error(), "v2") {
			t.Fatalf("version mismatch not named: %v", err)
		}

		t.Setenv(cassetteEnv, "byturn")
		m, err := Replay(t, types.ModelProfile{Version: "v2"}, path)
		if err != nil {
			t.Fatalf("byturn replay refused a version mismatch: %v", err)
		}
		var got string
		for chunk, err := range m.Generate(context.Background(), req) {
			if err != nil {
				t.Fatalf("byturn replay failed: %v", err)
			}
			got += chunk.Delta
		}
		if got != "old" {
			t.Fatalf("byturn replayed %q", got)
		}
		want := []time.Duration{12 * time.Millisecond, 30 * time.Millisecond}
		if diffs := m.Timings()[0]; len(diffs) != 2 || diffs[0] != want[0] || diffs[1] != want[1] {
			t.Fatalf("timings = %v, want %v", diffs, want)
		}
	})

	t.Run("round trip reproduces chunks", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "cassette.json")
		profile := types.ModelProfile{Version: "v1"}
		req := types.ModelRequest{Options: types.ModelOptions{MaxTokens: 8}}

		rec := Record(t, NewScriptedModel(profile, Text("hello"), ToolCall("search", map[string]any{"q": "gohan"})), path)
		var live []types.ModelChunk
		for chunk, err := range rec.Generate(context.Background(), req) {
			if err != nil {
				t.Fatalf("record: %v", err)
			}
			live = append(live, chunk)
		}

		m, err := Replay(t, profile, path)
		if err != nil {
			t.Fatalf("replay: %v", err)
		}
		var replayed []types.ModelChunk
		for chunk, err := range m.Generate(context.Background(), req) {
			if err != nil {
				t.Fatalf("replay: %v", err)
			}
			replayed = append(replayed, chunk)
		}
		if len(replayed) != len(live) {
			t.Fatalf("replayed %d chunks, recorded %d", len(replayed), len(live))
		}
		for i := range live {
			if !chunkEqual(replayed[i], live[i]) {
				t.Fatalf("chunk %d = %+v, want %+v", i, replayed[i], live[i])
			}
		}
		if requestKey(req) != recordedKey(t, path, 0) {
			t.Fatal("cassette key is not the assembled request hash")
		}
	})

	t.Run("strict refuses an unmatched request", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "cassette.json")
		profile := types.ModelProfile{Version: "v1"}
		req := types.ModelRequest{Options: types.ModelOptions{MaxTokens: 8}}
		rec := Record(t, NewScriptedModel(profile, Text("hello")), path)
		for _, err := range rec.Generate(context.Background(), req) {
			if err != nil {
				t.Fatalf("record: %v", err)
			}
		}

		m, err := Replay(t, profile, path)
		if err != nil {
			t.Fatalf("replay: %v", err)
		}
		other := types.ModelRequest{Options: types.ModelOptions{MaxTokens: 9}}
		for _, err := range m.Generate(context.Background(), other) {
			if err == nil {
				t.Fatal("strict replay answered an unmatched request")
			}
			if !strings.Contains(err.Error(), "matches no recorded call") {
				t.Fatalf("refusal does not name the mismatch: %v", err)
			}
			return
		}
		t.Fatal("replay produced no chunk and no error")
	})

	t.Run("rerecord overwrites", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "cassette.json")
		profile := types.ModelProfile{Version: "v1"}
		req := types.ModelRequest{Options: types.ModelOptions{MaxTokens: 8}}

		rec := Record(t, NewScriptedModel(profile, Text("first")), path)
		for _, err := range rec.Generate(context.Background(), req) {
			if err != nil {
				t.Fatalf("first record: %v", err)
			}
		}
		rec2 := Record(t, NewScriptedModel(profile, Text("second")), path)
		for _, err := range rec2.Generate(context.Background(), req) {
			if err != nil {
				t.Fatalf("rerecord: %v", err)
			}
		}

		m, err := Replay(t, profile, path)
		if err != nil {
			t.Fatalf("replay: %v", err)
		}
		var got string
		for chunk, err := range m.Generate(context.Background(), req) {
			if err != nil {
				t.Fatalf("replay: %v", err)
			}
			got += chunk.Delta
		}
		if got != "second" {
			t.Fatalf("cassette holds %q, want the rerecorded call", got)
		}
		if n := len(readCassette(t, path).Calls); n != 1 {
			t.Fatalf("cassette holds %d calls after rerecord, want 1", n)
		}
	})

	t.Run("GOHAN_CASSETTES selection resolves", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "cassette.json")
		profile := types.ModelProfile{Version: "v1"}
		req := types.ModelRequest{Options: types.ModelOptions{MaxTokens: 8}}
		writeCassette(t, path, cassetteFile{
			Version: "v1",
			Calls: []cassetteCall{{
				Key:    requestKey(types.ModelRequest{Options: types.ModelOptions{MaxTokens: 99}}),
				Chunks: []cassetteChunk{{Chunk: types.ModelChunk{Kind: types.DeltaText, Delta: "env"}}, {Chunk: types.ModelChunk{Finish: types.FinishStop}}},
			}},
		})

		m, err := Replay(t, profile, path)
		if err != nil {
			t.Fatalf("replay: %v", err)
		}
		for _, err := range m.Generate(context.Background(), req) {
			if err == nil {
				t.Fatal("default mode was not strict")
			}
		}

		t.Setenv(cassetteEnv, "byturn")
		m, err = Replay(t, profile, path)
		if err != nil {
			t.Fatalf("replay: %v", err)
		}
		var got string
		for chunk, err := range m.Generate(context.Background(), req) {
			if err != nil {
				t.Fatalf("byturn replay: %v", err)
			}
			got += chunk.Delta
		}
		if got != "env" {
			t.Fatalf("byturn replayed %q", got)
		}

		t.Setenv(cassetteEnv, "bogus")
		m, err = Replay(t, profile, path)
		if err != nil {
			t.Fatalf("replay: %v", err)
		}
		for _, err := range m.Generate(context.Background(), req) {
			if err == nil {
				t.Fatal("unrecognised mode did not fall back to strict")
			}
		}
	})

	t.Run("missing cassette is a clear error", func(t *testing.T) {
		_, err := Replay(t, types.ModelProfile{Version: "v1"}, filepath.Join(t.TempDir(), "absent.json"))
		if err == nil {
			t.Fatal("replay accepted a missing cassette")
		}
		if !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "cassette") {
			t.Fatalf("error does not name the missing cassette: %v", err)
		}
	})
}

func writeCassette(t *testing.T, path string, cf cassetteFile) {
	t.Helper()
	b, err := json.Marshal(cf)
	if err != nil {
		t.Fatalf("marshal cassette: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write cassette: %v", err)
	}
}

func readCassette(t *testing.T, path string) cassetteFile {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read cassette: %v", err)
	}
	var cf cassetteFile
	if err := json.Unmarshal(b, &cf); err != nil {
		t.Fatalf("parse cassette: %v", err)
	}
	return cf
}

// chunkEqual compares two chunks, with usage by value: each call gets a
// fresh usage pointer.
func chunkEqual(a, b types.ModelChunk) bool {
	if a.Kind != b.Kind || a.Delta != b.Delta || a.Finish != b.Finish {
		return false
	}
	if (a.ToolUse == nil) != (b.ToolUse == nil) {
		return false
	}
	if a.ToolUse != nil && !reflect.DeepEqual(a.ToolUse, b.ToolUse) {
		return false
	}
	if (a.Usage == nil) != (b.Usage == nil) {
		return false
	}
	return a.Usage == nil || reflect.DeepEqual(a.Usage, b.Usage)
}

func recordedKey(t *testing.T, path string, i int) string {
	t.Helper()
	calls := readCassette(t, path).Calls
	if i >= len(calls) {
		t.Fatalf("cassette has %d calls, want at least %d", len(calls), i+1)
	}
	return calls[i].Key
}
