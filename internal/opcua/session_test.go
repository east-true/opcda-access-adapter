package opcua

import (
	"bytes"
	"testing"
	"time"
)

func testClientNonce() []byte { return bytes.Repeat([]byte{7}, MinNonceBytes) }

func testCreateSessionRequest() CreateSessionRequest {
	return CreateSessionRequest{
		Header:                  serviceRequestHeader(),
		SessionName:             "test-session",
		ClientNonce:             testClientNonce(),
		RequestedSessionTimeout: 60_000,
	}
}

func newTestSessionRegistry(t *testing.T, limits SessionLimits) *SessionRegistry {
	t.Helper()
	registry, err := NewSessionRegistry(limits)
	if err != nil {
		t.Fatalf("NewSessionRegistry: %v", err)
	}
	return registry
}

// The encoding identifiers come from the OPC Foundation NodeIds table.
func TestSessionEncodingIdentifiers(t *testing.T) {
	cases := map[uint32]uint32{
		CreateSessionRequestEncodingID:    461,
		CreateSessionResponseEncodingID:   464,
		ActivateSessionRequestEncodingID:  467,
		ActivateSessionResponseEncodingID: 470,
		CloseSessionRequestEncodingID:     473,
		CloseSessionResponseEncodingID:    476,
		AnonymousIdentityTokenEncodingID:  321,
	}
	for got, want := range cases {
		if got != want {
			t.Fatalf("encoding id = %d, want %d", got, want)
		}
	}
}

func TestCreateSessionRoundTrip(t *testing.T) {
	limits := DefaultBinaryLimits()
	request := testCreateSessionRequest()
	request.EndpointURL = "opc.tcp://127.0.0.1:4840"
	request.ClientDescription = ApplicationDescription{
		ApplicationURI:  "urn:client",
		ApplicationName: LocalizedText{Text: "client"},
		ApplicationType: ApplicationTypeClient,
	}

	encoder := newTestEncoder(t, limits)
	encoder.WriteCreateSessionRequest(request)
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	decoder := newTestDecoder(t, encoded, limits)
	identifier, err := decoder.ReadServiceTypeID()
	if err != nil || identifier != CreateSessionRequestEncodingID {
		t.Fatalf("TypeId = %d, %v", identifier, err)
	}
	decoded, err := decoder.ReadCreateSessionRequest()
	if err != nil {
		t.Fatal(err)
	}
	if decoded.SessionName != "test-session" || decoded.RequestedSessionTimeout != 60_000 ||
		!bytes.Equal(decoded.ClientNonce, testClientNonce()) ||
		decoded.ClientDescription.ApplicationURI != "urn:client" {
		t.Fatalf("request = %+v", decoded)
	}
	if !decoder.Done() {
		t.Fatalf("%d bytes left", decoder.Remaining())
	}
}

func TestActivateAndCloseSessionRoundTrip(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoder := newTestEncoder(t, limits)
	encoder.WriteActivateSessionRequest(ActivateSessionRequest{
		Header:            serviceRequestHeader(),
		LocaleIDs:         []string{"en"},
		UserIdentityToken: NullExtensionObject(),
	})
	encoder.WriteCloseSessionRequest(CloseSessionRequest{
		Header: serviceRequestHeader(), DeleteSubscriptions: true,
	})
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	decoder := newTestDecoder(t, encoded, limits)
	if _, err := decoder.ReadServiceTypeID(); err != nil {
		t.Fatal(err)
	}
	activate, err := decoder.ReadActivateSessionRequest()
	if err != nil {
		t.Fatal(err)
	}
	if len(activate.LocaleIDs) != 1 || activate.LocaleIDs[0] != "en" {
		t.Fatalf("locale ids = %v", activate.LocaleIDs)
	}
	if _, err := decoder.ReadServiceTypeID(); err != nil {
		t.Fatal(err)
	}
	closeRequest, err := decoder.ReadCloseSessionRequest()
	if err != nil {
		t.Fatal(err)
	}
	if !closeRequest.DeleteSubscriptions {
		t.Fatal("deleteSubscriptions was lost")
	}
}

// OPC 10000-4 5.7.2: the server shall check the client nonce length and return
// Bad_NonceInvalid outside 32 to 128 bytes.
func TestCreateSessionChecksTheClientNonceLength(t *testing.T) {
	registry := newTestSessionRegistry(t, DefaultSessionLimits())
	for _, length := range []int{1, MinNonceBytes - 1, MaxNonceBytes + 1} {
		request := testCreateSessionRequest()
		request.ClientNonce = bytes.Repeat([]byte{1}, length)
		_, _, err := registry.Create(1, SecurityModeNone, request, channelEpoch)
		if err == nil {
			t.Fatalf("a %d byte nonce was accepted", length)
		}
		if got := codecStatus(t, err); got != StatusBadNonceInvalid {
			t.Fatalf("status = %s, want Bad_NonceInvalid", got.Hex())
		}
	}
	for _, length := range []int{MinNonceBytes, 64, MaxNonceBytes} {
		request := testCreateSessionRequest()
		request.ClientNonce = bytes.Repeat([]byte{1}, length)
		if _, _, err := registry.Create(1, SecurityModeNone, request, channelEpoch); err != nil {
			t.Fatalf("a %d byte nonce was refused: %v", length, err)
		}
	}
}

// With SecurityMode None an absent nonce is accepted, and under any other mode
// it is not. open62541 sends no nonce at all when the channel is unsecured, and
// under None there is no signature for the nonce to take part in, so accepting
// its absence costs no security. Where the nonce does work, the rule stands.
func TestCreateSessionAcceptsAnAbsentNonceOnlyWhenUnsecured(t *testing.T) {
	registry := newTestSessionRegistry(t, DefaultSessionLimits())
	request := testCreateSessionRequest()
	request.ClientNonce = nil
	if _, _, err := registry.Create(1, SecurityModeNone, request, channelEpoch); err != nil {
		t.Fatalf("an unsecured channel refused an absent nonce: %v", err)
	}

	for _, mode := range []SecurityMode{SecurityModeSign, SecurityModeSignAndEncrypt} {
		request := testCreateSessionRequest()
		request.ClientNonce = nil
		_, _, err := registry.Create(1, mode, request, channelEpoch)
		if err == nil {
			t.Fatalf("%s accepted an absent nonce", mode)
		}
		if got := codecStatus(t, err); got != StatusBadNonceInvalid {
			t.Fatalf("status = %s, want Bad_NonceInvalid", got.Hex())
		}
	}
}

func TestCreateSessionIssuesAnUnguessableToken(t *testing.T) {
	registry := newTestSessionRegistry(t, DefaultSessionLimits())
	first, firstNonce, err := registry.Create(1, SecurityModeNone, testCreateSessionRequest(), channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	second, secondNonce, err := registry.Create(1, SecurityModeNone, testCreateSessionRequest(), channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	// The token is opaque and random, so it cannot be derived from a session
	// identifier a client has already seen.
	if first.AuthenticationToken.Type != NodeIDTypeOpaque || len(first.AuthenticationToken.Opaque) != 32 {
		t.Fatalf("token = %+v", first.AuthenticationToken)
	}
	if bytes.Equal(first.AuthenticationToken.Opaque, second.AuthenticationToken.Opaque) {
		t.Fatal("two sessions share an authentication token")
	}
	if first.ID.Equal(second.ID) {
		t.Fatal("two sessions share a session id")
	}
	// The server nonce is fresh per session and long enough for the clause.
	if len(firstNonce) < MinNonceBytes || bytes.Equal(firstNonce, secondNonce) {
		t.Fatal("the server nonce was reused or too short")
	}
	// A session starts unactivated.
	if first.Activated {
		t.Fatal("a new session was already activated")
	}
}

func TestSessionTimeoutIsRevisedAndGreaterThanZero(t *testing.T) {
	limits := SessionLimits{MinTimeout: 10 * time.Second, MaxTimeout: time.Minute, MaxSessions: 8}
	registry := newTestSessionRegistry(t, limits)
	for _, testCase := range []struct {
		requested float64
		want      float64
	}{
		{0, 10_000},
		{-5, 10_000},
		{1_000, 10_000},
		{30_000, 30_000},
		{3_600_000, 60_000},
	} {
		request := testCreateSessionRequest()
		request.RequestedSessionTimeout = testCase.requested
		session, _, err := registry.Create(1, SecurityModeNone, request, channelEpoch)
		if err != nil {
			t.Fatal(err)
		}
		if session.Timeout != testCase.want {
			t.Fatalf("requested %v revised to %v, want %v", testCase.requested, session.Timeout, testCase.want)
		}
		if session.Timeout <= 0 {
			t.Fatal("the server provided a timeout that is not greater than zero")
		}
	}
}

// OPC 10000-4 Table 15: the token is used with the SecureChannelId to decide
// whether a client may use the session.
func TestSessionIsBoundToItsSecureChannel(t *testing.T) {
	registry := newTestSessionRegistry(t, DefaultSessionLimits())
	session, _, err := registry.Create(11, SecurityModeNone, testCreateSessionRequest(), channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Lookup(session.AuthenticationToken, 11, channelEpoch); err != nil {
		t.Fatalf("the owning channel was refused: %v", err)
	}
	// A token that leaked to another channel must not grant access.
	_, err = registry.Lookup(session.AuthenticationToken, 12, channelEpoch)
	if err == nil {
		t.Fatal("a session was used from another secure channel")
	}
	if got := codecStatus(t, err); got != StatusBadSecureChannelIDInvalid {
		t.Fatalf("status = %s", got.Hex())
	}
}

func TestSessionLookupRejectsUnknownAndExpired(t *testing.T) {
	registry := newTestSessionRegistry(t, DefaultSessionLimits())
	session, _, err := registry.Create(1, SecurityModeNone, testCreateSessionRequest(), channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	unknown := NodeID{Namespace: 1, Type: NodeIDTypeOpaque, Opaque: bytes.Repeat([]byte{9}, 32)}
	if _, err := registry.Lookup(unknown, 1, channelEpoch); err == nil {
		t.Fatal("an unknown token was accepted")
	} else if got := codecStatus(t, err); got != StatusBadSessionIDInvalid {
		t.Fatalf("status = %s", got.Hex())
	}
	// A numeric token is not a session token.
	if _, err := registry.Lookup(NumericNodeID(1, 1), 1, channelEpoch); err == nil {
		t.Fatal("a numeric token was accepted")
	}

	// Activity refreshes the timeout, so a busy session does not expire.
	if _, err := registry.Lookup(session.AuthenticationToken, 1, channelEpoch.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Lookup(session.AuthenticationToken, 1, channelEpoch.Add(80*time.Second)); err != nil {
		t.Fatalf("activity did not refresh the timeout: %v", err)
	}
	// Going quiet past the revised timeout closes it.
	_, err = registry.Lookup(session.AuthenticationToken, 1, channelEpoch.Add(200*time.Second))
	if err == nil {
		t.Fatal("a timed out session was still resolvable")
	}
	if got := codecStatus(t, err); got != StatusBadSessionClosed {
		t.Fatalf("status = %s, want Bad_SessionClosed", got.Hex())
	}
}

// Table 17: "Null or empty user token shall always be interpreted as anonymous."
func TestActivateAcceptsAnonymousIdentities(t *testing.T) {
	registry := newTestSessionRegistry(t, DefaultSessionLimits())
	session, _, err := registry.Create(1, SecurityModeNone, testCreateSessionRequest(), channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	nonce, err := registry.Activate(session, ActivateSessionRequest{
		UserIdentityToken: NullExtensionObject(),
	}, "", channelEpoch)
	if err != nil {
		t.Fatalf("a null identity token was refused: %v", err)
	}
	if len(nonce) < MinNonceBytes {
		t.Fatalf("server nonce is %d bytes", len(nonce))
	}
	if !session.Activated {
		t.Fatal("the session was not activated")
	}

	// An explicit AnonymousIdentityToken carrying the published policy id.
	encoder := newTestEncoder(t, DefaultBinaryLimits())
	encoder.WriteString("anon")
	body, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	token := ExtensionObject{
		TypeID:   NumericNodeID(0, AnonymousIdentityTokenEncodingID),
		Encoding: ExtensionObjectByteString,
		Body:     body,
	}
	if _, err := registry.Activate(session, ActivateSessionRequest{UserIdentityToken: token}, "anon", channelEpoch); err != nil {
		t.Fatalf("a matching anonymous token was refused: %v", err)
	}
}

func TestActivateRejectsOtherIdentityTypes(t *testing.T) {
	registry := newTestSessionRegistry(t, DefaultSessionLimits())
	session, _, err := registry.Create(1, SecurityModeNone, testCreateSessionRequest(), channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	// A UserNameIdentityToken is not accepted, because no such policy is
	// published.
	other := ExtensionObject{TypeID: NumericNodeID(0, 324), Encoding: ExtensionObjectNoBody}
	_, err = registry.Activate(session, ActivateSessionRequest{UserIdentityToken: other}, "", channelEpoch)
	if err == nil {
		t.Fatal("a non-anonymous identity token was accepted")
	}
	if got := codecStatus(t, err); got != StatusBadIdentityTokenInvalid {
		t.Fatalf("status = %s", got.Hex())
	}

	// An anonymous token naming a policy this endpoint does not publish.
	encoder := newTestEncoder(t, DefaultBinaryLimits())
	encoder.WriteString("other")
	body, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	mismatched := ExtensionObject{
		TypeID:   NumericNodeID(0, AnonymousIdentityTokenEncodingID),
		Encoding: ExtensionObjectByteString,
		Body:     body,
	}
	_, err = registry.Activate(session, ActivateSessionRequest{UserIdentityToken: mismatched}, "anon", channelEpoch)
	if err == nil {
		t.Fatal("a mismatched policy id was accepted")
	}
	if got := codecStatus(t, err); got != StatusBadIdentityTokenRejected {
		t.Fatalf("status = %s", got.Hex())
	}
}

func TestSessionRegistryBoundsAndCleanup(t *testing.T) {
	limits := DefaultSessionLimits()
	limits.MaxSessions = 2
	registry := newTestSessionRegistry(t, limits)
	for count := 0; count < limits.MaxSessions; count++ {
		if _, _, err := registry.Create(1, SecurityModeNone, testCreateSessionRequest(), channelEpoch); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := registry.Create(1, SecurityModeNone, testCreateSessionRequest(), channelEpoch)
	if err == nil {
		t.Fatal("the session limit was exceeded")
	}
	if got := codecStatus(t, err); got != StatusBadTooManySessions {
		t.Fatalf("status = %s, want Bad_TooManySessions", got.Hex())
	}

	// Quiet sessions are reclaimed, freeing the slots.
	if removed := registry.ExpireStale(channelEpoch.Add(time.Hour)); removed != 2 {
		t.Fatalf("reclaimed %d sessions, want 2", removed)
	}
	if registry.Count() != 0 {
		t.Fatalf("registry holds %d sessions", registry.Count())
	}
	if _, _, err := registry.Create(1, SecurityModeNone, testCreateSessionRequest(), channelEpoch.Add(time.Hour)); err != nil {
		t.Fatalf("a slot was not freed: %v", err)
	}
}

func TestCloseSessionRemovesIt(t *testing.T) {
	registry := newTestSessionRegistry(t, DefaultSessionLimits())
	session, _, err := registry.Create(1, SecurityModeNone, testCreateSessionRequest(), channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Close(session.AuthenticationToken, 1, channelEpoch); err != nil {
		t.Fatal(err)
	}
	if registry.Count() != 0 {
		t.Fatalf("registry holds %d sessions after close", registry.Count())
	}
	if err := registry.Close(session.AuthenticationToken, 1, channelEpoch); err == nil {
		t.Fatal("closing an unknown session succeeded")
	}
}

func TestSessionLimitsValidation(t *testing.T) {
	if err := DefaultSessionLimits().ValidateForConfiguration(); err != nil {
		t.Fatalf("default limits rejected: %v", err)
	}
	for name, mutate := range map[string]func(*SessionLimits){
		"zero minimum":   func(l *SessionLimits) { l.MinTimeout = 0 },
		"zero maximum":   func(l *SessionLimits) { l.MaxTimeout = 0 },
		"zero sessions":  func(l *SessionLimits) { l.MaxSessions = 0 },
		"inverted range": func(l *SessionLimits) { l.MinTimeout = l.MaxTimeout + time.Second },
	} {
		t.Run(name, func(t *testing.T) {
			limits := DefaultSessionLimits()
			mutate(&limits)
			if err := limits.ValidateForConfiguration(); err == nil {
				t.Fatalf("limits %+v were accepted", limits)
			}
			if _, err := NewSessionRegistry(limits); err == nil {
				t.Fatal("a registry was built from invalid limits")
			}
		})
	}
}
