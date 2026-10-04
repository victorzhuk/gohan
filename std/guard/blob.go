package guard

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrMIMEMismatch reports a blob whose declared MIME differs from the type
// sniffed from its stored bytes.
var ErrMIMEMismatch = errors.New("gohan.guard: blob MIME does not match content")

// CheckBlobMIME sniffs data and rejects it when the sniffed media type
// differs from the declared one. A declared charset or other parameter is
// ignored; application/octet-stream passes unchecked because it declares no
// specific type to contradict.
func CheckBlobMIME(declared string, data []byte) error {
	if mediaType(declared) == "application/octet-stream" {
		return nil
	}
	sniffed := http.DetectContentType(data)
	if mediaType(declared) != mediaType(sniffed) {
		return fmt.Errorf("%w: declared %q, content is %q", ErrMIMEMismatch, declared, sniffed)
	}
	return nil
}

func mediaType(v string) string {
	if i := strings.IndexByte(v, ';'); i >= 0 {
		v = v[:i]
	}
	return strings.ToLower(strings.TrimSpace(v))
}
