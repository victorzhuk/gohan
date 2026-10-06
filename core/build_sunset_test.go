package gohan

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

type sunsetSink struct {
	counts []sunsetCount
}

type sunsetCount struct {
	name  string
	attrs []types.Attr
}

func (s *sunsetSink) StartSpan(ctx context.Context, name string, attrs ...types.Attr) (context.Context, func(...types.Attr)) {
	return ctx, func(...types.Attr) {}
}

func (s *sunsetSink) Count(ctx context.Context, name string, n int64, attrs ...types.Attr) {
	s.counts = append(s.counts, sunsetCount{name: name, attrs: attrs})
}

func (s *sunsetSink) Record(ctx context.Context, name string, v float64, attrs ...types.Attr) {}

func sunsetProfile(name string, sunset time.Time) types.ModelProfile {
	return types.ModelProfile{Name: name, Sunset: sunset, Successor: "new"}
}

func TestBuildSunset(t *testing.T) {
	inside := time.Now().Add(20 * 24 * time.Hour)

	t.Run("lifecycle.sunset-inside-notice-window", func(t *testing.T) {
		_, err := Build(WithModels(fakeModel{profile: sunsetProfile("old", inside)}))
		if err == nil {
			t.Fatal("Build accepted a profile inside the notice window")
		}
		for _, part := range []string{"old", "new"} {
			if !strings.Contains(err.Error(), part) {
				t.Errorf("err = %v, want it to name %q", err, part)
			}
		}
	})

	t.Run("buildSunsetWarnOnly", func(t *testing.T) {
		sink := &sunsetSink{}
		s, err := Build(
			WithModels(fakeModel{profile: sunsetProfile("old", inside)}),
			WithTelemetry(sink),
			WithSunsetWarnOnly(),
		)
		if err != nil {
			t.Fatalf("Build with warn-only: %v", err)
		}
		if s == nil {
			t.Fatal("Build returned a nil stack")
		}
		var found bool
		for _, c := range sink.counts {
			if c.name != metricSunsetWarning {
				continue
			}
			for _, a := range c.attrs {
				if a.Key == types.KeyModelProfile && a.Value == "old" {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("counts = %+v, want %s{profile=old}", sink.counts, metricSunsetWarning)
		}
	})

	t.Run("buildSunsetZeroPasses", func(t *testing.T) {
		if _, err := Build(WithModels(fakeModel{profile: sunsetProfile("old", time.Time{})})); err != nil {
			t.Fatalf("Build rejected a zero Sunset: %v", err)
		}
	})

	t.Run("buildSunsetOutsideWindowPasses", func(t *testing.T) {
		outside := time.Now().Add(60 * 24 * time.Hour)
		if _, err := Build(WithModels(fakeModel{profile: sunsetProfile("old", outside)})); err != nil {
			t.Fatalf("Build rejected a profile outside the window: %v", err)
		}
	})
}
