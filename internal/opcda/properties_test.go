package opcda

import "testing"

// The value, quality and timestamp of an item are available as properties 2, 3
// and 4, and the adapter deliberately does not read them that way. Read and
// Subscribe carry the value with its timestamp and its raw quality together;
// a property fetch would answer the same question a second time, without
// them, and the two answers could differ.
func TestValueQualityAndTimestampAreNotReadAsProperties(t *testing.T) {
	if PropertyValue != 2 || PropertyQuality != 3 || PropertyTimestamp != 4 {
		t.Fatalf("value/quality/timestamp = %d/%d/%d", PropertyValue, PropertyQuality, PropertyTimestamp)
	}
}

// Table A.1 is written in terms of these identifiers, so the mapping cannot be
// implemented without them. scripts/spec-check/check.py checks every value
// against opcda.idl; this pins the set that Table A.1 actually names.
func TestTableA1PropertyIdentifiersAreDeclared(t *testing.T) {
	for _, testCase := range []struct {
		name string
		id   PropertyID
		want PropertyID
	}{
		{"Access Rights", PropertyAccessRights, 5},
		{"EU Units", PropertyEUUnits, 100},
		{"Item Description", PropertyDescription, 101},
		{"High EU", PropertyHighEU, 102},
		{"Low EU", PropertyLowEU, 103},
		{"High Instrument Range", PropertyHighIR, 104},
		{"Low Instrument Range", PropertyLowIR, 105},
		{"Close Label", PropertyCloseLabel, 106},
		{"Open Label", PropertyOpenLabel, 107},
	} {
		if testCase.id != testCase.want {
			t.Errorf("%s = %d, Table A.1 names %d", testCase.name, testCase.id, testCase.want)
		}
	}
}

// A source without IOPCItemProperties is working correctly. It has to be
// distinguishable from one that has not been asked yet, which is why the
// capability is a string rather than a bool -- the same reason Browse is.
func TestPropertiesCapabilityDistinguishesUnsupportedFromUnavailable(t *testing.T) {
	capabilities := Capabilities{Properties: "unsupported"}
	if capabilities.Properties == "unavailable" {
		t.Fatal("unsupported and unavailable must not be the same answer")
	}
	if DefaultLimits().MaxItemProperties <= 0 {
		t.Fatal("the per-item property bound must be positive")
	}
}

// Every limit is bounded, and a new one that nobody bounded would be a way to
// ask a source for an unbounded amount of work.
func TestItemPropertyLimitIsValidated(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxItemProperties = 0
	if err := limits.validate(); err == nil {
		t.Fatal("a zero item-property limit was accepted")
	}
	limits.MaxItemProperties = 1 << 20
	if err := limits.validate(); err == nil {
		t.Fatal("an item-property limit above the hard ceiling was accepted")
	}
}
