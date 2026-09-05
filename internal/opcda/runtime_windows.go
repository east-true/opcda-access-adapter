//go:build windows

package opcda

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

type daThreadCommand struct {
	context context.Context
	name    string
	// enqueued is when the caller handed this command over, so the wait in the
	// queue can be separated from the vendor's call. It is zero unless the
	// runtime was configured to collect timings.
	enqueued time.Time
	run      func(*daThreadSession)
	// skipped answers the caller when the DA thread drops the command because
	// its context expired before the command could run.
	skipped func()
}

type daThreadSession struct {
	server               *iopcServer
	serverGroupHandle    uint32
	hasServerGroup       bool
	itemMgt              *iopcItemMgt
	syncIO               *iopcSyncIO
	browse               *iopcBrowseServerAddressSpace
	browseCapability     string
	properties           *iopcItemProperties
	propertiesCapability string
	subscribeCapability  string
	generation           uint64
	registrations        *registrationCache
	nextClientHandle     uint32

	subscriptions            map[SubscriptionID]*daSubscription
	nextGroupClientHandle    uint32
	nextSubscriptionSequence uint64
	lastGeneration           uint64
	reconnectAttempt         uint32
	reconnectAt              time.Time
	jitterState              uint64
}

type windowsRuntime struct {
	config   Config
	commands chan daThreadCommand
	wake     windowsHandle
	stop     chan struct{}
	stopped  chan struct{}
	stopOnce sync.Once
	degraded atomic.Bool

	statusMu sync.RWMutex
	status   RuntimeStatus

	// timings is nil unless Config.CollectTimings asked for it, which is what
	// makes a production build retain no operation history.
	timings *timingCollector
}

// TimingSnapshot separates this adapter's share of a DA operation from the
// vendor's. It is empty unless the runtime was configured to collect timings.
// It is a method on the concrete runtime rather than on the Runtime interface:
// every fake runtime in this repository would otherwise have to carry a
// measurement it does not make.
func (r *windowsRuntime) TimingSnapshot() TimingSnapshot {
	return r.timings.snapshot()
}

func New(config Config) (Runtime, error) {
	config = config.withDefaults()
	if err := config.validate(); err != nil {
		return nil, err
	}
	if config.Source.ProgID != "" && config.Source.CLSID != "" {
		return nil, fmt.Errorf("configure exactly one source ProgID or CLSID")
	}

	wake, err := createWakeEvent()
	if err != nil {
		return nil, err
	}
	daRuntime := &windowsRuntime{
		config:   config,
		timings:  newTimingCollector(config.CollectTimings),
		commands: make(chan daThreadCommand, config.Limits.CommandQueue),
		wake:     wake,
		stop:     make(chan struct{}),
		stopped:  make(chan struct{}),
		status: RuntimeStatus{
			State:        RuntimeStateStarting,
			Source:       config.Source,
			WriteEnabled: config.WriteEnabled,
			Capabilities: Capabilities{Browse: "unavailable", Properties: "unavailable"},
		},
	}

	started := make(chan error, 1)
	go daRuntime.runDAThread(started)
	if err := <-started; err != nil {
		return nil, err
	}
	return daRuntime, nil
}

func (r *windowsRuntime) runDAThread(started chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(r.stopped)
	defer r.wake.close()

	initialized, err := coInitializeSTA()
	if err != nil {
		r.setState(RuntimeStateDegraded)
		started <- err
		return
	}
	if initialized {
		defer coUninitialize()
	}
	// Ensure the STA has a message queue before any COM server can require it.
	pumpWindowMessages()

	session := &daThreadSession{}
	defer func() {
		finishWatchdog := r.beginCOMWatchdog("OPC DA shutdown cleanup")
		session.disconnect()
		finishWatchdog()
		r.updateStatus(func(status *RuntimeStatus) {
			status.State = RuntimeStateStopped
			status.Capabilities = Capabilities{Browse: "unavailable", Properties: "unavailable"}
			status.QueueDepth = 0
			status.SubscriptionCount = 0
		})
	}()

	if r.config.Source.ProgID == "" && r.config.Source.CLSID == "" {
		r.setState(RuntimeStateNotConfigured)
		started <- nil
	} else {
		r.setState(RuntimeStateConnecting)
		started <- nil
		r.tryConnect(session, false)
	}

	for {
		if !r.processReadyCommands(session) {
			return
		}
		// Connection point callbacks are marshalled into this STA as window
		// messages, so they must be dispatched while subscriptions exist.
		if len(session.subscriptions) > 0 {
			pumpWindowMessages()
		}
		if !r.degraded.Load() && !session.reconnectAt.IsZero() && !time.Now().Before(session.reconnectAt) {
			r.tryConnect(session, true)
			continue
		}
		if err := waitForDAWork(r.wake, session.waitDuration(time.Now(), r.degraded.Load())); err != nil {
			r.markDegraded("DA thread wait failed; process restart is required")
			return
		}
	}
}

func (r *windowsRuntime) connect(session *daThreadSession) error {
	clsid, err := resolveSourceCLSID(r.config.Source)
	if err != nil {
		return err
	}
	r.updateStatus(func(status *RuntimeStatus) {
		status.Source.CLSID = clsid.String()
	})

	server, err := coCreateOPCServer(&clsid)
	if err != nil {
		return err
	}
	session.server = server
	serverGroupHandle, itemMgt, err := addDAGroup(server)
	if err != nil {
		session.disconnect()
		return err
	}
	session.serverGroupHandle = serverGroupHandle
	session.hasServerGroup = true
	session.itemMgt = itemMgt
	syncIO, err := querySyncIO(itemMgt)
	if err != nil {
		session.disconnect()
		return err
	}
	session.syncIO = syncIO
	subscribeSupported, subscribeErr := probeDataCallbackSupport(itemMgt)
	switch {
	case isConnectionLoss(subscribeErr):
		session.disconnect()
		return subscribeErr
	case subscribeErr != nil:
		session.subscribeCapability = "unavailable"
	case !subscribeSupported:
		session.subscribeCapability = "unsupported"
	default:
		session.subscribeCapability = "supported"
	}
	browse, supported, browseErr := queryBrowseInterface(server)
	switch {
	case isConnectionLoss(browseErr):
		session.disconnect()
		return browseErr
	case browseErr != nil:
		session.browseCapability = "unavailable"
	case !supported:
		session.browseCapability = "unsupported"
	default:
		session.browse = browse
		session.browseCapability = "supported"
	}
	// IOPCItemProperties is optional in exactly the way browsing is: a source
	// that does not implement it is working correctly and simply has no
	// properties to offer.
	properties, propertiesSupported, propertiesErr := queryPropertiesInterface(server)
	switch {
	case isConnectionLoss(propertiesErr):
		session.disconnect()
		return propertiesErr
	case propertiesErr != nil:
		session.propertiesCapability = "unavailable"
	case !propertiesSupported:
		session.propertiesCapability = "unsupported"
	default:
		session.properties = properties
		session.propertiesCapability = "supported"
	}
	session.beginConnectionGeneration(r.config.Limits.MaxRegisteredItems)
	session.reconnectAttempt = 0
	session.reconnectAt = time.Time{}
	return nil
}

func (session *daThreadSession) beginConnectionGeneration(maxRegisteredItems int) {
	session.lastGeneration++
	session.generation = session.lastGeneration
	session.registrations = newRegistrationCache(maxRegisteredItems, session.generation)
	session.subscriptions = make(map[SubscriptionID]*daSubscription)
	session.nextSubscriptionSequence = 0
	// Group client handle 1 belongs to the shared SyncIO group, so subscription
	// groups start at 2 and no OnDataChange can be attributed ambiguously.
	session.nextGroupClientHandle = 1
}

func (r *windowsRuntime) tryConnect(session *daThreadSession, reconnect bool) {
	if r.degraded.Load() {
		return
	}
	if reconnect {
		r.updateStatus(func(status *RuntimeStatus) {
			status.State = RuntimeStateReconnecting
			status.ReconnectCount++
		})
	} else {
		r.setState(RuntimeStateConnecting)
	}
	finishWatchdog := r.beginCOMWatchdog("OPC DA connect")
	err := r.connect(session)
	finishWatchdog()
	if r.degraded.Load() {
		return
	}
	if err != nil {
		r.recordSourceFailure("OPC DA connect", err)
		session.disconnect()
		r.scheduleReconnect(session)
		return
	}
	r.updateStatus(func(status *RuntimeStatus) {
		status.State = RuntimeStateConnected
		status.ConnectionGeneration = session.generation
		status.Capabilities = Capabilities{
			Browse:     session.browseCapability,
			Read:       true,
			Write:      true,
			Subscribe:  session.subscribeCapability == "supported",
			Properties: session.propertiesCapability,
		}
		status.DegradedReason = ""
		status.LastSourceError = SourceDiagnostic{}
		status.LastSourceErrorSet = false
	})
}

func (r *windowsRuntime) scheduleReconnect(session *daThreadSession) {
	r.setState(RuntimeStateDisconnected)
	delay := reconnectDelay(session.reconnectAttempt, r.config.ReconnectInitial, r.config.ReconnectMax, session.nextJitter())
	if session.reconnectAttempt < 63 {
		session.reconnectAttempt++
	}
	session.reconnectAt = time.Now().Add(delay)
}

func (session *daThreadSession) nextJitter() uint64 {
	if session.jitterState == 0 {
		session.jitterState = uint64(time.Now().UnixNano()) | 1
	}
	value := session.jitterState
	value ^= value << 13
	value ^= value >> 7
	value ^= value << 17
	session.jitterState = value
	return value
}

func (session *daThreadSession) waitDuration(now time.Time, degraded bool) time.Duration {
	if degraded || session.reconnectAt.IsZero() {
		return -1
	}
	if !now.Before(session.reconnectAt) {
		return 0
	}
	return session.reconnectAt.Sub(now)
}

func (session *daThreadSession) disconnect() {
	session.invalidateSubscriptions(NewAdapterError(
		CodeSubscriptionInvalidated,
		"OPC DA subscription was invalidated by source disconnect; explicit resubscribe is required",
	))
	if session.browse != nil {
		session.browse.release()
		session.browse = nil
	}
	if session.properties != nil {
		session.properties.release()
		session.properties = nil
	}
	session.browseCapability = "unavailable"
	session.propertiesCapability = "unavailable"
	session.subscribeCapability = "unavailable"
	if session.syncIO != nil {
		session.syncIO.release()
		session.syncIO = nil
	}
	if session.itemMgt != nil {
		session.itemMgt.release()
		session.itemMgt = nil
	}
	if session.hasServerGroup {
		_ = removeDAGroup(session.server, session.serverGroupHandle)
	}
	session.serverGroupHandle = 0
	session.hasServerGroup = false
	if session.server != nil {
		session.server.release()
		session.server = nil
	}
	if session.registrations != nil {
		session.registrations.reset(0)
		session.registrations = nil
	}
	session.generation = 0
}

// invalidateSubscriptions releases every DA group and advise cookie of the
// ending connection generation. Pending values are dropped rather than
// delivered, and no subscription is re-created implicitly on reconnect.
func (session *daThreadSession) invalidateSubscriptions(err error) {
	for id, subscription := range session.subscriptions {
		subscription.teardown(session.server, err)
		delete(session.subscriptions, id)
	}
}

func (r *windowsRuntime) processReadyCommands(session *daThreadSession) bool {
	for {
		if r.degraded.Load() {
			select {
			case <-r.stop:
				r.setState(RuntimeStateStopping)
				return false
			default:
				return true
			}
		}
		select {
		case <-r.stop:
			r.setState(RuntimeStateStopping)
			return false
		case command := <-r.commands:
			r.updateQueueDepth()
			if command.context.Err() == nil {
				dequeued := time.Now()
				finishWatchdog := r.beginCOMWatchdog(command.name)
				startedCall := time.Now()
				command.run(session)
				finishedCall := time.Now()
				finishWatchdog()
				if r.timings != nil {
					// The watchdog brackets the call on both sides, so the
					// dispatch figure is this thread's own overhead per
					// command and not part of the vendor's.
					r.timings.record(
						dequeued.Sub(command.enqueued),
						finishedCall.Sub(startedCall),
						time.Since(finishedCall)+startedCall.Sub(dequeued),
					)
				}
			} else if command.skipped != nil {
				command.skipped()
			}
		default:
			r.updateQueueDepth()
			return true
		}
	}
}

func (r *windowsRuntime) Status(context.Context) RuntimeStatus {
	r.statusMu.RLock()
	defer r.statusMu.RUnlock()
	status := r.status
	status.QueueDepth = len(r.commands)
	return status
}

func (r *windowsRuntime) Browse(ctx context.Context, request BrowseRequest) (BrowseResult, error) {
	if request.Filter == "" {
		request.Filter = BrowseFilterAll
	}
	if request.Filter != BrowseFilterAll && request.Filter != BrowseFilterBranch && request.Filter != BrowseFilterItem {
		return BrowseResult{}, NewAdapterError(CodeInvalidRequest, "browse filter must be all, branch, or item")
	}
	if len(request.Path) > r.config.Limits.MaxBrowseDepth {
		return BrowseResult{}, NewAdapterError(CodeRequestLimitExceeded, "Browse path depth limit exceeded")
	}
	for _, segment := range request.Path {
		if segment == "" {
			return BrowseResult{}, NewAdapterError(CodeInvalidRequest, "browse path segments must not be empty")
		}
		if len([]byte(segment)) > r.config.Limits.MaxItemIDBytes {
			return BrowseResult{}, NewAdapterError(CodeItemIDTooLong, "browse path segment exceeds configured limit")
		}
		for _, character := range segment {
			if character == 0 {
				return BrowseResult{}, NewAdapterError(CodeInvalidRequest, "browse path must not contain NUL")
			}
		}
	}

	type response struct {
		result BrowseResult
		err    error
	}
	responses := make(chan response, 1)
	command := daThreadCommand{
		context: ctx,
		name:    "Browse",
		run: func(session *daThreadSession) {
			result, err := session.browseAddressSpace(request, r.config.Limits)
			r.handleOperationFailure(session, err)
			responses <- response{result: result, err: err}
		},
	}
	if err := r.enqueue(ctx, command); err != nil {
		return BrowseResult{}, err
	}
	select {
	case response := <-responses:
		return response.result, response.err
	case <-ctx.Done():
		return BrowseResult{}, &AdapterError{Code: CodeRuntimeDeadline, Message: "Browse deadline exceeded", Cause: ctx.Err()}
	}
}

func (r *windowsRuntime) ReadBatch(ctx context.Context, request ReadRequest) ([]ReadResult, error) {
	if request.Source == "" {
		request.Source = DADataSourceDevice
	}
	if request.Source != DADataSourceDevice {
		return nil, NewAdapterError(CodeInvalidRequest, "v0 Read source must be device")
	}
	if len(request.Items) == 0 {
		return nil, NewAdapterError(CodeInvalidRequest, "Read requires at least one item")
	}
	if len(request.Items) > r.config.Limits.MaxReadItems {
		return nil, NewAdapterError(CodeRequestLimitExceeded, "Read item limit exceeded")
	}
	for _, itemID := range request.Items {
		if itemID == "" {
			return nil, NewAdapterError(CodeInvalidRequest, "itemId must not be empty")
		}
		for _, character := range itemID {
			if character == 0 {
				return nil, NewAdapterError(CodeInvalidRequest, "itemId must not contain NUL")
			}
		}
		if len([]byte(itemID)) > r.config.Limits.MaxItemIDBytes {
			return nil, NewAdapterError(CodeItemIDTooLong, "itemId exceeds configured limit")
		}
	}

	type response struct {
		results []ReadResult
		err     error
	}
	responses := make(chan response, 1)
	command := daThreadCommand{
		context: ctx,
		name:    "Read",
		run: func(session *daThreadSession) {
			if session.syncIO == nil || session.registrations == nil {
				responses <- response{err: NewAdapterError(CodeRuntimeUnavailable, "OPC DA runtime is not connected")}
				return
			}
			results, err := session.readDevice(request.Items, r.config.Limits.MaxBSTRCodeUnits)
			r.handleOperationFailure(session, err)
			responses <- response{results: results, err: err}
		},
	}
	if err := r.enqueue(ctx, command); err != nil {
		return nil, err
	}
	select {
	case response := <-responses:
		return response.results, response.err
	case <-ctx.Done():
		return nil, &AdapterError{Code: CodeRuntimeDeadline, Message: "Read deadline exceeded", Cause: ctx.Err()}
	}
}

func (r *windowsRuntime) WriteBatch(ctx context.Context, items []WriteItem) ([]WriteResult, error) {
	if !r.config.WriteEnabled {
		return nil, NewAdapterError(CodeWriteDisabled, "write is disabled")
	}
	if len(items) == 0 {
		return nil, NewAdapterError(CodeInvalidRequest, "Write requires at least one item")
	}
	if len(items) > r.config.Limits.MaxWriteItems {
		return nil, NewAdapterError(CodeRequestLimitExceeded, "Write item limit exceeded")
	}
	for _, item := range items {
		if item.ItemID == "" {
			return nil, NewAdapterError(CodeInvalidRequest, "itemId must not be empty")
		}
		for _, character := range item.ItemID {
			if character == 0 {
				return nil, NewAdapterError(CodeInvalidRequest, "itemId must not contain NUL")
			}
		}
		if len([]byte(item.ItemID)) > r.config.Limits.MaxItemIDBytes {
			return nil, NewAdapterError(CodeItemIDTooLong, "itemId exceeds configured limit")
		}
		if err := validateWriteValue(item.VarType, item.Value, r.config.Limits.MaxBSTRCodeUnits); err != nil {
			return nil, err
		}
	}

	type response struct {
		results []WriteResult
		err     error
	}
	responses := make(chan response, 1)
	command := daThreadCommand{
		context: ctx,
		name:    "Write",
		run: func(session *daThreadSession) {
			if session.syncIO == nil || session.registrations == nil {
				responses <- response{err: NewAdapterError(CodeRuntimeUnavailable, "OPC DA runtime is not connected")}
				return
			}
			results, err := session.writeValues(items, r.config.Limits.MaxBSTRCodeUnits)
			r.handleOperationFailure(session, err)
			responses <- response{results: results, err: err}
		},
	}
	if err := r.enqueue(ctx, command); err != nil {
		return nil, err
	}
	select {
	case response := <-responses:
		return response.results, response.err
	case <-ctx.Done():
		// An in-flight COM Write is deliberately not cancelled or replayed. The
		// buffered response channel lets the owning DA thread finish safely.
		return nil, &AdapterError{Code: CodeRuntimeDeadline, Message: "Write deadline exceeded; source outcome may be unknown", Cause: ctx.Err()}
	}
}

func (r *windowsRuntime) Subscribe(ctx context.Context, request SubscribeRequest) (Subscription, error) {
	if err := request.validate(r.config.Limits); err != nil {
		return nil, err
	}

	type response struct {
		subscription Subscription
		err          error
	}
	responses := make(chan response, 1)
	deadline := func() response {
		return response{err: &AdapterError{
			Code:    CodeRuntimeDeadline,
			Message: "Subscribe deadline exceeded",
			Cause:   ctx.Err(),
		}}
	}
	command := daThreadCommand{
		context: ctx,
		name:    "Subscribe",
		skipped: func() { responses <- deadline() },
		run: func(session *daThreadSession) {
			if session.server == nil || session.subscriptions == nil {
				responses <- response{err: NewAdapterError(CodeRuntimeUnavailable, "OPC DA runtime is not connected")}
				return
			}
			if session.subscribeCapability != "supported" {
				if session.subscribeCapability == "unsupported" {
					responses <- response{err: NewAdapterError(CodeSubscribeUnsupported, "OPC DA server does not support callback subscriptions")}
					return
				}
				responses <- response{err: NewAdapterError(CodeRuntimeUnavailable, "OPC DA Subscribe is unavailable")}
				return
			}
			if len(session.subscriptions) >= r.config.Limits.MaxSubscriptions {
				responses <- response{err: NewAdapterError(CodeSubscriptionLimit, "subscription limit exceeded")}
				return
			}
			session.nextSubscriptionSequence++
			id := subscriptionIDFor(session.generation, session.nextSubscriptionSequence)
			subscription, err := session.createSubscription(id, request, r.config.Limits.MaxBSTRCodeUnits)
			if err != nil {
				r.handleOperationFailure(session, err)
				responses <- response{err: err}
				return
			}
			// An advised DA group must never outlive the caller that asked for
			// it, so a group created after the deadline is torn down here on the
			// owning thread rather than left unowned in the session.
			if ctx.Err() != nil {
				subscription.teardown(session.server, NewAdapterError(
					CodeSubscriptionInvalidated,
					"OPC DA subscription was removed because Subscribe exceeded its deadline",
				))
				responses <- deadline()
				return
			}
			session.subscriptions[id] = subscription
			r.updateSubscriptionCount(len(session.subscriptions))
			responses <- response{subscription: subscription}
		},
	}
	if err := r.enqueue(ctx, command); err != nil {
		return nil, err
	}
	select {
	case response := <-responses:
		return response.subscription, response.err
	case <-r.stopped:
		return nil, NewAdapterError(CodeRuntimeUnavailable, "OPC DA runtime stopped before Subscribe completed")
	case <-ctx.Done():
		// A subscription the DA thread finished creating is handed over rather
		// than orphaned; otherwise the command tears it down itself.
		select {
		case response := <-responses:
			return response.subscription, response.err
		default:
			return nil, deadline().err
		}
	}
}

func (r *windowsRuntime) Unsubscribe(ctx context.Context, id SubscriptionID) error {
	if id == "" {
		return NewAdapterError(CodeInvalidRequest, "subscriptionId must not be empty")
	}
	responses := make(chan error, 1)
	command := daThreadCommand{
		context: ctx,
		name:    "Unsubscribe",
		skipped: func() {
			responses <- &AdapterError{Code: CodeRuntimeDeadline, Message: "Unsubscribe deadline exceeded", Cause: ctx.Err()}
		},
		run: func(session *daThreadSession) {
			if session.subscriptions == nil {
				responses <- NewAdapterError(CodeRuntimeUnavailable, "OPC DA runtime is not connected")
				return
			}
			subscription, ok := session.subscriptions[id]
			if !ok {
				responses <- NewAdapterError(CodeSubscriptionNotFound, "subscription is not known to this connection generation")
				return
			}
			delete(session.subscriptions, id)
			subscription.teardown(session.server, NewAdapterError(
				CodeSubscriptionInvalidated,
				"OPC DA subscription was removed by an explicit unsubscribe",
			))
			r.updateSubscriptionCount(len(session.subscriptions))
			responses <- nil
		},
	}
	if err := r.enqueue(ctx, command); err != nil {
		return err
	}
	select {
	case err := <-responses:
		return err
	case <-r.stopped:
		return NewAdapterError(CodeRuntimeUnavailable, "OPC DA runtime stopped before Unsubscribe completed")
	case <-ctx.Done():
		select {
		case err := <-responses:
			return err
		default:
			return &AdapterError{Code: CodeRuntimeDeadline, Message: "Unsubscribe deadline exceeded", Cause: ctx.Err()}
		}
	}
}

func (r *windowsRuntime) updateSubscriptionCount(count int) {
	r.updateStatus(func(status *RuntimeStatus) {
		status.SubscriptionCount = count
	})
}

func (r *windowsRuntime) Shutdown(ctx context.Context) error {
	r.stopOnce.Do(func() {
		r.setState(RuntimeStateStopping)
		close(r.stop)
		_ = r.wake.signal()
	})
	select {
	case <-r.stopped:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *windowsRuntime) enqueue(ctx context.Context, command daThreadCommand) error {
	if r.degraded.Load() {
		return NewAdapterError(CodeRuntimeUnavailable, "OPC DA runtime is degraded; process restart is required")
	}
	r.statusMu.RLock()
	state := r.status.State
	r.statusMu.RUnlock()
	if state != RuntimeStateConnected {
		return NewAdapterError(CodeRuntimeUnavailable, "OPC DA runtime is not connected")
	}
	select {
	case <-ctx.Done():
		return &AdapterError{Code: CodeRuntimeDeadline, Message: "request deadline exceeded before enqueue", Cause: ctx.Err()}
	default:
	}
	if r.timings != nil {
		command.enqueued = time.Now()
	}
	select {
	case r.commands <- command:
		r.updateQueueDepth()
		if err := r.wake.signal(); err != nil {
			r.markDegraded("failed to wake the DA thread; process restart is required")
			return &AdapterError{
				Code:    CodeRuntimeUnavailable,
				Message: command.name + " queue wake failed; source outcome may be unknown and process restart is required",
				Cause:   err,
			}
		}
		return nil
	default:
		return NewAdapterError(CodeQueueFull, "DA command queue is full")
	}
}

func (r *windowsRuntime) handleOperationFailure(session *daThreadSession, err error) {
	if !isConnectionLoss(err) || r.degraded.Load() {
		return
	}
	r.recordSourceFailure("OPC DA operation", err)
	session.disconnect()
	r.scheduleReconnect(session)
}

func (r *windowsRuntime) recordSourceFailure(fallbackOperation string, err error) {
	diagnostic := SourceDiagnostic{Operation: fallbackOperation}
	var sourceError *SourceError
	var callError *comCallError
	switch {
	case errors.As(err, &sourceError):
		diagnostic.Operation = sourceError.Operation
		diagnostic.HRESULT = sourceError.HRESULT
		diagnostic.HRESULTPresent = true
	case errors.As(err, &callError):
		diagnostic.Operation = callError.Operation
		diagnostic.HRESULT = callError.HRESULT
		diagnostic.HRESULTPresent = true
	}
	r.updateStatus(func(status *RuntimeStatus) {
		status.LastSourceError = diagnostic
		status.LastSourceErrorSet = true
	})
}

func (r *windowsRuntime) beginCOMWatchdog(operation string) func() {
	done := make(chan struct{})
	var once sync.Once
	timer := time.NewTimer(r.config.COMCallWatchdog)
	go func() {
		select {
		case <-timer.C:
			r.markDegraded(operation + " exceeded the COM call watchdog; process restart is required")
		case <-done:
		}
	}()
	return func() {
		once.Do(func() {
			if timer.Stop() {
				close(done)
			}
		})
	}
}

func (r *windowsRuntime) markDegraded(reason string) {
	r.degraded.Store(true)
	r.updateStatus(func(status *RuntimeStatus) {
		status.State = RuntimeStateDegraded
		status.Capabilities = Capabilities{Browse: "unavailable", Properties: "unavailable"}
		status.SubscriptionCount = 0
		status.DegradedReason = reason
	})
}

func (r *windowsRuntime) setState(state RuntimeState) {
	r.updateStatus(func(status *RuntimeStatus) {
		status.State = state
		if state != RuntimeStateConnected {
			status.Capabilities = Capabilities{Browse: "unavailable", Properties: "unavailable"}
			status.SubscriptionCount = 0
		}
	})
}

func (r *windowsRuntime) updateQueueDepth() {
	r.updateStatus(func(status *RuntimeStatus) {
		status.QueueDepth = len(r.commands)
	})
}

func (r *windowsRuntime) updateStatus(update func(*RuntimeStatus)) {
	r.statusMu.Lock()
	defer r.statusMu.Unlock()
	update(&r.status)
}

// AvailableItemProperties reports which properties the source offers for one
// item, in the source's own order.
func (r *windowsRuntime) AvailableItemProperties(ctx context.Context, itemID string) ([]AvailableProperty, error) {
	if err := r.validatePropertyItemID(itemID); err != nil {
		return nil, err
	}

	type response struct {
		available []AvailableProperty
		err       error
	}
	responses := make(chan response, 1)
	command := daThreadCommand{
		context: ctx,
		name:    "QueryAvailableProperties",
		run: func(session *daThreadSession) {
			available, err := session.queryAvailableProperties(itemID, r.config.Limits)
			r.handleOperationFailure(session, err)
			responses <- response{available: available, err: err}
		},
	}
	if err := r.enqueue(ctx, command); err != nil {
		return nil, err
	}
	select {
	case response := <-responses:
		return response.available, response.err
	case <-ctx.Done():
		return nil, &AdapterError{Code: CodeRuntimeDeadline, Message: "QueryAvailableProperties deadline exceeded", Cause: ctx.Err()}
	}
}

// ItemProperties reads property values for one item. Results match the
// requested identifiers in size and order.
func (r *windowsRuntime) ItemProperties(ctx context.Context, request ItemPropertiesRequest) ([]ItemPropertyValue, error) {
	if err := r.validatePropertyItemID(request.ItemID); err != nil {
		return nil, err
	}
	if len(request.Properties) == 0 {
		return nil, NewAdapterError(CodeInvalidRequest, "ItemProperties requires at least one property")
	}
	if len(request.Properties) > r.config.Limits.MaxItemProperties {
		return nil, NewAdapterError(CodeRequestLimitExceeded, "item property limit exceeded")
	}
	// The value and quality of an item belong to Read and Subscribe, which
	// carry the timestamp and the raw quality with them. Answering the same
	// question here would produce a second, poorer answer to it.
	for _, property := range request.Properties {
		if property == PropertyValue || property == PropertyQuality || property == PropertyTimestamp {
			return nil, NewAdapterError(CodeInvalidRequest,
				"item value, quality and timestamp are read through Read or Subscribe, not as properties")
		}
	}

	type response struct {
		values []ItemPropertyValue
		err    error
	}
	responses := make(chan response, 1)
	command := daThreadCommand{
		context: ctx,
		name:    "GetItemProperties",
		run: func(session *daThreadSession) {
			values, err := session.getItemProperties(request, r.config.Limits)
			r.handleOperationFailure(session, err)
			responses <- response{values: values, err: err}
		},
	}
	if err := r.enqueue(ctx, command); err != nil {
		return nil, err
	}
	select {
	case response := <-responses:
		return response.values, response.err
	case <-ctx.Done():
		return nil, &AdapterError{Code: CodeRuntimeDeadline, Message: "GetItemProperties deadline exceeded", Cause: ctx.Err()}
	}
}

func (r *windowsRuntime) validatePropertyItemID(itemID string) error {
	if itemID == "" {
		return NewAdapterError(CodeInvalidRequest, "itemId must not be empty")
	}
	for _, character := range itemID {
		if character == 0 {
			return NewAdapterError(CodeInvalidRequest, "itemId must not contain NUL")
		}
	}
	if len([]byte(itemID)) > r.config.Limits.MaxItemIDBytes {
		return NewAdapterError(CodeItemIDTooLong, "itemId exceeds configured limit")
	}
	return nil
}
