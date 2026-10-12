package std

import (
	"time"

	"github.com/victorzhuk/gohan/core/types"
)

// SunsetNoticeWindow is the recommended notice window Build applies to a
// pinned profile's Sunset: a profile that retires inside the window fails
// the build unless the warn-only option is set. A zero Sunset means no
// announced retirement and is never a failure.
const SunsetNoticeWindow = 30 * 24 * time.Hour

// LatencyTimeouts is the recommended per-class timeout table a profile's
// zero ModelTimeout falls back to. The index names the slot: 0 connect,
// 1 first chunk, 2 idle.
var LatencyTimeouts = map[types.LatencyClass][3]time.Duration{
	types.Interactive: {5 * time.Second, 10 * time.Second, 15 * time.Second},
	types.Agentic:     {10 * time.Second, 30 * time.Second, 60 * time.Second},
	types.Batch:       {30 * time.Second, 2 * time.Minute, 5 * time.Minute},
}

// ControlWait is the recommended bound on an AwaitingControl suspension:
// the gate holds the pending call for the wait and then denies it, so the
// run continues instead of waiting for a control write forever.
const ControlWait = 5 * time.Minute

// StreamBuffer is the recommended per-call chunk capacity, and the depth of
// the conversation hand-off: a provider that produces faster than its
// consumer reads blocks on its next read instead of racing ahead, and no
// chunk is dropped or reordered.
const StreamBuffer = 64
