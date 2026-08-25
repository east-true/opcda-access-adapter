package opcua

import (
	"bytes"
	"testing"
	"time"
)

func serviceRequestHeader() RequestHeader {
	return RequestHeader{
		AuthenticationToken: NumericNodeID(0, 0),
		Timestamp:           channelEpoch,
		RequestHandle:       9,
		TimeoutHint:         10_000,
		AdditionalHeader:    NullExtensionObject(),
	}
}

// OPC 10000-6 5.2.9: a message is prefixed by the NodeId of its
// DataTypeEncoding, with no length field.
func TestServiceMessagesArePrefixedByTheirEncodingNodeID(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoder := newTestEncoder(t, limits)
	encoder.WriteOpenSecureChannelRequest(OpenSecureChannelRequest{
		Header: serviceRequestHeader(), RequestType: TokenRequestIssue, SecurityMode: SecurityModeNone,
	})
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	// 446 does not fit the two-byte form, so the four-byte encoding is used.
	if encoded[0] != nodeIDEncodingFourByte {
		t.Fatalf("TypeId encoding = 0x%02X", encoded[0])
	}
	decoder := newTestDecoder(t, encoded, limits)
	identifier, err := decoder.ReadServiceTypeID()
	if err != nil {
		t.Fatal(err)
	}
	if identifier != OpenSecureChannelRequestEncodingID {
		t.Fatalf("TypeId = %d, want %d", identifier, OpenSecureChannelRequestEncodingID)
	}
}

// The encoding identifiers come from the OPC Foundation NodeIds table.
func TestServiceEncodingIdentifiers(t *testing.T) {
	cases := map[string]uint32{
		"ServiceFault":               397,
		"OpenSecureChannelRequest":   446,
		"OpenSecureChannelResponse":  449,
		"CloseSecureChannelRequest":  452,
		"CloseSecureChannelResponse": 455,
	}
	got := map[string]uint32{
		"ServiceFault":               ServiceFaultEncodingID,
		"OpenSecureChannelRequest":   OpenSecureChannelRequestEncodingID,
		"OpenSecureChannelResponse":  OpenSecureChannelResponseEncodingID,
		"CloseSecureChannelRequest":  CloseSecureChannelRequestEncodingID,
		"CloseSecureChannelResponse": CloseSecureChannelResponseEncodingID,
	}
	for name, want := range cases {
		if got[name] != want {
			t.Fatalf("%s = %d, want %d", name, got[name], want)
		}
	}
}

// A TypeId that is not a standard numeric identifier in namespace 0 is refused.
func TestServiceTypeIDRejectsNonStandardEncodings(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoder := newTestEncoder(t, limits)
	encoder.WriteNodeID(StringNodeID(3, "custom"))
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	decoder := newTestDecoder(t, encoded, limits)
	_, err = decoder.ReadServiceTypeID()
	if err == nil {
		t.Fatal("a non-standard TypeId was accepted")
	}
	if got := codecStatus(t, err); got != StatusBadServiceUnsupported {
		t.Fatalf("status = %s, want Bad_ServiceUnsupported", got.Hex())
	}
}

// SecurityTokenRequestType is Issue 0, Renew 1 in the OPC Foundation NodeSet.
func TestTokenRequestTypeWireValues(t *testing.T) {
	if TokenRequestIssue != 0 || TokenRequestRenew != 1 {
		t.Fatalf("Issue = %d, Renew = %d", TokenRequestIssue, TokenRequestRenew)
	}
}

func TestOpenSecureChannelRequestRoundTrip(t *testing.T) {
	limits := DefaultBinaryLimits()
	request := OpenSecureChannelRequest{
		Header:                serviceRequestHeader(),
		ClientProtocolVersion: ProtocolVersion,
		RequestType:           TokenRequestRenew,
		SecurityMode:          SecurityModeNone,
		ClientNonce:           []byte{1, 2, 3},
		RequestedLifetime:     60_000,
	}
	encoder := newTestEncoder(t, limits)
	encoder.WriteOpenSecureChannelRequest(request)
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	decoder := newTestDecoder(t, encoded, limits)
	if _, err := decoder.ReadServiceTypeID(); err != nil {
		t.Fatal(err)
	}
	decoded, err := decoder.ReadOpenSecureChannelRequest()
	if err != nil {
		t.Fatal(err)
	}
	if decoded.RequestType != TokenRequestRenew || decoded.SecurityMode != SecurityModeNone ||
		decoded.RequestedLifetime != 60_000 || !bytes.Equal(decoded.ClientNonce, []byte{1, 2, 3}) {
		t.Fatalf("request = %+v", decoded)
	}
	if decoded.Header.RequestHandle != 9 {
		t.Fatalf("request handle = %d", decoded.Header.RequestHandle)
	}
	if !decoder.Done() {
		t.Fatalf("%d bytes left", decoder.Remaining())
	}
}

// A value outside either enumeration is refused rather than reduced to a
// neighbouring one, so a malformed field never looks like a deliberate choice.
func TestOpenSecureChannelRequestRejectsUndefinedEnums(t *testing.T) {
	limits := DefaultBinaryLimits()
	build := func(requestType, securityMode int32) []byte {
		encoder := newTestEncoder(t, limits)
		encoder.WriteRequestHeader(serviceRequestHeader())
		encoder.WriteUInt32(ProtocolVersion)
		encoder.WriteInt32(requestType)
		encoder.WriteInt32(securityMode)
		encoder.WriteNullByteString()
		encoder.WriteUInt32(60_000)
		encoded, err := encoder.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	for _, testCase := range []struct {
		name        string
		requestType int32
		mode        int32
	}{
		{"request type above the enumeration", 2, int32(SecurityModeNone)},
		{"negative request type", -1, int32(SecurityModeNone)},
		{"security mode above the enumeration", int32(TokenRequestIssue), 4},
		{"negative security mode", int32(TokenRequestIssue), -1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			decoder := newTestDecoder(t, build(testCase.requestType, testCase.mode), limits)
			if _, err := decoder.ReadOpenSecureChannelRequest(); err == nil {
				t.Fatal("an undefined enumeration value was accepted")
			}
		})
	}
}

func TestOpenSecureChannelResponseRoundTrip(t *testing.T) {
	limits := DefaultBinaryLimits()
	response := OpenSecureChannelResponse{
		Header: ResponseHeader{
			Timestamp: channelEpoch, RequestHandle: 9,
			ServiceResult: StatusGood, AdditionalHeader: NullExtensionObject(),
		},
		ServerProtocolVersion: ProtocolVersion,
		SecurityToken: ChannelSecurityToken{
			SecureChannelID: 11, TokenID: 22,
			CreatedAt: channelEpoch, RevisedLifetime: 60_000,
		},
	}
	encoder := newTestEncoder(t, limits)
	encoder.WriteOpenSecureChannelResponse(response)
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	decoder := newTestDecoder(t, encoded, limits)
	identifier, err := decoder.ReadServiceTypeID()
	if err != nil || identifier != OpenSecureChannelResponseEncodingID {
		t.Fatalf("TypeId = %d, %v", identifier, err)
	}
	decoded, err := decoder.ReadOpenSecureChannelResponse()
	if err != nil {
		t.Fatal(err)
	}
	if decoded.SecurityToken.SecureChannelID != 11 || decoded.SecurityToken.TokenID != 22 ||
		decoded.SecurityToken.RevisedLifetime != 60_000 ||
		!decoded.SecurityToken.CreatedAt.Equal(channelEpoch) {
		t.Fatalf("token = %+v", decoded.SecurityToken)
	}
	// With SecurityMode None the nonce is null, not an empty byte string.
	if decoded.ServerNonce != nil {
		t.Fatalf("server nonce = %v, want null", decoded.ServerNonce)
	}
}

func TestCloseSecureChannelAndFaultRoundTrip(t *testing.T) {
	limits := DefaultBinaryLimits()
	encoder := newTestEncoder(t, limits)
	encoder.WriteCloseSecureChannelRequest(CloseSecureChannelRequest{Header: serviceRequestHeader()})
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	decoder := newTestDecoder(t, encoded, limits)
	identifier, err := decoder.ReadServiceTypeID()
	if err != nil || identifier != CloseSecureChannelRequestEncodingID {
		t.Fatalf("TypeId = %d, %v", identifier, err)
	}
	request, err := decoder.ReadCloseSecureChannelRequest()
	if err != nil || request.Header.RequestHandle != 9 {
		t.Fatalf("request = %+v, %v", request, err)
	}

	encoder = newTestEncoder(t, limits)
	encoder.WriteServiceFault(NewServiceFault(9, StatusBadServiceUnsupported, channelEpoch))
	encoded, err = encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	decoder = newTestDecoder(t, encoded, limits)
	identifier, err = decoder.ReadServiceTypeID()
	if err != nil || identifier != ServiceFaultEncodingID {
		t.Fatalf("TypeId = %d, %v", identifier, err)
	}
	header, err := decoder.ReadResponseHeader()
	if err != nil {
		t.Fatal(err)
	}
	// A fault echoes the request handle so it can be matched to its request.
	if header.RequestHandle != 9 || header.ServiceResult != StatusBadServiceUnsupported {
		t.Fatalf("fault header = %+v", header)
	}
}

func newTestService(t *testing.T) (*ChannelService, *ChannelRegistry) {
	t.Helper()
	registry := newTestRegistry(t, DefaultChannelLimits())
	return NewChannelService(registry, ProtocolVersion), registry
}

func TestOpenSecureChannelIssuesAndRenews(t *testing.T) {
	service, registry := newTestService(t)
	issue := OpenSecureChannelRequest{
		Header: serviceRequestHeader(), ClientProtocolVersion: ProtocolVersion,
		RequestType: TokenRequestIssue, SecurityMode: SecurityModeNone, RequestedLifetime: 60_000,
	}
	response, err := service.OpenSecureChannel(issue, 0, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if response.Header.ServiceResult != StatusGood || response.Header.RequestHandle != 9 {
		t.Fatalf("response header = %+v", response.Header)
	}
	if response.ServerProtocolVersion != ProtocolVersion {
		t.Fatalf("server protocol version = %d", response.ServerProtocolVersion)
	}
	channelID := response.SecurityToken.SecureChannelID
	if channelID == 0 || registry.Count() != 1 {
		t.Fatalf("channel %d, registry holds %d", channelID, registry.Count())
	}

	renew := issue
	renew.RequestType = TokenRequestRenew
	renewed, err := service.OpenSecureChannel(renew, channelID, channelEpoch.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if renewed.SecurityToken.SecureChannelID != channelID {
		t.Fatal("renewal changed the channel id")
	}
	if renewed.SecurityToken.TokenID == response.SecurityToken.TokenID {
		t.Fatal("renewal reused the token id")
	}
	if registry.Count() != 1 {
		t.Fatalf("renewal created a second channel: %d", registry.Count())
	}
}

// OPC 10000-6 6.7.4: the request version shall match the Hello version.
func TestOpenSecureChannelRequiresTheHelloProtocolVersion(t *testing.T) {
	service, _ := newTestService(t)
	request := OpenSecureChannelRequest{
		Header: serviceRequestHeader(), ClientProtocolVersion: ProtocolVersion + 1,
		RequestType: TokenRequestIssue, SecurityMode: SecurityModeNone, RequestedLifetime: 60_000,
	}
	_, err := service.OpenSecureChannel(request, 0, channelEpoch)
	if err == nil {
		t.Fatal("a mismatched protocol version opened a channel")
	}
	if got := codecStatus(t, err); got != StatusBadProtocolVersionUnsupport {
		t.Fatalf("status = %s", got.Hex())
	}
}

func TestRenewRequiresAnExistingChannel(t *testing.T) {
	service, _ := newTestService(t)
	renew := OpenSecureChannelRequest{
		Header: serviceRequestHeader(), ClientProtocolVersion: ProtocolVersion,
		RequestType: TokenRequestRenew, SecurityMode: SecurityModeNone, RequestedLifetime: 60_000,
	}
	_, err := service.OpenSecureChannel(renew, 0, channelEpoch)
	if err == nil {
		t.Fatal("a renew without a channel id succeeded")
	}
	if got := codecStatus(t, err); got != StatusBadSecureChannelIDInvalid {
		t.Fatalf("status = %s, want Bad_SecureChannelIdInvalid", got.Hex())
	}

	_, err = service.OpenSecureChannel(renew, 4242, channelEpoch)
	if err == nil {
		t.Fatal("a renew of an unknown channel succeeded")
	}
	if got := codecStatus(t, err); got != StatusBadTcpSecureChannelUnknown {
		t.Fatalf("status = %s", got.Hex())
	}
}

// A renewal is tied to the channel that already exists, so it must not change
// the security mode it was opened with.
func TestRenewMustNotChangeTheSecurityMode(t *testing.T) {
	service, _ := newTestService(t)
	issue := OpenSecureChannelRequest{
		Header: serviceRequestHeader(), ClientProtocolVersion: ProtocolVersion,
		RequestType: TokenRequestIssue, SecurityMode: SecurityModeNone, RequestedLifetime: 60_000,
	}
	response, err := service.OpenSecureChannel(issue, 0, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	renew := issue
	renew.RequestType = TokenRequestRenew
	renew.SecurityMode = SecurityModeSignAndEncrypt
	_, err = service.OpenSecureChannel(renew, response.SecurityToken.SecureChannelID, channelEpoch)
	if err == nil {
		t.Fatal("a renew changed the security mode")
	}
	if got := codecStatus(t, err); got != StatusBadSecurityModeRejected {
		t.Fatalf("status = %s", got.Hex())
	}
}

func TestOpenSecureChannelRefusesUnsupportedSecurityModes(t *testing.T) {
	service, registry := newTestService(t)
	for _, mode := range []SecurityMode{SecurityModeInvalid, SecurityModeSign, SecurityModeSignAndEncrypt} {
		request := OpenSecureChannelRequest{
			Header: serviceRequestHeader(), ClientProtocolVersion: ProtocolVersion,
			RequestType: TokenRequestIssue, SecurityMode: mode, RequestedLifetime: 60_000,
		}
		if _, err := service.OpenSecureChannel(request, 0, channelEpoch); err == nil {
			t.Fatalf("%s opened a channel", mode)
		}
	}
	if registry.Count() != 0 {
		t.Fatalf("refused requests left %d channels open", registry.Count())
	}
}

func TestCloseSecureChannelRemovesTheChannel(t *testing.T) {
	service, registry := newTestService(t)
	issue := OpenSecureChannelRequest{
		Header: serviceRequestHeader(), ClientProtocolVersion: ProtocolVersion,
		RequestType: TokenRequestIssue, SecurityMode: SecurityModeNone, RequestedLifetime: 60_000,
	}
	response, err := service.OpenSecureChannel(issue, 0, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	channelID := response.SecurityToken.SecureChannelID

	closeResponse, err := service.CloseSecureChannel(
		CloseSecureChannelRequest{Header: serviceRequestHeader()}, channelID, channelEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if closeResponse.Header.ServiceResult != StatusGood || closeResponse.Header.RequestHandle != 9 {
		t.Fatalf("close response = %+v", closeResponse.Header)
	}
	if registry.Count() != 0 {
		t.Fatalf("registry holds %d channels after close", registry.Count())
	}
	if _, err := service.CloseSecureChannel(
		CloseSecureChannelRequest{Header: serviceRequestHeader()}, channelID, channelEpoch); err == nil {
		t.Fatal("closing an unknown channel succeeded")
	}
}
