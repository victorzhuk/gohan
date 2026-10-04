package stores

import (
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func TestBlobChecks(t *testing.T) {
	t.Run("messages.url-only-from-user", func(t *testing.T) {
		cases := []struct {
			origin types.Origin
			want   bool
		}{
			{types.Origin{Kind: types.OriginUser}, true},
			{types.Origin{Kind: types.OriginSystem}, true},
			{types.Origin{Kind: types.OriginModel}, false},
			{types.Origin{Kind: types.OriginTool, Name: "fetch"}, false},
			{types.Origin{Kind: types.OriginProvider, Name: "p"}, false},
		}
		for _, c := range cases {
			if got := URLForwardable(c.origin); got != c.want {
				t.Errorf("URLForwardable(%v) = %v, want %v", c.origin, got, c.want)
			}
		}
	})

	t.Run("messages.blob-too-large", func(t *testing.T) {
		caps := BlobCaps{MaxBytes: 1 << 20, Formats: []string{"application/pdf"}}
		big := make([]byte, (1<<20)+1)
		err := CheckBlobs([]types.Block{types.File{
			BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginUser}},
			MIME:      "application/pdf",
			Data:      big,
		}}, caps)
		if !errors.Is(err, types.ErrBlobTooLarge) {
			t.Fatalf("CheckBlobs over MaxBytes = %v, want ErrBlobTooLarge", err)
		}
		// Between InlineBlobBytes and MaxBytes: stored by ref, not rejected.
		err = CheckBlobs([]types.Block{types.File{
			BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginUser}},
			MIME:      "application/pdf",
			Data:      make([]byte, InlineBlobBytes+1),
		}}, caps)
		if err != nil {
			t.Fatalf("CheckBlobs between spill threshold and MaxBytes = %v, want nil", err)
		}
	})

	t.Run("build.blob-caps", func(t *testing.T) {
		caps := BlobCaps{MaxBytes: 1 << 20, Formats: []string{"image/png", "image/jpeg"}}
		err := CheckBlobs([]types.Block{types.File{
			BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginUser}},
			MIME:      "application/pdf",
		}}, caps)
		if !errors.Is(err, types.ErrBlobTooLarge) {
			t.Fatalf("CheckBlobs with undeclared format = %v, want ErrBlobTooLarge", err)
		}
	})

	t.Run("caps per request", func(t *testing.T) {
		caps := BlobCaps{MaxBytes: 1 << 20, MaxPerRequest: 1, Formats: []string{"image/png"}}
		blocks := []types.Block{types.ToolResult{
			BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginTool, Name: "shot"}},
			ID:        "t1",
			Content: []types.Block{
				types.Image{MIME: "image/png"},
				types.Image{MIME: "image/png"},
			},
		}}
		err := CheckBlobs(blocks, caps)
		if !errors.Is(err, types.ErrBlobTooLarge) {
			t.Fatalf("CheckBlobs over MaxPerRequest = %v, want ErrBlobTooLarge", err)
		}
		caps.MaxPerRequest = 2
		if err := CheckBlobs(blocks, caps); err != nil {
			t.Fatalf("CheckBlobs at MaxPerRequest = %v, want nil", err)
		}
	})
}
