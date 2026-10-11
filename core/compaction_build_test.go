package gohan

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/guards"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// compactionBuildModel is the profile-backed model without a token counter.
type compactionBuildModel struct{}

func (compactionBuildModel) Profile() types.ModelProfile {
	return types.ModelProfile{Name: "compaction-test", ContextWindow: 1000, Caps: types.Caps{Tools: true}}
}

func (compactionBuildModel) Generate(context.Context, types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return nil
}

// countingBuildModel adds the exact-count port on top of the plain model.
type countingBuildModel struct{ compactionBuildModel }

func (countingBuildModel) Count(context.Context, types.ModelRequest) (int, error) {
	return 1, nil
}

// buildEstimator is a declared estimator for the estimator-fallback leg.
type buildCompactor struct{}

func (buildCompactor) Compact(context.Context, stores.History, types.ModelProfile) (types.Compaction, error) {
	return types.Compaction{}, nil
}

// buildFallback is the injected fallback; a non-nil err fails validation.
type buildFallback struct{ err error }

func (f buildFallback) Project(_ context.Context, h stores.History, _ types.ModelProfile) (stores.History, error) {
	return h, nil
}

func (f buildFallback) ValidateCompactionFallback(types.ModelProfile) error {
	return f.err
}

// buildEstimator is a declared estimator for the estimator-fallback leg.
type buildEstimator struct{}

func (buildEstimator) Estimate(types.ModelRequest, types.Caps) int { return 1 }

// buildGuard passes every consultation, so the fixtures exercise the build
// gate and not the guard's verdicts.
type buildGuard struct{}

func (buildGuard) Decide(_ context.Context, _ guards.GuardInput) (types.Decision[guards.GuardVerdict], error) {
	return types.Decision[guards.GuardVerdict]{Value: guards.GuardVerdict{Action: guards.Pass}}, nil
}

// noCompactionLog hides the optional compaction seam of the wrapped log, so
// a sink-requirement refusal is observable.
type noCompactionLog struct{ stores.SessionLog }

func compactionBuildStores() stores.Stores {
	return stores.Stores{
		SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom)),
		Audit:      stores.NewMemoryAuditLog(),
		EventLog:   stores.NewMemoryEventLog(),
	}
}

func compactionBuildOptions(model types.Model, extra ...Option) []Option {
	return append([]Option{
		WithModels(model),
		WithStores(compactionBuildStores()),
		WithNativeAgent(NativeSpec{
			Request:       FlowRequest{Name: "chat"},
			Profile:       "compaction-test",
			ContextPolicy: stores.ContextPolicy{Compactor: buildCompactor{}},
		}),
		WithCompactionFallback(buildFallback{}),
		WithCompactionGuard(buildGuard{}),
	}, extra...)
}

func TestCompactionBuildGate(t *testing.T) {
	t.Run("context.compaction-counting-required", func(t *testing.T) {
		_, err := Build(compactionBuildOptions(compactionBuildModel{})...)
		if err == nil {
			t.Fatal("compactor without a counting source accepted")
		}
		if !strings.Contains(err.Error(), "chat") || !strings.Contains(err.Error(), "compaction-test") {
			t.Fatalf("err = %v, want the flow and the profile named", err)
		}
	})

	t.Run("context.compaction-fallback-required", func(t *testing.T) {
		opts := []Option{
			WithModels(countingBuildModel{}),
			WithStores(compactionBuildStores()),
			WithNativeAgent(NativeSpec{
				Request:       FlowRequest{Name: "chat"},
				Profile:       "compaction-test",
				ContextPolicy: stores.ContextPolicy{Compactor: buildCompactor{}},
			}),
			WithCompactionGuard(buildGuard{}),
		}
		_, err := Build(opts...)
		if err == nil {
			t.Fatal("compactor without an injected fallback accepted")
		}
		if !strings.Contains(err.Error(), "chat") || !strings.Contains(err.Error(), "compaction-test") {
			t.Fatalf("err = %v, want the flow and the profile named", err)
		}

		_, err = Build(compactionBuildOptions(countingBuildModel{}, WithCompactionFallback(buildFallback{err: errors.New("profile out of range")}))...)
		if err == nil {
			t.Fatal("compactor with an unvalidated fallback accepted")
		}
		if !strings.Contains(err.Error(), "chat") || !strings.Contains(err.Error(), "compaction-test") {
			t.Fatalf("err = %v, want the flow and the profile named", err)
		}
	})

	t.Run("sink-without-compaction-seam-refused", func(t *testing.T) {
		st := compactionBuildStores()
		st.SessionLog = noCompactionLog{st.SessionLog}
		_, err := Build([]Option{
			WithModels(countingBuildModel{}),
			WithStores(st),
			WithNativeAgent(NativeSpec{
				Request:       FlowRequest{Name: "chat"},
				Profile:       "compaction-test",
				ContextPolicy: stores.ContextPolicy{Compactor: buildCompactor{}},
			}),
			WithCompactionFallback(buildFallback{}),
			WithCompactionGuard(buildGuard{}),
		}...)
		if err == nil || !strings.Contains(err.Error(), "SessionCompactionAppender") {
			t.Fatalf("err = %v, want the missing session compaction seam named", err)
		}
	})

	t.Run("active-compactor-accepted-and-seams-resolved", func(t *testing.T) {
		s, err := Build(compactionBuildOptions(countingBuildModel{})...)
		if err != nil {
			t.Fatalf("valid compaction build: %v", err)
		}
		cfg, ok := s.resolvedNative("chat")
		if !ok {
			t.Fatal("chat is not a registered native flow")
		}
		if cfg.compactionFallback == nil {
			t.Error("resolved compactionFallback missing")
		}
		if cfg.compactionCount == nil {
			t.Error("resolved compactionCount missing")
		}
		if cfg.contextGuard == nil {
			t.Error("resolved contextGuard missing")
		}
		n, err := cfg.compactionCount(context.Background(), types.ModelRequest{})
		if err != nil || n != 1 {
			t.Fatalf("compactionCount = %d, %v, want 1, nil", n, err)
		}
	})

	t.Run("estimator-fallback-resolved-when-model-has-no-counter", func(t *testing.T) {
		opts := compactionBuildOptions(compactionBuildModel{}, WithEstimator(buildEstimator{}))
		s, err := Build(opts...)
		if err != nil {
			t.Fatalf("estimator-backed compaction build: %v", err)
		}
		cfg, ok := s.resolvedNative("chat")
		if !ok {
			t.Fatal("chat is not a registered native flow")
		}
		n, err := cfg.compactionCount(context.Background(), types.ModelRequest{})
		if err != nil || n != 1 {
			t.Fatalf("compactionCount = %d, %v, want 1, nil", n, err)
		}
	})

	t.Run("context.compaction-guard-required", func(t *testing.T) {
		_, err := Build([]Option{
			WithModels(countingBuildModel{}),
			WithStores(compactionBuildStores()),
			WithNativeAgent(NativeSpec{
				Request:       FlowRequest{Name: "chat"},
				Profile:       "compaction-test",
				ContextPolicy: stores.ContextPolicy{Compactor: buildCompactor{}},
			}),
			WithCompactionFallback(buildFallback{}),
		}...)
		if err == nil {
			t.Fatal("active compactor without a configured guard accepted")
		}
		if !strings.Contains(err.Error(), "chat") || !strings.Contains(err.Error(), "WithCompactionGuard") {
			t.Fatalf("err = %v, want the flow named and the guard option named", err)
		}
	})

	t.Run("no-compactor-keeps-plain-path", func(t *testing.T) {
		_, err := Build([]Option{
			WithModels(compactionBuildModel{}),
			WithStores(stores.Stores{}),
			WithNativeAgent(NativeSpec{Request: FlowRequest{Name: "chat"}, Profile: "compaction-test"}),
		}...)
		if err != nil {
			t.Fatalf("plain build without a compactor: %v", err)
		}
	})
}
