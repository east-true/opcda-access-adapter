package opcua

import "time"

// Service messages follow OPC 10000-6 5.2.9: "Messages are Structures encoded
// as sequence of bytes prefixed by the NodeId of ... the OPC UA Binary
// DataTypeEncoding defined for the Message." There is no length prefix, unlike
// an ExtensionObject. Enumerations are encoded as Int32 values (5.2.4).

// DataTypeEncoding identifiers for the services this slice implements, taken
// from the OPC Foundation NodeIds table for the UA namespace. They are numeric
// identifiers in namespace 0.
const (
	ServiceFaultEncodingID               uint32 = 397
	OpenSecureChannelRequestEncodingID   uint32 = 446
	OpenSecureChannelResponseEncodingID  uint32 = 449
	CloseSecureChannelRequestEncodingID  uint32 = 452
	CloseSecureChannelResponseEncodingID uint32 = 455
)

// WriteServiceTypeID writes the DataTypeEncoding NodeId that prefixes a service
// message body.
func (e *Encoder) WriteServiceTypeID(identifier uint32) {
	e.WriteNodeID(NumericNodeID(0, identifier))
}

// ReadServiceTypeID reads the prefix and reports the numeric identifier. A
// message whose TypeId is not a numeric identifier in namespace 0 is refused:
// every service encoding defined by the specification has that shape, so
// anything else is either a type this adapter does not implement or malformed.
func (d *Decoder) ReadServiceTypeID() (uint32, error) {
	typeID, err := d.ReadNodeID()
	if err != nil {
		return 0, err
	}
	if typeID.Namespace != 0 || typeID.Type != NodeIDTypeNumeric {
		return 0, uacpError(StatusBadServiceUnsupported,
			"service TypeId %s is not a standard numeric encoding identifier", typeID)
	}
	return typeID.Numeric, nil
}

// StatusBadServiceUnsupported is from the OPC Foundation StatusCode list.
const StatusBadServiceUnsupported StatusCode = 0x800B0000

// OpenSecureChannelRequest is the request body of OPC 10000-6 Table 64. The
// parameters carried in the security header rather than the body are not
// repeated here.
type OpenSecureChannelRequest struct {
	Header                RequestHeader
	ClientProtocolVersion uint32
	RequestType           TokenRequestType
	SecurityMode          SecurityMode
	ClientNonce           []byte
	RequestedLifetime     uint32
}

// OpenSecureChannelResponse is the response body of Table 64.
type OpenSecureChannelResponse struct {
	Header                ResponseHeader
	ServerProtocolVersion uint32
	SecurityToken         ChannelSecurityToken
	ServerNonce           []byte
}

// CloseSecureChannelRequest and CloseSecureChannelResponse carry only their
// headers.
type CloseSecureChannelRequest struct {
	Header RequestHeader
}

type CloseSecureChannelResponse struct {
	Header ResponseHeader
}

// ServiceFault is the response a server returns when a service cannot be
// carried out at all; OPC 10000-4 defines it as a bare ResponseHeader.
type ServiceFault struct {
	Header ResponseHeader
}

func (e *Encoder) WriteOpenSecureChannelRequest(request OpenSecureChannelRequest) {
	e.WriteServiceTypeID(OpenSecureChannelRequestEncodingID)
	e.WriteRequestHeader(request.Header)
	e.WriteUInt32(request.ClientProtocolVersion)
	e.WriteInt32(int32(request.RequestType))
	e.WriteInt32(int32(request.SecurityMode))
	e.WriteByteString(request.ClientNonce)
	e.WriteUInt32(request.RequestedLifetime)
}

// ReadOpenSecureChannelRequest decodes the body after its TypeId has already
// been read, so a caller can dispatch on the identifier first.
func (d *Decoder) ReadOpenSecureChannelRequest() (OpenSecureChannelRequest, error) {
	var request OpenSecureChannelRequest
	var err error
	if request.Header, err = d.ReadRequestHeader(); err != nil {
		return OpenSecureChannelRequest{}, err
	}
	if request.ClientProtocolVersion, err = d.ReadUInt32(); err != nil {
		return OpenSecureChannelRequest{}, err
	}
	requestType, err := d.ReadInt32()
	if err != nil {
		return OpenSecureChannelRequest{}, err
	}
	switch TokenRequestType(requestType) {
	case TokenRequestIssue, TokenRequestRenew:
		request.RequestType = TokenRequestType(requestType)
	default:
		return OpenSecureChannelRequest{}, uacpError(StatusBadSecurityChecksFailed,
			"SecurityTokenRequestType %d is not defined", requestType)
	}
	securityMode, err := d.ReadInt32()
	if err != nil {
		return OpenSecureChannelRequest{}, err
	}
	// A mode outside the enumeration is not silently reduced to Invalid; it is
	// refused, so a malformed field can never look like a deliberate choice.
	if securityMode < int32(SecurityModeInvalid) || securityMode > int32(SecurityModeSignAndEncrypt) {
		return OpenSecureChannelRequest{}, uacpError(StatusBadSecurityChecksFailed,
			"MessageSecurityMode %d is not defined", securityMode)
	}
	request.SecurityMode = SecurityMode(securityMode)
	nonce, isNull, err := d.ReadByteString()
	if err != nil {
		return OpenSecureChannelRequest{}, err
	}
	if !isNull {
		request.ClientNonce = nonce
	}
	if request.RequestedLifetime, err = d.ReadUInt32(); err != nil {
		return OpenSecureChannelRequest{}, err
	}
	return request, nil
}

func (e *Encoder) WriteOpenSecureChannelResponse(response OpenSecureChannelResponse) {
	e.WriteServiceTypeID(OpenSecureChannelResponseEncodingID)
	e.WriteResponseHeader(response.Header)
	e.WriteUInt32(response.ServerProtocolVersion)
	e.WriteUInt32(response.SecurityToken.SecureChannelID)
	e.WriteUInt32(response.SecurityToken.TokenID)
	e.WriteDateTime(response.SecurityToken.CreatedAt)
	e.WriteUInt32(response.SecurityToken.RevisedLifetime)
	e.WriteByteString(response.ServerNonce)
}

func (d *Decoder) ReadOpenSecureChannelResponse() (OpenSecureChannelResponse, error) {
	var response OpenSecureChannelResponse
	var err error
	if response.Header, err = d.ReadResponseHeader(); err != nil {
		return OpenSecureChannelResponse{}, err
	}
	if response.ServerProtocolVersion, err = d.ReadUInt32(); err != nil {
		return OpenSecureChannelResponse{}, err
	}
	if response.SecurityToken.SecureChannelID, err = d.ReadUInt32(); err != nil {
		return OpenSecureChannelResponse{}, err
	}
	if response.SecurityToken.TokenID, err = d.ReadUInt32(); err != nil {
		return OpenSecureChannelResponse{}, err
	}
	if response.SecurityToken.CreatedAt, err = d.ReadDateTime(); err != nil {
		return OpenSecureChannelResponse{}, err
	}
	if response.SecurityToken.RevisedLifetime, err = d.ReadUInt32(); err != nil {
		return OpenSecureChannelResponse{}, err
	}
	nonce, isNull, err := d.ReadByteString()
	if err != nil {
		return OpenSecureChannelResponse{}, err
	}
	if !isNull {
		response.ServerNonce = nonce
	}
	return response, nil
}

func (e *Encoder) WriteCloseSecureChannelRequest(request CloseSecureChannelRequest) {
	e.WriteServiceTypeID(CloseSecureChannelRequestEncodingID)
	e.WriteRequestHeader(request.Header)
}

func (d *Decoder) ReadCloseSecureChannelRequest() (CloseSecureChannelRequest, error) {
	header, err := d.ReadRequestHeader()
	if err != nil {
		return CloseSecureChannelRequest{}, err
	}
	return CloseSecureChannelRequest{Header: header}, nil
}

func (e *Encoder) WriteCloseSecureChannelResponse(response CloseSecureChannelResponse) {
	e.WriteServiceTypeID(CloseSecureChannelResponseEncodingID)
	e.WriteResponseHeader(response.Header)
}

func (e *Encoder) WriteServiceFault(fault ServiceFault) {
	e.WriteServiceTypeID(ServiceFaultEncodingID)
	e.WriteResponseHeader(fault.Header)
}

// NewServiceFault builds the fault a server returns for a request it cannot
// carry out, echoing the client's request handle so the failure can be matched
// to its request.
func NewServiceFault(requestHandle uint32, result StatusCode, now time.Time) ServiceFault {
	return ServiceFault{Header: ResponseHeader{
		Timestamp:        now,
		RequestHandle:    requestHandle,
		ServiceResult:    result,
		AdditionalHeader: NullExtensionObject(),
	}}
}

// ChannelService applies an OpenSecureChannel request to a registry. It is the
// seam between the decoded message and the token lifecycle, and it holds the
// rules that involve both.
type ChannelService struct {
	registry     *ChannelRegistry
	helloVersion uint32
}

func NewChannelService(registry *ChannelRegistry, helloVersion uint32) *ChannelService {
	return &ChannelService{registry: registry, helloVersion: helloVersion}
}

// OpenSecureChannel handles an Issue or Renew request.
//
// The protocol version is checked first: OPC 10000-6 6.7.4 requires the version
// in the request to match the one from the Hello and the channel to be closed
// with Bad_ProtocolVersionUnsupported if it does not.
//
// existingChannelID is the SecureChannelId from the message header, which a
// Renew carries and an Issue leaves at zero.
func (s *ChannelService) OpenSecureChannel(request OpenSecureChannelRequest, existingChannelID uint32, now time.Time) (OpenSecureChannelResponse, error) {
	if err := RequireProtocolVersion(s.helloVersion, request.ClientProtocolVersion); err != nil {
		return OpenSecureChannelResponse{}, err
	}

	var token ChannelSecurityToken
	switch request.RequestType {
	case TokenRequestIssue:
		channel, err := s.registry.Issue(request.SecurityMode, request.RequestedLifetime, now)
		if err != nil {
			return OpenSecureChannelResponse{}, err
		}
		token = channel.Token()
	case TokenRequestRenew:
		if existingChannelID == 0 {
			return OpenSecureChannelResponse{}, uacpError(StatusBadSecureChannelIDInvalid,
				"a renew request did not identify an existing secure channel")
		}
		channel, err := s.registry.Lookup(existingChannelID)
		if err != nil {
			return OpenSecureChannelResponse{}, err
		}
		// A renew must not change the channel's security mode; the clause ties
		// renewal to the channel that already exists.
		if request.SecurityMode != channel.SecurityMode() {
			return OpenSecureChannelResponse{}, uacpError(StatusBadSecurityModeRejected,
				"a renew request changed the security mode from %s to %s",
				channel.SecurityMode(), request.SecurityMode)
		}
		if token, err = s.registry.Renew(existingChannelID, request.RequestedLifetime, now); err != nil {
			return OpenSecureChannelResponse{}, err
		}
	default:
		return OpenSecureChannelResponse{}, uacpError(StatusBadSecurityChecksFailed,
			"SecurityTokenRequestType %d is not defined", int32(request.RequestType))
	}

	return OpenSecureChannelResponse{
		Header: ResponseHeader{
			Timestamp:        now,
			RequestHandle:    request.Header.RequestHandle,
			ServiceResult:    StatusGood,
			AdditionalHeader: NullExtensionObject(),
		},
		ServerProtocolVersion: s.helloVersion,
		SecurityToken:         token,
		// OPC 10000-6 6.7.4: with SecurityMode None the nonces are ignored and
		// should be set to null. A null nonce is written rather than an empty
		// one, so a client cannot read it as a zero-length random value.
		ServerNonce: nil,
	}, nil
}

// CloseSecureChannel removes the channel the request arrived on.
func (s *ChannelService) CloseSecureChannel(request CloseSecureChannelRequest, channelID uint32, now time.Time) (CloseSecureChannelResponse, error) {
	if err := s.registry.Close(channelID); err != nil {
		return CloseSecureChannelResponse{}, err
	}
	return CloseSecureChannelResponse{Header: ResponseHeader{
		Timestamp:        now,
		RequestHandle:    request.Header.RequestHandle,
		ServiceResult:    StatusGood,
		AdditionalHeader: NullExtensionObject(),
	}}, nil
}
