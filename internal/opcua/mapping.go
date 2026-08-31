package opcua

import "github.com/east-true/opcda-access-adapter/internal/opcda"

// DataType is a symbolic OPC UA built-in DataType name. Numeric built-in
// identifiers belong with the UA Binary encoder and are deliberately not bound
// here, so this package commits only to the mapping itself.
type DataType string

const (
	DataTypeBoolean DataType = "Boolean"
	DataTypeSByte   DataType = "SByte"
	DataTypeByte    DataType = "Byte"
	DataTypeInt16   DataType = "Int16"
	DataTypeUInt16  DataType = "UInt16"
	DataTypeInt32   DataType = "Int32"
	DataTypeUInt32  DataType = "UInt32"
	DataTypeInt64   DataType = "Int64"
	DataTypeUInt64  DataType = "UInt64"
	DataTypeFloat   DataType = "Float"
	DataTypeDouble  DataType = "Double"
	DataTypeString  DataType = "String"
	DataTypeDecimal DataType = "Decimal"
	// DataTypeNull represents a DataValue carrying no value. VT_EMPTY and
	// VT_NULL have no row in OPC 10000-8 Table A.2; this is the adapter's
	// stated choice, recorded in ADR-0016.
	DataTypeNull DataType = "Null"
)

// DataTypeFor answers what UA DataType this adapter delivers for a DA VARTYPE:
// OPC 10000-8 Table A.2 applied to the type the DA core decodes the VARTYPE as.
// It reports false when the specification defines no mapping, so an unmapped
// VARTYPE fails explicitly instead of being coerced.
//
// The composition with DecodesAs is what keeps the answer honest. The table has
// no row for VT_INT, VT_UINT or VT_ERROR, but the DA core reads all three into
// an int32 or a uint32 and the Variant a client receives says exactly that. A
// node that consulted the table alone declared such an item to be the abstract
// base type while delivering an Int32 — the server contradicting itself about
// one value. Deriving both answers from the type the adapter actually produces
// removes the possibility rather than adding three special cases.
//
// Arrays and by-reference variants are rejected here. Table A.2 maps VT_ARRAY
// to an array of the mapped element type, but the DA core does not decode
// arrays, so claiming the mapping would overstate what the adapter can carry.
func DataTypeFor(varType opcda.DAVarType) (DataType, bool) {
	if varType.IsArray() || varType.IsByRef() {
		return "", false
	}
	return dataTypeFromTableA2(varType.DecodesAs())
}

// dataTypeFromTableA2 is the transcription of the table itself, with no
// adapter-specific normalisation. It is unexported because no caller wants the
// table's answer about a VARTYPE the adapter never delivers.
func dataTypeFromTableA2(varType opcda.DAVarType) (DataType, bool) {
	switch varType.Base() {
	case opcda.VTEmpty, opcda.VTNull:
		return DataTypeNull, true
	case opcda.VTI1:
		return DataTypeSByte, true
	case opcda.VTUI1:
		return DataTypeByte, true
	case opcda.VTI2:
		return DataTypeInt16, true
	case opcda.VTUI2:
		return DataTypeUInt16, true
	case opcda.VTI4:
		return DataTypeInt32, true
	case opcda.VTUI4:
		return DataTypeUInt32, true
	case opcda.VTI8:
		return DataTypeInt64, true
	case opcda.VTUI8:
		return DataTypeUInt64, true
	case opcda.VTR4:
		return DataTypeFloat, true
	case opcda.VTR8:
		return DataTypeDouble, true
	case opcda.VTBool:
		return DataTypeBoolean, true
	case opcda.VTBSTR:
		return DataTypeString, true
	case opcda.VTDecimal:
		return DataTypeDecimal, true
	case opcda.VTDate:
		// Table A.2 maps VT_DATE to Double, not DateTime.
		return DataTypeDouble, true
	default:
		// VT_INT, VT_UINT, VT_ERROR, VT_CY and everything else have no row in
		// Table A.2. They are reported as unmapped rather than guessed at.
		return "", false
	}
}

// OPC DA quality is a 16-bit field whose lower 8 bits are QQSSSSLL: main
// quality, sub status, and limit. The upper 8 bits are vendor specific.
// OPC 10000-8 A.3.2.3 states that the vendor quality is discarded by the
// mapping, so the DA core keeps the raw value and only this mapping drops it.
const (
	qualityLimitMask   = 0x03
	qualityMainAndSub  = 0xFC
	qualityVendorShift = 8
)

// Standard OPC DA 2.05a quality values, expressed as the QQSSSS field with the
// limit bits cleared. Only GOOD has been confirmed against a real server in
// this project; see docs/compatibility.md.
const (
	QualityBad                   uint16 = 0x00
	QualityConfigError           uint16 = 0x04
	QualityNotConnected          uint16 = 0x08
	QualityDeviceFailure         uint16 = 0x0C
	QualitySensorFailure         uint16 = 0x10
	QualityLastKnown             uint16 = 0x14
	QualityCommFailure           uint16 = 0x18
	QualityOutOfService          uint16 = 0x1C
	QualityWaitingForInitialData uint16 = 0x20

	QualityUncertain   uint16 = 0x40
	QualityLastUsable  uint16 = 0x44
	QualitySensorCal   uint16 = 0x50
	QualityEGUExceeded uint16 = 0x54
	QualitySubNormal   uint16 = 0x58

	QualityGood          uint16 = 0xC0
	QualityLocalOverride uint16 = 0xD8
)

// StatusCodeForQuality maps a raw DA quality onto a UA status code following
// OPC 10000-8 Table A.3, then attaches the DA limit field as UA limit bits per
// A.3.2.3. The vendor-specific upper byte is discarded, which the specification
// requires and which this adapter documents as a known loss.
//
// A QQSSSS combination outside Table A.3 falls back to its main-quality status
// code, so an unlisted vendor sub status still preserves good/uncertain/bad
// rather than being reported as an unexpected error.
func StatusCodeForQuality(raw uint16) StatusCode {
	limit := uint32(raw & qualityLimitMask)
	mainAndSub := raw & qualityMainAndSub

	var code StatusCode
	switch mainAndSub {
	case QualityGood:
		code = StatusGood
	case QualityLocalOverride:
		code = StatusGoodLocalOverride
	case QualityUncertain:
		code = StatusUncertain
	case QualitySubNormal:
		code = StatusUncertainSubNormal
	case QualitySensorCal:
		code = StatusUncertainSensorNotAccurate
	case QualityEGUExceeded:
		code = StatusUncertainEngineeringUnitsExceeded
	case QualityLastUsable:
		code = StatusUncertainLastUsableValue
	case QualityBad:
		code = StatusBad
	case QualityConfigError:
		code = StatusBadConfigurationError
	case QualityNotConnected:
		code = StatusBadNotConnected
	case QualityCommFailure:
		code = StatusBadNoCommunication
	case QualityDeviceFailure:
		code = StatusBadDeviceFailure
	case QualitySensorFailure:
		code = StatusBadSensorFailure
	case QualityLastKnown:
		// Table A.3 maps LAST_KNOWN to Bad_OutOfService alongside
		// OUT_OF_SERVICE. Table 61 contradicts it and explains itself: the
		// fieldbus code Bad_LastKnown "shall be mapped to
		// Uncertain_NoCommunicationLastUsable" because "OPC UA requires that
		// the Server shall return a Null value when the Severity is Bad".
		//
		// That reason holds here. LAST_KNOWN exists to deliver the last value
		// that had good quality, and a Bad severity drops the value, so
		// following Table A.3 destroys exactly what the quality is for. The
		// clause that explains itself is the one followed, and
		// scripts/spec-check/check.py records the deviation rather than
		// leaving it to be noticed.
		code = StatusUncertainNoCommunicationLastUsableValue
	case QualityOutOfService:
		code = StatusBadOutOfService
	case QualityWaitingForInitialData:
		code = StatusBadWaitingForInitialData
	default:
		code = mainQualityStatusCode(mainAndSub)
	}
	return code.WithLimitBits(limit)
}

// mainQualityStatusCode reduces an unlisted QQSSSS value to its main quality.
// OPC DA defines the main quality in bits 6:7 as 00 Bad, 01 Uncertain, and
// 11 Good; 10 is not defined and is treated as Bad.
func mainQualityStatusCode(mainAndSub uint16) StatusCode {
	switch mainAndSub & 0xC0 {
	case 0xC0:
		return StatusGood
	case 0x40:
		return StatusUncertain
	default:
		return StatusBad
	}
}

// QualityVendorBits returns the vendor-specific upper byte that the UA mapping
// discards. It exists so the loss can be observed and reported rather than
// silently disappearing.
func QualityVendorBits(raw uint16) uint8 {
	return uint8(raw >> qualityVendorShift)
}

// The DA error codes OPC 10000-8 Tables A.4 and A.5 name. The OPC ones are
// transcribed from opcerror.h and the rest are Windows COM codes, both taken
// at the commit ADR-0006 pins for the validation fixture and checked by
// scripts/spec-check/check.py.
//
// The tables spell the OPC codes inconsistently — Table A.4 writes
// OPC_E_BADRIGHTS where Table A.5 writes E_BADRIGHTS for the same code, and
// both write E_INVALIDITEMID for OPC_E_INVALIDITEMID. The names below are the
// header's.
const (
	OPCEBadRights     opcda.HRESULT = -1073479674 // 0xC0040006
	OPCEUnknownItemID opcda.HRESULT = -1073479673 // 0xC0040007
	OPCEInvalidHandle opcda.HRESULT = -1073479679 // 0xC0040001
	OPCEBadType       opcda.HRESULT = -1073479676 // 0xC0040004
	OPCEInvalidItemID opcda.HRESULT = -1073479672 // 0xC0040008
	OPCERange         opcda.HRESULT = -1073479669 // 0xC004000B
	OPCEInvalidPID    opcda.HRESULT = -1073479165 // 0xC0040203
	OPCENotSupported  opcda.HRESULT = -1073478650 // 0xC0040406
	OPCSClamp         opcda.HRESULT = 262158      // 0x0004000E
)

// Windows COM codes the same tables name. Their values come from
// golang.org/x/sys/windows, which is generated from the Windows SDK headers and
// is already a dependency of this module; mapping_windows_test.go asserts them
// against it, since that package builds only on Windows and this one builds
// everywhere.
const (
	EOutOfMemory      opcda.HRESULT = -2147024882 // 0x8007000E
	EAccessDenied     opcda.HRESULT = -2147024891 // 0x80070005
	DispETypeMismatch opcda.HRESULT = -2147352571 // 0x80020005
	DispEOverflow     opcda.HRESULT = -2147352566 // 0x8002000A
)

// StatusCodeForReadError maps a per-item DA Read HRESULT onto a UA status code
// following OPC 10000-8 Table A.4. A successful HRESULT is not an error and
// maps to Good; the quality field carries the data condition instead.
//
// Only OPC_E_BADRIGHTS and OPC_E_UNKNOWNITEMID have been observed against a
// real server; the rest are transcribed and untested, which docs/compatibility.md
// records. Transcribing them is still better than leaving them in the "Others"
// row, because that row reports Bad_UnexpectedError for a condition the table
// gives a precise answer for.
func StatusCodeForReadError(hr opcda.HRESULT) StatusCode {
	if hr.Succeeded() {
		return StatusGood
	}
	switch hr {
	case OPCEBadRights:
		return StatusBadNotReadable
	case EOutOfMemory:
		return StatusBadOutOfMemory
	case OPCEInvalidHandle, OPCEUnknownItemID:
		return StatusBadNodeIdUnknown
	case OPCEInvalidItemID:
		return StatusBadNodeIdInvalid
	case OPCEInvalidPID:
		// Table A.4 gives this row Bad_AttributeIdInvalid, where Table A.5
		// gives the same code Bad_NodeIdInvalid. The asymmetry is the tables';
		// each direction follows its own.
		return StatusBadAttributeIDInvalid
	case EAccessDenied:
		return StatusBadOutOfService
	default:
		return StatusBadUnexpectedError
	}
}

// StatusCodeForWriteError maps a per-item DA Write HRESULT onto a UA status
// code following OPC 10000-8 Table A.5.
//
// The same caveat applies: only OPC_E_BADRIGHTS and OPC_E_UNKNOWNITEMID have
// been observed against a real server.
func StatusCodeForWriteError(hr opcda.HRESULT) StatusCode {
	// OPC_S_CLAMP is a success code, so it is answered before the general
	// success case: the value was written, but not the one that was asked for.
	if hr == OPCSClamp {
		return StatusGoodClamped
	}
	if hr.Succeeded() {
		return StatusGood
	}
	switch hr {
	case OPCEBadRights:
		return StatusBadNotWritable
	case DispETypeMismatch, OPCEBadType:
		return StatusBadTypeMismatch
	case OPCERange, DispEOverflow:
		return StatusBadOutOfRange
	case EOutOfMemory:
		return StatusBadOutOfMemory
	case OPCEInvalidHandle, OPCEUnknownItemID:
		return StatusBadNodeIdUnknown
	case OPCEInvalidItemID, OPCEInvalidPID:
		return StatusBadNodeIdInvalid
	case OPCENotSupported:
		return StatusBadWriteNotSupported
	default:
		return StatusBadUnexpectedError
	}
}
