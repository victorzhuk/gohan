package stores

import (
	"context"
	"errors"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

func outputRunInfo() types.RunInfo {
	return types.RunInfo{
		SessionID: "s1",
		Principal: types.Principal{Tenant: "t-a", Subject: "u1"},
	}
}

func outputImageBlocks(data string) []types.Block {
	return []types.Block{
		types.Image{
			BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginUser}},
			MIME:      "image/png",
			Data:      []byte(data),
		},
	}
}

func TestOutputStore(t *testing.T) {
	t.Run("messages.blob-stored-by-ref", func(t *testing.T) {
		ctx := context.Background()
		s := NewMemoryOutputs()

		ref, err := s.Put(ctx, outputRunInfo(), outputImageBlocks("blob-bytes"))
		if err != nil {
			t.Fatalf("put: %v", err)
		}
		want, err := encodeOutput(outputImageBlocks("blob-bytes"))
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		got, err := s.Get(ctx, ref)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if string(got) != string(want) {
			t.Fatalf("Get returned %q, want the stored bytes", got)
		}

		if _, err := s.Get(ctx, "missing"); !errors.Is(err, ErrOutputNotFound) {
			t.Fatalf("Get(missing) error = %v, want ErrOutputNotFound", err)
		}

		// The session cascade drops the ref once no owner remains.
		if err := s.DeleteSessionDependents(ctx, "s1"); err != nil {
			t.Fatalf("delete session dependents: %v", err)
		}
		if _, err := s.Get(ctx, ref); !errors.Is(err, ErrOutputNotFound) {
			t.Fatalf("Get after DeleteSessionDependents = %v, want ErrOutputNotFound", err)
		}
	})

	t.Run("messages.blob-content-addressed", func(t *testing.T) {
		ctx := context.Background()
		s := NewMemoryOutputs()

		first, err := s.Put(ctx, outputRunInfo(), outputImageBlocks("same-bytes"))
		if err != nil {
			t.Fatalf("first put: %v", err)
		}
		ri := outputRunInfo()
		ri.SessionID = "s2"
		second, err := s.Put(ctx, ri, outputImageBlocks("same-bytes"))
		if err != nil {
			t.Fatalf("second put: %v", err)
		}
		if first != second {
			t.Fatalf("equal bytes produced refs %q and %q, want one ref", first, second)
		}

		// One copy: deleting one owning session keeps the bytes for the other.
		if err := s.DeleteSessionDependents(ctx, "s1"); err != nil {
			t.Fatalf("delete session dependents: %v", err)
		}
		if _, err := s.Get(ctx, first); err != nil {
			t.Fatalf("get after deleting one owner: %v", err)
		}
		if err := s.EraseSubject(ctx, "t-a", "u1"); err != nil {
			t.Fatalf("erase subject: %v", err)
		}
		if err := s.DeleteSessionDependents(ctx, "s2"); err != nil {
			t.Fatalf("delete session dependents: %v", err)
		}
		if _, err := s.Get(ctx, first); !errors.Is(err, ErrOutputNotFound) {
			t.Fatalf("Get after EraseSubject = %v, want ErrOutputNotFound", err)
		}
	})
}
