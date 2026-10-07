package gohan

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"slices"
	"strings"

	"github.com/victorzhuk/gohan/core/chains"
	"github.com/victorzhuk/gohan/core/runtime"
	"github.com/victorzhuk/gohan/core/stores"
	"github.com/victorzhuk/gohan/core/types"
)

// nativeTurnConfig binds one resolved native configuration into the per-turn
// wiring the effects consume: the composed model chain around the selected
// model, the assembler with the resolved PromptSet as the single system
// insertion point, the tool registry, the governed call path through the
// resolved tool chain, the run limits and the batch scheduler strategy.
// The approval gate and the batch reservation hook stay unwired: the
// conversation wiring supplies both per run.
func (s *Stack) nativeTurnConfig(cfg *resolvedNativeConfig) turnConfig {
	toolset := nativeToolset(cfg.tools)
	tc := turnConfig{
		// Supervision wraps only the provider, so the chain steps and
		// the middleware above it keep running on the caller's goroutine.
		model:        recordRunCost(runModelChain(cfg.modelChain, deprecatedRoute(s.telemetry, cfg.profile, NewModelStream(cfg.model).Generate, s.successorModelFunc(cfg.profile)))),
		assemble:     withProjections(cfg.contextPolicy, cfg.profile, nativeAssemble(s.prompts, cfg.assemble, cfg.redactor, cfg.redactionPolicy, s.stores.RedactionMaps, newBlobAssembly(cfg.profile.Name, cfg.model, s.stores))),
		providerGate: providerDeclarationGate(cfg),
		maxTurns:     cfg.limits.MaxTurns,
		limits:       cfg.limits,
		profile:      cfg.profile,
		specs:        cfg.specs,
		taintPolicy:  cfg.taintPolicy,
		instruction:  cloneBlocks(cfg.instruction),
		clients:      cfg.clients,
		scheduler: runtime.SchedulerConfig{
			EffectOf:    nativeEffectOf(cfg.tools),
			Parallel:    cfg.plan.ParallelTools,
			MaxParallel: cfg.plan.MaxParallelTools,
		},
		exec:     governedToolExec(cfg.toolChain, toolset, specLookup(cfg.specs), cfg.clients),
		verifier: cfg.verifier,
		journal:  s.stores.Journal,
	}
	tc.modelJournal = len(cfg.tools) == 0
	for _, t := range cfg.tools {
		if e := t.Spec().Effect; e == types.Idempotent || e == types.SideEffect {
			tc.modelJournal = true
			break
		}
	}
	return tc
}

// nativeRun creates one run's effect pair over the bound configuration. The
// two callbacks share the per-run scope, so a turn's pending batch crosses
// from the model effect into the batch effect the way the runtime
// alternates the phases.
func nativeRun(c turnConfig, history, input []types.Message) runtime.AgentRun {
	env := &turnEnv{c: c, msgs: slices.Clone(history)}
	if len(input) > 0 {
		env.msgs = append(env.msgs, input...)
	}
	return runtime.AgentRun{
		Input: input,
		ModelEffect: func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
			return modelEffect(env.effectCtx(ctx), st)
		},
		BatchEffect: func(ctx context.Context, st runtime.State) (runtime.State, []types.Event, runtime.Status, error) {
			return batchEffect(env.effectCtx(ctx), st)
		},
	}
}

// effectCtx installs the per-run scope the effects share. The lifecycle
// steps one effect at a time, so the lazy sink lookup never races.
func (env *turnEnv) effectCtx(ctx context.Context) context.Context {
	if env.sink == nil {
		env.sink, _ = types.SinkFrom(ctx)
	}
	return types.WithTaintWindow(withTurnEnv(ctx, env), rawTaintWindow(env.msgs))
}

func rawTaintWindow(msgs []types.Message) []types.Block {
	var blocks []types.Block
	for _, msg := range msgs {
		blocks = append(blocks, msg.Blocks...)
	}
	return blocks
}

// recordRunCost reports the run ledger's cumulative cost after one model
// invocation. Charging steps composed inside the chain record too; the
// ledger suppresses repeat emissions at an unchanged cost.
func recordRunCost(next types.ModelFunc) types.ModelFunc {
	return func(ctx context.Context, req types.ModelRequest) (out iter.Seq2[types.ModelChunk, error]) {
		return func(yield func(types.ModelChunk, error) bool) {
			for chunk, err := range next(ctx, req) {
				if !yield(chunk, err) {
					return
				}
			}
			if st, ok := chains.LimitsStateFrom(ctx); ok {
				st.RecordCost(ctx)
			}
		}
	}
}

// runModelChain composes the chain around the model: step 0 outermost, the
// model innermost. A step panic becomes a StepError, matching the tool side.
func runModelChain(ch chains.ModelChain, next types.ModelFunc) types.ModelFunc {
	for i := len(ch) - 1; i >= 0; i-- {
		step := ch[i]
		use := step.Use
		inner := next
		next = func(ctx context.Context, req types.ModelRequest) (out iter.Seq2[types.ModelChunk, error]) {
			defer func() {
				r := recover()
				if r == nil {
					return
				}
				var err error
				if e, isErr := r.(error); isErr {
					err = &chains.StepError{Step: step.Name, Err: e}
				} else {
					err = &chains.StepError{Step: step.Name, Err: fmt.Errorf("%v", r)}
				}
				out = func(yield func(types.ModelChunk, error) bool) {
					yield(types.ModelChunk{}, err)
				}
			}()
			if use == nil {
				return inner(ctx, req)
			}
			return use(inner)(ctx, req)
		}
	}
	return next
}

// governedToolExec runs one call through the resolved tool chain with the
// call's original identity: the chain steps and CallTool never see a zero
// ToolUse. Each step's Applies predicate is evaluated against the call's
// resolved spec; a step that does not govern the call composes over it
// without running. A nil Applies, or an unresolved tool name, keeps the
// step in the composition.
func governedToolExec(ch chains.ToolChain, set ToolSet, lookup func(string) (types.ToolSpec, bool), clientMaps ...map[string]*http.Client) func(context.Context, types.ToolUse) (types.ToolResult, error) {
	var clients map[string]*http.Client
	if len(clientMaps) > 0 {
		clients = clientMaps[0]
	}
	return func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
		spec, haveSpec := lookup(call.Name)
		if client := clients[call.Name]; client != nil && haveSpec {
			ctx = withEgressClient(ctx, client)
		}
		next := func(ctx context.Context, call types.ToolUse) (types.ToolResult, error) {
			return CallTool(ctx, set, call.Name, call.Args)
		}
		for i := len(ch) - 1; i >= 0; i-- {
			step := ch[i]
			use := step.Use
			skip := use != nil && step.Applies != nil && haveSpec && !step.Applies(spec)
			inner := next
			next = func(ctx context.Context, call types.ToolUse) (res types.ToolResult, err error) {
				if skip {
					return inner(ctx, call)
				}
				defer func() {
					r := recover()
					if r == nil {
						return
					}
					if e, isErr := r.(error); isErr {
						err = &chains.StepError{Step: step.Name, Err: e}
						return
					}
					err = &chains.StepError{Step: step.Name, Err: fmt.Errorf("%v", r)}
				}()
				if use == nil {
					return inner(ctx, call)
				}
				return use(inner)(ctx, call)
			}
		}
		return next(ctx, call)
	}
}

// assemblyInput builds the request input one model invocation hands the
// assembler: the resolved instruction, profile and registered tool specs,
// the run identity from the context and the loaded history. The turn input
// stays empty: the native wiring already loaded the input into the
// history. Providers and Filter stay unset; the native path has neither
// capability.
func (env *turnEnv) assemblyInput(ctx context.Context, history []types.Message) types.AssembleInput {
	ri, _ := types.RunInfoFrom(ctx)
	return types.AssembleInput{
		Run:     ri,
		Profile: env.c.profile,
		System:  env.c.instruction,
		Tools:   env.c.specs,
		History: history,
	}
}

// nativeAssemble applies the resolved assembler and prepends the PromptSet
// block. This is the single prompt insertion point of the native path; a
// set without one model-facing string inserts nothing.
func nativeAssemble(
	ps chains.PromptSet,
	assemble func(context.Context, types.AssembleInput) (types.ModelRequest, error),
	redactor types.Redactor,
	policy types.RedactionPolicy,
	maps stores.RedactionMaps,
	blobs blobAssembly,
) func(context.Context, types.AssembleInput) (types.ModelRequest, error) {
	return func(ctx context.Context, in types.AssembleInput) (types.ModelRequest, error) {
		req, err := assemble(ctx, in)
		if err != nil {
			return types.ModelRequest{}, err
		}
		if req, err = blobs.resolve(ctx, req); err != nil {
			return types.ModelRequest{}, err
		}
		if redactor != nil && len(policy.Detectors) > 0 {
			req, err = redactModelRequest(ctx, req, in.Run, redactor, policy, maps)
			if err != nil {
				return types.ModelRequest{}, err
			}
		}
		req.Messages = ProjectOperatorTurns(req.Messages, ps.OperatorTurn)
		if block, ok := promptBlock(ps); ok {
			req.System = append([]types.Block{block}, req.System...)
		}
		return req, nil
	}
}

// historyVersionKey carries the assembled history's store version through
// the assembly context, so the projection stage can hand Project an honest
// History identity without a store handle.
type historyVersionKey struct{}

func withHistoryVersion(ctx context.Context, v int64) context.Context {
	return context.WithValue(ctx, historyVersionKey{}, v)
}

func historyVersionFrom(ctx context.Context) int64 {
	v, _ := ctx.Value(historyVersionKey{}).(int64)
	return v
}

// withProjections applies the resolved policy's projections in declared
// order over a copy of the loaded history before the caller assembler runs.
// An empty projection list keeps the plain path with zero extra work; a
// projection error fails the assembly before any model call.
func withProjections(
	policy stores.ContextPolicy,
	profile types.ModelProfile,
	next func(context.Context, types.AssembleInput) (types.ModelRequest, error),
) func(context.Context, types.AssembleInput) (types.ModelRequest, error) {
	if len(policy.Projections) == 0 {
		return next
	}
	return func(ctx context.Context, in types.AssembleInput) (types.ModelRequest, error) {
		h := stores.History{Messages: slices.Clone(in.History), Version: historyVersionFrom(ctx)}
		for _, p := range policy.Projections {
			ph, err := p.Project(ctx, h, profile)
			if err != nil {
				return types.ModelRequest{}, fmt.Errorf("project history: %w", err)
			}
			h = ph
		}
		in.History = h.Messages
		return next(ctx, in)
	}
}

// ProjectOperatorTurns projects every operator-origin turn with a
// preceding system marker carrying the operator-turn prompt. The
// projection is idempotent: a turn already preceded by its marker passes
// through untouched, so a request that crosses two projections keeps
// exactly one marker per operator turn. An empty marker disables the
// projection.
func ProjectOperatorTurns(messages []types.Message, marker string) []types.Message {
	if marker == "" {
		return messages
	}
	out := make([]types.Message, 0, len(messages)+4)
	for i, msg := range messages {
		if !hasOperatorBlock(msg.Blocks) {
			out = append(out, msg)
			continue
		}
		if i > 0 && isOperatorFence(messages[i-1], marker) {
			out = append(out, msg)
			continue
		}
		out = append(out, operatorFence(marker), msg)
	}
	return out
}

func hasOperatorBlock(blocks []types.Block) bool {
	for _, b := range blocks {
		if b.BlockOrigin().Kind == types.OriginOperator {
			return true
		}
	}
	return false
}

func isOperatorFence(msg types.Message, marker string) bool {
	for _, b := range msg.Blocks {
		if txt, ok := b.(types.Text); ok && txt.BlockOrigin().Kind == types.OriginSystem && txt.Text == marker {
			return true
		}
	}
	return false
}

func operatorFence(marker string) types.Message {
	return types.Message{
		Role: types.RoleUser,
		Blocks: []types.Block{types.Text{
			BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginSystem}},
			Text:      marker,
		}},
	}
}

func redactModelRequest(ctx context.Context, req types.ModelRequest, run types.RunInfo, redactor types.Redactor, policy types.RedactionPolicy, maps stores.RedactionMaps) (types.ModelRequest, error) {
	ctx = types.WithRunInfo(ctx, run)
	blocks := append([]types.Block(nil), req.System...)
	for _, msg := range req.Messages {
		blocks = append(blocks, msg.Blocks...)
	}
	redacted, m, err := redactor.Redact(ctx, blocks, policy)
	if err != nil {
		return types.ModelRequest{}, fmt.Errorf("redact model request: %w", err)
	}
	systemCount := len(req.System)
	req.System = append([]types.Block(nil), redacted[:systemCount]...)
	offset := systemCount
	req.Messages = append([]types.Message(nil), req.Messages...)
	for i := range req.Messages {
		count := len(req.Messages[i].Blocks)
		req.Messages[i].Blocks = append([]types.Block(nil), redacted[offset:offset+count]...)
		offset += count
	}
	old, err := loadRedactionMap(ctx, run, maps)
	if err != nil {
		return types.ModelRequest{}, err
	}
	if len(old.Entries) > 0 {
		if m.Entries == nil {
			m.Entries = make(map[string]types.Entity, len(old.Entries))
		}
		for token, entity := range old.Entries {
			m.Entries[token] = entity
		}
	}
	if err := maps.Put(ctx, run.Principal.Tenant, run.SessionID, m); err != nil {
		return types.ModelRequest{}, fmt.Errorf("store redaction map: %w", err)
	}
	return req, nil
}

func loadRedactionMap(ctx context.Context, run types.RunInfo, maps stores.RedactionMaps) (types.RedactionMap, error) {
	if maps == nil {
		return types.RedactionMap{}, fmt.Errorf("redaction map store missing")
	}
	m, err := maps.Get(ctx, run.Principal.Tenant, run.SessionID)
	if errors.Is(err, stores.ErrRedactionMapNotFound) {
		return types.RedactionMap{Tenant: run.Principal.Tenant, Session: run.SessionID}, nil
	}
	if err != nil {
		return types.RedactionMap{}, fmt.Errorf("load redaction map: %w", err)
	}
	return m, nil
}

// promptBlock renders the model-facing PromptSet strings as one system
// block. Version names the set for hashing, not the model, and stays out.
func promptBlock(ps chains.PromptSet) (types.Text, bool) {
	facing := []string{
		ps.FenceOpen,
		ps.FenceClose,
		ps.DataNotInstructions,
		ps.OutcomeUnknown,
		ps.ReadBackHint,
		ps.OutputRefHint,
		ps.RepairInstruction,
		ps.NotesPreamble,
		ps.OperatorTurn,
	}
	parts := make([]string, 0, len(facing))
	for _, s := range facing {
		if s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return types.Text{}, false
	}
	return types.Text{
		BlockBase: types.BlockBase{Origin: types.Origin{Kind: types.OriginSystem}},
		Text:      strings.Join(parts, "\n"),
	}, true
}

func nativeToolset(tools []types.Tool) ToolSetFunc {
	return func(name string) (types.Tool, bool) {
		for _, t := range tools {
			if t.Spec().Name == name {
				return t, true
			}
		}
		return nil, false
	}
}

// nativeEffectOf resolves a call's effect from the registered tools. An
// unknown name resolves to the zero effect, so the scheduler keeps it
// sequential.
func nativeEffectOf(tools []types.Tool) func(types.ToolUse) types.Effect {
	effects := make(map[string]types.Effect, len(tools))
	for _, t := range tools {
		effects[t.Spec().Name] = t.Spec().Effect
	}
	return func(call types.ToolUse) types.Effect {
		return effects[call.Name]
	}
}
