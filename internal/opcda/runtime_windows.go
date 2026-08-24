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
	run     func(*daThreadSession)
}

type daThreadSession struct {
	server            *iopcServer
	serverGroupHandle uint32
	hasServerGroup    bool
	itemMgt           *iopcItemMgt
	syncIO            *iopcSyncIO
	browse            *iopcBrowseServerAddressSpace
	browseCapability  string
	generation        uint64
	registrations     *registrationCache
	nextClientHandle  uint32
	lastGeneration    uint64
	reconnectAttempt  uint32
	reconnectAt       time.Time
	jitterState       uint64
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
		commands: make(chan daThreadCommand, config.Limits.CommandQueue),
		wake:     wake,
		stop:     make(chan struct{}),
		stopped:  make(chan struct{}),
		status: RuntimeStatus{
			State:        RuntimeStateStarting,
			Source:       config.Source,
			WriteEnabled: config.WriteEnabled,
			Capabilities: Capabilities{Browse: "unavailable"},
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
			status.Capabilities = Capabilities{Browse: "unavailable"}
			status.QueueDepth = 0
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
	session.beginConnectionGeneration(r.config.Limits.MaxRegisteredItems)
	session.reconnectAttempt = 0
	session.reconnectAt = time.Time{}
	return nil
}

func (session *daThreadSession) beginConnectionGeneration(maxRegisteredItems int) {
	session.lastGeneration++
	session.generation = session.lastGeneration
	session.registrations = newRegistrationCache(maxRegisteredItems, session.generation)
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
		status.Capabilities = Capabilities{Browse: session.browseCapability, Read: true, Write: true}
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
	if session.browse != nil {
		session.browse.release()
		session.browse = nil
	}
	session.browseCapability = "unavailable"
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
				finishWatchdog := r.beginCOMWatchdog(command.name)
				command.run(session)
				finishWatchdog()
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
		status.Capabilities = Capabilities{Browse: "unavailable"}
		status.DegradedReason = reason
	})
}

func (r *windowsRuntime) setState(state RuntimeState) {
	r.updateStatus(func(status *RuntimeStatus) {
		status.State = state
		if state != RuntimeStateConnected {
			status.Capabilities = Capabilities{Browse: "unavailable"}
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
