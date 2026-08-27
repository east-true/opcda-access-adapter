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
	case QualityLastKnown, QualityOutOfService:
		// Table A.3 maps both LAST_KNOWN and OUT_OF_SERVICE to
		// Bad_OutOfService.
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

// OPC DA error codes this project has observed against a real server. Only
// these two are bound to numeric values; every other row of OPC 10000-8
// Tables A.4 and A.5 needs its DA constant confirmed against the OPC DA
// specification before it is added, and until then falls into the "Others" row.
const (
	OPCEBadRights     opcda.HRESULT = -1073479674 // 0xC0040006
	OPCEUnknownItemID opcda.HRESULT = -1073479673 // 0xC0040007
)

// StatusCodeForReadError maps a per-item DA Read HRESULT onto a UA status code
// following OPC 10000-8 Table A.4. A successful HRESULT is not an error and
// maps to Good; the quality field carries the data condition instead.
func StatusCodeForReadError(hr opcda.HRESULT) StatusCode {
	if hr.Succeeded() {
		return StatusGood
	}
	switch hr {
	case OPCEBadRights:
		return StatusBadNotReadable
	case OPCEUnknownItemID:
		return StatusBadNodeIdUnknown
	default:
		return StatusBadUnexpectedError
	}
}

// StatusCodeForWriteError maps a per-item DA Write HRESULT onto a UA status
// code following OPC 10000-8 Table A.5.
func StatusCodeForWriteError(hr opcda.HRESULT) StatusCode {
	if hr.Succeeded() {
		return StatusGood
	}
	switch hr {
	case OPCEBadRights:
		return StatusBadNotWritable
	case OPCEUnknownItemID:
		return StatusBadNodeIdUnknown
	default:
		return StatusBadUnexpectedError
	}
}
