package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

type wireError struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error"`
}

// mapResponseError runs the provider failure through the single classifier:
// the status and the error code decide the class, the retry headers decide
// the delay. The response body is the only content read and never carries
// the credential.
func (p *Provider) mapResponseError(resp *http.Response) *types.ModelError {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var wire wireError
	var code, message string
	if json.Unmarshal(body, &wire) == nil {
		message = wire.Error.Message
		switch typed := wire.Error.Code.(type) {
		case string:
			code = typed
		case float64:
			code = strconv.Itoa(int(typed))
		}
		if code == "" {
			code = wire.Error.Type
		}
	}
	retry, ok := retryAfter(resp.Header, p.now())
	if !ok {
		retry = 0
	}
	return &types.ModelError{
		Class:      types.ClassifyProviderError(resp.StatusCode, code),
		Provider:   p.profile.Name,
		Status:     resp.StatusCode,
		Code:       code,
		RetryAfter: retry,
		Err:        fmt.Errorf("provider error: %s", message),
	}
}

// retryAfter prefers Retry-After seconds, then the rate-limit reset headers.
func retryAfter(header http.Header, now time.Time) (time.Duration, bool) {
	if v := strings.TrimSpace(header.Get("Retry-After")); v != "" {
		if secs, err := strconv.Atoi(v); err == nil {
			return time.Duration(secs) * time.Second, true
		}
		if when, err := http.ParseTime(v); err == nil {
			if d := when.Sub(now); d > 0 {
				return d, true
			}
			return 0, true
		}
	}
	for name, fallback := range map[string]string{
		"X-Ratelimit-Reset-Tokens":   "1ms",
		"X-Ratelimit-Reset-Requests": "1ms",
		"X-Ratelimit-Reset":          "1ms",
	} {
		v := strings.TrimSpace(header.Get(name))
		if v == "" {
			continue
		}
		if d, ok := parseReset(v, fallback); ok {
			return d, true
		}
	}
	return 0, false
}

func parseReset(v, unit string) (time.Duration, bool) {
	if d, err := time.ParseDuration(v); err == nil {
		return d, true
	}
	if secs, err := strconv.ParseFloat(v, 64); err == nil {
		return time.Duration(secs * float64(time.Second)), true
	}
	return 0, false
}
