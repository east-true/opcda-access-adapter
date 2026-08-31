package opcua

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// UA Subscriptions follow OPC 10000-4 Tables 82, 63, 89, 164, 161, 140 and 148.
//
// A UA Subscription maps onto one DA subscription, which is one DA group. That
// keeps the DA sampling model intact: the DA server decides what a client sees
// between update-rate ticks, and this layer carries those notifications rather
// than re-sampling them.
const (
	CreateSubscriptionRequestEncodingID    uint32 = 787
	CreateSubscriptionResponseEncodingID   uint32 = 790
	DeleteSubscriptionsRequestEncodingID   uint32 = 847
	DeleteSubscriptionsResponseEncodingID  uint32 = 850
	CreateMonitoredItemsRequestEncodingID  uint32 = 751
	CreateMonitoredItemsResponseEncodingID uint32 = 754
	DeleteMonitoredItemsRequestEncodingID  uint32 = 781
	DeleteMonitoredItemsResponseEncodingID uint32 = 784
	SetPublishingModeRequestEncodingID     uint32 = 799
	SetPublishingModeResponseEncodingID    uint32 = 802
	PublishRequestEncodingID               uint32 = 826
	PublishResponseEncodingID              uint32 = 829
	MonitoredItemNotificationEncodingID    uint32 = 808
	DataChangeNotificationEncodingID       uint32 = 811
)

// Subscription status codes from the OPC Foundation StatusCode list.
const (
	StatusBadSubscriptionIDInvalid  StatusCode = 0x80280000
	StatusBadMonitoringModeInvalid  StatusCode = 0x80410000
	StatusBadMonitoredItemIDInvalid StatusCode = 0x80420000
	// A filter the server cannot perform, as distinct from one the attribute
	// does not allow: OPC 10000-4 keeps those apart and so does this.
	StatusBadMonitoredItemFilterUnsupported StatusCode = 0x80440000
	StatusBadDeadbandFilterInvalid          StatusCode = 0x808E0000
	StatusBadFilterNotAllowed               StatusCode = 0x80450000
	StatusBadTooManySubscriptions           StatusCode = 0x80770000
	StatusBadTooManyPublishRequests         StatusCode = 0x80780000
	StatusBadNoSubscription                 StatusCode = 0x80790000
	StatusBadSequenceNumberUnknown          StatusCode = 0x807A0000
	StatusBadTooManyMonitoredItems          StatusCode = 0x80DB0000
)

// MonitoringMode values from OPC 10000-4 Table 148.
type MonitoringMode int32

const (
	MonitoringModeDisabled  MonitoringMode = 0
	MonitoringModeSampling  MonitoringMode = 1
	MonitoringModeReporting MonitoringMode = 2
)

// MonitoringParameters is OPC 10000-4 Table 140.
type MonitoringParameters struct {
	ClientHandle     uint32
	SamplingInterval float64
	Filter           ExtensionObject
	QueueSize        uint32
	DiscardOldest    bool
}

// MonitoredItemCreateRequest is the per-item request of Table 63.
type MonitoredItemCreateRequest struct {
	ItemToMonitor       ReadValueID
	MonitoringMode      MonitoringMode
	RequestedParameters MonitoringParameters
}

type MonitoredItemCreateResult struct {
	StatusCode              StatusCode
	MonitoredItemID         uint32
	RevisedSamplingInterval float64
	RevisedQueueSize        uint32
	FilterResult            ExtensionObject
}

// MonitoredItemNotification is the per-item notification of Table 161.
type MonitoredItemNotification struct {
	ClientHandle uint32
	Value        DataValue
}

// NotificationMessage is OPC 10000-4 Table 164.
type NotificationMessage struct {
	SequenceNumber uint32
	PublishTime    time.Time
	// Notifications carries the data change notifications this adapter
	// produces. It has at most one element, because the adapter reports no
	// events, which Table 164 describes as the expected shape.
	Notifications []MonitoredItemNotification
	// HasData distinguishes a keep-alive, which carries no NotificationData at
	// all, from a notification that happens to be empty.
	HasData bool
}

type CreateSubscriptionRequest struct {
	Header                      RequestHeader
	RequestedPublishingInterval float64
	RequestedLifetimeCount      uint32
	RequestedMaxKeepAliveCount  uint32
	MaxNotificationsPerPublish  uint32
	PublishingEnabled           bool
	Priority                    byte
}

type CreateSubscriptionResponse struct {
	Header                    ResponseHeader
	SubscriptionID            uint32
	RevisedPublishingInterval float64
	RevisedLifetimeCount      uint32
	RevisedMaxKeepAliveCount  uint32
}

type DeleteSubscriptionsRequest struct {
	Header          RequestHeader
	SubscriptionIDs []uint32
}

type DeleteSubscriptionsResponse struct {
	Header      ResponseHeader
	Results     []StatusCode
	Diagnostics []DiagnosticInfo
}

type CreateMonitoredItemsRequest struct {
	Header             RequestHeader
	SubscriptionID     uint32
	TimestampsToReturn TimestampsToReturn
	ItemsToCreate      []MonitoredItemCreateRequest
}

type CreateMonitoredItemsResponse struct {
	Header      ResponseHeader
	Results     []MonitoredItemCreateResult
	Diagnostics []DiagnosticInfo
}

type DeleteMonitoredItemsRequest struct {
	Header           RequestHeader
	SubscriptionID   uint32
	MonitoredItemIDs []uint32
}

type DeleteMonitoredItemsResponse struct {
	Header      ResponseHeader
	Results     []StatusCode
	Diagnostics []DiagnosticInfo
}

type SetPublishingModeRequest struct {
	Header            RequestHeader
	PublishingEnabled bool
	SubscriptionIDs   []uint32
}

type SetPublishingModeResponse struct {
	Header      ResponseHeader
	Results     []StatusCode
	Diagnostics []DiagnosticInfo
}

// SubscriptionAcknowledgement is the per-entry acknowledgement of Table 89.
type SubscriptionAcknowledgement struct {
	SubscriptionID uint32
	SequenceNumber uint32
}

type PublishRequest struct {
	Header           RequestHeader
	Acknowledgements []SubscriptionAcknowledgement
}

type PublishResponse struct {
	Header                   ResponseHeader
	SubscriptionID           uint32
	AvailableSequenceNumbers []uint32
	MoreNotifications        bool
	NotificationMessage      NotificationMessage
	Results                  []StatusCode
	Diagnostics              []DiagnosticInfo
}

func (e *Encoder) writeUInt32Array(values []uint32) {
	e.WriteArrayLength(len(values))
	for _, value := range values {
		e.WriteUInt32(value)
	}
}

func (d *Decoder) readUInt32Array() ([]uint32, error) {
	length, isNull, err := d.ReadArrayLength(4)
	if err != nil || isNull {
		return nil, err
	}
	values := make([]uint32, 0, length)
	for index := 0; index < length; index++ {
		value, readErr := d.ReadUInt32()
		if readErr != nil {
			return nil, readErr
		}
		values = append(values, value)
	}
	return values, nil
}

func (e *Encoder) writeStatusCodeResults(results []StatusCode, diagnostics []DiagnosticInfo) {
	e.WriteArrayLength(len(results))
	for _, result := range results {
		e.WriteStatusCode(result)
	}
	e.WriteArrayLength(len(diagnostics))
	for _, diagnostic := range diagnostics {
		e.WriteDiagnosticInfo(diagnostic)
	}
}

func (d *Decoder) readStatusCodeResults() ([]StatusCode, []DiagnosticInfo, error) {
	length, isNull, err := d.ReadArrayLength(4)
	if err != nil {
		return nil, nil, err
	}
	var results []StatusCode
	if !isNull {
		results = make([]StatusCode, 0, length)
		for index := 0; index < length; index++ {
			status, statusErr := d.ReadStatusCode()
			if statusErr != nil {
				return nil, nil, statusErr
			}
			results = append(results, status)
		}
	}
	length, isNull, err = d.ReadArrayLength(1)
	if err != nil {
		return nil, nil, err
	}
	var diagnostics []DiagnosticInfo
	if !isNull {
		diagnostics = make([]DiagnosticInfo, 0, length)
		for index := 0; index < length; index++ {
			diagnostic, diagnosticErr := d.ReadDiagnosticInfo()
			if diagnosticErr != nil {
				return nil, nil, diagnosticErr
			}
			diagnostics = append(diagnostics, diagnostic)
		}
	}
	return results, diagnostics, nil
}

func (e *Encoder) WriteCreateSubscriptionRequest(request CreateSubscriptionRequest) {
	e.WriteServiceTypeID(CreateSubscriptionRequestEncodingID)
	e.WriteRequestHeader(request.Header)
	e.WriteDouble(request.RequestedPublishingInterval)
	e.WriteUInt32(request.RequestedLifetimeCount)
	e.WriteUInt32(request.RequestedMaxKeepAliveCount)
	e.WriteUInt32(request.MaxNotificationsPerPublish)
	e.WriteBoolean(request.PublishingEnabled)
	e.WriteByteValue(request.Priority)
}

func (d *Decoder) ReadCreateSubscriptionRequest() (CreateSubscriptionRequest, error) {
	var request CreateSubscriptionRequest
	var err error
	if request.Header, err = d.ReadRequestHeader(); err != nil {
		return CreateSubscriptionRequest{}, err
	}
	if request.RequestedPublishingInterval, err = d.ReadDouble(); err != nil {
		return CreateSubscriptionRequest{}, err
	}
	if request.RequestedLifetimeCount, err = d.ReadUInt32(); err != nil {
		return CreateSubscriptionRequest{}, err
	}
	if request.RequestedMaxKeepAliveCount, err = d.ReadUInt32(); err != nil {
		return CreateSubscriptionRequest{}, err
	}
	if request.MaxNotificationsPerPublish, err = d.ReadUInt32(); err != nil {
		return CreateSubscriptionRequest{}, err
	}
	if request.PublishingEnabled, err = d.ReadBoolean(); err != nil {
		return CreateSubscriptionRequest{}, err
	}
	if request.Priority, err = d.ReadByteValue(); err != nil {
		return CreateSubscriptionRequest{}, err
	}
	return request, nil
}

func (e *Encoder) WriteCreateSubscriptionResponse(response CreateSubscriptionResponse) {
	e.WriteServiceTypeID(CreateSubscriptionResponseEncodingID)
	e.WriteResponseHeader(response.Header)
	e.WriteUInt32(response.SubscriptionID)
	e.WriteDouble(response.RevisedPublishingInterval)
	e.WriteUInt32(response.RevisedLifetimeCount)
	e.WriteUInt32(response.RevisedMaxKeepAliveCount)
}

func (d *Decoder) ReadCreateSubscriptionResponse() (CreateSubscriptionResponse, error) {
	var response CreateSubscriptionResponse
	var err error
	if response.Header, err = d.ReadResponseHeader(); err != nil {
		return CreateSubscriptionResponse{}, err
	}
	if response.SubscriptionID, err = d.ReadUInt32(); err != nil {
		return CreateSubscriptionResponse{}, err
	}
	if response.RevisedPublishingInterval, err = d.ReadDouble(); err != nil {
		return CreateSubscriptionResponse{}, err
	}
	if response.RevisedLifetimeCount, err = d.ReadUInt32(); err != nil {
		return CreateSubscriptionResponse{}, err
	}
	if response.RevisedMaxKeepAliveCount, err = d.ReadUInt32(); err != nil {
		return CreateSubscriptionResponse{}, err
	}
	return response, nil
}

func (e *Encoder) WriteDeleteSubscriptionsRequest(request DeleteSubscriptionsRequest) {
	e.WriteServiceTypeID(DeleteSubscriptionsRequestEncodingID)
	e.WriteRequestHeader(request.Header)
	e.writeUInt32Array(request.SubscriptionIDs)
}

func (d *Decoder) ReadDeleteSubscriptionsRequest() (DeleteSubscriptionsRequest, error) {
	var request DeleteSubscriptionsRequest
	var err error
	if request.Header, err = d.ReadRequestHeader(); err != nil {
		return DeleteSubscriptionsRequest{}, err
	}
	if request.SubscriptionIDs, err = d.readUInt32Array(); err != nil {
		return DeleteSubscriptionsRequest{}, err
	}
	return request, nil
}

func (e *Encoder) WriteDeleteSubscriptionsResponse(response DeleteSubscriptionsResponse) {
	e.WriteServiceTypeID(DeleteSubscriptionsResponseEncodingID)
	e.WriteResponseHeader(response.Header)
	e.writeStatusCodeResults(response.Results, response.Diagnostics)
}

func (d *Decoder) ReadDeleteSubscriptionsResponse() (DeleteSubscriptionsResponse, error) {
	var response DeleteSubscriptionsResponse
	header, err := d.ReadResponseHeader()
	if err != nil {
		return DeleteSubscriptionsResponse{}, err
	}
	response.Header = header
	response.Results, response.Diagnostics, err = d.readStatusCodeResults()
	if err != nil {
		return DeleteSubscriptionsResponse{}, err
	}
	return response, nil
}

func (e *Encoder) WriteMonitoringParameters(value MonitoringParameters) {
	e.WriteUInt32(value.ClientHandle)
	e.WriteDouble(value.SamplingInterval)
	e.WriteExtensionObject(value.Filter)
	e.WriteUInt32(value.QueueSize)
	e.WriteBoolean(value.DiscardOldest)
}

func (d *Decoder) ReadMonitoringParameters() (MonitoringParameters, error) {
	var value MonitoringParameters
	var err error
	if value.ClientHandle, err = d.ReadUInt32(); err != nil {
		return MonitoringParameters{}, err
	}
	if value.SamplingInterval, err = d.ReadDouble(); err != nil {
		return MonitoringParameters{}, err
	}
	if value.Filter, err = d.ReadExtensionObject(); err != nil {
		return MonitoringParameters{}, err
	}
	if value.QueueSize, err = d.ReadUInt32(); err != nil {
		return MonitoringParameters{}, err
	}
	if value.DiscardOldest, err = d.ReadBoolean(); err != nil {
		return MonitoringParameters{}, err
	}
	return value, nil
}

func (e *Encoder) WriteCreateMonitoredItemsRequest(request CreateMonitoredItemsRequest) {
	e.WriteServiceTypeID(CreateMonitoredItemsRequestEncodingID)
	e.WriteRequestHeader(request.Header)
	e.WriteUInt32(request.SubscriptionID)
	e.WriteInt32(int32(request.TimestampsToReturn))
	e.WriteArrayLength(len(request.ItemsToCreate))
	for _, item := range request.ItemsToCreate {
		e.WriteReadValueID(item.ItemToMonitor)
		e.WriteInt32(int32(item.MonitoringMode))
		e.WriteMonitoringParameters(item.RequestedParameters)
	}
}

func (d *Decoder) ReadCreateMonitoredItemsRequest() (CreateMonitoredItemsRequest, error) {
	var request CreateMonitoredItemsRequest
	var err error
	if request.Header, err = d.ReadRequestHeader(); err != nil {
		return CreateMonitoredItemsRequest{}, err
	}
	if request.SubscriptionID, err = d.ReadUInt32(); err != nil {
		return CreateMonitoredItemsRequest{}, err
	}
	timestamps, err := d.ReadInt32()
	if err != nil {
		return CreateMonitoredItemsRequest{}, err
	}
	if timestamps < int32(TimestampsSource) || timestamps > int32(TimestampsInvalid) {
		return CreateMonitoredItemsRequest{}, decodingError("TimestampsToReturn %d is not defined", timestamps)
	}
	request.TimestampsToReturn = TimestampsToReturn(timestamps)
	// One item is at least a ReadValueId, a mode, and the parameters.
	length, isNull, err := d.ReadArrayLength(30)
	if err != nil {
		return CreateMonitoredItemsRequest{}, err
	}
	if !isNull {
		request.ItemsToCreate = make([]MonitoredItemCreateRequest, 0, length)
		for index := 0; index < length; index++ {
			var item MonitoredItemCreateRequest
			if item.ItemToMonitor, err = d.ReadReadValueID(); err != nil {
				return CreateMonitoredItemsRequest{}, err
			}
			mode, modeErr := d.ReadInt32()
			if modeErr != nil {
				return CreateMonitoredItemsRequest{}, modeErr
			}
			// A mode outside the enumeration is refused rather than reduced.
			if mode < int32(MonitoringModeDisabled) || mode > int32(MonitoringModeReporting) {
				return CreateMonitoredItemsRequest{}, decodingError("MonitoringMode %d is not defined", mode)
			}
			item.MonitoringMode = MonitoringMode(mode)
			if item.RequestedParameters, err = d.ReadMonitoringParameters(); err != nil {
				return CreateMonitoredItemsRequest{}, err
			}
			request.ItemsToCreate = append(request.ItemsToCreate, item)
		}
	}
	return request, nil
}

func (e *Encoder) WriteCreateMonitoredItemsResponse(response CreateMonitoredItemsResponse) {
	e.WriteServiceTypeID(CreateMonitoredItemsResponseEncodingID)
	e.WriteResponseHeader(response.Header)
	e.WriteArrayLength(len(response.Results))
	for _, result := range response.Results {
		e.WriteStatusCode(result.StatusCode)
		e.WriteUInt32(result.MonitoredItemID)
		e.WriteDouble(result.RevisedSamplingInterval)
		e.WriteUInt32(result.RevisedQueueSize)
		e.WriteExtensionObject(result.FilterResult)
	}
	e.WriteArrayLength(len(response.Diagnostics))
	for _, diagnostic := range response.Diagnostics {
		e.WriteDiagnosticInfo(diagnostic)
	}
}

func (d *Decoder) ReadCreateMonitoredItemsResponse() (CreateMonitoredItemsResponse, error) {
	var response CreateMonitoredItemsResponse
	header, err := d.ReadResponseHeader()
	if err != nil {
		return CreateMonitoredItemsResponse{}, err
	}
	response.Header = header
	length, isNull, err := d.ReadArrayLength(23)
	if err != nil {
		return CreateMonitoredItemsResponse{}, err
	}
	if !isNull {
		response.Results = make([]MonitoredItemCreateResult, 0, length)
		for index := 0; index < length; index++ {
			var result MonitoredItemCreateResult
			if result.StatusCode, err = d.ReadStatusCode(); err != nil {
				return CreateMonitoredItemsResponse{}, err
			}
			if result.MonitoredItemID, err = d.ReadUInt32(); err != nil {
				return CreateMonitoredItemsResponse{}, err
			}
			if result.RevisedSamplingInterval, err = d.ReadDouble(); err != nil {
				return CreateMonitoredItemsResponse{}, err
			}
			if result.RevisedQueueSize, err = d.ReadUInt32(); err != nil {
				return CreateMonitoredItemsResponse{}, err
			}
			if result.FilterResult, err = d.ReadExtensionObject(); err != nil {
				return CreateMonitoredItemsResponse{}, err
			}
			response.Results = append(response.Results, result)
		}
	}
	length, isNull, err = d.ReadArrayLength(1)
	if err != nil {
		return CreateMonitoredItemsResponse{}, err
	}
	if !isNull {
		response.Diagnostics = make([]DiagnosticInfo, 0, length)
		for index := 0; index < length; index++ {
			diagnostic, diagnosticErr := d.ReadDiagnosticInfo()
			if diagnosticErr != nil {
				return CreateMonitoredItemsResponse{}, diagnosticErr
			}
			response.Diagnostics = append(response.Diagnostics, diagnostic)
		}
	}
	return response, nil
}

func (e *Encoder) WriteDeleteMonitoredItemsRequest(request DeleteMonitoredItemsRequest) {
	e.WriteServiceTypeID(DeleteMonitoredItemsRequestEncodingID)
	e.WriteRequestHeader(request.Header)
	e.WriteUInt32(request.SubscriptionID)
	e.writeUInt32Array(request.MonitoredItemIDs)
}

func (d *Decoder) ReadDeleteMonitoredItemsRequest() (DeleteMonitoredItemsRequest, error) {
	var request DeleteMonitoredItemsRequest
	var err error
	if request.Header, err = d.ReadRequestHeader(); err != nil {
		return DeleteMonitoredItemsRequest{}, err
	}
	if request.SubscriptionID, err = d.ReadUInt32(); err != nil {
		return DeleteMonitoredItemsRequest{}, err
	}
	if request.MonitoredItemIDs, err = d.readUInt32Array(); err != nil {
		return DeleteMonitoredItemsRequest{}, err
	}
	return request, nil
}

func (e *Encoder) WriteDeleteMonitoredItemsResponse(response DeleteMonitoredItemsResponse) {
	e.WriteServiceTypeID(DeleteMonitoredItemsResponseEncodingID)
	e.WriteResponseHeader(response.Header)
	e.writeStatusCodeResults(response.Results, response.Diagnostics)
}

func (e *Encoder) WriteSetPublishingModeRequest(request SetPublishingModeRequest) {
	e.WriteServiceTypeID(SetPublishingModeRequestEncodingID)
	e.WriteRequestHeader(request.Header)
	e.WriteBoolean(request.PublishingEnabled)
	e.writeUInt32Array(request.SubscriptionIDs)
}

func (d *Decoder) ReadSetPublishingModeRequest() (SetPublishingModeRequest, error) {
	var request SetPublishingModeRequest
	var err error
	if request.Header, err = d.ReadRequestHeader(); err != nil {
		return SetPublishingModeRequest{}, err
	}
	if request.PublishingEnabled, err = d.ReadBoolean(); err != nil {
		return SetPublishingModeRequest{}, err
	}
	if request.SubscriptionIDs, err = d.readUInt32Array(); err != nil {
		return SetPublishingModeRequest{}, err
	}
	return request, nil
}

func (e *Encoder) WriteSetPublishingModeResponse(response SetPublishingModeResponse) {
	e.WriteServiceTypeID(SetPublishingModeResponseEncodingID)
	e.WriteResponseHeader(response.Header)
	e.writeStatusCodeResults(response.Results, response.Diagnostics)
}

func (e *Encoder) WritePublishRequest(request PublishRequest) {
	e.WriteServiceTypeID(PublishRequestEncodingID)
	e.WriteRequestHeader(request.Header)
	e.WriteArrayLength(len(request.Acknowledgements))
	for _, acknowledgement := range request.Acknowledgements {
		e.WriteUInt32(acknowledgement.SubscriptionID)
		e.WriteUInt32(acknowledgement.SequenceNumber)
	}
}

func (d *Decoder) ReadPublishRequest() (PublishRequest, error) {
	var request PublishRequest
	header, err := d.ReadRequestHeader()
	if err != nil {
		return PublishRequest{}, err
	}
	request.Header = header
	length, isNull, err := d.ReadArrayLength(8)
	if err != nil {
		return PublishRequest{}, err
	}
	if !isNull {
		request.Acknowledgements = make([]SubscriptionAcknowledgement, 0, length)
		for index := 0; index < length; index++ {
			var acknowledgement SubscriptionAcknowledgement
			if acknowledgement.SubscriptionID, err = d.ReadUInt32(); err != nil {
				return PublishRequest{}, err
			}
			if acknowledgement.SequenceNumber, err = d.ReadUInt32(); err != nil {
				return PublishRequest{}, err
			}
			request.Acknowledgements = append(request.Acknowledgements, acknowledgement)
		}
	}
	return request, nil
}

// WriteNotificationMessage writes Table 164. A keep-alive carries no
// NotificationData at all, which is how a client tells it from a notification
// that happens to be empty.
func (e *Encoder) WriteNotificationMessage(message NotificationMessage) {
	e.WriteUInt32(message.SequenceNumber)
	e.WriteDateTime(message.PublishTime)
	if !message.HasData {
		e.WriteArrayLength(0)
		return
	}
	e.WriteArrayLength(1)
	// The DataChangeNotification is carried in an ExtensionObject, as the
	// extensible NotificationData parameter requires.
	body, err := NewEncoder(e.limits)
	if err != nil {
		e.fail(err)
		return
	}
	body.WriteArrayLength(len(message.Notifications))
	for _, notification := range message.Notifications {
		body.WriteUInt32(notification.ClientHandle)
		body.WriteDataValue(notification.Value)
	}
	// Table 161's diagnosticInfos array; this adapter reports none.
	body.WriteArrayLength(0)
	encoded, err := body.Bytes()
	if err != nil {
		e.fail(err)
		return
	}
	e.WriteExtensionObject(ExtensionObject{
		TypeID:   NumericNodeID(0, DataChangeNotificationEncodingID),
		Encoding: ExtensionObjectByteString,
		Body:     encoded,
	})
}

func (d *Decoder) ReadNotificationMessage() (NotificationMessage, error) {
	var message NotificationMessage
	var err error
	if message.SequenceNumber, err = d.ReadUInt32(); err != nil {
		return NotificationMessage{}, err
	}
	if message.PublishTime, err = d.ReadDateTime(); err != nil {
		return NotificationMessage{}, err
	}
	length, isNull, err := d.ReadArrayLength(3)
	if err != nil {
		return NotificationMessage{}, err
	}
	if isNull || length == 0 {
		return message, nil
	}
	for index := 0; index < length; index++ {
		object, objectErr := d.ReadExtensionObject()
		if objectErr != nil {
			return NotificationMessage{}, objectErr
		}
		if object.TypeID.Namespace != 0 || object.TypeID.Numeric != DataChangeNotificationEncodingID {
			// A notification type this adapter does not produce is skipped
			// rather than misread.
			continue
		}
		body, bodyErr := NewDecoder(object.Body, d.limits)
		if bodyErr != nil {
			return NotificationMessage{}, bodyErr
		}
		count, countIsNull, countErr := body.ReadArrayLength(5)
		if countErr != nil {
			return NotificationMessage{}, countErr
		}
		message.HasData = true
		if countIsNull {
			continue
		}
		for item := 0; item < count; item++ {
			var notification MonitoredItemNotification
			if notification.ClientHandle, err = body.ReadUInt32(); err != nil {
				return NotificationMessage{}, err
			}
			if notification.Value, err = body.ReadDataValue(); err != nil {
				return NotificationMessage{}, err
			}
			message.Notifications = append(message.Notifications, notification)
		}
	}
	return message, nil
}

func (e *Encoder) WritePublishResponse(response PublishResponse) {
	e.WriteServiceTypeID(PublishResponseEncodingID)
	e.WriteResponseHeader(response.Header)
	e.WriteUInt32(response.SubscriptionID)
	e.writeUInt32Array(response.AvailableSequenceNumbers)
	e.WriteBoolean(response.MoreNotifications)
	e.WriteNotificationMessage(response.NotificationMessage)
	e.writeStatusCodeResults(response.Results, response.Diagnostics)
}

func (d *Decoder) ReadPublishResponse() (PublishResponse, error) {
	var response PublishResponse
	var err error
	if response.Header, err = d.ReadResponseHeader(); err != nil {
		return PublishResponse{}, err
	}
	if response.SubscriptionID, err = d.ReadUInt32(); err != nil {
		return PublishResponse{}, err
	}
	if response.AvailableSequenceNumbers, err = d.readUInt32Array(); err != nil {
		return PublishResponse{}, err
	}
	if response.MoreNotifications, err = d.ReadBoolean(); err != nil {
		return PublishResponse{}, err
	}
	if response.NotificationMessage, err = d.ReadNotificationMessage(); err != nil {
		return PublishResponse{}, err
	}
	response.Results, response.Diagnostics, err = d.readStatusCodeResults()
	if err != nil {
		return PublishResponse{}, err
	}
	return response, nil
}

// SubscriptionLimits bounds what subscriptions can cost.
type SubscriptionLimits struct {
	MaxSubscriptions           int
	MaxMonitoredItems          int
	MinPublishingInterval      time.Duration
	MaxPublishingInterval      time.Duration
	MinKeepAliveCount          uint32
	MaxKeepAliveCount          uint32
	MaxNotificationsPerPublish int
	RequestTimeout             time.Duration
	// MaxNodes bounds the address space, including nodes created by monitoring
	// a DA item that was never browsed.
	MaxNodes int
}

func DefaultSubscriptionLimits() SubscriptionLimits {
	return SubscriptionLimits{
		MaxSubscriptions:           8,
		MaxMonitoredItems:          100,
		MinPublishingInterval:      100 * time.Millisecond,
		MaxPublishingInterval:      time.Minute,
		MinKeepAliveCount:          1,
		MaxKeepAliveCount:          100,
		MaxNotificationsPerPublish: 500,
		RequestTimeout:             30 * time.Second,
		MaxNodes:                   DefaultPopulationLimits().MaxNodes,
	}
}

func (limits SubscriptionLimits) validate() error {
	if limits.MaxSubscriptions <= 0 || limits.MaxMonitoredItems <= 0 ||
		limits.MinPublishingInterval <= 0 || limits.MaxPublishingInterval <= 0 ||
		limits.MinKeepAliveCount == 0 || limits.MaxKeepAliveCount == 0 ||
		limits.MaxNotificationsPerPublish <= 0 || limits.RequestTimeout <= 0 ||
		limits.MaxNodes <= 0 {
		return fmt.Errorf("all subscription limits must be positive")
	}
	if limits.MinPublishingInterval > limits.MaxPublishingInterval {
		return fmt.Errorf("minimum publishing interval must not exceed the maximum")
	}
	if limits.MinKeepAliveCount > limits.MaxKeepAliveCount {
		return fmt.Errorf("minimum keep-alive count must not exceed the maximum")
	}
	return nil
}

func (limits SubscriptionLimits) ValidateForConfiguration() error { return limits.validate() }

// revisePublishingInterval clamps the request. Table 82: a zero or negative
// request means the fastest supported interval.
func (limits SubscriptionLimits) revisePublishingInterval(requested float64) float64 {
	interval := time.Duration(requested) * time.Millisecond
	if requested <= 0 || interval < limits.MinPublishingInterval {
		interval = limits.MinPublishingInterval
	}
	if interval > limits.MaxPublishingInterval {
		interval = limits.MaxPublishingInterval
	}
	return float64(interval / time.Millisecond)
}

// reviseKeepAliveCount clamps the request. Table 82: a zero request means the
// smallest supported keep-alive count.
func (limits SubscriptionLimits) reviseKeepAliveCount(requested uint32) uint32 {
	if requested == 0 || requested < limits.MinKeepAliveCount {
		return limits.MinKeepAliveCount
	}
	if requested > limits.MaxKeepAliveCount {
		return limits.MaxKeepAliveCount
	}
	return requested
}

// reviseLifetimeCount enforces Table 82's rule that the lifetime count shall be
// at least three times the keep-alive count.
func reviseLifetimeCount(requested, keepAlive uint32) uint32 {
	minimum := keepAlive * 3
	if requested < minimum {
		return minimum
	}
	return requested
}

// monitoredItem is one UA MonitoredItem. It maps to one item inside the DA
// subscription that backs its UA Subscription.
type monitoredItem struct {
	id           uint32
	clientHandle uint32
	itemID       opcda.DAItemID
	nodeID       NodeID
	mode         MonitoringMode
	timestamps   TimestampsToReturn

	// notReadable records that the source says this item cannot be read.
	// OPC 10000-4 5.13.2.1 requires the item to be created anyway and the bad
	// status delivered through Publish, so it stays out of the DA group and
	// reports its status instead of a value.
	notReadable bool

	// samplingInterval is the revised sampling interval, in milliseconds, that
	// this item was told it would get. A DA group has one update rate for
	// every item in it, so an item that asked for a slower rate than the group
	// runs at is paced here rather than being sent everything the group
	// delivers -- OPC 10000-4 7.21 requires the revised interval to be equal
	// or higher than the requested one, and reporting a rate the client then
	// does not receive would make that promise a lie in the other direction.
	samplingInterval float64
	// held is the newest notification this item has produced but not yet
	// reported, and lastReported is when it last reported one. Pacing holds
	// rather than drops: queueSize is one, so the newest value is the one the
	// client wants, and dropping would leave a client that asked for a slow
	// rate stuck on a stale value whenever the source went quiet.
	held         *DataValue
	lastReported time.Time

	// semanticGeneration is the address space's count of semantic property
	// changes as of this item's last notification. Clause 5.2 asks for the bit
	// on one notification per monitored item, so the two are compared rather
	// than a flag being set and cleared.
	semanticGeneration uint64
}

// uaSubscription is one UA Subscription backed by one DA subscription.
type uaSubscription struct {
	id                 uint32
	sessionToken       string
	publishingEnabled  bool
	publishingInterval float64
	keepAliveCount     uint32
	lifetimeCount      uint32
	maxNotifications   uint32
	priority           byte

	items      map[uint32]*monitoredItem
	nextItemID uint32
	byHandle   map[uint32]*monitoredItem

	// heldOrder lists the items holding an unreported value, in the order they
	// began holding one. opcda.Subscription drains "preserving first-seen
	// order", and that order is the source's own account of what changed
	// first, so it is carried through rather than being replaced by whatever
	// order a map happens to yield.
	heldOrder []uint32

	// deadband is the percent deadband the DA group carries. A.3.5 maps a
	// client's PercentDeadband onto it, and a DA group has exactly one, so it
	// is a property of the subscription rather than of a monitored item.
	deadband    float32
	deadbandSet bool

	// da is the DA subscription that supplies notifications. It is nil until
	// the first monitored item is created, because a DA group needs its items.
	da opcda.Subscription

	sequenceNumber uint32
	// pending holds notifications drained from the DA core but not yet
	// published, so a Publish that arrives after a change still carries it.
	pending []MonitoredItemNotification
	// retransmit holds sent messages a client has not acknowledged.
	retransmit map[uint32]NotificationMessage
	// keepAliveTicks counts publishing intervals with nothing to send.
	keepAliveTicks uint32
	// lastKeptAlive is when the client last did something that counts as being
	// present. Clause 5.14.1.1: the lifetime counter "counts the number of
	// consecutive publishing cycles in which there have been no Publish
	// requests available to send a Publish response for the Subscription. Any
	// Service call that uses the SubscriptionId or the processing of a Publish
	// response resets the lifetime counter". Elapsed time is used rather than a
	// tick count because this server has no publishing timer of its own -- a
	// Publish drives its own cycles, which is precisely what stops when the
	// client goes away.
	lastKeptAlive time.Time
}

// expired reports whether the client has been gone for the lifetime it was
// promised: lifetimeCount publishing intervals without a sign of it.
//
// Neither input needs guarding. The publishing interval is revised to at least
// the server's minimum, which validate keeps positive, and lastKeptAlive is set
// when the subscription is created and only ever moved forward. Guarding them
// would guard against a subscription that cannot exist, while reading as though
// one could -- and a guard returning false on a zero interval would quietly
// exempt that subscription from ever expiring.
func (subscription *uaSubscription) expired(now time.Time) bool {
	interval := time.Duration(subscription.publishingInterval) * time.Millisecond
	return now.Sub(subscription.lastKeptAlive) >= time.Duration(subscription.lifetimeCount)*interval
}

// SubscriptionService answers the subscription services over the DA Subscribe
// core.
//
// Publish is answered from what the DA core has already delivered rather than
// by polling the source: the DA server decides the rate, and this layer carries
// its notifications. A UA client's publishing interval therefore governs how
// often it collects, not how often the source samples.
type SubscriptionService struct {
	space   *AddressSpace
	runtime opcda.Runtime
	limits  SubscriptionLimits

	mu            sync.Mutex
	subscriptions map[uint32]*uaSubscription
	nextID        uint32
}

func NewSubscriptionService(space *AddressSpace, runtime opcda.Runtime, limits SubscriptionLimits, idSeed uint32) (*SubscriptionService, error) {
	if err := limits.validate(); err != nil {
		return nil, err
	}
	if space == nil || runtime == nil {
		return nil, fmt.Errorf("a subscription service needs an address space and a DA runtime")
	}
	return &SubscriptionService{
		space:         space,
		runtime:       runtime,
		limits:        limits,
		subscriptions: make(map[uint32]*uaSubscription),
		// Table 82 advises that subscription ids start from a random value
		// after start-up, so a restart does not reuse a client's identifiers.
		nextID: idSeed,
	}, nil
}

func (s *SubscriptionService) allocateID() uint32 {
	for {
		s.nextID++
		if s.nextID == 0 {
			continue
		}
		if _, taken := s.subscriptions[s.nextID]; !taken {
			return s.nextID
		}
	}
}

// CreateSubscription creates a UA Subscription. The DA subscription behind it
// is created with the first monitored item, because a DA group is defined by
// its items.
func (s *SubscriptionService) CreateSubscription(sessionToken string, request CreateSubscriptionRequest, now time.Time) (CreateSubscriptionResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.subscriptions) >= s.limits.MaxSubscriptions {
		return CreateSubscriptionResponse{}, uacpError(StatusBadTooManySubscriptions,
			"the %d subscription limit is reached", s.limits.MaxSubscriptions)
	}
	keepAlive := s.limits.reviseKeepAliveCount(request.RequestedMaxKeepAliveCount)
	subscription := &uaSubscription{
		id:                 s.allocateID(),
		sessionToken:       sessionToken,
		publishingEnabled:  request.PublishingEnabled,
		publishingInterval: s.limits.revisePublishingInterval(request.RequestedPublishingInterval),
		keepAliveCount:     keepAlive,
		lifetimeCount:      reviseLifetimeCount(request.RequestedLifetimeCount, keepAlive),
		maxNotifications:   request.MaxNotificationsPerPublish,
		priority:           request.Priority,
		items:              make(map[uint32]*monitoredItem),
		byHandle:           make(map[uint32]*monitoredItem),
		retransmit:         make(map[uint32]NotificationMessage),
		lastKeptAlive:      now,
	}
	s.subscriptions[subscription.id] = subscription
	return CreateSubscriptionResponse{
		Header: ResponseHeader{
			Timestamp: now, RequestHandle: request.Header.RequestHandle,
			ServiceResult: StatusGood, AdditionalHeader: NullExtensionObject(),
		},
		SubscriptionID:            subscription.id,
		RevisedPublishingInterval: subscription.publishingInterval,
		RevisedLifetimeCount:      subscription.lifetimeCount,
		RevisedMaxKeepAliveCount:  subscription.keepAliveCount,
	}, nil
}

// lookup resolves a subscription and enforces that it belongs to the session
// asking for it, so one client cannot reach another's subscription.
// lookup finds a subscription and records that its client is still there.
// Clause 5.14.1.1 resets the lifetime counter on "any Service call that uses
// the SubscriptionId or the processing of a Publish response", and this is the
// one funnel every such call passes through -- putting the reset here rather
// than at each call site means a new service cannot forget it.
func (s *SubscriptionService) lookup(sessionToken string, id uint32, now time.Time) (*uaSubscription, error) {
	subscription, ok := s.subscriptions[id]
	if !ok || subscription.sessionToken != sessionToken {
		return nil, uacpError(StatusBadSubscriptionIDInvalid, "subscription %d is not known to this session", id)
	}
	subscription.lastKeptAlive = now
	return subscription, nil
}

// CreateMonitoredItems adds items and, on the first call, creates the DA
// subscription that backs them.
//
// A DA group's item set is fixed at AddGroup time, so adding items to an
// existing UA Subscription replaces the DA subscription with one covering the
// full set. The DA core never resubscribes on its own, so doing it here is
// explicit rather than hidden.
func (s *SubscriptionService) CreateMonitoredItems(ctx context.Context, sessionToken string, request CreateMonitoredItemsRequest, now time.Time) (CreateMonitoredItemsResponse, error) {
	if request.TimestampsToReturn == TimestampsInvalid {
		return CreateMonitoredItemsResponse{}, uacpError(StatusBadInvalidArgument, "timestampsToReturn is invalid")
	}
	if len(request.ItemsToCreate) == 0 {
		return CreateMonitoredItemsResponse{}, uacpError(StatusBadNothingToDo, "no monitored items were requested")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	subscription, err := s.lookup(sessionToken, request.SubscriptionID, now)
	if err != nil {
		return CreateMonitoredItemsResponse{}, err
	}
	if len(subscription.items)+len(request.ItemsToCreate) > s.limits.MaxMonitoredItems {
		return CreateMonitoredItemsResponse{}, uacpError(StatusBadTooManyMonitoredItems,
			"the %d monitored item limit would be exceeded", s.limits.MaxMonitoredItems)
	}

	results := make([]MonitoredItemCreateResult, len(request.ItemsToCreate))
	created := make([]*monitoredItem, len(request.ItemsToCreate))
	accepted := make([]*monitoredItem, 0, len(request.ItemsToCreate))
	for index, create := range request.ItemsToCreate {
		result, item := s.prepareMonitoredItem(subscription, create, request.TimestampsToReturn)
		results[index] = result
		created[index] = item
		if item != nil {
			accepted = append(accepted, item)
		}
	}
	if len(accepted) == 0 {
		return s.monitoredItemsResponse(request, results, now), nil
	}

	// Register the accepted items, then rebuild the DA subscription over the
	// full set.
	for _, item := range accepted {
		subscription.items[item.id] = item
		subscription.byHandle[item.clientHandle] = item
	}
	rebuildErr := s.rebuildDASubscription(ctx, subscription, now)
	if rebuildErr == nil {
		// Now that the group exists the source has revised its update rate.
		// That rate is a floor: the group cannot sample faster than it, so no
		// item can be given anything quicker no matter what it asked for. An
		// item that asked for something slower keeps its own interval and is
		// paced to it.
		revised := subscription.daRevisedInterval()
		for index, item := range created {
			if item == nil || results[index].StatusCode != StatusGood {
				continue
			}
			if item.samplingInterval < revised {
				item.samplingInterval = revised
			}
			results[index].RevisedSamplingInterval = item.samplingInterval
		}
	}
	if err := rebuildErr; err != nil {
		// The DA source refused, so nothing was created: the items are removed
		// and every accepted result carries the source's status.
		status := statusForRuntimeError(err)
		for _, item := range accepted {
			delete(subscription.items, item.id)
			delete(subscription.byHandle, item.clientHandle)
		}
		for index := range results {
			if results[index].StatusCode == StatusGood {
				results[index] = MonitoredItemCreateResult{StatusCode: status, FilterResult: NullExtensionObject()}
			}
		}
	}
	return s.monitoredItemsResponse(request, results, now), nil
}

func (s *SubscriptionService) monitoredItemsResponse(request CreateMonitoredItemsRequest, results []MonitoredItemCreateResult, now time.Time) CreateMonitoredItemsResponse {
	return CreateMonitoredItemsResponse{
		Header: ResponseHeader{
			Timestamp: now, RequestHandle: request.Header.RequestHandle,
			ServiceResult: StatusGood, AdditionalHeader: NullExtensionObject(),
		},
		Results:     results,
		Diagnostics: []DiagnosticInfo{},
	}
}

// prepareMonitoredItem validates one request. It returns the result the client
// will see and, when the item is acceptable, the item itself.
func (s *SubscriptionService) prepareMonitoredItem(subscription *uaSubscription, create MonitoredItemCreateRequest, timestamps TimestampsToReturn) (MonitoredItemCreateResult, *monitoredItem) {
	failed := func(status StatusCode) MonitoredItemCreateResult {
		return MonitoredItemCreateResult{StatusCode: status, FilterResult: NullExtensionObject()}
	}
	// This adapter exposes no arrays, so an indexRange cannot apply.
	if create.ItemToMonitor.IndexRange != "" {
		return failed(StatusBadIndexRangeInvalid), nil
	}
	if create.ItemToMonitor.AttributeID != AttributeValue {
		// Only the Value attribute changes, so only it can be monitored here.
		return failed(StatusBadAttributeIDInvalid), nil
	}
	// A.3.5: a percent deadband is the one filter a DA group can apply. One is
	// accepted and passed to the group; anything else is refused rather than
	// accepted and quietly not applied, which would misreport what the client
	// will receive.
	deadband, filterStatus := deadbandForFilter(create.RequestedParameters.Filter, DefaultBinaryLimits())
	if filterStatus != StatusGood {
		return failed(filterStatus), nil
	}
	// The group has one deadband, so every item in the subscription shares it.
	// A second item asking for a different one cannot be honoured, and saying
	// so is better than applying somebody else's.
	if subscription.deadbandSet && subscription.deadband != deadband {
		return failed(StatusBadMonitoredItemFilterUnsupported), nil
	}
	// A node identifier naming a DA item can be monitored without having been
	// browsed, since a source need not implement Browse at all.
	node, kind := s.space.ResolveNode(create.ItemToMonitor.NodeID, s.limits.MaxNodes)
	switch kind {
	case NodeKindUnknown:
		return failed(StatusBadNodeIdUnknown), nil
	case NodeKindItemProperty:
		// OPC DA has no change notification for item properties: a group
		// notifies on item values only. Monitoring one would mean the adapter
		// inventing a sampling loop of its own, which is a source of updates
		// the source never agreed to. A client that wants a property reads it.
		return failed(StatusBadNotSupported), nil
	case NodeKindOther:
		return failed(StatusBadAttributeIDInvalid), nil
	}
	// A node the source will not let anyone read is still created. OPC 10000-4
	// 5.13.2.1: "the add operation for the item shall succeed and the bad
	// status Bad_NotReadable or Bad_UserAccessDenied shall be returned in the
	// Publish response". Table 65 agrees by omission -- Bad_NotReadable is not
	// among the operation level result codes CreateMonitoredItems may return.
	//
	// The point is not pedantry. A client told its create failed has to keep
	// re-creating the item to find out whether it ever became readable; a
	// client holding a created item watches its status instead, which is the
	// mechanism UA already gives it.
	notReadable := node.AccessRightsKnown && node.AccessLevel&AccessLevelCurrentRead == 0
	// Clause 7.2: a percent deadband "is defined as the percentage of the
	// EURange. That is, it applies only to AnalogItems with an EURange
	// Property". An item without one has no range to take a percentage of, so
	// the filter has no defined meaning there, and passing it to the group as
	// though it did would apply a percentage of nothing.
	//
	// Table 61 names the status for exactly this: Bad_DeadbandFilterInvalid is
	// "the specified PercentDeadband is not between 0.0 and 100.0 **or a
	// PercentDeadband is not supported, since an EURange is not configured**".
	// The second half is this case, so it is not a generic unsupported filter.
	if deadband > 0 && node.TypeDefinition.Numeric != NodeIDAnalogItemType {
		return failed(StatusBadDeadbandFilterInvalid), nil
	}
	if _, duplicate := subscription.byHandle[create.RequestedParameters.ClientHandle]; duplicate {
		// Two items sharing a client handle would make notifications
		// ambiguous, which is worse than refusing one of them.
		return failed(StatusBadInvalidArgument), nil
	}

	subscription.deadband = deadband
	subscription.deadbandSet = true
	subscription.nextItemID++
	sampling := requestedSamplingInterval(subscription, node, create.RequestedParameters.SamplingInterval)
	// A monitored item starts level with the address space. A change that
	// happened before it existed is not one it needs telling about.
	item := &monitoredItem{
		semanticGeneration: s.space.SemanticGeneration(node.ItemID),
		id:                 subscription.nextItemID,
		clientHandle:       create.RequestedParameters.ClientHandle,
		itemID:             node.ItemID,
		nodeID:             node.ID,
		mode:               create.MonitoringMode,
		timestamps:         timestamps,
		samplingInterval:   sampling,
		notReadable:        notReadable,
	}
	return MonitoredItemCreateResult{
		StatusCode:      StatusGood,
		MonitoredItemID: item.id,
		// The DA group's revised update rate is the floor under this item's
		// interval. It is filled in once the group exists, because only the
		// source can say what rate it settled on, and a vendor may revise it
		// far from what was requested.
		RevisedSamplingInterval: sampling,
		// The DA core coalesces per item, so the effective queue is one value
		// per item and reporting anything larger would overstate it.
		RevisedQueueSize: 1,
		FilterResult:     NullExtensionObject(),
	}, item
}

// requestedSamplingInterval reads a monitored item's requested sampling
// interval the way OPC 10000-4 7.21 defines it, and applies the floor clause
// 5.13.1.2 puts under it.
//
// Zero "indicates that the Server should use the fastest practical rate", which
// for this adapter is whatever rate the DA group settles on, so zero is carried
// through and the group's rate is applied to it later. Minus one "indicates
// that the default sampling interval defined by the publishing interval of the
// Subscription is requested", and "any negative number is interpreted as -1".
//
// The floor is the item's MinimumSamplingInterval: "if the Server specifies a
// value for the MinimumSamplingInterval Attribute it shall always return a
// revisedSamplingInterval that is equal or higher". Here that value is the DA
// Scan Rate, so it is the source's own statement about how often the item can
// change -- promising anything faster would be promising something the source
// has already said it will not do.
func requestedSamplingInterval(subscription *uaSubscription, node *Node, requested float64) float64 {
	interval := requested
	switch {
	case requested < 0:
		interval = subscription.publishingInterval
	case requested == 0:
		interval = 0
	}
	if node.MinimumSamplingIntervalKnown && node.MinimumSamplingInterval > interval {
		interval = node.MinimumSamplingInterval
	}
	return interval
}

// rebuildDASubscription replaces the DA subscription with one covering the
// subscription's current item set.
func (s *SubscriptionService) rebuildDASubscription(ctx context.Context, subscription *uaSubscription, now time.Time) error {
	itemIDs := make([]opcda.DAItemID, 0, len(subscription.items))
	seen := make(map[opcda.DAItemID]struct{}, len(subscription.items))
	for _, item := range subscription.items {
		if item.notReadable {
			// There is nothing for the group to read, and a source may refuse
			// AddItems for such an item -- which would fail the whole rebuild
			// and take every readable item in the request down with it.
			continue
		}
		if _, duplicate := seen[item.itemID]; duplicate {
			// Two monitored items may name the same DA ItemID; the DA group
			// carries it once and both are fed from that one notification.
			continue
		}
		seen[item.itemID] = struct{}{}
		itemIDs = append(itemIDs, item.itemID)
	}
	if len(itemIDs) == 0 {
		s.releaseDASubscription(ctx, subscription)
		return nil
	}

	createCtx, cancel := context.WithTimeout(ctx, s.limits.RequestTimeout)
	defer cancel()
	updateRate := time.Duration(subscription.publishingInterval) * time.Millisecond
	created, err := s.runtime.Subscribe(createCtx, opcda.SubscribeRequest{
		Items:               itemIDs,
		RequestedUpdateRate: updateRate,
		Deadband:            subscription.deadband,
	})
	if err != nil {
		return err
	}
	s.releaseDASubscription(ctx, subscription)
	subscription.da = created
	return nil
}

func (s *SubscriptionService) releaseDASubscription(ctx context.Context, subscription *uaSubscription) {
	if subscription.da == nil {
		return
	}
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.limits.RequestTimeout)
	defer cancel()
	_ = s.runtime.Unsubscribe(releaseCtx, subscription.da.Info().ID)
	subscription.da = nil
}

// DeleteMonitoredItems removes items and rebuilds the DA subscription over what
// remains.
func (s *SubscriptionService) DeleteMonitoredItems(ctx context.Context, sessionToken string, request DeleteMonitoredItemsRequest, now time.Time) (DeleteMonitoredItemsResponse, error) {
	if len(request.MonitoredItemIDs) == 0 {
		return DeleteMonitoredItemsResponse{}, uacpError(StatusBadNothingToDo, "no monitored items were named")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	subscription, err := s.lookup(sessionToken, request.SubscriptionID, now)
	if err != nil {
		return DeleteMonitoredItemsResponse{}, err
	}

	results := make([]StatusCode, len(request.MonitoredItemIDs))
	removed := false
	for index, id := range request.MonitoredItemIDs {
		item, ok := subscription.items[id]
		if !ok {
			results[index] = StatusBadMonitoredItemIDInvalid
			continue
		}
		delete(subscription.items, id)
		delete(subscription.byHandle, item.clientHandle)
		results[index] = StatusGood
		removed = true
	}
	if removed {
		if err := s.rebuildDASubscription(ctx, subscription, now); err != nil {
			// The items are gone either way; the DA subscription is released so
			// notifications for removed items cannot keep arriving.
			s.releaseDASubscription(ctx, subscription)
		}
	}
	return DeleteMonitoredItemsResponse{
		Header: ResponseHeader{
			Timestamp: now, RequestHandle: request.Header.RequestHandle,
			ServiceResult: StatusGood, AdditionalHeader: NullExtensionObject(),
		},
		Results:     results,
		Diagnostics: []DiagnosticInfo{},
	}, nil
}

// DeleteSubscriptions removes subscriptions and releases their DA groups.
func (s *SubscriptionService) DeleteSubscriptions(ctx context.Context, sessionToken string, request DeleteSubscriptionsRequest, now time.Time) (DeleteSubscriptionsResponse, error) {
	if len(request.SubscriptionIDs) == 0 {
		return DeleteSubscriptionsResponse{}, uacpError(StatusBadNothingToDo, "no subscriptions were named")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	results := make([]StatusCode, len(request.SubscriptionIDs))
	for index, id := range request.SubscriptionIDs {
		subscription, err := s.lookup(sessionToken, id, now)
		if err != nil {
			results[index] = StatusBadSubscriptionIDInvalid
			continue
		}
		s.releaseDASubscription(ctx, subscription)
		delete(s.subscriptions, id)
		results[index] = StatusGood
	}
	return DeleteSubscriptionsResponse{
		Header: ResponseHeader{
			Timestamp: now, RequestHandle: request.Header.RequestHandle,
			ServiceResult: StatusGood, AdditionalHeader: NullExtensionObject(),
		},
		Results:     results,
		Diagnostics: []DiagnosticInfo{},
	}, nil
}

// SetPublishingMode enables or disables publishing. Table 82 notes this does not
// affect a MonitoredItem's monitoring mode, so the DA subscription is left
// alone and only delivery stops.
func (s *SubscriptionService) SetPublishingMode(sessionToken string, request SetPublishingModeRequest, now time.Time) (SetPublishingModeResponse, error) {
	if len(request.SubscriptionIDs) == 0 {
		return SetPublishingModeResponse{}, uacpError(StatusBadNothingToDo, "no subscriptions were named")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	results := make([]StatusCode, len(request.SubscriptionIDs))
	for index, id := range request.SubscriptionIDs {
		subscription, err := s.lookup(sessionToken, id, now)
		if err != nil {
			results[index] = StatusBadSubscriptionIDInvalid
			continue
		}
		subscription.publishingEnabled = request.PublishingEnabled
		results[index] = StatusGood
	}
	return SetPublishingModeResponse{
		Header: ResponseHeader{
			Timestamp: now, RequestHandle: request.Header.RequestHandle,
			ServiceResult: StatusGood, AdditionalHeader: NullExtensionObject(),
		},
		Results:     results,
		Diagnostics: []DiagnosticInfo{},
	}, nil
}

// Publish answers with whatever the DA core has delivered since the last call.
//
// It does not block waiting for a notification. A real UA server holds a
// Publish request open, but this adapter answers immediately with a keep-alive
// when there is nothing to report: holding the request would occupy the
// connection's single request path, and the UA-TCP listener here serves one
// request at a time per connection.
// Publish holds the request until the subscription has something to report or
// a keep-alive is due, which is what OPC 10000-4 5.14.5.1 means by a Publish
// request being queued in the server. Answering immediately instead turns a
// conforming client into a busy loop: it issues the next Publish as soon as the
// last response arrives, so an empty response returned at once is answered at
// once, thousands of times a second. That was measured against a third-party
// client before this was fixed, and it starved the very sampling the
// subscription existed to deliver.
//
// The caller must not hold the connection's read loop while this runs, because
// a client sends other requests on the same channel while a Publish is
// outstanding.
func (s *SubscriptionService) Publish(ctx context.Context, sessionToken string, request PublishRequest, now time.Time) (PublishResponse, error) {
	acknowledgements, err := s.acknowledge(sessionToken, request, now)
	if err != nil {
		return PublishResponse{}, err
	}

	// The publishing interval is how often the subscription looks for
	// something to say, so it is also how often this waits.
	interval := s.publishPollInterval(sessionToken)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		response, ready, err := s.tryPublish(ctx, sessionToken, request, acknowledgements, time.Now().UTC())
		if err != nil {
			return PublishResponse{}, err
		}
		if ready {
			return response, nil
		}
		select {
		case <-ctx.Done():
			// The connection is gone or the server is stopping. Nothing is
			// sent, which is what a client that has disconnected expects.
			return PublishResponse{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

// acknowledge applies the request's acknowledgements, which happen as soon as
// the request arrives rather than when its response is finally sent.
func (s *SubscriptionService) acknowledge(sessionToken string, request PublishRequest, now time.Time) ([]StatusCode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	acknowledgements := make([]StatusCode, len(request.Acknowledgements))
	for index, acknowledgement := range request.Acknowledgements {
		subscription, err := s.lookup(sessionToken, acknowledgement.SubscriptionID, now)
		if err != nil {
			acknowledgements[index] = StatusBadSubscriptionIDInvalid
			continue
		}
		if _, ok := subscription.retransmit[acknowledgement.SequenceNumber]; !ok {
			acknowledgements[index] = StatusBadSequenceNumberUnknown
			continue
		}
		delete(subscription.retransmit, acknowledgement.SequenceNumber)
		acknowledgements[index] = StatusGood
	}
	return acknowledgements, nil
}

// publishPollInterval is how often a waiting Publish looks for something to
// send: the shortest publishing interval this session asked for.
func (s *SubscriptionService) publishPollInterval(sessionToken string) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()

	interval := s.limits.MaxPublishingInterval
	for _, subscription := range s.subscriptions {
		if subscription.sessionToken != sessionToken {
			continue
		}
		candidate := time.Duration(subscription.publishingInterval) * time.Millisecond
		if candidate > 0 && candidate < interval {
			interval = candidate
		}
	}
	if interval < s.limits.MinPublishingInterval {
		interval = s.limits.MinPublishingInterval
	}
	return interval
}

// tryPublish performs one publishing cycle. It reports ready when it has a
// response to send: either notifications, or a keep-alive that has come due.
func (s *SubscriptionService) tryPublish(ctx context.Context, sessionToken string, request PublishRequest, acknowledgements []StatusCode, now time.Time) (PublishResponse, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// A Publish request is outstanding for this session, so it is available to
	// every subscription the session owns -- none of them is starving, whichever
	// one this cycle picks. 5.14.1.1 counts "publishing cycles in which there
	// have been no Publish requests available", so this is what keeps them all
	// alive.
	for _, held := range s.subscriptions {
		if held.sessionToken == sessionToken {
			held.lastKeptAlive = now
		}
	}
	subscription := s.nextPublishable(sessionToken)
	if subscription == nil {
		return PublishResponse{}, false, uacpError(StatusBadNoSubscription,
			"this session has no subscription to publish")
	}

	s.collect(ctx, subscription, now)
	message, ready := s.buildMessage(subscription, now)
	if !ready {
		return PublishResponse{}, false, nil
	}

	available := make([]uint32, 0, len(subscription.retransmit))
	for sequence := range subscription.retransmit {
		available = append(available, sequence)
	}
	return PublishResponse{
		Header: ResponseHeader{
			Timestamp: now, RequestHandle: request.Header.RequestHandle,
			ServiceResult: StatusGood, AdditionalHeader: NullExtensionObject(),
		},
		SubscriptionID:           subscription.id,
		AvailableSequenceNumbers: available,
		MoreNotifications:        len(subscription.pending) > 0,
		NotificationMessage:      message,
		Results:                  acknowledgements,
		Diagnostics:              []DiagnosticInfo{},
	}, true, nil
}

// nextPublishable picks the session's highest-priority subscription that has
// something to say, falling back to any of its subscriptions so a keep-alive is
// still sent.
func (s *SubscriptionService) nextPublishable(sessionToken string) *uaSubscription {
	var best *uaSubscription
	for _, subscription := range s.subscriptions {
		if subscription.sessionToken != sessionToken {
			continue
		}
		if best == nil || subscription.priority > best.priority {
			best = subscription
		}
	}
	return best
}

// collect drains whatever the DA core has for this subscription.
func (s *SubscriptionService) collect(ctx context.Context, subscription *uaSubscription, now time.Time) {
	s.holdUnreadable(subscription)
	if subscription.da == nil {
		// Every item is unreadable, or none has been created yet. There is no
		// group to drain, but a held status still has to reach the client.
		s.flushHeld(subscription, now)
		return
	}
	select {
	case <-subscription.da.Done():
		// The DA subscription was invalidated, which the DA core does on a
		// source disconnect. The UA subscription is kept but its DA backing is
		// released; the client is told through the item status on the next
		// notification rather than by silently going quiet.
		s.reportInvalidation(subscription, now)
		s.releaseDASubscription(ctx, subscription)
		return
	default:
	}

	values := subscription.da.Drain()
	for _, value := range values {
		item := subscription.itemForDAItemID(value.ItemID)
		if item == nil {
			continue
		}
		// A disabled item is not reported, which is what MonitoringMode
		// Disabled and Sampling both mean for reporting.
		if item.mode != MonitoringModeReporting {
			continue
		}
		// A notification carries the same DA metadata an AddItems result does,
		// so it teaches the address space what Browse could not.
		s.space.LearnFromRead(item.nodeID, value.CanonicalType, value.AccessRights)
		notification := dataValueForSubscription(value, item.timestamps, now)
		// Clause 5.2: the bit goes on one data change notification per
		// monitored item that samples values at the time the change happened.
		// Comparing generations gives exactly that -- the item carries it once
		// and then agrees with the address space again.
		if generation := s.space.SemanticGeneration(item.itemID); generation != item.semanticGeneration {
			notification.Status = notification.Status.WithSemanticsChanged()
			item.semanticGeneration = generation
		}
		// The newest value wins. queueSize is one, so an older value in the
		// same sampling interval is one the client would have overwritten
		// anyway.
		if item.held == nil {
			subscription.heldOrder = append(subscription.heldOrder, item.id)
		}
		held := notification
		item.held = &held
	}
	s.flushHeld(subscription, now)
}

// flushHeld reports each item's held value once its own sampling interval has
// come round. An item that asked for no more than the group's rate reports
// every value the group delivers; one that asked for less reports the newest
// value it has, which is what a queue of one holds.
func (s *SubscriptionService) flushHeld(subscription *uaSubscription, now time.Time) {
	stillHeld := subscription.heldOrder[:0]
	for _, id := range subscription.heldOrder {
		item, ok := subscription.items[id]
		if !ok || item.held == nil {
			// The item was deleted, or reported by an earlier pass.
			continue
		}
		// An item that has never reported has a zero lastReported, which is
		// unboundedly long ago, so its first value is never paced -- a client
		// that has just subscribed is waiting for the current value, not for
		// an interval to elapse before it learns anything.
		if now.Sub(item.lastReported) < time.Duration(item.samplingInterval)*time.Millisecond {
			// Not due yet: it keeps its place in the queue as well as its value.
			stillHeld = append(stillHeld, id)
			continue
		}
		subscription.pending = append(subscription.pending, MonitoredItemNotification{
			ClientHandle: item.clientHandle,
			Value:        *item.held,
		})
		item.held = nil
		item.lastReported = now
	}
	subscription.heldOrder = stillHeld
}

// holdUnreadable gives each item the source will not let anyone read its status,
// once. OPC 10000-4 5.13.2.1 puts Bad_NotReadable in the Publish response rather
// than in the create result, and a status that does not change is reported once
// rather than repeated -- a monitored item reports changes, and this one has
// nothing further to say unless the source's answer changes.
func (s *SubscriptionService) holdUnreadable(subscription *uaSubscription) {
	for _, item := range subscription.items {
		if !item.notReadable || !item.lastReported.IsZero() || item.held != nil {
			continue
		}
		if item.mode != MonitoringModeReporting {
			continue
		}
		subscription.heldOrder = append(subscription.heldOrder, item.id)
		item.held = &DataValue{Value: NullVariant(), Status: StatusBadNotReadable}
	}
}

// reportInvalidation queues a bad status for every reporting item, so a client
// learns the source is gone instead of seeing the stream stop.
func (s *SubscriptionService) reportInvalidation(subscription *uaSubscription, now time.Time) {
	// Whatever was held is superseded: the source is gone, so a value waiting
	// for its sampling interval describes a world that no longer exists.
	subscription.heldOrder = subscription.heldOrder[:0]
	for _, item := range subscription.items {
		item.held = nil
		if item.mode != MonitoringModeReporting {
			continue
		}
		value := DataValue{Value: NullVariant(), Status: StatusBadNotConnected}
		if item.timestamps == TimestampsServer || item.timestamps == TimestampsBoth {
			value.ServerTimestamp = now
		}
		subscription.pending = append(subscription.pending, MonitoredItemNotification{
			ClientHandle: item.clientHandle, Value: value,
		})
	}
}

// daRevisedInterval reports the update rate the DA server settled on, in
// milliseconds. Before a group exists the subscription's publishing interval is
// the best answer available.
func (subscription *uaSubscription) daRevisedInterval() float64 {
	if subscription.da == nil {
		return subscription.publishingInterval
	}
	revised := subscription.da.Info().RevisedUpdateRate
	if revised <= 0 {
		return subscription.publishingInterval
	}
	return float64(revised / time.Millisecond)
}

func (subscription *uaSubscription) itemForDAItemID(itemID opcda.DAItemID) *monitoredItem {
	for _, item := range subscription.items {
		if item.itemID == itemID {
			return item
		}
	}
	return nil
}

// dataValueForSubscription maps one DA notification onto a DataValue using the
// same Part 8 rules a Read uses, so a subscribed value and a read value cannot
// disagree.
func dataValueForSubscription(value opcda.SubscriptionValue, timestamps TimestampsToReturn, now time.Time) DataValue {
	return dataValueForRead(opcda.ReadResult{
		ItemID:         value.ItemID,
		Value:          value.Value,
		VarType:        value.VarType,
		CanonicalType:  value.CanonicalType,
		AccessRights:   value.AccessRights,
		HRESULT:        value.HRESULT,
		HRESULTPresent: value.HRESULTPresent,
		ErrorCode:      value.ErrorCode,
	}, timestamps, now)
}

// buildMessage takes up to the negotiated maximum from the pending set. With
// nothing pending it counts one publishing cycle towards the keep-alive, and
// reports ready only once maxKeepAliveCount cycles have passed with nothing to
// say. Reporting ready on every empty cycle would answer the client's Publish
// immediately and let it ask again at once, which is the busy loop this holds
// back.
func (s *SubscriptionService) buildMessage(subscription *uaSubscription, now time.Time) (NotificationMessage, bool) {
	if !subscription.publishingEnabled || len(subscription.pending) == 0 {
		subscription.keepAliveTicks++
		if subscription.keepAliveTicks < subscription.keepAliveCount {
			return NotificationMessage{}, false
		}
		subscription.keepAliveTicks = 0
		// A keep-alive reuses the last sequence number, because Table 164's
		// sequence numbers count notifications rather than responses.
		return NotificationMessage{SequenceNumber: subscription.sequenceNumber, PublishTime: now}, true
	}

	maximum := s.limits.MaxNotificationsPerPublish
	// Table 82: a zero maxNotificationsPerPublish means the client has no
	// limit, so the server's own bound applies.
	if subscription.maxNotifications > 0 && int(subscription.maxNotifications) < maximum {
		maximum = int(subscription.maxNotifications)
	}
	count := len(subscription.pending)
	if count > maximum {
		count = maximum
	}
	notifications := subscription.pending[:count]
	subscription.pending = subscription.pending[count:]
	subscription.keepAliveTicks = 0
	subscription.sequenceNumber++

	message := NotificationMessage{
		SequenceNumber: subscription.sequenceNumber,
		PublishTime:    now,
		Notifications:  notifications,
		HasData:        true,
	}
	subscription.retransmit[message.SequenceNumber] = message
	return message, true
}

// ExpireStale deletes subscriptions whose clients have gone quiet for the
// lifetime they were told they had. Table 82: "when the publishing timer has
// expired this number of times without a Publish request being available to
// send a NotificationMessage, then the Subscription shall be deleted by the
// Server", and 5.14.1.1 adds that "closing the Subscription causes its
// MonitoredItems to be deleted".
//
// For this adapter that is not bookkeeping. A subscription holds a DA group
// open on the source, and a client that stops publishing while keeping its
// session alive would otherwise hold that group for as long as it liked.
func (s *SubscriptionService) ExpireStale(ctx context.Context, now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	expired := make([]*uaSubscription, 0)
	for id, subscription := range s.subscriptions {
		if !subscription.expired(now) {
			continue
		}
		delete(s.subscriptions, id)
		expired = append(expired, subscription)
	}
	for _, subscription := range expired {
		s.releaseDASubscription(ctx, subscription)
	}
	return len(expired)
}

// ReleaseSession removes every subscription a session owned, releasing their DA
// groups. A closed session must not leave DA groups open on the source.
func (s *SubscriptionService) ReleaseSession(ctx context.Context, sessionToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, subscription := range s.subscriptions {
		if subscription.sessionToken != sessionToken {
			continue
		}
		s.releaseDASubscription(ctx, subscription)
		delete(s.subscriptions, id)
	}
}

// Count reports how many subscriptions are held, for bounds and diagnostics.
func (s *SubscriptionService) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subscriptions)
}
