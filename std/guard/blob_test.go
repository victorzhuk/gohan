package guard

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.White)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

func TestBlobGuard(t *testing.T) {
	t.Run("guards.blob-guard-input", func(t *testing.T) {
		data := pngBytes(t)
		// A tool result stored an Image as a blob but declared the wrong MIME.
		if err := CheckBlobMIME("image/jpeg", data); !errors.Is(err, ErrMIMEMismatch) {
			t.Fatalf("CheckBlobMIME declared image/jpeg = %v, want ErrMIMEMismatch", err)
		}
		if err := CheckBlobMIME("image/png", data); err != nil {
			t.Fatalf("CheckBlobMIME declared image/png = %v, want nil", err)
		}
		if err := CheckBlobMIME("image/png; charset=binary", data); err != nil {
			t.Fatalf("CheckBlobMIME with parameter = %v, want nil", err)
		}
		if err := CheckBlobMIME("application/octet-stream", data); err != nil {
			t.Fatalf("CheckBlobMIME octet-stream = %v, want nil", err)
		}
	})
}
