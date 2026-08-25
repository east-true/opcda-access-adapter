//go:build windows

package opcda

import (
	"encoding/binary"
	"runtime"
	"sync/atomic"
	"testing"
	"unsafe"
)

// The vtable is built at package initialisation, so a populated table also
// proves syscall.NewCallback accepted every IOPCDataCallback signature.
func TestDataCallbackVTableIsPopulated(t *testing.T) {
	entries := map[string]uintptr{
		"QueryInterface":   dataCallbackVTable.QueryInterface,
		"AddRef":           dataCallbackVTable.AddRef,
		"Release":          dataCallbackVTable.Release,
		"OnDataChange":     dataCallbackVTable.OnDataChange,
		"OnReadComplete":   dataCallbackVTable.OnReadComplete,
		"OnWriteComplete":  dataCallbackVTable.OnWriteComplete,
		"OnCancelComplete": dataCallbackVTable.OnCancelComplete,
	}
	for name, entry := range entries {
		if entry == 0 {
			t.Fatalf("IOPCDataCallback::%s has no callback address", name)
		}
	}
}

func TestDataCallbackABILayout(t *testing.T) {
	pointerSize := unsafe.Sizeof(uintptr(0))
	if got, want := unsafe.Offsetof(iopcDataCallbackVTable{}.OnDataChange), 3*pointerSize; got != want {
		t.Fatalf("IOPCDataCallback::OnDataChange offset = %d, want %d", got, want)
	}
	if got, want := unsafe.Sizeof(iopcDataCallbackVTable{}), 7*pointerSize; got != want {
		t.Fatalf("IOPCDataCallback vtable size = %d, want %d", got, want)
	}
	if got, want := unsafe.Offsetof(iconnectionPointVTable{}.Advise), 5*pointerSize; got != want {
		t.Fatalf("IConnectionPoint::Advise offset = %d, want %d", got, want)
	}
	if got, want := unsafe.Offsetof(iconnectionPointVTable{}.Unadvise), 6*pointerSize; got != want {
		t.Fatalf("IConnectionPoint::Unadvise offset = %d, want %d", got, want)
	}
	if got, want := unsafe.Offsetof(iconnectionPointContainerVTable{}.FindConnectionPoint), 4*pointerSize; got != want {
		t.Fatalf("IConnectionPointContainer::FindConnectionPoint offset = %d, want %d", got, want)
	}
	// The server dereferences the object's first field as the vtable pointer.
	if offset := unsafe.Offsetof(dataCallbackObject{}.vtable); offset != 0 {
		t.Fatalf("callback object vtable offset = %d, want 0", offset)
	}
}

func TestDataCallbackQueryInterface(t *testing.T) {
	object := &dataCallbackObject{vtable: dataCallbackVTable, refs: 1}
	var pinner runtime.Pinner
	pinner.Pin(object)
	defer pinner.Unpin()
	this := uintptr(unsafe.Pointer(object))

	for _, iid := range []guid{iidIUnknown, iidIOPCDataCallback} {
		requested := iid
		var out uintptr
		result := dataCallbackQueryInterface(this, uintptr(unsafe.Pointer(&requested)), uintptr(unsafe.Pointer(&out)))
		if result != sOK {
			t.Fatalf("QueryInterface(%s) = 0x%X, want S_OK", iid, result)
		}
		if out != this {
			t.Fatalf("QueryInterface(%s) returned 0x%X, want the callback object", iid, out)
		}
	}
	if refs := atomic.LoadInt32(&object.refs); refs != 3 {
		t.Fatalf("refs = %d, want 3 after two successful QueryInterface calls", refs)
	}

	unsupported := iidIOPCServer
	var out uintptr = 0xDEAD
	result := dataCallbackQueryInterface(this, uintptr(unsafe.Pointer(&unsupported)), uintptr(unsafe.Pointer(&out)))
	if result != eNoInterface {
		t.Fatalf("QueryInterface(IOPCServer) = 0x%X, want E_NOINTERFACE", result)
	}
	if out != 0 {
		t.Fatalf("failed QueryInterface left 0x%X in the out parameter, want 0", out)
	}
	runtime.KeepAlive(object)
}

func TestDataCallbackAddRefRelease(t *testing.T) {
	object := &dataCallbackObject{vtable: dataCallbackVTable, refs: 1}
	var pinner runtime.Pinner
	pinner.Pin(object)
	defer pinner.Unpin()
	this := uintptr(unsafe.Pointer(object))

	if got := dataCallbackAddRef(this); got != 2 {
		t.Fatalf("AddRef = %d, want 2", got)
	}
	if got := dataCallbackRelease(this); got != 1 {
		t.Fatalf("Release = %d, want 1", got)
	}
	if got := dataCallbackRelease(this); got != 0 {
		t.Fatalf("Release = %d, want 0", got)
	}
	// An over-release from a buggy server must not drive the count negative.
	if got := dataCallbackRelease(this); got != 0 {
		t.Fatalf("over-Release = %d, want 0", got)
	}
	if refs := atomic.LoadInt32(&object.refs); refs != 0 {
		t.Fatalf("refs = %d, want 0", refs)
	}
	// A null this pointer must be tolerated rather than panic into the caller.
	if got := dataCallbackAddRef(0); got != 0 {
		t.Fatalf("AddRef(nil) = %d, want 0", got)
	}
	runtime.KeepAlive(object)
}

type dataChangeFixture struct {
	subscription *daSubscription
	this         uintptr
	handles      []uint32
	variants     []variant
	qualities    []uint16
	timestamps   []filetime
	errors       []int32
	pinner       runtime.Pinner
}

func (fixture *dataChangeFixture) call(t *testing.T, groupHandle uintptr, count uintptr) uintptr {
	t.Helper()
	return dataCallbackOnDataChange(
		fixture.this,
		0,
		groupHandle,
		0,
		0,
		count,
		uintptr(unsafe.Pointer(&fixture.handles[0])),
		uintptr(unsafe.Pointer(&fixture.variants[0])),
		uintptr(unsafe.Pointer(&fixture.qualities[0])),
		uintptr(unsafe.Pointer(&fixture.timestamps[0])),
		uintptr(unsafe.Pointer(&fixture.errors[0])),
	)
}

// newDataChangeFixture builds a subscription and a server-shaped notification
// without contacting a DA server.
func newDataChangeFixture(t *testing.T) *dataChangeFixture {
	t.Helper()
	subscription := &daSubscription{
		id:                "sub-1-1",
		generation:        1,
		groupClientHandle: 2,
		itemCount:         2,
		maxBSTRCodeUnits:  DefaultLimits().MaxBSTRCodeUnits,
		registrations: map[uint32]itemRegistration{
			1: {ItemID: "Random.Int4", ServerHandle: 11, CanonicalType: VTI4, Generation: 1,
				AccessRights: DAAccessRights{Raw: 1, Read: true}},
			2: {ItemID: "Random.Real4", ServerHandle: 12, CanonicalType: VTR4, Generation: 1,
				AccessRights: DAAccessRights{Raw: 3, Read: true, Write: true}},
		},
		pending: newPendingUpdates(2),
	}
	subscription.callback = &dataCallbackObject{vtable: dataCallbackVTable, refs: 1}

	fixture := &dataChangeFixture{
		subscription: subscription,
		handles:      []uint32{1, 2},
		variants:     make([]variant, 2),
		qualities:    []uint16{0xC0, 0x1C},
		timestamps:   []filetime{{LowDateTime: 0xD53E8000, HighDateTime: 0x01D5B7A5}, {}},
		errors:       []int32{0, 0},
	}
	fixture.variants[0].VT = uint16(VTI4)
	binary.LittleEndian.PutUint32(fixture.variants[0].Data[:], 4242)
	fixture.variants[1].VT = uint16(VTI4)
	binary.LittleEndian.PutUint32(fixture.variants[1].Data[:], 7)

	fixture.pinner.Pin(subscription.callback)
	fixture.pinner.Pin(&fixture.handles[0])
	fixture.pinner.Pin(&fixture.variants[0])
	fixture.pinner.Pin(&fixture.qualities[0])
	fixture.pinner.Pin(&fixture.timestamps[0])
	fixture.pinner.Pin(&fixture.errors[0])
	fixture.this = uintptr(unsafe.Pointer(subscription.callback))

	subscription.callbackID = registerCallback(subscription)
	subscription.callback.id = subscription.callbackID
	t.Cleanup(func() {
		unregisterCallback(subscription.callbackID)
		fixture.pinner.Unpin()
	})
	return fixture
}

func TestOnDataChangeCoalescesIntoPendingSet(t *testing.T) {
	fixture := newDataChangeFixture(t)
	if result := fixture.call(t, 2, 2); result != sOK {
		t.Fatalf("OnDataChange = 0x%X, want S_OK", result)
	}
	// A second notification for the same handles must replace, not accumulate.
	binary.LittleEndian.PutUint32(fixture.variants[0].Data[:], 5555)
	if result := fixture.call(t, 2, 1); result != sOK {
		t.Fatalf("second OnDataChange = 0x%X, want S_OK", result)
	}

	values := fixture.subscription.Drain()
	if len(values) != 2 {
		t.Fatalf("drained %d values, want 2", len(values))
	}
	if values[0].ItemID != "Random.Int4" || values[1].ItemID != "Random.Real4" {
		t.Fatalf("item order = %q,%q", values[0].ItemID, values[1].ItemID)
	}
	if got := values[0].Value.Value; got != int32(5555) {
		t.Fatalf("coalesced value = %v, want 5555", got)
	}
	// Raw source metadata is preserved exactly.
	if got := values[0].Value.QualityRaw; got != 0xC0 {
		t.Fatalf("quality = 0x%X, want 0xC0", got)
	}
	if !values[0].Value.TimestampPresent {
		t.Fatal("a non-zero FILETIME was reported as absent")
	}
	if values[1].Value.TimestampPresent {
		t.Fatal("a zero FILETIME was reported as present")
	}
	if got := *values[1].CanonicalType; got != VTR4 {
		t.Fatalf("canonical type = %s, want VT_R4", got)
	}
	if values[1].AccessRights == nil || !values[1].AccessRights.Write {
		t.Fatal("access rights were not preserved")
	}
	if fixture.subscription.RejectedNotifications() != 0 {
		t.Fatal("a well-formed notification was rejected")
	}
}

func TestOnDataChangePreservesFailedItemHRESULT(t *testing.T) {
	fixture := newDataChangeFixture(t)
	const eFail = int32(-2147467259) // E_FAIL
	fixture.errors[0] = eFail
	if result := fixture.call(t, 2, 2); result != sOK {
		t.Fatalf("OnDataChange = 0x%X, want S_OK", result)
	}
	values := fixture.subscription.Drain()
	if len(values) != 2 {
		t.Fatalf("drained %d values, want 2", len(values))
	}
	// A failed item keeps its exact HRESULT and carries no value.
	if values[0].HRESULT != HRESULT(eFail) || !values[0].HRESULTPresent {
		t.Fatalf("HRESULT = %s, want %s", values[0].HRESULT.Hex(), HRESULT(eFail).Hex())
	}
	if values[0].Value != nil {
		t.Fatal("a failed item carried a value")
	}
	if values[1].Value == nil {
		t.Fatal("a succeeding item in the same batch lost its value")
	}
}

func TestOnDataChangeRejectsInconsistentCount(t *testing.T) {
	fixture := newDataChangeFixture(t)
	if result := fixture.call(t, 2, 3); result != eInvalidArg {
		t.Fatalf("OnDataChange with an oversized count = 0x%X, want E_INVALIDARG", result)
	}
	if values := fixture.subscription.Drain(); values != nil {
		t.Fatalf("rejected notification produced %d values", len(values))
	}
	if fixture.subscription.RejectedNotifications() != 1 {
		t.Fatalf("rejected count = %d, want 1", fixture.subscription.RejectedNotifications())
	}
}

func TestOnDataChangeIgnoresForeignGroupAndUnknownHandles(t *testing.T) {
	fixture := newDataChangeFixture(t)
	if result := fixture.call(t, 99, 2); result != sOK {
		t.Fatalf("OnDataChange for another group = 0x%X, want S_OK", result)
	}
	if values := fixture.subscription.Drain(); values != nil {
		t.Fatalf("a foreign group handle produced %d values", len(values))
	}

	fixture.handles[0] = 404
	if result := fixture.call(t, 2, 2); result != sOK {
		t.Fatalf("OnDataChange = 0x%X, want S_OK", result)
	}
	values := fixture.subscription.Drain()
	if len(values) != 1 || values[0].ItemID != "Random.Real4" {
		t.Fatalf("unknown client handle was not skipped: %+v", values)
	}
}

func TestOnDataChangeToleratesUnknownCallbackAndNullArrays(t *testing.T) {
	fixture := newDataChangeFixture(t)
	// An OnDataChange delivered after teardown must not panic or crash.
	unregisterCallback(fixture.subscription.callbackID)
	if result := fixture.call(t, 2, 2); result != sOK {
		t.Fatalf("OnDataChange after unregister = 0x%X, want S_OK", result)
	}
	fixture.subscription.callbackID = registerCallback(fixture.subscription)
	fixture.subscription.callback.id = fixture.subscription.callbackID

	result := dataCallbackOnDataChange(fixture.this, 0, 2, 0, 0, 1, 0, 0, 0, 0, 0)
	if result != ePointer {
		t.Fatalf("OnDataChange with null arrays = 0x%X, want E_POINTER", result)
	}
	if result := dataCallbackOnDataChange(0, 0, 2, 0, 0, 1, 1, 1, 1, 1, 1); result != ePointer {
		t.Fatalf("OnDataChange with a null this = 0x%X, want E_POINTER", result)
	}
}

func TestSubscriptionInvalidationDropsPendingValues(t *testing.T) {
	fixture := newDataChangeFixture(t)
	if result := fixture.call(t, 2, 2); result != sOK {
		t.Fatalf("OnDataChange = 0x%X, want S_OK", result)
	}
	cause := NewAdapterError(CodeSubscriptionInvalidated, "source disconnected")
	fixture.subscription.teardown(nil, cause)

	select {
	case <-fixture.subscription.Done():
	default:
		t.Fatal("teardown did not close Done")
	}
	if values := fixture.subscription.Drain(); values != nil {
		t.Fatalf("invalidated subscription delivered %d values", len(values))
	}
	if fixture.subscription.Err() != cause {
		t.Fatalf("Err = %v, want the invalidation cause", fixture.subscription.Err())
	}
	if _, found := lookupCallback(fixture.subscription.callbackID); found {
		t.Fatal("teardown left the callback registered")
	}
}
