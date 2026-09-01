package opcua

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

func testBrowseService(t *testing.T, limits BrowseLimits) (*BrowseService, *AddressSpace) {
	t.Helper()
	space := testAddressSpace(t)
	service, err := NewBrowseService(space, limits)
	if err != nil {
		t.Fatalf("NewBrowseService: %v", err)
	}
	return service, space
}

func browseAll(node NodeID) BrowseDescription {
	return BrowseDescription{
		NodeID:          node,
		BrowseDirection: BrowseDirectionForward,
		ResultMask:      ResultMaskAll,
	}
}

func browseRequest(descriptions ...BrowseDescription) BrowseRequest {
	return BrowseRequest{
		Header:        RequestHeader{RequestHandle: 1, AdditionalHeader: NullExtensionObject()},
		NodesToBrowse: descriptions,
	}
}

// The encoding identifiers and enumeration values come from the OPC Foundation
// tables.
func TestBrowseIdentifiersAndEnums(t *testing.T) {
	ids := map[uint32]uint32{
		BrowseRequestEncodingID: 527, BrowseResponseEncodingID: 530,
		BrowseNextRequestEncodingID: 533, BrowseNextResponseEncodingID: 536,
	}
	for got, want := range ids {
		if got != want {
			t.Fatalf("encoding id %d, want %d", got, want)
		}
	}
	directions := map[BrowseDirection]int32{
		BrowseDirectionForward: 0, BrowseDirectionInverse: 1,
		BrowseDirectionBoth: 2, BrowseDirectionInvalid: 3,
	}
	for direction, want := range directions {
		if int32(direction) != want {
			t.Fatalf("BrowseDirection %d, want %d", int32(direction), want)
		}
	}
	// resultMask bits from Table 34.
	masks := map[uint32]uint32{
		ResultMaskReferenceType: 1, ResultMaskIsForward: 2, ResultMaskNodeClass: 4,
		ResultMaskBrowseName: 8, ResultMaskDisplayName: 16, ResultMaskTypeDefinition: 32,
	}
	for got, want := range masks {
		if got != want {
			t.Fatalf("result mask %d, want %d", got, want)
		}
	}
}

func TestBrowseWalksTheAddressSpace(t *testing.T) {
	service, space := testBrowseService(t, DefaultBrowseLimits())
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryBranch, Name: "Test"},
		{Kind: opcda.BrowseEntryItem, Name: "Top", ItemID: itemID("Top")},
	}); err != nil {
		t.Fatal(err)
	}

	response, err := service.Browse(context.Background(), testSession, browseRequest(browseAll(space.SourceFolderID())), channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 1 || response.Results[0].StatusCode != StatusGood {
		t.Fatalf("results = %+v", response.Results)
	}
	references := response.Results[0].References
	if len(references) != 2 {
		t.Fatalf("references = %d, want 2", len(references))
	}
	byName := map[string]ReferenceDescription{}
	for _, reference := range references {
		byName[reference.BrowseName.Name] = reference
	}
	branch, ok := byName["Test"]
	if !ok || branch.NodeClass != NodeClassObject {
		t.Fatalf("branch reference = %+v", branch)
	}
	item, ok := byName["Top"]
	if !ok || item.NodeClass != NodeClassVariable {
		t.Fatalf("item reference = %+v", item)
	}
	// The Annex A.3.1.3 floor, which is what an item carries until its
	// properties are known.
	if item.TypeDefinition.NodeID.Numeric != NodeIDDataItemType {
		t.Fatalf("item type definition = %s", item.TypeDefinition.NodeID)
	}
	if !item.IsForward {
		t.Fatal("a child reference was not forward")
	}
}

// Table 34: the size and order of the results match nodesToBrowse, so a failing
// node occupies its slot rather than shortening the list.
func TestBrowseResultsKeepRequestOrderIncludingFailures(t *testing.T) {
	service, space := testBrowseService(t, DefaultBrowseLimits())
	unknown := StringNodeID(AdapterNamespaceIndex, "item:missing")
	response, err := service.Browse(context.Background(), testSession, browseRequest(
		browseAll(space.SourceFolderID()),
		browseAll(unknown),
		browseAll(NumericNodeID(0, NodeIDObjectsFolder)),
	), channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 3 {
		t.Fatalf("results = %d, want 3", len(response.Results))
	}
	if response.Results[0].StatusCode != StatusGood {
		t.Fatalf("first result = %s", response.Results[0].StatusCode.Hex())
	}
	if response.Results[1].StatusCode != StatusBadNodeIdUnknown {
		t.Fatalf("second result = %s, want Bad_NodeIdUnknown", response.Results[1].StatusCode.Hex())
	}
	if response.Results[2].StatusCode != StatusGood {
		t.Fatalf("third result = %s", response.Results[2].StatusCode.Hex())
	}
	// The service call itself succeeded; the failure is per node.
	if response.Header.ServiceResult != StatusGood {
		t.Fatalf("service result = %s", response.Header.ServiceResult.Hex())
	}
}

func TestBrowseDirectionSelectsReferences(t *testing.T) {
	service, space := testBrowseService(t, DefaultBrowseLimits())
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "One", ItemID: itemID("One")},
	}); err != nil {
		t.Fatal(err)
	}

	counts := map[BrowseDirection]int{}
	for _, direction := range []BrowseDirection{
		BrowseDirectionForward, BrowseDirectionInverse, BrowseDirectionBoth,
	} {
		description := browseAll(space.SourceFolderID())
		description.BrowseDirection = direction
		response, err := service.Browse(context.Background(), testSession, browseRequest(description), channelEpoch)
		if err != nil {
			t.Fatal(err)
		}
		counts[direction] = len(response.Results[0].References)
	}
	if counts[BrowseDirectionForward] != 1 {
		t.Fatalf("forward = %d, want the child", counts[BrowseDirectionForward])
	}
	if counts[BrowseDirectionInverse] != 1 {
		t.Fatalf("inverse = %d, want the parent", counts[BrowseDirectionInverse])
	}
	if counts[BrowseDirectionBoth] != 2 {
		t.Fatalf("both = %d, want child and parent", counts[BrowseDirectionBoth])
	}

	// The invalid direction is refused per node.
	description := browseAll(space.SourceFolderID())
	description.BrowseDirection = BrowseDirectionInvalid
	response, err := service.Browse(context.Background(), testSession, browseRequest(description), channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if response.Results[0].StatusCode != StatusBadBrowseDirectionInvalid {
		t.Fatalf("status = %s", response.Results[0].StatusCode.Hex())
	}
}

// Table 34: a zero nodeClassMask returns all classes; otherwise it is a mask.
func TestBrowseNodeClassMaskIsAMask(t *testing.T) {
	service, space := testBrowseService(t, DefaultBrowseLimits())
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryBranch, Name: "Folder"},
		{Kind: opcda.BrowseEntryItem, Name: "Item", ItemID: itemID("Item")},
	}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		mask uint32
		want int
	}{
		{"zero returns everything", 0, 2},
		{"objects only", uint32(NodeClassObject), 1},
		{"variables only", uint32(NodeClassVariable), 1},
		{"objects and variables", uint32(NodeClassObject | NodeClassVariable), 2},
		{"methods only", uint32(NodeClassMethod), 0},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			description := browseAll(space.SourceFolderID())
			description.NodeClassMask = testCase.mask
			response, err := service.Browse(context.Background(), testSession, browseRequest(description), channelEpoch)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(response.Results[0].References); got != testCase.want {
				t.Fatalf("references = %d, want %d", got, testCase.want)
			}
		})
	}
}

func TestBrowseReferenceTypeFilter(t *testing.T) {
	service, space := testBrowseService(t, DefaultBrowseLimits())
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Item", ItemID: itemID("Item")},
		{Kind: opcda.BrowseEntryBranch, Name: "Folder"},
	}); err != nil {
		t.Fatal(err)
	}

	// Annex A.3.1.2: a folder standing for a DA branch references child
	// branches with Organizes and DA leaves with HasComponent. A client that
	// filters a Browse by reference type sees the difference, and this test
	// asserted the opposite until the deviation was found.
	browseByType := func(t *testing.T, referenceType uint32) []ReferenceDescription {
		t.Helper()
		description := browseAll(space.SourceFolderID())
		description.ReferenceTypeID = NumericNodeID(0, referenceType)
		response, err := service.Browse(context.Background(), testSession, browseRequest(description), channelEpoch)
		if err != nil {
			t.Fatal(err)
		}
		return response.Results[0].References
	}

	organized := browseByType(t, NodeIDOrganizes)
	if len(organized) != 1 || organized[0].BrowseName.Name != "Folder" {
		t.Fatalf("Organizes returned %+v, want the branch alone", organized)
	}
	components := browseByType(t, NodeIDHasComponent)
	if len(components) != 1 || components[0].BrowseName.Name != "Item" {
		t.Fatalf("HasComponent returned %+v, want the item alone", components)
	}
	// Both are hierarchical, so a client that does not filter still sees both.
	description := browseAll(space.SourceFolderID())
	description.ReferenceTypeID = NumericNodeID(0, NodeIDHierarchicalRefs)
	description.IncludeSubtypes = true
	response, err := service.Browse(context.Background(), testSession, browseRequest(description), channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results[0].References) != 2 {
		t.Fatalf("hierarchical references = %d, want both children", len(response.Results[0].References))
	}

	// A reference type that names nothing is an error, not a filter that
	// silently matches nothing.
	description.ReferenceTypeID = NumericNodeID(0, 999_999)
	response, err = service.Browse(context.Background(), testSession, browseRequest(description), channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if response.Results[0].StatusCode != StatusBadReferenceTypeIDInvalid {
		t.Fatalf("status = %s", response.Results[0].StatusCode.Hex())
	}
}

// Table 34 makes resultMask a request for specific fields, so anything unasked
// is omitted rather than sent anyway.
func TestBrowseResultMaskOmitsUnrequestedFields(t *testing.T) {
	service, space := testBrowseService(t, DefaultBrowseLimits())
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Item", ItemID: itemID("Item")},
	}); err != nil {
		t.Fatal(err)
	}

	description := browseAll(space.SourceFolderID())
	description.ResultMask = ResultMaskBrowseName
	response, err := service.Browse(context.Background(), testSession, browseRequest(description), channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	reference := response.Results[0].References[0]
	if reference.BrowseName.Name != "Item" {
		t.Fatalf("browse name = %q", reference.BrowseName.Name)
	}
	if reference.DisplayName.Text != "" {
		t.Fatalf("display name was sent unasked: %q", reference.DisplayName.Text)
	}
	if reference.NodeClass != NodeClassUnspecified {
		t.Fatalf("node class was sent unasked: %d", reference.NodeClass)
	}
	if !reference.ReferenceTypeID.IsNull() {
		t.Fatalf("reference type was sent unasked: %s", reference.ReferenceTypeID)
	}
	if reference.IsForward {
		t.Fatal("isForward was sent unasked")
	}
	// The target NodeId is not maskable; a client always needs it.
	if reference.NodeID.NodeID.IsNull() {
		t.Fatal("the target node id was omitted")
	}
}

// Table 168: type definitions exist only for Object and Variable.
func TestTypeDefinitionOnlyForObjectsAndVariables(t *testing.T) {
	space := testAddressSpace(t)
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Item", ItemID: itemID("Item")},
	}); err != nil {
		t.Fatal(err)
	}
	source, _ := space.Node(space.SourceFolderID())
	var reference Reference
	for _, candidate := range source.References {
		if candidate.IsForward {
			reference = candidate
		}
	}
	// A reference class the space does not produce still obeys the rule.
	reference.NodeClass = NodeClassMethod
	described := applyResultMask(reference, ResultMaskAll)
	if !described.TypeDefinition.NodeID.IsNull() {
		t.Fatalf("a method carried a type definition: %s", described.TypeDefinition.NodeID)
	}
}

func TestBrowseContinuationPoints(t *testing.T) {
	limits := DefaultBrowseLimits()
	limits.MaxReferencesPerNode = 2
	service, space := testBrowseService(t, limits)

	entries := make([]opcda.BrowseEntry, 0, 5)
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		entries = append(entries, opcda.BrowseEntry{
			Kind: opcda.BrowseEntryItem, Name: name, ItemID: itemID(name),
		})
	}
	if err := space.PopulateBranch(nil, entries); err != nil {
		t.Fatal(err)
	}

	response, err := service.Browse(context.Background(), testSession, browseRequest(browseAll(space.SourceFolderID())), channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	result := response.Results[0]
	if len(result.References) != 2 || len(result.ContinuationPoint) == 0 {
		t.Fatalf("first page = %d refs, point %v", len(result.References), result.ContinuationPoint)
	}
	seen := len(result.References)

	point := result.ContinuationPoint
	for pages := 0; pages < 5 && len(point) > 0; pages++ {
		next, nextErr := service.BrowseNext(testSession, BrowseNextRequest{
			Header:             RequestHeader{RequestHandle: 2, AdditionalHeader: NullExtensionObject()},
			ContinuationPoints: [][]byte{point},
		}, channelEpoch)
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		if next.Results[0].StatusCode != StatusGood {
			t.Fatalf("continuation status = %s", next.Results[0].StatusCode.Hex())
		}
		seen += len(next.Results[0].References)
		point = next.Results[0].ContinuationPoint
	}
	if seen != 5 {
		t.Fatalf("saw %d references across pages, want 5", seen)
	}
	// Every point was consumed, so none is left held.
	if service.ContinuationPointCount() != 0 {
		t.Fatalf("%d continuation points are still held", service.ContinuationPointCount())
	}
}

// A continuation point is consumed by use, so a stale one cannot be replayed.
func TestContinuationPointIsConsumedAndValidated(t *testing.T) {
	limits := DefaultBrowseLimits()
	limits.MaxReferencesPerNode = 1
	service, space := testBrowseService(t, limits)
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "a", ItemID: itemID("a")},
		{Kind: opcda.BrowseEntryItem, Name: "b", ItemID: itemID("b")},
	}); err != nil {
		t.Fatal(err)
	}
	response, err := service.Browse(context.Background(), testSession, browseRequest(browseAll(space.SourceFolderID())), channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	point := response.Results[0].ContinuationPoint

	next := BrowseNextRequest{
		Header:             RequestHeader{RequestHandle: 2, AdditionalHeader: NullExtensionObject()},
		ContinuationPoints: [][]byte{point},
	}
	if _, err := service.BrowseNext(testSession, next, channelEpoch); err != nil {
		t.Fatal(err)
	}
	// Replaying the same point is refused.
	replayed, err := service.BrowseNext(testSession, next, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Results[0].StatusCode != StatusBadContinuationPointInvalid {
		t.Fatalf("replay status = %s", replayed.Results[0].StatusCode.Hex())
	}

	// An invented point is refused too.
	invented := BrowseNextRequest{
		Header:             RequestHeader{AdditionalHeader: NullExtensionObject()},
		ContinuationPoints: [][]byte{{1, 2, 3, 4}},
	}
	result, err := service.BrowseNext(testSession, invented, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if result.Results[0].StatusCode != StatusBadContinuationPointInvalid {
		t.Fatalf("invented point status = %s", result.Results[0].StatusCode.Hex())
	}
}

// Table 37: releasing returns empty results and frees the server's resources.
func TestBrowseNextReleasesContinuationPoints(t *testing.T) {
	limits := DefaultBrowseLimits()
	limits.MaxReferencesPerNode = 1
	service, space := testBrowseService(t, limits)
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "a", ItemID: itemID("a")},
		{Kind: opcda.BrowseEntryItem, Name: "b", ItemID: itemID("b")},
	}); err != nil {
		t.Fatal(err)
	}
	response, err := service.Browse(context.Background(), testSession, browseRequest(browseAll(space.SourceFolderID())), channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if service.ContinuationPointCount() != 1 {
		t.Fatalf("points held = %d", service.ContinuationPointCount())
	}

	released, err := service.BrowseNext(testSession, BrowseNextRequest{
		Header:                    RequestHeader{AdditionalHeader: NullExtensionObject()},
		ReleaseContinuationPoints: true,
		ContinuationPoints:        [][]byte{response.Results[0].ContinuationPoint},
	}, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if len(released.Results) != 0 || len(released.Diagnostics) != 0 {
		t.Fatalf("release returned %d results", len(released.Results))
	}
	if service.ContinuationPointCount() != 0 {
		t.Fatalf("points held after release = %d", service.ContinuationPointCount())
	}
}

// A client that abandons a browse must not hold server resources forever.
func TestContinuationPointsExpire(t *testing.T) {
	limits := DefaultBrowseLimits()
	limits.MaxReferencesPerNode = 1
	limits.ContinuationPointExpiry = time.Minute
	service, space := testBrowseService(t, limits)
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "a", ItemID: itemID("a")},
		{Kind: opcda.BrowseEntryItem, Name: "b", ItemID: itemID("b")},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Browse(context.Background(), testSession, browseRequest(browseAll(space.SourceFolderID())), channelEpoch); err != nil {
		t.Fatal(err)
	}
	if removed := service.ExpireContinuationPoints(channelEpoch.Add(30 * time.Second)); removed != 0 {
		t.Fatalf("a live point was reclaimed")
	}
	if removed := service.ExpireContinuationPoints(channelEpoch.Add(2 * time.Minute)); removed != 1 {
		t.Fatalf("reclaimed %d points, want 1", removed)
	}
}

func TestBrowseRefusesOversizedAndEmptyRequests(t *testing.T) {
	limits := DefaultBrowseLimits()
	limits.MaxNodesPerBrowse = 2
	service, space := testBrowseService(t, limits)

	_, err := service.Browse(context.Background(), testSession, browseRequest(), channelEpoch)
	if err == nil {
		t.Fatal("an empty browse request was accepted")
	}
	if got := codecStatus(t, err); got != StatusBadNothingToDo {
		t.Fatalf("status = %s, want Bad_NothingToDo", got.Hex())
	}

	descriptions := make([]BrowseDescription, 3)
	for index := range descriptions {
		descriptions[index] = browseAll(space.SourceFolderID())
	}
	_, err = service.Browse(context.Background(), testSession, browseRequest(descriptions...), channelEpoch)
	if err == nil {
		t.Fatal("an oversized browse request was accepted")
	}
	if got := codecStatus(t, err); got != StatusBadTooManyOperations {
		t.Fatalf("status = %s, want Bad_TooManyOperations", got.Hex())
	}
}

// A null viewId means the entire address space; no other view exists here.
func TestBrowseRefusesAnUnknownView(t *testing.T) {
	service, space := testBrowseService(t, DefaultBrowseLimits())
	request := browseRequest(browseAll(space.SourceFolderID()))
	request.View.ViewID = NumericNodeID(0, 999)
	_, err := service.Browse(context.Background(), testSession, request, channelEpoch)
	if err == nil {
		t.Fatal("an unknown view was accepted")
	}
	if got := codecStatus(t, err); got != StatusBadViewIDUnknown {
		t.Fatalf("status = %s", got.Hex())
	}
}

// Table 34: zero means the client imposes no limit, so the server's bound
// applies; a smaller client value tightens it.
func TestRequestedMaxReferencesIsHonoured(t *testing.T) {
	limits := DefaultBrowseLimits()
	limits.MaxReferencesPerNode = 10
	service, space := testBrowseService(t, limits)
	entries := make([]opcda.BrowseEntry, 0, 4)
	for _, name := range []string{"a", "b", "c", "d"} {
		entries = append(entries, opcda.BrowseEntry{
			Kind: opcda.BrowseEntryItem, Name: name, ItemID: itemID(name),
		})
	}
	if err := space.PopulateBranch(nil, entries); err != nil {
		t.Fatal(err)
	}

	request := browseRequest(browseAll(space.SourceFolderID()))
	response, err := service.Browse(context.Background(), testSession, request, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results[0].References) != 4 {
		t.Fatalf("unlimited request returned %d", len(response.Results[0].References))
	}

	request.RequestedMaxReferencesPerNode = 2
	response, err = service.Browse(context.Background(), testSession, request, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results[0].References) != 2 || len(response.Results[0].ContinuationPoint) == 0 {
		t.Fatalf("limited request returned %d refs", len(response.Results[0].References))
	}

	// A client asking for more than the server allows does not raise the bound.
	request.RequestedMaxReferencesPerNode = 1000
	response, err = service.Browse(context.Background(), testSession, request, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results[0].References) != 4 {
		t.Fatalf("a client could not raise the server bound: %d", len(response.Results[0].References))
	}
}

func TestBrowseRoundTrip(t *testing.T) {
	limits := DefaultBinaryLimits()
	request := browseRequest(browseAll(NumericNodeID(0, NodeIDObjectsFolder)))
	request.RequestedMaxReferencesPerNode = 8

	encoder := newTestEncoder(t, limits)
	encoder.WriteBrowseRequest(request)
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	decoder := newTestDecoder(t, encoded, limits)
	identifier, err := decoder.ReadServiceTypeID()
	if err != nil || identifier != BrowseRequestEncodingID {
		t.Fatalf("TypeId = %d, %v", identifier, err)
	}
	decoded, err := decoder.ReadBrowseRequest()
	if err != nil {
		t.Fatal(err)
	}
	if decoded.RequestedMaxReferencesPerNode != 8 || len(decoded.NodesToBrowse) != 1 {
		t.Fatalf("request = %+v", decoded)
	}
	if decoded.NodesToBrowse[0].ResultMask != ResultMaskAll {
		t.Fatalf("result mask = %d", decoded.NodesToBrowse[0].ResultMask)
	}

	encoder = newTestEncoder(t, limits)
	encoder.WriteBrowseResponse(BrowseResponse{
		Header: ResponseHeader{ServiceResult: StatusGood, AdditionalHeader: NullExtensionObject()},
		Results: []BrowseResult{{
			StatusCode:        StatusGood,
			ContinuationPoint: []byte{1, 2},
			References: []ReferenceDescription{{
				ReferenceTypeID: NumericNodeID(0, NodeIDOrganizes),
				IsForward:       true,
				NodeID:          ExpandedNodeID{NodeID: StringNodeID(1, "item:Test/Float")},
				BrowseName:      QualifiedName{Namespace: 1, Name: "Test/Float"},
				NodeClass:       NodeClassVariable,
			}},
		}},
	})
	encoded, err = encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	decoder = newTestDecoder(t, encoded, limits)
	if _, err := decoder.ReadServiceTypeID(); err != nil {
		t.Fatal(err)
	}
	response, err := decoder.ReadBrowseResponse()
	if err != nil {
		t.Fatal(err)
	}
	reference := response.Results[0].References[0]
	// The exact DA ItemID survives the wire round trip inside the NodeId.
	if reference.NodeID.NodeID.StringID != "item:Test/Float" {
		t.Fatalf("node id = %q", reference.NodeID.NodeID.StringID)
	}
	if reference.BrowseName.Name != "Test/Float" {
		t.Fatalf("browse name = %q", reference.BrowseName.Name)
	}
}

// Table 36 makes Bad_BrowseDirectionInvalid an operation level result, so an
// undefined direction decodes and then fails on its own node. Refusing it while
// decoding would drop the connection over one field of one operation, taking
// the rest of the request and every other session on the channel with it.
func TestAnUndefinedBrowseDirectionFailsItsOwnNode(t *testing.T) {
	limits := DefaultBinaryLimits()
	for _, direction := range []int32{-1, int32(BrowseDirectionInvalid), 4, 1000} {
		encoder := newTestEncoder(t, limits)
		encoder.WriteNodeID(NumericNodeID(0, 85))
		encoder.WriteInt32(direction)
		encoder.WriteNodeID(NumericNodeID(0, 0))
		encoder.WriteBoolean(false)
		encoder.WriteUInt32(0)
		encoder.WriteUInt32(0)
		encoded, err := encoder.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		decoder := newTestDecoder(t, encoded, limits)
		decoded, err := decoder.ReadBrowseDescription()
		if err != nil {
			t.Fatalf("BrowseDirection %d failed decoding: %v", direction, err)
		}
		if decoded.BrowseDirection.Valid() {
			t.Fatalf("BrowseDirection %d passed as a Table 112 value", direction)
		}
	}

	// And the service answers per node, leaving its neighbours alone.
	service, space := testBrowseService(t, DefaultBrowseLimits())
	populateItems(t, space, 2)
	bad := browseAll(space.SourceFolderID())
	bad.BrowseDirection = 77
	response, err := service.Browse(context.Background(), testSession,
		browseRequest(browseAll(space.SourceFolderID()), bad), channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if response.Results[0].StatusCode != StatusGood {
		t.Fatalf("the good node = %s", response.Results[0].StatusCode.Hex())
	}
	if response.Results[1].StatusCode != StatusBadBrowseDirectionInvalid {
		t.Fatalf("the bad node = %s, want Bad_BrowseDirectionInvalid",
			response.Results[1].StatusCode.Hex())
	}
}

func TestBrowseLimitsValidation(t *testing.T) {
	if err := DefaultBrowseLimits().ValidateForConfiguration(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*BrowseLimits){
		"zero nodes":      func(l *BrowseLimits) { l.MaxNodesPerBrowse = 0 },
		"zero references": func(l *BrowseLimits) { l.MaxReferencesPerNode = 0 },
		"zero points":     func(l *BrowseLimits) { l.MaxContinuationPoints = 0 },
		"zero expiry":     func(l *BrowseLimits) { l.ContinuationPointExpiry = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			limits := DefaultBrowseLimits()
			mutate(&limits)
			if err := limits.ValidateForConfiguration(); err == nil {
				t.Fatal("invalid limits were accepted")
			}
			if _, err := NewBrowseService(testAddressSpace(t), limits); err == nil {
				t.Fatal("a service was built from invalid limits")
			}
		})
	}
}

// Table 34's includeSubtypes was decoded and then ignored, so a client that
// browsed for HierarchicalReferences with subtypes included — which is how a
// generic client walks an address space — saw nothing at all. This project's
// own probe browsed with an unspecified reference type and never noticed; a
// third-party client did, immediately.
func TestBrowseHonoursIncludeSubtypes(t *testing.T) {
	service, space := testBrowseService(t, DefaultBrowseLimits())
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "Int32", ItemID: itemID("Test/Int32")},
	}); err != nil {
		t.Fatal(err)
	}

	hierarchical := browseAll(space.SourceFolderID())
	hierarchical.ReferenceTypeID = NumericNodeID(0, NodeIDHierarchicalRefs)

	// Without subtypes an Organizes reference does not match, because the
	// filter is then an equality test.
	response, err := service.Browse(context.Background(), testSession, browseRequest(hierarchical), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(response.Results[0].References); got != 0 {
		t.Fatalf("references without subtypes = %d, want none", got)
	}

	// With subtypes the Organizes reference matches, because Organizes is a
	// subtype of HierarchicalReferences.
	hierarchical.IncludeSubtypes = true
	response, err = service.Browse(context.Background(), testSession, browseRequest(hierarchical), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(response.Results[0].References); got != 1 {
		t.Fatalf("references with subtypes = %d, want the item", got)
	}
}

// The subtype relation is the NodeSet's, not an invented one: a reference type
// matches its own supertypes and nothing else.
func TestReferenceTypeSubtypeRelation(t *testing.T) {
	organizes := NumericNodeID(0, NodeIDOrganizes)
	hasProperty := NumericNodeID(0, NodeIDHasProperty)
	hasTypeDefinition := NumericNodeID(0, NodeIDHasTypeDefinition)
	references := NumericNodeID(0, NodeIDReferences)
	hierarchical := NumericNodeID(0, NodeIDHierarchicalRefs)
	nonHierarchical := NumericNodeID(0, NodeIDNonHierarchicalRefs)
	aggregates := NumericNodeID(0, NodeIDAggregates)

	for _, testCase := range []struct {
		name      string
		requested NodeID
		actual    NodeID
		want      bool
	}{
		{"Organizes is hierarchical", hierarchical, organizes, true},
		{"HasProperty aggregates", aggregates, hasProperty, true},
		{"HasProperty is hierarchical", hierarchical, hasProperty, true},
		{"everything is a Reference", references, hasTypeDefinition, true},
		{"HasTypeDefinition is not hierarchical", hierarchical, hasTypeDefinition, false},
		{"HasTypeDefinition is non-hierarchical", nonHierarchical, hasTypeDefinition, true},
		{"Organizes does not aggregate", aggregates, organizes, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := referenceTypeMatches(testCase.requested, testCase.actual, true); got != testCase.want {
				t.Fatalf("match = %v, want %v", got, testCase.want)
			}
			// Without includeSubtypes only an exact match counts.
			if referenceTypeMatches(testCase.requested, testCase.actual, false) {
				t.Fatal("a subtype matched without includeSubtypes")
			}
		})
	}
}

// populateItems fills the source folder with a named number of items.
func populateItems(t *testing.T, space *AddressSpace, count int) {
	t.Helper()
	entries := make([]opcda.BrowseEntry, 0, count)
	for index := 0; index < count; index++ {
		name := fmt.Sprintf("item%02d", index)
		entries = append(entries, opcda.BrowseEntry{
			Kind: opcda.BrowseEntryItem, Name: name, ItemID: itemID(name),
		})
	}
	if err := space.PopulateBranch(nil, entries); err != nil {
		t.Fatal(err)
	}
}

// OPC 10000-4 5.9.3.1 defines "too large" as exceeding "the maximum number of
// results to return that was specified by the Client in the original Browse
// request", so that limit governs every BrowseNext continuing it. BrowseNext
// has no parameter to restate it, which is exactly why it has to be carried.
func TestBrowseNextKeepsTheOriginalRequestsLimit(t *testing.T) {
	service, space := testBrowseService(t, DefaultBrowseLimits())
	populateItems(t, space, 7)

	request := browseRequest(browseAll(space.SourceFolderID()))
	request.RequestedMaxReferencesPerNode = 2
	response, err := service.Browse(context.Background(), testSession, request, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results[0].References) != 2 {
		t.Fatalf("browse returned %d references, want the requested 2",
			len(response.Results[0].References))
	}

	// Every continuation returns the same two, not the server's own far
	// larger MaxReferencesPerNode.
	point := response.Results[0].ContinuationPoint
	total := len(response.Results[0].References)
	for step := 0; point != nil; step++ {
		if step > 10 {
			t.Fatal("the continuation never ended")
		}
		next, err := service.BrowseNext(testSession, BrowseNextRequest{
			Header:             RequestHeader{AdditionalHeader: NullExtensionObject()},
			ContinuationPoints: [][]byte{point},
		}, channelEpoch)
		if err != nil {
			t.Fatal(err)
		}
		result := next.Results[0]
		if result.StatusCode != StatusGood {
			t.Fatalf("continuation = %s", result.StatusCode.Hex())
		}
		if len(result.References) > 2 {
			t.Fatalf("continuation returned %d references, more than the requested 2",
				len(result.References))
		}
		total += len(result.References)
		point = result.ContinuationPoint
	}
	if total != 7 {
		t.Fatalf("the continuation delivered %d references, want all 7", total)
	}
}

// 5.9.3.1: "the BrowseNext shall be submitted on the same Session that was used
// to submit the Browse ... that is being continued."
func TestAContinuationPointBelongsToOneSession(t *testing.T) {
	limits := DefaultBrowseLimits()
	limits.MaxReferencesPerNode = 1
	service, space := testBrowseService(t, limits)
	populateItems(t, space, 3)

	response, err := service.Browse(context.Background(), testSession,
		browseRequest(browseAll(space.SourceFolderID())), channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	point := response.Results[0].ContinuationPoint

	next := BrowseNextRequest{
		Header:             RequestHeader{AdditionalHeader: NullExtensionObject()},
		ContinuationPoints: [][]byte{point},
	}
	stranger, err := service.BrowseNext("another-session", next, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if stranger.Results[0].StatusCode != StatusBadContinuationPointInvalid {
		t.Fatalf("another session continued it: %s", stranger.Results[0].StatusCode.Hex())
	}

	// The refusal did not consume it: its owner can still continue.
	owner, err := service.BrowseNext(testSession, next, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if owner.Results[0].StatusCode != StatusGood {
		t.Fatalf("the owning session = %s", owner.Results[0].StatusCode.Hex())
	}

	// A release from the wrong session leaves it alone too.
	release := BrowseNextRequest{
		Header:                    RequestHeader{AdditionalHeader: NullExtensionObject()},
		ReleaseContinuationPoints: true,
		ContinuationPoints:        [][]byte{owner.Results[0].ContinuationPoint},
	}
	if _, err := service.BrowseNext("another-session", release, channelEpoch); err != nil {
		t.Fatal(err)
	}
	if service.ContinuationPointCount() != 1 {
		t.Fatal("another session released a point it did not own")
	}
}

// 7.9: points "remain active until ... the Session is closed".
func TestClosingASessionReleasesItsContinuationPoints(t *testing.T) {
	limits := DefaultBrowseLimits()
	limits.MaxReferencesPerNode = 1
	service, space := testBrowseService(t, limits)
	populateItems(t, space, 3)

	for _, session := range []string{testSession, "second"} {
		if _, err := service.Browse(context.Background(), session,
			browseRequest(browseAll(space.SourceFolderID())), channelEpoch); err != nil {
			t.Fatal(err)
		}
	}
	if service.ContinuationPointCount() != 2 {
		t.Fatalf("points held = %d", service.ContinuationPointCount())
	}
	if released := service.ReleaseSession(testSession); released != 1 {
		t.Fatalf("released %d points, want the one that session held", released)
	}
	// The other session's point is untouched.
	if service.ContinuationPointCount() != 1 {
		t.Fatalf("points held = %d, want the other session's", service.ContinuationPointCount())
	}
}

// 7.9: "a Server shall automatically free ContinuationPoints from prior
// requests from a Session if they are needed to process a new request from this
// Session." A session at its limit loses its own oldest point rather than being
// refused -- the newest request is the one the client is waiting on.
func TestANewRequestFreesTheSessionsOldestPoint(t *testing.T) {
	limits := DefaultBrowseLimits()
	limits.MaxReferencesPerNode = 1
	limits.MaxContinuationPoints = 2
	service, space := testBrowseService(t, limits)
	populateItems(t, space, 4)

	browse := func(at time.Time) []byte {
		t.Helper()
		response, err := service.Browse(context.Background(), testSession,
			browseRequest(browseAll(space.SourceFolderID())), at)
		if err != nil {
			t.Fatal(err)
		}
		if response.Results[0].StatusCode != StatusGood {
			t.Fatalf("browse = %s", response.Results[0].StatusCode.Hex())
		}
		return response.Results[0].ContinuationPoint
	}

	first := browse(channelEpoch)
	second := browse(channelEpoch.Add(time.Second))
	// The third request is over the limit, so the first point is freed for it.
	third := browse(channelEpoch.Add(2 * time.Second))
	if service.ContinuationPointCount() != 2 {
		t.Fatalf("points held = %d, want the session's limit", service.ContinuationPointCount())
	}

	continued := func(point []byte) StatusCode {
		t.Helper()
		response, err := service.BrowseNext(testSession, BrowseNextRequest{
			Header:             RequestHeader{AdditionalHeader: NullExtensionObject()},
			ContinuationPoints: [][]byte{point},
		}, channelEpoch.Add(3*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		return response.Results[0].StatusCode
	}
	// "The Server returns a Bad_ContinuationPointInvalid error if a Client
	// tries to use a ContinuationPoint that has been released."
	if status := continued(first); status != StatusBadContinuationPointInvalid {
		t.Fatalf("the freed point = %s", status.Hex())
	}
	if status := continued(second); status != StatusGood {
		t.Fatalf("the second point = %s", status.Hex())
	}
	if status := continued(third); status != StatusGood {
		t.Fatalf("the third point = %s", status.Hex())
	}
}

// 7.9: "a Server shall process the operations until it uses the maximum number
// of continuation points in this response. Once that happens the Server shall
// return a Bad_NoContinuationPoints error for any remaining operations." A
// point handed out earlier in the same response is not spare capacity --
// freeing it would revoke a point in the very response that carries it.
func TestOneResponseCannotSpendMoreThanItsPoints(t *testing.T) {
	limits := DefaultBrowseLimits()
	limits.MaxReferencesPerNode = 1
	limits.MaxContinuationPoints = 2
	service, space := testBrowseService(t, limits)
	populateItems(t, space, 3)

	// Four nodes to browse, each needing a point, but only two are available.
	folder := space.SourceFolderID()
	response, err := service.Browse(context.Background(), testSession,
		browseRequest(browseAll(folder), browseAll(folder), browseAll(folder), browseAll(folder)),
		channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 4 {
		t.Fatalf("results = %d", len(response.Results))
	}
	for index, result := range response.Results[:2] {
		if result.StatusCode != StatusGood || result.ContinuationPoint == nil {
			t.Fatalf("operation %d = %s with point %v", index, result.StatusCode.Hex(),
				result.ContinuationPoint)
		}
	}
	for index, result := range response.Results[2:] {
		if result.StatusCode != StatusBadNoContinuationPoints {
			t.Fatalf("operation %d = %s, want Bad_NoContinuationPoints", index+2,
				result.StatusCode.Hex())
		}
	}
	// Both points the response did hand out are still usable.
	for index, result := range response.Results[:2] {
		next, err := service.BrowseNext(testSession, BrowseNextRequest{
			Header:             RequestHeader{AdditionalHeader: NullExtensionObject()},
			ContinuationPoints: [][]byte{result.ContinuationPoint},
		}, channelEpoch)
		if err != nil {
			t.Fatal(err)
		}
		if next.Results[0].StatusCode != StatusGood {
			t.Fatalf("point %d was revoked by its own response: %s", index,
				next.Results[0].StatusCode.Hex())
		}
	}
}

// The limit is per session -- 7.9: "Servers specify a maximum number of
// ContinuationPoints per Session" -- so one session filling its own allowance
// neither blocks another nor gets spent by it.
func TestOneSessionsPointsAreNotAnothersToSpend(t *testing.T) {
	limits := DefaultBrowseLimits()
	limits.MaxReferencesPerNode = 1
	limits.MaxContinuationPoints = 2
	service, space := testBrowseService(t, limits)
	populateItems(t, space, 4)

	browse := func(session string, at time.Time) []byte {
		t.Helper()
		response, err := service.Browse(context.Background(), session,
			browseRequest(browseAll(space.SourceFolderID())), at)
		if err != nil {
			t.Fatal(err)
		}
		if response.Results[0].StatusCode != StatusGood {
			t.Fatalf("%s browse = %s", session, response.Results[0].StatusCode.Hex())
		}
		return response.Results[0].ContinuationPoint
	}

	// One session fills its whole allowance.
	first := browse(testSession, channelEpoch)
	second := browse(testSession, channelEpoch.Add(time.Second))

	// Another session gets its own allowance on top, and takes nothing away.
	browse("second", channelEpoch.Add(2*time.Second))
	browse("second", channelEpoch.Add(3*time.Second))
	if service.ContinuationPointCount() != 4 {
		t.Fatalf("points held = %d, want an allowance each", service.ContinuationPointCount())
	}
	for index, point := range [][]byte{first, second} {
		response, err := service.BrowseNext(testSession, BrowseNextRequest{
			Header:             RequestHeader{AdditionalHeader: NullExtensionObject()},
			ContinuationPoints: [][]byte{point},
		}, channelEpoch.Add(4*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if response.Results[0].StatusCode != StatusGood {
			t.Fatalf("point %d was spent by the other session: %s", index,
				response.Results[0].StatusCode.Hex())
		}
	}
}
