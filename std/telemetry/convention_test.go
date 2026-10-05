package telemetry

import (
	"regexp"
	"slices"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestTelemetryConvention(t *testing.T) {
	t.Run("telemetry.canonical-keys", func(t *testing.T) {
		got := GenAI()
		for _, tc := range []struct {
			key  string
			want []string
		}{
			{types.KeySessionID, []string{"gen_ai.conversation.id"}},
			{types.KeyModelProfile, []string{"gen_ai.request.model"}},
			{types.KeyModelVersion, []string{"gen_ai.response.model"}},
			{types.KeyModelEndpoint, []string{"gen_ai.provider.name"}},
			{types.KeyUsageInput, []string{"gen_ai.usage.input_tokens"}},
			{types.KeyUsageOutput, []string{"gen_ai.usage.output_tokens"}},
			{types.KeyToolName, []string{"gen_ai.tool.name"}},
		} {
			if names := got.Rename(tc.key); !slices.Equal(names, tc.want) {
				t.Errorf("%s mapped to %v, want %v", tc.key, names, tc.want)
			}
		}
		// Keys with no semantic convention keep their canonical name, so
		// the run-tree keys survive every backend.
		for _, key := range []string{
			types.KeyFlow, types.KeyRunID, types.KeyRootRunID,
			types.KeyParentRunID, types.KeyTurn,
		} {
			if names := got.Rename(key); !slices.Equal(names, []string{key}) {
				t.Errorf("%s was renamed to %v, want passthrough", key, names)
			}
		}
	})

	t.Run("telemetry.rename-is-config", func(t *testing.T) {
		if types.KeyModelVersion != "gohan.model.version" {
			t.Fatalf("core canonical key changed: %s", types.KeyModelVersion)
		}
		c := GenAI()
		c.Map = func(attr string) []string {
			if attr == types.KeyModelVersion {
				return []string{"gen_ai.response.version_name"}
			}
			return genAIAttributes(attr)
		}
		attrs := c.ApplyAttrs(types.String(types.KeyModelVersion, "gpt-x"),
			types.String(types.KeyFlow, "support"))
		if len(attrs) != 2 {
			t.Fatalf("got %d attrs, want 2: %+v", len(attrs), attrs)
		}
		if attrs[0].Key != "gen_ai.response.version_name" {
			t.Errorf("renamed key = %s, want gen_ai.response.version_name", attrs[0].Key)
		}
		if attrs[1].Key != types.KeyFlow {
			t.Errorf("unmapped key changed: %s", attrs[1].Key)
		}
	})

	t.Run("content mapping drops content under the default", func(t *testing.T) {
		c := GenAI()
		if c.Content != ContentNone {
			t.Fatalf("default content mapping = %d, want ContentNone", c.Content)
		}
		attrs := c.ApplyAttrs(
			types.String("gen_ai.input.messages", "secret prompt"),
			types.String("langfuse.observation.output", "secret reply"),
			types.String(types.KeyToolName, "search"),
		)
		if len(attrs) != 1 || attrs[0].Key != "gen_ai.tool.name" {
			t.Fatalf("content survived the default mapping: %+v", attrs)
		}
	})

	t.Run("content mapping hashes and passes under their modes", func(t *testing.T) {
		hashed := Convention{Map: nil, Content: ContentHashes}
		out := hashed.ApplyAttrs(types.String("gen_ai.input.messages", "secret prompt"))
		if len(out) != 1 || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(out[0].Value.(string)) {
			t.Fatalf("hashed value = %+v, want a sha256 hex digest", out)
		}
		full := Convention{Content: ContentFull}
		out = full.ApplyAttrs(types.String("gen_ai.input.messages", "secret prompt"))
		if len(out) != 1 || out[0].Value != "secret prompt" {
			t.Fatalf("full value = %+v, want passthrough", out)
		}
	})

	t.Run("preset maps a sample of keys to the backend's spelling", func(t *testing.T) {
		lf := Langfuse()
		session := lf.Rename(types.KeySessionID)
		for _, want := range []string{"gen_ai.conversation.id", "langfuse.session.id"} {
			if !slices.Contains(session, want) {
				t.Errorf("session mapping %v missing %s", session, want)
			}
		}
		if names := lf.Rename(types.KeyRelease); !slices.Contains(names, "langfuse.release") {
			t.Errorf("release mapped to %v, missing langfuse.release", names)
		}
		if names := lf.Rename("gohan.prompt.version"); !slices.Contains(names, "langfuse.prompt.version") {
			t.Errorf("prompt version mapped to %v, missing langfuse.prompt.version", names)
		}
		if lf.SchemaURL == "" || GenAI().SchemaURL == "" {
			t.Error("preset left its schema URL unset")
		}
	})
}
