package opcua

import (
	"fmt"
	"sync"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// The address space maps the DA source onto UA nodes. Design §35.2 fixes the
// identity rules and this implementation follows them exactly: the exact DA
// ItemID is the node identity, names come from what DA Browse returned, and no
// ItemID is ever reconstructed by guessing a server's delimiter.

// NodeClass values from OPC 10000-6. They are a bit mask, which is why Browse
// filters them with a mask rather than an equality test.
type NodeClass uint32

const (
	NodeClassUnspecified   NodeClass = 0
	NodeClassObject        NodeClass = 1
	NodeClassVariable      NodeClass = 2
	NodeClassMethod        NodeClass = 4
	NodeClassObjectType    NodeClass = 8
	NodeClassVariableType  NodeClass = 16
	NodeClassReferenceType NodeClass = 32
	NodeClassDataType      NodeClass = 64
	NodeClassView          NodeClass = 128
)

// Attribute identifiers from the OPC Foundation AttributeIds table.
const (
	AttributeNodeID          uint32 = 1
	AttributeNodeClass       uint32 = 2
	AttributeBrowseName      uint32 = 3
	AttributeDisplayName     uint32 = 4
	AttributeDescription     uint32 = 5
	AttributeValue           uint32 = 13
	AttributeDataType        uint32 = 14
	AttributeValueRank       uint32 = 15
	AttributeArrayDimensions uint32 = 16
	AttributeAccessLevel     uint32 = 17
	AttributeUserAccessLevel uint32 = 18
	AttributeHistorizing     uint32 = 20
)

// AccessLevel bits from OPC 10000-3 AccessLevelType.
const (
	AccessLevelCurrentRead  byte = 1 << 0
	AccessLevelCurrentWrite byte = 1 << 1
)

// ValueRank values. A scalar is -1; the DA core decodes no arrays.
const ValueRankScalar int32 = -1

// Standard node identifiers from the OPC Foundation NodeIds table.
const (
	NodeIDReferences           uint32 = 31
	NodeIDNonHierarchicalRefs  uint32 = 32
	NodeIDHierarchicalRefs     uint32 = 33
	NodeIDHasChild             uint32 = 34
	NodeIDOrganizes            uint32 = 35
	NodeIDAggregates           uint32 = 44
	NodeIDHasTypeDefinition    uint32 = 40
	NodeIDHasProperty          uint32 = 46
	NodeIDHasComponent         uint32 = 47
	NodeIDBaseObjectType       uint32 = 58
	NodeIDFolderType           uint32 = 61
	NodeIDBaseDataVariableType uint32 = 63
	NodeIDRootFolder           uint32 = 84
	NodeIDObjectsFolder        uint32 = 85
	NodeIDTypesFolder          uint32 = 86
	NodeIDPropertyType         uint32 = 68
	NodeIDServerType           uint32 = 2004
	NodeIDServer               uint32 = 2253
	NodeIDServerArray          uint32 = 2254
	NodeIDNamespaceArray       uint32 = 2255
)

// ValueRankOneDimension is the ValueRank of the standard Server properties.
// Nothing the DA source supplies is an array.
const ValueRankOneDimension int32 = 1

// Built-in DataType identifiers from the same table.
const (
	NodeIDBoolean      uint32 = 1
	NodeIDSByte        uint32 = 2
	NodeIDByte         uint32 = 3
	NodeIDInt16        uint32 = 4
	NodeIDUInt16       uint32 = 5
	NodeIDInt32        uint32 = 6
	NodeIDUInt32       uint32 = 7
	NodeIDInt64        uint32 = 8
	NodeIDUInt64       uint32 = 9
	NodeIDFloat        uint32 = 10
	NodeIDDouble       uint32 = 11
	NodeIDString       uint32 = 12
	NodeIDDateTime     uint32 = 13
	NodeIDBaseDataType uint32 = 24
	NodeIDDecimal      uint32 = 50
)

// dataTypeNodeIDs maps the UA DataType names this adapter produces onto their
// standard identifiers.
var dataTypeNodeIDs = map[DataType]uint32{
	DataTypeBoolean: NodeIDBoolean,
	DataTypeSByte:   NodeIDSByte,
	DataTypeByte:    NodeIDByte,
	DataTypeInt16:   NodeIDInt16,
	DataTypeUInt16:  NodeIDUInt16,
	DataTypeInt32:   NodeIDInt32,
	DataTypeUInt32:  NodeIDUInt32,
	DataTypeInt64:   NodeIDInt64,
	DataTypeUInt64:  NodeIDUInt64,
	DataTypeFloat:   NodeIDFloat,
	DataTypeDouble:  NodeIDDouble,
	DataTypeString:  NodeIDString,
	DataTypeDecimal: NodeIDDecimal,
	// A value with no type carries the abstract base type rather than a
	// concrete one it does not have.
	DataTypeNull: NodeIDBaseDataType,
}

// DataTypeNodeID resolves a UA DataType to its standard NodeId.
func DataTypeNodeID(dataType DataType) (NodeID, bool) {
	identifier, ok := dataTypeNodeIDs[dataType]
	if !ok {
		return NodeID{}, false
	}
	return NumericNodeID(0, identifier), true
}

// Reference is one reference from a node.
type Reference struct {
	ReferenceTypeID NodeID
	IsForward       bool
	TargetID        ExpandedNodeID
	BrowseName      QualifiedName
	DisplayName     LocalizedText
	NodeClass       NodeClass
	TypeDefinition  ExpandedNodeID
}

// Node is one node in the address space.
type Node struct {
	ID             NodeID
	Class          NodeClass
	BrowseName     QualifiedName
	DisplayName    LocalizedText
	TypeDefinition NodeID

	// DataType and the access level apply to variables only.
	DataType  NodeID
	ValueRank int32
	// AccessLevel is what the adapter reports. AccessRightsKnown records
	// whether the source actually told us: OPC DA carries access rights in
	// AddItems, not in Browse, so a browsed item usually arrives without them.
	AccessLevel       byte
	AccessRightsKnown bool
	// DataTypeKnown records whether the source told us the canonical type. OPC
	// DA carries it in the AddItems result, not in Browse, so a browsed item
	// arrives without it and the node reports the abstract base type until a
	// Read teaches it otherwise.
	DataTypeKnown bool

	// DescriptionOffered records that the source offers Item Description for
	// this item, which OPC 10000-8 Table A.1 maps to the Description attribute.
	// It stores whether the property exists, never its text: the description is
	// read from the source each time a client asks for it.
	DescriptionOffered bool

	// ItemID is the exact DA ItemID a variable stands for. It is empty for a
	// folder, because design §35.2 forbids inventing an ItemID for a branch.
	ItemID opcda.DAItemID

	// LocalValue answers a variable the server reports about itself, such as
	// the standard NamespaceArray or ServerStatus. It is nil for every
	// variable that stands for a DA item, and the two are mutually exclusive:
	// a DA process value is never held in the address space, only passed
	// through from a source read. It is a function because some of these
	// values, CurrentTime above all, have to be answered as of the read.
	LocalValue func(now time.Time) Variant

	References []Reference
}

// IsLocalVariable reports a variable the server answers from its own address
// space rather than from the DA source.
func (n *Node) IsLocalVariable() bool {
	return n != nil && n.Class == NodeClassVariable && n.LocalValue != nil
}

// staticLocalValue holds a value that does not change between reads.
func staticLocalValue(value Variant) func(time.Time) Variant {
	return func(time.Time) Variant { return value }
}

// AddressSpaceConfig carries what the adapter cannot derive from the source.
type AddressSpaceConfig struct {
	// NamespaceURI identifies this adapter's namespace and is kept stable
	// across restarts. Design §35.2 forbids treating a namespace index as
	// persistent identity, so the URI is the durable name.
	NamespaceURI string
	// SourceFolderName is the browse name of the folder that holds the source's
	// address space.
	SourceFolderName string
	// ApplicationURI is this server's own URI, which the standard ServerArray
	// property reports. It is the endpoint's ApplicationUri; OPC 10000-5 8.3.2
	// has index 0 of the array name the local server.
	ApplicationURI string
	// The remaining fields describe the adapter itself and are reported by the
	// standard Server BuildInfo. None of them comes from the DA source.
	ProductURI       string
	ManufacturerName string
	ProductName      string
	SoftwareVersion  string
	BuildNumber      string
	BuildDate        time.Time
}

func (config AddressSpaceConfig) validate() error {
	if config.NamespaceURI == "" {
		return fmt.Errorf("a namespace URI is required and must be stable across restarts")
	}
	if config.SourceFolderName == "" {
		return fmt.Errorf("a source folder name is required")
	}
	return nil
}

func (config AddressSpaceConfig) ValidateForConfiguration() error { return config.validate() }

// AdapterNamespaceIndex is the index this adapter's namespace occupies. Index 0
// is the OPC UA namespace, so the adapter's own nodes start at 1.
const AdapterNamespaceIndex uint16 = 1

// AddressSpace holds the standard nodes plus the nodes discovered from the DA
// source. It is safe for concurrent readers because a Browse can arrive on any
// connection.
type AddressSpace struct {
	config AddressSpaceConfig

	mu    sync.RWMutex
	nodes map[string]*Node
	// sourceFolder is the node DA branches and items hang from.
	sourceFolder NodeID
	// binaryLimits bounds the encoder used for the server's own structures.
	binaryLimits BinaryLimits
	// standardNodeCount is how many of the nodes are this server's own, fixed
	// from construction, so the budget can count only what the source added.
	standardNodeCount int
	// startTime is what the standard ServerStatus reports as StartTime.
	startTime time.Time
}

// nodeKey renders a NodeId as a map key. NodeID carries a byte slice, so it is
// not directly comparable.
func nodeKey(id NodeID) string { return id.String() }

func NewAddressSpace(config AddressSpaceConfig) (*AddressSpace, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	space := &AddressSpace{
		config:       config,
		nodes:        make(map[string]*Node),
		sourceFolder: StringNodeID(AdapterNamespaceIndex, config.SourceFolderName),
		binaryLimits: DefaultBinaryLimits(),
		// The start time is when the address space was built, which is when
		// this server began answering.
		startTime: time.Now().UTC(),
	}
	space.addStandardNodes()
	return space, nil
}

// NamespaceURIs is the server's namespace table: index 0 is the OPC UA
// namespace and index 1 is this adapter's.
func (s *AddressSpace) NamespaceURIs() []string {
	return []string{"http://opcfoundation.org/UA/", s.config.NamespaceURI}
}

func (s *AddressSpace) SourceFolderID() NodeID { return s.sourceFolder }

func folderNode(id NodeID, name string) *Node {
	return &Node{
		ID:             id,
		Class:          NodeClassObject,
		BrowseName:     QualifiedName{Namespace: 0, Name: name},
		DisplayName:    LocalizedText{Text: name},
		TypeDefinition: NumericNodeID(0, NodeIDFolderType),
	}
}

func (s *AddressSpace) addStandardNodes() {
	root := folderNode(NumericNodeID(0, NodeIDRootFolder), "Root")
	objects := folderNode(NumericNodeID(0, NodeIDObjectsFolder), "Objects")
	types := folderNode(NumericNodeID(0, NodeIDTypesFolder), "Types")
	source := &Node{
		ID:             s.sourceFolder,
		Class:          NodeClassObject,
		BrowseName:     QualifiedName{Namespace: AdapterNamespaceIndex, Name: s.config.SourceFolderName},
		DisplayName:    LocalizedText{Text: s.config.SourceFolderName},
		TypeDefinition: NumericNodeID(0, NodeIDFolderType),
	}

	// OPC 10000-5 8.3.2 places a Server object in every server's address
	// space. A UA client reads its NamespaceArray before it does anything
	// else, because a namespace index means nothing on its own: only the URI
	// it stands for is stable, which is the same reason design §35.2 keeps the
	// URI rather than the index as this adapter's durable name. Without these
	// nodes a conforming client cannot get past connecting.
	server := &Node{
		ID:             NumericNodeID(0, NodeIDServer),
		Class:          NodeClassObject,
		BrowseName:     QualifiedName{Namespace: 0, Name: "Server"},
		DisplayName:    LocalizedText{Text: "Server"},
		TypeDefinition: NumericNodeID(0, NodeIDServerType),
	}
	// The ServerArray names the servers this endpoint knows about. This
	// adapter aggregates nothing, so it holds exactly one entry: itself.
	serverArray := standardProperty(NodeIDServerArray, "ServerArray", []string{s.config.ApplicationURI})
	namespaceArray := standardProperty(NodeIDNamespaceArray, "NamespaceArray", s.NamespaceURIs())

	organizes := NumericNodeID(0, NodeIDOrganizes)
	hasProperty := NumericNodeID(0, NodeIDHasProperty)
	addForward(root, organizes, objects)
	addForward(root, organizes, types)
	addForward(objects, organizes, source)
	addForward(objects, organizes, server)
	addForward(server, hasProperty, serverArray)
	addForward(server, hasProperty, namespaceArray)
	// The inverse reference lets a client walk back up the hierarchy.
	addInverse(objects, organizes, root)
	addInverse(types, organizes, root)
	addInverse(source, organizes, objects)
	addInverse(server, organizes, objects)
	addInverse(serverArray, hasProperty, server)
	addInverse(namespaceArray, hasProperty, server)

	standard := []*Node{root, objects, types, source, server, serverArray, namespaceArray}
	standard = append(standard, s.addServerStatusNodes(server)...)
	for _, node := range standard {
		s.nodes[nodeKey(node.ID)] = node
	}
	s.standardNodeCount = len(s.nodes)
}

// standardProperty builds one of the Server object's String array properties.
func standardProperty(identifier uint32, name string, values []string) *Node {
	value := Variant{Type: BuiltInString, IsArray: true, Value: values}
	return &Node{
		ID:             NumericNodeID(0, identifier),
		Class:          NodeClassVariable,
		BrowseName:     QualifiedName{Namespace: 0, Name: name},
		DisplayName:    LocalizedText{Text: name},
		TypeDefinition: NumericNodeID(0, NodeIDPropertyType),
		DataType:       NumericNodeID(0, NodeIDString),
		DataTypeKnown:  true,
		ValueRank:      ValueRankOneDimension,
		// The server knows its own properties exactly, so unlike a DA item
		// these carry a genuine access level rather than an assumed one.
		AccessLevel:       AccessLevelCurrentRead,
		AccessRightsKnown: true,
		LocalValue:        staticLocalValue(value),
	}
}

func referenceTo(referenceType NodeID, target *Node, forward bool) Reference {
	return Reference{
		ReferenceTypeID: referenceType,
		IsForward:       forward,
		TargetID:        ExpandedNodeID{NodeID: target.ID},
		BrowseName:      target.BrowseName,
		DisplayName:     target.DisplayName,
		NodeClass:       target.Class,
		TypeDefinition:  ExpandedNodeID{NodeID: target.TypeDefinition},
	}
}

func addForward(from *Node, referenceType NodeID, to *Node) {
	from.References = append(from.References, referenceTo(referenceType, to, true))
}

func addInverse(from *Node, referenceType NodeID, to *Node) {
	from.References = append(from.References, referenceTo(referenceType, to, false))
}

// Node resolves a node identifier.
func (s *AddressSpace) Node(id NodeID) (*Node, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	node, ok := s.nodes[nodeKey(id)]
	return node, ok
}

// VariableItemID resolves a NodeId to the exact DA ItemID it stands for.
func (s *AddressSpace) VariableItemID(id NodeID) (opcda.DAItemID, bool) {
	node, ok := s.Node(id)
	if !ok || node.Class != NodeClassVariable || node.ItemID == "" {
		return "", false
	}
	return node.ItemID, true
}

// BranchNodeID is the node identifier for a DA browse path.
//
// A branch has no ItemID: design §35.2 forbids reconstructing one from a browse
// path. Its identity is therefore the navigation path itself, marked so it can
// never be confused with an item's exact ItemID.
func BranchNodeID(path []string) NodeID {
	if len(path) == 0 {
		return NodeID{}
	}
	encoded := "branch:"
	for index, segment := range path {
		if index > 0 {
			encoded += "\x1f"
		}
		encoded += segment
	}
	return StringNodeID(AdapterNamespaceIndex, encoded)
}

// itemNodePrefix marks a node identifier as naming a DA item, so an item and a
// branch can never collide.
const itemNodePrefix = "item:"

// ItemNodeID is the node identifier for a DA item. The exact ItemID is carried
// unchanged, with no trimming, case conversion, or delimiter normalisation.
func ItemNodeID(itemID opcda.DAItemID) NodeID {
	return StringNodeID(AdapterNamespaceIndex, itemNodePrefix+string(itemID))
}

// ItemIDForNode recovers the exact DA ItemID a node identifier names.
//
// A node identifier is self-describing: it carries the ItemID verbatim, so an
// item can be addressed without having been browsed. That matters because a DA
// server need not implement Browse at all — the interface is optional in DA
// 2.05a — and a client of such a source knows its ItemIDs from elsewhere. The
// source remains the authority on whether the item exists and answers
// OPC_E_UNKNOWNITEMID if it does not, which Part 8 Table A.4 maps to exactly
// the Bad_NodeIdUnknown a client would expect.
func ItemIDForNode(id NodeID) (opcda.DAItemID, bool) {
	if id.Namespace != AdapterNamespaceIndex || id.Type != NodeIDTypeString {
		return "", false
	}
	if len(id.StringID) <= len(itemNodePrefix) || id.StringID[:len(itemNodePrefix)] != itemNodePrefix {
		return "", false
	}
	return opcda.DAItemID(id.StringID[len(itemNodePrefix):]), true
}

// ResolveVariable returns the node for a variable, creating an unbrowsed one
// when the identifier names a DA item the address space has not seen.
//
// The created node knows neither its canonical type nor its access rights,
// which is the same state a browsed item starts in, so the source decides both.
// It is not linked into the hierarchy: it was never browsed, so Browse must not
// report it as a child of anything.
func (s *AddressSpace) ResolveVariable(id NodeID, maxNodes int) (*Node, bool) {
	if node, ok := s.Node(id); ok {
		if node.Class != NodeClassVariable || node.ItemID == "" {
			return nil, false
		}
		return node, true
	}
	itemID, ok := ItemIDForNode(id)
	if !ok {
		return nil, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Re-check under the write lock; another caller may have added it.
	if node, exists := s.nodes[nodeKey(id)]; exists {
		if node.Class != NodeClassVariable || node.ItemID == "" {
			return nil, false
		}
		return node, true
	}
	// Addressing items directly must not let a client grow the space without
	// limit, so the same node budget applies. A non-positive budget creates
	// nothing: there is no unlimited, and a caller that passes one by mistake
	// is refused rather than quietly allowed to grow the space without bound.
	if len(s.nodes)-s.standardNodeCount >= max(maxNodes, 0) {
		return nil, false
	}
	node := &Node{
		ID:          id,
		Class:       NodeClassVariable,
		BrowseName:  QualifiedName{Namespace: AdapterNamespaceIndex, Name: string(itemID)},
		DisplayName: LocalizedText{Text: string(itemID)},
		// DataItemType is the floor Annex A.3.1.3 sets. An item whose properties
		// nobody has asked for yet is not known to be analog or discrete, but it
		// is known to be a DA item, and BaseDataVariableType is a type Annex A
		// does not offer.
		TypeDefinition: NumericNodeID(0, NodeIDDataItemType),
		ValueRank:      ValueRankScalar,
		ItemID:         itemID,
		DataType:       NumericNodeID(0, NodeIDBaseDataType),
	}
	node.AccessLevel, node.AccessRightsKnown = accessLevelFor(nil)
	s.nodes[nodeKey(node.ID)] = node
	return node, true
}

// PopulateBranch adds the entries of one DA Browse result under the node the
// path names. It replaces whatever that node previously referenced, so a
// re-browse reflects the source rather than accumulating stale nodes.
func (s *AddressSpace) PopulateBranch(path []string, entries []opcda.BrowseEntry) error {
	parentID := s.sourceFolder
	if len(path) > 0 {
		parentID = BranchNodeID(path)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	parent, ok := s.nodes[nodeKey(parentID)]
	if !ok {
		return fmt.Errorf("browse path %v has no node", path)
	}

	// Keep the inverse references that lead back up; replace the forward ones.
	retained := make([]Reference, 0, len(parent.References))
	for _, reference := range parent.References {
		if !reference.IsForward {
			retained = append(retained, reference)
		}
	}
	parent.References = retained

	organizes := NumericNodeID(0, NodeIDOrganizes)
	for _, entry := range entries {
		child, err := s.nodeForEntry(path, entry)
		if err != nil {
			return err
		}
		s.nodes[nodeKey(child.ID)] = child
		addForward(parent, organizes, child)
		addInverse(child, organizes, parent)
	}
	return nil
}

func (s *AddressSpace) nodeForEntry(path []string, entry opcda.BrowseEntry) (*Node, error) {
	if entry.Name == "" {
		return nil, fmt.Errorf("a browse entry carried no name")
	}
	// BrowseName and DisplayName come from what DA Browse returned. Design
	// §35.2 forbids tidying them: no case changes and no delimiter rewriting.
	name := QualifiedName{Namespace: AdapterNamespaceIndex, Name: entry.Name}
	display := LocalizedText{Text: entry.Name}

	switch entry.Kind {
	case opcda.BrowseEntryBranch:
		childPath := append(append([]string(nil), path...), entry.Name)
		return &Node{
			ID:             BranchNodeID(childPath),
			Class:          NodeClassObject,
			BrowseName:     name,
			DisplayName:    display,
			TypeDefinition: NumericNodeID(0, NodeIDFolderType),
		}, nil
	case opcda.BrowseEntryItem:
		if entry.ItemID == nil {
			return nil, fmt.Errorf("a browse item carried no ItemID")
		}
		node := &Node{
			ID:          ItemNodeID(*entry.ItemID),
			Class:       NodeClassVariable,
			BrowseName:  name,
			DisplayName: display,
			// The Annex A.3.1.3 floor, until the item's properties are known.
			TypeDefinition: NumericNodeID(0, NodeIDDataItemType),
			ValueRank:      ValueRankScalar,
			ItemID:         *entry.ItemID,
			// A type the mapping cannot express is reported as the abstract
			// base type rather than guessed at.
			DataType: NumericNodeID(0, NodeIDBaseDataType),
		}
		if entry.CanonicalType != nil {
			if dataType, ok := DataTypeFor(*entry.CanonicalType); ok {
				if id, resolved := DataTypeNodeID(dataType); resolved {
					node.DataType = id
					node.DataTypeKnown = true
				}
			}
		}
		node.AccessLevel, node.AccessRightsKnown = accessLevelFor(entry.AccessRights)
		return node, nil
	default:
		return nil, fmt.Errorf("browse entry kind %q is not known", entry.Kind)
	}
}

// accessLevelFor maps DA access rights onto the UA access level and reports
// whether the source supplied them.
//
// OPC DA carries access rights in the AddItems result, not in Browse, so a
// browsed item normally arrives without them. When they are unknown the adapter
// reports the node as readable and writable, because the adapter itself imposes
// no restriction: the source is the authority and answers OPC_E_BADRIGHTS for
// an operation it does not permit, which Part 8 Table A.4 and A.5 map to
// Bad_NotReadable and Bad_NotWritable. Reporting no access instead would be the
// adapter claiming a restriction it does not enforce and cannot verify.
func accessLevelFor(rights *opcda.DAAccessRights) (byte, bool) {
	if rights == nil {
		return AccessLevelCurrentRead | AccessLevelCurrentWrite, false
	}
	var level byte
	if rights.Read {
		level |= AccessLevelCurrentRead
	}
	if rights.Write {
		level |= AccessLevelCurrentWrite
	}
	return level, true
}

// LearnFromRead records what a Read result taught about a variable.
//
// OPC DA reports an item's canonical type and access rights in the AddItems
// result, which a Browse never produces, so a browsed node starts out knowing
// neither. A Read goes through AddItems, so its result is the source telling us
// — and recording it makes the node's attributes accurate for every client that
// comes after, rather than leaving them permanently unknown.
func (s *AddressSpace) LearnFromRead(id NodeID, canonicalType *opcda.DAVarType, rights *opcda.DAAccessRights) {
	if canonicalType == nil && rights == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	node, ok := s.nodes[nodeKey(id)]
	if !ok || node.Class != NodeClassVariable {
		return
	}
	if canonicalType != nil && !node.DataTypeKnown {
		if dataType, mapped := DataTypeFor(*canonicalType); mapped {
			if resolved, ok := DataTypeNodeID(dataType); ok {
				node.DataType = resolved
				node.DataTypeKnown = true
			}
		}
	}
	if rights != nil && !node.AccessRightsKnown {
		node.AccessLevel, node.AccessRightsKnown = accessLevelFor(rights)
	}
}

// NodeCount reports how many nodes the space holds, for diagnostics.
func (s *AddressSpace) NodeCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.nodes)
}

// SourceNodeCount reports how many nodes came from the DA source. The node
// budget counts these and not the server's own standard nodes: the budget
// exists to stop a source with a very large or hostile address space from
// exhausting memory, and the standard nodes are a fixed set this server always
// publishes. Counting them would mean that adding a node the specification
// requires silently reduced how many DA items an operator's configured limit
// allows.
func (s *AddressSpace) SourceNodeCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.nodes) - s.standardNodeCount
}
