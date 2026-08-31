package opcua

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Browse follows OPC 10000-4 Tables 34, 37, 113, 168, 112 and 194.
const (
	BrowseRequestEncodingID      uint32 = 527
	BrowseResponseEncodingID     uint32 = 530
	BrowseNextRequestEncodingID  uint32 = 533
	BrowseNextResponseEncodingID uint32 = 536
)

// Browse status codes from the OPC Foundation StatusCode list.
const (
	StatusBadNothingToDo              StatusCode = 0x800F0000
	StatusBadTooManyOperations        StatusCode = 0x80100000
	StatusBadContinuationPointInvalid StatusCode = 0x804A0000
	StatusBadNoContinuationPoints     StatusCode = 0x804B0000
	StatusBadReferenceTypeIDInvalid   StatusCode = 0x804C0000
	StatusBadBrowseDirectionInvalid   StatusCode = 0x804D0000
	StatusBadViewIDUnknown            StatusCode = 0x806B0000
)

// BrowseDirection values from OPC 10000-4 Table 112.
type BrowseDirection int32

const (
	BrowseDirectionForward BrowseDirection = 0
	BrowseDirectionInverse BrowseDirection = 1
	BrowseDirectionBoth    BrowseDirection = 2
	BrowseDirectionInvalid BrowseDirection = 3
)

// resultMask bits from OPC 10000-4 Table 34. A client asks for exactly the
// ReferenceDescription fields it wants; anything not asked for is omitted.
const (
	ResultMaskReferenceType  uint32 = 1 << 0
	ResultMaskIsForward      uint32 = 1 << 1
	ResultMaskNodeClass      uint32 = 1 << 2
	ResultMaskBrowseName     uint32 = 1 << 3
	ResultMaskDisplayName    uint32 = 1 << 4
	ResultMaskTypeDefinition uint32 = 1 << 5
	ResultMaskAll            uint32 = 0x3F
)

// ViewDescription is OPC 10000-4 Table 194.
type ViewDescription struct {
	ViewID      NodeID
	Timestamp   time.Time
	ViewVersion uint32
}

// BrowseDescription is the per-node request of Table 34.
type BrowseDescription struct {
	NodeID          NodeID
	BrowseDirection BrowseDirection
	ReferenceTypeID NodeID
	IncludeSubtypes bool
	NodeClassMask   uint32
	ResultMask      uint32
}

// ReferenceDescription is OPC 10000-4 Table 168.
type ReferenceDescription struct {
	ReferenceTypeID NodeID
	IsForward       bool
	NodeID          ExpandedNodeID
	BrowseName      QualifiedName
	DisplayName     LocalizedText
	NodeClass       NodeClass
	TypeDefinition  ExpandedNodeID
}

// BrowseResult is OPC 10000-4 Table 113.
type BrowseResult struct {
	StatusCode        StatusCode
	ContinuationPoint []byte
	References        []ReferenceDescription
}

type BrowseRequest struct {
	Header                        RequestHeader
	View                          ViewDescription
	RequestedMaxReferencesPerNode uint32
	NodesToBrowse                 []BrowseDescription
}

type BrowseResponse struct {
	Header      ResponseHeader
	Results     []BrowseResult
	Diagnostics []DiagnosticInfo
}

type BrowseNextRequest struct {
	Header                    RequestHeader
	ReleaseContinuationPoints bool
	ContinuationPoints        [][]byte
}

type BrowseNextResponse struct {
	Header      ResponseHeader
	Results     []BrowseResult
	Diagnostics []DiagnosticInfo
}

func (e *Encoder) WriteViewDescription(value ViewDescription) {
	e.WriteNodeID(value.ViewID)
	e.WriteDateTime(value.Timestamp)
	e.WriteUInt32(value.ViewVersion)
}

func (d *Decoder) ReadViewDescription() (ViewDescription, error) {
	var value ViewDescription
	var err error
	if value.ViewID, err = d.ReadNodeID(); err != nil {
		return ViewDescription{}, err
	}
	if value.Timestamp, err = d.ReadDateTime(); err != nil {
		return ViewDescription{}, err
	}
	if value.ViewVersion, err = d.ReadUInt32(); err != nil {
		return ViewDescription{}, err
	}
	return value, nil
}

func (e *Encoder) WriteBrowseDescription(value BrowseDescription) {
	e.WriteNodeID(value.NodeID)
	e.WriteInt32(int32(value.BrowseDirection))
	e.WriteNodeID(value.ReferenceTypeID)
	e.WriteBoolean(value.IncludeSubtypes)
	e.WriteUInt32(value.NodeClassMask)
	e.WriteUInt32(value.ResultMask)
}

func (d *Decoder) ReadBrowseDescription() (BrowseDescription, error) {
	var value BrowseDescription
	var err error
	if value.NodeID, err = d.ReadNodeID(); err != nil {
		return BrowseDescription{}, err
	}
	direction, err := d.ReadInt32()
	if err != nil {
		return BrowseDescription{}, err
	}
	// A direction outside the enumeration is refused rather than reduced to a
	// neighbouring one.
	if direction < int32(BrowseDirectionForward) || direction > int32(BrowseDirectionInvalid) {
		return BrowseDescription{}, decodingError("BrowseDirection %d is not defined", direction)
	}
	value.BrowseDirection = BrowseDirection(direction)
	if value.ReferenceTypeID, err = d.ReadNodeID(); err != nil {
		return BrowseDescription{}, err
	}
	if value.IncludeSubtypes, err = d.ReadBoolean(); err != nil {
		return BrowseDescription{}, err
	}
	if value.NodeClassMask, err = d.ReadUInt32(); err != nil {
		return BrowseDescription{}, err
	}
	if value.ResultMask, err = d.ReadUInt32(); err != nil {
		return BrowseDescription{}, err
	}
	return value, nil
}

func (e *Encoder) WriteReferenceDescription(value ReferenceDescription) {
	e.WriteNodeID(value.ReferenceTypeID)
	e.WriteBoolean(value.IsForward)
	e.WriteExpandedNodeID(value.NodeID)
	e.WriteQualifiedName(value.BrowseName)
	e.WriteLocalizedText(value.DisplayName)
	e.WriteInt32(int32(value.NodeClass))
	e.WriteExpandedNodeID(value.TypeDefinition)
}

func (d *Decoder) ReadReferenceDescription() (ReferenceDescription, error) {
	var value ReferenceDescription
	var err error
	if value.ReferenceTypeID, err = d.ReadNodeID(); err != nil {
		return ReferenceDescription{}, err
	}
	if value.IsForward, err = d.ReadBoolean(); err != nil {
		return ReferenceDescription{}, err
	}
	if value.NodeID, err = d.ReadExpandedNodeID(); err != nil {
		return ReferenceDescription{}, err
	}
	if value.BrowseName, err = d.ReadQualifiedName(); err != nil {
		return ReferenceDescription{}, err
	}
	if value.DisplayName, err = d.ReadLocalizedText(); err != nil {
		return ReferenceDescription{}, err
	}
	nodeClass, err := d.ReadInt32()
	if err != nil {
		return ReferenceDescription{}, err
	}
	value.NodeClass = NodeClass(nodeClass)
	if value.TypeDefinition, err = d.ReadExpandedNodeID(); err != nil {
		return ReferenceDescription{}, err
	}
	return value, nil
}

func (e *Encoder) WriteBrowseResult(value BrowseResult) {
	e.WriteStatusCode(value.StatusCode)
	if len(value.ContinuationPoint) == 0 {
		e.WriteNullByteString()
	} else {
		e.WriteByteString(value.ContinuationPoint)
	}
	e.WriteArrayLength(len(value.References))
	for _, reference := range value.References {
		e.WriteReferenceDescription(reference)
	}
}

func (d *Decoder) ReadBrowseResult() (BrowseResult, error) {
	var value BrowseResult
	status, err := d.ReadStatusCode()
	if err != nil {
		return BrowseResult{}, err
	}
	value.StatusCode = status
	point, isNull, err := d.ReadByteString()
	if err != nil {
		return BrowseResult{}, err
	}
	if !isNull {
		value.ContinuationPoint = point
	}
	// A ReferenceDescription is at least its fixed prefixes.
	length, refsNull, err := d.ReadArrayLength(16)
	if err != nil {
		return BrowseResult{}, err
	}
	if !refsNull {
		value.References = make([]ReferenceDescription, 0, length)
		for index := 0; index < length; index++ {
			reference, refErr := d.ReadReferenceDescription()
			if refErr != nil {
				return BrowseResult{}, refErr
			}
			value.References = append(value.References, reference)
		}
	}
	return value, nil
}

func (e *Encoder) WriteBrowseRequest(request BrowseRequest) {
	e.WriteServiceTypeID(BrowseRequestEncodingID)
	e.WriteRequestHeader(request.Header)
	e.WriteViewDescription(request.View)
	e.WriteUInt32(request.RequestedMaxReferencesPerNode)
	e.WriteArrayLength(len(request.NodesToBrowse))
	for _, description := range request.NodesToBrowse {
		e.WriteBrowseDescription(description)
	}
}

func (d *Decoder) ReadBrowseRequest() (BrowseRequest, error) {
	var request BrowseRequest
	var err error
	if request.Header, err = d.ReadRequestHeader(); err != nil {
		return BrowseRequest{}, err
	}
	if request.View, err = d.ReadViewDescription(); err != nil {
		return BrowseRequest{}, err
	}
	if request.RequestedMaxReferencesPerNode, err = d.ReadUInt32(); err != nil {
		return BrowseRequest{}, err
	}
	// A BrowseDescription is at least two NodeIds and three fixed fields.
	length, isNull, err := d.ReadArrayLength(13)
	if err != nil {
		return BrowseRequest{}, err
	}
	if !isNull {
		request.NodesToBrowse = make([]BrowseDescription, 0, length)
		for index := 0; index < length; index++ {
			description, descriptionErr := d.ReadBrowseDescription()
			if descriptionErr != nil {
				return BrowseRequest{}, descriptionErr
			}
			request.NodesToBrowse = append(request.NodesToBrowse, description)
		}
	}
	return request, nil
}

func (e *Encoder) writeBrowseResults(results []BrowseResult, diagnostics []DiagnosticInfo) {
	e.WriteArrayLength(len(results))
	for _, result := range results {
		e.WriteBrowseResult(result)
	}
	e.WriteArrayLength(len(diagnostics))
	for _, diagnostic := range diagnostics {
		e.WriteDiagnosticInfo(diagnostic)
	}
}

func (d *Decoder) readBrowseResults() ([]BrowseResult, []DiagnosticInfo, error) {
	length, isNull, err := d.ReadArrayLength(9)
	if err != nil {
		return nil, nil, err
	}
	var results []BrowseResult
	if !isNull {
		results = make([]BrowseResult, 0, length)
		for index := 0; index < length; index++ {
			result, resultErr := d.ReadBrowseResult()
			if resultErr != nil {
				return nil, nil, resultErr
			}
			results = append(results, result)
		}
	}
	length, isNull, err = d.ReadArrayLength(1)
	if err != nil {
		return nil, nil, err
	}
	var diagnostics []DiagnosticInfo
	if !isNull {
		diagnostics = make([]DiagnosticInfo, 0, length)
		for index := 0; index < length; index++ {
			diagnostic, diagnosticErr := d.ReadDiagnosticInfo()
			if diagnosticErr != nil {
				return nil, nil, diagnosticErr
			}
			diagnostics = append(diagnostics, diagnostic)
		}
	}
	return results, diagnostics, nil
}

func (e *Encoder) WriteBrowseResponse(response BrowseResponse) {
	e.WriteServiceTypeID(BrowseResponseEncodingID)
	e.WriteResponseHeader(response.Header)
	e.writeBrowseResults(response.Results, response.Diagnostics)
}

func (d *Decoder) ReadBrowseResponse() (BrowseResponse, error) {
	var response BrowseResponse
	header, err := d.ReadResponseHeader()
	if err != nil {
		return BrowseResponse{}, err
	}
	response.Header = header
	response.Results, response.Diagnostics, err = d.readBrowseResults()
	if err != nil {
		return BrowseResponse{}, err
	}
	return response, nil
}

func (e *Encoder) WriteBrowseNextRequest(request BrowseNextRequest) {
	e.WriteServiceTypeID(BrowseNextRequestEncodingID)
	e.WriteRequestHeader(request.Header)
	e.WriteBoolean(request.ReleaseContinuationPoints)
	e.WriteArrayLength(len(request.ContinuationPoints))
	for _, point := range request.ContinuationPoints {
		e.WriteByteString(point)
	}
}

func (d *Decoder) ReadBrowseNextRequest() (BrowseNextRequest, error) {
	var request BrowseNextRequest
	var err error
	if request.Header, err = d.ReadRequestHeader(); err != nil {
		return BrowseNextRequest{}, err
	}
	if request.ReleaseContinuationPoints, err = d.ReadBoolean(); err != nil {
		return BrowseNextRequest{}, err
	}
	length, isNull, err := d.ReadArrayLength(4)
	if err != nil {
		return BrowseNextRequest{}, err
	}
	if !isNull {
		request.ContinuationPoints = make([][]byte, 0, length)
		for index := 0; index < length; index++ {
			point, pointIsNull, pointErr := d.ReadByteString()
			if pointErr != nil {
				return BrowseNextRequest{}, pointErr
			}
			if pointIsNull {
				point = nil
			}
			request.ContinuationPoints = append(request.ContinuationPoints, point)
		}
	}
	return request, nil
}

func (e *Encoder) WriteBrowseNextResponse(response BrowseNextResponse) {
	e.WriteServiceTypeID(BrowseNextResponseEncodingID)
	e.WriteResponseHeader(response.Header)
	e.writeBrowseResults(response.Results, response.Diagnostics)
}

func (d *Decoder) ReadBrowseNextResponse() (BrowseNextResponse, error) {
	var response BrowseNextResponse
	header, err := d.ReadResponseHeader()
	if err != nil {
		return BrowseNextResponse{}, err
	}
	response.Header = header
	response.Results, response.Diagnostics, err = d.readBrowseResults()
	if err != nil {
		return BrowseNextResponse{}, err
	}
	return response, nil
}

// BrowseLimits bounds what one Browse can cost the server.
type BrowseLimits struct {
	MaxNodesPerBrowse    int
	MaxReferencesPerNode int
	// MaxContinuationPoints is per session, as 7.9 defines it: "Servers
	// specify a maximum number of ContinuationPoints per Session". The total
	// the process can hold is therefore this times the session limit.
	MaxContinuationPoints   int
	ContinuationPointExpiry time.Duration
}

func DefaultBrowseLimits() BrowseLimits {
	return BrowseLimits{
		MaxNodesPerBrowse:       64,
		MaxReferencesPerNode:    256,
		MaxContinuationPoints:   16,
		ContinuationPointExpiry: 5 * time.Minute,
	}
}

func (limits BrowseLimits) validate() error {
	if limits.MaxNodesPerBrowse <= 0 || limits.MaxReferencesPerNode <= 0 ||
		limits.MaxContinuationPoints <= 0 || limits.ContinuationPointExpiry <= 0 {
		return fmt.Errorf("all browse limits must be positive")
	}
	return nil
}

func (limits BrowseLimits) ValidateForConfiguration() error { return limits.validate() }

// continuation holds the references a Browse could not fit in one response.
type continuation struct {
	remaining []ReferenceDescription
	createdAt time.Time
	// session owns this point. Clause 5.9.3.1: "the BrowseNext shall be
	// submitted on the same Session that was used to submit the Browse or
	// BrowseNext that is being continued", and 7.9 has points released when
	// that session closes.
	session string
	// maximum is the per-node limit the original Browse asked for, carried
	// forward because BrowseNext has no parameter to restate it. Clause
	// 5.9.3.1 defines "too large" as exceeding "the maximum number of results
	// to return that was specified by the Client in the original Browse
	// request", so it governs every response continuing that Browse, not just
	// the first.
	maximum int
}

// BrowseService answers Browse and BrowseNext against the address space.
type BrowseService struct {
	space  *AddressSpace
	limits BrowseLimits
	// populator fills a branch from the DA source before it is browsed. It is
	// nil when no source is attached, in which case only the standard nodes
	// exist.
	populator *Populator

	mu     sync.Mutex
	points map[string]*continuation
}

func NewBrowseService(space *AddressSpace, limits BrowseLimits) (*BrowseService, error) {
	if err := limits.validate(); err != nil {
		return nil, err
	}
	return &BrowseService{space: space, limits: limits, points: make(map[string]*continuation)}, nil
}

// AttachPopulator makes the service fill a branch from the DA source before
// browsing it.
func (s *BrowseService) AttachPopulator(populator *Populator) { s.populator = populator }

// Browse answers one request. Table 34: the size and order of the results match
// the size and order of nodesToBrowse, so a per-node failure occupies its slot
// rather than shortening the list.
func (s *BrowseService) Browse(ctx context.Context, session string, request BrowseRequest, now time.Time) (BrowseResponse, error) {
	if len(request.NodesToBrowse) == 0 {
		return BrowseResponse{}, uacpError(StatusBadNothingToDo, "the browse request named no nodes")
	}
	if len(request.NodesToBrowse) > s.limits.MaxNodesPerBrowse {
		return BrowseResponse{}, uacpError(StatusBadTooManyOperations,
			"the browse request named %d nodes; the limit is %d",
			len(request.NodesToBrowse), s.limits.MaxNodesPerBrowse)
	}
	// A null viewId means the entire address space; no other view exists.
	if !request.View.ViewID.IsNull() {
		return BrowseResponse{}, uacpError(StatusBadViewIDUnknown, "this server publishes no views")
	}

	maximum := s.limits.MaxReferencesPerNode
	// Table 34: zero means the client imposes no limit, so the server's own
	// bound applies.
	if request.RequestedMaxReferencesPerNode > 0 &&
		int(request.RequestedMaxReferencesPerNode) < maximum {
		maximum = int(request.RequestedMaxReferencesPerNode)
	}

	results := make([]BrowseResult, 0, len(request.NodesToBrowse))
	for _, description := range request.NodesToBrowse {
		// The branch is filled from the source before it is read, so a client
		// sees the source's current contents rather than a stale snapshot. A
		// population failure is reported for that node alone; the other nodes
		// in the same request are unaffected.
		if err := s.ensurePopulated(ctx, description.NodeID, now); err != nil {
			results = append(results, BrowseResult{StatusCode: statusForPopulationError(err)})
			continue
		}
		results = append(results, s.browseNode(session, description, maximum, now))
	}
	return BrowseResponse{
		Header: ResponseHeader{
			Timestamp: now, RequestHandle: request.Header.RequestHandle,
			ServiceResult: StatusGood, AdditionalHeader: NullExtensionObject(),
		},
		Results:     results,
		Diagnostics: []DiagnosticInfo{},
	}, nil
}

// ensurePopulated fills the branch a node stands for. Only the source folder
// and branch nodes have a DA browse path; anything else needs no population.
func (s *BrowseService) ensurePopulated(ctx context.Context, id NodeID, now time.Time) error {
	if s.populator == nil {
		return nil
	}
	if id.Equal(s.space.SourceFolderID()) {
		return s.populator.EnsureBranch(ctx, nil, now)
	}
	// Browsing a source variable is a client asking what it has, and OPC
	// 10000-8 Table A.1 properties are part of that answer. They are discovered
	// here rather than when the item node is created, so an item nobody browses
	// costs no call to the source.
	if node, kind := s.space.ClassifyNode(id); kind == NodeKindItem {
		return s.populator.EnsureItemProperties(ctx, node.ItemID, now)
	}
	path, ok := PathForNode(id)
	if !ok {
		return nil
	}
	return s.populator.EnsureBranch(ctx, path, now)
}

// statusForPopulationError keeps a CodecError's own status and maps a DA
// failure through the same rules the data services use.
func statusForPopulationError(err error) StatusCode {
	var codecErr *CodecError
	if errors.As(err, &codecErr) {
		return codecErr.Status
	}
	return statusForRuntimeError(err)
}

func (s *BrowseService) browseNode(session string, description BrowseDescription, maximum int, now time.Time) BrowseResult {
	if description.BrowseDirection == BrowseDirectionInvalid {
		return BrowseResult{StatusCode: StatusBadBrowseDirectionInvalid}
	}
	node, ok := s.space.Node(description.NodeID)
	if !ok {
		return BrowseResult{StatusCode: StatusBadNodeIdUnknown}
	}
	// Table 34: an unspecified referenceTypeId returns all references. A
	// specified one that names no reference type is an error rather than a
	// filter that silently matches nothing.
	if !description.ReferenceTypeID.IsNull() && !s.isKnownReferenceType(description.ReferenceTypeID) {
		return BrowseResult{StatusCode: StatusBadReferenceTypeIDInvalid}
	}

	matched := make([]ReferenceDescription, 0, len(node.References))
	for _, reference := range node.References {
		if !directionMatches(description.BrowseDirection, reference.IsForward) {
			continue
		}
		if !description.ReferenceTypeID.IsNull() &&
			!referenceTypeMatches(description.ReferenceTypeID, reference.ReferenceTypeID, description.IncludeSubtypes) {
			continue
		}
		// Table 34: a zero nodeClassMask returns all node classes; otherwise it
		// is a mask, not an equality test.
		if description.NodeClassMask != 0 &&
			description.NodeClassMask&uint32(reference.NodeClass) == 0 {
			continue
		}
		matched = append(matched, applyResultMask(reference, description.ResultMask))
	}

	if len(matched) <= maximum {
		return BrowseResult{StatusCode: StatusGood, References: matched}
	}
	point, err := s.storeContinuation(session, matched[maximum:], maximum, now)
	if err != nil {
		// The operation fails rather than silently truncating: a client that
		// received part of an answer and no continuation point would have no
		// way of knowing the rest existed. The error carries its own status --
		// a failed random read is not the server running out of points.
		return BrowseResult{StatusCode: statusForPopulationError(err)}
	}
	return BrowseResult{StatusCode: StatusGood, ContinuationPoint: point, References: matched[:maximum]}
}

// referenceSupertypes gives each reference type this address space uses the
// chain of types it inherits from, taken from the OPC Foundation NodeSet. It
// is what makes Table 34's includeSubtypes work: a client that browses for
// HierarchicalReferences with subtypes included expects the Organizes and
// HasProperty references below to match, and gets an empty result if they do
// not. Only the types the address space actually emits need an entry, because
// a type nothing references can never be on the right-hand side of a match.
var referenceSupertypes = map[uint32][]uint32{
	NodeIDOrganizes:         {NodeIDHierarchicalRefs, NodeIDReferences},
	NodeIDHasProperty:       {NodeIDAggregates, NodeIDHasChild, NodeIDHierarchicalRefs, NodeIDReferences},
	NodeIDHasComponent:      {NodeIDAggregates, NodeIDHasChild, NodeIDHierarchicalRefs, NodeIDReferences},
	NodeIDHasTypeDefinition: {NodeIDNonHierarchicalRefs, NodeIDReferences},
}

// referenceTypeMatches applies Table 34's referenceTypeId filter. Without
// includeSubtypes it is an equality test; with it, a reference also matches
// when the requested type is one of its supertypes.
func referenceTypeMatches(requested, actual NodeID, includeSubtypes bool) bool {
	if requested.Equal(actual) {
		return true
	}
	if !includeSubtypes {
		return false
	}
	if requested.Namespace != 0 || requested.Type != NodeIDTypeNumeric ||
		actual.Namespace != 0 || actual.Type != NodeIDTypeNumeric {
		return false
	}
	for _, supertype := range referenceSupertypes[actual.Numeric] {
		if supertype == requested.Numeric {
			return true
		}
	}
	return false
}

// isKnownReferenceType recognises the reference types this address space uses.
func (s *BrowseService) isKnownReferenceType(id NodeID) bool {
	if id.Namespace != 0 || id.Type != NodeIDTypeNumeric {
		return false
	}
	switch id.Numeric {
	case NodeIDReferences, NodeIDNonHierarchicalRefs, NodeIDHierarchicalRefs,
		NodeIDHasChild, NodeIDOrganizes, NodeIDAggregates,
		NodeIDHasTypeDefinition, NodeIDHasProperty, NodeIDHasComponent:
		return true
	default:
		return false
	}
}

func directionMatches(direction BrowseDirection, isForward bool) bool {
	switch direction {
	case BrowseDirectionForward:
		return isForward
	case BrowseDirectionInverse:
		return !isForward
	default:
		return true
	}
}

// applyResultMask returns only the fields the client asked for. Table 34 makes
// the mask a request for specific fields, so anything unasked is omitted rather
// than sent anyway.
func applyResultMask(reference Reference, mask uint32) ReferenceDescription {
	description := ReferenceDescription{NodeID: reference.TargetID}
	if mask&ResultMaskReferenceType != 0 {
		description.ReferenceTypeID = reference.ReferenceTypeID
	}
	if mask&ResultMaskIsForward != 0 {
		description.IsForward = reference.IsForward
	}
	if mask&ResultMaskNodeClass != 0 {
		description.NodeClass = reference.NodeClass
	}
	if mask&ResultMaskBrowseName != 0 {
		description.BrowseName = reference.BrowseName
	}
	if mask&ResultMaskDisplayName != 0 {
		description.DisplayName = reference.DisplayName
	}
	// Table 168: type definitions exist only for Object and Variable; anything
	// else carries a null NodeId.
	if mask&ResultMaskTypeDefinition != 0 &&
		(reference.NodeClass == NodeClassObject || reference.NodeClass == NodeClassVariable) {
		description.TypeDefinition = reference.TypeDefinition
	}
	return description
}

func (s *BrowseService) storeContinuation(session string, remaining []ReferenceDescription, maximum int, now time.Time) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(now)
	// Clause 7.9: "a Server shall automatically free ContinuationPoints from
	// prior requests from a Session if they are needed to process a new request
	// from this Session". So a session at its limit loses its own oldest point
	// rather than being refused -- the newest request is the one the client is
	// waiting on, and the abandoned one is what it has stopped asking about.
	// Only this session's points are touched: another session's are not this
	// one's to spend.
	//
	// "Prior requests" is the whole of it. A point handed out earlier in this
	// same response is not spare capacity: 7.9 continues, "a Server shall
	// process the operations until it uses the maximum number of continuation
	// points in this response. Once that happens the Server shall return a
	// Bad_NoContinuationPoints error for any remaining operations". Freeing
	// those would revoke a point in the very response that carries it.
	if !s.freeOldestLocked(session, now) {
		return nil, uacpError(StatusBadNoContinuationPoints,
			"this session holds its %d continuation points for this response alone",
			s.limits.MaxContinuationPoints)
	}
	identifier := make([]byte, 16)
	if _, err := rand.Read(identifier); err != nil {
		return nil, uacpError(StatusBadTcpInternalError, "could not generate a continuation point")
	}
	s.points[string(identifier)] = &continuation{
		remaining: remaining, createdAt: now, session: session, maximum: maximum,
	}
	return identifier, nil
}

// freeOldestLocked drops this session's oldest points from prior requests until
// it has room for one more, and reports whether it made any. It cannot touch a
// point this request itself created, which is what a createdAt of now marks.
func (s *BrowseService) freeOldestLocked(session string, now time.Time) bool {
	for {
		held := 0
		oldestKey := ""
		var oldest time.Time
		for key, point := range s.points {
			if point.session != session {
				continue
			}
			held++
			if !point.createdAt.Before(now) {
				continue
			}
			if oldestKey == "" || point.createdAt.Before(oldest) {
				oldestKey, oldest = key, point.createdAt
			}
		}
		if held < s.limits.MaxContinuationPoints {
			return true
		}
		if oldestKey == "" {
			// Every point this session holds belongs to this response.
			return false
		}
		delete(s.points, oldestKey)
	}
}

func (s *BrowseService) expireLocked(now time.Time) {
	for key, point := range s.points {
		if now.Sub(point.createdAt) >= s.limits.ContinuationPointExpiry {
			delete(s.points, key)
		}
	}
}

// BrowseNext continues or releases continuation points.
func (s *BrowseService) BrowseNext(session string, request BrowseNextRequest, now time.Time) (BrowseNextResponse, error) {
	if len(request.ContinuationPoints) == 0 {
		return BrowseNextResponse{}, uacpError(StatusBadNothingToDo, "no continuation point was supplied")
	}
	if len(request.ContinuationPoints) > s.limits.MaxNodesPerBrowse {
		return BrowseNextResponse{}, uacpError(StatusBadTooManyOperations,
			"%d continuation points exceed the %d limit",
			len(request.ContinuationPoints), s.limits.MaxNodesPerBrowse)
	}

	header := ResponseHeader{
		Timestamp: now, RequestHandle: request.Header.RequestHandle,
		ServiceResult: StatusGood, AdditionalHeader: NullExtensionObject(),
	}
	// Table 37: when releasing, the results and diagnosticInfos arrays are
	// empty.
	if request.ReleaseContinuationPoints {
		s.mu.Lock()
		for _, point := range request.ContinuationPoints {
			// A session releases only what it owns.
			if held, ok := s.points[string(point)]; ok && held.session == session {
				delete(s.points, string(point))
			}
		}
		s.mu.Unlock()
		return BrowseNextResponse{Header: header, Results: []BrowseResult{}, Diagnostics: []DiagnosticInfo{}}, nil
	}

	results := make([]BrowseResult, 0, len(request.ContinuationPoints))
	for _, point := range request.ContinuationPoints {
		results = append(results, s.continueBrowse(session, point, now))
	}
	return BrowseNextResponse{Header: header, Results: results, Diagnostics: []DiagnosticInfo{}}, nil
}

func (s *BrowseService) continueBrowse(session string, point []byte, now time.Time) BrowseResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked(now)
	stored, ok := s.points[string(point)]
	// Clause 5.9.3.1: "the BrowseNext shall be submitted on the same Session
	// that was used to submit the Browse ... that is being continued". Another
	// session's point is not invalid in general, but it is invalid here, and
	// saying so is all a client on the wrong session needs to hear.
	if !ok || stored.session != session {
		return BrowseResult{StatusCode: StatusBadContinuationPointInvalid}
	}
	// A continuation point is consumed by use: the client gets a new one if
	// more remains, so a stale point can never be replayed.
	delete(s.points, string(point))

	// The original Browse's limit, not the server's: 5.9.3.1 defines "too
	// large" against "the maximum number of results to return that was
	// specified by the Client in the original Browse request", so a client that
	// asked for five per node gets five here too rather than the server's own
	// far larger bound.
	maximum := stored.maximum
	if len(stored.remaining) <= maximum {
		return BrowseResult{StatusCode: StatusGood, References: stored.remaining}
	}
	next := make([]byte, 16)
	if _, err := rand.Read(next); err != nil {
		// Never Bad_NoContinuationPoints: 7.9 says a server "shall never return
		// Bad_NoContinuationPoints error when continuing a previously halted
		// operation", and this is not that failure in any case.
		return BrowseResult{StatusCode: StatusBadTcpInternalError}
	}
	s.points[string(next)] = &continuation{
		remaining: stored.remaining[maximum:], createdAt: now,
		session: session, maximum: maximum,
	}
	return BrowseResult{
		StatusCode: StatusGood, ContinuationPoint: next, References: stored.remaining[:maximum],
	}
}

// ContinuationPointCount reports how many points are held, for bounds and
// diagnostics.
func (s *BrowseService) ContinuationPointCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.points)
}

// ReleaseSession drops every continuation point a session held. Clause 7.9:
// points "remain active until the Client retrieves the remaining results, the
// Client releases the ContinuationPoint or the Session is closed".
func (s *BrowseService) ReleaseSession(session string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	released := 0
	for key, point := range s.points {
		if point.session == session {
			delete(s.points, key)
			released++
		}
	}
	return released
}

// ExpireContinuationPoints drops points a client abandoned.
func (s *BrowseService) ExpireContinuationPoints(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := len(s.points)
	s.expireLocked(now)
	return before - len(s.points)
}
