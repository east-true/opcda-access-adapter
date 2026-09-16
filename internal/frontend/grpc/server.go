package grpcfrontend

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"slices"
	"sync/atomic"
	"time"
	"unicode/utf8"

	opcdav1 "github.com/east-true/opcda-access-adapter/api/opcda/v1"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
)

type Config struct {
	MaxConcurrent       int
	MaxConcurrentStream uint32
	MaxReceiveBytes     int
	MaxSendBytes        int
	MaxMetadataBytes    uint32
	ConnectionTimeout   time.Duration
	MaxConnectionIdle   time.Duration
	MaxConnectionAge    time.Duration
	MaxConnectionGrace  time.Duration
	KeepaliveMinTime    time.Duration
	RequestDeadline     time.Duration
	MaxReadItems        int
	MaxWriteItems       int
	MaxBrowseEntries    int
	MaxBrowseDepth      int
	MaxItemIDBytes      int
	MaxItemProperties   int
	// MaxSubscribeItems bounds one Subscribe request. MaxSubscriptionStreams
	// bounds concurrent Subscribe streams; the DA core enforces its own
	// subscription ceiling independently.
	MaxSubscribeItems      int
	MaxSubscriptionStreams int
}

type Server struct {
	opcdav1.UnimplementedOPCDAAccessServer

	runtime       opcda.Runtime
	config        Config
	server        *grpcgo.Server
	requests      chan struct{}
	subscriptions chan struct{}
	listening     atomic.Bool
}

func New(runtime opcda.Runtime, config Config) *Server {
	defaults := opcda.DefaultLimits()
	if config.MaxConcurrent <= 0 {
		config.MaxConcurrent = 32
	}
	if config.MaxConcurrentStream == 0 {
		config.MaxConcurrentStream = 16
	}
	if config.MaxReceiveBytes <= 0 {
		config.MaxReceiveBytes = 1 << 20
	}
	if config.MaxSendBytes <= 0 {
		config.MaxSendBytes = 4 << 20
	}
	if config.MaxMetadataBytes == 0 {
		config.MaxMetadataBytes = 32 << 10
	}
	if config.ConnectionTimeout <= 0 {
		config.ConnectionTimeout = 5 * time.Second
	}
	if config.MaxConnectionIdle <= 0 {
		config.MaxConnectionIdle = 2 * time.Minute
	}
	if config.MaxConnectionAge <= 0 {
		config.MaxConnectionAge = 30 * time.Minute
	}
	if config.MaxConnectionGrace <= 0 {
		config.MaxConnectionGrace = 30 * time.Second
	}
	if config.KeepaliveMinTime <= 0 {
		config.KeepaliveMinTime = 30 * time.Second
	}
	if config.RequestDeadline <= 0 {
		config.RequestDeadline = 10 * time.Second
	}
	if config.MaxReadItems <= 0 {
		config.MaxReadItems = defaults.MaxReadItems
	}
	if config.MaxWriteItems <= 0 {
		config.MaxWriteItems = defaults.MaxWriteItems
	}
	if config.MaxBrowseEntries <= 0 {
		config.MaxBrowseEntries = defaults.MaxBrowseEntries
	}
	if config.MaxItemProperties <= 0 {
		config.MaxItemProperties = defaults.MaxItemProperties
	}
	if config.MaxBrowseDepth <= 0 {
		config.MaxBrowseDepth = defaults.MaxBrowseDepth
	}
	if config.MaxItemIDBytes <= 0 {
		config.MaxItemIDBytes = defaults.MaxItemIDBytes
	}
	if config.MaxSubscribeItems <= 0 {
		config.MaxSubscribeItems = defaults.MaxSubscriptionItems
	}
	if config.MaxSubscriptionStreams <= 0 {
		config.MaxSubscriptionStreams = defaults.MaxSubscriptions
	}

	s := &Server{
		runtime:       runtime,
		config:        config,
		requests:      make(chan struct{}, config.MaxConcurrent),
		subscriptions: make(chan struct{}, config.MaxSubscriptionStreams),
	}
	s.server = grpcgo.NewServer(
		grpcgo.MaxRecvMsgSize(config.MaxReceiveBytes),
		grpcgo.MaxSendMsgSize(config.MaxSendBytes),
		grpcgo.MaxConcurrentStreams(config.MaxConcurrentStream),
		grpcgo.MaxHeaderListSize(config.MaxMetadataBytes),
		grpcgo.ConnectionTimeout(config.ConnectionTimeout),
		grpcgo.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle:     config.MaxConnectionIdle,
			MaxConnectionAge:      config.MaxConnectionAge,
			MaxConnectionAgeGrace: config.MaxConnectionGrace,
		}),
		grpcgo.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             config.KeepaliveMinTime,
			PermitWithoutStream: false,
		}),
		grpcgo.UnaryInterceptor(s.boundUnary),
	)
	opcdav1.RegisterOPCDAAccessServer(s.server, s)
	return s
}

func (s *Server) Serve(listener net.Listener) error {
	s.listening.Store(true)
	err := s.server.Serve(listener)
	s.listening.Store(false)
	// Having been stopped is not a failure to serve. gRPC reports it this way
	// when Stop arrives before Serve does, which is what a shutdown racing a
	// just-started listener looks like.
	if errors.Is(err, grpcgo.ErrServerStopped) {
		return nil
	}
	return err
}

func (s *Server) Stop() {
	s.listening.Store(false)
	s.server.Stop()
}

// GracefulStop stops the server without cutting a call off, and without
// hanging on one: when ctx expires the server is stopped hard instead.
//
// That bound rests on handlers honouring cancellation, because a hard Stop
// cancels in-flight RPCs rather than forcing their handlers to return. Every
// handler here reaches the DA runtime, which is bounded by its own request
// deadline and COM watchdog, so the assumption holds -- but it is an
// assumption, and a handler that ignored its context would hold shutdown open
// whatever context this was given.
func (s *Server) GracefulStop(ctx context.Context) error {
	s.listening.Store(false)
	done := make(chan struct{})
	go func() {
		s.server.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		s.server.Stop()
		<-done
		return ctx.Err()
	}
}

func (s *Server) boundUnary(
	ctx context.Context,
	request any,
	_ *grpcgo.UnaryServerInfo,
	handler grpcgo.UnaryHandler,
) (any, error) {
	select {
	case s.requests <- struct{}{}:
		defer func() { <-s.requests }()
	default:
		return nil, grpcOperationError(codes.ResourceExhausted, "adapter", string(opcda.CodeQueueFull), "too many concurrent gRPC requests", "", nil)
	}
	ctx, cancel := context.WithTimeout(ctx, s.config.RequestDeadline)
	defer cancel()
	return handler(ctx, request)
}

func (s *Server) Status(ctx context.Context, _ *opcdav1.DAStatusRequest) (*opcdav1.DAStatusResponse, error) {
	runtimeStatus := s.runtime.Status(ctx)
	response := &opcdav1.DAStatusResponse{
		RuntimeState: string(runtimeStatus.State),
		Source: &opcdav1.DASource{
			ProgId:               runtimeStatus.Source.ProgID,
			Clsid:                runtimeStatus.Source.CLSID,
			ConnectionGeneration: runtimeStatus.ConnectionGeneration,
		},
		Capabilities: &opcdav1.DACapabilities{
			Browse:     runtimeStatus.Capabilities.Browse,
			Read:       runtimeStatus.Capabilities.Read,
			Write:      runtimeStatus.Capabilities.Write,
			Subscribe:  runtimeStatus.Capabilities.Subscribe,
			Properties: runtimeStatus.Capabilities.Properties,
		},
		WriteEnabled: runtimeStatus.WriteEnabled,
		Runtime: &opcdav1.DARuntimeStatus{
			QueueDepth:        uint32(max(runtimeStatus.QueueDepth, 0)),
			ReconnectCount:    runtimeStatus.ReconnectCount,
			DegradedReason:    runtimeStatus.DegradedReason,
			SubscriptionCount: uint32(max(runtimeStatus.SubscriptionCount, 0)),
		},
		Frontend: &opcdav1.DAGRPCFrontendStatus{Listening: s.listening.Load()},
	}
	if runtimeStatus.LastSourceErrorSet {
		response.Source.LastError = &opcdav1.DASourceError{Operation: runtimeStatus.LastSourceError.Operation}
		if runtimeStatus.LastSourceError.HRESULTPresent {
			response.Source.LastError.Hresult = encodeHRESULT(runtimeStatus.LastSourceError.HRESULT)
		}
	}
	return response, nil
}

func (s *Server) Browse(ctx context.Context, request *opcdav1.DABrowseRequest) (*opcdav1.DABrowseResponse, error) {
	if request == nil {
		return nil, invalidRequest("Browse request is required")
	}
	if len(request.Path) > s.config.MaxBrowseDepth {
		return nil, requestLimit("Browse path depth limit exceeded")
	}
	path := make([]string, len(request.Path))
	for i, segment := range request.Path {
		if err := validateText(segment, "Browse path segment", s.config.MaxItemIDBytes); err != nil {
			return nil, err
		}
		path[i] = segment
	}
	filter := opcda.BrowseFilterAll
	switch request.Filter {
	case opcdav1.DABrowseFilter_DA_BROWSE_FILTER_ALL:
	case opcdav1.DABrowseFilter_DA_BROWSE_FILTER_BRANCH:
		filter = opcda.BrowseFilterBranch
	case opcdav1.DABrowseFilter_DA_BROWSE_FILTER_ITEM:
		filter = opcda.BrowseFilterItem
	default:
		return nil, invalidRequest("Browse filter is unknown")
	}
	result, err := s.runtime.Browse(ctx, opcda.BrowseRequest{Path: path, Filter: filter})
	if err != nil {
		return nil, mapOperationError(err)
	}
	if len(result.Entries) > s.config.MaxBrowseEntries {
		return nil, grpcAdapterError(codes.ResourceExhausted, opcda.CodeBrowseResultLimitExceeded, "Browse result limit exceeded")
	}
	if !slices.Equal(result.Path, path) {
		return nil, internalResultMismatch("runtime returned a Browse path that does not match the request")
	}
	response := &opcdav1.DABrowseResponse{Path: append([]string(nil), result.Path...), Entries: make([]*opcdav1.DABrowseEntry, len(result.Entries))}
	for i, entry := range result.Entries {
		if entry.Name == "" || !utf8.ValidString(entry.Name) {
			return nil, internalResultMismatch("runtime returned an invalid Browse entry name")
		}
		encoded := &opcdav1.DABrowseEntry{Name: entry.Name, AccessRights: encodeAccessRights(entry.AccessRights)}
		switch entry.Kind {
		case opcda.BrowseEntryBranch:
			// A branch carries an ItemID when the source names it through
			// GetItemID, which OPC 10000-8 A.3.1.2 asks a wrapper to obtain.
			// This used to refuse one outright, on the belief that a branch has
			// none; what the adapter must not do is invent one, and an ItemID
			// that arrives here came from the source.
			if entry.ItemID != nil {
				if err := validateText(string(*entry.ItemID), "Browse branch ItemID", s.config.MaxItemIDBytes); err != nil {
					return nil, internalResultMismatch("runtime returned an invalid Browse branch ItemID")
				}
				encoded.ItemId = string(*entry.ItemID)
				encoded.ItemIdPresent = true
			}
			encoded.Kind = opcdav1.DABrowseEntryKind_DA_BROWSE_ENTRY_KIND_BRANCH
		case opcda.BrowseEntryItem:
			if entry.ItemID == nil {
				return nil, internalResultMismatch("runtime omitted the exact ItemID for a Browse item")
			}
			if err := validateText(string(*entry.ItemID), "Browse ItemID", s.config.MaxItemIDBytes); err != nil {
				return nil, internalResultMismatch("runtime returned an invalid Browse ItemID")
			}
			encoded.Kind = opcdav1.DABrowseEntryKind_DA_BROWSE_ENTRY_KIND_ITEM
			encoded.ItemId = string(*entry.ItemID)
			encoded.ItemIdPresent = true
		default:
			return nil, internalResultMismatch("runtime returned an unknown Browse entry kind")
		}
		encoded.CanonicalDataType = encodeVarType(entry.CanonicalType)
		response.Entries[i] = encoded
	}
	return response, nil
}

func (s *Server) Read(ctx context.Context, request *opcdav1.DAReadRequest) (*opcdav1.DAReadResponse, error) {
	if request == nil {
		return nil, invalidRequest("Read request is required")
	}
	if request.Source != opcdav1.DADataSource_DA_DATA_SOURCE_DEVICE {
		return nil, invalidRequest("Read source must be DEVICE")
	}
	if len(request.Items) == 0 {
		return nil, invalidRequest("Read items must contain at least one entry")
	}
	if len(request.Items) > s.config.MaxReadItems {
		return nil, requestLimit("Read item limit exceeded")
	}
	items := make([]opcda.DAItemID, len(request.Items))
	for i, item := range request.Items {
		if item == nil {
			return nil, invalidRequest("Read item must not be null")
		}
		if err := validateText(item.ItemId, "Read ItemID", s.config.MaxItemIDBytes); err != nil {
			return nil, err
		}
		items[i] = opcda.DAItemID(item.ItemId)
	}
	results, err := s.runtime.ReadBatch(ctx, opcda.ReadRequest{Items: items, Source: opcda.DADataSourceDevice})
	if err != nil {
		return nil, mapOperationError(err)
	}
	if len(results) != len(items) {
		return nil, internalResultMismatch("runtime returned a different number of Read results")
	}
	response := &opcdav1.DAReadResponse{Results: make([]*opcdav1.DAReadResult, len(results))}
	for i, result := range results {
		if result.ItemID != items[i] {
			return nil, internalResultMismatch("runtime returned Read results out of request order")
		}
		encoded, err := encodeReadResult(result)
		if err != nil {
			return nil, err
		}
		response.Results[i] = encoded
	}
	return response, nil
}

func (s *Server) Write(ctx context.Context, request *opcdav1.DAWriteRequest) (*opcdav1.DAWriteResponse, error) {
	if !s.runtime.Status(ctx).WriteEnabled {
		return nil, grpcAdapterError(codes.PermissionDenied, opcda.CodeWriteDisabled, "Write is disabled")
	}
	if request == nil {
		return nil, invalidRequest("Write request is required")
	}
	if len(request.Items) == 0 {
		return nil, invalidRequest("Write items must contain at least one entry")
	}
	if len(request.Items) > s.config.MaxWriteItems {
		return nil, requestLimit("Write item limit exceeded")
	}
	items := make([]opcda.WriteItem, len(request.Items))
	for i, item := range request.Items {
		// A value arrives in one of two fields, and which one is the VARTYPE's
		// business rather than this check's: it asks only that something was
		// sent, and decodeWriteItemValue decides whether it was the right one.
		if item == nil || item.DataType == nil || (item.Value == nil && item.ArrayValue == nil) {
			return nil, invalidRequest("Write item, data type, and value are required")
		}
		if err := validateText(item.ItemId, "Write ItemID", s.config.MaxItemIDBytes); err != nil {
			return nil, err
		}
		varType, err := decodeWriteVarType(item.DataType)
		if err != nil {
			return nil, err
		}
		value, err := decodeWriteItemValue(varType, item)
		if err != nil {
			return nil, err
		}
		items[i] = opcda.WriteItem{ItemID: opcda.DAItemID(item.ItemId), VarType: varType, Value: value}
	}
	results, err := s.runtime.WriteBatch(ctx, items)
	if err != nil {
		return nil, mapOperationError(err)
	}
	if len(results) != len(items) {
		return nil, internalResultMismatch("runtime returned a different number of Write results")
	}
	response := &opcdav1.DAWriteResponse{Results: make([]*opcdav1.DAWriteResult, len(results))}
	for i, result := range results {
		if result.ItemID != items[i].ItemID || (!result.HRESULTPresent && result.ErrorCode == "") {
			return nil, internalResultMismatch("runtime returned Write results that do not match the request")
		}
		encoded := &opcdav1.DAWriteResult{ItemId: string(result.ItemID), ErrorCode: result.ErrorCode}
		if result.HRESULTPresent {
			encoded.Hresult = encodeHRESULT(result.HRESULT)
			encoded.Ok = result.ErrorCode == "" && result.HRESULT.Succeeded()
		}
		response.Results[i] = encoded
	}
	return response, nil
}

func encodeReadResult(result opcda.ReadResult) (*opcdav1.DAReadResult, error) {
	encoded := &opcdav1.DAReadResult{
		ItemId:            string(result.ItemID),
		DataType:          encodeVarType(result.VarType),
		CanonicalDataType: encodeVarType(result.CanonicalType),
		AccessRights:      encodeAccessRights(result.AccessRights),
		ErrorCode:         result.ErrorCode,
	}
	if result.HRESULTPresent {
		encoded.Hresult = encodeHRESULT(result.HRESULT)
	}
	if result.Value == nil {
		if result.ErrorCode == "" && (!result.HRESULTPresent || result.HRESULT.Succeeded()) {
			return nil, internalResultMismatch("runtime returned a successful Read result without a value")
		}
		return encoded, nil
	}
	if result.ErrorCode != "" || !result.HRESULTPresent || result.HRESULT.Failed() || result.VarType == nil || result.Value.ItemID != result.ItemID || result.Value.VarType != *result.VarType || result.Value.HRESULT != result.HRESULT {
		return nil, internalResultMismatch("runtime returned an inconsistent Read value")
	}
	encoded.QualityRaw = uint32(result.Value.QualityRaw)
	encoded.QualityPresent = true
	encoded.TimestampPresent = result.Value.TimestampPresent
	if result.Value.TimestampPresent {
		encoded.TimestampUnixSeconds = result.Value.Timestamp.Unix()
		encoded.TimestampNanos = int32(result.Value.Timestamp.Nanosecond())
	}
	scalar, array, err := encodeDAValue(result.Value.VarType, result.Value.Value)
	if err != nil {
		encoded.ErrorCode = string(opcda.CodeUnsupportedVarType)
		return encoded, nil
	}
	encoded.Value = scalar
	encoded.ArrayValue = array
	encoded.Ok = true
	return encoded, nil
}

func encodeScalar(varType opcda.DAVarType, value any) (*opcdav1.DAScalarValue, error) {
	if varType.IsArray() || varType.IsByRef() {
		return nil, fmt.Errorf("unsupported VARTYPE %s", varType)
	}
	encoded := &opcdav1.DAScalarValue{}
	switch varType.Base() {
	case opcda.VTEmpty, opcda.VTNull:
		if value != nil {
			return nil, fmt.Errorf("value does not match %s", varType)
		}
		encoded.Value = &opcdav1.DAScalarValue_EmptyOrNull{EmptyOrNull: true}
	case opcda.VTI1:
		v, ok := value.(int8)
		if !ok {
			return nil, fmt.Errorf("value does not match %s", varType)
		}
		encoded.Value = &opcdav1.DAScalarValue_I1Value{I1Value: int32(v)}
	case opcda.VTUI1:
		v, ok := value.(uint8)
		if !ok {
			return nil, fmt.Errorf("value does not match %s", varType)
		}
		encoded.Value = &opcdav1.DAScalarValue_Ui1Value{Ui1Value: uint32(v)}
	case opcda.VTI2:
		v, ok := value.(int16)
		if !ok {
			return nil, fmt.Errorf("value does not match %s", varType)
		}
		encoded.Value = &opcdav1.DAScalarValue_I2Value{I2Value: int32(v)}
	case opcda.VTUI2:
		v, ok := value.(uint16)
		if !ok {
			return nil, fmt.Errorf("value does not match %s", varType)
		}
		encoded.Value = &opcdav1.DAScalarValue_Ui2Value{Ui2Value: uint32(v)}
	case opcda.VTI4:
		v, ok := value.(int32)
		if !ok {
			return nil, fmt.Errorf("value does not match %s", varType)
		}
		encoded.Value = &opcdav1.DAScalarValue_I4Value{I4Value: v}
	case opcda.VTUI4:
		v, ok := value.(uint32)
		if !ok {
			return nil, fmt.Errorf("value does not match %s", varType)
		}
		encoded.Value = &opcdav1.DAScalarValue_Ui4Value{Ui4Value: v}
	case opcda.VTI8:
		v, ok := value.(int64)
		if !ok {
			return nil, fmt.Errorf("value does not match %s", varType)
		}
		encoded.Value = &opcdav1.DAScalarValue_I8Value{I8Value: v}
	case opcda.VTUI8:
		v, ok := value.(uint64)
		if !ok {
			return nil, fmt.Errorf("value does not match %s", varType)
		}
		encoded.Value = &opcdav1.DAScalarValue_Ui8Value{Ui8Value: v}
	case opcda.VTR4:
		v, ok := value.(float32)
		if !ok {
			return nil, fmt.Errorf("value does not match %s", varType)
		}
		encoded.Value = &opcdav1.DAScalarValue_R4Value{R4Value: v}
	case opcda.VTR8:
		v, ok := value.(float64)
		if !ok {
			return nil, fmt.Errorf("value does not match %s", varType)
		}
		encoded.Value = &opcdav1.DAScalarValue_R8Value{R8Value: v}
	case opcda.VTBool:
		v, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("value does not match %s", varType)
		}
		encoded.Value = &opcdav1.DAScalarValue_BoolValue{BoolValue: v}
	case opcda.VTBSTR:
		v, ok := value.(string)
		if !ok || !utf8.ValidString(v) {
			return nil, fmt.Errorf("value does not match %s", varType)
		}
		encoded.Value = &opcdav1.DAScalarValue_BstrValue{BstrValue: v}
	case opcda.VTError:
		v, ok := value.(int32)
		if !ok {
			return nil, fmt.Errorf("value does not match %s", varType)
		}
		encoded.Value = &opcdav1.DAScalarValue_ErrorValue{ErrorValue: v}
	case opcda.VTInt:
		v, ok := value.(int32)
		if !ok {
			return nil, fmt.Errorf("value does not match %s", varType)
		}
		encoded.Value = &opcdav1.DAScalarValue_IntValue{IntValue: v}
	case opcda.VTUInt:
		v, ok := value.(uint32)
		if !ok {
			return nil, fmt.Errorf("value does not match %s", varType)
		}
		encoded.Value = &opcdav1.DAScalarValue_UintValue{UintValue: v}
	default:
		return nil, fmt.Errorf("unsupported VARTYPE %s", varType)
	}
	return encoded, nil
}

func decodeWriteVarType(dataType *opcdav1.DAVarType) (opcda.DAVarType, error) {
	if dataType.Raw > math.MaxUint16 {
		return 0, grpcFrontendError(codes.InvalidArgument, opcda.CodeUnsupportedVarType, "Write VARTYPE exceeds 16 bits")
	}
	varType := opcda.DAVarType(dataType.Raw)
	if dataType.Name != "" && dataType.Name != varType.String() {
		return 0, grpcFrontendError(codes.InvalidArgument, opcda.CodeTypeMismatch, "Write VARTYPE name does not match its raw value")
	}
	if varType.IsByRef() {
		return 0, grpcAdapterError(codes.Unimplemented, opcda.CodeUnsupportedVarType, "byref Write values are unsupported")
	}
	return varType, nil
}

func decodeWriteValue(varType opcda.DAVarType, value *opcdav1.DAScalarValue) (any, error) {
	invalid := func() (any, error) {
		return nil, grpcFrontendError(codes.InvalidArgument, opcda.CodeTypeMismatch, "Write value field does not match the explicit VARTYPE")
	}
	switch varType.Base() {
	case opcda.VTEmpty, opcda.VTNull:
		v, ok := value.Value.(*opcdav1.DAScalarValue_EmptyOrNull)
		if !ok || !v.EmptyOrNull {
			return invalid()
		}
		return nil, nil
	case opcda.VTI1:
		v, ok := value.Value.(*opcdav1.DAScalarValue_I1Value)
		if !ok || v.I1Value < math.MinInt8 || v.I1Value > math.MaxInt8 {
			return invalid()
		}
		return int8(v.I1Value), nil
	case opcda.VTUI1:
		v, ok := value.Value.(*opcdav1.DAScalarValue_Ui1Value)
		if !ok || v.Ui1Value > math.MaxUint8 {
			return invalid()
		}
		return uint8(v.Ui1Value), nil
	case opcda.VTI2:
		v, ok := value.Value.(*opcdav1.DAScalarValue_I2Value)
		if !ok || v.I2Value < math.MinInt16 || v.I2Value > math.MaxInt16 {
			return invalid()
		}
		return int16(v.I2Value), nil
	case opcda.VTUI2:
		v, ok := value.Value.(*opcdav1.DAScalarValue_Ui2Value)
		if !ok || v.Ui2Value > math.MaxUint16 {
			return invalid()
		}
		return uint16(v.Ui2Value), nil
	case opcda.VTI4:
		v, ok := value.Value.(*opcdav1.DAScalarValue_I4Value)
		if !ok {
			return invalid()
		}
		return v.I4Value, nil
	case opcda.VTUI4:
		v, ok := value.Value.(*opcdav1.DAScalarValue_Ui4Value)
		if !ok {
			return invalid()
		}
		return v.Ui4Value, nil
	case opcda.VTI8:
		v, ok := value.Value.(*opcdav1.DAScalarValue_I8Value)
		if !ok {
			return invalid()
		}
		return v.I8Value, nil
	case opcda.VTUI8:
		v, ok := value.Value.(*opcdav1.DAScalarValue_Ui8Value)
		if !ok {
			return invalid()
		}
		return v.Ui8Value, nil
	case opcda.VTR4:
		v, ok := value.Value.(*opcdav1.DAScalarValue_R4Value)
		if !ok {
			return invalid()
		}
		return v.R4Value, nil
	case opcda.VTR8:
		v, ok := value.Value.(*opcdav1.DAScalarValue_R8Value)
		if !ok {
			return invalid()
		}
		return v.R8Value, nil
	case opcda.VTBool:
		v, ok := value.Value.(*opcdav1.DAScalarValue_BoolValue)
		if !ok {
			return invalid()
		}
		return v.BoolValue, nil
	case opcda.VTBSTR:
		v, ok := value.Value.(*opcdav1.DAScalarValue_BstrValue)
		if !ok || !utf8.ValidString(v.BstrValue) {
			return invalid()
		}
		return v.BstrValue, nil
	case opcda.VTError:
		v, ok := value.Value.(*opcdav1.DAScalarValue_ErrorValue)
		if !ok {
			return invalid()
		}
		return v.ErrorValue, nil
	case opcda.VTInt:
		v, ok := value.Value.(*opcdav1.DAScalarValue_IntValue)
		if !ok {
			return invalid()
		}
		return v.IntValue, nil
	case opcda.VTUInt:
		v, ok := value.Value.(*opcdav1.DAScalarValue_UintValue)
		if !ok {
			return invalid()
		}
		return v.UintValue, nil
	default:
		return nil, grpcAdapterError(codes.Unimplemented, opcda.CodeUnsupportedVarType, fmt.Sprintf("unsupported Write VARTYPE %s", varType))
	}
}

func encodeVarType(value *opcda.DAVarType) *opcdav1.DAVarType {
	if value == nil {
		return nil
	}
	info := value.Information()
	return &opcdav1.DAVarType{Raw: uint32(info.Code), Name: value.String(), Array: info.Array, Byref: info.ByRef}
}

func encodeAccessRights(value *opcda.DAAccessRights) *opcdav1.DAAccessRights {
	if value == nil {
		return nil
	}
	return &opcdav1.DAAccessRights{Raw: value.Raw, Read: value.Read, Write: value.Write}
}

func encodeHRESULT(value opcda.HRESULT) *opcdav1.DAHRESULT {
	representation := value.Representation()
	return &opcdav1.DAHRESULT{Value: representation.Value, Raw: uint32(representation.Value), Hex: representation.Hex}
}

func validateText(value, label string, maximum int) error {
	if value == "" {
		return invalidRequest(label + " must not be empty")
	}
	if !utf8.ValidString(value) {
		return invalidRequest(label + " must be valid UTF-8")
	}
	if len([]byte(value)) > maximum {
		return grpcFrontendError(codes.InvalidArgument, opcda.CodeItemIDTooLong, label+" exceeds configured limit")
	}
	for _, character := range value {
		if character == 0 {
			return invalidRequest(label + " must not contain NUL")
		}
	}
	return nil
}

func invalidRequest(message string) error {
	return grpcFrontendError(codes.InvalidArgument, opcda.CodeInvalidRequest, message)
}

func requestLimit(message string) error {
	return grpcFrontendError(codes.ResourceExhausted, opcda.CodeRequestLimitExceeded, message)
}

func internalResultMismatch(message string) error {
	return grpcAdapterError(codes.Internal, opcda.CodeInternalResultMismatch, message)
}

func grpcAdapterError(code codes.Code, adapterCode opcda.ErrorCode, message string) error {
	return grpcOperationError(code, "adapter", string(adapterCode), message, "", nil)
}

func grpcFrontendError(code codes.Code, errorCode opcda.ErrorCode, message string) error {
	return grpcOperationError(code, "frontend", string(errorCode), message, "", nil)
}

func grpcOperationError(code codes.Code, layer, errorCode, message, operation string, hresult *opcdav1.DAHRESULT) error {
	detail := &opcdav1.DAOperationError{Layer: layer, Code: errorCode, Message: message, Operation: operation, Hresult: hresult}
	base := status.New(code, message)
	withDetail, err := base.WithDetails(detail)
	if err != nil {
		return base.Err()
	}
	return withDetail.Err()
}

func mapOperationError(err error) error {
	if sourceError, ok := opcda.AsSourceError(err); ok {
		return grpcOperationError(codes.Unavailable, "source", string(opcda.CodeDAMethodFailed), sourceError.Operation+" failed", sourceError.Operation, encodeHRESULT(sourceError.HRESULT))
	}
	if adapterError, ok := opcda.AsAdapterError(err); ok {
		code := codes.Unavailable
		switch adapterError.Code {
		case opcda.CodeInvalidRequest, opcda.CodeItemIDTooLong, opcda.CodeInvalidValue, opcda.CodeBSTRTooLong, opcda.CodeTypeMismatch:
			code = codes.InvalidArgument
		case opcda.CodeRequestLimitExceeded, opcda.CodeBrowseResultLimitExceeded, opcda.CodeRegisteredItemLimit, opcda.CodeQueueFull:
			code = codes.ResourceExhausted
		case opcda.CodeWriteDisabled:
			code = codes.PermissionDenied
		case opcda.CodeRuntimeDeadline:
			code = codes.DeadlineExceeded
		case opcda.CodeBrowseUnsupported, opcda.CodePropertiesUnsupported, opcda.CodeUnsupportedVarType:
			code = codes.Unimplemented
		case opcda.CodeInternalResultMismatch:
			code = codes.Internal
		case opcda.CodeSubscriptionLimit:
			code = codes.ResourceExhausted
		case opcda.CodeSubscriptionNotFound:
			code = codes.NotFound
		case opcda.CodeSubscribeUnsupported:
			code = codes.Unimplemented
		case opcda.CodeSubscriptionInvalidated:
			// Aborted rather than Unavailable: the client must resubscribe
			// explicitly, and the adapter must not look transparently retryable.
			code = codes.Aborted
		}
		return grpcAdapterError(code, adapterError.Code, adapterError.Message)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return grpcAdapterError(codes.DeadlineExceeded, opcda.CodeRuntimeDeadline, "request deadline exceeded")
	}
	if errors.Is(err, context.Canceled) {
		return grpcOperationError(codes.Canceled, "adapter", string(opcda.CodeRuntimeDeadline), "request canceled", "", nil)
	}
	return grpcOperationError(codes.Internal, "adapter", string(opcda.CodeInternalError), "internal adapter error", "", nil)
}

// AvailableItemProperties reports which OPC DA properties a source offers for
// one item, in the source's own order.
func (s *Server) AvailableItemProperties(ctx context.Context, request *opcdav1.DAAvailableItemPropertiesRequest) (*opcdav1.DAAvailableItemPropertiesResponse, error) {
	if request == nil {
		return nil, invalidRequest("AvailableItemProperties request is required")
	}
	if err := validateText(request.ItemId, "AvailableItemProperties ItemID", s.config.MaxItemIDBytes); err != nil {
		return nil, err
	}
	available, err := s.runtime.AvailableItemProperties(ctx, request.ItemId)
	if err != nil {
		return nil, mapOperationError(err)
	}
	response := &opcdav1.DAAvailableItemPropertiesResponse{
		ItemId:     request.ItemId,
		Properties: make([]*opcdav1.DAAvailableProperty, len(available)),
	}
	for index, property := range available {
		varType := property.VarType
		response.Properties[index] = &opcdav1.DAAvailableProperty{
			PropertyId:  uint32(property.ID),
			Description: property.Description,
			DataType:    encodeVarType(&varType),
		}
	}
	return response, nil
}

// ItemProperties reads named OPC DA properties of one item. Results match the
// requested identifiers in size and order.
func (s *Server) ItemProperties(ctx context.Context, request *opcdav1.DAItemPropertiesRequest) (*opcdav1.DAItemPropertiesResponse, error) {
	if request == nil {
		return nil, invalidRequest("ItemProperties request is required")
	}
	if err := validateText(request.ItemId, "ItemProperties ItemID", s.config.MaxItemIDBytes); err != nil {
		return nil, err
	}
	if len(request.PropertyIds) == 0 {
		return nil, invalidRequest("ItemProperties propertyIds must contain at least one entry")
	}
	if len(request.PropertyIds) > s.config.MaxItemProperties {
		return nil, requestLimit("ItemProperties property limit exceeded")
	}
	properties := make([]opcda.PropertyID, len(request.PropertyIds))
	for index, id := range request.PropertyIds {
		properties[index] = opcda.PropertyID(id)
	}
	values, err := s.runtime.ItemProperties(ctx, opcda.ItemPropertiesRequest{
		ItemID: request.ItemId, Properties: properties,
	})
	if err != nil {
		return nil, mapOperationError(err)
	}
	if len(values) != len(properties) {
		return nil, internalResultMismatch("runtime returned a different number of item property results")
	}
	response := &opcdav1.DAItemPropertiesResponse{
		ItemId:  request.ItemId,
		Results: make([]*opcdav1.DAItemPropertyResult, len(values)),
	}
	for index, value := range values {
		if value.ID != properties[index] {
			return nil, internalResultMismatch("runtime returned item property results out of request order")
		}
		response.Results[index] = encodeItemPropertyResult(value)
	}
	return response, nil
}

func encodeItemPropertyResult(value opcda.ItemPropertyValue) *opcdav1.DAItemPropertyResult {
	encoded := &opcdav1.DAItemPropertyResult{
		PropertyId: uint32(value.ID),
		ErrorCode:  value.ErrorCode,
	}
	if value.HRESULTPresent {
		encoded.Hresult = encodeHRESULT(value.HRESULT)
	}
	if value.VarTypePresent {
		varType := value.VarType
		encoded.DataType = encodeVarType(&varType)
	}
	if !value.OK {
		// A property the source refused keeps its HRESULT and carries nothing
		// else. Nothing is substituted for it.
		return encoded
	}
	if !value.ValuePresent {
		// The source answered and gave no value. That is an answer, not a
		// failure, and reporting it as one would invent a refusal the source
		// never made.
		encoded.Ok = true
		return encoded
	}
	scalar, array, err := encodeDAValue(value.VarType, value.Value)
	if err != nil {
		encoded.ErrorCode = string(opcda.CodeUnsupportedVarType)
		return encoded
	}
	encoded.Value = scalar
	encoded.ArrayValue = array
	encoded.ValuePresent = true
	encoded.Ok = true
	return encoded
}

// encodeDAValue writes a DA value into the pair of fields a result carries:
// the scalar one, or the array one. Exactly one is set, and the result's
// data_type already says which, so a client that only understands scalars can
// tell an array apart from a value that was not carried.
func encodeDAValue(varType opcda.DAVarType, value any) (*opcdav1.DAScalarValue, *opcdav1.DAArrayValue, error) {
	if varType.IsArray() {
		array, ok := value.(opcda.DAArray)
		if !ok {
			return nil, nil, fmt.Errorf("a %s value did not carry an array", varType)
		}
		encoded, err := encodeArray(array)
		if err != nil {
			return nil, nil, err
		}
		return nil, encoded, nil
	}
	scalar, err := encodeScalar(varType, value)
	if err != nil {
		return nil, nil, err
	}
	return scalar, nil, nil
}

// encodeArray writes an array as its shape plus its elements in the published
// order: the last dimension varies fastest. The lower bound is the source's and
// is carried rather than normalised, because a client writing back to an index
// it read must reach the element it read.
//
// The elements are filled from backing slices rather than one message at a
// time. Every element of an array has the same type, so each protobuf message
// and each oneof wrapper can be taken from one allocation instead of two of
// their own -- which for a thousand-element array was two thousand allocations
// to carry a thousand numbers.
func encodeArray(array opcda.DAArray) (*opcdav1.DAArrayValue, error) {
	dimensions := make([]opcdav1.DADimension, len(array.Dimensions))
	dimensionPointers := make([]*opcdav1.DADimension, len(array.Dimensions))
	for index, dimension := range array.Dimensions {
		dimensions[index].LowerBound = dimension.LowerBound
		dimensions[index].Length = dimension.Length
		dimensionPointers[index] = &dimensions[index]
	}

	values := make([]opcdav1.DAScalarValue, len(array.Elements))
	elements := make([]*opcdav1.DAScalarValue, len(array.Elements))
	for index := range values {
		elements[index] = &values[index]
	}
	if err := fillArrayValues(array, values); err != nil {
		return nil, err
	}
	return &opcdav1.DAArrayValue{
		ElementDataType: encodeVarType(&array.ElementType),
		Dimensions:      dimensionPointers,
		Elements:        elements,
	}, nil
}

// fillArrayValues sets every element from one backing slice per element type.
// It answers exactly what encodeScalar answers for the same element, which the
// tests hold it to: two encoders that drift are two wire formats.
func fillArrayValues(array opcda.DAArray, values []opcdav1.DAScalarValue) error {
	count := len(array.Elements)
	switch array.ElementType.Base() {
	case opcda.VTEmpty, opcda.VTNull:
		wrappers := make([]opcdav1.DAScalarValue_EmptyOrNull, count)
		for index, element := range array.Elements {
			if element != nil {
				return arrayElementMismatch(index, array.ElementType)
			}
			wrappers[index].EmptyOrNull = true
			values[index].Value = &wrappers[index]
		}
		return nil
	case opcda.VTI1:
		wrappers := make([]opcdav1.DAScalarValue_I1Value, count)
		return fillArrayElements(array, values, func(value *opcdav1.DAScalarValue, index int, element int8) bool {
			wrappers[index].I1Value = int32(element)
			value.Value = &wrappers[index]
			return true
		})
	case opcda.VTUI1:
		wrappers := make([]opcdav1.DAScalarValue_Ui1Value, count)
		return fillArrayElements(array, values, func(value *opcdav1.DAScalarValue, index int, element uint8) bool {
			wrappers[index].Ui1Value = uint32(element)
			value.Value = &wrappers[index]
			return true
		})
	case opcda.VTI2:
		wrappers := make([]opcdav1.DAScalarValue_I2Value, count)
		return fillArrayElements(array, values, func(value *opcdav1.DAScalarValue, index int, element int16) bool {
			wrappers[index].I2Value = int32(element)
			value.Value = &wrappers[index]
			return true
		})
	case opcda.VTUI2:
		wrappers := make([]opcdav1.DAScalarValue_Ui2Value, count)
		return fillArrayElements(array, values, func(value *opcdav1.DAScalarValue, index int, element uint16) bool {
			wrappers[index].Ui2Value = uint32(element)
			value.Value = &wrappers[index]
			return true
		})
	case opcda.VTI4:
		wrappers := make([]opcdav1.DAScalarValue_I4Value, count)
		return fillArrayElements(array, values, func(value *opcdav1.DAScalarValue, index int, element int32) bool {
			wrappers[index].I4Value = element
			value.Value = &wrappers[index]
			return true
		})
	case opcda.VTUI4:
		wrappers := make([]opcdav1.DAScalarValue_Ui4Value, count)
		return fillArrayElements(array, values, func(value *opcdav1.DAScalarValue, index int, element uint32) bool {
			wrappers[index].Ui4Value = element
			value.Value = &wrappers[index]
			return true
		})
	case opcda.VTI8:
		wrappers := make([]opcdav1.DAScalarValue_I8Value, count)
		return fillArrayElements(array, values, func(value *opcdav1.DAScalarValue, index int, element int64) bool {
			wrappers[index].I8Value = element
			value.Value = &wrappers[index]
			return true
		})
	case opcda.VTUI8:
		wrappers := make([]opcdav1.DAScalarValue_Ui8Value, count)
		return fillArrayElements(array, values, func(value *opcdav1.DAScalarValue, index int, element uint64) bool {
			wrappers[index].Ui8Value = element
			value.Value = &wrappers[index]
			return true
		})
	case opcda.VTR4:
		wrappers := make([]opcdav1.DAScalarValue_R4Value, count)
		return fillArrayElements(array, values, func(value *opcdav1.DAScalarValue, index int, element float32) bool {
			wrappers[index].R4Value = element
			value.Value = &wrappers[index]
			return true
		})
	case opcda.VTR8:
		wrappers := make([]opcdav1.DAScalarValue_R8Value, count)
		return fillArrayElements(array, values, func(value *opcdav1.DAScalarValue, index int, element float64) bool {
			wrappers[index].R8Value = element
			value.Value = &wrappers[index]
			return true
		})
	case opcda.VTBool:
		wrappers := make([]opcdav1.DAScalarValue_BoolValue, count)
		return fillArrayElements(array, values, func(value *opcdav1.DAScalarValue, index int, element bool) bool {
			wrappers[index].BoolValue = element
			value.Value = &wrappers[index]
			return true
		})
	case opcda.VTBSTR:
		wrappers := make([]opcdav1.DAScalarValue_BstrValue, count)
		return fillArrayElements(array, values, func(value *opcdav1.DAScalarValue, index int, element string) bool {
			if !utf8.ValidString(element) {
				return false
			}
			wrappers[index].BstrValue = element
			value.Value = &wrappers[index]
			return true
		})
	case opcda.VTError:
		wrappers := make([]opcdav1.DAScalarValue_ErrorValue, count)
		return fillArrayElements(array, values, func(value *opcdav1.DAScalarValue, index int, element int32) bool {
			wrappers[index].ErrorValue = element
			value.Value = &wrappers[index]
			return true
		})
	case opcda.VTInt:
		wrappers := make([]opcdav1.DAScalarValue_IntValue, count)
		return fillArrayElements(array, values, func(value *opcdav1.DAScalarValue, index int, element int32) bool {
			wrappers[index].IntValue = element
			value.Value = &wrappers[index]
			return true
		})
	case opcda.VTUInt:
		wrappers := make([]opcdav1.DAScalarValue_UintValue, count)
		return fillArrayElements(array, values, func(value *opcdav1.DAScalarValue, index int, element uint32) bool {
			wrappers[index].UintValue = element
			value.Value = &wrappers[index]
			return true
		})
	default:
		return fmt.Errorf("unsupported VARTYPE %s", array.ElementType)
	}
}

// fillArrayElements asserts every element to the one Go type its VARTYPE
// produces and hands it to the setter. An element of another type is refused
// with the index, because a client told only that an element is wrong has to
// guess which of a thousand it was.
func fillArrayElements[E any](array opcda.DAArray, values []opcdav1.DAScalarValue,
	set func(*opcdav1.DAScalarValue, int, E) bool) error {
	for index, element := range array.Elements {
		typed, ok := element.(E)
		if !ok {
			return arrayElementMismatch(index, array.ElementType)
		}
		if !set(&values[index], index, typed) {
			return arrayElementMismatch(index, array.ElementType)
		}
	}
	return nil
}

func arrayElementMismatch(index int, elementType opcda.DAVarType) error {
	return fmt.Errorf("array element %d: value does not match %s", index, elementType)
}

// decodeArray reads the shape a client supplied for an array Write. It is the
// same description a Read publishes, so a client can send back what it was
// given; the adapter never infers a shape, and a message whose elements and
// dimensions do not describe each other is refused rather than reshaped.
func decodeArray(varType opcda.DAVarType, supplied *opcdav1.DAArrayValue) (opcda.DAArray, error) {
	if supplied == nil {
		return opcda.DAArray{}, invalidRequest(
			fmt.Sprintf("a %s Write must carry an array value", varType))
	}
	elementType := varType.Base()
	// The element type may be named, and then it has to agree. Leaving it out
	// is not a disagreement: data_type already carries it.
	if named := supplied.GetElementDataType(); named != nil {
		decoded, err := decodeWriteVarType(named)
		if err != nil {
			return opcda.DAArray{}, err
		}
		if decoded != elementType {
			return opcda.DAArray{}, grpcFrontendError(codes.InvalidArgument, opcda.CodeTypeMismatch,
				fmt.Sprintf("array element type %s does not match the declared %s", decoded, varType))
		}
	}

	array := opcda.DAArray{
		ElementType: elementType,
		Dimensions:  make([]opcda.DADimension, len(supplied.GetDimensions())),
		Elements:    make([]any, len(supplied.GetElements())),
	}
	for index, dimension := range supplied.GetDimensions() {
		array.Dimensions[index] = opcda.DADimension{
			LowerBound: dimension.GetLowerBound(),
			Length:     dimension.GetLength(),
		}
	}
	for index, element := range supplied.GetElements() {
		value, err := decodeWriteValue(elementType, element)
		if err != nil {
			return opcda.DAArray{}, fmt.Errorf("array element %d: %w", index, err)
		}
		array.Elements[index] = value
	}
	// The shape and the elements have to describe each other. What the runtime
	// is willing to carry is a separate question the DA layer's bounds answer,
	// so a client's mistake is not reported as an operator's limit.
	if err := array.MatchesItsShape(); err != nil {
		return opcda.DAArray{}, invalidRequest(err.Error())
	}
	return array, nil
}

// decodeWriteItemValue reads whichever of the two value fields the declared
// VARTYPE calls for, and refuses an item that supplies the other one or both.
// A request that names a scalar and carries an array has not said what it
// wants written, and guessing would write something the client did not ask for.
func decodeWriteItemValue(varType opcda.DAVarType, item *opcdav1.DAWriteItem) (any, error) {
	scalar, array := item.GetValue(), item.GetArrayValue()
	if scalar != nil && array != nil {
		return nil, invalidRequest("a Write item carries both a scalar and an array value")
	}
	if varType.IsArray() {
		if scalar != nil {
			return nil, invalidRequest(
				fmt.Sprintf("a %s Write carries a scalar value", varType))
		}
		return decodeArray(varType, array)
	}
	if array != nil {
		return nil, invalidRequest(
			fmt.Sprintf("a %s Write carries an array value", varType))
	}
	return decodeWriteValue(varType, scalar)
}
