package gohan

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

type sinkCheckpoints struct {
	stores.Checkpoints
	put []stores.Checkpoint
}

func (c *sinkCheckpoints) Put(ctx context.Context, cp stores.Checkpoint) (types.ResumeToken, error) {
	c.put = append(c.put, cp)
	return c.Checkpoints.Put(ctx, cp)
}

type sinkEvents struct {
	stores.EventLog
	appended []stores.Event
}

func (l *sinkEvents) Append(ctx context.Context, runID string, e stores.Event) error {
	l.appended = append(l.appended, e)
	return l.EventLog.Append(ctx, runID, e)
}

// sinkRuntime emits one assistant message and then finishes.
type sinkRuntime struct {
	token string
	steps int
}

func (r *sinkRuntime) Name() string                         { return "sink.test" }
func (r *sinkRuntime) Granularity() runtime.StepGranularity { return runtime.GranularityTurn }

func (r *sinkRuntime) Start(context.Context, runtime.AgentRun) (runtime.State, error) {
	return runtime.State{}, nil
}

func (r *sinkRuntime) Step(_ context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
	r.steps++
	if r.steps == 1 {
		return st, []types.Event{types.AssistantMessage{Message: types.Message{
			Role:   types.RoleAssistant,
			Blocks: []types.Block{types.Text{Text: r.token + " booked"}},
		}}}, runtime.Continue, nil
	}
	return st, nil, runtime.DoneStatus, nil
}

func sinkSerialize(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func sinkContains(haystacks []string, needle string) bool {
	for _, h := range haystacks {
		if strings.Contains(h, needle) {
			return true
		}
	}
	return false
}

func TestRedactionSinks(t *testing.T) {
	t.Run("redaction.stores-never-see-raw-entities", func(t *testing.T) {
		redactor := scenarioRedactor()
		ctx := types.WithPrincipal(context.Background(), types.Principal{Tenant: "t1", Subject: "u1"})
		tokenBlocks, _, err := redactor.Redact(types.WithRunInfo(ctx, types.RunInfo{SessionID: "s1", Principal: types.Principal{Tenant: "t1"}}), []types.Block{types.Text{Text: "Anna Petrova"}}, scenarioPolicy())
		if err != nil {
			t.Fatal(err)
		}
		token := tokenBlocks[0].(types.Text).Text

		maps := stores.NewMemoryRedactionMaps()
		cps := &sinkCheckpoints{Checkpoints: stores.NewMemoryCheckpoints(
			stores.WithMemoryCheckpointClock(time.Now),
			stores.WithMemoryCheckpointRunInfo(types.RunInfoFrom),
		)}
		runs := stores.NewMemoryRuns(
			stores.WithMemoryRunClock(time.Now),
			stores.WithMemoryRunInfo(types.RunInfoFrom),
		)
		events := &sinkEvents{EventLog: stores.NewMemoryEventLog(stores.WithMemoryEventLogClock(time.Now))}
		log := stores.NewMemorySessionLog(stores.WithSessionPrincipals(types.PrincipalFrom))

		stack, err := Build(
			WithStores(stores.Stores{
				SessionLog:    log,
				Runs:          runs,
				Checkpoints:   cps,
				RedactionMaps: maps,
			}),
			WithModels(fakeModel{profile: profile("primary", true)}),
			WithRedactor(redactor),
			WithRedactionPolicy(scenarioPolicy()),
			WithNativeAgent(NativeSpec{Request: FlowRequest{Name: "chat"}, Profile: "primary"}),
		)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		conv, err := NewConversation(stack, "chat", &sinkRuntime{token: token},
			WithConversationRuns(runs),
			WithConversationEventLog(events),
			WithConversationCheckpoints(cps),
		)
		if err != nil {
			t.Fatalf("NewConversation: %v", err)
		}
		for _, sendErr := range conv.Send(ctx, "s1", types.Message{Role: types.RoleUser, Blocks: []types.Block{types.Text{Text: "Book for Anna Petrova"}}}) {
			if sendErr != nil {
				t.Fatalf("Send: %v", sendErr)
			}
		}

		var sinks []string
		history, err := log.Load(ctx, "s1")
		if err != nil {
			t.Fatalf("session log load: %v", err)
		}
		for _, m := range history.Messages {
			sinks = append(sinks, sinkSerialize(m))
		}
		for _, e := range events.appended {
			sinks = append(sinks, sinkSerialize(e))
		}
		for _, cp := range cps.put {
			sinks = append(sinks, sinkSerialize(cp))
		}
		if sinkContains(sinks, "Anna Petrova") {
			for i, s := range sinks {
				if strings.Contains(s, "Anna Petrova") {
					t.Fatalf("sink %d contains the raw entity: %.400s", i, s)
				}
			}
			t.Fatal("a stored sink contains the raw entity")
		}

		m, err := maps.Get(ctx, "t1", "s1")
		if err != nil {
			t.Fatalf("redaction map get: %v", err)
		}
		found := false
		for entryToken, entity := range m.Entries {
			if entity.Value != "Anna Petrova" {
				continue
			}
			if entity.Kind != types.EntityPerson {
				t.Fatalf("entity kind = %v", entity.Kind)
			}
			if entryToken == "" || strings.Contains(entryToken, "Anna Petrova") {
				t.Fatalf("token = %q", entryToken)
			}
			found = true
		}
		if !found {
			t.Fatal("redaction map holds no entity entry")
		}
		if _, err := maps.Get(ctx, "other-tenant", "s1"); !errors.Is(err, stores.ErrRedactionMapNotFound) {
			t.Fatalf("cross-tenant get err = %v, want ErrRedactionMapNotFound", err)
		}
	})
}
