//go:build windows

package opcda

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// IID_IOPCDataCallback and IID_IConnectionPointContainer follow the OPC
// Foundation opcda.idl and the Microsoft ocidl.idl definitions.
var (
	iidIOPCDataCallback = guid{
		Data1: 0x39C13A70, Data2: 0x011E, Data3: 0x11D0,
		Data4: [8]byte{0x96, 0x75, 0x00, 0x20, 0xAF, 0xD8, 0xAD, 0xB3},
	}
	iidIConnectionPointContainer = guid{
		Data1: 0xB196B284, Data2: 0xBAB4, Data3: 0x101A,
		Data4: [8]byte{0xB6, 0x9C, 0x00, 0xAA, 0x00, 0x34, 0x1D, 0x07},
	}
	iidIUnknown = guid{
		Data1: 0x00000000, Data2: 0x0000, Data3: 0x0000,
		Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46},
	}
)

const (
	sOK            = uintptr(0)
	eNoInterface   = uintptr(0x80004002)
	ePointer       = uintptr(0x80004003)
	eInvalidArg    = uintptr(0x80070057)
	opcActiveState = int32(1)
)

type iconnectionPointContainer struct {
	VTable *iconnectionPointContainerVTable
}

type iconnectionPointContainerVTable struct {
	iUnknownVTable
	EnumConnectionPoints uintptr
	FindConnectionPoint  uintptr
}

func (container *iconnectionPointContainer) release() uint32 {
	if container == nil || container.VTable == nil {
		return 0
	}
	return releaseCOM(unsafe.Pointer(container), container.VTable.Release)
}

type iconnectionPoint struct {
	VTable *iconnectionPointVTable
}

type iconnectionPointVTable struct {
	iUnknownVTable
	GetConnectionInterface      uintptr
	GetConnectionPointContainer uintptr
	Advise                      uintptr
	Unadvise                    uintptr
	EnumConnections             uintptr
}

func (point *iconnectionPoint) release() uint32 {
	if point == nil || point.VTable == nil {
		return 0
	}
	return releaseCOM(unsafe.Pointer(point), point.VTable.Release)
}

type iopcDataCallbackVTable struct {
	iUnknownVTable
	OnDataChange     uintptr
	OnReadComplete   uintptr
	OnWriteComplete  uintptr
	OnCancelComplete uintptr
}

// dataCallbackObject is the COM object handed to the DA server. It deliberately
// carries only an integer identity so the callback never dereferences a Go
// pointer taken from server-owned memory; the owning subscription is resolved
// through the process-wide registry below.
type dataCallbackObject struct {
	vtable *iopcDataCallbackVTable
	refs   int32
	id     uint64
}

var dataCallbackVTable = &iopcDataCallbackVTable{
	iUnknownVTable: iUnknownVTable{
		QueryInterface: syscall.NewCallback(dataCallbackQueryInterface),
		AddRef:         syscall.NewCallback(dataCallbackAddRef),
		Release:        syscall.NewCallback(dataCallbackRelease),
	},
	OnDataChange:     syscall.NewCallback(dataCallbackOnDataChange),
	OnReadComplete:   syscall.NewCallback(dataCallbackOnReadComplete),
	OnWriteComplete:  syscall.NewCallback(dataCallbackOnWriteComplete),
	OnCancelComplete: syscall.NewCallback(dataCallbackOnCancelComplete),
}

// A DA server registered as an in-process handler can invoke the callback from
// a thread the adapter does not own, so the registry and every structure it
// exposes to the callback are synchronised.
var (
	callbackRegistryMu sync.Mutex
	callbackRegistry   = make(map[uint64]*daSubscription)
	nextCallbackID     atomic.Uint64
)

func registerCallback(subscription *daSubscription) uint64 {
	id := nextCallbackID.Add(1)
	callbackRegistryMu.Lock()
	callbackRegistry[id] = subscription
	callbackRegistryMu.Unlock()
	return id
}

func unregisterCallback(id uint64) {
	callbackRegistryMu.Lock()
	delete(callbackRegistry, id)
	callbackRegistryMu.Unlock()
}

func lookupCallback(id uint64) (*daSubscription, bool) {
	callbackRegistryMu.Lock()
	defer callbackRegistryMu.Unlock()
	subscription, ok := callbackRegistry[id]
	return subscription, ok
}

// comPointer converts a COM-supplied address into a pointer. The address always
// refers to apartment- or server-owned memory that the Go collector does not
// manage, which is the case the unsafeptr diagnostic cannot distinguish.
func comPointer(address uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&address))
}

func callbackObject(this uintptr) *dataCallbackObject {
	if this == 0 {
		return nil
	}
	return (*dataCallbackObject)(comPointer(this))
}

func dataCallbackQueryInterface(this, riid, ppv uintptr) uintptr {
	object := callbackObject(this)
	if object == nil || ppv == 0 {
		return ePointer
	}
	*(*uintptr)(comPointer(ppv)) = 0
	if riid == 0 {
		return ePointer
	}
	requested := (*guid)(comPointer(riid))
	if *requested != iidIUnknown && *requested != iidIOPCDataCallback {
		return eNoInterface
	}
	atomic.AddInt32(&object.refs, 1)
	*(*uintptr)(comPointer(ppv)) = this
	return sOK
}

func dataCallbackAddRef(this uintptr) uintptr {
	object := callbackObject(this)
	if object == nil {
		return 0
	}
	return uintptr(atomic.AddInt32(&object.refs, 1))
}

func dataCallbackRelease(this uintptr) uintptr {
	object := callbackObject(this)
	if object == nil {
		return 0
	}
	// The adapter owns the allocation and frees it during subscription
	// teardown, so reaching zero never frees memory the server may still touch.
	remaining := atomic.AddInt32(&object.refs, -1)
	if remaining < 0 {
		atomic.StoreInt32(&object.refs, 0)
		return 0
	}
	return uintptr(remaining)
}

// The asynchronous IO completions are part of IOPCDataCallback but belong to
// IOPCAsyncIO2, which this adapter does not use. Their exact arity is still
// declared so a server that calls them cleans up the stdcall stack correctly.
func dataCallbackOnReadComplete(
	_ uintptr, _ uintptr, _ uintptr, _ uintptr, _ uintptr,
	_ uintptr, _ uintptr, _ uintptr, _ uintptr, _ uintptr, _ uintptr,
) uintptr {
	return sOK
}

func dataCallbackOnWriteComplete(
	_ uintptr, _ uintptr, _ uintptr, _ uintptr, _ uintptr, _ uintptr, _ uintptr,
) uintptr {
	return sOK
}

func dataCallbackOnCancelComplete(_ uintptr, _ uintptr, _ uintptr) uintptr { return sOK }

func dataCallbackOnDataChange(
	this uintptr,
	transactionID uintptr,
	groupHandle uintptr,
	masterQuality uintptr,
	masterError uintptr,
	count uintptr,
	clientHandles uintptr,
	values uintptr,
	qualities uintptr,
	timestamps uintptr,
	itemErrors uintptr,
) uintptr {
	// A Go panic must never unwind into the calling COM apartment.
	defer func() {
		_ = recover()
	}()
	_ = transactionID
	_ = masterQuality
	_ = masterError

	object := callbackObject(this)
	if object == nil {
		return ePointer
	}
	subscription, ok := lookupCallback(object.id)
	if !ok {
		return sOK
	}
	if groupHandle != uintptr(subscription.groupClientHandle) {
		return sOK
	}
	if count == 0 {
		return sOK
	}
	// A count larger than the subscribed item set means the server-supplied
	// arrays cannot be trusted, so the whole notification is rejected.
	if count > uintptr(subscription.itemCount) {
		subscription.rejected.Add(1)
		return eInvalidArg
	}
	if clientHandles == 0 || values == 0 || qualities == 0 || timestamps == 0 || itemErrors == 0 {
		subscription.rejected.Add(1)
		return ePointer
	}

	length := int(count)
	handles := unsafe.Slice((*uint32)(comPointer(clientHandles)), length)
	variants := unsafe.Slice((*variant)(comPointer(values)), length)
	qualityCodes := unsafe.Slice((*uint16)(comPointer(qualities)), length)
	stamps := unsafe.Slice((*filetime)(comPointer(timestamps)), length)
	errorCodes := unsafe.Slice((*int32)(comPointer(itemErrors)), length)

	acceptedHandles := make([]uint32, 0, length)
	accepted := make([]SubscriptionValue, 0, length)
	for index := 0; index < length; index++ {
		handle := handles[index]
		registration, known := subscription.registrationFor(handle)
		if !known {
			continue
		}
		itemHR := HRESULT(errorCodes[index])
		valueType := DAVarType(variants[index].VT)
		canonicalType := registration.CanonicalType
		rights := registration.AccessRights
		entry := SubscriptionValue{
			ItemID:         registration.ItemID,
			VarType:        &valueType,
			CanonicalType:  &canonicalType,
			AccessRights:   &rights,
			HRESULT:        itemHR,
			HRESULTPresent: true,
		}
		if itemHR.Succeeded() {
			decoded, decodeErr := decodeVariant(&variants[index], subscription.maxBSTRCodeUnits)
			if decodeErr != nil {
				if adapterErr, ok := AsAdapterError(decodeErr); ok {
					entry.ErrorCode = string(adapterErr.Code)
				} else {
					entry.ErrorCode = string(CodeInvalidValue)
				}
			} else {
				timestamp, present := stamps[index].toTime()
				entry.Value = &DAValue{
					ItemID:           registration.ItemID,
					VarType:          valueType,
					Value:            decoded,
					QualityRaw:       qualityCodes[index],
					Timestamp:        timestamp,
					TimestampPresent: present,
					HRESULT:          itemHR,
					AccessRights:     &rights,
				}
			}
		}
		acceptedHandles = append(acceptedHandles, handle)
		accepted = append(accepted, entry)
	}
	// The VARIANT array stays owned by the server; the callback must not clear
	// or free it.
	subscription.pending.merge(acceptedHandles, accepted)
	return sOK
}

// daSubscription owns exactly one DA group, its advise cookie, and the COM
// callback object. All COM pointers belong to the DA thread; only pending and
// rejected are touched from other threads.
type daSubscription struct {
	id                SubscriptionID
	info              SubscriptionInfo
	generation        uint64
	groupClientHandle uint32

	serverGroupHandle uint32
	hasServerGroup    bool
	itemMgt           *iopcItemMgt
	connectionPoint   *iconnectionPoint
	cookie            uint32
	advised           bool

	callback   *dataCallbackObject
	callbackID uint64
	pinner     runtime.Pinner
	pinned     bool

	registrations    map[uint32]itemRegistration
	itemCount        int
	maxBSTRCodeUnits int

	pending  *pendingUpdates
	rejected atomic.Uint64
}

func (subscription *daSubscription) registrationFor(handle uint32) (itemRegistration, bool) {
	registration, ok := subscription.registrations[handle]
	return registration, ok
}

func (subscription *daSubscription) Info() SubscriptionInfo   { return subscription.info }
func (subscription *daSubscription) Updates() <-chan struct{} { return subscription.pending.notify }
func (subscription *daSubscription) Drain() []SubscriptionValue {
	return subscription.pending.drain()
}
func (subscription *daSubscription) Done() <-chan struct{} { return subscription.pending.done }
func (subscription *daSubscription) Err() error            { return subscription.pending.failure() }

// RejectedNotifications counts OnDataChange calls refused because the server
// supplied an inconsistent count or a null array. It is diagnostic only and
// never represents coalesced values.
func (subscription *daSubscription) RejectedNotifications() uint64 {
	return subscription.rejected.Load()
}

func addSubscriptionGroup(server *iopcServer, name string, updateRate time.Duration, deadband float32, groupClientHandle uint32) (uint32, uint32, *iopcItemMgt, error) {
	groupName, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("subscription group name contains an embedded NUL")
	}
	var (
		serverHandle uint32
		revisedRate  uint32
		items        *iopcItemMgt
		deadbandArg  uintptr
	)
	percentDeadband := deadband
	if deadband > 0 {
		deadbandArg = uintptr(unsafe.Pointer(&percentDeadband))
	}
	requestedRate := updateRate / time.Millisecond
	result, _, _ := syscall.SyscallN(
		server.VTable.AddGroup,
		uintptr(unsafe.Pointer(server)),
		uintptr(unsafe.Pointer(groupName)),
		1,
		uintptr(uint32(requestedRate)),
		uintptr(groupClientHandle),
		0,
		deadbandArg,
		0,
		uintptr(unsafe.Pointer(&serverHandle)),
		uintptr(unsafe.Pointer(&revisedRate)),
		uintptr(unsafe.Pointer(&iidIOPCItemMgt)),
		uintptr(unsafe.Pointer(&items)),
	)
	runtime.KeepAlive(server)
	runtime.KeepAlive(groupName)
	runtime.KeepAlive(&percentDeadband)
	if hr := hresultFromCall(result); hr.Failed() {
		return 0, 0, nil, &SourceError{Operation: "IOPCServer::AddGroup(subscription)", HRESULT: hr}
	}
	if items == nil {
		return 0, 0, nil, fmt.Errorf("IOPCServer::AddGroup(subscription) returned a nil IOPCItemMgt")
	}
	return serverHandle, revisedRate, items, nil
}

// addActiveItems registers the subscription's items as active. Per-item source
// failures are preserved and never fail the whole subscription.
func addActiveItems(items *iopcItemMgt, itemIDs []DAItemID, generation uint64) ([]uint32, []registrationAttempt, error) {
	definitions := make([]opcItemDef, len(itemIDs))
	wideItemIDs := make([][]uint16, len(itemIDs))
	clientHandles := make([]uint32, len(itemIDs))
	for index, itemID := range itemIDs {
		wide, err := syscall.UTF16FromString(string(itemID))
		if err != nil {
			return nil, nil, fmt.Errorf("item ID contains an embedded NUL")
		}
		wideItemIDs[index] = wide
		clientHandles[index] = uint32(index) + 1
		definitions[index] = opcItemDef{
			ItemID:            &wideItemIDs[index][0],
			Active:            opcActiveState,
			ClientHandle:      clientHandles[index],
			RequestedDataType: uint16(VTEmpty),
		}
	}

	var resultPointer, errorPointer unsafe.Pointer
	result, _, _ := syscall.SyscallN(
		items.VTable.AddItems,
		uintptr(unsafe.Pointer(items)),
		uintptr(len(definitions)),
		uintptr(unsafe.Pointer(&definitions[0])),
		uintptr(unsafe.Pointer(&resultPointer)),
		uintptr(unsafe.Pointer(&errorPointer)),
	)
	runtime.KeepAlive(items)
	runtime.KeepAlive(definitions)
	runtime.KeepAlive(wideItemIDs)
	defer coTaskMemFree(resultPointer)
	defer coTaskMemFree(errorPointer)
	if hr := hresultFromCall(result); hr.Failed() {
		return nil, nil, &SourceError{Operation: "IOPCItemMgt::AddItems(subscription)", HRESULT: hr}
	}
	if resultPointer == nil || errorPointer == nil {
		return nil, nil, fmt.Errorf("IOPCItemMgt::AddItems(subscription) returned nil result arrays")
	}

	addResults := unsafe.Slice((*opcItemResult)(resultPointer), len(itemIDs))
	itemErrors := unsafe.Slice((*int32)(errorPointer), len(itemIDs))
	attempts := make([]registrationAttempt, len(itemIDs))
	for index, itemID := range itemIDs {
		if addResults[index].Blob != nil {
			coTaskMemFree(addResults[index].Blob)
		}
		itemHR := HRESULT(itemErrors[index])
		attempts[index] = registrationAttempt{hresult: itemHR, hresultSet: true}
		if itemHR.Failed() {
			continue
		}
		rights := DAAccessRights{
			Raw:   addResults[index].AccessRights,
			Read:  addResults[index].AccessRights&opcAccessRightRead != 0,
			Write: addResults[index].AccessRights&opcAccessRightWrite != 0,
		}
		attempts[index].registration = itemRegistration{
			ItemID:        itemID,
			ServerHandle:  addResults[index].ServerHandle,
			CanonicalType: DAVarType(addResults[index].CanonicalDataType),
			AccessRights:  rights,
			Generation:    generation,
		}
		attempts[index].ok = true
	}
	return clientHandles, attempts, nil
}

// findDataCallbackConnectionPoint locates the group's IOPCDataCallback
// connection point. A source that does not implement connection points, or
// implements them without this sink interface, reports (nil, false, nil) rather
// than an error: that is a capability answer, not a failure.
func findDataCallbackConnectionPoint(items *iopcItemMgt) (*iconnectionPoint, bool, error) {
	var container *iconnectionPointContainer
	result, _, _ := syscall.SyscallN(
		items.VTable.QueryInterface,
		uintptr(unsafe.Pointer(items)),
		uintptr(unsafe.Pointer(&iidIConnectionPointContainer)),
		uintptr(unsafe.Pointer(&container)),
	)
	runtime.KeepAlive(items)
	hr := hresultFromCall(result)
	if hr == ENoInterface {
		return nil, false, nil
	}
	if hr.Failed() {
		return nil, false, &SourceError{Operation: "IOPCItemMgt::QueryInterface(IConnectionPointContainer)", HRESULT: hr}
	}
	if container == nil {
		return nil, false, fmt.Errorf("QueryInterface(IConnectionPointContainer) returned a nil interface")
	}
	defer container.release()

	var point *iconnectionPoint
	result, _, _ = syscall.SyscallN(
		container.VTable.FindConnectionPoint,
		uintptr(unsafe.Pointer(container)),
		uintptr(unsafe.Pointer(&iidIOPCDataCallback)),
		uintptr(unsafe.Pointer(&point)),
	)
	runtime.KeepAlive(container)
	hr = hresultFromCall(result)
	if hr == ConnectENoConnection || hr == ENoInterface {
		return nil, false, nil
	}
	if hr.Failed() {
		return nil, false, &SourceError{Operation: "IConnectionPointContainer::FindConnectionPoint(IOPCDataCallback)", HRESULT: hr}
	}
	if point == nil {
		return nil, false, fmt.Errorf("FindConnectionPoint(IOPCDataCallback) returned a nil connection point")
	}
	return point, true, nil
}

// probeDataCallbackSupport answers whether the source exposes the callback
// connection point, without advising. It is the Subscribe counterpart of the
// Browse interface probe and never activates a subscription.
func probeDataCallbackSupport(items *iopcItemMgt) (bool, error) {
	point, supported, err := findDataCallbackConnectionPoint(items)
	if err != nil {
		return false, err
	}
	if point != nil {
		point.release()
	}
	return supported, nil
}

func adviseDataCallback(items *iopcItemMgt, callback *dataCallbackObject) (*iconnectionPoint, uint32, error) {
	point, supported, err := findDataCallbackConnectionPoint(items)
	if err != nil {
		return nil, 0, err
	}
	if !supported {
		return nil, 0, NewAdapterError(CodeSubscribeUnsupported, "OPC DA server does not expose an IOPCDataCallback connection point")
	}

	var cookie uint32
	result, _, _ := syscall.SyscallN(
		point.VTable.Advise,
		uintptr(unsafe.Pointer(point)),
		uintptr(unsafe.Pointer(callback)),
		uintptr(unsafe.Pointer(&cookie)),
	)
	runtime.KeepAlive(point)
	runtime.KeepAlive(callback)
	if hr := hresultFromCall(result); hr.Failed() {
		point.release()
		return nil, 0, &SourceError{Operation: "IConnectionPoint::Advise(IOPCDataCallback)", HRESULT: hr}
	}
	return point, cookie, nil
}

func unadviseDataCallback(point *iconnectionPoint, cookie uint32) error {
	if point == nil {
		return nil
	}
	result, _, _ := syscall.SyscallN(
		point.VTable.Unadvise,
		uintptr(unsafe.Pointer(point)),
		uintptr(cookie),
	)
	runtime.KeepAlive(point)
	if hr := hresultFromCall(result); hr.Failed() {
		return &SourceError{Operation: "IConnectionPoint::Unadvise", HRESULT: hr}
	}
	return nil
}

// createSubscription runs on the DA thread. It creates one DA group, registers
// the items as active, and advises the callback. Any failure after AddGroup
// releases everything it created before returning.
func (session *daThreadSession) createSubscription(id SubscriptionID, request SubscribeRequest, maxBSTRCodeUnits int) (*daSubscription, error) {
	session.nextGroupClientHandle++
	groupClientHandle := session.nextGroupClientHandle

	serverGroupHandle, revisedRate, itemMgt, err := addSubscriptionGroup(
		session.server, string(id), request.RequestedUpdateRate, request.Deadband, groupClientHandle,
	)
	if err != nil {
		return nil, err
	}

	subscription := &daSubscription{
		id:                id,
		generation:        session.generation,
		groupClientHandle: groupClientHandle,
		serverGroupHandle: serverGroupHandle,
		hasServerGroup:    true,
		itemMgt:           itemMgt,
		registrations:     make(map[uint32]itemRegistration, len(request.Items)),
		itemCount:         len(request.Items),
		maxBSTRCodeUnits:  maxBSTRCodeUnits,
	}

	clientHandles, attempts, err := addActiveItems(itemMgt, request.Items, session.generation)
	if err != nil {
		subscription.releaseCOM(session.server)
		return nil, err
	}

	statuses := make([]SubscriptionItemStatus, len(request.Items))
	activeItems := 0
	for index, itemID := range request.Items {
		attempt := attempts[index]
		statuses[index] = SubscriptionItemStatus{
			ItemID:         itemID,
			HRESULT:        attempt.hresult,
			HRESULTPresent: attempt.hresultSet,
			ErrorCode:      attempt.errorCode,
		}
		if !attempt.ok {
			continue
		}
		canonicalType := attempt.registration.CanonicalType
		rights := attempt.registration.AccessRights
		statuses[index].Active = true
		statuses[index].CanonicalType = &canonicalType
		statuses[index].AccessRights = &rights
		subscription.registrations[clientHandles[index]] = attempt.registration
		activeItems++
	}

	subscription.pending = newPendingUpdates(activeItems)
	subscription.info = SubscriptionInfo{
		ID:                   id,
		ConnectionGeneration: session.generation,
		RequestedUpdateRate:  request.RequestedUpdateRate,
		RevisedUpdateRate:    time.Duration(revisedRate) * time.Millisecond,
		Deadband:             request.Deadband,
		Items:                statuses,
		ActiveItemCount:      activeItems,
	}

	subscription.callback = &dataCallbackObject{vtable: dataCallbackVTable, refs: 1}
	subscription.pinner.Pin(subscription.callback)
	subscription.pinner.Pin(dataCallbackVTable)
	subscription.pinned = true
	subscription.callbackID = registerCallback(subscription)
	subscription.callback.id = subscription.callbackID

	point, cookie, err := adviseDataCallback(itemMgt, subscription.callback)
	if err != nil {
		subscription.releaseCOM(session.server)
		return nil, err
	}
	subscription.connectionPoint = point
	subscription.cookie = cookie
	subscription.advised = true
	return subscription, nil
}

// releaseCOM reverses creation on the DA thread. It never blocks on a consumer
// and never delivers the pending set, which belongs to a connection generation
// that is ending.
func (subscription *daSubscription) releaseCOM(server *iopcServer) {
	if subscription.advised {
		_ = unadviseDataCallback(subscription.connectionPoint, subscription.cookie)
		subscription.advised = false
	}
	if subscription.connectionPoint != nil {
		subscription.connectionPoint.release()
		subscription.connectionPoint = nil
	}
	if subscription.itemMgt != nil {
		subscription.itemMgt.release()
		subscription.itemMgt = nil
	}
	if subscription.hasServerGroup {
		_ = removeDAGroup(server, subscription.serverGroupHandle)
		subscription.hasServerGroup = false
		subscription.serverGroupHandle = 0
	}
	if subscription.callbackID != 0 {
		unregisterCallback(subscription.callbackID)
		subscription.callbackID = 0
	}
	// Unpinning is safe only once the server can no longer reach the object.
	// A server that leaked a reference keeps the allocation pinned instead of
	// risking a use-after-free; the leak is bounded by MaxSubscriptions.
	if subscription.pinned && subscription.callback != nil && atomic.LoadInt32(&subscription.callback.refs) <= 1 {
		subscription.pinner.Unpin()
		subscription.pinned = false
	}
}

func (subscription *daSubscription) teardown(server *iopcServer, err error) {
	subscription.releaseCOM(server)
	if subscription.pending != nil {
		subscription.pending.invalidate(err)
	}
}
