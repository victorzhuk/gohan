package anthropic

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/victorzhuk/gohan/core/types"
)

// mapError normalizes a non-2xx body: the provider error type runs through
// the shared classifier, the Retry-After header becomes a duration. The
// body text never enters the error string, so a credential echoed by the
// provider stays out of logs.
func mapError(status int, header http.Header, body io.Reader) error {
	code, message := parseErrorBody(body)
	return &types.ModelError{
		Class:      types.ClassifyProviderError(status, code),
		Provider:   providerName,
		Status:     status,
		Code:       code,
		RetryAfter: parseRetryAfter(header),
		Err:        ioLimit(message),
	}
}

func parseErrorBody(r io.Reader) (code, message string) {
	var payload struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	data, err := io.ReadAll(io.LimitReader(r, 64*1024))
	if err != nil {
		return "", ""
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", ""
	}
	return payload.Error.Type, payload.Error.Message
}

func ioLimit(message string) error {
	if message == "" {
		return nil
	}
	const cap = 256
	if len(message) > cap {
		message = message[:cap]
	}
	return errors.New(message)
}
