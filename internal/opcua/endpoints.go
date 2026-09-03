package opcua

import (
	"fmt"
	"time"
)

// Discovery service encoding identifiers from the OPC Foundation NodeIds table.
const (
	GetEndpointsRequestEncodingID  uint32 = 428
	GetEndpointsResponseEncodingID uint32 = 431
)

// ApplicationType values from OPC 10000-4 Table 111.
type ApplicationType int32

const (
	ApplicationTypeServer          ApplicationType = 0
	ApplicationTypeClient          ApplicationType = 1
	ApplicationTypeClientAndServer ApplicationType = 2
	ApplicationTypeDiscoveryServer ApplicationType = 3
)

// UserTokenType values from OPC 10000-4 Table 193.
type UserTokenType int32

const (
	UserTokenTypeAnonymous   UserTokenType = 0
	UserTokenTypeUserName    UserTokenType = 1
	UserTokenTypeCertificate UserTokenType = 2
	UserTokenTypeIssuedToken UserTokenType = 3
)

// ApplicationDescription is OPC 10000-4 Table 109.
type ApplicationDescription struct {
	ApplicationURI      string
	ProductURI          string
	ApplicationName     LocalizedText
	ApplicationType     ApplicationType
	GatewayServerURI    string
	DiscoveryProfileURI string
	DiscoveryURLs       []string
}

// UserTokenPolicy is OPC 10000-4 Table 192.
type UserTokenPolicy struct {
	PolicyID          string
	TokenType         UserTokenType
	IssuedTokenType   string
	IssuerEndpointURL string
	SecurityPolicyURI string
}

// EndpointDescription is OPC 10000-4 Table 135.
type EndpointDescription struct {
	EndpointURL         string
	Server              ApplicationDescription
	ServerCertificate   []byte
	SecurityMode        SecurityMode
	SecurityPolicyURI   string
	UserIdentityTokens  []UserTokenPolicy
	TransportProfileURI string
	SecurityLevel       byte
}

// GetEndpointsRequest is OPC 10000-4 Table 5. The clause states the
// authenticationToken is always null and shall be ignored if provided, so this
// service needs no session.
type GetEndpointsRequest struct {
	Header      RequestHeader
	EndpointURL string
	LocaleIDs   []string
	ProfileURIs []string
}

type GetEndpointsResponse struct {
	Header    ResponseHeader
	Endpoints []EndpointDescription
}

func (e *Encoder) writeStringArray(values []string) {
	if values == nil {
		e.WriteNullArray()
		return
	}
	e.WriteArrayLength(len(values))
	for _, value := range values {
		e.WriteString(value)
	}
}

// readStringArray bounds the element count against the bytes remaining, since a
// String is at least its four byte length prefix.
func (d *Decoder) readStringArray() ([]string, error) {
	length, isNull, err := d.ReadArrayLength(4)
	if err != nil || isNull {
		return nil, err
	}
	values := make([]string, 0, length)
	for index := 0; index < length; index++ {
		value, valueIsNull, readErr := d.ReadString()
		if readErr != nil {
			return nil, readErr
		}
		if valueIsNull {
			value = ""
		}
		values = append(values, value)
	}
	return values, nil
}

func (e *Encoder) WriteApplicationDescription(value ApplicationDescription) {
	e.WriteString(value.ApplicationURI)
	e.WriteString(value.ProductURI)
	e.WriteLocalizedText(value.ApplicationName)
	e.WriteInt32(int32(value.ApplicationType))
	e.WriteOptionalString(value.GatewayServerURI)
	e.WriteOptionalString(value.DiscoveryProfileURI)
	e.writeStringArray(value.DiscoveryURLs)
}

func (d *Decoder) ReadApplicationDescription() (ApplicationDescription, error) {
	var value ApplicationDescription
	read := func(target *string) error {
		text, isNull, err := d.ReadString()
		if err != nil {
			return err
		}
		if !isNull {
			*target = text
		}
		return nil
	}
	if err := read(&value.ApplicationURI); err != nil {
		return ApplicationDescription{}, err
	}
	if err := read(&value.ProductURI); err != nil {
		return ApplicationDescription{}, err
	}
	name, err := d.ReadLocalizedText()
	if err != nil {
		return ApplicationDescription{}, err
	}
	value.ApplicationName = name
	applicationType, err := d.ReadInt32()
	if err != nil {
		return ApplicationDescription{}, err
	}
	if applicationType < int32(ApplicationTypeServer) || applicationType > int32(ApplicationTypeDiscoveryServer) {
		return ApplicationDescription{}, decodingError("ApplicationType %d is not defined", applicationType)
	}
	value.ApplicationType = ApplicationType(applicationType)
	if err := read(&value.GatewayServerURI); err != nil {
		return ApplicationDescription{}, err
	}
	if err := read(&value.DiscoveryProfileURI); err != nil {
		return ApplicationDescription{}, err
	}
	if value.DiscoveryURLs, err = d.readStringArray(); err != nil {
		return ApplicationDescription{}, err
	}
	return value, nil
}

func (e *Encoder) WriteUserTokenPolicy(value UserTokenPolicy) {
	e.WriteOptionalString(value.PolicyID)
	e.WriteInt32(int32(value.TokenType))
	e.WriteOptionalString(value.IssuedTokenType)
	e.WriteOptionalString(value.IssuerEndpointURL)
	e.WriteOptionalString(value.SecurityPolicyURI)
}

func (d *Decoder) ReadUserTokenPolicy() (UserTokenPolicy, error) {
	var value UserTokenPolicy
	read := func(target *string) error {
		text, isNull, err := d.ReadString()
		if err != nil {
			return err
		}
		if !isNull {
			*target = text
		}
		return nil
	}
	if err := read(&value.PolicyID); err != nil {
		return UserTokenPolicy{}, err
	}
	tokenType, err := d.ReadInt32()
	if err != nil {
		return UserTokenPolicy{}, err
	}
	if tokenType < int32(UserTokenTypeAnonymous) || tokenType > int32(UserTokenTypeIssuedToken) {
		return UserTokenPolicy{}, decodingError("UserTokenType %d is not defined", tokenType)
	}
	value.TokenType = UserTokenType(tokenType)
	if err := read(&value.IssuedTokenType); err != nil {
		return UserTokenPolicy{}, err
	}
	if err := read(&value.IssuerEndpointURL); err != nil {
		return UserTokenPolicy{}, err
	}
	if err := read(&value.SecurityPolicyURI); err != nil {
		return UserTokenPolicy{}, err
	}
	return value, nil
}

func (e *Encoder) WriteEndpointDescription(value EndpointDescription) {
	e.WriteString(value.EndpointURL)
	e.WriteApplicationDescription(value.Server)
	// With no certificate the field is null rather than an empty ByteString.
	if len(value.ServerCertificate) == 0 {
		e.WriteNullByteString()
	} else {
		e.WriteByteString(value.ServerCertificate)
	}
	e.WriteInt32(int32(value.SecurityMode))
	e.WriteString(value.SecurityPolicyURI)
	if value.UserIdentityTokens == nil {
		e.WriteNullArray()
	} else {
		e.WriteArrayLength(len(value.UserIdentityTokens))
		for _, policy := range value.UserIdentityTokens {
			e.WriteUserTokenPolicy(policy)
		}
	}
	e.WriteString(value.TransportProfileURI)
	e.WriteByteValue(value.SecurityLevel)
}

func (d *Decoder) ReadEndpointDescription() (EndpointDescription, error) {
	var value EndpointDescription
	read := func(target *string) error {
		text, isNull, err := d.ReadString()
		if err != nil {
			return err
		}
		if !isNull {
			*target = text
		}
		return nil
	}
	if err := read(&value.EndpointURL); err != nil {
		return EndpointDescription{}, err
	}
	server, err := d.ReadApplicationDescription()
	if err != nil {
		return EndpointDescription{}, err
	}
	value.Server = server
	certificate, isNull, err := d.ReadByteString()
	if err != nil {
		return EndpointDescription{}, err
	}
	if !isNull {
		value.ServerCertificate = certificate
	}
	securityMode, err := d.ReadInt32()
	if err != nil {
		return EndpointDescription{}, err
	}
	if securityMode < int32(SecurityModeInvalid) || securityMode > int32(SecurityModeSignAndEncrypt) {
		return EndpointDescription{}, decodingError("MessageSecurityMode %d is not defined", securityMode)
	}
	value.SecurityMode = SecurityMode(securityMode)
	if err := read(&value.SecurityPolicyURI); err != nil {
		return EndpointDescription{}, err
	}
	// A UserTokenPolicy is at least five length prefixes and an enumeration.
	length, tokensNull, err := d.ReadArrayLength(24)
	if err != nil {
		return EndpointDescription{}, err
	}
	if !tokensNull {
		value.UserIdentityTokens = make([]UserTokenPolicy, 0, length)
		for index := 0; index < length; index++ {
			policy, policyErr := d.ReadUserTokenPolicy()
			if policyErr != nil {
				return EndpointDescription{}, policyErr
			}
			value.UserIdentityTokens = append(value.UserIdentityTokens, policy)
		}
	}
	if err := read(&value.TransportProfileURI); err != nil {
		return EndpointDescription{}, err
	}
	if value.SecurityLevel, err = d.ReadByteValue(); err != nil {
		return EndpointDescription{}, err
	}
	return value, nil
}

func (e *Encoder) WriteGetEndpointsRequest(request GetEndpointsRequest) {
	e.WriteServiceTypeID(GetEndpointsRequestEncodingID)
	e.WriteRequestHeader(request.Header)
	e.WriteString(request.EndpointURL)
	e.writeStringArray(request.LocaleIDs)
	e.writeStringArray(request.ProfileURIs)
}

func (d *Decoder) ReadGetEndpointsRequest() (GetEndpointsRequest, error) {
	var request GetEndpointsRequest
	header, err := d.ReadRequestHeader()
	if err != nil {
		return GetEndpointsRequest{}, err
	}
	request.Header = header
	endpointURL, isNull, err := d.ReadString()
	if err != nil {
		return GetEndpointsRequest{}, err
	}
	if !isNull {
		request.EndpointURL = endpointURL
	}
	if request.LocaleIDs, err = d.readStringArray(); err != nil {
		return GetEndpointsRequest{}, err
	}
	if request.ProfileURIs, err = d.readStringArray(); err != nil {
		return GetEndpointsRequest{}, err
	}
	return request, nil
}

func (e *Encoder) WriteGetEndpointsResponse(response GetEndpointsResponse) {
	e.WriteServiceTypeID(GetEndpointsResponseEncodingID)
	e.WriteResponseHeader(response.Header)
	if response.Endpoints == nil {
		e.WriteNullArray()
		return
	}
	e.WriteArrayLength(len(response.Endpoints))
	for _, endpoint := range response.Endpoints {
		e.WriteEndpointDescription(endpoint)
	}
}

func (d *Decoder) ReadGetEndpointsResponse() (GetEndpointsResponse, error) {
	var response GetEndpointsResponse
	header, err := d.ReadResponseHeader()
	if err != nil {
		return GetEndpointsResponse{}, err
	}
	response.Header = header
	// An EndpointDescription is at least its fixed prefixes and enumerations.
	length, isNull, err := d.ReadArrayLength(32)
	if err != nil {
		return GetEndpointsResponse{}, err
	}
	if isNull {
		return response, nil
	}
	response.Endpoints = make([]EndpointDescription, 0, length)
	for index := 0; index < length; index++ {
		endpoint, endpointErr := d.ReadEndpointDescription()
		if endpointErr != nil {
			return GetEndpointsResponse{}, endpointErr
		}
		response.Endpoints = append(response.Endpoints, endpoint)
	}
	return response, nil
}

// EndpointConfig describes the one endpoint this adapter publishes.
//
// SecurityPolicyURI is required and is not defaulted. OPC 10000-7 governs
// profiles and does not list the URIs: its clause 1 says "the actual Profiles
// are maintained in an online database and accessible via
// https://profiles.opcfoundation.org/", and no other part carries them. With no
// pinned document to check a transcription against, the value is supplied by
// configuration rather than guessed from recollection -- a server that
// published a wrong policy URI would be unusable by a real client.
type EndpointConfig struct {
	EndpointURL         string
	ApplicationURI      string
	ProductURI          string
	ApplicationName     string
	SecurityPolicyURI   string
	TransportProfileURI string
	// AnonymousPolicyID names the anonymous UserTokenPolicy this endpoint
	// accepts. OPC 10000-4 allows a null or empty identifier.
	AnonymousPolicyID string
}

func (config EndpointConfig) validate() error {
	if config.EndpointURL == "" {
		return fmt.Errorf("an endpoint URL is required")
	}
	if config.ApplicationURI == "" {
		return fmt.Errorf("an application URI is required")
	}
	if config.SecurityPolicyURI == "" {
		return fmt.Errorf("a security policy URI is required; the known URIs are published " +
			"at https://profiles.opcfoundation.org/, which OPC 10000-7 clause 1 points to " +
			"rather than listing")
	}
	if len(config.SecurityPolicyURI) > MaxSecurityPolicyURIBytes {
		return fmt.Errorf("the security policy URI must not exceed %d bytes", MaxSecurityPolicyURIBytes)
	}
	return nil
}

func (config EndpointConfig) ValidateForConfiguration() error { return config.validate() }

// EndpointService answers GetEndpoints. It publishes exactly one endpoint,
// because the adapter serves one source over one listener.
type EndpointService struct {
	config   EndpointConfig
	endpoint EndpointDescription
}

func NewEndpointService(config EndpointConfig) (*EndpointService, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &EndpointService{
		config: config,
		endpoint: EndpointDescription{
			EndpointURL: config.EndpointURL,
			Server: ApplicationDescription{
				ApplicationURI:  config.ApplicationURI,
				ProductURI:      config.ProductURI,
				ApplicationName: LocalizedText{Text: config.ApplicationName},
				ApplicationType: ApplicationTypeServer,
				DiscoveryURLs:   []string{config.EndpointURL},
			},
			// No certificate is issued because only the unsecured policy is
			// served; the field is null rather than an empty ByteString.
			ServerCertificate: nil,
			SecurityMode:      SecurityModeNone,
			SecurityPolicyURI: config.SecurityPolicyURI,
			UserIdentityTokens: []UserTokenPolicy{{
				PolicyID:  config.AnonymousPolicyID,
				TokenType: UserTokenTypeAnonymous,
			}},
			TransportProfileURI: config.TransportProfileURI,
			// OPC 10000-4 Table 135: 0 means the description is not
			// recommended. An unsecured endpoint is exactly that.
			SecurityLevel: 0,
		},
	}, nil
}

// Endpoint reports the published description.
func (s *EndpointService) Endpoint() EndpointDescription { return s.endpoint }

// GetEndpoints answers the request. Table 5: all endpoints are returned if the
// profile list is empty, so a non-empty list that does not name this
// endpoint's transport profile returns nothing rather than everything.
func (s *EndpointService) GetEndpoints(request GetEndpointsRequest, now time.Time) GetEndpointsResponse {
	response := GetEndpointsResponse{
		Header: ResponseHeader{
			Timestamp:        now,
			RequestHandle:    request.Header.RequestHandle,
			ServiceResult:    StatusGood,
			AdditionalHeader: NullExtensionObject(),
		},
		Endpoints: []EndpointDescription{},
	}
	if len(request.ProfileURIs) > 0 && !matchesProfile(request.ProfileURIs, s.endpoint.TransportProfileURI) {
		return response
	}
	response.Endpoints = append(response.Endpoints, s.endpoint)
	return response
}

func matchesProfile(requested []string, transportProfileURI string) bool {
	for _, profile := range requested {
		if profile == transportProfileURI {
			return true
		}
	}
	return false
}

// AnonymousIdentityOnly reports that every UserTokenPolicy this endpoint
// publishes is Anonymous. It is what tells the session layer that no
// UserTokenSignature can ever be computed against this server, which OPC
// 10000-4 Table 101 shows is possible even with SecurityMode None.
func (s *EndpointService) AnonymousIdentityOnly() bool {
	for _, policy := range s.endpoint.UserIdentityTokens {
		if policy.TokenType != UserTokenTypeAnonymous {
			return false
		}
	}
	return true
}
