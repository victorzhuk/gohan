package types

import (
	"strings"
	"testing"
	"time"
)

func TestProblemDetail(t *testing.T) {
	t.Run("errors.detail-never-carries-provider-body", func(t *testing.T) {
		body := `{"error":{"message":"request payload {\"prompt\":\"internal draft q42\"} exceeded quota on upstream-a"}}`
		err := &ModelError{
			Class:  ClassTransient,
			Status: 500,
			Err:    &providerBodyError{body: body},
		}
		p := ProblemOf(err)
		if p.Code != CodeModelUnavailable {
			t.Fatalf("Code = %q, want %q", p.Code, CodeModelUnavailable)
		}
		if p.Status != 503 {
			t.Errorf("Status = %d, want 503", p.Status)
		}
		for _, leak := range []string{"upstream-a", "internal draft q42", "quota"} {
			if strings.Contains(p.Detail, leak) {
				t.Errorf("Detail %q leaks provider body text %q", p.Detail, leak)
			}
		}
		if strings.Contains(p.Title, "upstream-a") {
			t.Errorf("Title %q leaks provider body text", p.Title)
		}
		for k, v := range p.Fields {
			if strings.Contains(v, body) {
				t.Errorf("Fields[%q] leaks provider body", k)
			}
		}
	})

	t.Run("errors.retry-after-on-retryable", func(t *testing.T) {
		p := ProblemOf(ErrMailboxFull)
		if p.Code != CodeMailboxFull {
			t.Errorf("Code = %q, want %q", p.Code, CodeMailboxFull)
		}
		if p.Kind != Retryable {
			t.Errorf("Kind = %v, want Retryable", p.Kind)
		}
		if p.Status != 429 {
			t.Errorf("Status = %d, want 429", p.Status)
		}
		if p.RetryAfter != time.Second {
			t.Errorf("RetryAfter = %v, want 1s", p.RetryAfter)
		}

		p = ProblemOf(ErrRunActive)
		if p.Code != CodeRunActive {
			t.Errorf("Code = %q, want %q", p.Code, CodeRunActive)
		}
		if p.Kind != Retryable {
			t.Errorf("Kind = %v, want Retryable", p.Kind)
		}
		if p.Status != 409 {
			t.Errorf("Status = %d, want 409", p.Status)
		}
	})
}

// providerBodyError stands in for the response body a provider returns;
// ModelError.Err carries it verbatim.
type providerBodyError struct {
	body string
}

func (e *providerBodyError) Error() string { return e.body }
