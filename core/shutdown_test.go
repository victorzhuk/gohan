package gohan

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// driveRun simulates a run the stack drives. The run sees the armed
// preemption request and signals armed; it lands at its persisted safe
// point — clearing itself from the registry — when land closes, or leaves
// itself for Recover to reclaim when release closes first.
func driveRun(t *testing.T, s *Stack, runID string, land, release <-chan struct{}) (chan struct{}, error) {
	t.Helper()
	pre := NewPreemptor()
	if err := s.beginRun(runID, pre); err != nil {
		return nil, err
	}
	armed := make(chan struct{}, 1)
	go func() {
		for !pre.pending() {
			select {
			case <-release:
				return
			case <-time.After(200 * time.Microsecond):
			}
		}
		armed <- struct{}{}
		select {
		case <-release:
			return
		case <-land:
			s.endRun(runID)
			return
		}
	}()
	return armed, nil
}

func TestStackShutdownHealth(t *testing.T) {
	ctx := context.Background()

	t.Run("runtime.shutdown-rejects-new", func(t *testing.T) {
		s, err := Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if err := s.Shutdown(ctx); err != nil {
			t.Fatalf("shutdown: %v", err)
		}
		if err := s.beginRun("r-late", NewPreemptor()); !errors.Is(err, types.ErrShuttingDown) {
			t.Fatalf("beginRun after shutdown: %v, want ErrShuttingDown", err)
		}
		if s.Ready() {
			t.Fatal("Ready = true after shutdown, want false")
		}
	})

	t.Run("runtime.readyz-false-on-schema-skew", func(t *testing.T) {
		s, err := Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		s.RegisterHealthProbe("postgres", func(ctx context.Context) error {
			return types.ErrSchemaTooOld
		})
		rep := s.Health(ctx)
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
		if rep.Ready || s.Ready() {
			t.Fatal("Ready = true on schema skew, want false")
		}
	})

	t.Run("runtime.readyz-false-during-shutdown", func(t *testing.T) {
		s, err := Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		land := make(chan struct{})
		release := make(chan struct{})
		defer close(release)
		armed, err := driveRun(t, s, "r1", land, release)
		if err != nil {
			t.Fatalf("begin run: %v", err)
		}
		shutdownErr := make(chan error, 1)
		go func() { shutdownErr <- s.Shutdown(ctx) }()
		select {
		case <-armed:
		case <-time.After(2 * time.Second):
			close(land)
			t.Fatal("preemption request never armed")
		}
		if rep := s.Health(ctx); rep.Ready {
			close(land)
			t.Fatal("Ready = true while draining, want false")
		} else if c := rep.Checks["shutdown"]; c.OK {
			close(land)
			t.Fatal("shutdown check OK while draining, want not OK")
		}
		close(land)
		if err := <-shutdownErr; err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	})

	t.Run("runtime.shutdown-grace-exhausted", func(t *testing.T) {
		s, err := Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		release := make(chan struct{})
		defer close(release)
		if _, err := driveRun(t, s, "stuck", make(chan struct{}), release); err != nil {
			t.Fatalf("begin run: %v", err)
		}
		gctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		err = s.Shutdown(gctx)
		if !errors.Is(err, types.ErrShutdownIncomplete) {
			t.Fatalf("shutdown: %v, want ErrShutdownIncomplete", err)
		}
		inc, ok := errors.AsType[*ShutdownIncomplete](err)
		if !ok {
			t.Fatalf("shutdown error %T does not carry ShutdownIncomplete", err)
		}
		if !slices.Equal(inc.RunIDs, []string{"stuck"}) {
			t.Fatalf("RunIDs = %v, want [stuck]", inc.RunIDs)
		}
	})

	t.Run("shutdown is idempotent", func(t *testing.T) {
		s, err := Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		land := make(chan struct{})
		release := make(chan struct{})
		defer close(release)
		armed, err := driveRun(t, s, "r2", land, release)
		if err != nil {
			t.Fatalf("begin run: %v", err)
		}
		done := make(chan error, 1)
		go func() { done <- s.Shutdown(ctx) }()
		select {
		case <-armed:
		case <-time.After(2 * time.Second):
			close(land)
			t.Fatal("preemption request never armed")
		}
		close(land)
		if err := <-done; err != nil {
			t.Fatalf("first shutdown: %v", err)
		}
		if err := s.Shutdown(ctx); err != nil {
			t.Fatalf("second shutdown: %v", err)
		}
	})

	t.Run("ready false after completed shutdown", func(t *testing.T) {
		s, err := Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if err := s.Shutdown(ctx); err != nil {
			t.Fatalf("shutdown: %v", err)
		}
		if s.Ready() {
			t.Fatal("Ready = true after completed shutdown, want false")
		}
		if rep := s.Health(ctx); rep.Ready {
			t.Fatal("Health.Ready = true after completed shutdown, want false")
		}
	})
}

func TestConversationShutdownAdmission(t *testing.T) {
	ctx := principalCtx(context.Background())

	newConv := func(t *testing.T, rt runtime.Runtime) (Conversation, *Stack) {
		t.Helper()
		s, err := Build()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		s.stores = stores.Stores{SessionLog: stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))}
		conv, err := NewConversation(s, "chat", rt,
			WithConversationRuns(&runsFixture{MemoryRuns: stores.NewMemoryRuns(stores.WithMemoryRunClock(time.Now)), onSess: map[string]string{}}),
			WithConversationEventLog(stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))),
			WithConversationCheckpoints(stores.NewMemoryCheckpoints()),
		)
		if err != nil {
			t.Fatalf("new conversation: %v", err)
		}
		return conv, s
	}

	t.Run("shutdownRejectsSend", func(t *testing.T) {
		conv, s := newConv(t, &convRT{steps: 0})
		if err := s.Shutdown(ctx); err != nil {
			t.Fatalf("shutdown: %v", err)
		}
		tuples := 0
		for ev, err := range conv.Send(ctx, "sess-1", userMsg("hi")) {
			tuples++
			if ev != nil {
				t.Fatalf("delivered %+v with the refusal", ev)
			}
			if !errors.Is(err, types.ErrShuttingDown) {
				t.Fatalf("send after shutdown: %v, want ErrShuttingDown", err)
			}
		}
		if tuples != 1 {
			t.Fatalf("tuples = %d, want exactly the one error tuple", tuples)
		}
		if s.Ready() {
			t.Fatal("Ready = true after shutdown, want false")
		}
	})

	t.Run("shutdownRejectsContinue", func(t *testing.T) {
		conv, s := newConv(t, &convRT{steps: 0})
		if err := s.Shutdown(ctx); err != nil {
			t.Fatalf("shutdown: %v", err)
		}
		tuples := 0
		for ev, err := range conv.Continue(ctx, "sess-1") {
			tuples++
			if ev != nil {
				t.Fatalf("delivered %+v with the refusal", ev)
			}
			if !errors.Is(err, types.ErrShuttingDown) {
				t.Fatalf("continue after shutdown: %v, want ErrShuttingDown", err)
			}
		}
		if tuples != 1 {
			t.Fatalf("tuples = %d, want exactly the one error tuple", tuples)
		}
	})

	t.Run("shutdownGraceExhaustedSend", func(t *testing.T) {
		rt := &convRT{steps: 1, gate: make(chan struct{}), entered: make(chan struct{})}
		conv, s := newConv(t, rt)
		drained := make(chan struct{})
		go func() {
			defer close(drained)
			for _, err := range conv.Send(ctx, "sess-1", userMsg("hi")) {
				_ = err
			}
		}()
		select {
		case <-rt.entered:
		case <-time.After(2 * time.Second):
			close(rt.gate)
			t.Fatal("run never entered its step")
		}
		gctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		err := s.Shutdown(gctx)
		if !errors.Is(err, types.ErrShutdownIncomplete) {
			t.Fatalf("shutdown: %v, want ErrShutdownIncomplete", err)
		}
		inc, ok := errors.AsType[*ShutdownIncomplete](err)
		if !ok {
			t.Fatalf("shutdown error %T does not carry ShutdownIncomplete", err)
		}
		if len(inc.RunIDs) != 1 || inc.RunIDs[0] == "" {
			t.Fatalf("RunIDs = %v, want the in-flight run", inc.RunIDs)
		}
		close(rt.gate)
		<-drained
		if err := s.Shutdown(ctx); err != nil {
			t.Fatalf("shutdown after drain: %v", err)
		}
	})
}
