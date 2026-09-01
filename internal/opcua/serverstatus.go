package opcua

import "time"

// The Server object's status, from OPC 10000-5 8.3.2 and the OPC Foundation
// NodeSet. A generic UA client treats ServerStatus as the server's liveness
// signal: it reads State on a timer and tears the connection down when the
// read fails. A server that omits these nodes therefore looks dead to a
// conforming client no matter how healthy it is, which is exactly what a
// third-party client showed before they were added.
const (
	NodeIDServerStatus                     uint32 = 2256
	NodeIDServerStatusStartTime            uint32 = 2257
	NodeIDServerStatusCurrentTime          uint32 = 2258
	NodeIDServerStatusState                uint32 = 2259
	NodeIDServerStatusBuildInfo            uint32 = 2260
	NodeIDServerStatusBuildInfoProductName uint32 = 2261
	NodeIDServerStatusBuildInfoProductURI  uint32 = 2262
	NodeIDServerStatusBuildInfoManufacture uint32 = 2263
	NodeIDServerStatusBuildInfoSoftware    uint32 = 2264
	NodeIDServerStatusBuildInfoBuildNumber uint32 = 2265
	NodeIDServerStatusBuildInfoBuildDate   uint32 = 2266
	NodeIDServerServiceLevel               uint32 = 2267
	NodeIDServerAuditing                   uint32 = 2994

	// The ServerCapabilities Object and the children OPC 10000-5 Table 10
	// makes mandatory, plus the OperationLimits it makes optional. Part 4 7.9
	// sends a client here for the continuation point limit, and every other
	// bound this server enforces is worth the same treatment: a limit a client
	// can read is one it does not have to discover by being refused.
	NodeIDServerCapabilities                   uint32 = 2268
	NodeIDServerCapabilitiesProfileArray       uint32 = 2269
	NodeIDServerCapabilitiesLocaleIDArray      uint32 = 2271
	NodeIDServerCapabilitiesMinSampleRate      uint32 = 2272
	NodeIDServerCapabilitiesMaxBrowseCP        uint32 = 2735
	NodeIDServerCapabilitiesMaxQueryCP         uint32 = 2736
	NodeIDServerCapabilitiesMaxHistoryCP       uint32 = 2737
	NodeIDServerCapabilitiesSoftwareCerts      uint32 = 3704
	NodeIDServerCapabilitiesModellingRules     uint32 = 2996
	NodeIDServerCapabilitiesAggregateFunctions uint32 = 2997
	NodeIDServerCapabilitiesOperationLimits    uint32 = 11704
	NodeIDOperationLimitsMaxNodesPerRead       uint32 = 11705
	NodeIDOperationLimitsMaxNodesPerWrite      uint32 = 11707
	NodeIDOperationLimitsMaxNodesPerBrowse     uint32 = 11710

	// Type definitions and DataTypes these nodes refer to.
	NodeIDServerStatusType       uint32 = 2138
	NodeIDBuildInfoType          uint32 = 3051
	NodeIDBaseDataVariable       uint32 = 63
	NodeIDServerStatusDataType   uint32 = 862
	NodeIDBuildInfoDataType      uint32 = 338
	NodeIDServerStateDataType    uint32 = 852
	NodeIDUtcTimeDataType        uint32 = 294
	NodeIDServerCapabilitiesType uint32 = 2013
	NodeIDOperationLimitsType    uint32 = 11564
	NodeIDDurationDataType       uint32 = 290
	NodeIDLocaleIDDataType       uint32 = 295
	NodeIDSignedSoftwareCert     uint32 = 344
	NodeIDUInt16DataType         uint32 = 5
	NodeIDUInt32DataType         uint32 = 7
	NodeIDServerStatusEncodingID uint32 = 864
	NodeIDBuildInfoEncodingID    uint32 = 340
)

// ServerStateRunning is the ServerState enumeration value the NodeSet assigns
// to Running. The adapter is only ever Running while it is answering: a DA
// source that is disconnected is reported per item, never by claiming the UA
// server itself has failed.
const ServerStateRunning int32 = 0

// serviceLevelFullyOperational is the ServiceLevel value for a server that is
// fully operational. OPC 10000-4 6.6.2.4.2 uses 255 for that, and the adapter
// is not part of a redundant set where any lower value would mean something.
const serviceLevelFullyOperational byte = 255

// buildInfo is what this server reports about itself. Nothing here is derived
// from the DA source; it describes the adapter.
type buildInfo struct {
	productURI       string
	manufacturerName string
	productName      string
	softwareVersion  string
	buildNumber      string
	buildDate        time.Time
}

// encode writes the BuildInfo structure. The field order is the NodeSet's:
// ProductUri, ManufacturerName, ProductName, SoftwareVersion, BuildNumber,
// BuildDate — note that ProductUri comes before ManufacturerName and that
// ProductName is third, which is not the order the names suggest.
func (info buildInfo) encode(e *Encoder) {
	e.WriteString(info.productURI)
	e.WriteString(info.manufacturerName)
	e.WriteString(info.productName)
	e.WriteString(info.softwareVersion)
	e.WriteString(info.buildNumber)
	e.WriteDateTime(info.buildDate)
}

// extensionObject wraps an encoded structure body. A structure is carried in a
// Variant as an ExtensionObject naming its DefaultBinary encoding.
func (s *AddressSpace) extensionObject(encodingID uint32, write func(*Encoder)) (Variant, bool) {
	encoder, err := NewEncoder(s.binaryLimits)
	if err != nil {
		return NullVariant(), false
	}
	write(encoder)
	body, err := encoder.Bytes()
	if err != nil {
		return NullVariant(), false
	}
	return Variant{
		Type: BuiltInExtensionObject,
		Value: ExtensionObject{
			TypeID:   NumericNodeID(0, encodingID),
			Encoding: ExtensionObjectByteString,
			Body:     body,
		},
	}, true
}

// addServerStatusNodes builds the ServerStatus subtree under the Server object.
func (s *AddressSpace) addServerStatusNodes(server *Node) []*Node {
	info := buildInfo{
		productURI:       s.config.ProductURI,
		manufacturerName: s.config.ManufacturerName,
		productName:      s.config.ProductName,
		softwareVersion:  s.config.SoftwareVersion,
		buildNumber:      s.config.BuildNumber,
		buildDate:        s.config.BuildDate,
	}

	status := s.localVariable(NodeIDServerStatus, "ServerStatus",
		NodeIDServerStatusType, NodeIDServerStatusDataType,
		func(now time.Time) Variant {
			// The ServerStatusDataType field order is StartTime, CurrentTime,
			// State, BuildInfo, SecondsTillShutdown, ShutdownReason.
			value, ok := s.extensionObject(NodeIDServerStatusEncodingID, func(e *Encoder) {
				e.WriteDateTime(s.startTime)
				e.WriteDateTime(now)
				e.WriteInt32(ServerStateRunning)
				// A nested structure is written inline, not as its own
				// ExtensionObject: the field's type is the structure itself.
				info.encode(e)
				// SecondsTillShutdown is meaningful only while shutting down,
				// and ShutdownReason with it.
				e.WriteUInt32(0)
				e.WriteLocalizedText(LocalizedText{})
			})
			if !ok {
				return NullVariant()
			}
			return value
		})

	build := s.localVariable(NodeIDServerStatusBuildInfo, "BuildInfo",
		NodeIDBuildInfoType, NodeIDBuildInfoDataType,
		func(time.Time) Variant {
			value, ok := s.extensionObject(NodeIDBuildInfoEncodingID, info.encode)
			if !ok {
				return NullVariant()
			}
			return value
		})

	startTime := s.localVariable(NodeIDServerStatusStartTime, "StartTime",
		NodeIDBaseDataVariable, NodeIDUtcTimeDataType,
		staticLocalValue(Variant{Type: BuiltInDateTime, Value: s.startTime}))
	currentTime := s.localVariable(NodeIDServerStatusCurrentTime, "CurrentTime",
		NodeIDBaseDataVariable, NodeIDUtcTimeDataType,
		func(now time.Time) Variant { return Variant{Type: BuiltInDateTime, Value: now} })
	state := s.localVariable(NodeIDServerStatusState, "State",
		NodeIDBaseDataVariable, NodeIDServerStateDataType,
		staticLocalValue(Variant{Type: BuiltInInt32, Value: ServerStateRunning}))

	buildStrings := []struct {
		id    uint32
		name  string
		value string
	}{
		{NodeIDServerStatusBuildInfoProductURI, "ProductUri", info.productURI},
		{NodeIDServerStatusBuildInfoManufacture, "ManufacturerName", info.manufacturerName},
		{NodeIDServerStatusBuildInfoProductName, "ProductName", info.productName},
		{NodeIDServerStatusBuildInfoSoftware, "SoftwareVersion", info.softwareVersion},
		{NodeIDServerStatusBuildInfoBuildNumber, "BuildNumber", info.buildNumber},
	}
	buildChildren := make([]*Node, 0, len(buildStrings)+1)
	for _, field := range buildStrings {
		buildChildren = append(buildChildren, s.localVariable(field.id, field.name,
			NodeIDBaseDataVariable, NodeIDString,
			staticLocalValue(Variant{Type: BuiltInString, Value: field.value})))
	}
	buildChildren = append(buildChildren, s.localVariable(
		NodeIDServerStatusBuildInfoBuildDate, "BuildDate",
		NodeIDBaseDataVariable, NodeIDUtcTimeDataType,
		staticLocalValue(Variant{Type: BuiltInDateTime, Value: info.buildDate})))

	serviceLevel := s.localVariable(NodeIDServerServiceLevel, "ServiceLevel",
		NodeIDPropertyType, NodeIDByte,
		staticLocalValue(Variant{Type: BuiltInByte, Value: serviceLevelFullyOperational}))
	// The adapter emits no audit events, and OPC 10000-5 has this property say
	// so rather than leaving a client to discover it.
	auditing := s.localVariable(NodeIDServerAuditing, "Auditing",
		NodeIDPropertyType, NodeIDBoolean,
		staticLocalValue(Variant{Type: BuiltInBoolean, Value: false}))

	hasComponent := NumericNodeID(0, NodeIDHasComponent)
	hasProperty := NumericNodeID(0, NodeIDHasProperty)

	addForward(server, hasComponent, status)
	addInverse(status, hasComponent, server)
	addForward(server, hasProperty, serviceLevel)
	addInverse(serviceLevel, hasProperty, server)
	addForward(server, hasProperty, auditing)
	addInverse(auditing, hasProperty, server)

	for _, child := range []*Node{startTime, currentTime, state, build} {
		addForward(status, hasComponent, child)
		addInverse(child, hasComponent, status)
	}
	for _, child := range buildChildren {
		addForward(build, hasComponent, child)
		addInverse(child, hasComponent, build)
	}

	nodes := []*Node{status, build, startTime, currentTime, state, serviceLevel, auditing}
	return append(nodes, buildChildren...)
}

// localVariable builds one node the server answers for itself.
// addServerCapabilitiesNodes builds the ServerCapabilities Object and the
// children OPC 10000-5 Table 10 makes mandatory.
//
// Part 4 7.9 says a server "specif[ies] a maximum number of ContinuationPoints
// per Session in the ServerCapabilities Object defined in OPC 10000-5", and
// every other bound this server enforces deserves the same: a limit a client
// can read is one it does not have to discover by being refused.
func (s *AddressSpace) addServerCapabilitiesNodes(server *Node) []*Node {
	capabilities := &Node{
		ID:             NumericNodeID(0, NodeIDServerCapabilities),
		Class:          NodeClassObject,
		BrowseName:     QualifiedName{Namespace: 0, Name: "ServerCapabilities"},
		DisplayName:    LocalizedText{Text: "ServerCapabilities"},
		TypeDefinition: NumericNodeID(0, NodeIDServerCapabilitiesType),
	}

	constant := func(identifier uint32, name string, dataType uint32, value Variant) *Node {
		return s.localVariable(identifier, name, NodeIDPropertyType, dataType,
			func(time.Time) Variant { return value })
	}

	// ADR-0016 forbids claiming a profile this project has not been certified
	// against, so the array is empty. That is not a gap: an empty
	// ServerProfileArray says no profile is claimed, which is exactly true,
	// while naming one would be the claim the ADR forbids.
	profiles := constant(NodeIDServerCapabilitiesProfileArray, "ServerProfileArray",
		NodeIDString, Variant{Type: BuiltInString, IsArray: true, Value: []string{}})
	// This server serves no localised text of its own, so it offers no locale.
	locales := constant(NodeIDServerCapabilitiesLocaleIDArray, "LocaleIdArray",
		NodeIDLocaleIDDataType, Variant{Type: BuiltInString, IsArray: true, Value: []string{}})
	// The fastest publishing interval a subscription can be revised to.
	sampleRate := constant(NodeIDServerCapabilitiesMinSampleRate, "MinSupportedSampleRate",
		NodeIDDurationDataType, Variant{Type: BuiltInDouble,
			Value: float64(s.config.Limits.MinPublishingInterval / time.Millisecond)})
	browsePoints := constant(NodeIDServerCapabilitiesMaxBrowseCP, "MaxBrowseContinuationPoints",
		NodeIDUInt16DataType, Variant{Type: BuiltInUInt16,
			Value: uint16(s.config.Limits.MaxBrowseContinuationPoints)})
	// Query and HistoryRead are not implemented, and 10000-5 6.3.2 reads zero
	// as "the Server does not limit" -- so these say none are available rather
	// than none are imposed, which is what refusing both services means.
	queryPoints := constant(NodeIDServerCapabilitiesMaxQueryCP, "MaxQueryContinuationPoints",
		NodeIDUInt16DataType, Variant{Type: BuiltInUInt16, Value: uint16(0)})
	historyPoints := constant(NodeIDServerCapabilitiesMaxHistoryCP, "MaxHistoryContinuationPoints",
		NodeIDUInt16DataType, Variant{Type: BuiltInUInt16, Value: uint16(0)})
	// This server presents no software certificates, which is the same answer
	// its endpoint gives.
	certificates := constant(NodeIDServerCapabilitiesSoftwareCerts, "SoftwareCertificates",
		NodeIDSignedSoftwareCert, Variant{Type: BuiltInExtensionObject, IsArray: true,
			Value: []ExtensionObject{}})

	// Both are mandatory folders and both are empty: this server defines no
	// modelling rules of its own and computes no aggregates.
	modellingRules := folderNode(NumericNodeID(0, NodeIDServerCapabilitiesModellingRules), "ModellingRules")
	aggregates := folderNode(NumericNodeID(0, NodeIDServerCapabilitiesAggregateFunctions), "AggregateFunctions")

	limits := &Node{
		ID:             NumericNodeID(0, NodeIDServerCapabilitiesOperationLimits),
		Class:          NodeClassObject,
		BrowseName:     QualifiedName{Namespace: 0, Name: "OperationLimits"},
		DisplayName:    LocalizedText{Text: "OperationLimits"},
		TypeDefinition: NumericNodeID(0, NodeIDOperationLimitsType),
	}
	maxRead := constant(NodeIDOperationLimitsMaxNodesPerRead, "MaxNodesPerRead",
		NodeIDUInt32DataType, Variant{Type: BuiltInUInt32, Value: uint32(s.config.Limits.MaxNodesPerRead)})
	maxWrite := constant(NodeIDOperationLimitsMaxNodesPerWrite, "MaxNodesPerWrite",
		NodeIDUInt32DataType, Variant{Type: BuiltInUInt32, Value: uint32(s.config.Limits.MaxNodesPerWrite)})
	maxBrowse := constant(NodeIDOperationLimitsMaxNodesPerBrowse, "MaxNodesPerBrowse",
		NodeIDUInt32DataType, Variant{Type: BuiltInUInt32, Value: uint32(s.config.Limits.MaxNodesPerBrowse)})

	hasComponent := NumericNodeID(0, NodeIDHasComponent)
	hasProperty := NumericNodeID(0, NodeIDHasProperty)
	addForward(server, hasComponent, capabilities)
	addInverse(capabilities, hasComponent, server)
	for _, property := range []*Node{profiles, locales, sampleRate, browsePoints,
		queryPoints, historyPoints, certificates} {
		addForward(capabilities, hasProperty, property)
		addInverse(property, hasProperty, capabilities)
	}
	for _, child := range []*Node{modellingRules, aggregates, limits} {
		addForward(capabilities, hasComponent, child)
		addInverse(child, hasComponent, capabilities)
	}
	for _, property := range []*Node{maxRead, maxWrite, maxBrowse} {
		addForward(limits, hasProperty, property)
		addInverse(property, hasProperty, limits)
	}

	return []*Node{capabilities, profiles, locales, sampleRate, browsePoints,
		queryPoints, historyPoints, certificates, modellingRules, aggregates,
		limits, maxRead, maxWrite, maxBrowse}
}

func (s *AddressSpace) localVariable(identifier uint32, name string, typeDefinition, dataType uint32, value func(time.Time) Variant) *Node {
	return &Node{
		ID:             NumericNodeID(0, identifier),
		Class:          NodeClassVariable,
		BrowseName:     QualifiedName{Namespace: 0, Name: name},
		DisplayName:    LocalizedText{Text: name},
		TypeDefinition: NumericNodeID(0, typeDefinition),
		DataType:       NumericNodeID(0, dataType),
		DataTypeKnown:  true,
		ValueRank:      ValueRankScalar,
		// The server knows its own status exactly, so unlike a DA item these
		// carry a genuine access level rather than an assumed one.
		AccessLevel:       AccessLevelCurrentRead,
		AccessRightsKnown: true,
		LocalValue:        value,
	}
}
