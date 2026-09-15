package opcda

import "testing"

// The detection limits have a hard ceiling as well as a floor, and only the
// floor had a case. A ceiling that drifted inward would refuse a value the
// bound itself names, which is the same defect as every other unpinned bound
// here: nothing said a setting of exactly the documented shape is accepted.
func TestDetectionLimitsReachTheirCeilings(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		limits   LocalDetectionLimits
		accepted bool
	}{
		{"the defaults", DefaultLocalDetectionLimits(), true},
		{
			name:     "exactly the server ceiling",
			limits:   LocalDetectionLimits{MaxServers: maximumDetectedServers, MaxProgIDCodeUnits: 64},
			accepted: true,
		},
		{
			name:     "one server past the ceiling",
			limits:   LocalDetectionLimits{MaxServers: maximumDetectedServers + 1, MaxProgIDCodeUnits: 64},
			accepted: false,
		},
		{
			name:     "exactly the ProgID ceiling",
			limits:   LocalDetectionLimits{MaxServers: 8, MaxProgIDCodeUnits: maximumProgIDCodeUnits},
			accepted: true,
		},
		{
			name:     "one code unit past the ceiling",
			limits:   LocalDetectionLimits{MaxServers: 8, MaxProgIDCodeUnits: maximumProgIDCodeUnits + 1},
			accepted: false,
		},
		{
			name:     "the smallest of each",
			limits:   LocalDetectionLimits{MaxServers: 1, MaxProgIDCodeUnits: 1},
			accepted: true,
		},
		{
			name:     "a negative server limit",
			limits:   LocalDetectionLimits{MaxServers: -1, MaxProgIDCodeUnits: 64},
			accepted: false,
		},
		{
			name:     "a negative ProgID limit",
			limits:   LocalDetectionLimits{MaxServers: 8, MaxProgIDCodeUnits: -1},
			accepted: false,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.limits.Validate()
			if accepted := err == nil; accepted != testCase.accepted {
				t.Errorf("accepted = %v, want %v (%v)", accepted, testCase.accepted, err)
			}
		})
	}
}

// VT_EMPTY and VT_NULL are the two VARTYPEs that carry no value, so what makes
// a Write of one valid is that nothing came with it. Inverting the check makes
// the rule its own opposite: a Write of VT_EMPTY carrying a number would be
// accepted and one carrying nothing refused, which is the adapter inventing a
// value for a type defined as having none.
func TestAnEmptyWriteValueIsTheAbsenceOfOne(t *testing.T) {
	for _, varType := range []DAVarType{VTEmpty, VTNull} {
		t.Run(varType.String(), func(t *testing.T) {
			if err := validateWriteValue(varType, nil, 64); err != nil {
				t.Errorf("a %s Write carrying nothing was refused: %v", varType, err)
			}
			for _, value := range []any{int32(0), "", false, []byte{}, 0.0} {
				if err := validateWriteValue(varType, value, 64); err == nil {
					t.Errorf("a %s Write carrying %#v was accepted", varType, value)
				}
			}
		})
	}

	// The control: a type that does carry a value still requires the right
	// one, so the case above is about absence rather than about everything
	// being refused.
	if err := validateWriteValue(VTI4, int32(1), 64); err != nil {
		t.Errorf("a VT_I4 Write carrying an int32 was refused: %v", err)
	}
	if err := validateWriteValue(VTI4, nil, 64); err == nil {
		t.Error("a VT_I4 Write carrying nothing was accepted")
	}
}
