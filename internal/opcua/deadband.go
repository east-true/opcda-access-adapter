package opcua

// OPC 10000-8 A.3.5: "The SamplingInterval and the Deadband value are used for
// the subscription to setup a periodic data change call back on the COM UA
// Wrapper. Note that only the PercentDeadbandType is supported by the COM UA
// Wrapper."
//
// The DA core has always carried a percent deadband -- it is IOPCServer::
// AddGroup's pPercentDeadband, and the gRPC frontend exposes it. Only the UA
// frontend refused it, along with every other filter.
//
// A DA percent deadband belongs to the **group**, and one UA subscription is
// backed by one DA group. So the deadband is a property of the subscription,
// not of a monitored item, and two items in one subscription cannot have
// different ones. The adapter accepts a filter the source can honour and
// refuses one it cannot, rather than accepting a filter and quietly applying
// something else.

const (
	// NodeIDDataChangeFilterEncodingDefaultBinary identifies the filter body a
	// client sends, from NodeIds.csv.
	NodeIDDataChangeFilterEncodingDefaultBinary uint32 = 724
)

// DataChangeTrigger and DeadbandType are OPC 10000-6's enumerations, whose
// values come from Opc.Ua.Types.bsd.
const (
	DataChangeTriggerStatus               uint32 = 0
	DataChangeTriggerStatusValue          uint32 = 1
	DataChangeTriggerStatusValueTimestamp uint32 = 2

	DeadbandNone     uint32 = 0
	DeadbandAbsolute uint32 = 1
	DeadbandPercent  uint32 = 2
)

// DataChangeFilter is OPC 10000-6's structure of the same name.
type DataChangeFilter struct {
	Trigger       uint32
	DeadbandType  uint32
	DeadbandValue float64
}

// ReadDataChangeFilter decodes the structure from an ExtensionObject body.
func (d *Decoder) ReadDataChangeFilter() (DataChangeFilter, error) {
	trigger, err := d.ReadUInt32()
	if err != nil {
		return DataChangeFilter{}, err
	}
	deadbandType, err := d.ReadUInt32()
	if err != nil {
		return DataChangeFilter{}, err
	}
	deadbandValue, err := d.ReadDouble()
	if err != nil {
		return DataChangeFilter{}, err
	}
	return DataChangeFilter{Trigger: trigger, DeadbandType: deadbandType, DeadbandValue: deadbandValue}, nil
}

// deadbandForFilter reports the percent deadband a monitoring filter asks for,
// and the status to refuse it with when the source cannot honour it.
//
// An absent filter asks for no deadband, which is what the adapter has always
// sent. Everything else is judged against what a DA group can actually do.
func deadbandForFilter(filter ExtensionObject, limits BinaryLimits) (float32, StatusCode) {
	if filter.TypeID.IsNull() {
		return 0, StatusGood
	}
	if filter.TypeID.Namespace != 0 || filter.TypeID.Type != NodeIDTypeNumeric ||
		filter.TypeID.Numeric != NodeIDDataChangeFilterEncodingDefaultBinary {
		// An event filter, an aggregate filter, or something unknown. A DA
		// group offers none of them.
		return 0, StatusBadMonitoredItemFilterUnsupported
	}
	decoder, err := NewDecoder(filter.Body, limits)
	if err != nil {
		return 0, StatusBadMonitoredItemFilterUnsupported
	}
	decoded, err := decoder.ReadDataChangeFilter()
	if err != nil {
		return 0, StatusBadMonitoredItemFilterUnsupported
	}

	switch decoded.Trigger {
	case DataChangeTriggerStatus, DataChangeTriggerStatusValue:
		// A DA group notifies on a change of value or quality, which is what
		// both of these ask for.
	default:
		// StatusValueTimestamp asks to be told when only the timestamp moves.
		// DA has no such notification, and reporting fewer changes than asked
		// for would misdescribe the subscription.
		return 0, StatusBadMonitoredItemFilterUnsupported
	}

	switch decoded.DeadbandType {
	case DeadbandNone:
		return 0, StatusGood
	case DeadbandPercent:
		// A.3.5 names this as the one the wrapper supports.
		if decoded.DeadbandValue < 0 || decoded.DeadbandValue > 100 {
			return 0, StatusBadDeadbandFilterInvalid
		}
		return float32(decoded.DeadbandValue), StatusGood
	default:
		// Absolute deadband. AddGroup takes a percentage and nothing else.
		return 0, StatusBadMonitoredItemFilterUnsupported
	}
}
