package grpcfrontend

import (
	"context"
	"time"

	opcdav1 "github.com/east-true/opcda-access-adapter/api/opcda/v1"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

const (
	minimumUpdateRateMilliseconds = 1
	maximumUpdateRateMilliseconds = 3_600_000
)

// Subscribe streams one DA group's notifications. Backpressure is the HTTP/2
// flow-control window and nothing else: the handler holds no buffer of its own,
// so a slow client simply blocks in Send while the DA core keeps coalescing per
// item. That is exactly what OPC DA does between update-rate ticks, so a slow
// consumer observes the values it would have seen at a slower requested rate.
// Nothing is queued, dropped with a counter, replayed, or resubscribed.
func (s *Server) Subscribe(request *opcdav1.DASubscribeRequest, stream grpcgo.ServerStreamingServer[opcdav1.DASubscribeResponse]) error {
	// Server-streaming RPCs bypass the unary interceptor, so admission and the
	// absence of the unary request deadline are both handled here. A long-lived
	// stream must never inherit the unary deadline.
	select {
	case s.subscriptions <- struct{}{}:
		defer func() { <-s.subscriptions }()
	default:
		return grpcAdapterError(codes.ResourceExhausted, opcda.CodeSubscriptionLimit, "too many concurrent Subscribe streams")
	}

	subscribeRequest, err := s.decodeSubscribeRequest(request)
	if err != nil {
		return err
	}

	ctx := stream.Context()
	createCtx, cancelCreate := context.WithTimeout(ctx, s.config.RequestDeadline)
	subscription, err := s.runtime.Subscribe(createCtx, subscribeRequest)
	cancelCreate()
	if err != nil {
		return mapOperationError(err)
	}
	info := subscription.Info()
	// Ending the stream for any reason releases the DA group. The detached
	// context is required because the stream context is already done on the
	// cancellation path; this is cleanup, not a retry.
	defer func() {
		releaseCtx, cancelRelease := context.WithTimeout(context.WithoutCancel(ctx), s.config.RequestDeadline)
		defer cancelRelease()
		_ = s.runtime.Unsubscribe(releaseCtx, info.ID)
	}()

	created, err := encodeSubscriptionCreated(info, s.config.MaxItemIDBytes)
	if err != nil {
		return err
	}
	if err := stream.Send(created); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return mapOperationError(ctx.Err())
		case <-subscription.Done():
			// The subscription ended with the connection generation. The client
			// is told explicitly and must resubscribe; the adapter never does.
			return mapOperationError(subscription.Err())
		case <-subscription.Updates():
		}
		values := subscription.Drain()
		if len(values) == 0 {
			continue
		}
		update, err := encodeSubscriptionUpdate(values, s.config.MaxItemIDBytes)
		if err != nil {
			return err
		}
		// Send blocks while the client is behind. That block is the only
		// backpressure mechanism and must not be replaced by a buffer.
		if err := stream.Send(update); err != nil {
			return err
		}
	}
}

func (s *Server) decodeSubscribeRequest(request *opcdav1.DASubscribeRequest) (opcda.SubscribeRequest, error) {
	if request == nil {
		return opcda.SubscribeRequest{}, invalidRequest("Subscribe request is required")
	}
	if len(request.Items) == 0 {
		return opcda.SubscribeRequest{}, invalidRequest("Subscribe items must contain at least one entry")
	}
	if len(request.Items) > s.config.MaxSubscribeItems {
		return opcda.SubscribeRequest{}, requestLimit("Subscribe item limit exceeded")
	}
	if request.RequestedUpdateRateMs < minimumUpdateRateMilliseconds ||
		request.RequestedUpdateRateMs > maximumUpdateRateMilliseconds {
		return opcda.SubscribeRequest{}, invalidRequest("requested update rate must be between 1ms and 1h")
	}
	if request.PercentDeadband < 0 || request.PercentDeadband > 100 {
		return opcda.SubscribeRequest{}, invalidRequest("percent deadband must be between 0 and 100")
	}
	items := make([]opcda.DAItemID, len(request.Items))
	for index, item := range request.Items {
		if item == nil {
			return opcda.SubscribeRequest{}, invalidRequest("Subscribe item must not be null")
		}
		if err := validateText(item.ItemId, "Subscribe ItemID", s.config.MaxItemIDBytes); err != nil {
			return opcda.SubscribeRequest{}, err
		}
		items[index] = opcda.DAItemID(item.ItemId)
	}
	return opcda.SubscribeRequest{
		Items:               items,
		RequestedUpdateRate: time.Duration(request.RequestedUpdateRateMs) * time.Millisecond,
		Deadband:            request.PercentDeadband,
	}, nil
}

func encodeSubscriptionCreated(info opcda.SubscriptionInfo, maxItemIDBytes int) (*opcdav1.DASubscribeResponse, error) {
	if info.ID == "" {
		return nil, internalResultMismatch("runtime returned a subscription without an identifier")
	}
	if info.ActiveItemCount < 0 || info.ActiveItemCount > len(info.Items) {
		return nil, internalResultMismatch("runtime returned an inconsistent active item count")
	}
	created := &opcdav1.DASubscriptionCreated{
		SubscriptionId:        string(info.ID),
		ConnectionGeneration:  info.ConnectionGeneration,
		RequestedUpdateRateMs: durationMilliseconds(info.RequestedUpdateRate),
		RevisedUpdateRateMs:   durationMilliseconds(info.RevisedUpdateRate),
		PercentDeadband:       info.Deadband,
		ActiveItemCount:       uint32(info.ActiveItemCount),
		Items:                 make([]*opcdav1.DASubscriptionItemStatus, len(info.Items)),
	}
	for index, item := range info.Items {
		if err := validateText(string(item.ItemID), "subscription ItemID", maxItemIDBytes); err != nil {
			return nil, internalResultMismatch("runtime returned an invalid subscription ItemID")
		}
		status := &opcdav1.DASubscriptionItemStatus{
			ItemId:    string(item.ItemID),
			Active:    item.Active,
			ErrorCode: item.ErrorCode,
		}
		if item.HRESULTPresent {
			status.Hresult = encodeHRESULT(item.HRESULT)
		}
		if item.Active {
			if item.CanonicalType == nil || item.AccessRights == nil {
				return nil, internalResultMismatch("runtime returned an active subscription item without DA metadata")
			}
			status.CanonicalDataType = encodeVarType(item.CanonicalType)
			status.AccessRights = encodeAccessRights(item.AccessRights)
		}
		created.Items[index] = status
	}
	return &opcdav1.DASubscribeResponse{
		Message: &opcdav1.DASubscribeResponse_Created{Created: created},
	}, nil
}

func encodeSubscriptionUpdate(values []opcda.SubscriptionValue, maxItemIDBytes int) (*opcdav1.DASubscribeResponse, error) {
	update := &opcdav1.DASubscriptionUpdate{Values: make([]*opcdav1.DAReadResult, len(values))}
	for index, value := range values {
		if err := validateText(string(value.ItemID), "subscription ItemID", maxItemIDBytes); err != nil {
			return nil, internalResultMismatch("runtime returned an invalid notification ItemID")
		}
		// A notification entry has the same shape and the same semantics as a
		// device Read result, so it is encoded by the same function.
		encoded, err := encodeReadResult(opcda.ReadResult{
			ItemID:         value.ItemID,
			Value:          value.Value,
			VarType:        value.VarType,
			CanonicalType:  value.CanonicalType,
			AccessRights:   value.AccessRights,
			HRESULT:        value.HRESULT,
			HRESULTPresent: value.HRESULTPresent,
			ErrorCode:      value.ErrorCode,
		})
		if err != nil {
			return nil, err
		}
		update.Values[index] = encoded
	}
	return &opcdav1.DASubscribeResponse{
		Message: &opcdav1.DASubscribeResponse_Update{Update: update},
	}, nil
}

func durationMilliseconds(value time.Duration) uint32 {
	milliseconds := value / time.Millisecond
	if milliseconds < 0 {
		return 0
	}
	if milliseconds > maximumUpdateRateMilliseconds {
		return maximumUpdateRateMilliseconds
	}
	return uint32(milliseconds)
}
