package guard

import (
	"context"

	"github.com/victorzhuk/gohan/core/guards"
	"github.com/victorzhuk/gohan/core/types"
)

type RedactionGuard struct {
	redactor types.Redactor
	policy   types.RedactionPolicy
}

func NewRedactionGuard(redactor types.Redactor, policy types.RedactionPolicy) *RedactionGuard {
	return &RedactionGuard{redactor: redactor, policy: policy}
}

func (g *RedactionGuard) Name() string { return "redaction" }

func (g *RedactionGuard) Decide(ctx context.Context, in guards.GuardInput) (types.Decision[guards.GuardVerdict], error) {
	if g.redactor == nil || len(g.policy.Detectors) == 0 {
		return types.Decision[guards.GuardVerdict]{Value: guards.GuardVerdict{Action: guards.Pass}, Confidence: 1}, nil
	}
	blocks, _, err := g.redactor.Redact(ctx, in.Blocks, g.policy)
	if err != nil {
		return types.Decision[guards.GuardVerdict]{}, err
	}
	return types.Decision[guards.GuardVerdict]{Value: guards.GuardVerdict{Action: guards.Rewrite, Blocks: blocks}, Confidence: 1}, nil
}
