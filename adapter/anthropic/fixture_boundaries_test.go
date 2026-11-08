package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

// fbEvent is one decoded server-sent event of a shipped fixture.
type fbEvent struct {
	Event        string `json:"-"`
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta"`
	Message struct {
		Usage struct {
			InputTokens int `json:"input_tokens"`
		} `json:"usage"`
	} `json:"message"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// fbEvents decodes the shipped wire body into its events.
func fbEvents(t *testing.T, body []byte) []fbEvent {
	t.Helper()
	var out []fbEvent
	for _, raw := range strings.Split(string(body), "\n\n") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		name := ""
		var data strings.Builder
		for _, line := range strings.Split(raw, "\n") {
			switch {
			case strings.HasPrefix(line, "event:"):
				name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		if data.Len() == 0 {
			continue
		}
		ev := fbEvent{Event: name}
		if err := json.Unmarshal([]byte(data.String()), &ev); err != nil {
			t.Fatalf("decode %s event: %v", name, err)
		}
		out = append(out, ev)
	}
	return out
}

// fbToolStarts returns the tool block every index opened with.
func fbToolStarts(events []fbEvent) map[int]fbEvent {
	out := map[int]fbEvent{}
	for _, ev := range events {
		if ev.Event != "content_block_start" {
			continue
		}
		switch ev.ContentBlock.Type {
		case "tool_use", "server_tool_use":
			out[ev.Index] = ev
		}
	}
	return out
}

// fbStoppedIndices returns the indices the provider closed.
func fbStoppedIndices(events []fbEvent) map[int]bool {
	out := map[int]bool{}
	for _, ev := range events {
		if ev.Event == "content_block_stop" {
			out[ev.Index] = true
		}
	}
	return out
}

// fbPartialArgs joins the argument fragments of one block index.
func fbPartialArgs(events []fbEvent, index int) string {
	var out strings.Builder
	for _, ev := range events {
		if ev.Event == "content_block_delta" && ev.Index == index && ev.Delta.Type == "input_json_delta" {
			out.WriteString(ev.Delta.PartialJSON)
		}
	}
	return out.String()
}

type fbRoundTripper func(*http.Request) (*http.Response, error)

func (f fbRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fbModel serves one shipped fixture to a real provider instance.
func fbModel(t *testing.T, fixture string) *Model {
	t.Helper()
	pb, ok := Fixtures()[fixture]
	if !ok {
		t.Fatalf("shipped fixture %q is missing", fixture)
	}
	m, err := New(Config{
		APIKey: "fixture" + "-key",
		KeyID:  "fixture-key-1",
		Profile: types.ModelProfile{
			Name:    "claude-fixture",
			Version: "served-1",
			Caps: types.Caps{
				Tools:            true,
				ParallelTools:    true,
				Constrained:      true,
				Streaming:        true,
				Temperature:      true,
				Cache:            types.CacheExplicit,
				CacheBreakpoints: 4,
				Fidelity:         map[types.BlockKind]types.Fidelity{},
			},
		},
		Client: &http.Client{Transport: fbRoundTripper(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: pb.Status,
				Header:     pb.Header,
				Body:       io.NopCloser(bytes.NewReader(pb.Body)),
				Request:    r,
			}, nil
		})},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

// fbRequest builds one request that declares the tools the fixture calls.
func fbRequest(specs ...types.ToolSpec) types.ModelRequest {
	return types.ModelRequest{
		Messages: []types.Message{
			{ID: "m1", Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "hi"}}},
		},
		Tools: specs,
	}
}

// fbCollect drains one provider stream into its chunks.
func fbCollect(t *testing.T, m types.Model, req types.ModelRequest) []types.ModelChunk {
	t.Helper()
	var out []types.ModelChunk
	for chunk, err := range m.Generate(context.Background(), req) {
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		out = append(out, chunk)
	}
	return out
}

// fbFinishes returns the finish reasons the stream reported.
func fbFinishes(chunks []types.ModelChunk) []types.FinishReason {
	var out []types.FinishReason
	for _, chunk := range chunks {
		if chunk.Finish != "" {
			out = append(out, chunk.Finish)
		}
	}
	return out
}

func TestProviderFixtureBoundaries(t *testing.T) {
	t.Run("provider_tool_calls", func(t *testing.T) {
		pb, ok := Fixtures()["provider tool calls"]
		if !ok {
			t.Fatal("shipped fixture \"provider tool calls\" is missing")
		}
		events := fbEvents(t, pb.Body)
		starts := fbToolStarts(events)
		stops := fbStoppedIndices(events)

		if len(starts) != 2 {
			t.Fatalf("shipped fixture opened %d tool blocks, want two provider tool calls", len(starts))
		}
		for index, want := range map[int]string{0: "srvtoolu_1", 1: "srvtoolu_2"} {
			start, ok := starts[index]
			if !ok {
				t.Fatalf("tool block index %d has no content_block_start", index)
			}
			if start.ContentBlock.ID != want {
				t.Fatalf("tool block index %d carries id %q, want %q", index, start.ContentBlock.ID, want)
			}
			if start.ContentBlock.Name != "web_search" {
				t.Fatalf("tool block index %d carries name %q, want web_search", index, start.ContentBlock.Name)
			}
			if !stops[index] {
				t.Fatalf("tool block index %d has no content_block_stop, want every tool index closed", index)
			}
		}

		input, output := 0, 0
		for _, ev := range events {
			if ev.Event == "message_start" {
				input = ev.Message.Usage.InputTokens
			}
			if ev.Event == "message_delta" {
				output = ev.Usage.OutputTokens
			}
		}
		if input != 6 || output != 3 {
			t.Fatalf("recording usage %d input and %d output tokens, want 6 and 3", input, output)
		}

		specs := []types.ToolSpec{{
			Name:        "web_search",
			Description: "provider side search",
			Schema:      json.RawMessage(`{"type":"object"}`),
			Executor:    types.ByProvider,
		}}
		chunks := fbCollect(t, fbModel(t, "provider tool calls"), fbRequest(specs...))

		finishes := fbFinishes(chunks)
		if len(finishes) != 1 || finishes[0] != types.FinishStop {
			t.Fatalf("finish reasons %v, want exactly one stop", finishes)
		}
		var usage *types.Usage
		for _, chunk := range chunks {
			if chunk.Usage != nil {
				usage = chunk.Usage
			}
		}
		if usage == nil {
			t.Fatal("stream reported no usage, want the recorded usage counts")
		}
		if usage.InputTokens != input || usage.OutputTokens != output {
			t.Fatalf("stream usage %d input and %d output tokens, want %d and %d", usage.InputTokens, usage.OutputTokens, input, output)
		}
		if usage.ProviderToolCalls["web_search"] != 2 {
			t.Fatalf("provider tool calls %v, want two web_search calls", usage.ProviderToolCalls)
		}
		for _, chunk := range chunks {
			if chunk.ToolUse != nil && len(chunk.ToolUse.Args) > 0 {
				t.Fatalf("completed fixture surfaced executable arguments %q, want none", string(chunk.ToolUse.Args))
			}
		}
	})

	t.Run("truncated_tool_call", func(t *testing.T) {
		pb, ok := Fixtures()["truncated tool call"]
		if !ok {
			t.Fatal("shipped fixture \"truncated tool call\" is missing")
		}
		events := fbEvents(t, pb.Body)
		starts := fbToolStarts(events)
		stops := fbStoppedIndices(events)

		start, ok := starts[0]
		if !ok {
			t.Fatal("truncated fixture opened no tool block")
		}
		if len(starts) != 1 {
			t.Fatalf("truncated fixture opened %d tool blocks, want one", len(starts))
		}
		if start.ContentBlock.Type != "tool_use" {
			t.Fatalf("truncated fixture opened a %q block, want tool_use", start.ContentBlock.Type)
		}
		if start.ContentBlock.ID != "toolu_1" || start.ContentBlock.Name != "search" {
			t.Fatalf("truncated fixture carries %q/%q, want toolu_1/search", start.ContentBlock.ID, start.ContentBlock.Name)
		}
		if stops[0] {
			t.Fatal("truncated fixture carries a content_block_stop, want the block left open")
		}

		fragment := fbPartialArgs(events, 0)
		if fragment == "" {
			t.Fatal("truncated fixture carries no argument fragment")
		}
		if json.Valid([]byte(fragment)) {
			t.Fatalf("truncated fixture argument fragment %q is valid JSON, want a genuinely unfinished fragment", fragment)
		}

		specs := []types.ToolSpec{{
			Name:        "search",
			Description: "harness side search",
			Schema:      json.RawMessage(`{"type":"object"}`),
		}}
		chunks := fbCollect(t, fbModel(t, "truncated tool call"), fbRequest(specs...))

		finishes := fbFinishes(chunks)
		if len(finishes) != 1 || finishes[0] != types.FinishMaxTokens {
			t.Fatalf("finish reasons %v, want exactly one max_tokens", finishes)
		}
		previews := 0
		for _, chunk := range chunks {
			if chunk.ToolUse == nil {
				continue
			}
			if len(chunk.ToolUse.Args) > 0 {
				t.Fatalf("truncated call surfaced executable arguments %q, want none", string(chunk.ToolUse.Args))
			}
			if chunk.ToolUse.ID != "toolu_1" || chunk.ToolUse.Name != "search" {
				t.Fatalf("preview carries %q/%q, want toolu_1/search", chunk.ToolUse.ID, chunk.ToolUse.Name)
			}
			previews++
		}
		if previews == 0 {
			t.Fatal("truncated call surfaced no identity preview")
		}
	})
}
