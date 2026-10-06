package redact

import (
	"context"
	"errors"
)

type KeyFunc func(ctx context.Context, tenant string) ([]byte, error)

func (f KeyFunc) Key(ctx context.Context, tenant string) ([]byte, error) {
	if f == nil {
		return nil, errors.New("key source is nil")
	}
	return f(ctx, tenant)
}
