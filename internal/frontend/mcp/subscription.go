package mcpfrontend

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/jiind/opcda-access-adapter/internal/frontend/dajson"
	"github.com/jiind/opcda-access-adapter/internal/opcda"
)

type subscribeInput struct {
	Items                 []itemInput `json:"items"`
	RequestedUpdateRateMs int64       `json:"requestedUpdateRateMs"`
	PercentDeadband       float32     `json:"percentDeadband"`
}

type subscriptionInput struct {
	SubscriptionID opcda.SubscriptionID `json:"subscriptionId"`
	WaitMs         int64                `json:"waitMs"`
}

type ownedSubscription struct {
	handle opcda.Subscription
	mu     sync.Mutex
	items  map[opcda.DAItemID]struct{}
}

// subscribe는 세션이 소유하는 DA 그룹 하나를 만들고 원본의 수정 주기를 보고한다.
func (s *Server) subscribe(ctx context.Context, input subscribeInput) (any, error) {
	// 1. 연결 세대가 끝난 핸들을 정리하고 세션의 그룹 개수를 제한한다.
	ids, err := s.itemIDs(input.Items, s.config.Limits.MaxSubscriptionItems)
	if err != nil {
		return nil, err
	}
	if input.RequestedUpdateRateMs < 1 || input.RequestedUpdateRateMs > 3600000 || math.IsNaN(float64(input.PercentDeadband)) || input.PercentDeadband < 0 || input.PercentDeadband > 100 {
		return nil, invalid("invalid DA group update rate or percent deadband")
	}
	seen := make(map[opcda.DAItemID]struct{}, len(ids))
	for _, id := range ids {
		if _, duplicate := seen[id]; duplicate {
			return nil, invalid("Subscribe items must be unique")
		}
		seen[id] = struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, subscription := range s.subscriptions {
		select {
		case <-subscription.handle.Done():
			delete(s.subscriptions, id)
		default:
		}
	}
	if s.closed || len(s.subscriptions) >= s.config.Limits.MaxSubscriptions {
		return nil, opcda.NewAdapterError(opcda.CodeSubscriptionLimit, "MCP subscription limit reached")
	}

	// 2. 요청 취소·잘못된 결과·과대 응답이면 생성한 그룹을 즉시 해제한다.
	handle, err := s.runtime.Subscribe(ctx, opcda.SubscribeRequest{Items: ids, RequestedUpdateRate: time.Duration(input.RequestedUpdateRateMs) * time.Millisecond, Deadband: input.PercentDeadband})
	if err != nil {
		return nil, err
	}
	info := handle.Info()
	committed := false
	defer func() {
		if !committed {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.config.RequestDeadline)
			defer cancel()
			_ = s.runtime.Unsubscribe(cleanup, info.ID)
		}
	}()
	if info.ID == "" || len(info.ID) > 128 || len(info.Items) != len(ids) || info.ActiveItemCount < 0 || info.ActiveItemCount > len(ids) {
		return nil, mismatch()
	}
	if _, duplicate := s.subscriptions[info.ID]; duplicate {
		return nil, mismatch()
	}
	items := make([]map[string]any, len(info.Items))
	active := make(map[opcda.DAItemID]struct{}, info.ActiveItemCount)
	for index, item := range info.Items {
		if item.ItemID != ids[index] || (!item.HRESULTPresent && item.ErrorCode == "") {
			return nil, mismatch()
		}
		entry := map[string]any{"itemId": item.ItemID, "active": item.Active, "hresult": hresult(item.HRESULT, item.HRESULTPresent), "errorCode": item.ErrorCode}
		if item.CanonicalType != nil {
			entry["canonicalDataType"] = item.CanonicalType.Information()
		}
		if item.AccessRights != nil {
			entry["accessRights"] = item.AccessRights
		}
		if item.Active {
			active[item.ItemID] = struct{}{}
		}
		items[index] = entry
	}
	if len(active) != info.ActiveItemCount {
		return nil, mismatch()
	}
	response := map[string]any{"subscriptionId": info.ID, "connectionGeneration": info.ConnectionGeneration, "requestedUpdateRateMs": info.RequestedUpdateRate.Milliseconds(), "revisedUpdateRateMs": info.RevisedUpdateRate.Milliseconds(), "percentDeadband": info.Deadband, "activeItemCount": info.ActiveItemCount, "items": items}
	packed, err := result(response)
	if err != nil || packed.IsError {
		return nil, opcda.NewAdapterError(opcda.CodeRequestLimitExceeded, "subscription response exceeds the output limit")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.subscriptions[info.ID] = &ownedSubscription{handle: handle, items: active}
	committed = true
	return response, nil
}

// lookup는 현재 MCP 세션이 생성한 핸들만 반환한다.
func (s *Server) lookup(id opcda.SubscriptionID) (*ownedSubscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	subscription, ok := s.subscriptions[id]
	if !ok || s.closed {
		return nil, opcda.NewAdapterError(opcda.CodeSubscriptionNotFound, "subscription is not owned by this MCP session")
	}
	return subscription, nil
}

// next는 프런트엔드 큐 없이 DA 그룹의 현재 병합 집합을 직접 읽는다.
func (s *Server) next(ctx context.Context, input subscriptionInput) (any, error) {
	// 1. 한 핸들에 대한 병렬 소비를 거부해 항목을 서로 빼앗지 않게 한다.
	owned, err := s.lookup(input.SubscriptionID)
	if err != nil {
		return nil, err
	}
	if !owned.mu.TryLock() {
		return nil, opcda.NewAdapterError(opcda.CodeQueueFull, "a subscription read is already pending")
	}
	defer owned.mu.Unlock()
	if input.WaitMs < 0 || input.WaitMs > 30000 {
		return nil, invalid("subscription wait must be between 0 and 30000 milliseconds")
	}
	if err := subscriptionFailure(owned.handle); err != nil {
		return nil, err
	}

	// 2. 선택한 대기 시간 동안만 새 병합 집합을 기다린다.
	values := owned.handle.Drain()
	if len(values) == 0 && input.WaitMs > 0 {
		timer := time.NewTimer(time.Duration(input.WaitMs) * time.Millisecond)
		defer timer.Stop()
	waiting:
		for len(values) == 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-owned.handle.Done():
				return nil, invalidated()
			case <-timer.C:
				break waiting
			case <-owned.handle.Updates():
			}
			values = owned.handle.Drain()
		}
	}
	if err := subscriptionFailure(owned.handle); err != nil {
		return nil, err
	}
	if len(values) > len(owned.items) {
		return nil, mismatch()
	}

	// 3. 현재 그룹의 항목만 전달하고 재연결 뒤의 오래된 값을 거부한다.
	fields := dajson.NewReadResultFields(len(values))
	encoded := make([]dajson.ReadResult, len(values))
	seen := make(map[opcda.DAItemID]struct{}, len(values))
	for index, value := range values {
		_, active := owned.items[value.ItemID]
		_, duplicate := seen[value.ItemID]
		read := opcda.ReadResult{ItemID: value.ItemID, Value: value.Value, VarType: value.VarType, CanonicalType: value.CanonicalType, AccessRights: value.AccessRights, HRESULT: value.HRESULT, HRESULTPresent: value.HRESULTPresent, ErrorCode: value.ErrorCode}
		if !active || duplicate || !validReadResult(read) {
			return nil, mismatch()
		}
		seen[value.ItemID] = struct{}{}
		encoded[index] = dajson.EncodeReadResult(read, &fields)
	}
	if err := subscriptionFailure(owned.handle); err != nil {
		return nil, err
	}
	return map[string]any{"subscriptionId": input.SubscriptionID, "connectionGeneration": owned.handle.Info().ConnectionGeneration, "results": encoded}, nil
}

// subscriptionFailure는 무효화된 그룹에서 빈 성공 응답을 반환하지 않게 한다.
func subscriptionFailure(handle opcda.Subscription) error {
	select {
	case <-handle.Done():
		return invalidated()
	default:
		return nil
	}
}

// invalidated는 명시적 재구독이 필요한 종료를 나타낸다.
func invalidated() error {
	return opcda.NewAdapterError(opcda.CodeSubscriptionInvalidated, "DA subscription invalidated; explicitly resubscribe")
}

// unsubscribe는 세션 소유 그룹을 원본에서 해제한 뒤 핸들을 제거한다.
func (s *Server) unsubscribe(ctx context.Context, id opcda.SubscriptionID) (any, error) {
	// 1. 소유권이 없는 핸들로 다른 DA 그룹에 접근하지 않는다.
	if _, err := s.lookup(id); err != nil {
		return nil, err
	}
	if err := s.runtime.Unsubscribe(ctx, id); err != nil {
		return nil, err
	}

	// 2. 해제가 성공한 핸들은 다시 사용할 수 없게 제거한다.
	s.mu.Lock()
	delete(s.subscriptions, id)
	s.mu.Unlock()
	return map[string]any{"subscriptionId": id, "released": true}, nil
}

// closeSubscriptions는 세션 종료 이후의 신규 그룹 생성을 막고 남은 그룹을 정리한다.
func (s *Server) closeSubscriptions(ctx context.Context) error {
	// 1. 전송이 종료되면 모든 핸들의 세션 소유권을 원자적으로 회수한다.
	s.mu.Lock()
	s.closed = true
	owned := s.subscriptions
	s.subscriptions = make(map[opcda.SubscriptionID]*ownedSubscription)
	s.mu.Unlock()

	// 2. 전체 정리 제한 시간 안에서 그룹을 해제한다.
	var failed bool
	for id := range owned {
		if err := s.runtime.Unsubscribe(ctx, id); err != nil {
			if adapter, ok := opcda.AsAdapterError(err); !ok || adapter.Code != opcda.CodeSubscriptionNotFound {
				failed = true
			}
		}
	}
	if failed {
		return opcda.NewAdapterError(opcda.CodeRuntimeUnavailable, "MCP subscription cleanup failed")
	}
	return nil
}
