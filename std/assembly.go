package std

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"unsafe"

	"github.com/victorzhuk/gohan/core/types"
	"github.com/victorzhuk/gohan/std/cache"
	"github.com/victorzhuk/gohan/std/tokens"
)

// AssembleInput carries everything one StablePrefix assembly reads. It
// mirrors the assembly contract: the providers map is keyed by slot and the
// history arrives already loaded, so assembly itself never touches a store.
type AssembleInput = types.AssembleInput

// StablePrefix assembles a request whose byte prefix up to the last
// CacheBreak is identical across runs that differ only after the boundary:
// no timestamps, run IDs or random values precede it, and tools are emitted
// sorted by name.
//
// A StablePrefix built through NewStablePrefix consults the content-keyed
// store the caller supplied: the pointer memo below stays the filter-free
// fast path, and a memo miss falls through to cache.Key. A hit returns the
// stored prefix without serving the freshly built one; a repopulating miss
// writes the built prefix. The zero value carries no store and only
// memoizes.
type StablePrefix struct {
	store *cache.Store
	scope cache.Scope
}

// PrefixOption adjusts a StablePrefix at construction.
type PrefixOption func(*StablePrefix)

// WithCacheStore wires the exact-match store a lookup consults on a memo
// miss. The scope class is the one the cache step declares for its entries;
// an undeclared scope is shared across the tenant's principals.
func WithCacheStore(s *cache.Store, scope cache.Scope) PrefixOption {
	return func(p *StablePrefix) {
		p.store = s
		if scope == "" {
			scope = cache.ScopeShared
		}
		p.scope = scope
	}
}

// NewStablePrefix returns an assembler configured by the options.
func NewStablePrefix(opts ...PrefixOption) StablePrefix {
	var p StablePrefix
	for _, opt := range opts {
		opt(&p)
	}
	return p
}

// The prefix memo holds the last assembled request for a filter-free input.
// The key captures the input's identity: slice backing pointers and lengths,
// the provider map identity, and the scalar and string values the providers
// observe. Inputs that are equal but freshly allocated simply rebuild; only
// identical inputs hit. A hit therefore requires the caller's slices and the
// provider map to be unchanged since the miss, so a caller must treat the
// assembled request and its inputs as read-only for the run's life. A
// non-nil filter never consults or updates the memo: a Go func value exposes
// no identity (reflect's Pointer and UnsafePointer both return the code
// pointer for kind Func), so no filter key can be sound.
var (
	prefixMu   sync.Mutex
	prefixLast prefixKey
	prefixHave bool
	prefixReq  types.ModelRequest
)

type sliceID struct {
	ptr unsafe.Pointer
	n   int
}

type prefixKey struct {
	providers uintptr
	tools     sliceID
	system    sliceID
	history   sliceID
	input     sliceID
	flow      string
	sessionID string
	runID     string
	rootRunID string
	parentID  string
	depth     int
	turn      int
	subject   string
	tenant    string
	scopes    sliceID
	latency   types.LatencyClass
	feature   string
	cluster   string
	costCtr   string
	residency string
	releaseID string
	variant   string
	mode      types.RunMode
}

func prefixID(in AssembleInput) prefixKey {
	ri := in.Run
	return prefixKey{
		providers: reflect.ValueOf(in.Providers).Pointer(),
		tools:     sliceID{unsafe.Pointer(unsafe.SliceData(in.Tools)), len(in.Tools)},
		system:    sliceID{unsafe.Pointer(unsafe.SliceData(in.System)), len(in.System)},
		history:   sliceID{unsafe.Pointer(unsafe.SliceData(in.History)), len(in.History)},
		input:     sliceID{unsafe.Pointer(unsafe.SliceData(in.Input)), len(in.Input)},
		flow:      ri.Flow,
		sessionID: ri.SessionID,
		runID:     ri.RunID,
		rootRunID: ri.RootRunID,
		parentID:  ri.ParentRunID,
		depth:     ri.Depth,
		turn:      ri.Turn,
		subject:   ri.Principal.Subject,
		tenant:    ri.Principal.Tenant,
		scopes:    sliceID{unsafe.Pointer(unsafe.SliceData(ri.Principal.Scopes)), len(ri.Principal.Scopes)},
		latency:   ri.LatencyClass,
		feature:   ri.CostTags.Feature,
		cluster:   ri.CostTags.Environment,
		costCtr:   ri.CostTags.CostCenter,
		residency: ri.Residency,
		releaseID: ri.ReleaseID,
		variant:   ri.Variant,
		mode:      ri.Mode,
	}
}

func cachedPrefix(in AssembleInput) (types.ModelRequest, bool) {
	key := prefixID(in)
	prefixMu.Lock()
	defer prefixMu.Unlock()
	if prefixHave && prefixLast == key {
		return prefixReq, true
	}
	return types.ModelRequest{}, false
}

func storePrefix(in AssembleInput, req types.ModelRequest) {
	prefixMu.Lock()
	prefixLast = prefixID(in)
	prefixReq = req
	prefixHave = true
	prefixMu.Unlock()
}

// Assemble builds the request in the canonical order: system instruction,
// tool specs sorted by name, static providers, a CacheBreak, session
// providers, a second CacheBreak, history, turn providers, new input.
//
// With a store wired, a filter-free assembly consults the memo first and
// falls through to cache.Key on a memo miss. A hit serves the stored
// prefix and fills Usage.CachedInputTokens through the request mark; a
// repopulating miss writes the built prefix and marks
// Usage.CacheWriteTokens. The cache computes no cost.
func (p StablePrefix) Assemble(ctx context.Context, in AssembleInput) (types.ModelRequest, error) {
	if in.Filter != nil {
		return build(ctx, in)
	}
	if p.store == nil {
		if req, ok := cachedPrefix(in); ok {
			return req, nil
		}
		req, err := build(ctx, in)
		if err != nil {
			return types.ModelRequest{}, err
		}
		storePrefix(in, req)
		return req, nil
	}
	if req, ok := cachedPrefix(in); ok {
		return p.serve(ctx, in, req, true), nil
	}
	req, err := build(ctx, in)
	if err != nil {
		return types.ModelRequest{}, err
	}
	if stored, ok := p.lookup(ctx, in, req); ok {
		storePrefix(in, stored)
		return p.serve(ctx, in, stored, true), nil
	}
	if err := p.write(ctx, in, req); err != nil {
		return types.ModelRequest{}, err
	}
	storePrefix(in, req)
	return p.serve(ctx, in, req, false), nil
}

// serve counts the consult and returns the prefix marked with the outcome
// the charge site reads. Tokens come from the deterministic estimator, so
// a served prefix always reports the same count.
func (p StablePrefix) serve(ctx context.Context, in AssembleInput, req types.ModelRequest, hit bool) types.ModelRequest {
	o := cache.Outcome{Hit: hit, Tokens: tokens.Heuristic.Estimate(req, in.Profile.Caps)}
	countCache(ctx, hit)
	return cache.Mark(req, o)
}

// lookup builds the contract key over the freshly built request and
// consults the store. The tenant and the principal come from the run info,
// not the caller's context, so a lookup never depends on context wiring.
func (p StablePrefix) lookup(ctx context.Context, in AssembleInput, req types.ModelRequest) (types.ModelRequest, bool) {
	key, err := cache.Key(p.keyCtx(ctx, in), cache.Input{
		Scope:    p.scope,
		Manifest: manifestOf(in),
		Request:  req,
	})
	if err != nil {
		return types.ModelRequest{}, false
	}
	return p.store.Get(key)
}

// write stores the built prefix under the contract key. A key error means
// the request cannot be content-addressed; the miss stands and the run
// carries the freshly built request unmarked.
func (p StablePrefix) write(ctx context.Context, in AssembleInput, req types.ModelRequest) error {
	key, err := cache.Key(p.keyCtx(ctx, in), cache.Input{
		Scope:    p.scope,
		Manifest: manifestOf(in),
		Request:  req,
	})
	if err != nil {
		return nil
	}
	p.store.Put(key, req)
	return nil
}

func (p StablePrefix) keyCtx(ctx context.Context, in AssembleInput) context.Context {
	return types.WithPrincipal(ctx, in.Run.Principal)
}

func countCache(ctx context.Context, hit bool) {
	tel := types.TelemetryFrom(ctx)
	if tel == nil {
		return
	}
	if hit {
		tel.Count(ctx, "gohan.cache.hit", 1)
		return
	}
	tel.Count(ctx, "gohan.cache.miss", 1)
}

// manifestOf computes the manifest identity the key hashes: the system
// blocks carry the prompts, the provider slot layout stands in for the
// chains, the tool specs for the toolset, and the profile pins the model
// Version.
func manifestOf(in AssembleInput) cache.Manifest {
	return cache.Manifest{
		Prompts:      hashValues(blockValues(in.System)),
		Chains:       slotLayout(in.Providers),
		Toolset:      hashValues(toolValues(in.Tools)),
		ModelVersion: in.Profile.Version,
	}
}

func slotLayout(providers map[types.ContextSlot][]types.ContextProvider) string {
	slots := make([]int, 0, len(providers))
	for slot, ps := range providers {
		slots = append(slots, int(slot)*1000+len(ps))
	}
	sort.Ints(slots)
	return fmt.Sprint(slots)
}

func toolValues(specs []types.ToolSpec) []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, fmt.Sprintf("%s\x1f%s\x1f%s\x1f%v\x1f%s", s.Name, s.Description, s.Schema, s.Effect, strings.Join(s.RequiredScopes, ",")))
	}
	return out
}

func blockValues(blocks []types.Block) []string {
	out := make([]string, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, blockValue(b))
	}
	return out
}

func blockValue(b types.Block) string {
	switch v := b.(type) {
	case fmt.Stringer:
		return fmt.Sprintf("%T:%s", b, v.String())
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			return fmt.Sprintf("%T", b)
		}
		return string(raw)
	}
}

func hashValues(vals []string) string {
	h := sha256.New()
	for _, v := range vals {
		_, _ = fmt.Fprintf(h, "%d\x1f%s\x1e", len(v), v)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func build(ctx context.Context, in AssembleInput) (types.ModelRequest, error) {
	tools, err := NarrowTools(in.Tools, in.Filter, in.Run.Turn)
	if err != nil {
		return types.ModelRequest{}, fmt.Errorf("narrow tools: %w", err)
	}
	sorted := make([]types.ToolSpec, len(tools))
	copy(sorted, tools)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	system := make([]types.Block, 0, len(in.System)+4)
	system = append(system, in.System...)
	system, err = appendSlot(ctx, system, in, types.SlotStatic, in.Run)
	if err != nil {
		return types.ModelRequest{}, err
	}
	system = append(system, types.CacheBreak{})
	system, err = appendSlot(ctx, system, in, types.SlotSession, in.Run)
	if err != nil {
		return types.ModelRequest{}, err
	}
	system = append(system, types.CacheBreak{})

	messages := make([]types.Message, 0, len(in.History)+len(in.Input)+1)
	messages = append(messages, in.History...)
	turn, err := provideSlot(ctx, in, types.SlotTurn, in.Run)
	if err != nil {
		return types.ModelRequest{}, err
	}
	if len(turn) > 0 {
		messages = append(messages, types.Message{Role: types.RoleUser, Blocks: turn})
	}
	messages = append(messages, in.Input...)

	return types.ModelRequest{
		System:   system,
		Tools:    sorted,
		Messages: messages,
		Metadata: types.RequestMetadata{CostTags: in.Run.CostTags},
	}, nil
}

func appendSlot(ctx context.Context, system []types.Block, in AssembleInput, slot types.ContextSlot, ri types.RunInfo) ([]types.Block, error) {
	blocks, err := provideSlot(ctx, in, slot, ri)
	if err != nil {
		return system, err
	}
	return append(system, blocks...), nil
}

func provideSlot(ctx context.Context, in AssembleInput, slot types.ContextSlot, ri types.RunInfo) ([]types.Block, error) {
	var blocks []types.Block
	for _, p := range in.Providers[slot] {
		got, err := p.Provide(ctx, ri)
		if err != nil {
			return nil, fmt.Errorf("provide slot %d: %w", slot, err)
		}
		blocks = append(blocks, got...)
	}
	return blocks, nil
}
