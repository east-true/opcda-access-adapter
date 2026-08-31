//go:build windows

package opcda

import (
	"fmt"
	"runtime"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

const (
	opcBrowseBranch = 1
	opcBrowseLeaf   = 2
	opcBrowseFlat   = 3

	opcNamespaceHierarchical = 1
	opcNamespaceFlat         = 2

	opcBrowseUp   = 1
	opcBrowseDown = 2
	opcBrowseTo   = 3
)

var iidIOPCBrowseServerAddressSpace = guid{
	Data1: 0x39C13A4F, Data2: 0x011E, Data3: 0x11D0,
	Data4: [8]byte{0x96, 0x75, 0x00, 0x20, 0xAF, 0xD8, 0xAD, 0xB3},
}

type iopcBrowseServerAddressSpace struct {
	VTable *iopcBrowseServerAddressSpaceVTable
}

type iopcBrowseServerAddressSpaceVTable struct {
	iUnknownVTable
	QueryOrganization    uintptr
	ChangeBrowsePosition uintptr
	BrowseOPCItemIDs     uintptr
	GetItemID            uintptr
	BrowseAccessPaths    uintptr
}

func (browse *iopcBrowseServerAddressSpace) release() uint32 {
	if browse == nil || browse.VTable == nil {
		return 0
	}
	return releaseCOM(unsafe.Pointer(browse), browse.VTable.Release)
}

type iEnumString struct {
	VTable *iEnumStringVTable
}

type iEnumStringVTable struct {
	iUnknownVTable
	Next  uintptr
	Skip  uintptr
	Reset uintptr
	Clone uintptr
}

func (enumerator *iEnumString) release() uint32 {
	if enumerator == nil || enumerator.VTable == nil {
		return 0
	}
	return releaseCOM(unsafe.Pointer(enumerator), enumerator.VTable.Release)
}

func queryBrowseInterface(server *iopcServer) (*iopcBrowseServerAddressSpace, bool, error) {
	var browse *iopcBrowseServerAddressSpace
	result, _, _ := syscall.SyscallN(
		server.VTable.QueryInterface,
		uintptr(unsafe.Pointer(server)),
		uintptr(unsafe.Pointer(&iidIOPCBrowseServerAddressSpace)),
		uintptr(unsafe.Pointer(&browse)),
	)
	runtime.KeepAlive(server)
	hr := hresultFromCall(result)
	if hr == ENoInterface {
		return nil, false, nil
	}
	if hr.Failed() {
		return nil, false, &SourceError{Operation: "IOPCServer::QueryInterface(IOPCBrowseServerAddressSpace)", HRESULT: hr}
	}
	if browse == nil {
		return nil, false, fmt.Errorf("QueryInterface(IOPCBrowseServerAddressSpace) returned nil")
	}
	return browse, true, nil
}

func (browse *iopcBrowseServerAddressSpace) queryOrganization() (uint32, error) {
	var organization uint32
	result, _, _ := syscall.SyscallN(
		browse.VTable.QueryOrganization,
		uintptr(unsafe.Pointer(browse)),
		uintptr(unsafe.Pointer(&organization)),
	)
	runtime.KeepAlive(browse)
	if hr := hresultFromCall(result); hr.Failed() {
		return 0, &SourceError{Operation: "IOPCBrowseServerAddressSpace::QueryOrganization", HRESULT: hr}
	}
	if organization != opcNamespaceHierarchical && organization != opcNamespaceFlat {
		return 0, fmt.Errorf("IOPCBrowseServerAddressSpace returned unknown organization %d", organization)
	}
	return organization, nil
}

func (browse *iopcBrowseServerAddressSpace) changePosition(direction uint32, value string) error {
	wide, err := syscall.UTF16PtrFromString(value)
	if err != nil {
		return NewAdapterError(CodeInvalidRequest, "browse path contains NUL")
	}
	result, _, _ := syscall.SyscallN(
		browse.VTable.ChangeBrowsePosition,
		uintptr(unsafe.Pointer(browse)),
		uintptr(direction),
		uintptr(unsafe.Pointer(wide)),
	)
	runtime.KeepAlive(browse)
	runtime.KeepAlive(wide)
	if hr := hresultFromCall(result); hr.Failed() {
		return &SourceError{Operation: "IOPCBrowseServerAddressSpace::ChangeBrowsePosition", HRESULT: hr}
	}
	return nil
}

func (browse *iopcBrowseServerAddressSpace) enumerateNames(filter uint32, maximum, maxCodeUnits int) ([]string, error) {
	empty, err := syscall.UTF16PtrFromString("")
	if err != nil {
		return nil, err
	}
	var enumerator *iEnumString
	result, _, _ := syscall.SyscallN(
		browse.VTable.BrowseOPCItemIDs,
		uintptr(unsafe.Pointer(browse)),
		uintptr(filter),
		uintptr(unsafe.Pointer(empty)),
		uintptr(VTEmpty),
		0,
		uintptr(unsafe.Pointer(&enumerator)),
	)
	runtime.KeepAlive(browse)
	runtime.KeepAlive(empty)
	if hr := hresultFromCall(result); hr.Failed() {
		return nil, &SourceError{Operation: "IOPCBrowseServerAddressSpace::BrowseOPCItemIDs", HRESULT: hr}
	}
	if enumerator == nil {
		// Some servers represent an empty result with a successful nil
		// enumerator. Treat it only as empty; never synthesize entries.
		return []string{}, nil
	}
	defer enumerator.release()

	names := make([]string, 0, maximum)
	for {
		var (
			namePointer *uint16
			fetched     uint32
		)
		nextResult, _, _ := syscall.SyscallN(
			enumerator.VTable.Next,
			uintptr(unsafe.Pointer(enumerator)),
			1,
			uintptr(unsafe.Pointer(&namePointer)),
			uintptr(unsafe.Pointer(&fetched)),
		)
		runtime.KeepAlive(enumerator)
		nextHR := hresultFromCall(nextResult)
		if nextHR.Failed() {
			return nil, &SourceError{Operation: "IEnumString::Next", HRESULT: nextHR}
		}
		if fetched == 0 {
			return names, nil
		}
		if fetched != 1 || namePointer == nil {
			if namePointer != nil {
				coTaskMemFree(unsafe.Pointer(namePointer))
			}
			return nil, fmt.Errorf("IEnumString::Next returned invalid fetched count %d", fetched)
		}
		name, decodeErr := decodeTaskString(namePointer, maxCodeUnits)
		coTaskMemFree(unsafe.Pointer(namePointer))
		if decodeErr != nil {
			return nil, decodeErr
		}
		names = append(names, name)
		if len(names) > maximum {
			return nil, NewAdapterError(CodeBrowseResultLimitExceeded, "Browse result limit exceeded")
		}
		if nextHR == SFalse {
			return names, nil
		}
	}
}

func (browse *iopcBrowseServerAddressSpace) getItemID(name string, maxCodeUnits, maxItemIDBytes int) (DAItemID, error) {
	wide, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return "", NewAdapterError(CodeInvalidValue, "browse name contains NUL")
	}
	var itemIDPointer *uint16
	result, _, _ := syscall.SyscallN(
		browse.VTable.GetItemID,
		uintptr(unsafe.Pointer(browse)),
		uintptr(unsafe.Pointer(wide)),
		uintptr(unsafe.Pointer(&itemIDPointer)),
	)
	runtime.KeepAlive(browse)
	runtime.KeepAlive(wide)
	if hr := hresultFromCall(result); hr.Failed() {
		return "", &SourceError{Operation: "IOPCBrowseServerAddressSpace::GetItemID", HRESULT: hr}
	}
	if itemIDPointer == nil {
		return "", fmt.Errorf("GetItemID returned a nil ItemID")
	}
	defer coTaskMemFree(unsafe.Pointer(itemIDPointer))
	itemID, err := decodeTaskString(itemIDPointer, maxCodeUnits)
	if err != nil {
		return "", err
	}
	if len([]byte(itemID)) > maxItemIDBytes {
		return "", NewAdapterError(CodeItemIDTooLong, "source ItemID exceeds configured limit")
	}
	return DAItemID(itemID), nil
}

func decodeTaskString(pointer *uint16, maximum int) (string, error) {
	units := make([]uint16, 0, min(maximum, 256))
	for index := 0; index <= maximum; index++ {
		unit := *(*uint16)(unsafe.Add(unsafe.Pointer(pointer), uintptr(index)*unsafe.Sizeof(uint16(0))))
		if unit == 0 {
			if !wellFormedUTF16(units) {
				return "", NewAdapterError(CodeInvalidValue, "source string contains invalid UTF-16")
			}
			return string(utf16.Decode(units)), nil
		}
		if index == maximum {
			return "", NewAdapterError(CodeBSTRTooLong, "source string exceeds configured limit")
		}
		units = append(units, unit)
	}
	return "", NewAdapterError(CodeBSTRTooLong, "source string exceeds configured limit")
}

func (session *daThreadSession) browseAddressSpace(request BrowseRequest, limits Limits) (BrowseResult, error) {
	if session.browse == nil {
		if session.browseCapability == "unsupported" {
			return BrowseResult{}, NewAdapterError(CodeBrowseUnsupported, "OPC DA server does not support Browse")
		}
		return BrowseResult{}, NewAdapterError(CodeRuntimeUnavailable, "OPC DA Browse is unavailable")
	}
	organization, err := session.browse.queryOrganization()
	if err != nil {
		return BrowseResult{}, err
	}
	if organization == opcNamespaceFlat && len(request.Path) != 0 {
		return BrowseResult{}, NewAdapterError(CodeInvalidRequest, "flat OPC DA namespace has no nested browse path")
	}
	if organization == opcNamespaceHierarchical {
		if err := session.browse.changePosition(opcBrowseTo, ""); err != nil {
			return BrowseResult{}, err
		}
		for _, segment := range request.Path {
			if err := session.browse.changePosition(opcBrowseDown, segment); err != nil {
				return BrowseResult{}, err
			}
		}
	}

	result := BrowseResult{Path: append([]string(nil), request.Path...)}
	if organization == opcNamespaceFlat {
		if request.Filter == BrowseFilterBranch {
			return result, nil
		}
		names, err := session.browse.enumerateNames(opcBrowseFlat, limits.MaxBrowseEntries, limits.MaxBSTRCodeUnits)
		if err != nil {
			return BrowseResult{}, err
		}
		entries, err := session.browse.itemEntries(names, limits)
		if err != nil {
			return BrowseResult{}, err
		}
		result.Entries = entries
		return result, nil
	}

	if request.Filter == BrowseFilterAll || request.Filter == BrowseFilterBranch {
		browse := session.browse
		names, err := browse.enumerateNames(opcBrowseBranch, limits.MaxBrowseEntries, limits.MaxBSTRCodeUnits)
		if err != nil {
			return BrowseResult{}, err
		}
		for _, name := range names {
			entry := BrowseEntry{Kind: BrowseEntryBranch, Name: name}
			// A.3.1.2: "The ItemId obtained using the GetItemID is used as a
			// part of the NodeId for each Branch." A branch has an ItemID in
			// the source even though it is not an item, and GetItemID is how
			// the source states it -- which is a different thing from
			// reconstructing one from a browse path, the thing design §35.2
			// forbids.
			//
			// A source may refuse to name a branch. That is its answer, not a
			// failure, and the branch keeps a path-based identity.
			if itemID, err := browse.getItemID(name, limits.MaxBSTRCodeUnits, limits.MaxItemIDBytes); err == nil && itemID != "" {
				branchItemID := itemID
				entry.ItemID = &branchItemID
			}
			result.Entries = append(result.Entries, entry)
		}
	}
	if request.Filter == BrowseFilterAll || request.Filter == BrowseFilterItem {
		remaining := limits.MaxBrowseEntries - len(result.Entries)
		names, err := session.browse.enumerateNames(opcBrowseLeaf, remaining, limits.MaxBSTRCodeUnits)
		if err != nil {
			return BrowseResult{}, err
		}
		entries, err := session.browse.itemEntries(names, limits)
		if err != nil {
			return BrowseResult{}, err
		}
		result.Entries = append(result.Entries, entries...)
	}
	return result, nil
}

func (browse *iopcBrowseServerAddressSpace) itemEntries(names []string, limits Limits) ([]BrowseEntry, error) {
	entries := make([]BrowseEntry, 0, len(names))
	for _, name := range names {
		itemID, err := browse.getItemID(name, limits.MaxBSTRCodeUnits, limits.MaxItemIDBytes)
		if err != nil {
			return nil, err
		}
		entryItemID := itemID
		entries = append(entries, BrowseEntry{
			Kind: BrowseEntryItem, Name: name, ItemID: &entryItemID,
		})
	}
	return entries, nil
}
