package opcua

import (
	"context"
	"fmt"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// Read and Write follow OPC 10000-4 Tables 47, 53, 167 and 180.
const (
	ReadRequestEncodingID   uint32 = 631
	ReadResponseEncodingID  uint32 = 634
	WriteRequestEncodingID  uint32 = 673
	WriteResponseEncodingID uint32 = 676
)

// Status codes used by these services, from the OPC Foundation list.
const (
	StatusBadAttributeIDInvalid StatusCode = 0x80350000
	StatusBadIndexRangeInvalid  StatusCode = 0x80360000
	StatusBadInvalidArgument    StatusCode = 0x80AB0000
	StatusBadTimeout            StatusCode = 0x800A0000
	StatusBadDataTypeIDUnknown  StatusCode = 0x80110000
)

// TimestampsToReturn values from OPC 10000-4 Table 180.
type TimestampsToReturn int32

const (
	TimestampsSource  TimestampsToReturn = 0
	TimestampsServer  TimestampsToReturn = 1
	TimestampsBoth    TimestampsToReturn = 2
	TimestampsNeither TimestampsToReturn = 3
	TimestampsInvalid TimestampsToReturn = 4
)

// ReadValueID is OPC 10000-4 Table 167.
type ReadValueID struct {
	NodeID       NodeID
	AttributeID  uint32
	IndexRange   string
	DataEncoding QualifiedName
}

// WriteValue is the per-node request of Table 53.
type WriteValue struct {
	NodeID      NodeID
	AttributeID uint32
	IndexRange  string
	Value       DataValue
}

type ReadRequest struct {
	Header             RequestHeader
	MaxAge             float64
	TimestampsToReturn TimestampsToReturn
	NodesToRead        []ReadValueID
}

type ReadResponse struct {
	Header      ResponseHeader
	Results     []DataValue
	Diagnostics []DiagnosticInfo
}

type WriteRequest struct {
	Header       RequestHeader
	NodesToWrite []WriteValue
}

type WriteResponse struct {
	Header      ResponseHeader
	Results     []StatusCode
	Diagnostics []DiagnosticInfo
}

func (e *Encoder) WriteReadValueID(value ReadValueID) {
	e.WriteNodeID(value.NodeID)
	e.WriteUInt32(value.AttributeID)
	if value.IndexRange == "" {
		e.WriteNullString()
	} else {
		e.WriteString(value.IndexRange)
	}
	e.WriteQualifiedName(value.DataEncoding)
}

func (d *Decoder) ReadReadValueID() (ReadValueID, error) {
	var value ReadValueID
	var err error
	if value.NodeID, err = d.ReadNodeID(); err != nil {
		return ReadValueID{}, err
	}
	if value.AttributeID, err = d.ReadUInt32(); err != nil {
		return ReadValueID{}, err
	}
	indexRange, isNull, err := d.ReadString()
	if err != nil {
		return ReadValueID{}, err
	}
	if !isNull {
		value.IndexRange = indexRange
	}
	if value.DataEncoding, err = d.ReadQualifiedName(); err != nil {
		return ReadValueID{}, err
	}
	return value, nil
}

func (e *Encoder) WriteWriteValue(value WriteValue) {
	e.WriteNodeID(value.NodeID)
	e.WriteUInt32(value.AttributeID)
	if value.IndexRange == "" {
		e.WriteNullString()
	} else {
		e.WriteString(value.IndexRange)
	}
	e.WriteDataValue(value.Value)
}

func (d *Decoder) ReadWriteValue() (WriteValue, error) {
	var value WriteValue
	var err error
	if value.NodeID, err = d.ReadNodeID(); err != nil {
		return WriteValue{}, err
	}
	if value.AttributeID, err = d.ReadUInt32(); err != nil {
		return WriteValue{}, err
	}
	indexRange, isNull, err := d.ReadString()
	if err != nil {
		return WriteValue{}, err
	}
	if !isNull {
		value.IndexRange = indexRange
	}
	if value.Value, err = d.ReadDataValue(); err != nil {
		return WriteValue{}, err
	}
	return value, nil
}

func (e *Encoder) WriteReadRequest(request ReadRequest) {
	e.WriteServiceTypeID(ReadRequestEncodingID)
	e.WriteRequestHeader(request.Header)
	e.WriteDouble(request.MaxAge)
	e.WriteInt32(int32(request.TimestampsToReturn))
	e.WriteArrayLength(len(request.NodesToRead))
	for _, value := range request.NodesToRead {
		e.WriteReadValueID(value)
	}
}

func (d *Decoder) ReadReadRequest() (ReadRequest, error) {
	var request ReadRequest
	var err error
	if request.Header, err = d.ReadRequestHeader(); err != nil {
		return ReadRequest{}, err
	}
	if request.MaxAge, err = d.ReadDouble(); err != nil {
		return ReadRequest{}, err
	}
	timestamps, err := d.ReadInt32()
	if err != nil {
		return ReadRequest{}, err
	}
	if timestamps < int32(TimestampsSource) || timestamps > int32(TimestampsInvalid) {
		return ReadRequest{}, decodingError("TimestampsToReturn %d is not defined", timestamps)
	}
	request.TimestampsToReturn = TimestampsToReturn(timestamps)
	// A ReadValueId is at least a NodeId, an attribute id, and two prefixes.
	length, isNull, err := d.ReadArrayLength(12)
	if err != nil {
		return ReadRequest{}, err
	}
	if !isNull {
		request.NodesToRead = make([]ReadValueID, 0, length)
		for index := 0; index < length; index++ {
			value, valueErr := d.ReadReadValueID()
			if valueErr != nil {
				return ReadRequest{}, valueErr
			}
			request.NodesToRead = append(request.NodesToRead, value)
		}
	}
	return request, nil
}

func (e *Encoder) WriteReadResponse(response ReadResponse) {
	e.WriteServiceTypeID(ReadResponseEncodingID)
	e.WriteResponseHeader(response.Header)
	e.WriteArrayLength(len(response.Results))
	for _, result := range response.Results {
		e.WriteDataValue(result)
	}
	e.WriteArrayLength(len(response.Diagnostics))
	for _, diagnostic := range response.Diagnostics {
		e.WriteDiagnosticInfo(diagnostic)
	}
}

func (d *Decoder) ReadReadResponse() (ReadResponse, error) {
	var response ReadResponse
	header, err := d.ReadResponseHeader()
	if err != nil {
		return ReadResponse{}, err
	}
	response.Header = header
	length, isNull, err := d.ReadArrayLength(1)
	if err != nil {
		return ReadResponse{}, err
	}
	if !isNull {
		response.Results = make([]DataValue, 0, length)
		for index := 0; index < length; index++ {
			value, valueErr := d.ReadDataValue()
			if valueErr != nil {
				return ReadResponse{}, valueErr
			}
			response.Results = append(response.Results, value)
		}
	}
	length, isNull, err = d.ReadArrayLength(1)
	if err != nil {
		return ReadResponse{}, err
	}
	if !isNull {
		response.Diagnostics = make([]DiagnosticInfo, 0, length)
		for index := 0; index < length; index++ {
			diagnostic, diagnosticErr := d.ReadDiagnosticInfo()
			if diagnosticErr != nil {
				return ReadResponse{}, diagnosticErr
			}
			response.Diagnostics = append(response.Diagnostics, diagnostic)
		}
	}
	return response, nil
}

func (e *Encoder) WriteWriteRequest(request WriteRequest) {
	e.WriteServiceTypeID(WriteRequestEncodingID)
	e.WriteRequestHeader(request.Header)
	e.WriteArrayLength(len(request.NodesToWrite))
	for _, value := range request.NodesToWrite {
		e.WriteWriteValue(value)
	}
}

func (d *Decoder) ReadWriteRequest() (WriteRequest, error) {
	var request WriteRequest
	header, err := d.ReadRequestHeader()
	if err != nil {
		return WriteRequest{}, err
	}
	request.Header = header
	length, isNull, err := d.ReadArrayLength(10)
	if err != nil {
		return WriteRequest{}, err
	}
	if !isNull {
		request.NodesToWrite = make([]WriteValue, 0, length)
		for index := 0; index < length; index++ {
			value, valueErr := d.ReadWriteValue()
			if valueErr != nil {
				return WriteRequest{}, valueErr
			}
			request.NodesToWrite = append(request.NodesToWrite, value)
		}
	}
	return request, nil
}

func (e *Encoder) WriteWriteResponse(response WriteResponse) {
	e.WriteServiceTypeID(WriteResponseEncodingID)
	e.WriteResponseHeader(response.Header)
	e.WriteArrayLength(len(response.Results))
	for _, result := range response.Results {
		e.WriteStatusCode(result)
	}
	e.WriteArrayLength(len(response.Diagnostics))
	for _, diagnostic := range response.Diagnostics {
		e.WriteDiagnosticInfo(diagnostic)
	}
}

func (d *Decoder) ReadWriteResponse() (WriteResponse, error) {
	var response WriteResponse
	header, err := d.ReadResponseHeader()
	if err != nil {
		return WriteResponse{}, err
	}
	response.Header = header
	length, isNull, err := d.ReadArrayLength(4)
	if err != nil {
		return WriteResponse{}, err
	}
	if !isNull {
		response.Results = make([]StatusCode, 0, length)
		for index := 0; index < length; index++ {
			status, statusErr := d.ReadStatusCode()
			if statusErr != nil {
				return WriteResponse{}, statusErr
			}
			response.Results = append(response.Results, status)
		}
	}
	length, isNull, err = d.ReadArrayLength(1)
	if err != nil {
		return WriteResponse{}, err
	}
	if !isNull {
		response.Diagnostics = make([]DiagnosticInfo, 0, length)
		for index := 0; index < length; index++ {
			diagnostic, diagnosticErr := d.ReadDiagnosticInfo()
			if diagnosticErr != nil {
				return WriteResponse{}, diagnosticErr
			}
			response.Diagnostics = append(response.Diagnostics, diagnostic)
		}
	}
	return response, nil
}

// DataAccessLimits bounds one Read or Write.
type DataAccessLimits struct {
	MaxNodesPerRead  int
	MaxNodesPerWrite int
	RequestTimeout   time.Duration
}

func DefaultDataAccessLimits() DataAccessLimits {
	return DataAccessLimits{
		MaxNodesPerRead:  100,
		MaxNodesPerWrite: 100,
		RequestTimeout:   30 * time.Second,
	}
}

func (limits DataAccessLimits) validate() error {
	if limits.MaxNodesPerRead <= 0 || limits.MaxNodesPerWrite <= 0 || limits.RequestTimeout <= 0 {
		return fmt.Errorf("all data access limits must be positive")
	}
	return nil
}

func (limits DataAccessLimits) ValidateForConfiguration() error { return limits.validate() }

// DataAccessService answers Read and Write against the DA runtime.
type DataAccessService struct {
	space   *AddressSpace
	runtime opcda.Runtime
	limits  DataAccessLimits
}

func NewDataAccessService(space *AddressSpace, runtime opcda.Runtime, limits DataAccessLimits) (*DataAccessService, error) {
	if err := limits.validate(); err != nil {
		return nil, err
	}
	if space == nil || runtime == nil {
		return nil, fmt.Errorf("a data access service needs an address space and a DA runtime")
	}
	return &DataAccessService{space: space, runtime: runtime, limits: limits}, nil
}

// Read answers a Read request. The results match nodesToRead in size and order,
// so a node that cannot be read occupies its slot with a status rather than
// shortening the list.
func (s *DataAccessService) Read(ctx context.Context, request ReadRequest, now time.Time) (ReadResponse, error) {
	if len(request.NodesToRead) == 0 {
		return ReadResponse{}, uacpError(StatusBadNothingToDo, "the read request named no nodes")
	}
	if len(request.NodesToRead) > s.limits.MaxNodesPerRead {
		return ReadResponse{}, uacpError(StatusBadTooManyOperations,
			"the read request named %d nodes; the limit is %d",
			len(request.NodesToRead), s.limits.MaxNodesPerRead)
	}
	if request.MaxAge < 0 {
		// Table 47: negative values are invalid for maxAge.
		return ReadResponse{}, uacpError(StatusBadInvalidArgument, "maxAge must not be negative")
	}
	if request.TimestampsToReturn == TimestampsInvalid {
		return ReadResponse{}, uacpError(StatusBadInvalidArgument, "timestampsToReturn is invalid")
	}

	results := make([]DataValue, len(request.NodesToRead))
	// Value reads go to the source in one batch, preserving the DA core's
	// per-item semantics; other attributes are answered from the address space.
	itemIDs := make([]opcda.DAItemID, 0, len(request.NodesToRead))
	positions := make([]int, 0, len(request.NodesToRead))
	readNodes := make(map[int]NodeID, len(request.NodesToRead))

	for index, target := range request.NodesToRead {
		// Table 167: indexRange is for arrays, and this adapter exposes none.
		if target.IndexRange != "" {
			results[index] = failedDataValue(StatusBadIndexRangeInvalid)
			continue
		}
		node, ok := s.space.Node(target.NodeID)
		if !ok {
			results[index] = failedDataValue(StatusBadNodeIdUnknown)
			continue
		}
		if target.AttributeID != AttributeValue {
			results[index] = s.readAttribute(node, target.AttributeID, now, request.TimestampsToReturn)
			continue
		}
		if node.Class != NodeClassVariable || node.ItemID == "" {
			results[index] = failedDataValue(StatusBadAttributeIDInvalid)
			continue
		}
		// A right the source actually reported is enforced here; an unknown one
		// is left to the source, which answers OPC_E_BADRIGHTS if it does not
		// permit the read.
		if node.AccessRightsKnown && node.AccessLevel&AccessLevelCurrentRead == 0 {
			results[index] = failedDataValue(StatusBadNotReadable)
			continue
		}
		itemIDs = append(itemIDs, node.ItemID)
		positions = append(positions, index)
		readNodes[index] = node.ID
	}

	if len(itemIDs) > 0 {
		s.readFromSource(ctx, itemIDs, positions, readNodes, results, request.TimestampsToReturn, now)
	}
	return ReadResponse{
		Header: ResponseHeader{
			Timestamp: now, RequestHandle: request.Header.RequestHandle,
			ServiceResult: StatusGood, AdditionalHeader: NullExtensionObject(),
		},
		Results:     results,
		Diagnostics: []DiagnosticInfo{},
	}, nil
}

func failedDataValue(status StatusCode) DataValue {
	// Table 131: when the status indicates an error the value is null.
	return DataValue{Value: NullVariant(), Status: status}
}

// readFromSource performs one device Read and maps each result. A method-level
// source failure gives every requested node the same status rather than
// pretending some succeeded.
func (s *DataAccessService) readFromSource(ctx context.Context, itemIDs []opcda.DAItemID, positions []int, nodes map[int]NodeID, results []DataValue, timestamps TimestampsToReturn, now time.Time) {
	readCtx, cancel := context.WithTimeout(ctx, s.limits.RequestTimeout)
	defer cancel()

	sourceResults, err := s.runtime.ReadBatch(readCtx, opcda.ReadRequest{
		Items: itemIDs, Source: opcda.DADataSourceDevice,
	})
	if err != nil {
		status := statusForRuntimeError(err)
		for _, index := range positions {
			results[index] = failedDataValue(status)
		}
		return
	}
	if len(sourceResults) != len(itemIDs) {
		for _, index := range positions {
			results[index] = failedDataValue(StatusBadInternalError)
		}
		return
	}
	for offset, index := range positions {
		result := sourceResults[offset]
		results[index] = dataValueForRead(result, timestamps, now)
		// The source has just told us the item's canonical type and access
		// rights, which a Browse never reports. Recording them makes the node
		// accurate for every client that follows.
		s.space.LearnFromRead(nodes[index], result.CanonicalType, result.AccessRights)
	}
}

// dataValueForRead maps one DA result onto a DataValue using the Part 8
// mapping: the HRESULT and quality decide the status, the DA timestamp is the
// SourceTimestamp, and the adapter's own time is the ServerTimestamp.
func dataValueForRead(result opcda.ReadResult, timestamps TimestampsToReturn, now time.Time) DataValue {
	value := DataValue{Value: NullVariant(), Status: StatusGood}

	if result.HRESULTPresent && result.HRESULT.Failed() {
		value.Status = StatusCodeForReadError(result.HRESULT)
	} else if result.ErrorCode != "" || result.Value == nil {
		value.Status = StatusBadInternalError
	} else {
		value.Status = StatusCodeForQuality(result.Value.QualityRaw)
		variant, ok := variantForDAValue(*result.Value)
		if !ok {
			// A VARTYPE the mapping cannot express is reported rather than
			// coerced into a type it is not.
			value.Status = StatusBadTypeMismatch
		} else if !value.Status.IsBad() {
			value.Value = variant
		}
	}

	// A DA timestamp becomes the SourceTimestamp, and its absence is preserved
	// rather than filled in with the adapter's own clock.
	if result.Value != nil && result.Value.TimestampPresent &&
		(timestamps == TimestampsSource || timestamps == TimestampsBoth) {
		value.SourceTimestamp = result.Value.Timestamp
	}
	if timestamps == TimestampsServer || timestamps == TimestampsBoth {
		value.ServerTimestamp = now
	}
	return value
}

// variantForDAValue converts a decoded DA scalar into a Variant. The Go type
// the DA core produced decides the built-in type, so a width is never widened
// or narrowed on the way out.
func variantForDAValue(value opcda.DAValue) (Variant, bool) {
	switch typed := value.Value.(type) {
	case nil:
		return NullVariant(), true
	case bool:
		return Variant{Type: BuiltInBoolean, Value: typed}, true
	case int8:
		return Variant{Type: BuiltInSByte, Value: typed}, true
	case uint8:
		return Variant{Type: BuiltInByte, Value: typed}, true
	case int16:
		return Variant{Type: BuiltInInt16, Value: typed}, true
	case uint16:
		return Variant{Type: BuiltInUInt16, Value: typed}, true
	case int32:
		return Variant{Type: BuiltInInt32, Value: typed}, true
	case uint32:
		return Variant{Type: BuiltInUInt32, Value: typed}, true
	case int64:
		return Variant{Type: BuiltInInt64, Value: typed}, true
	case uint64:
		return Variant{Type: BuiltInUInt64, Value: typed}, true
	case float32:
		return Variant{Type: BuiltInFloat, Value: typed}, true
	case float64:
		return Variant{Type: BuiltInDouble, Value: typed}, true
	case string:
		return Variant{Type: BuiltInString, Value: typed}, true
	default:
		return Variant{}, false
	}
}

// readAttribute answers a non-Value attribute from the address space.
func (s *DataAccessService) readAttribute(node *Node, attributeID uint32, now time.Time, timestamps TimestampsToReturn) DataValue {
	var variant Variant
	switch attributeID {
	case AttributeNodeID:
		variant = Variant{Type: BuiltInNodeID, Value: node.ID}
	case AttributeNodeClass:
		variant = Variant{Type: BuiltInInt32, Value: int32(node.Class)}
	case AttributeBrowseName:
		variant = Variant{Type: BuiltInQualifiedName, Value: node.BrowseName}
	case AttributeDisplayName:
		variant = Variant{Type: BuiltInLocalizedText, Value: node.DisplayName}
	case AttributeDataType:
		if node.Class != NodeClassVariable {
			return failedDataValue(StatusBadAttributeIDInvalid)
		}
		variant = Variant{Type: BuiltInNodeID, Value: node.DataType}
	case AttributeValueRank:
		if node.Class != NodeClassVariable {
			return failedDataValue(StatusBadAttributeIDInvalid)
		}
		variant = Variant{Type: BuiltInInt32, Value: node.ValueRank}
	case AttributeAccessLevel, AttributeUserAccessLevel:
		if node.Class != NodeClassVariable {
			return failedDataValue(StatusBadAttributeIDInvalid)
		}
		variant = Variant{Type: BuiltInByte, Value: node.AccessLevel}
	case AttributeHistorizing:
		if node.Class != NodeClassVariable {
			return failedDataValue(StatusBadAttributeIDInvalid)
		}
		// The adapter stores no history, which is a fact about the adapter
		// rather than an unsupported attribute.
		variant = Variant{Type: BuiltInBoolean, Value: false}
	default:
		return failedDataValue(StatusBadAttributeIDInvalid)
	}
	value := DataValue{Value: variant, Status: StatusGood}
	if timestamps == TimestampsServer || timestamps == TimestampsBoth {
		value.ServerTimestamp = now
	}
	return value
}

// Write answers a Write request. The results match nodesToWrite in size and
// order.
func (s *DataAccessService) Write(ctx context.Context, request WriteRequest, now time.Time) (WriteResponse, error) {
	if len(request.NodesToWrite) == 0 {
		return WriteResponse{}, uacpError(StatusBadNothingToDo, "the write request named no nodes")
	}
	if len(request.NodesToWrite) > s.limits.MaxNodesPerWrite {
		return WriteResponse{}, uacpError(StatusBadTooManyOperations,
			"the write request named %d nodes; the limit is %d",
			len(request.NodesToWrite), s.limits.MaxNodesPerWrite)
	}

	results := make([]StatusCode, len(request.NodesToWrite))
	items := make([]opcda.WriteItem, 0, len(request.NodesToWrite))
	positions := make([]int, 0, len(request.NodesToWrite))

	for index, target := range request.NodesToWrite {
		if target.IndexRange != "" {
			// Table 53: a server returns Bad_WriteNotSupported when an
			// indexRange is given and writing one is not possible.
			results[index] = StatusBadWriteNotSupported
			continue
		}
		// Table 53: a server returns Bad_WriteNotSupported if it does not
		// support writing timestamps. The DA core writes values only.
		if !target.Value.SourceTimestamp.IsZero() || !target.Value.ServerTimestamp.IsZero() {
			results[index] = StatusBadWriteNotSupported
			continue
		}
		// A status code cannot be written to a DA item either.
		if target.Value.Status != StatusGood {
			results[index] = StatusBadWriteNotSupported
			continue
		}
		node, ok := s.space.Node(target.NodeID)
		if !ok {
			results[index] = StatusBadNodeIdUnknown
			continue
		}
		if target.AttributeID != AttributeValue {
			// Every other attribute this adapter exposes is read-only.
			results[index] = StatusBadNotWritable
			continue
		}
		if node.Class != NodeClassVariable || node.ItemID == "" {
			results[index] = StatusBadAttributeIDInvalid
			continue
		}
		if node.AccessRightsKnown && node.AccessLevel&AccessLevelCurrentWrite == 0 {
			results[index] = StatusBadNotWritable
			continue
		}
		item, ok := writeItemForNode(node, target.Value.Value)
		if !ok {
			results[index] = StatusBadTypeMismatch
			continue
		}
		items = append(items, item)
		positions = append(positions, index)
	}

	if len(items) > 0 {
		s.writeToSource(ctx, items, positions, results)
	}
	return WriteResponse{
		Header: ResponseHeader{
			Timestamp: now, RequestHandle: request.Header.RequestHandle,
			ServiceResult: StatusGood, AdditionalHeader: NullExtensionObject(),
		},
		Results:     results,
		Diagnostics: []DiagnosticInfo{},
	}, nil
}

func (s *DataAccessService) writeToSource(ctx context.Context, items []opcda.WriteItem, positions []int, results []StatusCode) {
	writeCtx, cancel := context.WithTimeout(ctx, s.limits.RequestTimeout)
	defer cancel()

	sourceResults, err := s.runtime.WriteBatch(writeCtx, items)
	if err != nil {
		status := statusForRuntimeError(err)
		for _, index := range positions {
			results[index] = status
		}
		return
	}
	if len(sourceResults) != len(items) {
		for _, index := range positions {
			results[index] = StatusBadInternalError
		}
		return
	}
	for offset, index := range positions {
		result := sourceResults[offset]
		if result.HRESULTPresent && result.HRESULT.Failed() {
			results[index] = StatusCodeForWriteError(result.HRESULT)
			continue
		}
		if result.ErrorCode != "" {
			results[index] = StatusBadTypeMismatch
			continue
		}
		results[index] = StatusGood
	}
}

// writeItemForNode builds the strictly typed DA write.
//
// When the node's canonical DataType is known, it decides the VARTYPE and the
// Variant must already carry exactly that Go type: nothing is widened,
// narrowed, or converted, which matches the DA core's strict typed write.
//
// OPC DA reports the canonical type in the AddItems result rather than in
// Browse, so a browsed item that has never been read has no known type. There
// the client's own Variant type decides the VARTYPE and the source is the
// authority: the DA core still writes strictly, and a server whose canonical
// type differs answers with a type-mismatch HRESULT that Table A.5 maps to
// Bad_TypeMismatch. Refusing locally instead would make every browsed item
// permanently unwritable over a restriction the adapter invented.
func writeItemForNode(node *Node, variant Variant) (opcda.WriteItem, bool) {
	if variant.IsNull() || variant.IsArray {
		return opcda.WriteItem{}, false
	}
	if node.DataTypeKnown {
		varType, ok := varTypeForDataTypeNode(node.DataType)
		if !ok || !variantMatchesVarType(variant, varType) {
			return opcda.WriteItem{}, false
		}
		return opcda.WriteItem{ItemID: node.ItemID, VarType: varType, Value: variant.Value}, true
	}
	varType, ok := varTypeForVariant(variant)
	if !ok {
		return opcda.WriteItem{}, false
	}
	return opcda.WriteItem{ItemID: node.ItemID, VarType: varType, Value: variant.Value}, true
}

// varTypeForVariant maps a client's Variant onto the VARTYPE that represents
// the same width, so nothing is widened or narrowed on the way to the source.
func varTypeForVariant(variant Variant) (opcda.DAVarType, bool) {
	switch variant.Type {
	case BuiltInBoolean:
		return opcda.VTBool, true
	case BuiltInSByte:
		return opcda.VTI1, true
	case BuiltInByte:
		return opcda.VTUI1, true
	case BuiltInInt16:
		return opcda.VTI2, true
	case BuiltInUInt16:
		return opcda.VTUI2, true
	case BuiltInInt32:
		return opcda.VTI4, true
	case BuiltInUInt32:
		return opcda.VTUI4, true
	case BuiltInInt64:
		return opcda.VTI8, true
	case BuiltInUInt64:
		return opcda.VTUI8, true
	case BuiltInFloat:
		return opcda.VTR4, true
	case BuiltInDouble:
		return opcda.VTR8, true
	case BuiltInString:
		return opcda.VTBSTR, true
	default:
		return 0, false
	}
}

// varTypeForDataTypeNode inverts the Part 8 DataType mapping for the types this
// adapter can write. The abstract base type is not writable: it means the
// source's canonical type had no mapping.
func varTypeForDataTypeNode(dataType NodeID) (opcda.DAVarType, bool) {
	if dataType.Namespace != 0 || dataType.Type != NodeIDTypeNumeric {
		return 0, false
	}
	switch dataType.Numeric {
	case NodeIDBoolean:
		return opcda.VTBool, true
	case NodeIDSByte:
		return opcda.VTI1, true
	case NodeIDByte:
		return opcda.VTUI1, true
	case NodeIDInt16:
		return opcda.VTI2, true
	case NodeIDUInt16:
		return opcda.VTUI2, true
	case NodeIDInt32:
		return opcda.VTI4, true
	case NodeIDUInt32:
		return opcda.VTUI4, true
	case NodeIDInt64:
		return opcda.VTI8, true
	case NodeIDUInt64:
		return opcda.VTUI8, true
	case NodeIDFloat:
		return opcda.VTR4, true
	case NodeIDDouble:
		return opcda.VTR8, true
	case NodeIDString:
		return opcda.VTBSTR, true
	default:
		return 0, false
	}
}

func variantMatchesVarType(variant Variant, varType opcda.DAVarType) bool {
	expected, ok := DataTypeFor(varType)
	if !ok {
		return false
	}
	switch expected {
	case DataTypeBoolean:
		return variant.Type == BuiltInBoolean
	case DataTypeSByte:
		return variant.Type == BuiltInSByte
	case DataTypeByte:
		return variant.Type == BuiltInByte
	case DataTypeInt16:
		return variant.Type == BuiltInInt16
	case DataTypeUInt16:
		return variant.Type == BuiltInUInt16
	case DataTypeInt32:
		return variant.Type == BuiltInInt32
	case DataTypeUInt32:
		return variant.Type == BuiltInUInt32
	case DataTypeInt64:
		return variant.Type == BuiltInInt64
	case DataTypeUInt64:
		return variant.Type == BuiltInUInt64
	case DataTypeFloat:
		return variant.Type == BuiltInFloat
	case DataTypeDouble:
		return variant.Type == BuiltInDouble
	case DataTypeString:
		return variant.Type == BuiltInString
	default:
		return false
	}
}

// statusForRuntimeError maps a DA runtime failure onto a UA status. A source
// method failure keeps its HRESULT mapping; adapter errors map by their code.
func statusForRuntimeError(err error) StatusCode {
	if sourceError, ok := opcda.AsSourceError(err); ok {
		return StatusCodeForReadError(sourceError.HRESULT)
	}
	adapterError, ok := opcda.AsAdapterError(err)
	if !ok {
		return StatusBadInternalError
	}
	switch adapterError.Code {
	case opcda.CodeRuntimeUnavailable:
		return StatusBadNotConnected
	case opcda.CodeRuntimeDeadline:
		return StatusBadTimeout
	case opcda.CodeWriteDisabled:
		return StatusBadNotWritable
	case opcda.CodeQueueFull, opcda.CodeRequestLimitExceeded, opcda.CodeRegisteredItemLimit:
		return StatusBadTooManyOperations
	case opcda.CodeTypeMismatch, opcda.CodeInvalidValue:
		return StatusBadTypeMismatch
	case opcda.CodeUnsupportedVarType:
		return StatusBadDataTypeIDUnknown
	default:
		return StatusBadInternalError
	}
}
