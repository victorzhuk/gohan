package sandbox

import (
	"context"
	"fmt"
	"strings"

	"github.com/victorzhuk/gohan/core/types"
)

type listFilesArgs struct {
	Path string `json:"path"`
}

// listFiles reaches the optional listing capability by type assertion on
// the opened workspace. A workspace without it fails the call whole: no
// partial listing ever reaches the model.
func listFiles(ctx context.Context, w types.Workspace, a listFilesArgs) ([]types.Block, error) {
	l, ok := w.(types.WorkspaceLister)
	if !ok {
		return nil, fmt.Errorf("list %s: %w", a.Path, types.ErrWorkspaceListingRequired)
	}
	entries, err := l.List(ctx, a.Path)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", a.Path, err)
	}
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "%s\t%d\t%s\n", e.Path, e.Size, e.MIME)
	}
	return resultText(b.String()), nil
}
