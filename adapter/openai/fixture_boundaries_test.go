package openai_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/adapter/openai"
	"github.com/victorzhuk/gohan/core/types"
)

// fbChunk is one decoded chunk of a shipped recording.
type fbChunk struct {
	Choices []struct {
		Delta struct {
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

// fbRecording is one shipped recording as it sits in the embedded testdata.
type fbRecording struct {
	Name   string
	Status int
	Header http.Header
	Body   string
}

// fbLoadRecording reads one shipped recording through the embedded filesystem.
func fbLoadRecording(t *testing.T, file string) fbRecording {
	t.Helper()
	data, err := recordingsFS.ReadFile("testdata/" + file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	var rec struct {
		Name   string              `json:"name"`
		Status int                 `json:"status"`
		Header map[string][]string `json:"header"`
		Body   string              `json:"body"`
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("decode %s: %v", file, err)
	}
	return fbRecording{Name: rec.Name, Status: rec.Status, Header: http.Header(rec.Header), Body: rec.Body}
}

// fbChunks decodes the shipped wire body into its chunks.
func fbChunks(t *testing.T, body string) []fbChunk {
	t.Helper()
	var out []fbChunk
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var chunk fbChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("decode chunk %q: %v", payload, err)
		}
		out = append(out, chunk)
	}
	return out
}

type fbTransport struct {
	rec fbRecording
}

func (tr *fbTransport) Do(r *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: tr.rec.Status,
		Header:     tr.rec.Header,
		Body:       io.NopCloser(strings.NewReader(tr.rec.Body)),
		Request:    r,
	}, nil
}

// fbProvider builds a real provider instance over one shipped recording.
func fbProvider(t *testing.T, rec fbRecording) *openai.Provider {
	t.Helper()
	p, err := openai.New(openai.Config{
		Profile: types.ModelProfile{
			Name:    fixtureProfile,
			Version: fixtureVersion,
			Keys:    types.PlatformKey,
			Caps: types.Caps{
				Tools:            true,
				ParallelTools:    true,
				Constrained:      true,
				Streaming:        true,
				Temperature:      true,
				Cache:            types.CacheExplicit,
				CacheBreakpoints: 4,
				Fidelity: map[types.BlockKind]types.Fidelity{
					types.KindText:       types.Preserved,
					types.KindToolUse:    types.Preserved,
					types.KindToolResult: types.Preserved,
				},
			},
		},
		Keys:      simpleKeys(fixtureKeyID, fixtureToken),
		Transport: &fbTransport{rec: rec},
	})
	if err != nil {
		t.Fatalf("build provider: %v", err)
	}
	return p
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
	t.Run("truncated_tool_call", func(t *testing.T) {
		rec := fbLoadRecording(t, "truncated-tool-call.json")
		if rec.Name != "truncated tool call" {
			t.Fatalf("recording name %q, want \"truncated tool call\"", rec.Name)
		}

		chunks := fbChunks(t, rec.Body)
		id := ""
		name := ""
		finish := ""
		var args strings.Builder
		for _, chunk := range chunks {
			for _, choice := range chunk.Choices {
				for _, call := range choice.Delta.ToolCalls {
					if call.ID != "" {
						id = call.ID
					}
					if call.Function.Name != "" {
						name = call.Function.Name
					}
					args.WriteString(call.Function.Arguments)
				}
				if choice.FinishReason != nil {
					finish = *choice.FinishReason
				}
			}
		}
		if id != "call_1" || name != "search" {
			t.Fatalf("recorded call carries %q/%q, want call_1/search", id, name)
		}
		fragment := args.String()
		if fragment == "" {
			t.Fatal("recorded call carries no argument fragment")
		}
		if json.Valid([]byte(fragment)) {
			t.Fatalf("recorded argument fragment %q is valid JSON, want a genuinely unfinished fragment", fragment)
		}
		if finish != "length" {
			t.Fatalf("recorded finish reason %q, want length", finish)
		}

		p := fbProvider(t, rec)
		req := types.ModelRequest{
			Messages: []types.Message{
				{ID: "probe", Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "probe"}}},
			},
			Tools: []types.ToolSpec{{
				Name:        "search",
				Description: "harness side search",
				Schema:      json.RawMessage(`{"type":"object"}`),
			}},
		}
		out := collect(t, p, req)

		finishes := fbFinishes(out)
		if len(finishes) != 1 || finishes[0] != types.FinishMaxTokens {
			t.Fatalf("finish reasons %v, want exactly one max_tokens", finishes)
		}
		previews := 0
		for _, chunk := range out {
			if chunk.ToolUse == nil {
				continue
			}
			if len(chunk.ToolUse.Args) > 0 {
				t.Fatalf("truncated call surfaced executable arguments %q, want none", string(chunk.ToolUse.Args))
			}
			if chunk.ToolUse.ID != "call_1" || chunk.ToolUse.Name != "search" {
				t.Fatalf("preview carries %q/%q, want call_1/search", chunk.ToolUse.ID, chunk.ToolUse.Name)
			}
			previews++
		}
		if previews == 0 {
			t.Fatal("truncated call surfaced no identity preview")
		}
	})
}
