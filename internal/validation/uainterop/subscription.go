package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// revisedUpdateRateFor rounds a requested rate up to a whole multiple of
// revisionQuantum. Real DA servers revise the rate they were asked for, and a
// client is entitled to be told the rate the server settled on rather than the
// one it requested. Revising here is what makes the adapter's
// revisedSamplingInterval observable from outside.
const revisionQuantum = 250 * time.Millisecond

func revisedUpdateRateFor(requested time.Duration) time.Duration {
	if requested <= 0 {
		return revisionQuantum
	}
	quanta := (requested + revisionQuantum - 1) / revisionQuantum
	return quanta * revisionQuantum
}

// scriptedSubscription is one DA group. Its pending set is per-subscription and
// keyed by ItemID, so it is bounded by the item count and cannot overflow —
// the same shape the DA core's subscription has, for the same reason: DA is
// update-rate sampling, so only the latest value between ticks exists.
type scriptedSubscription struct {
	source *scriptedSource
	info   opcda.SubscriptionInfo

	updates chan struct{}
	done    chan struct{}
	stop    chan struct{}

	mu        sync.Mutex
	pending   map[opcda.DAItemID]opcda.SubscriptionValue
	order     []opcda.DAItemID
	err       error
	closed    bool
	firstTick bool
}

func (s *scriptedSource) Subscribe(_ context.Context, request opcda.SubscribeRequest) (opcda.Subscription, error) {
	s.mu.Lock()

	revised := revisedUpdateRateFor(request.RequestedUpdateRate)
	s.nextSub++
	id := opcda.SubscriptionID(fmt.Sprintf("scripted-%d", s.nextSub))

	statuses := make([]opcda.SubscriptionItemStatus, 0, len(request.Items))
	active := 0
	watched := make([]*scriptedItem, 0, len(request.Items))
	for _, itemID := range request.Items {
		item, ok := s.byID[itemID]
		if !ok {
			// A failed item keeps its own HRESULT instead of failing the
			// whole group, exactly as AddItems reports per item.
			statuses = append(statuses, opcda.SubscriptionItemStatus{
				ItemID:         itemID,
				HRESULT:        opcda.HRESULT(-1073479673), // OPC_E_UNKNOWNITEMID
				HRESULTPresent: true,
			})
			continue
		}
		varType := item.varType
		itemRights := item.rights
		statuses = append(statuses, opcda.SubscriptionItemStatus{
			ItemID:         itemID,
			Active:         true,
			CanonicalType:  &varType,
			AccessRights:   &itemRights,
			HRESULT:        opcda.SOK,
			HRESULTPresent: true,
		})
		active++
		watched = append(watched, item)
	}

	sub := &scriptedSubscription{
		source: s,
		info: opcda.SubscriptionInfo{
			ID:                   id,
			ConnectionGeneration: 1,
			RequestedUpdateRate:  request.RequestedUpdateRate,
			RevisedUpdateRate:    revised,
			Deadband:             request.Deadband,
			Items:                statuses,
			ActiveItemCount:      active,
		},
		updates:   make(chan struct{}, 1),
		done:      make(chan struct{}),
		stop:      make(chan struct{}),
		pending:   make(map[opcda.DAItemID]opcda.SubscriptionValue),
		firstTick: true,
	}
	s.subs[id] = sub
	s.mu.Unlock()

	go sub.run(revised, watched)
	return sub, nil
}

func (s *scriptedSource) Unsubscribe(_ context.Context, id opcda.SubscriptionID) error {
	s.mu.Lock()
	sub, ok := s.subs[id]
	if ok {
		delete(s.subs, id)
	}
	s.mu.Unlock()

	if !ok {
		return opcda.NewAdapterError(opcda.CodeSubscriptionNotFound, "no such subscription")
	}
	sub.invalidate(nil)
	return nil
}

// run samples at the revised rate. The first tick reports every active item,
// which is the initial-value callback a DA server delivers on activation; later
// ticks report only the items that actually changed.
func (s *scriptedSubscription) run(rate time.Duration, watched []*scriptedItem) {
	ticker := time.NewTicker(rate)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.sample(watched)
		}
	}
}

func (s *scriptedSubscription) sample(watched []*scriptedItem) {
	s.source.mu.Lock()
	s.source.ticks++
	tick := s.source.ticks

	values := make([]opcda.SubscriptionValue, 0, len(watched))
	for _, item := range watched {
		if item.changes {
			advance(item, tick)
		}
		s.mu.Lock()
		first := s.firstTick
		s.mu.Unlock()
		if !first && !item.changes && !item.dirty {
			continue
		}
		item.dirty = false
		read := s.source.readLocked(item.itemID)
		values = append(values, opcda.SubscriptionValue{
			ItemID:         read.ItemID,
			Value:          read.Value,
			VarType:        read.VarType,
			CanonicalType:  read.CanonicalType,
			AccessRights:   read.AccessRights,
			HRESULT:        read.HRESULT,
			HRESULTPresent: read.HRESULTPresent,
			ErrorCode:      read.ErrorCode,
		})
	}
	s.source.mu.Unlock()

	if len(values) == 0 {
		return
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.firstTick = false
	for _, value := range values {
		if _, seen := s.pending[value.ItemID]; !seen {
			s.order = append(s.order, value.ItemID)
		}
		// A later value replaces an earlier one for the same item. Nothing
		// queues up, because only the latest cache value exists.
		s.pending[value.ItemID] = value
	}
	s.mu.Unlock()

	select {
	case s.updates <- struct{}{}:
	default:
	}
}

// advance moves a changing item's value on. The values are deterministic
// functions of the tick count so a client can tell a real change from a repeat.
func advance(item *scriptedItem, tick uint64) {
	switch item.varType {
	case opcda.VTR8:
		item.value = float64(tick%100) + 0.5
	case opcda.VTI4:
		item.value = int32(tick)
	}
}

func (s *scriptedSubscription) Info() opcda.SubscriptionInfo { return s.info }

func (s *scriptedSubscription) Updates() <-chan struct{} { return s.updates }

func (s *scriptedSubscription) Done() <-chan struct{} { return s.done }

func (s *scriptedSubscription) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *scriptedSubscription) Drain() []opcda.SubscriptionValue {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.order) == 0 {
		return nil
	}
	batch := make([]opcda.SubscriptionValue, 0, len(s.order))
	for _, id := range s.order {
		batch = append(batch, s.pending[id])
	}
	s.pending = make(map[opcda.DAItemID]opcda.SubscriptionValue)
	s.order = nil
	return batch
}

func (s *scriptedSubscription) invalidate(err error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.err = err
	s.mu.Unlock()

	close(s.stop)
	close(s.done)
}

// This source offers no OPC DA item properties. PROPERTIES_UNSUPPORTED is the
// same answer a real source without IOPCItemProperties gives.
func (*scriptedSource) AvailableItemProperties(context.Context, string) ([]opcda.AvailableProperty, error) {
	return nil, opcda.NewAdapterError(opcda.CodePropertiesUnsupported, "this source offers no item properties")
}

func (*scriptedSource) ItemProperties(context.Context, opcda.ItemPropertiesRequest) ([]opcda.ItemPropertyValue, error) {
	return nil, opcda.NewAdapterError(opcda.CodePropertiesUnsupported, "this source offers no item properties")
}
