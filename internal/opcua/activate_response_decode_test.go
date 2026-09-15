package opcua

import (
	"testing"
)

// ReadActivateSessionResponse walks three variable-length fields in a row --
// the server nonce, the per-token results, and the diagnostics -- and each has
// an error path the sweep found untested: inverting the diagnostic array's
// error check survived.
//
// The response is what a client decodes, so a malformed one comes from
// whatever the client is talking to. Failing to notice a truncated diagnostic
// would leave the client reading the next field out of the middle of this one.
//
// The bytes are built by encoding a valid response and then cutting it, rather
// than by hand. That keeps the test honest about the shape -- if the encoder
// changes, this changes with it -- and the assertion is about the decoder
// refusing damage rather than about the two agreeing, which a round trip
// against itself could not show.

func encodedActivateResponse(t *testing.T, response ActivateSessionResponse) []byte {
	t.Helper()
	encoder, err := NewEncoder(DefaultBinaryLimits())
	if err != nil {
		t.Fatal(err)
	}
	encoder.WriteActivateSessionResponse(response)
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatalf("encoding a valid response failed: %v", err)
	}
	return encoded
}

func decodeActivateResponse(t *testing.T, data []byte) (ActivateSessionResponse, error) {
	t.Helper()
	decoder, err := NewDecoder(data, DefaultBinaryLimits())
	if err != nil {
		return ActivateSessionResponse{}, err
	}
	// The dispatcher consumes the service type id before the body, so a decode
	// starting at the body has to step over it the same way.
	if _, err := decoder.ReadServiceTypeID(); err != nil {
		return ActivateSessionResponse{}, err
	}
	return decoder.ReadActivateSessionResponse()
}

func TestATruncatedActivateResponseIsRefusedAtEveryField(t *testing.T) {
	complete := encodedActivateResponse(t, ActivateSessionResponse{
		ServerNonce: []byte("0123456789abcdef0123456789abcdef"),
		Results:     []StatusCode{StatusGood, StatusBadIdentityTokenInvalid},
		Diagnostics: []DiagnosticInfo{
			{HasSymbolicID: true, SymbolicID: 7},
			{HasAdditionalInfo: true, AdditionalInfo: "the token was refused"},
		},
	})

	// The whole thing decodes, or every cut below would be testing nothing.
	decoded, err := decodeActivateResponse(t, complete)
	if err != nil {
		t.Fatalf("a complete response was refused: %v", err)
	}
	if len(decoded.Results) != 2 || len(decoded.Diagnostics) != 2 {
		t.Fatalf("decoded %d results and %d diagnostics, want 2 and 2",
			len(decoded.Results), len(decoded.Diagnostics))
	}
	if string(decoded.ServerNonce) != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("the nonce decoded as %q", decoded.ServerNonce)
	}

	// Every prefix short of the whole is damage, and none of them may decode.
	// Cutting at every length rather than at a chosen few is what reaches the
	// arrays' inner loops without having to work out where they begin.
	for cut := 1; cut < len(complete); cut++ {
		if _, err := decodeActivateResponse(t, complete[:cut]); err == nil {
			t.Errorf("a response cut to %d of %d bytes decoded", cut, len(complete))
		}
	}
}

// A response carrying no diagnostics at all is normal: the field is optional
// and a server that has nothing to add sends an empty array. Refusing it would
// break every well-behaved peer.
func TestAnActivateResponseWithoutDiagnosticsDecodes(t *testing.T) {
	encoded := encodedActivateResponse(t, ActivateSessionResponse{
		ServerNonce: []byte("nonce"),
		Results:     []StatusCode{StatusGood},
	})
	decoded, err := decodeActivateResponse(t, encoded)
	if err != nil {
		t.Fatalf("a response with no diagnostics was refused: %v", err)
	}
	if len(decoded.Results) != 1 || decoded.Results[0] != StatusGood {
		t.Errorf("results decoded as %v", decoded.Results)
	}
	if len(decoded.Diagnostics) != 0 {
		t.Errorf("diagnostics decoded as %v, want none", decoded.Diagnostics)
	}
}
