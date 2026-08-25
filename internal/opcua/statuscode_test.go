package opcua

import "testing"

// Bit positions are OPC 10000-4 Table 176 and Table 177.
func TestStatusCodeBitAssignments(t *testing.T) {
	cases := []struct {
		name     string
		code     StatusCode
		severity uint32
		good     bool
		bad      bool
	}{
		{"Good", StatusGood, SeverityGood, true, false},
		{"Uncertain", StatusUncertain, SeverityUncertain, false, false},
		{"Bad", StatusBad, SeverityBad, false, true},
		// Severity 11 is reserved and clients must treat it as Bad.
		{"reserved severity", StatusCode(0xC0000000), SeverityReserved, false, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.code.Severity(); got != testCase.severity {
				t.Fatalf("severity = %d, want %d", got, testCase.severity)
			}
			if testCase.code.IsGood() != testCase.good {
				t.Fatalf("IsGood = %t, want %t", testCase.code.IsGood(), testCase.good)
			}
			if testCase.code.IsBad() != testCase.bad {
				t.Fatalf("IsBad = %t, want %t", testCase.code.IsBad(), testCase.bad)
			}
		})
	}

	// SubCode occupies bits 16:27, so a published code's identity survives.
	if got := StatusBadOutOfService & 0x0FFF0000 >> 16; got != 0x08D {
		t.Fatalf("Bad_OutOfService sub code = 0x%03X, want 0x08D", got)
	}
}

func TestWithLimitBitsIsReversibleAndBounded(t *testing.T) {
	for _, limit := range []uint32{LimitNone, LimitLow, LimitHigh, LimitConstant} {
		code := StatusUncertain.WithLimitBits(limit)
		if got := code.LimitBits(); got != limit {
			t.Fatalf("limit %d round-tripped to %d", limit, got)
		}
		if !code.IsUncertain() {
			t.Fatalf("limit %d changed the severity: %s", limit, code.Hex())
		}
		// Replacing a limit must not accumulate bits.
		if got := code.WithLimitBits(LimitNone); got != StatusUncertain {
			t.Fatalf("clearing the limit left %s", got.Hex())
		}
	}
	// Values wider than two bits cannot escape the limit field.
	if got := StatusGood.WithLimitBits(0xFF).LimitBits(); got != LimitConstant {
		t.Fatalf("oversized limit produced %d", got)
	}
}

func TestLimitBitsAreIgnoredWhenInfoTypeIsNotDataValue(t *testing.T) {
	// Info bits are meaningless unless InfoType says they describe a DataValue.
	raw := StatusCode(uint32(StatusGood) | 0x0300)
	if raw.InfoType() != InfoTypeNotUsed {
		t.Fatalf("info type = %d, want NotUsed", raw.InfoType())
	}
	if got := raw.LimitBits(); got != LimitNone {
		t.Fatalf("limit bits = %d, want None when the info type is unused", got)
	}
}

func TestStatusCodeHex(t *testing.T) {
	if got := StatusBadNodeIdUnknown.Hex(); got != "0x80340000" {
		t.Fatalf("hex = %s", got)
	}
}
