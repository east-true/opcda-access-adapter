package opcua

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// browsingRuntime records what the populator asked the DA source for.
type browsingRuntime struct {
	stubRuntime

	mu      sync.Mutex
	paths   [][]string
	entries map[string][]opcda.BrowseEntry
	err     error
	calls   atomic.Int64
	blockOn chan struct{}
}

func newBrowsingRuntime() *browsingRuntime {
	return &browsingRuntime{entries: make(map[string][]opcda.BrowseEntry)}
}

func (r *browsingRuntime) setEntries(path []string, entries []opcda.BrowseEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[pathKey(path)] = entries
}

func (r *browsingRuntime) Browse(ctx context.Context, request opcda.BrowseRequest) (opcda.BrowseResult, error) {
	r.calls.Add(1)
	if r.blockOn != nil {
		select {
		case <-r.blockOn:
		case <-ctx.Done():
			return opcda.BrowseResult{}, ctx.Err()
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = append(r.paths, append([]string(nil), request.Path...))
	if r.err != nil {
		return opcda.BrowseResult{}, r.err
	}
	return opcda.BrowseResult{Path: request.Path, Entries: r.entries[pathKey(request.Path)]}, nil
}

func (r *browsingRuntime) browsedPaths() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]string(nil), r.paths...)
}

func newTestPopulator(t *testing.T, runtime opcda.Runtime, limits PopulationLimits) (*Populator, *AddressSpace) {
	t.Helper()
	space := testAddressSpace(t)
	populator, err := NewPopulator(space, runtime, limits)
	if err != nil {
		t.Fatalf("NewPopulator: %v", err)
	}
	return populator, space
}

func TestEnsureBranchPopulatesFromTheSource(t *testing.T) {
	runtime := newBrowsingRuntime()
	runtime.setEntries(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryBranch, Name: "Test"},
		{Kind: opcda.BrowseEntryItem, Name: "Top", ItemID: itemID("Top")},
	})
	runtime.setEntries([]string{"Test"}, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Nested", ItemID: itemID("Test/Nested")},
	})
	populator, space := newTestPopulator(t, runtime, DefaultPopulationLimits())

	if err := populator.EnsureBranch(context.Background(), nil, channelEpoch); err != nil {
		t.Fatal(err)
	}
	if _, ok := space.Node(ItemNodeID("Top")); !ok {
		t.Fatal("the root item is missing")
	}
	// A nested branch is not browsed until it is asked for.
	if _, ok := space.Node(ItemNodeID("Test/Nested")); ok {
		t.Fatal("a nested branch was browsed eagerly")
	}

	if err := populator.EnsureBranch(context.Background(), []string{"Test"}, channelEpoch); err != nil {
		t.Fatal(err)
	}
	if _, ok := space.Node(ItemNodeID("Test/Nested")); !ok {
		t.Fatal("the nested item is missing after browsing its branch")
	}
	paths := runtime.browsedPaths()
	if len(paths) != 2 || len(paths[0]) != 0 || paths[1][0] != "Test" {
		t.Fatalf("browsed paths = %v", paths)
	}
}

// A branch already browsed within the refresh interval is not browsed again.
func TestEnsureBranchReusesARecentBrowse(t *testing.T) {
	runtime := newBrowsingRuntime()
	limits := DefaultPopulationLimits()
	limits.RefreshInterval = time.Minute
	populator, _ := newTestPopulator(t, runtime, limits)

	if err := populator.EnsureBranch(context.Background(), nil, channelEpoch); err != nil {
		t.Fatal(err)
	}
	if err := populator.EnsureBranch(context.Background(), nil, channelEpoch.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if calls := runtime.calls.Load(); calls != 1 {
		t.Fatalf("DA browse calls = %d, want 1", calls)
	}

	// Past the interval it goes back to the source, because a DA address space
	// can change while the server runs.
	if err := populator.EnsureBranch(context.Background(), nil, channelEpoch.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if calls := runtime.calls.Load(); calls != 2 {
		t.Fatalf("DA browse calls = %d, want 2", calls)
	}
}

// Concurrent callers asking for the same branch share one DA call rather than
// queueing several identical ones on the runtime's owning thread.
func TestConcurrentEnsureBranchIssuesOneBrowse(t *testing.T) {
	runtime := newBrowsingRuntime()
	runtime.blockOn = make(chan struct{})
	populator, _ := newTestPopulator(t, runtime, DefaultPopulationLimits())

	const callers = 8
	var waiting sync.WaitGroup
	errs := make([]error, callers)
	for index := 0; index < callers; index++ {
		waiting.Add(1)
		go func(slot int) {
			defer waiting.Done()
			errs[slot] = populator.EnsureBranch(context.Background(), nil, channelEpoch)
		}(index)
	}
	// Let the callers pile up behind the in-flight browse before releasing it.
	time.Sleep(50 * time.Millisecond)
	close(runtime.blockOn)
	waiting.Wait()

	for index, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", index, err)
		}
	}
	if calls := runtime.calls.Load(); calls != 1 {
		t.Fatalf("DA browse calls = %d, want 1 for %d concurrent callers", calls, callers)
	}
}

// A failed browse is not recorded as done, so the next caller retries rather
// than seeing an empty branch as authoritative.
func TestFailedBrowseIsNotCached(t *testing.T) {
	runtime := newBrowsingRuntime()
	runtime.err = opcda.NewAdapterError(opcda.CodeRuntimeUnavailable, "not connected")
	populator, _ := newTestPopulator(t, runtime, DefaultPopulationLimits())

	if err := populator.EnsureBranch(context.Background(), nil, channelEpoch); err == nil {
		t.Fatal("a failed browse was reported as success")
	}
	if populator.BrowsedBranchCount() != 0 {
		t.Fatal("a failed browse was cached")
	}
	runtime.err = nil
	if err := populator.EnsureBranch(context.Background(), nil, channelEpoch); err != nil {
		t.Fatalf("the retry failed: %v", err)
	}
	if calls := runtime.calls.Load(); calls != 2 {
		t.Fatalf("DA browse calls = %d, want a retry", calls)
	}
}

func TestPopulationIsBounded(t *testing.T) {
	runtime := newBrowsingRuntime()
	entries := make([]opcda.BrowseEntry, 0, 20)
	for index := 0; index < 20; index++ {
		name := string(rune('a' + index))
		entries = append(entries, opcda.BrowseEntry{
			Kind: opcda.BrowseEntryItem, Name: name, ItemID: itemID(name),
		})
	}
	runtime.setEntries(nil, entries)

	limits := DefaultPopulationLimits()
	// Twenty entries exceeds this. The budget counts what the source added,
	// not the server's own standard nodes.
	limits.MaxNodes = 10
	populator, space := newTestPopulator(t, runtime, limits)

	err := populator.EnsureBranch(context.Background(), nil, channelEpoch)
	if err == nil {
		t.Fatal("population past the node limit was accepted")
	}
	if got := codecStatus(t, err); got != StatusBadTooManyOperations {
		t.Fatalf("status = %s", got.Hex())
	}
	// The budget is checked before the entries are added, so nothing partial
	// was left behind.
	if space.SourceNodeCount() > limits.MaxNodes {
		t.Fatalf("the space holds %d source nodes past its %d limit",
			space.SourceNodeCount(), limits.MaxNodes)
	}

	// Depth is bounded too, so a client cannot drive the adapter arbitrarily
	// deep into the source.
	deepLimits := DefaultPopulationLimits()
	deepLimits.MaxDepth = 2
	deep, _ := newTestPopulator(t, newBrowsingRuntime(), deepLimits)
	if err := deep.EnsureBranch(context.Background(), []string{"a", "b", "c"}, channelEpoch); err == nil {
		t.Fatal("a browse past the depth limit was accepted")
	}
}

// A reconnect may expose a different address space, so invalidation sends the
// next browse back to the source.
func TestInvalidateForcesARebrowse(t *testing.T) {
	runtime := newBrowsingRuntime()
	populator, _ := newTestPopulator(t, runtime, DefaultPopulationLimits())
	if err := populator.EnsureBranch(context.Background(), nil, channelEpoch); err != nil {
		t.Fatal(err)
	}
	populator.Invalidate()
	if populator.BrowsedBranchCount() != 0 {
		t.Fatal("invalidate did not clear the cache")
	}
	if err := populator.EnsureBranch(context.Background(), nil, channelEpoch); err != nil {
		t.Fatal(err)
	}
	if calls := runtime.calls.Load(); calls != 2 {
		t.Fatalf("DA browse calls = %d, want a re-browse", calls)
	}
}

func TestPathForNode(t *testing.T) {
	// A branch carries its full path, including names with awkward characters.
	path := []string{"Test", "Sub Group", "MiXeD.CaSe"}
	recovered, ok := PathForNode(BranchNodeID(path))
	if !ok {
		t.Fatal("a branch node had no path")
	}
	if len(recovered) != len(path) {
		t.Fatalf("path = %v, want %v", recovered, path)
	}
	for index, segment := range path {
		if recovered[index] != segment {
			t.Fatalf("segment %d = %q, want %q", index, recovered[index], segment)
		}
	}

	// An item has no browse path of its own.
	if _, ok := PathForNode(ItemNodeID("Test/Float")); ok {
		t.Fatal("an item reported a browse path")
	}
	// Nor does anything outside this adapter's namespace.
	if _, ok := PathForNode(NumericNodeID(0, NodeIDObjectsFolder)); ok {
		t.Fatal("a standard node reported a browse path")
	}
	if _, ok := PathForNode(StringNodeID(AdapterNamespaceIndex, "branch:")); ok {
		t.Fatal("an empty branch path was accepted")
	}
}

// Browse fills a branch from the source before reading it, so a client sees the
// source's current contents.
func TestBrowseServicePopulatesOnDemand(t *testing.T) {
	runtime := newBrowsingRuntime()
	runtime.setEntries(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Live", ItemID: itemID("Live")},
	})
	space := testAddressSpace(t)
	service, err := NewBrowseService(space, DefaultBrowseLimits())
	if err != nil {
		t.Fatal(err)
	}
	populator, err := NewPopulator(space, runtime, DefaultPopulationLimits())
	if err != nil {
		t.Fatal(err)
	}
	service.AttachPopulator(populator)

	response, err := service.Browse(context.Background(),
		browseRequest(browseAll(space.SourceFolderID())), channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results[0].References) != 1 {
		t.Fatalf("references = %d, want the populated item", len(response.Results[0].References))
	}
	if response.Results[0].References[0].BrowseName.Name != "Live" {
		t.Fatalf("browse name = %q", response.Results[0].References[0].BrowseName.Name)
	}
}

// A population failure is reported for that node alone; the rest of the request
// is unaffected.
func TestBrowsePopulationFailureIsPerNode(t *testing.T) {
	runtime := newBrowsingRuntime()
	runtime.err = opcda.NewAdapterError(opcda.CodeRuntimeUnavailable, "not connected")
	space := testAddressSpace(t)
	service, err := NewBrowseService(space, DefaultBrowseLimits())
	if err != nil {
		t.Fatal(err)
	}
	populator, err := NewPopulator(space, runtime, DefaultPopulationLimits())
	if err != nil {
		t.Fatal(err)
	}
	service.AttachPopulator(populator)

	response, err := service.Browse(context.Background(), browseRequest(
		browseAll(space.SourceFolderID()),
		browseAll(NumericNodeID(0, NodeIDObjectsFolder)),
	), channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if response.Results[0].StatusCode != StatusBadNotConnected {
		t.Fatalf("source folder = %s, want Bad_NotConnected", response.Results[0].StatusCode.Hex())
	}
	// A standard node needs no population and is unaffected.
	if response.Results[1].StatusCode != StatusGood {
		t.Fatalf("Objects folder = %s", response.Results[1].StatusCode.Hex())
	}
}

// Without a source attached only the standard nodes exist, and browsing them
// still works.
func TestBrowseWithoutAPopulator(t *testing.T) {
	space := testAddressSpace(t)
	service, err := NewBrowseService(space, DefaultBrowseLimits())
	if err != nil {
		t.Fatal(err)
	}
	response, err := service.Browse(context.Background(),
		browseRequest(browseAll(NumericNodeID(0, NodeIDRootFolder))), channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if response.Results[0].StatusCode != StatusGood {
		t.Fatalf("status = %s", response.Results[0].StatusCode.Hex())
	}
}

func TestPopulationLimitsValidation(t *testing.T) {
	if err := DefaultPopulationLimits().ValidateForConfiguration(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*PopulationLimits){
		"zero nodes":   func(l *PopulationLimits) { l.MaxNodes = 0 },
		"zero depth":   func(l *PopulationLimits) { l.MaxDepth = 0 },
		"zero refresh": func(l *PopulationLimits) { l.RefreshInterval = 0 },
		"zero timeout": func(l *PopulationLimits) { l.RequestTimeout = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			limits := DefaultPopulationLimits()
			mutate(&limits)
			if err := limits.ValidateForConfiguration(); err == nil {
				t.Fatal("invalid limits were accepted")
			}
			if _, err := NewPopulator(testAddressSpace(t), newBrowsingRuntime(), limits); err == nil {
				t.Fatal("a populator was built from invalid limits")
			}
		})
	}
	if _, err := NewPopulator(nil, newBrowsingRuntime(), DefaultPopulationLimits()); err == nil {
		t.Fatal("a populator was built with no address space")
	}
	if _, err := NewPopulator(testAddressSpace(t), nil, DefaultPopulationLimits()); err == nil {
		t.Fatal("a populator was built with no runtime")
	}
}
