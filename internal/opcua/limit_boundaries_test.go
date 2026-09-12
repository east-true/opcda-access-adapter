package opcua

import (
	"context"
	"strings"
	"testing"
)

// This project bounds everything it reads and documents every bound, and the
// mutation sweep found that almost none of them is tested at the bound itself.
// Loosening a comparison by one -- `>` to `>=` -- survived at six places, which
// means each limit was only ever exercised well inside or well outside, never
// at the value that decides which side of the line it falls on.
//
// Off by one in these is not cosmetic. Tightening by one refuses a request the
// documented limit says is allowed; loosening by one admits a request the
// limit exists to refuse. Every case below pairs "exactly the limit is
// accepted" with "one more is refused", because only the pair pins the line.

func TestMessageSizeAtTheReceiveBufferIsAccepted(t *testing.T) {
	const buffer uint32 = 8192
	header := func(size uint32) []byte {
		return []byte{'M', 'S', 'G', 'F',
			byte(size), byte(size >> 8), byte(size >> 16), byte(size >> 24)}
	}

	// Exactly the negotiated buffer is a legal message: 7.1.2 makes the buffer
	// the largest a peer may send, not the first size it may not.
	decoded, err := DecodeMessageHeader(header(buffer), buffer)
	if err != nil {
		t.Fatalf("a message of exactly the receive buffer was refused: %v", err)
	}
	if decoded.Size != buffer {
		t.Fatalf("size = %d, want %d", decoded.Size, buffer)
	}

	if _, err := DecodeMessageHeader(header(buffer+1), buffer); err == nil {
		t.Fatal("a message one byte over the receive buffer was accepted")
	}

	// The other end of the same field: a size below the header itself.
	if _, err := DecodeMessageHeader(header(HeaderSize), buffer); err != nil {
		t.Fatalf("a message of exactly the header size was refused: %v", err)
	}
	if _, err := DecodeMessageHeader(header(HeaderSize-1), buffer); err == nil {
		t.Fatal("a message one byte shorter than the header was accepted")
	}
}

func TestPercentDeadbandAcceptsItsWholeRange(t *testing.T) {
	limits := DefaultBinaryLimits()
	for _, testCase := range []struct {
		name  string
		value float64
		want  StatusCode
	}{
		// A.3.5 makes this a percentage, so nought and a hundred are both
		// inside it. Nought means "report every change", which is a request a
		// client can legitimately make.
		{"zero percent", 0, StatusGood},
		{"one hundred percent", 100, StatusGood},
		{"just over one hundred", 100.0001, StatusBadDeadbandFilterInvalid},
		{"negative", -0.0001, StatusBadDeadbandFilterInvalid},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			filter := dataChangeFilter(t, DataChangeTriggerStatusValue, DeadbandPercent, testCase.value)
			_, status := deadbandForFilter(filter, limits)
			if status != testCase.want {
				t.Errorf("deadband %v = %s, want %s", testCase.value, status.Hex(), testCase.want.Hex())
			}
		})
	}
}

func TestSecurityPolicyURIAtTheByteLimitIsAccepted(t *testing.T) {
	config := testEndpointConfig()

	config.SecurityPolicyURI = strings.Repeat("u", MaxSecurityPolicyURIBytes)
	if err := config.validate(); err != nil {
		t.Fatalf("a URI of exactly %d bytes was refused: %v", MaxSecurityPolicyURIBytes, err)
	}

	config.SecurityPolicyURI = strings.Repeat("u", MaxSecurityPolicyURIBytes+1)
	if err := config.validate(); err == nil {
		t.Fatalf("a URI of %d bytes was accepted", MaxSecurityPolicyURIBytes+1)
	}
}

func TestArrayLengthAtTheLimitIsEncodable(t *testing.T) {
	limits := DefaultBinaryLimits()

	encoder, err := NewEncoder(limits)
	if err != nil {
		t.Fatal(err)
	}
	encoder.WriteArrayLength(limits.MaxArrayLength)
	if _, err := encoder.Bytes(); err != nil {
		t.Fatalf("an array of exactly %d elements was refused: %v", limits.MaxArrayLength, err)
	}

	encoder, err = NewEncoder(limits)
	if err != nil {
		t.Fatal(err)
	}
	encoder.WriteArrayLength(limits.MaxArrayLength + 1)
	if _, err := encoder.Bytes(); err == nil {
		t.Fatalf("an array of %d elements was accepted", limits.MaxArrayLength+1)
	}
}

func TestBrowseAtTheNodeLimitIsAccepted(t *testing.T) {
	limits := DefaultBrowseLimits()
	service, _ := testBrowseService(t, limits)

	nodes := func(count int) []BrowseDescription {
		described := make([]BrowseDescription, count)
		for i := range described {
			described[i] = browseAll(NumericNodeID(0, NodeIDObjectsFolder))
		}
		return described
	}

	response, err := service.Browse(context.Background(), testSession,
		browseRequest(nodes(limits.MaxNodesPerBrowse)...), channelEpoch)
	if err != nil {
		t.Fatalf("a Browse of exactly %d nodes was refused: %v", limits.MaxNodesPerBrowse, err)
	}
	if len(response.Results) != limits.MaxNodesPerBrowse {
		t.Fatalf("results = %d, want %d", len(response.Results), limits.MaxNodesPerBrowse)
	}

	if _, err := service.Browse(context.Background(), testSession,
		browseRequest(nodes(limits.MaxNodesPerBrowse+1)...), channelEpoch); err == nil {
		t.Fatalf("a Browse of %d nodes was accepted", limits.MaxNodesPerBrowse+1)
	}
}
