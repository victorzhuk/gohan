package gohan

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/victorzhuk/gohan/core/types"
)

type deprecationModel struct {
	profile types.ModelProfile
	err     error
	chunks  []types.ModelChunk
}

func (m *deprecationModel) Profile() types.ModelProfile { return m.profile }

func (m *deprecationModel) Generate(ctx context.Context, req types.ModelRequest) iter.Seq2[types.ModelChunk, error] {
	return func(yield func(types.ModelChunk, error) bool) {
		for _, c := range m.chunks {
			if !yield(c, nil) {
				return
			}
		}
		if m.err != nil {
			yield(types.ModelChunk{}, m.err)
		}
	}
}

func deprecationCounts(sink *sunsetSink) map[string][]sunsetCount {
	out := make(map[string][]sunsetCount)
	for _, c := range sink.counts {
		out[c.name] = append(out[c.name], c)
	}
	return out
}

func profileHasAttr(count sunsetCount, key, want string) bool {
	for _, a := range count.attrs {
		if a.Key == key && a.Value == want {
			return true
		}
	}
	return false
}

func TestDeprecatedNeverSilent(t *testing.T) {
	t.Run("deprecatedRoutesToSuccessor", deprecatedRoutesToSuccessor)
	t.Run("deprecatedReturnsClassNoFallback", deprecatedReturnsClassNoFallback)
}

func deprecatedRoutesToSuccessor(t *testing.T) {
	t.Helper()
	deprecatedErr := &types.ModelError{Class: types.ClassDeprecated, Provider: "old", Err: errors.New("model_deprecated")}
	oldProfile := types.ModelProfile{Name: "old", Successor: "new"}
	newProfile := types.ModelProfile{Name: "new"}
	req := types.ModelRequest{}

	sink := &sunsetSink{}
	s, err := Build(
		WithModels(
			&deprecationModel{profile: oldProfile, err: deprecatedErr},
			&deprecationModel{profile: newProfile, chunks: []types.ModelChunk{{Kind: types.DeltaText, Delta: "hello"}}},
		),
		WithTelemetry(sink),
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	rc, err := s.RecipeModel("old")
	if err != nil {
		t.Fatalf("RecipeModel: %v", err)
	}
	var chunks []types.ModelChunk
	for c, err := range rc.Call(context.Background(), req) {
		if err != nil {
			t.Fatalf("call: %v", err)
		}
		chunks = append(chunks, c)
	}
	if len(chunks) != 1 || chunks[0].Delta != "hello" {
		t.Errorf("chunks = %+v, want one hello from the successor", chunks)
	}
	counts := deprecationCounts(sink)
	calls, ok := counts[metricDeprecatedCalls]
	if !ok || len(calls) != 1 || !profileHasAttr(calls[0], types.KeyModelProfile, "old") {
		t.Errorf("deprecated_calls = %+v, want one emission for old", calls)
	}
	routed, ok := counts[metricDeprecatedRoute]
	if !ok || len(routed) != 1 ||
		!profileHasAttr(routed[0], types.KeyModelProfile, "old") ||
		!profileHasAttr(routed[0], "successor", "new") {
		t.Errorf("deprecated_routed = %+v, want one emission for old->new", routed)
	}
}

func deprecatedReturnsClassNoFallback(t *testing.T) {
	t.Helper()
	deprecatedErr := &types.ModelError{Class: types.ClassDeprecated, Provider: "old", Err: errors.New("model_deprecated")}
	req := types.ModelRequest{}

	sink := &sunsetSink{}
	s, err := Build(
		WithModels(&deprecationModel{profile: types.ModelProfile{Name: "old"}, err: deprecatedErr}),
		WithTelemetry(sink),
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	rc, err := s.RecipeModel("old")
	if err != nil {
		t.Fatalf("RecipeModel: %v", err)
	}
	var chunks []types.ModelChunk
	var callErr error
	for c, err := range rc.Call(context.Background(), req) {
		if err != nil {
			callErr = err
			break
		}
		chunks = append(chunks, c)
	}
	var me *types.ModelError
	if !errors.As(callErr, &me) || me.Class != types.ClassDeprecated {
		t.Fatalf("err = %v, want *ModelError{Class: Deprecated}", callErr)
	}
	if len(chunks) != 0 {
		t.Errorf("chunks = %+v, want no fallback assistant message", chunks)
	}
	counts := deprecationCounts(sink)
	calls, ok := counts[metricDeprecatedCalls]
	if !ok || len(calls) != 1 || !profileHasAttr(calls[0], types.KeyModelProfile, "old") {
		t.Errorf("deprecated_calls = %+v, want one emission for old", calls)
	}
	if _, ok := counts[metricDeprecatedRoute]; ok {
		t.Errorf("deprecated_routed emitted without a declared successor")
	}
}

func TestSuccessorParamsValidated(t *testing.T) {
	successorParamsValidated(t)
}

func successorParamsValidated(t *testing.T) {
	t.Helper()
	oldProfile := types.ModelProfile{Name: "old", Successor: "new"}
	newProfile := types.ModelProfile{Name: "new"}
	spec := nativeSpec("agent", "old")
	temp := 0.7
	spec.ModelOptions = types.ModelOptions{Temperature: &temp}
	_, err := Build(
		WithModels(
			fakeModel{profile: oldProfile},
			fakeModel{profile: newProfile},
		),
		WithNativeAgent(spec),
	)
	if err == nil {
		t.Fatal("Build accepted a flow whose successor lacks the set option")
	}
	for _, part := range []string{"temperature", "new"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("err = %v, want it to name %q", err, part)
		}
	}
}

func TestLifecycle(t *testing.T) {
	t.Run("lifecycle.deprecated-never-silent", func(t *testing.T) {
		deprecatedRoutesToSuccessor(t)
		deprecatedReturnsClassNoFallback(t)
	})
	t.Run("lifecycle.successor-params-validated", successorParamsValidated)
}
