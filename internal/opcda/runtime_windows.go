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
	server *iUnknown
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
	if config.Limits.CommandQueue <= 0 {
		return nil, fmt.Errorf("DA command queue limit must be positive")
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
		if session.server != nil {
			session.server.release()
			session.server = nil
		}
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
	r.updateStatus(func(status *RuntimeStatus) {
		status.State = RuntimeStateConnected
		status.ConnectionGeneration = 1
		// AddGroup and operation interfaces are established in Phase 2.
		status.Capabilities = Capabilities{Browse: "unavailable"}
	})
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

func (*windowsRuntime) ReadBatch(context.Context, ReadRequest) ([]ReadResult, error) {
	return nil, NewAdapterError(CodeRuntimeUnavailable, "OPC DA Read is not initialized")
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
