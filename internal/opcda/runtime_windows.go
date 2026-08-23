//go:build windows

package opcda

import (
	"context"
	"fmt"
	"runtime"
	"sync"
)

type daThreadCommand struct {
	context context.Context
	run     func(*daThreadSession)
}

type daThreadSession struct {
	server            *iopcServer
	serverGroupHandle uint32
	hasServerGroup    bool
	itemMgt           *iopcItemMgt
	syncIO            *iopcSyncIO
	generation        uint64
	registrations     *registrationCache
	nextClientHandle  uint32
}

type windowsRuntime struct {
	config   Config
	commands chan daThreadCommand
	wake     windowsHandle
	stop     chan struct{}
	stopped  chan struct{}
	stopOnce sync.Once

	statusMu sync.RWMutex
	status   RuntimeStatus
}

func New(config Config) (Runtime, error) {
	if err := config.Limits.validate(); err != nil {
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
		session.disconnect()
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
		r.connect(session)
	}

	for {
		if !r.processReadyCommands(session) {
			return
		}
		if err := waitForDAWork(r.wake); err != nil {
			r.setState(RuntimeStateDegraded)
			return
		}
	}
}

func (r *windowsRuntime) connect(session *daThreadSession) {
	clsid, err := resolveSourceCLSID(r.config.Source)
	if err != nil {
		r.setState(RuntimeStateDisconnected)
		return
	}
	r.updateStatus(func(status *RuntimeStatus) {
		status.Source.CLSID = clsid.String()
	})

	server, err := coCreateOPCServer(&clsid)
	if err != nil {
		r.setState(RuntimeStateDisconnected)
		return
	}
	session.server = server
	serverGroupHandle, itemMgt, err := addDAGroup(server)
	if err != nil {
		session.disconnect()
		r.setState(RuntimeStateDisconnected)
		return
	}
	session.serverGroupHandle = serverGroupHandle
	session.hasServerGroup = true
	session.itemMgt = itemMgt
	syncIO, err := querySyncIO(itemMgt)
	if err != nil {
		session.disconnect()
		r.setState(RuntimeStateDisconnected)
		return
	}
	session.syncIO = syncIO
	session.generation = 1
	session.registrations = newRegistrationCache(r.config.Limits.MaxRegisteredItems, session.generation)
	r.updateStatus(func(status *RuntimeStatus) {
		status.State = RuntimeStateConnected
		status.ConnectionGeneration = session.generation
		status.Capabilities = Capabilities{Browse: "unavailable", Read: true, Write: true}
	})
}

func (session *daThreadSession) disconnect() {
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
	}
	session.generation = 0
}

func (r *windowsRuntime) processReadyCommands(session *daThreadSession) bool {
	for {
		select {
		case <-r.stop:
			r.setState(RuntimeStateStopping)
			return false
		case command := <-r.commands:
			r.updateQueueDepth()
			if command.context.Err() == nil {
				command.run(session)
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

func (*windowsRuntime) Browse(context.Context, BrowseRequest) (BrowseResult, error) {
	return BrowseResult{}, NewAdapterError(CodeRuntimeUnavailable, "OPC DA Browse is not initialized")
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
		run: func(session *daThreadSession) {
			if session.syncIO == nil || session.registrations == nil {
				responses <- response{err: NewAdapterError(CodeRuntimeUnavailable, "OPC DA runtime is not connected")}
				return
			}
			results, err := session.readDevice(request.Items, r.config.Limits.MaxBSTRCodeUnits)
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

func (r *windowsRuntime) WriteBatch(context.Context, []WriteItem) ([]WriteResult, error) {
	if !r.config.WriteEnabled {
		return nil, NewAdapterError(CodeWriteDisabled, "write is disabled")
	}
	return nil, NewAdapterError(CodeRuntimeUnavailable, "OPC DA Write is not initialized")
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
	select {
	case <-ctx.Done():
		return &AdapterError{Code: CodeRuntimeDeadline, Message: "request deadline exceeded before enqueue", Cause: ctx.Err()}
	default:
	}
	select {
	case r.commands <- command:
		r.updateQueueDepth()
		if err := r.wake.signal(); err != nil {
			return &AdapterError{Code: CodeRuntimeUnavailable, Message: "failed to wake DA runtime", Cause: err}
		}
		return nil
	default:
		return NewAdapterError(CodeQueueFull, "DA command queue is full")
	}
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
