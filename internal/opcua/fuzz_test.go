package opcua

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// FuzzDecodeUABinary drives the decoder with arbitrary bytes. A malformed
// message must always produce an error rather than a panic, an out-of-range
// read, or an allocation sized by an unverified length. OPC 10000-6 5.1.8
// requires exactly this: reject what the decoder does not support.
func FuzzDecodeUABinary(f *testing.F) {
	limits := DefaultBinaryLimits()

	seed, err := NewEncoder(limits)
	if err != nil {
		f.Fatal(err)
	}
	seed.WriteBoolean(true)
	seed.WriteString("水Boy")
	seed.WriteByteString([]byte{1, 2, 3})
	seed.WriteGuid(Guid{Data1: 1, Data2: 2, Data3: 3})
	seed.WriteDateTime(time.Unix(0, 0).UTC())
	seed.WriteArrayLength(2)
	seed.WriteInt32(7)
	seed.WriteInt32(8)
	encoded, err := seed.Bytes()
	if err != nil {
		f.Fatal(err)
	}

	f.Add(encoded)
	f.Add([]byte{})
	// A null length, an empty length, and lengths that must be refused.
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	f.Add([]byte{0x00, 0x00, 0x00, 0x00})
	f.Add([]byte{0xFE, 0xFF, 0xFF, 0xFF})
	f.Add([]byte{0x00, 0x00, 0x00, 0x80})
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0x7F})

	f.Fuzz(func(t *testing.T, data []byte) {
		decoder, err := NewDecoder(data, limits)
		if err != nil {
			return
		}
		// Walk the buffer with every reader until one refuses. No sequence of
		// calls may panic or read past the end.
		for step := 0; decoder.Remaining() > 0; step++ {
			var readErr error
			switch step % 12 {
			case 0:
				_, readErr = decoder.ReadBoolean()
			case 1:
				_, readErr = decoder.ReadSByte()
			case 2:
				_, readErr = decoder.ReadByteValue()
			case 3:
				_, readErr = decoder.ReadInt16()
			case 4:
				_, readErr = decoder.ReadUInt32()
			case 5:
				_, readErr = decoder.ReadInt64()
			case 6:
				_, readErr = decoder.ReadDouble()
			case 7:
				_, readErr = decoder.ReadStatusCode()
			case 8:
				_, _, readErr = decoder.ReadString()
			case 9:
				_, _, readErr = decoder.ReadByteString()
			case 10:
				_, readErr = decoder.ReadGuid()
			case 11:
				var length int
				var isNull bool
				length, isNull, readErr = decoder.ReadArrayLength(4)
				if readErr == nil && !isNull && length > limits.MaxArrayLength {
					t.Fatalf("array length %d passed the %d limit", length, limits.MaxArrayLength)
				}
			}
			if readErr != nil {
				// Every refusal must carry a UA status a peer can be told.
				if _, ok := readErr.(*CodecError); !ok {
					t.Fatalf("decode error %v is not a CodecError", readErr)
				}
				return
			}
			if decoder.Remaining() < 0 {
				t.Fatalf("decoder read past the end of the buffer")
			}
		}
	})
}

// FuzzDecodeUACP drives the connection protocol framing with arbitrary bytes.
// A header must be validated against the negotiated buffer before any body is
// read, and no message body may panic or over-read.
func FuzzDecodeUACP(f *testing.F) {
	limits := DefaultBinaryLimits()

	hello, err := EncodeHello(Hello{
		ProtocolVersion:   ProtocolVersion,
		ReceiveBufferSize: MinimumBufferSize,
		SendBufferSize:    MinimumBufferSize,
		EndpointURL:       "opc.tcp://127.0.0.1:4840",
	}, limits)
	if err != nil {
		f.Fatal(err)
	}
	header, err := EncodeMessageHeader(MessageTypeHello, ChunkFinal, len(hello), 65536)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(append(header, hello...))
	f.Add([]byte{})
	f.Add([]byte{'H', 'E', 'L', 'F', 8, 0, 0, 0})
	f.Add([]byte{'M', 'S', 'G', 'C', 0xFF, 0xFF, 0xFF, 0xFF})
	f.Add([]byte{'E', 'R', 'R', 'F', 8, 0, 0, 0})
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 0})

	f.Fuzz(func(t *testing.T, data []byte) {
		const negotiated = uint32(65536)
		decoded, err := DecodeMessageHeader(data, negotiated)
		if err != nil {
			if _, ok := err.(*CodecError); !ok {
				t.Fatalf("header error %v is not a CodecError", err)
			}
			return
		}
		if decoded.Size > negotiated || decoded.Size < HeaderSize {
			t.Fatalf("an out-of-range message size passed validation: %d", decoded.Size)
		}
		if decoded.BodySize() < 0 {
			t.Fatalf("negative body size %d", decoded.BodySize())
		}
		if len(data) < int(decoded.Size) {
			return
		}
		body := data[HeaderSize:decoded.Size]
		switch decoded.Type {
		case MessageTypeHello:
			_, _ = DecodeHello(body, limits)
		case MessageTypeAcknowledge:
			_, _ = DecodeAcknowledge(body, limits)
		case MessageTypeError:
			_, _ = DecodeProtocolError(body, limits)
		}
	})
}

// FuzzDecodeUASC drives the secure conversation framing with arbitrary bytes.
// A malformed security header must be refused rather than trusted, and no
// declared length may be honoured beyond the bytes present.
func FuzzDecodeUASC(f *testing.F) {
	limits := DefaultBinaryLimits()

	header, err := EncodeSecureConversationHeader(SecureConversationHeader{
		Type: MessageTypeOpenChannel, Chunk: ChunkFinal, SecureChannelID: 1,
	}, 12, 65536)
	if err != nil {
		f.Fatal(err)
	}
	security, err := EncodeAsymmetricSecurityHeader(AsymmetricSecurityHeader{}, 4096, limits)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(append(header, security...))
	f.Add([]byte{})
	f.Add([]byte{'M', 'S', 'G', 'F', 12, 0, 0, 0, 1, 0, 0, 0})
	f.Add([]byte{'O', 'P', 'N', 'F', 0xFF, 0xFF, 0xFF, 0xFF, 0, 0, 0, 0})
	f.Add([]byte{'C', 'L', 'O', 'A', 12, 0, 0, 0, 0, 0, 0, 0})

	f.Fuzz(func(t *testing.T, data []byte) {
		const negotiated = uint32(65536)
		decoded, err := DecodeSecureConversationHeader(data, negotiated)
		if err != nil {
			if _, ok := err.(*CodecError); !ok {
				t.Fatalf("header error %v is not a CodecError", err)
			}
			return
		}
		if decoded.Size < SecureConversationHeaderSize || decoded.Size > negotiated {
			t.Fatalf("an out-of-range message size passed validation: %d", decoded.Size)
		}
		if len(data) < int(decoded.Size) {
			return
		}
		body := data[SecureConversationHeaderSize:decoded.Size]
		if decoded.Type == MessageTypeOpenChannel {
			security, consumed, securityErr := DecodeAsymmetricSecurityHeader(body, 4096, limits)
			if securityErr != nil {
				if _, ok := securityErr.(*CodecError); !ok {
					t.Fatalf("security header error %v is not a CodecError", securityErr)
				}
				return
			}
			if consumed < 0 || consumed > len(body) {
				t.Fatalf("security header consumed %d of %d bytes", consumed, len(body))
			}
			if len(security.SecurityPolicyURI) > MaxSecurityPolicyURIBytes {
				t.Fatalf("a policy URI of %d bytes passed the limit", len(security.SecurityPolicyURI))
			}
			if n := len(security.ReceiverCertificateThumbprint); n != 0 && n != CertificateThumbprintBytes {
				t.Fatalf("a thumbprint of %d bytes passed validation", n)
			}
			body = body[consumed:]
		} else {
			_, _ = DecodeSequenceHeader(body, limits)
			return
		}
		_, _ = DecodeSequenceHeader(body, limits)
	})
}

// FuzzDecodeStructuredTypes drives NodeId, ExtensionObject, DiagnosticInfo and
// the service headers with arbitrary bytes. Recursion and every declared length
// must stay bounded no matter what a peer sends.
func FuzzDecodeStructuredTypes(f *testing.F) {
	limits := DefaultBinaryLimits()

	seed, err := NewEncoder(limits)
	if err != nil {
		f.Fatal(err)
	}
	seed.WriteNodeID(StringNodeID(2, "Test/Float"))
	seed.WriteExtensionObject(NullExtensionObject())
	seed.WriteDiagnosticInfo(DiagnosticInfo{SymbolicID: -1, HasSymbolicID: true})
	seed.WriteRequestHeader(RequestHeader{
		AuthenticationToken: NumericNodeID(0, 1), AdditionalHeader: NullExtensionObject(),
	})
	encoded, err := seed.Bytes()
	if err != nil {
		f.Fatal(err)
	}

	f.Add(encoded)
	f.Add([]byte{})
	f.Add([]byte{0x00, 0x00})
	f.Add([]byte{0x03, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	// A chain of DiagnosticInfo masks that ask for unbounded recursion.
	f.Add(bytes.Repeat([]byte{0x40}, 64))
	f.Add([]byte{0x05, 0, 0, 0xFF, 0xFF, 0xFF, 0x7F})

	f.Fuzz(func(t *testing.T, data []byte) {
		for _, read := range []func(*Decoder) error{
			func(d *Decoder) error { _, err := d.ReadNodeID(); return err },
			func(d *Decoder) error { _, err := d.ReadExpandedNodeID(); return err },
			func(d *Decoder) error { _, err := d.ReadQualifiedName(); return err },
			func(d *Decoder) error { _, err := d.ReadLocalizedText(); return err },
			func(d *Decoder) error { _, err := d.ReadExtensionObject(); return err },
			func(d *Decoder) error { _, err := d.ReadDiagnosticInfo(); return err },
			func(d *Decoder) error { _, err := d.ReadRequestHeader(); return err },
			func(d *Decoder) error { _, err := d.ReadResponseHeader(); return err },
			func(d *Decoder) error { _, err := d.ReadApplicationDescription(); return err },
			func(d *Decoder) error { _, err := d.ReadUserTokenPolicy(); return err },
			func(d *Decoder) error { _, err := d.ReadEndpointDescription(); return err },
			func(d *Decoder) error { _, err := d.ReadGetEndpointsRequest(); return err },
			func(d *Decoder) error { _, err := d.ReadGetEndpointsResponse(); return err },
			func(d *Decoder) error { _, err := d.ReadSignatureData(); return err },
			func(d *Decoder) error { _, err := d.ReadCreateSessionRequest(); return err },
			func(d *Decoder) error { _, err := d.ReadCreateSessionResponse(); return err },
			func(d *Decoder) error { _, err := d.ReadActivateSessionRequest(); return err },
			func(d *Decoder) error { _, err := d.ReadActivateSessionResponse(); return err },
			func(d *Decoder) error { _, err := d.ReadCloseSessionRequest(); return err },
			func(d *Decoder) error { _, err := d.ReadViewDescription(); return err },
			func(d *Decoder) error { _, err := d.ReadBrowseDescription(); return err },
			func(d *Decoder) error { _, err := d.ReadReferenceDescription(); return err },
			func(d *Decoder) error { _, err := d.ReadBrowseResult(); return err },
			func(d *Decoder) error { _, err := d.ReadBrowseRequest(); return err },
			func(d *Decoder) error { _, err := d.ReadBrowseResponse(); return err },
			func(d *Decoder) error { _, err := d.ReadBrowseNextRequest(); return err },
			func(d *Decoder) error { _, err := d.ReadBrowseNextResponse(); return err },
			func(d *Decoder) error { _, err := d.ReadVariant(); return err },
			func(d *Decoder) error { _, err := d.ReadDataValue(); return err },
			func(d *Decoder) error { _, err := d.ReadReadValueID(); return err },
			func(d *Decoder) error { _, err := d.ReadWriteValue(); return err },
			func(d *Decoder) error { _, err := d.ReadReadRequest(); return err },
			func(d *Decoder) error { _, err := d.ReadReadResponse(); return err },
			func(d *Decoder) error { _, err := d.ReadWriteRequest(); return err },
			func(d *Decoder) error { _, err := d.ReadWriteResponse(); return err },
			func(d *Decoder) error { _, err := d.ReadMonitoringParameters(); return err },
			func(d *Decoder) error { _, err := d.ReadCreateSubscriptionRequest(); return err },
			func(d *Decoder) error { _, err := d.ReadCreateSubscriptionResponse(); return err },
			func(d *Decoder) error { _, err := d.ReadCreateMonitoredItemsRequest(); return err },
			func(d *Decoder) error { _, err := d.ReadCreateMonitoredItemsResponse(); return err },
			func(d *Decoder) error { _, err := d.ReadDeleteMonitoredItemsRequest(); return err },
			func(d *Decoder) error { _, err := d.ReadDeleteSubscriptionsRequest(); return err },
			func(d *Decoder) error { _, err := d.ReadSetPublishingModeRequest(); return err },
			func(d *Decoder) error { _, err := d.ReadPublishRequest(); return err },
			func(d *Decoder) error { _, err := d.ReadPublishResponse(); return err },
			func(d *Decoder) error { _, err := d.ReadNotificationMessage(); return err },
		} {
			decoder, err := NewDecoder(data, limits)
			if err != nil {
				return
			}
			if err := read(decoder); err != nil {
				if _, ok := err.(*CodecError); !ok {
					t.Fatalf("decode error %v is not a CodecError", err)
				}
				continue
			}
			if decoder.Remaining() < 0 {
				t.Fatal("a reader consumed past the end of the buffer")
			}
		}
	})
}

// FuzzDecodeSecureChannelService drives the service bodies with arbitrary
// bytes. An undefined enumeration value must be refused rather than reduced to
// a neighbouring one, and no length may be honoured beyond the bytes present.
func FuzzDecodeSecureChannelService(f *testing.F) {
	limits := DefaultBinaryLimits()

	seed, err := NewEncoder(limits)
	if err != nil {
		f.Fatal(err)
	}
	seed.WriteOpenSecureChannelRequest(OpenSecureChannelRequest{
		Header:       RequestHeader{AuthenticationToken: NumericNodeID(0, 0), AdditionalHeader: NullExtensionObject()},
		RequestType:  TokenRequestIssue,
		SecurityMode: SecurityModeNone,
	})
	encoded, err := seed.Bytes()
	if err != nil {
		f.Fatal(err)
	}

	f.Add(encoded)
	f.Add([]byte{})
	f.Add([]byte{0x01, 0x00, 0xBE, 0x01})
	f.Add(bytes.Repeat([]byte{0xFF}, 48))

	f.Fuzz(func(t *testing.T, data []byte) {
		decoder, err := NewDecoder(data, limits)
		if err != nil {
			return
		}
		identifier, err := decoder.ReadServiceTypeID()
		if err != nil {
			if _, ok := err.(*CodecError); !ok {
				t.Fatalf("TypeId error %v is not a CodecError", err)
			}
			return
		}
		switch identifier {
		case OpenSecureChannelRequestEncodingID:
			request, readErr := decoder.ReadOpenSecureChannelRequest()
			if readErr != nil {
				if _, ok := readErr.(*CodecError); !ok {
					t.Fatalf("decode error %v is not a CodecError", readErr)
				}
				return
			}
			switch request.RequestType {
			case TokenRequestIssue, TokenRequestRenew:
			default:
				t.Fatalf("an undefined request type %d was accepted", int32(request.RequestType))
			}
			if request.SecurityMode > SecurityModeSignAndEncrypt {
				t.Fatalf("an undefined security mode %d was accepted", uint32(request.SecurityMode))
			}
		case OpenSecureChannelResponseEncodingID:
			_, _ = decoder.ReadOpenSecureChannelResponse()
		case CloseSecureChannelRequestEncodingID:
			_, _ = decoder.ReadCloseSecureChannelRequest()
		default:
			_, _ = decoder.ReadResponseHeader()
		}
		if decoder.Remaining() < 0 {
			t.Fatal("a reader consumed past the end of the buffer")
		}
	})
}

// FuzzDecodeDateTime checks that no wire value produces a panic or an instant
// outside the range OPC 10000-6 5.2.2.5 defines.
func FuzzDecodeDateTime(f *testing.F) {
	f.Add(int64(0))
	f.Add(int64(-1))
	f.Add(DateTimeMax)
	f.Add(int64(1))
	f.Add(int64(-9223372036854775808))
	// Between the clause's upper bound and the maximum: a range the encoder
	// cannot produce, so only the wire can, and only rule 12 covers it.
	f.Add(EncodeDateTime(dateTimeUpperBound.Add(-time.Second)) + int64(2*dateTimeTicksPerSecond))

	f.Fuzz(func(t *testing.T, ticks int64) {
		decoded := DecodeDateTime(ticks)
		if decoded.Before(dateTimeLowerBound) {
			t.Fatalf("ticks %d decoded before 1601: %s", ticks, decoded)
		}
		if decoded.After(dateTimeUpperBound) {
			t.Fatalf("ticks %d decoded after 9999: %s", ticks, decoded)
		}
		// Re-encoding a decoded instant must stay inside the wire range.
		if encoded := EncodeDateTime(decoded); encoded < DateTimeMin {
			t.Fatalf("re-encoding produced %d", encoded)
		}
	})
}

// FuzzDispatchService drives arbitrary bytes through the service dispatch a
// real client's MSG chunk reaches, rather than through one decoder chosen by
// hand.
//
// The targets above fuzz the framing layers and two SecureChannel services. The
// other thirteen request decoders — CreateSession, ActivateSession, Browse,
// BrowseNext, Read, Write, CreateSubscription, CreateMonitoredItems,
// DeleteMonitoredItems, DeleteSubscriptions, SetPublishingMode, Publish,
// CloseSession — had none, and GetEndpoints is reachable before a session
// exists at all. Design §35.5 requires the hand-written parser to bound what a
// peer can make it do, and those are the majority of it.
//
// They were missed because the targets were written per layer, so a decoder
// added afterwards inherited nothing. Fuzzing the dispatch instead of a list of
// decoders is what fixes that: a service added to dispatchService is covered
// here without anyone remembering to extend this file.
func FuzzDispatchService(f *testing.F) {
	listener, err := NewListenerWithRuntime(testListenerConfig(), &fuzzRuntime{}, 1000, 2000)
	if err != nil {
		f.Fatal(err)
	}

	// Seeds: one well-formed body per service the dispatch answers, so the
	// fuzzer starts inside each decoder rather than having to find it.
	seeds := [][]byte{
		{}, {0x00}, bytes.Repeat([]byte{0xFF}, 64),
	}
	add := func(write func(*Encoder)) {
		encoder, encodeErr := NewEncoder(DefaultBinaryLimits())
		if encodeErr != nil {
			f.Fatal(encodeErr)
		}
		write(encoder)
		body, bodyErr := encoder.Bytes()
		if bodyErr != nil {
			f.Fatal(bodyErr)
		}
		seeds = append(seeds, body)
	}
	header := RequestHeader{
		AuthenticationToken: NodeID{Namespace: 1, Type: NodeIDTypeOpaque, Opaque: bytes.Repeat([]byte{7}, 32)},
		AdditionalHeader:    NullExtensionObject(),
	}
	add(func(e *Encoder) { e.WriteGetEndpointsRequest(GetEndpointsRequest{Header: header}) })
	add(func(e *Encoder) { e.WriteCreateSessionRequest(CreateSessionRequest{Header: header}) })
	add(func(e *Encoder) {
		e.WriteActivateSessionRequest(ActivateSessionRequest{Header: header, UserIdentityToken: NullExtensionObject()})
	})
	add(func(e *Encoder) { e.WriteCloseSessionRequest(CloseSessionRequest{Header: header}) })
	add(func(e *Encoder) {
		e.WriteBrowseRequest(BrowseRequest{Header: header, NodesToBrowse: []BrowseDescription{browseAll(NumericNodeID(0, NodeIDObjectsFolder))}})
	})
	add(func(e *Encoder) { e.WriteBrowseNextRequest(BrowseNextRequest{Header: header}) })
	add(func(e *Encoder) {
		e.WriteReadRequest(ReadRequest{Header: header, TimestampsToReturn: TimestampsBoth,
			NodesToRead: []ReadValueID{{NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue}}})
	})
	add(func(e *Encoder) {
		e.WriteWriteRequest(WriteRequest{Header: header, NodesToWrite: []WriteValue{{
			NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue,
			Value: DataValue{Value: Variant{Type: BuiltInInt32, Value: int32(1)}}}}})
	})
	add(func(e *Encoder) {
		e.WriteCreateSubscriptionRequest(CreateSubscriptionRequest{Header: header,
			RequestedPublishingInterval: 250, RequestedMaxKeepAliveCount: 3, PublishingEnabled: true})
	})
	add(func(e *Encoder) {
		e.WriteCreateMonitoredItemsRequest(CreateMonitoredItemsRequest{Header: header,
			TimestampsToReturn: TimestampsBoth,
			ItemsToCreate: []MonitoredItemCreateRequest{{
				ItemToMonitor:       ReadValueID{NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue},
				MonitoringMode:      MonitoringModeReporting,
				RequestedParameters: MonitoringParameters{Filter: NullExtensionObject()}}}})
	})
	add(func(e *Encoder) {
		e.WriteDeleteMonitoredItemsRequest(DeleteMonitoredItemsRequest{Header: header})
	})
	add(func(e *Encoder) { e.WriteDeleteSubscriptionsRequest(DeleteSubscriptionsRequest{Header: header}) })
	add(func(e *Encoder) { e.WriteSetPublishingModeRequest(SetPublishingModeRequest{Header: header}) })
	add(func(e *Encoder) { e.WritePublishRequest(PublishRequest{Header: header}) })
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		decoder, err := NewDecoder(data, listener.config.Binary)
		if err != nil {
			return
		}
		identifier, err := decoder.ReadServiceTypeID()
		if err != nil {
			if _, ok := err.(*CodecError); !ok {
				t.Fatalf("TypeId error %v is not a CodecError", err)
			}
			return
		}
		// Publish is the one service the dispatch does not answer, because the
		// listener holds it; its decoder is driven directly.
		if identifier == PublishRequestEncodingID {
			if _, readErr := decoder.ReadPublishRequest(); readErr != nil {
				if _, ok := readErr.(*CodecError); !ok {
					t.Fatalf("decode error %v is not a CodecError", readErr)
				}
			}
			return
		}
		_, _, serviceErr, fatal := listener.dispatchService(1, identifier, decoder)
		// A decoding failure closes the connection and must always carry a UA
		// status, so a peer is never answered with an untyped error.
		if fatal != nil {
			if _, ok := fatal.(*CodecError); !ok {
				t.Fatalf("fatal error %v is not a CodecError", fatal)
			}
		}
		if serviceErr != nil {
			var codecErr *CodecError
			if !errors.As(serviceErr, &codecErr) {
				t.Fatalf("service error %v carries no UA status", serviceErr)
			}
		}
		if decoder.Remaining() < 0 {
			t.Fatal("a reader consumed past the end of the buffer")
		}
	})
}

// fuzzRuntime answers every DA call without touching a source, so the fuzzer
// exercises the decoders rather than a stub's behaviour.
type fuzzRuntime struct{}

func (fuzzRuntime) Status(context.Context) opcda.RuntimeStatus {
	return opcda.RuntimeStatus{State: opcda.RuntimeStateConnected}
}

func (fuzzRuntime) Browse(context.Context, opcda.BrowseRequest) (opcda.BrowseResult, error) {
	return opcda.BrowseResult{}, nil
}

func (fuzzRuntime) ReadBatch(_ context.Context, request opcda.ReadRequest) ([]opcda.ReadResult, error) {
	results := make([]opcda.ReadResult, 0, len(request.Items))
	for _, item := range request.Items {
		results = append(results, opcda.ReadResult{ItemID: item, HRESULTPresent: true})
	}
	return results, nil
}

func (fuzzRuntime) WriteBatch(_ context.Context, items []opcda.WriteItem) ([]opcda.WriteResult, error) {
	results := make([]opcda.WriteResult, 0, len(items))
	for _, item := range items {
		results = append(results, opcda.WriteResult{ItemID: item.ItemID, HRESULTPresent: true})
	}
	return results, nil
}

func (fuzzRuntime) Subscribe(context.Context, opcda.SubscribeRequest) (opcda.Subscription, error) {
	return nil, opcda.NewAdapterError(opcda.CodeSubscribeUnsupported, "not exposed to the fuzzer")
}

func (fuzzRuntime) Unsubscribe(context.Context, opcda.SubscriptionID) error { return nil }
func (fuzzRuntime) Shutdown(context.Context) error                          { return nil }

// This source offers no OPC DA item properties. PROPERTIES_UNSUPPORTED is the
// same answer a real source without IOPCItemProperties gives.
func (fuzzRuntime) AvailableItemProperties(context.Context, string) ([]opcda.AvailableProperty, error) {
	return nil, opcda.NewAdapterError(opcda.CodePropertiesUnsupported, "this source offers no item properties")
}

func (fuzzRuntime) ItemProperties(context.Context, opcda.ItemPropertiesRequest) ([]opcda.ItemPropertyValue, error) {
	return nil, opcda.NewAdapterError(opcda.CodePropertiesUnsupported, "this source offers no item properties")
}
