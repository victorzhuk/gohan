package eino

import (
	"context"
	"strings"
	"testing"
	"time"

	gohan "github.com/victorzhuk/gohan/core"
	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/testkit/gohantest"
)

func TestHealth(t *testing.T) {
	t.Run("runtime.readyz-false-on-schema-skew", func(t *testing.T) {
		stack, _ := preemptStack(t, gohantest.NewScriptedModel(conformanceProfile, gohantest.Text("hi")))
		stack.RegisterHealthProbe("postgres", func(ctx context.Context) error {
			return types.ErrSchemaTooOld
		})
		ctx := preemptPrincipal(context.Background())
		rep := stack.Health(ctx)
		c, ok := rep.Checks["postgres"]
		if !ok {
			t.Fatal("Health report has no postgres check")
		}
		if c.OK {
			t.Fatal("postgres check OK, want not OK on schema skew")
		}
		if !strings.Contains(c.Detail, "schema older than this release supports") {
			t.Errorf("detail %q does not name ErrSchemaTooOld", c.Detail)
		}
		if rep.Ready || stack.Ready() {
			t.Fatal("Ready = true on schema skew, want false")
		}
	})

	t.Run("runtime.readyz-false-during-shutdown", func(t *testing.T) {
		ctx := preemptPrincipal(context.Background())
		bm := &preemptBlockingModel{entered: make(chan struct{}), release: make(chan struct{}), cancelled: make(chan struct{})}
		stack, conv := preemptStack(t, bm)
		drained := make(chan preemptStream, 1)
		go func() {
			var out preemptStream
			for ev, err := range conv.Send(ctx, "s1", types.Message{
				Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "go"}},
			}) {
				if err != nil {
					out.err = err
					break
				}
				out.evs = append(out.evs, ev)
			}
			drained <- out
		}()
		select {
		case <-bm.entered:
		case <-time.After(2 * time.Second):
			t.Fatal("eino run never entered the model call")
		}
		shutdownErr := make(chan error, 1)
		go func() { shutdownErr <- stack.Shutdown(ctx) }()
		var rep gohan.HealthReport
		deadline := time.After(2 * time.Second)
		for {
			rep = stack.Health(ctx)
			if !rep.Ready && !rep.Checks["shutdown"].OK {
				break
			}
			select {
			case <-deadline:
				t.Fatal("stack never left ready during drain")
			case <-time.After(5 * time.Millisecond):
			}
		}
		close(bm.release)
		<-drained
		if err := <-shutdownErr; err != nil {
			t.Fatalf("shutdown: %v", err)
		}
		if stack.Ready() {
			t.Fatal("Ready = true after shutdown, want false")
		}
	})
}
