package opcua

import "testing"

// A field that is simply not present must be written as a null String, not as
// a zero-length one. The two are distinct on the wire, and a receiver that
// distinguishes them reads a zero-length string as "specified, and empty".
//
// Table 192 says issuedTokenType "may only be specified if TokenType is
// ISSUEDTOKEN", so writing an empty one on an ANONYMOUS policy specifies a
// field the clause forbids. The OPC Foundation's own .NET stack refused the
// endpoint with Bad_IdentityTokenInvalid because of it, and no client of this
// project's own noticed, because its decoder reads null and empty alike.
func TestUnspecifiedEndpointStringsAreWrittenAsNull(t *testing.T) {
	encoder, err := NewEncoder(DefaultBinaryLimits())
	if err != nil {
		t.Fatal(err)
	}
	encoder.WriteUserTokenPolicy(UserTokenPolicy{
		PolicyID:  "anonymous",
		TokenType: UserTokenTypeAnonymous,
	})
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	decoder, err := NewDecoder(encoded, DefaultBinaryLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := decoder.ReadString(); err != nil { // policyId
		t.Fatal(err)
	}
	if _, err := decoder.ReadInt32(); err != nil { // tokenType
		t.Fatal(err)
	}
	for _, field := range []string{"issuedTokenType", "issuerEndpointUrl", "securityPolicyUri"} {
		value, isNull, readErr := decoder.ReadString()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !isNull {
			t.Fatalf("%s was written as a %d byte string, want null", field, len(value))
		}
	}
}

// The same rule for the two ApplicationDescription fields the clause calls out
// as null-or-empty when they do not apply.
func TestUnspecifiedApplicationDescriptionStringsAreWrittenAsNull(t *testing.T) {
	encoder, err := NewEncoder(DefaultBinaryLimits())
	if err != nil {
		t.Fatal(err)
	}
	encoder.WriteApplicationDescription(ApplicationDescription{
		ApplicationURI:  "urn:example:adapter",
		ProductURI:      "urn:example:adapter",
		ApplicationName: LocalizedText{Text: "Adapter"},
		ApplicationType: ApplicationTypeServer,
		DiscoveryURLs:   []string{"opc.tcp://127.0.0.1:4840"},
	})
	encoded, err := encoder.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	decoder, err := NewDecoder(encoded, DefaultBinaryLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"applicationUri", "productUri"} {
		if _, isNull, readErr := decoder.ReadString(); readErr != nil {
			t.Fatal(readErr)
		} else if isNull {
			t.Fatalf("%s was written as null", field)
		}
	}
	if _, err := decoder.ReadLocalizedText(); err != nil {
		t.Fatal(err)
	}
	if _, err := decoder.ReadInt32(); err != nil { // applicationType
		t.Fatal(err)
	}
	for _, field := range []string{"gatewayServerUri", "discoveryProfileUri"} {
		if _, isNull, readErr := decoder.ReadString(); readErr != nil {
			t.Fatal(readErr)
		} else if !isNull {
			t.Fatalf("%s was written as an empty string, want null", field)
		}
	}
}
