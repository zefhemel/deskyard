package engine

import (
	"context"
	"errors"
)

var ErrReclaimUnsupported = errors.New("memory reclamation requires a rootful Linux engine with cgroup v2 and available swap")

// MemoryReclaimer is optional: engines without host cgroup access need not implement it.
type MemoryReclaimer interface {
	// ProbeMemoryReclaim verifies the complete helper path on a disposable
	// container. It must not reclaim memory from existing desktops.
	ProbeMemoryReclaim(context.Context) error
	ReclaimMemory(context.Context, string) (MemoryReclaim, error)
}

// MemoryReclaimSupport is the result of the supervisor's startup probe.
type MemoryReclaimSupport struct {
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason,omitempty"`
}

// MemoryReclaim reports resident cgroup memory, including file cache. Swapped
// pages can still consume compressed RAM outside this accounting.
type MemoryReclaim struct {
	RequestedBytes  uint64 `json:"requestedBytes"`
	BeforeBytes     uint64 `json:"beforeBytes"`
	AfterBytes      uint64 `json:"afterBytes"`
	SwapBeforeBytes uint64 `json:"swapBeforeBytes"`
	SwapAfterBytes  uint64 `json:"swapAfterBytes"`
	Partial         bool   `json:"partial"`
}
