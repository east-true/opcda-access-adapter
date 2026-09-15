package opcua

import "testing"

// ReadDeleteSubscriptionsResponse walks a header and then two variable-length
// arrays, and each step's error path was untested: inverting the header check
// and inverting the results-array length check both survived.
//
// The response is what a client decodes, so a malformed one comes from
// whatever the client is talking to. Carrying on past a truncated field leaves
// the client reading the next one out of the middle of this one -- a status
// code assembled from the bytes of a diagnostic, and no sign that anything
// went wrong.
//
// The bytes are built by encoding a valid response and then cutting it, so the
// shape stays honest: if the encoder changes, this changes with it, and the
// assertion is about the decoder refusing damage rather than about the two
// agreeing, which a round trip against itself could not show.
func TestATruncatedDeleteSubscriptionsResponseIsRefused(t *testing.T) {
	encoder, err := NewEncoder(DefaultBinaryLimits())
	if err != nil {
		t.Fatal(err)
	}
	encoder.WriteDeleteSubscriptionsResponse(DeleteSubscriptionsResponse{
		Header: ResponseHeader{
			RequestHandle: 9, ServiceResult: StatusGood, AdditionalHeader: NullExtensionObject(),
		},
		Results: []StatusCode{StatusGood, StatusBadSubscriptionIDInvalid},
		Diagnostics: []DiagnosticInfo{
			{HasSymbolicID: true, SymbolicID: 3},
			{HasAdditionalInfo: true, AdditionalInfo: "no such subscription"},
		},
	})
	complete, err := encoder.Bytes()
	if err != nil {
		t.Fatalf("encoding a valid response failed: %v", err)
	}

	decode := func(data []byte) (DeleteSubscriptionsResponse, error) {
		decoder, decodeErr := NewDecoder(data, DefaultBinaryLimits())
		if decodeErr != nil {
			return DeleteSubscriptionsResponse{}, decodeErr
		}
		// The dispatcher consumes the service type id before the body, so a
		// decode starting at the body steps over it the same way.
		if _, idErr := decoder.ReadServiceTypeID(); idErr != nil {
			return DeleteSubscriptionsResponse{}, idErr
		}
		return decoder.ReadDeleteSubscriptionsResponse()
	}

	// The whole thing decodes, or every cut below would be testing nothing.
	decoded, err := decode(complete)
	if err != nil {
		t.Fatalf("a complete response was refused: %v", err)
	}
	if len(decoded.Results) != 2 || decoded.Results[1] != StatusBadSubscriptionIDInvalid {
		t.Fatalf("results decoded as %v", decoded.Results)
	}
	if len(decoded.Diagnostics) != 2 {
		t.Fatalf("diagnostics decoded as %v", decoded.Diagnostics)
	}
	if decoded.Header.RequestHandle != 9 {
		t.Fatalf("the header decoded as %+v", decoded.Header)
	}

	// Every prefix short of the whole is damage, and none of them may decode.
	// Cutting at every length rather than at a chosen few is what reaches the
	// arrays' inner loops without having to work out where they begin.
	for cut := 1; cut < len(complete); cut++ {
		if _, err := decode(complete[:cut]); err == nil {
			t.Errorf("a response cut to %d of %d bytes decoded", cut, len(complete))
		}
	}
}
