package std

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"unsafe"

	"github.com/victorzhuk/gohan/core/types"
)

// AssembleInput carries everything one StablePrefix assembly reads. It
// mirrors the assembly contract: the providers map is keyed by slot and the
// history arrives already loaded, so assembly itself never touches a store.
type AssembleInput = types.AssembleInput

// StablePrefix assembles a request whose byte prefix up to the last
// CacheBreak is identical across runs that differ only after the boundary:
// no timestamps, run IDs or random values precede it, and tools are emitted
// sorted by name.
type StablePrefix struct{}

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
func (StablePrefix) Assemble(ctx context.Context, in AssembleInput) (types.ModelRequest, error) {
	if in.Filter == nil {
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
	return build(ctx, in)
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

	return types.ModelRequest{System: system, Tools: sorted, Messages: messages}, nil
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
