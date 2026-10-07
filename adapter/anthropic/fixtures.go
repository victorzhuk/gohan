package anthropic

import (
	"net/http"
	"strings"
)

// Playback is one recorded provider response: status, headers and the wire
// body exactly as Anthropic served it. The conformance run feeds these to
// the real decoding path without a network call.
type Playback struct {
	Status int
	Header http.Header
	Body   []byte
}

func sse(events ...string) []byte {
	return []byte(strings.Join(events, ""))
}

func event(name, data string) string {
	return "event: " + name + "\ndata: " + data + "\n\n"
}

func textDelta(text string) string {
	return event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"`+text+`"}}`)
}

// Fixtures returns the recorded responses the conformance suite replays,
// keyed by suite fixture name.
func Fixtures() map[string]Playback {
	return map[string]Playback{
		"probe": {Status: 200, Body: sse(
			event("message_start", `{"type":"message_start","message":{"model":"served-1","usage":{"input_tokens":10}}}`),
			event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
			textDelta("hel"),
			textDelta("lo"),
			event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`),
			event("message_stop", `{"type":"message_stop"}`),
		)},
		"usage reported": {Status: 200, Body: sse(
			event("message_start", `{"type":"message_start","message":{"model":"served-1","usage":{"input_tokens":100,"cache_read_input_tokens":20}}}`),
			event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
			textDelta("hello"),
			event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":30,"cache_creation_input_tokens":15}}`),
			event("message_stop", `{"type":"message_stop"}`),
		)},
		"usage estimated": {Status: 200, Body: sse(
			event("message_start", `{"type":"message_start","message":{"model":"served-1"}}`),
			event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
			textDelta("hello there"),
			event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`),
			event("message_stop", `{"type":"message_stop"}`),
		)},
		"truncated tool call": {Status: 200, Body: sse(
			event("message_start", `{"type":"message_start","message":{"model":"served-1","usage":{"input_tokens":12}}}`),
			event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"search"}}`),
			event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"q\":\"go\"}"}}`),
			event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":9}}`),
			event("message_stop", `{"type":"message_stop"}`),
		)},
		"raw round trip": {Status: 200, Body: sse(
			event("message_start", `{"type":"message_start","message":{"model":"served-1","usage":{"input_tokens":8}}}`),
			event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
			textDelta("ok"),
			event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`),
			event("message_stop", `{"type":"message_stop"}`),
		)},
		"cache breakpoints": {Status: 200, Body: sse(
			event("message_start", `{"type":"message_start","message":{"model":"served-1","usage":{"input_tokens":40}}}`),
			event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
			textDelta("cached"),
			event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2,"cache_creation_input_tokens":12}}`),
			event("message_stop", `{"type":"message_stop"}`),
		)},
		"provider tool calls": {Status: 200, Body: sse(
			event("message_start", `{"type":"message_start","message":{"model":"served-1","usage":{"input_tokens":6}}}`),
			event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search"}}`),
			event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"q\":\"go\"}"}}`),
			event("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"server_tool_use","id":"srvtoolu_2","name":"web_search"}}`),
			event("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"q\":\"llm\"}"}}`),
			event("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`),
			event("message_stop", `{"type":"message_stop"}`),
		)},
		"rate limited with retry after": errorPlayback(429, "rate_limit_error", "Slow down.", map[string][]string{"Retry-After": {"7"}}),
		"quota code":                    errorPlayback(400, "insufficient_quota", "Quota exhausted.", nil),
		"transient":                     errorPlayback(500, "api_error", "Internal error.", nil),
		"auth":                          errorPlayback(401, "authentication_error", "Bad key.", nil),
		"permanent":                     errorPlayback(400, "invalid_request_error", "Bad request.", nil),
		"context overflow":              errorPlayback(400, "context_length_exceeded", "Too long.", nil),
		"deprecated":                    errorPlayback(404, "model_not_found", "No such model.", nil),
	}
}

func errorPlayback(status int, code, message string, header map[string][]string) Playback {
	body := `{"type":"error","error":{"type":"` + code + `","message":"` + message + `"}}`
	return Playback{Status: status, Header: http.Header(header), Body: []byte(body)}
}
