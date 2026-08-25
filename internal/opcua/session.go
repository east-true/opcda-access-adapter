package opcua

import (
	"crypto/rand"
	"fmt"
	"time"
)

// Session services follow OPC 10000-4 clause 5.7: CreateSession (Table 15),
// ActivateSession (Table 17), and CloseSession (Table 19). Encoding identifiers
// come from the OPC Foundation NodeIds table.
const (
	CreateSessionRequestEncodingID    uint32 = 461
	CreateSessionResponseEncodingID   uint32 = 464
	ActivateSessionRequestEncodingID  uint32 = 467
	ActivateSessionResponseEncodingID uint32 = 470
	CloseSessionRequestEncodingID     uint32 = 473
	CloseSessionResponseEncodingID    uint32 = 476
	AnonymousIdentityTokenEncodingID  uint32 = 321
)

// Session status codes from the OPC Foundation StatusCode list.
const (
	StatusBadUserAccessDenied      StatusCode = 0x801F0000
	StatusBadIdentityTokenInvalid  StatusCode = 0x80200000
	StatusBadIdentityTokenRejected StatusCode = 0x80210000
	StatusBadNonceInvalid          StatusCode = 0x80240000
	StatusBadSessionIDInvalid      StatusCode = 0x80250000
	StatusBadSessionClosed         StatusCode = 0x80260000
	StatusBadSessionNotActivated   StatusCode = 0x80270000
	StatusBadTooManySessions       StatusCode = 0x80560000
)

// Nonce bounds from OPC 10000-4 Table 15: the client nonce shall be between 32
// and 128 bytes inclusive and the server shall check the length. The check is
// not conditioned on the security mode, so it applies here too.
const (
	MinNonceBytes = 32
	MaxNonceBytes = 128
)

// SignatureData is OPC 10000-4 Table 174.
type SignatureData struct {
	Algorithm string
	Signature []byte
}

// SignedSoftwareCertificate is OPC 10000-4 Table 175. CreateSession states the
// server array "shall be empty"; it is decoded for completeness only.
type SignedSoftwareCertificate struct {
	CertificateData []byte
	Signature       []byte
}

func (e *Encoder) WriteSignatureData(value SignatureData) {
	if value.Algorithm == "" {
		e.WriteNullString()
	} else {
		e.WriteString(value.Algorithm)
	}
	if len(value.Signature) == 0 {
		e.WriteNullByteString()
	} else {
		e.WriteByteString(value.Signature)
	}
}

func (d *Decoder) ReadSignatureData() (SignatureData, error) {
	var value SignatureData
	algorithm, isNull, err := d.ReadString()
	if err != nil {
		return SignatureData{}, err
	}
	if !isNull {
		value.Algorithm = algorithm
	}
	signature, isNull, err := d.ReadByteString()
	if err != nil {
		return SignatureData{}, err
	}
	if !isNull {
		value.Signature = signature
	}
	return value, nil
}

// CreateSessionRequest is OPC 10000-4 Table 15.
type CreateSessionRequest struct {
	Header                  RequestHeader
	ClientDescription       ApplicationDescription
	ServerURI               string
	EndpointURL             string
	SessionName             string
	ClientNonce             []byte
	ClientCertificate       []byte
	RequestedSessionTimeout float64
	MaxResponseMessageSize  uint32
}

type CreateSessionResponse struct {
	Header                     ResponseHeader
	SessionID                  NodeID
	AuthenticationToken        NodeID
	RevisedSessionTimeout      float64
	ServerNonce                []byte
	ServerCertificate          []byte
	ServerEndpoints            []EndpointDescription
	ServerSoftwareCertificates []SignedSoftwareCertificate
	ServerSignature            SignatureData
	MaxRequestMessageSize      uint32
}

// ActivateSessionRequest is OPC 10000-4 Table 17.
type ActivateSessionRequest struct {
	Header                     RequestHeader
	ClientSignature            SignatureData
	ClientSoftwareCertificates []SignedSoftwareCertificate
	LocaleIDs                  []string
	UserIdentityToken          ExtensionObject
	UserTokenSignature         SignatureData
}

type ActivateSessionResponse struct {
	Header      ResponseHeader
	ServerNonce []byte
	Results     []StatusCode
	Diagnostics []DiagnosticInfo
}

// CloseSessionRequest is OPC 10000-4 Table 19.
type CloseSessionRequest struct {
	Header              RequestHeader
	DeleteSubscriptions bool
}

type CloseSessionResponse struct {
	Header ResponseHeader
}

func (d *Decoder) readSoftwareCertificates() ([]SignedSoftwareCertificate, error) {
	// Each entry is at least two length prefixes.
	length, isNull, err := d.ReadArrayLength(8)
	if err != nil || isNull {
		return nil, err
	}
	values := make([]SignedSoftwareCertificate, 0, length)
	for index := 0; index < length; index++ {
		var value SignedSoftwareCertificate
		data, dataIsNull, readErr := d.ReadByteString()
		if readErr != nil {
			return nil, readErr
		}
		if !dataIsNull {
			value.CertificateData = data
		}
		signature, signatureIsNull, readErr := d.ReadByteString()
		if readErr != nil {
			return nil, readErr
		}
		if !signatureIsNull {
			value.Signature = signature
		}
		values = append(values, value)
	}
	return values, nil
}

func (d *Decoder) ReadCreateSessionRequest() (CreateSessionRequest, error) {
	var request CreateSessionRequest
	var err error
	if request.Header, err = d.ReadRequestHeader(); err != nil {
		return CreateSessionRequest{}, err
	}
	if request.ClientDescription, err = d.ReadApplicationDescription(); err != nil {
		return CreateSessionRequest{}, err
	}
	read := func(target *string) error {
		text, isNull, readErr := d.ReadString()
		if readErr != nil {
			return readErr
		}
		if !isNull {
			*target = text
		}
		return nil
	}
	if err = read(&request.ServerURI); err != nil {
		return CreateSessionRequest{}, err
	}
	if err = read(&request.EndpointURL); err != nil {
		return CreateSessionRequest{}, err
	}
	if err = read(&request.SessionName); err != nil {
		return CreateSessionRequest{}, err
	}
	nonce, isNull, err := d.ReadByteString()
	if err != nil {
		return CreateSessionRequest{}, err
	}
	if !isNull {
		request.ClientNonce = nonce
	}
	certificate, isNull, err := d.ReadByteString()
	if err != nil {
		return CreateSessionRequest{}, err
	}
	if !isNull {
		request.ClientCertificate = certificate
	}
	if request.RequestedSessionTimeout, err = d.ReadDouble(); err != nil {
		return CreateSessionRequest{}, err
	}
	if request.MaxResponseMessageSize, err = d.ReadUInt32(); err != nil {
		return CreateSessionRequest{}, err
	}
	return request, nil
}

func (e *Encoder) WriteCreateSessionRequest(request CreateSessionRequest) {
	e.WriteServiceTypeID(CreateSessionRequestEncodingID)
	e.WriteRequestHeader(request.Header)
	e.WriteApplicationDescription(request.ClientDescription)
	e.WriteString(request.ServerURI)
	e.WriteString(request.EndpointURL)
	e.WriteString(request.SessionName)
	e.WriteByteString(request.ClientNonce)
	if len(request.ClientCertificate) == 0 {
		e.WriteNullByteString()
	} else {
		e.WriteByteString(request.ClientCertificate)
	}
	e.WriteDouble(request.RequestedSessionTimeout)
	e.WriteUInt32(request.MaxResponseMessageSize)
}

func (e *Encoder) WriteCreateSessionResponse(response CreateSessionResponse) {
	e.WriteServiceTypeID(CreateSessionResponseEncodingID)
	e.WriteResponseHeader(response.Header)
	e.WriteNodeID(response.SessionID)
	e.WriteNodeID(response.AuthenticationToken)
	e.WriteDouble(response.RevisedSessionTimeout)
	e.WriteByteString(response.ServerNonce)
	if len(response.ServerCertificate) == 0 {
		e.WriteNullByteString()
	} else {
		e.WriteByteString(response.ServerCertificate)
	}
	e.WriteArrayLength(len(response.ServerEndpoints))
	for _, endpoint := range response.ServerEndpoints {
		e.WriteEndpointDescription(endpoint)
	}
	// Table 15: this array shall be empty.
	e.WriteArrayLength(0)
	e.WriteSignatureData(response.ServerSignature)
	e.WriteUInt32(response.MaxRequestMessageSize)
}

func (d *Decoder) ReadCreateSessionResponse() (CreateSessionResponse, error) {
	var response CreateSessionResponse
	var err error
	if response.Header, err = d.ReadResponseHeader(); err != nil {
		return CreateSessionResponse{}, err
	}
	if response.SessionID, err = d.ReadNodeID(); err != nil {
		return CreateSessionResponse{}, err
	}
	if response.AuthenticationToken, err = d.ReadNodeID(); err != nil {
		return CreateSessionResponse{}, err
	}
	if response.RevisedSessionTimeout, err = d.ReadDouble(); err != nil {
		return CreateSessionResponse{}, err
	}
	nonce, isNull, err := d.ReadByteString()
	if err != nil {
		return CreateSessionResponse{}, err
	}
	if !isNull {
		response.ServerNonce = nonce
	}
	certificate, isNull, err := d.ReadByteString()
	if err != nil {
		return CreateSessionResponse{}, err
	}
	if !isNull {
		response.ServerCertificate = certificate
	}
	length, endpointsNull, err := d.ReadArrayLength(32)
	if err != nil {
		return CreateSessionResponse{}, err
	}
	if !endpointsNull {
		response.ServerEndpoints = make([]EndpointDescription, 0, length)
		for index := 0; index < length; index++ {
			endpoint, endpointErr := d.ReadEndpointDescription()
			if endpointErr != nil {
				return CreateSessionResponse{}, endpointErr
			}
			response.ServerEndpoints = append(response.ServerEndpoints, endpoint)
		}
	}
	if response.ServerSoftwareCertificates, err = d.readSoftwareCertificates(); err != nil {
		return CreateSessionResponse{}, err
	}
	if response.ServerSignature, err = d.ReadSignatureData(); err != nil {
		return CreateSessionResponse{}, err
	}
	if response.MaxRequestMessageSize, err = d.ReadUInt32(); err != nil {
		return CreateSessionResponse{}, err
	}
	return response, nil
}

func (d *Decoder) ReadActivateSessionRequest() (ActivateSessionRequest, error) {
	var request ActivateSessionRequest
	var err error
	if request.Header, err = d.ReadRequestHeader(); err != nil {
		return ActivateSessionRequest{}, err
	}
	if request.ClientSignature, err = d.ReadSignatureData(); err != nil {
		return ActivateSessionRequest{}, err
	}
	if request.ClientSoftwareCertificates, err = d.readSoftwareCertificates(); err != nil {
		return ActivateSessionRequest{}, err
	}
	if request.LocaleIDs, err = d.readStringArray(); err != nil {
		return ActivateSessionRequest{}, err
	}
	if request.UserIdentityToken, err = d.ReadExtensionObject(); err != nil {
		return ActivateSessionRequest{}, err
	}
	if request.UserTokenSignature, err = d.ReadSignatureData(); err != nil {
		return ActivateSessionRequest{}, err
	}
	return request, nil
}

func (e *Encoder) WriteActivateSessionRequest(request ActivateSessionRequest) {
	e.WriteServiceTypeID(ActivateSessionRequestEncodingID)
	e.WriteRequestHeader(request.Header)
	e.WriteSignatureData(request.ClientSignature)
	e.WriteArrayLength(len(request.ClientSoftwareCertificates))
	for _, certificate := range request.ClientSoftwareCertificates {
		e.WriteByteString(certificate.CertificateData)
		e.WriteByteString(certificate.Signature)
	}
	e.writeStringArray(request.LocaleIDs)
	e.WriteExtensionObject(request.UserIdentityToken)
	e.WriteSignatureData(request.UserTokenSignature)
}

func (e *Encoder) WriteActivateSessionResponse(response ActivateSessionResponse) {
	e.WriteServiceTypeID(ActivateSessionResponseEncodingID)
	e.WriteResponseHeader(response.Header)
	e.WriteByteString(response.ServerNonce)
	e.WriteArrayLength(len(response.Results))
	for _, result := range response.Results {
		e.WriteStatusCode(result)
	}
	e.WriteArrayLength(len(response.Diagnostics))
	for _, diagnostic := range response.Diagnostics {
		e.WriteDiagnosticInfo(diagnostic)
	}
}

func (d *Decoder) ReadActivateSessionResponse() (ActivateSessionResponse, error) {
	var response ActivateSessionResponse
	var err error
	if response.Header, err = d.ReadResponseHeader(); err != nil {
		return ActivateSessionResponse{}, err
	}
	nonce, isNull, err := d.ReadByteString()
	if err != nil {
		return ActivateSessionResponse{}, err
	}
	if !isNull {
		response.ServerNonce = nonce
	}
	length, resultsNull, err := d.ReadArrayLength(4)
	if err != nil {
		return ActivateSessionResponse{}, err
	}
	if !resultsNull {
		response.Results = make([]StatusCode, 0, length)
		for index := 0; index < length; index++ {
			status, statusErr := d.ReadStatusCode()
			if statusErr != nil {
				return ActivateSessionResponse{}, statusErr
			}
			response.Results = append(response.Results, status)
		}
	}
	length, diagnosticsNull, err := d.ReadArrayLength(1)
	if err != nil {
		return ActivateSessionResponse{}, err
	}
	if !diagnosticsNull {
		response.Diagnostics = make([]DiagnosticInfo, 0, length)
		for index := 0; index < length; index++ {
			diagnostic, diagnosticErr := d.ReadDiagnosticInfo()
			if diagnosticErr != nil {
				return ActivateSessionResponse{}, diagnosticErr
			}
			response.Diagnostics = append(response.Diagnostics, diagnostic)
		}
	}
	return response, nil
}

func (d *Decoder) ReadCloseSessionRequest() (CloseSessionRequest, error) {
	header, err := d.ReadRequestHeader()
	if err != nil {
		return CloseSessionRequest{}, err
	}
	deleteSubscriptions, err := d.ReadBoolean()
	if err != nil {
		return CloseSessionRequest{}, err
	}
	return CloseSessionRequest{Header: header, DeleteSubscriptions: deleteSubscriptions}, nil
}

func (e *Encoder) WriteCloseSessionRequest(request CloseSessionRequest) {
	e.WriteServiceTypeID(CloseSessionRequestEncodingID)
	e.WriteRequestHeader(request.Header)
	e.WriteBoolean(request.DeleteSubscriptions)
}

func (e *Encoder) WriteCloseSessionResponse(response CloseSessionResponse) {
	e.WriteServiceTypeID(CloseSessionResponseEncodingID)
	e.WriteResponseHeader(response.Header)
}

// SessionLimits bounds session lifetime and count.
type SessionLimits struct {
	MinTimeout  time.Duration
	MaxTimeout  time.Duration
	MaxSessions int
}

func DefaultSessionLimits() SessionLimits {
	return SessionLimits{
		MinTimeout:  10 * time.Second,
		MaxTimeout:  10 * time.Minute,
		MaxSessions: 8,
	}
}

func (limits SessionLimits) validate() error {
	if limits.MinTimeout <= 0 || limits.MaxTimeout <= 0 || limits.MaxSessions <= 0 {
		return fmt.Errorf("all session limits must be positive")
	}
	if limits.MinTimeout > limits.MaxTimeout {
		return fmt.Errorf("minimum session timeout must not exceed the maximum")
	}
	return nil
}

func (limits SessionLimits) ValidateForConfiguration() error { return limits.validate() }

// reviseTimeout clamps the client's request. OPC 10000-4 Table 15 requires the
// revised value to be greater than zero.
func (limits SessionLimits) reviseTimeout(requested float64) float64 {
	requestedDuration := time.Duration(requested) * time.Millisecond
	if requested <= 0 || requestedDuration < limits.MinTimeout {
		requestedDuration = limits.MinTimeout
	}
	if requestedDuration > limits.MaxTimeout {
		requestedDuration = limits.MaxTimeout
	}
	return float64(requestedDuration / time.Millisecond)
}

// Session is one client session.
type Session struct {
	ID                  NodeID
	AuthenticationToken NodeID
	// ChannelID is the SecureChannel the session was created on. OPC 10000-4
	// Table 15 says the authentication token is used together with the
	// SecureChannelId to decide whether a client may use the session, so the
	// binding is kept and enforced.
	ChannelID    uint32
	Name         string
	Timeout      float64
	CreatedAt    time.Time
	LastActivity time.Time
	Activated    bool
}

func (s *Session) expired(now time.Time) bool {
	deadline := s.LastActivity.Add(time.Duration(s.Timeout) * time.Millisecond)
	return !now.Before(deadline)
}

// SessionRegistry issues and tracks sessions. It is not safe for concurrent
// use; the owning listener drives it from one goroutine.
type SessionRegistry struct {
	limits   SessionLimits
	sessions map[string]*Session
	nextID   uint32
}

func NewSessionRegistry(limits SessionLimits) (*SessionRegistry, error) {
	if err := limits.validate(); err != nil {
		return nil, err
	}
	return &SessionRegistry{limits: limits, sessions: make(map[string]*Session)}, nil
}

func (r *SessionRegistry) Count() int { return len(r.sessions) }

// randomBytes produces a cryptographically random value. A failure here is not
// recoverable: issuing a predictable session token would be worse than refusing
// the request.
func randomBytes(length int) ([]byte, error) {
	value := make([]byte, length)
	if _, err := rand.Read(value); err != nil {
		return nil, uacpError(StatusBadTcpInternalError, "could not generate a random value")
	}
	return value, nil
}

// Create issues a session bound to the given SecureChannel.
func (r *SessionRegistry) Create(channelID uint32, request CreateSessionRequest, now time.Time) (*Session, []byte, error) {
	// Table 15 and the Bad_NonceInvalid description: the server shall check the
	// client nonce length, and the rule is not conditioned on the security
	// mode.
	if len(request.ClientNonce) < MinNonceBytes || len(request.ClientNonce) > MaxNonceBytes {
		return nil, nil, uacpError(StatusBadNonceInvalid,
			"the client nonce is %d bytes; it must be between %d and %d",
			len(request.ClientNonce), MinNonceBytes, MaxNonceBytes)
	}
	if len(r.sessions) >= r.limits.MaxSessions {
		return nil, nil, uacpError(StatusBadTooManySessions,
			"the %d session limit is reached", r.limits.MaxSessions)
	}

	// The authentication token is opaque and random so it cannot be guessed
	// from a session identifier a client has seen.
	tokenBytes, err := randomBytes(32)
	if err != nil {
		return nil, nil, err
	}
	serverNonce, err := randomBytes(MinNonceBytes)
	if err != nil {
		return nil, nil, err
	}
	r.nextID++
	session := &Session{
		ID:                  NumericNodeID(1, r.nextID),
		AuthenticationToken: NodeID{Namespace: 1, Type: NodeIDTypeOpaque, Opaque: tokenBytes},
		ChannelID:           channelID,
		Name:                request.SessionName,
		Timeout:             r.limits.reviseTimeout(request.RequestedSessionTimeout),
		CreatedAt:           now,
		LastActivity:        now,
	}
	r.sessions[string(tokenBytes)] = session
	return session, serverNonce, nil
}

// Lookup resolves a session by its authentication token and enforces the
// channel binding and the timeout.
func (r *SessionRegistry) Lookup(token NodeID, channelID uint32, now time.Time) (*Session, error) {
	if token.Type != NodeIDTypeOpaque {
		return nil, uacpError(StatusBadSessionIDInvalid, "the authentication token is not a session token")
	}
	session, ok := r.sessions[string(token.Opaque)]
	if !ok {
		return nil, uacpError(StatusBadSessionIDInvalid, "the session is not known")
	}
	if session.expired(now) {
		delete(r.sessions, string(token.Opaque))
		return nil, uacpError(StatusBadSessionClosed, "the session timed out")
	}
	// A token that leaked to another channel must not grant access.
	if session.ChannelID != channelID {
		return nil, uacpError(StatusBadSecureChannelIDInvalid,
			"the session belongs to another secure channel")
	}
	session.LastActivity = now
	return session, nil
}

// Activate completes activation. The identity token must be anonymous: no other
// user token type is accepted, because none is implemented.
func (r *SessionRegistry) Activate(session *Session, request ActivateSessionRequest, anonymousPolicyID string, now time.Time) ([]byte, error) {
	if err := requireAnonymousIdentity(request.UserIdentityToken, anonymousPolicyID); err != nil {
		return nil, err
	}
	serverNonce, err := randomBytes(MinNonceBytes)
	if err != nil {
		return nil, err
	}
	session.Activated = true
	session.LastActivity = now
	return serverNonce, nil
}

// requireAnonymousIdentity implements OPC 10000-4 Table 17: "Null or empty user
// token shall always be interpreted as anonymous." Anything else is rejected,
// since only the anonymous policy is published.
func requireAnonymousIdentity(token ExtensionObject, anonymousPolicyID string) error {
	if token.TypeID.IsNull() && token.Encoding == ExtensionObjectNoBody {
		return nil
	}
	if token.TypeID.Namespace != 0 || token.TypeID.Type != NodeIDTypeNumeric ||
		token.TypeID.Numeric != AnonymousIdentityTokenEncodingID {
		return uacpError(StatusBadIdentityTokenInvalid,
			"only an anonymous user identity token is accepted")
	}
	if token.Encoding == ExtensionObjectNoBody {
		return nil
	}
	if token.Encoding != ExtensionObjectByteString {
		return uacpError(StatusBadIdentityTokenInvalid,
			"the user identity token body is not binary encoded")
	}
	decoder, err := NewDecoder(token.Body, DefaultBinaryLimits())
	if err != nil {
		return err
	}
	policyID, isNull, err := decoder.ReadString()
	if err != nil {
		return uacpError(StatusBadIdentityTokenInvalid, "the anonymous token could not be decoded")
	}
	if isNull {
		policyID = ""
	}
	// Table 187: a server providing a null or empty PolicyId shall accept null
	// or empty and treat them as equal.
	if policyID != anonymousPolicyID {
		return uacpError(StatusBadIdentityTokenRejected,
			"the user token policy is not one this endpoint publishes")
	}
	return nil
}

// Close removes a session.
func (r *SessionRegistry) Close(token NodeID, channelID uint32, now time.Time) error {
	session, err := r.Lookup(token, channelID, now)
	if err != nil {
		return err
	}
	delete(r.sessions, string(session.AuthenticationToken.Opaque))
	return nil
}

// ExpireStale removes sessions that have gone quiet past their revised timeout.
func (r *SessionRegistry) ExpireStale(now time.Time) int {
	removed := 0
	for key, session := range r.sessions {
		if session.expired(now) {
			delete(r.sessions, key)
			removed++
		}
	}
	return removed
}
