package opcua

import (
	"testing"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// The rows are OPC 10000-8 Table A.2 exactly, plus the adapter's stated choice
// for VT_EMPTY and VT_NULL, which that table does not list.
func TestDataTypeMappingFollowsPart8TableA2(t *testing.T) {
	cases := []struct {
		varType opcda.DAVarType
		want    DataType
	}{
		{opcda.VTI2, DataTypeInt16},
		{opcda.VTI4, DataTypeInt32},
		{opcda.VTR4, DataTypeFloat},
		{opcda.VTR8, DataTypeDouble},
		{opcda.VTBSTR, DataTypeString},
		{opcda.VTBool, DataTypeBoolean},
		{opcda.VTUI1, DataTypeByte},
		{opcda.VTI1, DataTypeSByte},
		{opcda.VTUI2, DataTypeUInt16},
		{opcda.VTUI4, DataTypeUInt32},
		{opcda.VTI8, DataTypeInt64},
		{opcda.VTUI8, DataTypeUInt64},
		// Table A.2 maps VT_DATE to Double, not DateTime.
		{opcda.VTDate, DataTypeDouble},
		{opcda.VTDecimal, DataTypeDecimal},
		{opcda.VTEmpty, DataTypeNull},
		{opcda.VTNull, DataTypeNull},
	}
	for _, testCase := range cases {
		t.Run(testCase.varType.String(), func(t *testing.T) {
			got, ok := DataTypeFor(testCase.varType)
			if !ok {
				t.Fatalf("%s has no mapping", testCase.varType)
			}
			if got != testCase.want {
				t.Fatalf("%s mapped to %s, want %s", testCase.varType, got, testCase.want)
			}
		})
	}
}

func TestDataTypeMappingRejectsTypesTheSpecDoesNotList(t *testing.T) {
	// VT_CY has no row in Table A.2 and the DA core decodes no value for it, so
	// it must fail explicitly rather than borrow a similar type's mapping.
	//
	// VT_INT, VT_UINT and VT_ERROR have no row either, and this test used to
	// require them to fail for that reason. They do not any more, and the
	// difference is not coercion: the DA core reads all three out of the same
	// storage as VT_I4 and VT_UI4 and hands up an int32 or a uint32, so the
	// value a client receives *is* an Int32. Refusing to say so left a node
	// declaring the abstract base type while delivering an Int32 — the server
	// contradicting itself about one value, which is worse than either
	// consistent answer. See TestDataTypeAgreesWithWhatTheAdapterDelivers.
	for _, varType := range []opcda.DAVarType{opcda.VTCY} {
		if got, ok := DataTypeFor(varType); ok {
			t.Fatalf("%s was mapped to %s despite having no Table A.2 row", varType, got)
		}
	}
	// The DA core decodes no arrays or by-reference variants, so claiming the
	// Table A.2 array mapping would overstate what the adapter can carry.
	if _, ok := DataTypeFor(opcda.VTI4 | opcda.VTArray); ok {
		t.Fatal("an array VARTYPE was mapped")
	}
	if _, ok := DataTypeFor(opcda.VTI4 | opcda.VTByRef); ok {
		t.Fatal("a by-reference VARTYPE was mapped")
	}
}

// The rows are OPC 10000-8 Table A.3 exactly.
func TestQualityMappingFollowsPart8TableA3(t *testing.T) {
	cases := []struct {
		name    string
		quality uint16
		want    StatusCode
	}{
		{"GOOD", QualityGood, StatusGood},
		{"LOCAL_OVERRIDE", QualityLocalOverride, StatusGoodLocalOverride},
		{"UNCERTAIN", QualityUncertain, StatusUncertain},
		{"SUB_NORMAL", QualitySubNormal, StatusUncertainSubNormal},
		{"SENSOR_CAL", QualitySensorCal, StatusUncertainSensorNotAccurate},
		{"EGU_EXCEEDED", QualityEGUExceeded, StatusUncertainEngineeringUnitsExceeded},
		{"LAST_USABLE", QualityLastUsable, StatusUncertainLastUsableValue},
		{"BAD", QualityBad, StatusBad},
		{"CONFIG_ERROR", QualityConfigError, StatusBadConfigurationError},
		{"NOT_CONNECTED", QualityNotConnected, StatusBadNotConnected},
		{"COMM_FAILURE", QualityCommFailure, StatusBadNoCommunication},
		{"DEVICE_FAILURE", QualityDeviceFailure, StatusBadDeviceFailure},
		{"SENSOR_FAILURE", QualitySensorFailure, StatusBadSensorFailure},
		// Table A.3 maps both of these to Bad_OutOfService.
		{"LAST_KNOWN", QualityLastKnown, StatusBadOutOfService},
		{"OUT_OF_SERVICE", QualityOutOfService, StatusBadOutOfService},
		{"WAITING_FOR_INITIAL_DATA", QualityWaitingForInitialData, StatusBadWaitingForInitialData},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := StatusCodeForQuality(testCase.quality); got != testCase.want {
				t.Fatalf("quality 0x%02X mapped to %s, want %s", testCase.quality, got.Hex(), testCase.want.Hex())
			}
		})
	}
}

// The one quality this project has actually observed from a real DA server.
func TestQualityMappingMatchesTheObservedFixtureQuality(t *testing.T) {
	const observedGood = uint16(192) // 0xC0, recorded in docs/compatibility.md
	if got := StatusCodeForQuality(observedGood); got != StatusGood {
		t.Fatalf("observed fixture quality mapped to %s, want Good", got.Hex())
	}
}

func TestQualityMappingCarriesTheLimitField(t *testing.T) {
	cases := []struct {
		name  string
		limit uint16
		want  uint32
	}{
		{"none", 0, LimitNone},
		{"low", 1, LimitLow},
		{"high", 2, LimitHigh},
		{"constant", 3, LimitConstant},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			code := StatusCodeForQuality(QualityGood | testCase.limit)
			if !code.IsGood() {
				t.Fatalf("limit changed the severity: %s", code.Hex())
			}
			if got := code.LimitBits(); got != testCase.want {
				t.Fatalf("limit bits = %d, want %d", got, testCase.want)
			}
			if testCase.want == LimitNone {
				// With no limit the published status code must be untouched.
				if code != StatusGood {
					t.Fatalf("unlimited Good became %s", code.Hex())
				}
				if code.InfoType() != InfoTypeNotUsed {
					t.Fatalf("info type = %d, want NotUsed", code.InfoType())
				}
				return
			}
			if code.InfoType() != InfoTypeDataValue {
				t.Fatalf("info type = %d, want DataValue", code.InfoType())
			}
		})
	}
}

func TestQualityMappingDiscardsVendorBitsButKeepsThemObservable(t *testing.T) {
	// OPC 10000-8 A.3.2.3 requires the vendor byte to be discarded.
	withVendor := uint16(0xAB00) | QualityUncertain
	if got := StatusCodeForQuality(withVendor); got != StatusUncertain {
		t.Fatalf("vendor bits changed the mapping: %s", got.Hex())
	}
	if got := QualityVendorBits(withVendor); got != 0xAB {
		t.Fatalf("vendor bits = 0x%02X, want 0xAB", got)
	}
}

func TestQualityMappingFallsBackToMainQuality(t *testing.T) {
	// A sub status outside Table A.3 must still preserve good/uncertain/bad.
	cases := []struct {
		name    string
		quality uint16
		check   func(StatusCode) bool
		want    StatusCode
	}{
		{"unlisted good sub status", 0xC4, StatusCode.IsGood, StatusGood},
		{"unlisted uncertain sub status", 0x60, StatusCode.IsUncertain, StatusUncertain},
		{"unlisted bad sub status", 0x24, StatusCode.IsBad, StatusBad},
		// Main quality 10 is not defined by OPC DA and is treated as Bad.
		{"undefined main quality", 0x80, StatusCode.IsBad, StatusBad},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := StatusCodeForQuality(testCase.quality)
			if got != testCase.want {
				t.Fatalf("quality 0x%02X mapped to %s, want %s", testCase.quality, got.Hex(), testCase.want.Hex())
			}
			if !testCase.check(got) {
				t.Fatalf("quality 0x%02X lost its main quality: %s", testCase.quality, got.Hex())
			}
		})
	}
}

func TestReadAndWriteErrorMappingFollowsPart8TablesA4AndA5(t *testing.T) {
	if got := StatusCodeForReadError(OPCEBadRights); got != StatusBadNotReadable {
		t.Fatalf("read OPC_E_BADRIGHTS = %s, want Bad_NotReadable", got.Hex())
	}
	if got := StatusCodeForWriteError(OPCEBadRights); got != StatusBadNotWritable {
		t.Fatalf("write OPC_E_BADRIGHTS = %s, want Bad_NotWritable", got.Hex())
	}
	for _, mapper := range []func(opcda.HRESULT) StatusCode{StatusCodeForReadError, StatusCodeForWriteError} {
		if got := mapper(OPCEUnknownItemID); got != StatusBadNodeIdUnknown {
			t.Fatalf("OPC_E_UNKNOWNITEMID = %s, want Bad_NodeIdUnknown", got.Hex())
		}
		// Table A.4 and Table A.5 both end with an explicit "Others" row.
		if got := mapper(opcda.HRESULT(-2147467259)); got != StatusBadUnexpectedError {
			t.Fatalf("unbound HRESULT = %s, want Bad_UnexpectedError", got.Hex())
		}
		// A successful item is not an error; its condition lives in the quality.
		if got := mapper(opcda.SOK); got != StatusGood {
			t.Fatalf("S_OK = %s, want Good", got.Hex())
		}
	}
}

// The two DA error codes bound to numeric values are the ones this project has
// observed against a real server.
func TestBoundDAErrorCodesMatchTheObservedHRESULTs(t *testing.T) {
	if OPCEBadRights.Hex() != "0xC0040006" {
		t.Fatalf("OPC_E_BADRIGHTS = %s", OPCEBadRights.Hex())
	}
	if OPCEUnknownItemID.Hex() != "0xC0040007" {
		t.Fatalf("OPC_E_UNKNOWNITEMID = %s", OPCEUnknownItemID.Hex())
	}
}

// A node must not declare one type and deliver another. The DA core reads
// VT_INT and VT_ERROR into an int32 and VT_UINT into a uint32, so those are
// what a client receives; consulting OPC 10000-8 Table A.2 alone, which has no
// row for any of them, made the node declare the abstract base type instead.
func TestDataTypeAgreesWithWhatTheAdapterDelivers(t *testing.T) {
	for _, testCase := range []struct {
		varType opcda.DAVarType
		want    DataType
	}{
		{opcda.VTInt, DataTypeInt32},
		{opcda.VTError, DataTypeInt32},
		{opcda.VTUInt, DataTypeUInt32},
		// The types the table covers are unaffected.
		{opcda.VTI4, DataTypeInt32},
		{opcda.VTUI4, DataTypeUInt32},
		{opcda.VTBSTR, DataTypeString},
	} {
		t.Run(testCase.varType.Name(), func(t *testing.T) {
			got, ok := DataTypeFor(testCase.varType)
			if !ok {
				t.Fatalf("%s has no data type, but the adapter delivers one", testCase.varType)
			}
			if got != testCase.want {
				t.Fatalf("DataTypeFor = %q, want %q", got, testCase.want)
			}
		})
	}

	// A VARTYPE with no Table A.2 row still has no answer.
	for _, varType := range []opcda.DAVarType{opcda.VTCY, opcda.VTVariant} {
		if _, ok := DataTypeFor(varType); ok {
			t.Fatalf("%s was given a data type the table does not define", varType)
		}
	}

	// VT_DATE and VT_DECIMAL do have rows, and the transcription keeps them,
	// but the DA core decodes neither — so no value of either type ever reaches
	// a client and the rows are unreachable today. They are kept rather than
	// dropped because the table is what they transcribe; the limitation is the
	// decoder's and is recorded as such.
	for _, varType := range []opcda.DAVarType{opcda.VTDate, opcda.VTDecimal} {
		if _, ok := DataTypeFor(varType); !ok {
			t.Fatalf("%s lost its Table A.2 row", varType)
		}
	}
}
