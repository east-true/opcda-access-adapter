package opcua

import (
	"testing"
	"time"

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
		// Table A.3 maps both of these to Bad_OutOfService. LAST_KNOWN
		// deliberately does not follow it -- see the test below, which is
		// where the reason lives.
		{"LAST_KNOWN", QualityLastKnown, StatusUncertainNoCommunicationLastUsableValue},
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

// All thirteen rows of Tables A.4 and A.5 are bound, and spec-check compares
// each value against opcerror.h. These two are different in kind: they are the
// ones a real DA server has actually produced for this project, so their values
// are pinned to what the fixture returned rather than to what a header says.
// If the header and the fixture ever disagree, that is worth failing over.
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

// OPC 10000-8 Table A.4 - OPC DA Read error mapping, transcribed row for row.
//
// Only OPC_E_BADRIGHTS and OPC_E_UNKNOWNITEMID have been observed against a
// real server; the rest are transcribed and untested, which docs/compatibility.md
// records. They are bound anyway because the alternative is the table's "Others"
// row, which answers Bad_UnexpectedError for conditions the table gives a
// precise status for.
func TestReadErrorMappingFollowsPart8TableA4(t *testing.T) {
	for _, row := range []struct {
		daError string
		hresult opcda.HRESULT
		want    StatusCode
	}{
		{"OPC_E_BADRIGHTS", OPCEBadRights, StatusBadNotReadable},
		{"E_OUTOFMEMORY", EOutOfMemory, StatusBadOutOfMemory},
		{"OPC_E_INVALIDHANDLE", OPCEInvalidHandle, StatusBadNodeIdUnknown},
		{"OPC_E_UNKNOWNITEMID", OPCEUnknownItemID, StatusBadNodeIdUnknown},
		{"E_INVALIDITEMID", OPCEInvalidItemID, StatusBadNodeIdInvalid},
		{"E_INVALID_PID", OPCEInvalidPID, StatusBadAttributeIDInvalid},
		{"E_ACCESSDENIED", EAccessDenied, StatusBadOutOfService},
	} {
		t.Run(row.daError, func(t *testing.T) {
			if got := StatusCodeForReadError(row.hresult); got != row.want {
				t.Fatalf("status = %s, Table A.4 says %s", got.Hex(), row.want.Hex())
			}
		})
	}

	// "Others" is the table's own last row.
	if got := StatusCodeForReadError(opcda.HRESULT(-2147467259)); got != StatusBadUnexpectedError {
		t.Fatalf("an unlisted code mapped to %s, want Bad_UnexpectedError", got.Hex())
	}
	// A successful HRESULT is not an error; the quality carries the condition.
	if got := StatusCodeForReadError(opcda.SOK); got != StatusGood {
		t.Fatalf("S_OK mapped to %s", got.Hex())
	}
}

// OPC 10000-8 Table A.5 - OPC DA Write error code mapping, transcribed row for
// row. Same caveat about what has actually been observed.
func TestWriteErrorMappingFollowsPart8TableA5(t *testing.T) {
	for _, row := range []struct {
		daError string
		hresult opcda.HRESULT
		want    StatusCode
	}{
		{"E_BADRIGHTS", OPCEBadRights, StatusBadNotWritable},
		{"DISP_E_TYPEMISMATCH", DispETypeMismatch, StatusBadTypeMismatch},
		{"E_BADTYPE", OPCEBadType, StatusBadTypeMismatch},
		{"E_RANGE", OPCERange, StatusBadOutOfRange},
		{"DISP_E_OVERFLOW", DispEOverflow, StatusBadOutOfRange},
		{"E_OUTOFMEMORY", EOutOfMemory, StatusBadOutOfMemory},
		{"E_INVALIDHANDLE", OPCEInvalidHandle, StatusBadNodeIdUnknown},
		{"E_UNKNOWNITEMID", OPCEUnknownItemID, StatusBadNodeIdUnknown},
		{"E_INVALIDITEMID", OPCEInvalidItemID, StatusBadNodeIdInvalid},
		{"E_INVALID_PID", OPCEInvalidPID, StatusBadNodeIdInvalid},
		{"E_NOTSUPPORTED", OPCENotSupported, StatusBadWriteNotSupported},
		{"S_CLAMP", OPCSClamp, StatusGoodClamped},
	} {
		t.Run(row.daError, func(t *testing.T) {
			if got := StatusCodeForWriteError(row.hresult); got != row.want {
				t.Fatalf("status = %s, Table A.5 says %s", got.Hex(), row.want.Hex())
			}
		})
	}

	if got := StatusCodeForWriteError(opcda.HRESULT(-2147467259)); got != StatusBadUnexpectedError {
		t.Fatalf("an unlisted code mapped to %s, want Bad_UnexpectedError", got.Hex())
	}
	if got := StatusCodeForWriteError(opcda.SOK); got != StatusGood {
		t.Fatalf("S_OK mapped to %s", got.Hex())
	}
}

// The tables give OPC_E_INVALID_PID different answers in each direction:
// Bad_AttributeIdInvalid when reading, Bad_NodeIdInvalid when writing. The
// asymmetry is the specification's, and each direction follows its own table
// rather than being reconciled into one answer.
func TestInvalidPropertyIDKeepsEachTableSAnswer(t *testing.T) {
	if got := StatusCodeForReadError(OPCEInvalidPID); got != StatusBadAttributeIDInvalid {
		t.Fatalf("read status = %s, Table A.4 says Bad_AttributeIdInvalid", got.Hex())
	}
	if got := StatusCodeForWriteError(OPCEInvalidPID); got != StatusBadNodeIdInvalid {
		t.Fatalf("write status = %s, Table A.5 says Bad_NodeIdInvalid", got.Hex())
	}
}

// OPC_S_CLAMP is a success code: the write happened, but the source stored a
// value other than the one asked for. Answering the general success case first
// would report Good and lose that.
func TestClampedWriteIsNotReportedAsPlainGood(t *testing.T) {
	if !OPCSClamp.Succeeded() {
		t.Fatal("OPC_S_CLAMP is a success code")
	}
	if got := StatusCodeForWriteError(OPCSClamp); got != StatusGoodClamped {
		t.Fatalf("status = %s, want Good_Clamped", got.Hex())
	}
}

// LAST_KNOWN is the one row where this adapter does not follow Table A.3, and
// the reason is that two clauses of Part 8 disagree while only one explains
// itself.
//
// Table A.3 maps LAST_KNOWN to Bad_OutOfService. Table 61 says the fieldbus
// code Bad_LastKnown "shall be mapped to Uncertain_NoCommunicationLastUsable",
// because "OPC UA requires that the Server shall return a Null value when the
// Severity is Bad".
//
// That reason holds exactly here: LAST_KNOWN exists to deliver the last value
// that had good quality, and a Bad severity means the adapter must drop the
// value. Following Table A.3 destroys the thing the quality is for.
func TestLastKnownKeepsTheValueItExistsToCarry(t *testing.T) {
	status := StatusCodeForQuality(QualityLastKnown)
	if status.IsBad() {
		t.Fatalf("LAST_KNOWN is %s, which is Bad, so the value would be dropped", status.Hex())
	}
	if status != StatusUncertainNoCommunicationLastUsableValue {
		t.Fatalf("LAST_KNOWN is %s, Table 61 names Uncertain_NoCommunicationLastUsable", status.Hex())
	}
	// OUT_OF_SERVICE is a different condition and keeps the table's answer:
	// there is no last known value to protect, the source is simply not
	// operational.
	if got := StatusCodeForQuality(QualityOutOfService); got != StatusBadOutOfService {
		t.Fatalf("OUT_OF_SERVICE is %s, Table A.3 says Bad_OutOfService", got.Hex())
	}
	// The two used to be indistinguishable. They are not any more.
	if StatusCodeForQuality(QualityLastKnown) == StatusCodeForQuality(QualityOutOfService) {
		t.Fatal("LAST_KNOWN and OUT_OF_SERVICE map to the same code again")
	}
}

// The point of the change is that the value survives, so that is what is
// checked rather than only the code.
func TestALastKnownValueReachesTheClient(t *testing.T) {
	varType := opcda.VTI4
	value := dataValueForRead(opcda.ReadResult{
		ItemID: "Test/Int32", VarType: &varType, HRESULT: opcda.SOK, HRESULTPresent: true,
		Value: &opcda.DAValue{
			ItemID: "Test/Int32", VarType: varType, Value: int32(7),
			QualityRaw: QualityLastKnown, HRESULT: opcda.SOK,
		},
	}, TimestampsBoth, time.Now())

	if value.Status.IsBad() {
		t.Fatalf("status = %s", value.Status.Hex())
	}
	if value.Value.Value != int32(7) {
		t.Fatalf("the last known value did not survive: %#v", value.Value.Value)
	}
}
