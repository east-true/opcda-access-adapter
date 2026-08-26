package opcua

import (
	"crypto/rand"
	"fmt"
	"sync"
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

	// ended is closed when the session is terminated, so a request still in
	// flight learns of it instead of running on against a session that is
	// gone. OPC 10000-4 5.7.2 requires exactly that: "When a Session is
	// terminated, all outstanding requests on the Session are aborted and
	// Bad_SessionClosed StatusCodes are returned to the Client."
	ended chan struct{}
	// inFlight counts the requests this session has issued that have not been
	// answered yet. The clause terminates a session when "the Client fails to
	// issue a Service request… within the timeout period"; a request still
	// being served is one the client did issue, so it holds the session open.
	// Without this a held Publish would let a perfectly behaved client's
	// session expire underneath it.
	inFlight int
}

func (s *Session) expired(now time.Time) bool {
	// A request the server has not answered yet is a request the client did
	// issue, so the session is not idle however long the answer takes.
	if s.inFlight > 0 {
		return false
	}
	deadline := s.LastActivity.Add(time.Duration(s.Timeout) * time.Millisecond)
	return !now.Before(deadline)
}

// SessionInfo is an immutable snapshot of a session, taken under the registry's
// lock. Callers get one of these rather than a *Session: the session itself is
// shared mutable state and stays inside the registry, so there is no way for a
// caller to read a field while another connection is writing it.
type SessionInfo struct {
	ID                  NodeID
	AuthenticationToken NodeID
	ChannelID           uint32
	Name                string
	Timeout             float64
	Activated           bool
}

// Key identifies the session a subscription belongs to.
func (info SessionInfo) Key() string { return string(info.AuthenticationToken.Opaque) }

// snapshot copies a session's observable state. It is called with the lock
// held.
func (s *Session) snapshot() SessionInfo {
	return SessionInfo{
		ID:                  s.ID,
		AuthenticationToken: s.AuthenticationToken,
		ChannelID:           s.ChannelID,
		Name:                s.Name,
		Timeout:             s.Timeout,
		Activated:           s.Activated,
	}
}

// SessionRegistry issues and tracks sessions. It is safe for concurrent use: a
// listener serves every connection on its own goroutine and expires stale
// sessions from another, so every one of these methods can run at the same time
// as any other.
type SessionRegistry struct {
	limits SessionLimits

	mu       sync.Mutex
	sessions map[string]*Session
	nextID   uint32
	// onEnd is what the listener needs done when a session ends by any route.
	onEnd func(SessionInfo)
}

func NewSessionRegistry(limits SessionLimits) (*SessionRegistry, error) {
	if err := limits.validate(); err != nil {
		return nil, err
	}
	return &SessionRegistry{limits: limits, sessions: make(map[string]*Session)}, nil
}

func (r *SessionRegistry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sessions)
}

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

// SessionSecurity is what the nonce rule depends on: the channel's security
// mode, and whether this endpoint could ever ask a client to sign anything.
type SessionSecurity struct {
	Mode SecurityMode
	// AnonymousIdentityOnly reports that every UserTokenPolicy this endpoint
	// publishes is Anonymous, so no UserTokenSignature can ever be computed
	// against it. It is a separate fact from the security mode: OPC 10000-4
	// Table 101 gives a UserTokenSignature for SecurityMode None whose inputs
	// include the ClientNonce, so an unsecured channel alone does not make the
	// nonce inert.
	AnonymousIdentityOnly bool
}

// checkClientNonce applies OPC 10000-4 5.7.2's rule that the server shall check
// the client nonce length, returning Bad_NonceInvalid below 32 or above 128
// bytes.
//
// One deliberate deviation: an absent nonce is accepted when the SecurityMode
// is None *and* this endpoint publishes only the anonymous user token policy.
//
// Read literally the rule is unconditional, but no reference server enforces it
// that way. The OPC Foundation's own StandardServer skips the check entirely
// for an empty nonce at every security mode, enforces a configurable minimum
// and no maximum, and then discards the nonce with the comment "ignore nonce if
// security policy set to none". open62541's server skips the 32 to 128 check
// whenever the policy is None. Enforcing the text literally made this adapter
// stricter than the specification's own reference implementation and unusable
// with open62541, whose client sends no nonce on an unsecured channel.
//
// Both conditions are needed, and the second is easy to miss. 5.7.2 gives the
// ClientNonce exactly one job: the Server proves possession of its
// ApplicationInstanceCertificate in the response, and the same clause says the
// Server ignores certificates entirely when the securityPolicyUri is None. But
// Table 101's last row defines a UserTokenSignature *for SecurityMode None*
// over "ServerNonce | HASH(ServerCertificate) | ClientNonce", so a client
// authenticating with a certificate signs the nonce even on an unsecured
// channel. Only because this endpoint accepts nothing but an anonymous
// identity — ActivateSession refuses every other token — does no signature
// exist for the nonce to weaken.
//
// A nonce that is present is always checked against the full 32 to 128 range,
// and where the nonce does real work the rule is enforced exactly as written.
// Both are stricter than the Foundation's server, which enforces no maximum and
// accepts an absent nonce unconditionally.
func checkClientNonce(nonce []byte, security SessionSecurity) error {
	if len(nonce) == 0 && security.Mode == SecurityModeNone && security.AnonymousIdentityOnly {
		return nil
	}
	if len(nonce) < MinNonceBytes || len(nonce) > MaxNonceBytes {
		return uacpError(StatusBadNonceInvalid,
			"the client nonce is %d bytes; it must be between %d and %d",
			len(nonce), MinNonceBytes, MaxNonceBytes)
	}
	return nil
}

// Create issues a session bound to the given SecureChannel.
func (r *SessionRegistry) Create(channelID uint32, security SessionSecurity, request CreateSessionRequest, now time.Time) (SessionInfo, []byte, error) {
	if err := checkClientNonce(request.ClientNonce, security); err != nil {
		return SessionInfo{}, nil, err
	}

	// The authentication token is opaque and random so it cannot be guessed
	// from a session identifier a client has seen. It is generated before the
	// lock is taken, because the random source is not this registry's state.
	tokenBytes, err := randomBytes(32)
	if err != nil {
		return SessionInfo{}, nil, err
	}
	serverNonce, err := randomBytes(MinNonceBytes)
	if err != nil {
		return SessionInfo{}, nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.sessions) >= r.limits.MaxSessions {
		return SessionInfo{}, nil, uacpError(StatusBadTooManySessions,
			"the %d session limit is reached", r.limits.MaxSessions)
	}
	r.nextID++
	session := &Session{
		ended:               make(chan struct{}),
		ID:                  NumericNodeID(1, r.nextID),
		AuthenticationToken: NodeID{Namespace: 1, Type: NodeIDTypeOpaque, Opaque: tokenBytes},
		ChannelID:           channelID,
		Name:                request.SessionName,
		Timeout:             r.limits.reviseTimeout(request.RequestedSessionTimeout),
		CreatedAt:           now,
		LastActivity:        now,
	}
	r.sessions[string(tokenBytes)] = session
	return session.snapshot(), serverNonce, nil
}

// Lookup resolves a session by its authentication token and enforces the
// channel binding and the timeout.
func (r *SessionRegistry) Lookup(token NodeID, channelID uint32, now time.Time) (SessionInfo, error) {
	var ended []SessionInfo
	info, err := func() (SessionInfo, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		session, lookupErr := r.lookupLocked(token, channelID, now, &ended)
		if lookupErr != nil {
			return SessionInfo{}, lookupErr
		}
		session.LastActivity = now
		return session.snapshot(), nil
	}()
	r.notifyEnded(ended)
	return info, err
}

// lookupLocked is the shared resolution step. It returns the live session, so
// every caller must hold the lock for as long as it uses the result.
func (r *SessionRegistry) lookupLocked(token NodeID, channelID uint32, now time.Time, ended *[]SessionInfo) (*Session, error) {
	if token.Type != NodeIDTypeOpaque {
		return nil, uacpError(StatusBadSessionIDInvalid, "the authentication token is not a session token")
	}
	session, ok := r.sessions[string(token.Opaque)]
	if !ok {
		return nil, uacpError(StatusBadSessionIDInvalid, "the session is not known")
	}
	if session.expired(now) {
		if info, removed := r.terminateLocked(session); removed {
			*ended = append(*ended, info)
		}
		return nil, uacpError(StatusBadSessionClosed, "the session timed out")
	}
	// A token that leaked to another channel must not grant access.
	if session.ChannelID != channelID {
		return nil, uacpError(StatusBadSecureChannelIDInvalid,
			"the session belongs to another secure channel")
	}
	return session, nil
}

// Activate completes activation. The identity token must be anonymous: no other
// user token type is accepted, because none is implemented.
func (r *SessionRegistry) Activate(token NodeID, channelID uint32, request ActivateSessionRequest, anonymousPolicyID string, now time.Time) ([]byte, error) {
	if err := requireAnonymousIdentity(request.UserIdentityToken, anonymousPolicyID); err != nil {
		return nil, err
	}
	serverNonce, err := randomBytes(MinNonceBytes)
	if err != nil {
		return nil, err
	}

	// The session is resolved again under the lock rather than being passed in
	// by the caller: a caller holding a *Session across two calls is exactly
	// the shared-mutable-state hazard this registry exists to prevent.
	var ended []SessionInfo
	err = func() error {
		r.mu.Lock()
		defer r.mu.Unlock()
		session, lookupErr := r.lookupLocked(token, channelID, now, &ended)
		if lookupErr != nil {
			return lookupErr
		}
		session.Activated = true
		session.LastActivity = now
		return nil
	}()
	r.notifyEnded(ended)
	if err != nil {
		return nil, err
	}
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
	var ended []SessionInfo
	err := func() error {
		r.mu.Lock()
		defer r.mu.Unlock()
		session, lookupErr := r.lookupLocked(token, channelID, now, &ended)
		if lookupErr != nil {
			return lookupErr
		}
		if info, removed := r.terminateLocked(session); removed {
			ended = append(ended, info)
		}
		return nil
	}()
	r.notifyEnded(ended)
	return err
}

// ExpireStale removes sessions that have gone quiet past their revised timeout.
func (r *SessionRegistry) ExpireStale(now time.Time) int {
	var ended []SessionInfo
	func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, session := range r.sessions {
			if session.expired(now) {
				if info, removed := r.terminateLocked(session); removed {
					ended = append(ended, info)
				}
			}
		}
	}()
	r.notifyEnded(ended)
	return len(ended)
}

// terminateLocked ends a session. OPC 10000-4 5.7.2 defines termination as one
// operation with consequences beyond forgetting the session: "When a Session is
// terminated, all outstanding requests on the Session are aborted and
// Bad_SessionClosed StatusCodes are returned to the Client."
//
// Every route that ends a session goes through here — an explicit CloseSession,
// the timeout, and a lookup that finds an already-expired session — because a
// route that ends a session some other way is a route that skips one of those
// consequences. That is how DA groups came to be leaked on timeout: the
// release lived at the CloseSession call site rather than in the operation.
// It removes the session and wakes anything serving a request for it, and
// returns the snapshot the caller must hand to the end hook once the lock is
// released. The hook is deliberately not called here: releasing a session's
// subscriptions unsubscribes DA groups, which is a COM call on Windows, and
// running that under the registry lock would stall every other connection's
// session work for its duration.
func (r *SessionRegistry) terminateLocked(session *Session) (SessionInfo, bool) {
	key := string(session.AuthenticationToken.Opaque)
	if _, live := r.sessions[key]; !live {
		return SessionInfo{}, false
	}
	delete(r.sessions, key)
	close(session.ended)
	return session.snapshot(), true
}

// notifyEnded runs the end hook for sessions terminateLocked removed. It must
// be called with the lock released.
func (r *SessionRegistry) notifyEnded(ended []SessionInfo) {
	r.mu.Lock()
	hook := r.onEnd
	r.mu.Unlock()
	if hook == nil {
		return
	}
	for _, session := range ended {
		hook(session)
	}
}

// OnSessionEnd registers what must happen when a session ends, whichever route
// ended it. The listener uses it to release the session's subscriptions, so a
// closed session cannot leave DA groups open on the source.
func (r *SessionRegistry) OnSessionEnd(hook func(SessionInfo)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onEnd = hook
}

// Ended returns a channel closed when the session terminates, so a request in
// flight can abort instead of outliving the session it belongs to.
func (r *SessionRegistry) Ended(token NodeID) (<-chan struct{}, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok := r.sessions[string(token.Opaque)]
	if !ok {
		return nil, false
	}
	return session.ended, true
}

// BeginRequest marks a request as being served, holding the session open for as
// long as it takes. EndRequest must follow, which is why it is returned rather
// than left to the caller to remember.
func (r *SessionRegistry) BeginRequest(token NodeID) (release func(), ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, live := r.sessions[string(token.Opaque)]
	if !live {
		return nil, false
	}
	session.inFlight++
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if session.inFlight > 0 {
				session.inFlight--
			}
			// The request has been answered, so the idle clock restarts now
			// rather than when the request arrived.
			session.LastActivity = time.Now().UTC()
		})
	}, true
}
