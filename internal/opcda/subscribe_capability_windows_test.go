//go:build windows

package opcda

import (
	"runtime"
	"sync/atomic"
	"syscall"
	"testing"
	"unsafe"
)

// A vendor DA 2.0 server may implement synchronous IO without connection
// points. These fakes reproduce that shape at the COM ABI level so the
// capability probe is exercised without a DA server.
// callResult converts an HRESULT into the raw value a COM method returns. It
// takes a parameter so the conversion is not a constant expression.
func callResult(hr HRESULT) uintptr {
	return uintptr(uint32(int32(hr)))
}

type fakeConnectionPoint struct {
	vtable   *iconnectionPointVTable
	releases atomic.Int32
}

type fakeContainer struct {
	vtable *iconnectionPointContainerVTable
}

type fakeItemMgt struct {
	vtable *iopcItemMgtVTable
}

type connectionPointFixture struct {
	itemMgt   *iopcItemMgt
	point     *fakeConnectionPoint
	container *fakeContainer
	owner     *fakeItemMgt

	queryResult          uintptr
	findResult           uintptr
	returnNilPoint       bool
	containerQueryCalled atomic.Int32
	findCalled           atomic.Int32

	pinner runtime.Pinner
}

func newConnectionPointFixture(t *testing.T) *connectionPointFixture {
	t.Helper()
	fixture := &connectionPointFixture{queryResult: sOK, findResult: sOK}

	fixture.point = &fakeConnectionPoint{}
	fixture.point.vtable = &iconnectionPointVTable{
		iUnknownVTable: iUnknownVTable{
			QueryInterface: syscall.NewCallback(func(uintptr, uintptr, uintptr) uintptr { return eNoInterface }),
			AddRef:         syscall.NewCallback(func(uintptr) uintptr { return 1 }),
			Release: syscall.NewCallback(func(uintptr) uintptr {
				fixture.point.releases.Add(1)
				return 0
			}),
		},
	}

	fixture.container = &fakeContainer{}
	fixture.container.vtable = &iconnectionPointContainerVTable{
		iUnknownVTable: iUnknownVTable{
			QueryInterface: syscall.NewCallback(func(uintptr, uintptr, uintptr) uintptr { return eNoInterface }),
			AddRef:         syscall.NewCallback(func(uintptr) uintptr { return 1 }),
			Release:        syscall.NewCallback(func(uintptr) uintptr { return 0 }),
		},
		FindConnectionPoint: syscall.NewCallback(func(_ uintptr, riid uintptr, out uintptr) uintptr {
			fixture.findCalled.Add(1)
			if out == 0 {
				return ePointer
			}
			*(*uintptr)(comPointer(out)) = 0
			if requested := (*guid)(comPointer(riid)); *requested != iidIOPCDataCallback {
				return callResult(ConnectENoConnection)
			}
			if fixture.findResult != sOK || fixture.returnNilPoint {
				return fixture.findResult
			}
			*(*uintptr)(comPointer(out)) = uintptr(unsafe.Pointer(fixture.point))
			return sOK
		}),
	}

	fixture.owner = &fakeItemMgt{}
	fixture.owner.vtable = &iopcItemMgtVTable{
		iUnknownVTable: iUnknownVTable{
			QueryInterface: syscall.NewCallback(func(_ uintptr, riid uintptr, out uintptr) uintptr {
				if out == 0 {
					return ePointer
				}
				*(*uintptr)(comPointer(out)) = 0
				if requested := (*guid)(comPointer(riid)); *requested != iidIConnectionPointContainer {
					return eNoInterface
				}
				fixture.containerQueryCalled.Add(1)
				if fixture.queryResult != sOK {
					return fixture.queryResult
				}
				*(*uintptr)(comPointer(out)) = uintptr(unsafe.Pointer(fixture.container))
				return sOK
			}),
			AddRef:  syscall.NewCallback(func(uintptr) uintptr { return 1 }),
			Release: syscall.NewCallback(func(uintptr) uintptr { return 0 }),
		},
	}

	fixture.pinner.Pin(fixture.point)
	fixture.pinner.Pin(fixture.point.vtable)
	fixture.pinner.Pin(fixture.container)
	fixture.pinner.Pin(fixture.container.vtable)
	fixture.pinner.Pin(fixture.owner)
	fixture.pinner.Pin(fixture.owner.vtable)
	t.Cleanup(fixture.pinner.Unpin)

	fixture.itemMgt = (*iopcItemMgt)(unsafe.Pointer(fixture.owner))
	return fixture
}

func TestSubscribeCapabilityProbeAcceptsAConnectionPoint(t *testing.T) {
	fixture := newConnectionPointFixture(t)
	supported, err := probeDataCallbackSupport(fixture.itemMgt)
	if err != nil {
		t.Fatalf("probe returned %v", err)
	}
	if !supported {
		t.Fatal("a server exposing IOPCDataCallback was reported as unsupported")
	}
	// Probing must not leak the connection point it only inspected.
	if releases := fixture.point.releases.Load(); releases != 1 {
		t.Fatalf("connection point releases = %d, want 1", releases)
	}
}

func TestSubscribeCapabilityProbeReportsMissingConnectionPointContainer(t *testing.T) {
	fixture := newConnectionPointFixture(t)
	// A synchronous-only vendor server has no IConnectionPointContainer at all.
	fixture.queryResult = eNoInterface

	supported, err := probeDataCallbackSupport(fixture.itemMgt)
	if err != nil {
		t.Fatalf("a missing connection point container was reported as an error: %v", err)
	}
	if supported {
		t.Fatal("a server without IConnectionPointContainer was reported as supported")
	}
	if fixture.findCalled.Load() != 0 {
		t.Fatal("the probe looked for a connection point on a server without a container")
	}
}

func TestSubscribeCapabilityProbeReportsMissingDataCallbackSink(t *testing.T) {
	fixture := newConnectionPointFixture(t)
	// The container exists but offers no IOPCDataCallback sink.
	fixture.findResult = callResult(ConnectENoConnection)

	supported, err := probeDataCallbackSupport(fixture.itemMgt)
	if err != nil {
		t.Fatalf("a missing sink interface was reported as an error: %v", err)
	}
	if supported {
		t.Fatal("a server without an IOPCDataCallback sink was reported as supported")
	}
	if fixture.findCalled.Load() != 1 {
		t.Fatalf("FindConnectionPoint calls = %d, want 1", fixture.findCalled.Load())
	}
}

func TestSubscribeCapabilityProbeSurfacesUnexpectedFailures(t *testing.T) {
	fixture := newConnectionPointFixture(t)
	// An HRESULT that is neither success nor a capability answer must not be
	// silently reduced to "unsupported".
	fixture.findResult = callResult(rpcSServerUnavailable)

	supported, err := probeDataCallbackSupport(fixture.itemMgt)
	if supported {
		t.Fatal("a failing probe reported the capability as supported")
	}
	sourceError, ok := AsSourceError(err)
	if !ok || sourceError.HRESULT != rpcSServerUnavailable {
		t.Fatalf("probe error = %v, want a SourceError carrying the exact HRESULT", err)
	}
	// A connection-loss HRESULT from the probe must still be recognised so the
	// runtime reconnects instead of recording a permanent capability answer.
	if !isConnectionLoss(err) {
		t.Fatal("a disconnect during the probe was not recognised as connection loss")
	}
}

func TestSubscribeCapabilityProbeRejectsANilConnectionPoint(t *testing.T) {
	fixture := newConnectionPointFixture(t)
	// A server that reports success but returns no interface is malformed.
	fixture.returnNilPoint = true

	supported, err := probeDataCallbackSupport(fixture.itemMgt)
	if supported || err == nil {
		t.Fatalf("a nil connection point was accepted: supported=%t err=%v", supported, err)
	}
	if _, isSource := AsSourceError(err); isSource {
		t.Fatalf("a malformed success was reported as a source HRESULT: %v", err)
	}
}
