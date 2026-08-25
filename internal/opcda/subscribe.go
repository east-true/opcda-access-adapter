package opcda

import (
	"fmt"
	"sync"
	"time"
)

// SubscriptionID identifies one DA subscription for the lifetime of one
// connection generation. It is never reused after invalidation.
type SubscriptionID string

// SubscribeRequest mirrors the OPC DA 2.05a group concept. A subscription is
// exactly one DA group: an update rate, an optional percent deadband, and a
// set of active items delivered through IOPCDataCallback::OnDataChange.
type SubscribeRequest struct {
	Items []DAItemID
	// RequestedUpdateRate is dwRequestedUpdateRate. The server answers with a
	// revised rate that the adapter reports unchanged.
	RequestedUpdateRate time.Duration
	// Deadband is pPercentDeadband. Zero means the parameter is not sent.
	Deadband float32
}

// SubscriptionItemStatus preserves the exact per-item AddItems outcome. Failed
// items keep their source HRESULT instead of failing the whole subscription.
type SubscriptionItemStatus struct {
	ItemID         DAItemID
	Active         bool
	CanonicalType  *DAVarType
	AccessRights   *DAAccessRights
	HRESULT        HRESULT
	HRESULTPresent bool
	ErrorCode      string
}

// SubscriptionValue is one coalesced OnDataChange entry. Its shape mirrors
// ReadResult so an item the source reported as failed keeps its exact HRESULT
// instead of being silently dropped from the batch.
type SubscriptionValue struct {
	ItemID         DAItemID
	Value          *DAValue
	VarType        *DAVarType
	CanonicalType  *DAVarType
	AccessRights   *DAAccessRights
	HRESULT        HRESULT
	HRESULTPresent bool
	ErrorCode      string
}

// SubscriptionInfo is an immutable snapshot taken when the DA group was
// created. RevisedUpdateRate is the server's answer, never the requested value.
type SubscriptionInfo struct {
	ID                   SubscriptionID
	ConnectionGeneration uint64
	RequestedUpdateRate  time.Duration
	RevisedUpdateRate    time.Duration
	Deadband             float32
	Items                []SubscriptionItemStatus
	ActiveItemCount      int
}

// Subscription is the DA-native subscription handle. It is safe to use from a
// goroutine other than the owning DA thread.
//
// Delivery follows the DA group model rather than an event log. OPC DA already
// samples: between two update-rate ticks a server reports only the latest cache
// value, so the adapter coalesces per item in the same way. A consumer slower
// than the revised update rate therefore observes the same values it would have
// observed at a slower requested rate. No notification queue overflows, no
// synthetic drop counter exists, and a slow consumer never terminates the
// subscription, because OPC DA defines none of those.
type Subscription interface {
	Info() SubscriptionInfo
	// Updates is signalled whenever the pending set becomes non-empty.
	Updates() <-chan struct{}
	// Drain removes and returns the whole pending set as one batch, preserving
	// first-seen order. It returns nil when nothing is pending.
	Drain() []SubscriptionValue
	// Done is closed when the subscription is invalidated by unsubscribe,
	// source disconnect, or shutdown.
	Done() <-chan struct{}
	// Err reports why the subscription was invalidated, or nil while live.
	Err() error
}

const (
	// DA servers commonly reject sub-millisecond rates and dwRequestedUpdateRate
	// is expressed in whole milliseconds.
	minimumUpdateRate = time.Millisecond
	maximumUpdateRate = time.Hour
)

func (request SubscribeRequest) validate(limits Limits) error {
	if len(request.Items) == 0 {
		return NewAdapterError(CodeInvalidRequest, "Subscribe requires at least one item")
	}
	if len(request.Items) > limits.MaxSubscriptionItems {
		return NewAdapterError(CodeRequestLimitExceeded, "Subscribe item limit exceeded")
	}
	seen := make(map[DAItemID]struct{}, len(request.Items))
	for _, itemID := range request.Items {
		if itemID == "" {
			return NewAdapterError(CodeInvalidRequest, "itemId must not be empty")
		}
		for _, character := range itemID {
			if character == 0 {
				return NewAdapterError(CodeInvalidRequest, "itemId must not contain NUL")
			}
		}
		if len([]byte(itemID)) > limits.MaxItemIDBytes {
			return NewAdapterError(CodeItemIDTooLong, "itemId exceeds configured limit")
		}
		if _, duplicate := seen[itemID]; duplicate {
			return NewAdapterError(CodeInvalidRequest, "Subscribe items must be unique")
		}
		seen[itemID] = struct{}{}
	}
	if request.RequestedUpdateRate < minimumUpdateRate || request.RequestedUpdateRate > maximumUpdateRate {
		return NewAdapterError(CodeInvalidRequest, "requested update rate must be between 1ms and 1h")
	}
	if request.RequestedUpdateRate%time.Millisecond != 0 {
		return NewAdapterError(CodeInvalidRequest, "requested update rate must be a whole number of milliseconds")
	}
	if request.Deadband < 0 || request.Deadband > 100 {
		return NewAdapterError(CodeInvalidRequest, "percent deadband must be between 0 and 100")
	}
	return nil
}

// pendingUpdates is the per-subscription coalescing set. Its size is bounded by
// the subscription's active item count, so it has no overflow condition.
type pendingUpdates struct {
	mu     sync.Mutex
	order  []uint32
	latest map[uint32]SubscriptionValue
	notify chan struct{}
	done   chan struct{}
	err    error
	closed bool
}

func newPendingUpdates(capacity int) *pendingUpdates {
	return &pendingUpdates{
		order:  make([]uint32, 0, capacity),
		latest: make(map[uint32]SubscriptionValue, capacity),
		notify: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
}

// merge coalesces one OnDataChange batch. A handle already pending keeps its
// first-seen position and takes the newer tuple, which is exactly what a DA
// server does between update-rate ticks.
func (pending *pendingUpdates) merge(handles []uint32, values []SubscriptionValue) {
	if len(handles) == 0 {
		return
	}
	pending.mu.Lock()
	if pending.closed {
		pending.mu.Unlock()
		return
	}
	for index, handle := range handles {
		if _, exists := pending.latest[handle]; !exists {
			pending.order = append(pending.order, handle)
		}
		pending.latest[handle] = values[index]
	}
	empty := len(pending.latest) == 0
	pending.mu.Unlock()
	if empty {
		return
	}
	select {
	case pending.notify <- struct{}{}:
	default:
	}
}

func (pending *pendingUpdates) drain() []SubscriptionValue {
	pending.mu.Lock()
	defer pending.mu.Unlock()
	if len(pending.order) == 0 {
		return nil
	}
	values := make([]SubscriptionValue, 0, len(pending.order))
	for _, handle := range pending.order {
		values = append(values, pending.latest[handle])
	}
	pending.order = pending.order[:0]
	clear(pending.latest)
	return values
}

// invalidate drops the pending set. Values captured under a dead connection
// generation are never delivered, matching the no-last-good-data invariant.
func (pending *pendingUpdates) invalidate(err error) {
	pending.mu.Lock()
	if pending.closed {
		pending.mu.Unlock()
		return
	}
	pending.closed = true
	pending.err = err
	pending.order = nil
	pending.latest = nil
	pending.mu.Unlock()
	close(pending.done)
}

func (pending *pendingUpdates) failure() error {
	pending.mu.Lock()
	defer pending.mu.Unlock()
	return pending.err
}

func (pending *pendingUpdates) size() int {
	pending.mu.Lock()
	defer pending.mu.Unlock()
	return len(pending.order)
}

func subscriptionIDFor(generation uint64, sequence uint64) SubscriptionID {
	return SubscriptionID(fmt.Sprintf("sub-%d-%d", generation, sequence))
}
