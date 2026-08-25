// Package opcua holds the DA-native to OPC UA mapping used by a future UA
// frontend. It is deliberately not a general-purpose UA SDK: it contains only
// what this adapter needs to represent one OPC DA source.
package opcua

import "fmt"

// StatusCode is the 32-bit OPC UA status code.
//
// Bit assignments follow OPC 10000-4 Table 176: Severity in bits 30:31, SubCode
// in bits 16:27, InfoType in bits 10:11, and InfoBits in bits 0:9. When InfoType
// is DataValue, OPC 10000-4 Table 177 places the LimitBits in bits 8:9.
type StatusCode uint32

const (
	severityMask   StatusCode = 0xC0000000
	severityShift             = 30
	infoTypeMask   StatusCode = 0x00000C00
	infoTypeShift             = 10
	limitBitsMask  StatusCode = 0x00000300
	limitBitsShift            = 8
)

// Severity values from OPC 10000-4 Table 176.
const (
	SeverityGood      = 0x0
	SeverityUncertain = 0x1
	SeverityBad       = 0x2
	SeverityReserved  = 0x3
)

// InfoType values from OPC 10000-4 Table 176.
const (
	InfoTypeNotUsed   = 0x0
	InfoTypeDataValue = 0x1
)

// LimitBits values from OPC 10000-4 Table 177. OPC DA encodes its limit field
// with the same four values, so the two-bit field transfers directly.
const (
	LimitNone     = 0x0
	LimitLow      = 0x1
	LimitHigh     = 0x2
	LimitConstant = 0x3
)

// Status codes used by the OPC DA mapping. Every value here is taken from the
// OPC Foundation StatusCode list published for the UA namespace; none is
// derived by hand from the bit layout.
const (
	StatusGood      StatusCode = 0x00000000
	StatusUncertain StatusCode = 0x40000000
	StatusBad       StatusCode = 0x80000000

	StatusGoodLocalOverride StatusCode = 0x00960000
	StatusGoodClamped       StatusCode = 0x00300000

	StatusUncertainSubNormal                StatusCode = 0x40950000
	StatusUncertainSensorNotAccurate        StatusCode = 0x40930000
	StatusUncertainEngineeringUnitsExceeded StatusCode = 0x40940000
	StatusUncertainLastUsableValue          StatusCode = 0x40900000

	StatusBadConfigurationError    StatusCode = 0x80890000
	StatusBadNotConnected          StatusCode = 0x808A0000
	StatusBadNoCommunication       StatusCode = 0x80310000
	StatusBadDeviceFailure         StatusCode = 0x808B0000
	StatusBadSensorFailure         StatusCode = 0x808C0000
	StatusBadOutOfService          StatusCode = 0x808D0000
	StatusBadWaitingForInitialData StatusCode = 0x80320000

	StatusBadNotReadable        StatusCode = 0x803A0000
	StatusBadNotWritable        StatusCode = 0x803B0000
	StatusBadNodeIdUnknown      StatusCode = 0x80340000
	StatusBadNodeIdInvalid      StatusCode = 0x80330000
	StatusBadAttributeIdInvalid StatusCode = 0x80350000
	StatusBadTypeMismatch       StatusCode = 0x80740000
	StatusBadOutOfRange         StatusCode = 0x803C0000
	StatusBadOutOfMemory        StatusCode = 0x80030000
	StatusBadWriteNotSupported  StatusCode = 0x80730000
	StatusBadUnexpectedError    StatusCode = 0x80010000
)

func (code StatusCode) Severity() uint32 {
	return uint32((code & severityMask) >> severityShift)
}

// IsGood, IsUncertain, and IsBad follow OPC 10000-4 Table 176, where the
// reserved severity is to be treated as Bad.
func (code StatusCode) IsGood() bool      { return code.Severity() == SeverityGood }
func (code StatusCode) IsUncertain() bool { return code.Severity() == SeverityUncertain }
func (code StatusCode) IsBad() bool {
	severity := code.Severity()
	return severity == SeverityBad || severity == SeverityReserved
}

func (code StatusCode) InfoType() uint32 {
	return uint32((code & infoTypeMask) >> infoTypeShift)
}

// LimitBits is meaningful only when InfoType is DataValue; it reports LimitNone
// otherwise, as OPC 10000-4 requires unused info bits to be zero.
func (code StatusCode) LimitBits() uint32 {
	if code.InfoType() != InfoTypeDataValue {
		return LimitNone
	}
	return uint32((code & limitBitsMask) >> limitBitsShift)
}

// WithLimitBits attaches a data-value limit to a status code. It sets InfoType
// to DataValue only when a limit is actually present, so a value with no limit
// keeps the exact published status code.
func (code StatusCode) WithLimitBits(limit uint32) StatusCode {
	cleared := code &^ (infoTypeMask | limitBitsMask)
	if limit == LimitNone {
		return cleared
	}
	return cleared |
		StatusCode(uint32(InfoTypeDataValue)<<infoTypeShift) |
		StatusCode((limit&0x3)<<limitBitsShift)
}

func (code StatusCode) Hex() string {
	return fmt.Sprintf("0x%08X", uint32(code))
}
