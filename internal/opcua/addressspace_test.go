package opcua

import (
	"testing"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

func testAddressSpace(t *testing.T) *AddressSpace {
	t.Helper()
	space, err := NewAddressSpace(AddressSpaceConfig{
		NamespaceURI:     "urn:example:opcda-access-adapter",
		SourceFolderName: "Source",
	})
	if err != nil {
		t.Fatalf("NewAddressSpace: %v", err)
	}
	return space
}

func itemID(value string) *opcda.DAItemID {
	id := opcda.DAItemID(value)
	return &id
}

func varType(value opcda.DAVarType) *opcda.DAVarType { return &value }

// NodeClass is a bit mask in OPC 10000-6, which is why Browse filters it with a
// mask rather than an equality test.
func TestNodeClassValuesAreABitMask(t *testing.T) {
	cases := map[NodeClass]uint32{
		NodeClassUnspecified: 0, NodeClassObject: 1, NodeClassVariable: 2,
		NodeClassMethod: 4, NodeClassObjectType: 8, NodeClassVariableType: 16,
		NodeClassReferenceType: 32, NodeClassDataType: 64, NodeClassView: 128,
	}
	for class, want := range cases {
		if uint32(class) != want {
			t.Fatalf("NodeClass %d, want %d", uint32(class), want)
		}
	}
	// A mask selecting objects and variables matches both and nothing else.
	mask := uint32(NodeClassObject | NodeClassVariable)
	if mask&uint32(NodeClassMethod) != 0 {
		t.Fatal("an object/variable mask matched a method")
	}
}

// The identifiers come from the OPC Foundation NodeIds and AttributeIds tables.
func TestStandardIdentifiers(t *testing.T) {
	nodes := map[uint32]uint32{
		NodeIDOrganizes: 35, NodeIDHasTypeDefinition: 40, NodeIDHasProperty: 46,
		NodeIDHasComponent: 47, NodeIDBaseObjectType: 58, NodeIDFolderType: 61,
		NodeIDBaseDataVariableType: 63, NodeIDRootFolder: 84,
		NodeIDObjectsFolder: 85, NodeIDTypesFolder: 86,
	}
	for got, want := range nodes {
		if got != want {
			t.Fatalf("node id %d, want %d", got, want)
		}
	}
	attributes := map[uint32]uint32{
		AttributeNodeID: 1, AttributeNodeClass: 2, AttributeBrowseName: 3,
		AttributeDisplayName: 4, AttributeValue: 13, AttributeDataType: 14,
		AttributeValueRank: 15, AttributeAccessLevel: 17,
	}
	for got, want := range attributes {
		if got != want {
			t.Fatalf("attribute id %d, want %d", got, want)
		}
	}
	// AccessLevel bits from OPC 10000-3 AccessLevelType.
	if AccessLevelCurrentRead != 1 || AccessLevelCurrentWrite != 2 {
		t.Fatalf("access level bits = %d/%d", AccessLevelCurrentRead, AccessLevelCurrentWrite)
	}
}

func TestStandardNodesAreReachableFromRoot(t *testing.T) {
	space := testAddressSpace(t)
	root, ok := space.Node(NumericNodeID(0, NodeIDRootFolder))
	if !ok {
		t.Fatal("the root folder is missing")
	}
	if root.TypeDefinition.Numeric != NodeIDFolderType {
		t.Fatalf("root type definition = %s", root.TypeDefinition)
	}

	forward := map[string]bool{}
	for _, reference := range root.References {
		if reference.IsForward {
			forward[reference.TargetID.NodeID.String()] = true
		}
	}
	for _, expected := range []NodeID{
		NumericNodeID(0, NodeIDObjectsFolder), NumericNodeID(0, NodeIDTypesFolder),
	} {
		if !forward[expected.String()] {
			t.Fatalf("root does not reference %s", expected)
		}
	}

	// The source folder hangs under Objects and links back to it.
	objects, _ := space.Node(NumericNodeID(0, NodeIDObjectsFolder))
	found := false
	for _, reference := range objects.References {
		if reference.IsForward && reference.TargetID.NodeID.Equal(space.SourceFolderID()) {
			found = true
		}
	}
	if !found {
		t.Fatal("the source folder is not under Objects")
	}
	source, _ := space.Node(space.SourceFolderID())
	inverse := 0
	for _, reference := range source.References {
		if !reference.IsForward {
			inverse++
		}
	}
	if inverse == 0 {
		t.Fatal("the source folder has no inverse reference to walk back up")
	}
}

// The namespace table names the adapter's namespace by URI. Design §35.2
// forbids treating an index as persistent identity, so the URI is the durable
// name.
func TestNamespaceTable(t *testing.T) {
	space := testAddressSpace(t)
	uris := space.NamespaceURIs()
	if len(uris) != 2 {
		t.Fatalf("namespace table = %v", uris)
	}
	if uris[0] != "http://opcfoundation.org/UA/" {
		t.Fatalf("index 0 = %q, want the OPC UA namespace", uris[0])
	}
	if uris[AdapterNamespaceIndex] != "urn:example:opcda-access-adapter" {
		t.Fatalf("index %d = %q", AdapterNamespaceIndex, uris[AdapterNamespaceIndex])
	}
}

// The exact DA ItemID is the node identity: no trimming, no case conversion,
// and no delimiter normalisation.
func TestItemNodesCarryTheExactItemID(t *testing.T) {
	space := testAddressSpace(t)
	awkward := []string{
		"Test/Float",
		"Random.Int2",
		"Weird  Spaced/Item",
		"MiXeD.CaSe",
		"trailing ",
		"tab\tinside",
	}
	entries := make([]opcda.BrowseEntry, 0, len(awkward))
	for _, value := range awkward {
		entries = append(entries, opcda.BrowseEntry{
			Kind: opcda.BrowseEntryItem, Name: value, ItemID: itemID(value),
		})
	}
	if err := space.PopulateBranch(nil, entries); err != nil {
		t.Fatal(err)
	}
	for _, value := range awkward {
		node, ok := space.Node(ItemNodeID(opcda.DAItemID(value)))
		if !ok {
			t.Fatalf("no node for %q", value)
		}
		if string(node.ItemID) != value {
			t.Fatalf("ItemID = %q, want %q", node.ItemID, value)
		}
		if node.BrowseName.Name != value {
			t.Fatalf("browse name = %q, want the source name unchanged", node.BrowseName.Name)
		}
		resolved, ok := space.VariableItemID(node.ID)
		if !ok || string(resolved) != value {
			t.Fatalf("resolved ItemID = %q", resolved)
		}
	}
}

// A branch has no ItemID, so its identity is the navigation path and it can
// never be mistaken for an item.
func TestBranchNodesHaveNoItemID(t *testing.T) {
	space := testAddressSpace(t)
	err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryBranch, Name: "Test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	branch, ok := space.Node(BranchNodeID([]string{"Test"}, nil))
	if !ok {
		t.Fatal("the branch node is missing")
	}
	if branch.Class != NodeClassObject || branch.ItemID != "" {
		t.Fatalf("branch = %+v", branch)
	}
	if _, ok := space.VariableItemID(branch.ID); ok {
		t.Fatal("a branch resolved to an ItemID")
	}

	// A branch identifier and an item identifier with the same text differ.
	if BranchNodeID([]string{"Test"}, nil).Equal(ItemNodeID("Test")) {
		t.Fatal("a branch and an item share a node identifier")
	}
}

// A nested branch keeps the full path, so two branches with the same name under
// different parents are distinct nodes.
func TestNestedBranchesAreDistinct(t *testing.T) {
	space := testAddressSpace(t)
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryBranch, Name: "A"},
		{Kind: opcda.BrowseEntryBranch, Name: "B"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, parent := range []string{"A", "B"} {
		if err := space.PopulateBranch([]string{parent}, []opcda.BrowseEntry{
			{Kind: opcda.BrowseEntryBranch, Name: "Shared"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	first := BranchNodeID([]string{"A", "Shared"}, nil)
	second := BranchNodeID([]string{"B", "Shared"}, nil)
	if first.Equal(second) {
		t.Fatal("same-named branches under different parents collided")
	}
	if _, ok := space.Node(first); !ok {
		t.Fatal("the nested branch under A is missing")
	}
	if _, ok := space.Node(second); !ok {
		t.Fatal("the nested branch under B is missing")
	}
}

func TestItemNodesCarryDataTypeAndAccessLevel(t *testing.T) {
	space := testAddressSpace(t)
	rights := func(read, write bool) *opcda.DAAccessRights {
		var raw uint32
		if read {
			raw |= 1
		}
		if write {
			raw |= 2
		}
		return &opcda.DAAccessRights{Raw: raw, Read: read, Write: write}
	}
	entries := []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "i4", ItemID: itemID("i4"),
			CanonicalType: varType(opcda.VTI4), AccessRights: rights(true, true)},
		{Kind: opcda.BrowseEntryItem, Name: "r4", ItemID: itemID("r4"),
			CanonicalType: varType(opcda.VTR4), AccessRights: rights(true, false)},
		{Kind: opcda.BrowseEntryItem, Name: "bstr", ItemID: itemID("bstr"),
			CanonicalType: varType(opcda.VTBSTR), AccessRights: rights(true, false)},
		// A VARTYPE with no Table A.2 row falls back to the abstract base type
		// rather than borrowing a similar one.
		{Kind: opcda.BrowseEntryItem, Name: "cy", ItemID: itemID("cy"),
			CanonicalType: varType(opcda.VTCY), AccessRights: rights(true, false)},
		// OPC DA carries access rights in AddItems, not in Browse, so a browsed
		// item normally arrives without them.
		{Kind: opcda.BrowseEntryItem, Name: "unknown", ItemID: itemID("unknown")},
	}
	if err := space.PopulateBranch(nil, entries); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		item        string
		dataType    uint32
		accessLevel byte
	}{
		{"i4", NodeIDInt32, AccessLevelCurrentRead | AccessLevelCurrentWrite},
		{"r4", NodeIDFloat, AccessLevelCurrentRead},
		{"bstr", NodeIDString, AccessLevelCurrentRead},
		{"cy", NodeIDBaseDataType, AccessLevelCurrentRead},
		{"unknown", NodeIDBaseDataType, AccessLevelCurrentRead | AccessLevelCurrentWrite},
	}
	for _, testCase := range cases {
		t.Run(testCase.item, func(t *testing.T) {
			node, ok := space.Node(ItemNodeID(opcda.DAItemID(testCase.item)))
			if !ok {
				t.Fatal("node missing")
			}
			if node.DataType.Numeric != testCase.dataType {
				t.Fatalf("data type = %s, want id %d", node.DataType, testCase.dataType)
			}
			if node.AccessLevel != testCase.accessLevel {
				t.Fatalf("access level = %d, want %d", node.AccessLevel, testCase.accessLevel)
			}
			// Only rights the source actually reported are marked known, which
			// is what decides whether the adapter enforces them itself.
			if node.AccessRightsKnown != (testCase.item != "unknown") {
				t.Fatalf("access rights known = %t for %q", node.AccessRightsKnown, testCase.item)
			}
			// The DA core decodes no arrays, so every variable is a scalar.
			if node.ValueRank != ValueRankScalar {
				t.Fatalf("value rank = %d", node.ValueRank)
			}
			// A browsed item whose properties nobody has asked for yet sits at
			// the Annex A.3.1.3 floor. BaseDataVariableType is a type Annex A
			// does not offer for a DA item at all.
			if node.TypeDefinition.Numeric != NodeIDDataItemType {
				t.Fatalf("type definition = %s", node.TypeDefinition)
			}
		})
	}
}

// A re-browse reflects the source rather than accumulating stale nodes.
func TestPopulateReplacesForwardReferences(t *testing.T) {
	space := testAddressSpace(t)
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "gone", ItemID: itemID("gone")},
		{Kind: opcda.BrowseEntryItem, Name: "stays", ItemID: itemID("stays")},
	}); err != nil {
		t.Fatal(err)
	}
	if err := space.PopulateBranch(nil, []opcda.BrowseEntry{
		{Kind: opcda.BrowseEntryItem, Name: "stays", ItemID: itemID("stays")},
	}); err != nil {
		t.Fatal(err)
	}

	source, _ := space.Node(space.SourceFolderID())
	forward := 0
	for _, reference := range source.References {
		if reference.IsForward {
			forward++
			if reference.BrowseName.Name == "gone" {
				t.Fatal("a removed item is still referenced")
			}
		}
	}
	if forward != 1 {
		t.Fatalf("forward references = %d, want 1", forward)
	}
	// The inverse reference back to Objects survives the replacement.
	inverse := 0
	for _, reference := range source.References {
		if !reference.IsForward {
			inverse++
		}
	}
	if inverse == 0 {
		t.Fatal("the inverse reference was lost")
	}
}

func TestPopulateRejectsMalformedEntries(t *testing.T) {
	space := testAddressSpace(t)
	cases := []struct {
		name  string
		entry opcda.BrowseEntry
	}{
		{"item without an ItemID", opcda.BrowseEntry{Kind: opcda.BrowseEntryItem, Name: "x"}},
		{"entry without a name", opcda.BrowseEntry{Kind: opcda.BrowseEntryBranch}},
		{"unknown kind", opcda.BrowseEntry{Kind: "other", Name: "x"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if err := space.PopulateBranch(nil, []opcda.BrowseEntry{testCase.entry}); err == nil {
				t.Fatal("a malformed entry was accepted")
			}
		})
	}
	// A path with no node is refused rather than silently creating one.
	if err := space.PopulateBranch([]string{"missing"}, nil); err == nil {
		t.Fatal("populating an unknown path succeeded")
	}
}

func TestDataTypeNodeIDResolution(t *testing.T) {
	cases := map[DataType]uint32{
		DataTypeBoolean: NodeIDBoolean, DataTypeSByte: NodeIDSByte,
		DataTypeByte: NodeIDByte, DataTypeInt16: NodeIDInt16,
		DataTypeUInt16: NodeIDUInt16, DataTypeInt32: NodeIDInt32,
		DataTypeUInt32: NodeIDUInt32, DataTypeInt64: NodeIDInt64,
		DataTypeUInt64: NodeIDUInt64, DataTypeFloat: NodeIDFloat,
		DataTypeDouble: NodeIDDouble, DataTypeString: NodeIDString,
		DataTypeDecimal: NodeIDDecimal, DataTypeNull: NodeIDBaseDataType,
	}
	for dataType, want := range cases {
		id, ok := DataTypeNodeID(dataType)
		if !ok {
			t.Fatalf("%s did not resolve", dataType)
		}
		if id.Namespace != 0 || id.Numeric != want {
			t.Fatalf("%s = %s, want ns=0;i=%d", dataType, id, want)
		}
	}
	if _, ok := DataTypeNodeID(DataType("NotAType")); ok {
		t.Fatal("an unknown data type resolved")
	}
}

func TestAddressSpaceConfigValidation(t *testing.T) {
	valid := AddressSpaceConfig{NamespaceURI: "urn:x", SourceFolderName: "Source"}
	if err := valid.ValidateForConfiguration(); err != nil {
		t.Fatal(err)
	}
	for name, config := range map[string]AddressSpaceConfig{
		"no namespace uri": {SourceFolderName: "Source"},
		"no folder name":   {NamespaceURI: "urn:x"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := config.ValidateForConfiguration(); err == nil {
				t.Fatal("an invalid config was accepted")
			}
			if _, err := NewAddressSpace(config); err == nil {
				t.Fatal("an address space was built from an invalid config")
			}
		})
	}
}

// A node identifier carries the exact DA ItemID, so it is self-describing.
func TestItemIDForNode(t *testing.T) {
	for _, value := range []string{"Test/Float", "MiXeD.CaSe", "with space", "tab\there"} {
		recovered, ok := ItemIDForNode(ItemNodeID(opcda.DAItemID(value)))
		if !ok {
			t.Fatalf("%q was not recognised as an item", value)
		}
		if string(recovered) != value {
			t.Fatalf("recovered %q, want %q", recovered, value)
		}
	}
	// A branch, a standard node, and a bare identifier name no item.
	for _, id := range []NodeID{
		BranchNodeID([]string{"Test"}, nil),
		NumericNodeID(0, NodeIDObjectsFolder),
		StringNodeID(AdapterNamespaceIndex, "not-an-item"),
		StringNodeID(AdapterNamespaceIndex, "item:"),
		StringNodeID(3, "item:Test/Float"),
	} {
		if _, ok := ItemIDForNode(id); ok {
			t.Fatalf("%s was read as an item", id)
		}
	}
}

// Resolving an item that was never browsed creates a node that knows neither
// its type nor its rights, so the source decides both.
func TestResolveVariableCreatesAnUnbrowsedNode(t *testing.T) {
	space := testAddressSpace(t)
	node, ok := space.ResolveVariable(ItemNodeID("Vendor/Tag"), 1000)
	if !ok {
		t.Fatal("an item identifier did not resolve")
	}
	if node.Class != NodeClassVariable || string(node.ItemID) != "Vendor/Tag" {
		t.Fatalf("node = %+v", node)
	}
	if node.DataTypeKnown || node.AccessRightsKnown {
		t.Fatal("an unbrowsed node claimed to know its type or rights")
	}
	// It is not linked into the hierarchy, because it was never browsed.
	source, _ := space.Node(space.SourceFolderID())
	for _, reference := range source.References {
		if reference.IsForward && reference.TargetID.NodeID.Equal(node.ID) {
			t.Fatal("an unbrowsed node appeared as a child of the source folder")
		}
	}
	// Resolving again returns the same node rather than a second one.
	before := space.NodeCount()
	if _, ok := space.ResolveVariable(ItemNodeID("Vendor/Tag"), 1000); !ok {
		t.Fatal("the second resolve failed")
	}
	if space.NodeCount() != before {
		t.Fatalf("resolving twice created %d nodes", space.NodeCount()-before)
	}
	// A branch identifier is not a variable.
	if _, ok := space.ResolveVariable(BranchNodeID([]string{"Test"}, nil), 1000); ok {
		t.Fatal("a branch identifier resolved as a variable")
	}
}

// A.3.1.2: "The ItemId obtained using the GetItemID is used as a part of the
// NodeId for each Branch." A.3.1.5 sanctions a NodeId that carries both that
// ItemID and other information, and names the trade-off: the NodeId will not
// be the ItemId. The path has to survive because Browse is path-based.
func TestABranchNodeIDCarriesTheItemIDTheSourceGaveIt(t *testing.T) {
	named := opcda.DAItemID("Plant.Line1")
	withID := BranchNodeID([]string{"Plant", "Line1"}, &named)
	withoutID := BranchNodeID([]string{"Plant", "Line1"}, nil)

	if withID.Equal(withoutID) {
		t.Fatal("the source's ItemID is not part of the identifier")
	}
	recovered, ok := BranchItemIDForNode(withID)
	if !ok || recovered != named {
		t.Fatalf("recovered %q, %v", recovered, ok)
	}
	// A source that will not name a branch leaves the path alone.
	if _, ok := BranchItemIDForNode(withoutID); ok {
		t.Fatal("an unnamed branch reported an ItemID")
	}

	// The path still navigates, which is what Browse needs, whether or not the
	// source named the branch.
	for _, id := range []NodeID{withID, withoutID} {
		path, ok := PathForNode(id)
		if !ok {
			t.Fatalf("%s has no path", id)
		}
		if len(path) != 2 || path[0] != "Plant" || path[1] != "Line1" {
			t.Fatalf("path = %v", path)
		}
	}
	// An item identifier is still never mistaken for a branch.
	if _, ok := BranchItemIDForNode(ItemNodeID("Plant.Line1")); ok {
		t.Fatal("an item node reported a branch ItemID")
	}
}
