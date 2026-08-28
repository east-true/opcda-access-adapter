package opcua

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// The address space is populated from DA Browse lazily: a branch is browsed the
// first time a client browses its node, not when the server starts.
//
// This matters for the reasons design §35.2 and Annex A both raise. A DA
// address space can be large and can change while the server runs, so browsing
// it all at startup would delay startup, hold a snapshot that drifts, and do
// work for branches no client ever visits. Browsing on demand also keeps every
// DA call on the request path where its failure can be reported to the client
// that caused it.

// PopulationLimits bounds what browsing on demand can cost.
type PopulationLimits struct {
	// MaxNodes caps the whole address space, so a source with a very large or
	// cyclic-looking hierarchy cannot exhaust memory.
	MaxNodes int
	// MaxDepth caps how deep a client can drive the adapter into the source.
	MaxDepth int
	// RefreshInterval is how long a populated branch is reused before it is
	// browsed again. A DA address space can change while the server runs.
	RefreshInterval time.Duration
	// RequestTimeout bounds one DA Browse call.
	RequestTimeout time.Duration
}

func DefaultPopulationLimits() PopulationLimits {
	return PopulationLimits{
		MaxNodes:        50_000,
		MaxDepth:        32,
		RefreshInterval: time.Minute,
		RequestTimeout:  30 * time.Second,
	}
}

func (limits PopulationLimits) validate() error {
	if limits.MaxNodes <= 0 || limits.MaxDepth <= 0 ||
		limits.RefreshInterval <= 0 || limits.RequestTimeout <= 0 {
		return fmt.Errorf("all address space population limits must be positive")
	}
	return nil
}

func (limits PopulationLimits) ValidateForConfiguration() error { return limits.validate() }

// Populator fills the address space from DA Browse on demand.
type Populator struct {
	space   *AddressSpace
	runtime opcda.Runtime
	limits  PopulationLimits

	mu       sync.Mutex
	browsed  map[string]time.Time
	inflight map[string]chan struct{}
}

func NewPopulator(space *AddressSpace, runtime opcda.Runtime, limits PopulationLimits) (*Populator, error) {
	if err := limits.validate(); err != nil {
		return nil, err
	}
	if space == nil || runtime == nil {
		return nil, fmt.Errorf("a populator needs an address space and a DA runtime")
	}
	return &Populator{
		space:    space,
		runtime:  runtime,
		limits:   limits,
		browsed:  make(map[string]time.Time),
		inflight: make(map[string]chan struct{}),
	}, nil
}

// pathKey renders a browse path as a map key. A separator no DA name can
// contain keeps two different paths from colliding.
func pathKey(path []string) string {
	key := ""
	for index, segment := range path {
		if index > 0 {
			key += "\x1f"
		}
		key += segment
	}
	return key
}

// EnsureBranch browses the DA path behind a node if it has not been browsed
// recently, and returns once the address space reflects it.
//
// A DA Browse is serialized on the runtime's owning thread, so concurrent
// clients asking for the same branch wait on one call rather than queueing
// several identical ones.
func (p *Populator) EnsureBranch(ctx context.Context, path []string, now time.Time) error {
	if len(path) > p.limits.MaxDepth {
		return uacpError(StatusBadTooManyOperations,
			"browse depth %d exceeds the %d limit", len(path), p.limits.MaxDepth)
	}
	key := pathKey(path)

	p.mu.Lock()
	if browsedAt, ok := p.browsed[key]; ok && now.Sub(browsedAt) < p.limits.RefreshInterval {
		p.mu.Unlock()
		return nil
	}
	if waiting, running := p.inflight[key]; running {
		p.mu.Unlock()
		// Another caller is already browsing this branch. Waiting for it costs
		// one DA call instead of several identical ones queued behind each
		// other on the runtime's owning thread.
		select {
		case <-waiting:
		case <-ctx.Done():
			return uacpError(StatusBadTimeout, "waiting for a concurrent browse was cancelled")
		}
		// Re-check rather than assuming the other caller succeeded.
		p.mu.Lock()
		browsedAt, ok := p.browsed[key]
		p.mu.Unlock()
		if ok && now.Sub(browsedAt) < p.limits.RefreshInterval {
			return nil
		}
		return uacpError(StatusBadNotConnected, "a concurrent browse of this branch did not succeed")
	}

	done := make(chan struct{})
	p.inflight[key] = done
	p.mu.Unlock()

	err := p.browse(ctx, path, key, now)
	p.mu.Lock()
	delete(p.inflight, key)
	p.mu.Unlock()
	close(done)
	return err
}

func (p *Populator) browse(ctx context.Context, path []string, key string, now time.Time) error {
	browseCtx, cancel := context.WithTimeout(ctx, p.limits.RequestTimeout)
	defer cancel()

	result, err := p.runtime.Browse(browseCtx, opcda.BrowseRequest{
		Path: path, Filter: opcda.BrowseFilterAll,
	})
	if err != nil {
		return err
	}
	// The node budget is checked before the entries are added, so a large
	// branch cannot push the space past its bound and then be trimmed.
	if p.space.SourceNodeCount()+len(result.Entries) > p.limits.MaxNodes {
		return uacpError(StatusBadTooManyOperations,
			"the address space would exceed its %d node limit", p.limits.MaxNodes)
	}
	if err := p.space.PopulateBranch(path, result.Entries); err != nil {
		return uacpError(StatusBadInternalError, "%s", err.Error())
	}

	p.mu.Lock()
	p.browsed[key] = now
	p.mu.Unlock()
	return nil
}

// PathForNode recovers the DA browse path a node stands for. The source folder
// is the empty path; a branch carries its path in its identifier. An item has
// no browse path of its own, and neither does anything outside this adapter's
// namespace.
func PathForNode(id NodeID) ([]string, bool) {
	if id.Namespace != AdapterNamespaceIndex || id.Type != NodeIDTypeString {
		return nil, false
	}
	const branchPrefix = "branch:"
	if len(id.StringID) >= len(branchPrefix) && id.StringID[:len(branchPrefix)] == branchPrefix {
		encoded := id.StringID[len(branchPrefix):]
		if encoded == "" {
			return nil, false
		}
		return splitBranchPath(encoded), true
	}
	return nil, false
}

func splitBranchPath(encoded string) []string {
	segments := make([]string, 0, 4)
	current := ""
	for _, character := range encoded {
		if character == '\x1f' {
			segments = append(segments, current)
			current = ""
			continue
		}
		current += string(character)
	}
	return append(segments, current)
}

// Invalidate forgets what has been browsed, so the next browse of each branch
// goes back to the source. A reconnect invalidates the whole space, because a
// new connection generation may expose a different address space.
func (p *Populator) Invalidate() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.browsed = make(map[string]time.Time)
}

// BrowsedBranchCount reports how many branches are cached, for diagnostics.
func (p *Populator) BrowsedBranchCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.browsed)
}

// itemPropertiesKey namespaces a discovery in the same map branch browses use.
// A branch key is a path and an item key is an ItemID, and the prefix keeps a
// path from ever colliding with an ItemID that spells the same thing.
func itemPropertiesKey(itemID opcda.DAItemID) string { return "properties\x1f" + string(itemID) }

// EnsureItemProperties asks the source which OPC 10000-8 Table A.1 properties
// an item has and attaches the corresponding property nodes.
//
// A source that does not implement IOPCItemProperties is not an error. It has
// no properties to offer, the item simply has none, and browsing it succeeds
// with the references it does have.
func (p *Populator) EnsureItemProperties(ctx context.Context, itemID opcda.DAItemID, now time.Time) error {
	key := itemPropertiesKey(itemID)

	p.mu.Lock()
	if discoveredAt, ok := p.browsed[key]; ok && now.Sub(discoveredAt) < p.limits.RefreshInterval {
		p.mu.Unlock()
		return nil
	}
	if waiting, running := p.inflight[key]; running {
		p.mu.Unlock()
		select {
		case <-waiting:
		case <-ctx.Done():
			return uacpError(StatusBadTimeout, "waiting for a concurrent property discovery was cancelled")
		}
		p.mu.Lock()
		discoveredAt, ok := p.browsed[key]
		p.mu.Unlock()
		if ok && now.Sub(discoveredAt) < p.limits.RefreshInterval {
			return nil
		}
		return uacpError(StatusBadNotConnected, "a concurrent property discovery did not succeed")
	}

	done := make(chan struct{})
	p.inflight[key] = done
	p.mu.Unlock()

	err := p.discoverItemProperties(ctx, itemID, key, now)
	p.mu.Lock()
	delete(p.inflight, key)
	p.mu.Unlock()
	close(done)
	return err
}

func (p *Populator) discoverItemProperties(ctx context.Context, itemID opcda.DAItemID, key string, now time.Time) error {
	discoveryCtx, cancel := context.WithTimeout(ctx, p.limits.RequestTimeout)
	defer cancel()

	available, err := p.runtime.AvailableItemProperties(discoveryCtx, string(itemID))
	if err != nil {
		if adapterErr, ok := opcda.AsAdapterError(err); ok && adapterErr.Code == opcda.CodePropertiesUnsupported {
			// The source offers no properties. Recording the answer stops the
			// adapter asking again for every browse of every item.
			p.mu.Lock()
			p.browsed[key] = now
			p.mu.Unlock()
			return nil
		}
		return err
	}
	if err := p.space.AttachItemProperties(itemID, available, p.limits.MaxNodes); err != nil {
		// The discovery is not recorded, so the next browse tries again rather
		// than serving whatever partial answer a truncation would have left.
		return uacpError(StatusBadTooManyOperations, "%s", err.Error())
	}
	p.mu.Lock()
	p.browsed[key] = now
	p.mu.Unlock()
	return nil
}
