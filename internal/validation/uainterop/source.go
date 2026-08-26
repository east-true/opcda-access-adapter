// Package main provides the scripted DA source the third-party OPC UA client
// interoperability harness runs against.
//
// The harness answers one question the project's own probes cannot: does an
// OPC UA client written by somebody else understand what this adapter puts on
// the wire? Every UA test before it exercised this project's encoder against
// this project's decoder, which agree with each other by construction — the
// duplicated SecureChannelId that survived a full unit suite and was caught
// only by a real socket is the standing reminder of what that cannot catch.
//
// The source below is a stand-in, not a DA server. It exists so the UA
// frontend can be driven on any platform, and it deliberately never touches
// COM. What it does reproduce faithfully is the *shape* of what the DA core
// hands the UA layer: exact ItemIDs, raw qualities, canonical VARTYPEs,
// timestamp presence, per-item HRESULTs, and access rights reported by
// AddItems rather than Browse. A third-party client judging those is judging
// the adapter's real output.
package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// scriptedItem is one DA item the source can answer for.
type scriptedItem struct {
	itemID           opcda.DAItemID
	varType          opcda.DAVarType
	value            any
	quality          uint16
	timestampPresent bool
	rights           opcda.DAAccessRights
	// changes marks an item whose value advances on every subscription tick,
	// so a client can observe a real data change rather than a repeat.
	changes bool
	// dirty marks an item whose cached value a write has just changed. A DA
	// server reports such a change through OnDataChange like any other, so a
	// client that writes and then waits for a notification gets one.
	dirty bool
}

// branch is one level of the browse hierarchy. Names are the browse names; the
// ItemID is carried by the item itself and is never derived from the path.
type branch struct {
	name  string
	items []*scriptedItem
}

const (
	readWrite = 0x03
	readOnly  = 0x01
	writeOnly = 0x02
)

func rights(raw uint32) opcda.DAAccessRights {
	return opcda.DAAccessRights{
		Raw:   raw,
		Read:  raw&readOnly != 0,
		Write: raw&writeOnly != 0,
	}
}

// scriptedSource implements opcda.Runtime over an in-memory item table.
type scriptedSource struct {
	// browsable is false for a source that does not implement
	// IOPCBrowseServerAddressSpace, which DA 2.05a leaves optional. A client
	// then has to address items by ItemID alone.
	browsable    bool
	writeEnabled bool

	mu       sync.Mutex
	branches []*branch
	byID     map[opcda.DAItemID]*scriptedItem
	ticks    uint64
	subs     map[opcda.SubscriptionID]*scriptedSubscription
	nextSub  uint64
}

func newScriptedSource(browsable, writeEnabled bool) *scriptedSource {
	source := &scriptedSource{
		browsable:    browsable,
		writeEnabled: writeEnabled,
		byID:         make(map[opcda.DAItemID]*scriptedItem),
		subs:         make(map[opcda.SubscriptionID]*scriptedSubscription),
	}
	source.populate()
	return source
}

// sampleTimestamp is fixed so a client can assert an exact SourceTimestamp
// rather than a value that drifts with when the harness happened to run.
var sampleTimestamp = time.Date(2024, time.March, 14, 15, 9, 26, 535000000, time.UTC)

func (s *scriptedSource) populate() {
	add := func(name string, items ...*scriptedItem) {
		b := &branch{name: name, items: items}
		s.branches = append(s.branches, b)
		for _, item := range items {
			s.byID[item.itemID] = item
		}
	}
	item := func(id string, vt opcda.DAVarType, value any, quality uint16, ts bool, raw uint32) *scriptedItem {
		return &scriptedItem{
			itemID:           opcda.DAItemID(id),
			varType:          vt,
			value:            value,
			quality:          quality,
			timestampPresent: ts,
			rights:           rights(raw),
		}
	}

	good := uint16(0xC0)

	// Every VARTYPE the adapter maps, so a client sees each Part 8 Table A.2
	// row as the UA built-in type the table names. VT_DATE is the row worth
	// watching: the table maps it to Double, not DateTime.
	add("Types",
		item("Types.Bool", opcda.VTBool, true, good, true, readWrite),
		item("Types.SByte", opcda.VTI1, int8(-8), good, true, readWrite),
		item("Types.Byte", opcda.VTUI1, uint8(200), good, true, readWrite),
		item("Types.Int16", opcda.VTI2, int16(-1234), good, true, readWrite),
		item("Types.UInt16", opcda.VTUI2, uint16(60000), good, true, readWrite),
		item("Types.Int32", opcda.VTI4, int32(-70000), good, true, readWrite),
		item("Types.UInt32", opcda.VTUI4, uint32(4000000000), good, true, readWrite),
		item("Types.Int64", opcda.VTI8, int64(-5000000000), good, true, readWrite),
		item("Types.UInt64", opcda.VTUI8, uint64(18000000000000000000), good, true, readWrite),
		item("Types.Float", opcda.VTR4, float32(1.5), good, true, readWrite),
		item("Types.Double", opcda.VTR8, float64(2.25), good, true, readWrite),
		item("Types.String", opcda.VTBSTR, "hello", good, true, readWrite),
		// A VT_DATE carries an OLE automation date, a float64 count of days.
		item("Types.Date", opcda.VTDate, float64(45365.5), good, true, readWrite),
	)

	// Raw DA qualities, so a client sees Table A.3 applied rather than a
	// quality flattened to Good. LastKnown and OutOfService share a row.
	add("Quality",
		item("Quality.Bad", opcda.VTR8, float64(0), 0x00, true, readWrite),
		item("Quality.Uncertain", opcda.VTR8, float64(1), 0x40, true, readWrite),
		item("Quality.LastKnown", opcda.VTR8, float64(2), 0x14, true, readWrite),
		item("Quality.OutOfService", opcda.VTR8, float64(3), 0x1C, true, readWrite),
		item("Quality.LocalOverride", opcda.VTR8, float64(4), 0xD8, true, readWrite),
	)

	// Access rights come from AddItems in DA, never from Browse, so these
	// prove the AccessLevel a client reads is the source's answer.
	add("Rights",
		item("Rights.ReadOnly", opcda.VTI4, int32(7), good, true, readOnly),
		item("Rights.WriteOnly", opcda.VTI4, int32(8), good, true, writeOnly),
	)

	// A DA server need not report a timestamp. The adapter must leave the
	// SourceTimestamp unset rather than substituting its own clock.
	add("Timestamp",
		item("Timestamp.Absent", opcda.VTI4, int32(9), good, false, readWrite),
	)

	// ItemIDs a naive implementation would normalize. A client reading these
	// back proves the exact bytes survived the round trip through a UA
	// NodeId, which is the adapter's central promise about identity.
	add("Odd",
		item("Odd.Item With Spaces", opcda.VTI4, int32(10), good, true, readWrite),
		item("Odd/Slash.Separated", opcda.VTI4, int32(11), good, true, readWrite),
		item("Odd.온도", opcda.VTR8, float64(21.5), good, true, readWrite),
		item("Odd.MiXeD.CaSe", opcda.VTI4, int32(13), good, true, readWrite),
	)

	ramp := item("Simulation.Ramp", opcda.VTR8, float64(0), good, true, readWrite)
	ramp.changes = true
	counter := item("Simulation.Counter", opcda.VTI4, int32(0), good, true, readWrite)
	counter.changes = true
	add("Simulation", ramp, counter)

	add("Writable",
		item("Writable.Setpoint", opcda.VTR8, float64(50), good, true, readWrite),
	)
}

func (s *scriptedSource) Status(context.Context) opcda.RuntimeStatus {
	s.mu.Lock()
	subs := len(s.subs)
	s.mu.Unlock()
	browseCapability := "supported"
	if !s.browsable {
		browseCapability = "unsupported"
	}
	return opcda.RuntimeStatus{
		State:                opcda.RuntimeStateConnected,
		ConnectionGeneration: 1,
		WriteEnabled:         s.writeEnabled,
		SubscriptionCount:    subs,
		Capabilities: opcda.Capabilities{
			Browse:    browseCapability,
			Read:      true,
			Write:     s.writeEnabled,
			Subscribe: true,
		},
	}
}

func (s *scriptedSource) Browse(_ context.Context, request opcda.BrowseRequest) (opcda.BrowseResult, error) {
	if !s.browsable {
		return opcda.BrowseResult{}, opcda.NewAdapterError(
			opcda.CodeBrowseUnsupported,
			"the source does not implement IOPCBrowseServerAddressSpace",
		)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	result := opcda.BrowseResult{Path: request.Path}
	switch len(request.Path) {
	case 0:
		if request.Filter == opcda.BrowseFilterItem {
			return result, nil
		}
		for _, b := range s.branches {
			result.Entries = append(result.Entries, opcda.BrowseEntry{
				Kind: opcda.BrowseEntryBranch,
				Name: b.name,
			})
		}
	case 1:
		if request.Filter == opcda.BrowseFilterBranch {
			return result, nil
		}
		for _, b := range s.branches {
			if b.name != request.Path[0] {
				continue
			}
			for _, it := range b.items {
				id := it.itemID
				// Browse reports neither the canonical type nor the access
				// rights, which is what a real DA server does: both arrive
				// with AddItems. Leaving them nil keeps the harness honest
				// about what the UA layer has to cope with.
				result.Entries = append(result.Entries, opcda.BrowseEntry{
					Kind:   opcda.BrowseEntryItem,
					Name:   leafName(string(id)),
					ItemID: &id,
				})
			}
			return result, nil
		}
	}
	return result, nil
}

// leafName is the browse name shown for an item. It is display only; the
// ItemID beside it stays exact.
func leafName(itemID string) string {
	if index := strings.LastIndexAny(itemID, "./"); index >= 0 && index+1 < len(itemID) {
		return itemID[index+1:]
	}
	return itemID
}

func (s *scriptedSource) ReadBatch(_ context.Context, request opcda.ReadRequest) ([]opcda.ReadResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	results := make([]opcda.ReadResult, 0, len(request.Items))
	for _, id := range request.Items {
		results = append(results, s.readLocked(id))
	}
	return results, nil
}

func (s *scriptedSource) readLocked(id opcda.DAItemID) opcda.ReadResult {
	item, ok := s.byID[id]
	if !ok {
		// The source is the authority on whether an item exists. Part 8
		// Table A.4 turns this into Bad_NodeIdUnknown.
		return opcda.ReadResult{
			ItemID:         id,
			HRESULT:        opcda.HRESULT(-1073479673), // OPC_E_UNKNOWNITEMID
			HRESULTPresent: true,
		}
	}
	if !item.rights.Read {
		return opcda.ReadResult{
			ItemID:         id,
			HRESULT:        opcda.HRESULT(-1073479674), // OPC_E_BADRIGHTS
			HRESULTPresent: true,
			CanonicalType:  &item.varType,
			AccessRights:   &item.rights,
		}
	}
	varType := item.varType
	itemRights := item.rights
	return opcda.ReadResult{
		ItemID: id,
		Value: &opcda.DAValue{
			ItemID:           id,
			VarType:          varType,
			Value:            item.value,
			QualityRaw:       item.quality,
			Timestamp:        sampleTimestamp,
			TimestampPresent: item.timestampPresent,
			AccessRights:     &itemRights,
		},
		VarType:        &varType,
		CanonicalType:  &varType,
		AccessRights:   &itemRights,
		HRESULT:        opcda.SOK,
		HRESULTPresent: true,
	}
}

func (s *scriptedSource) WriteBatch(_ context.Context, items []opcda.WriteItem) ([]opcda.WriteResult, error) {
	// The DA runtime refuses the whole batch when write is disabled, rather
	// than reporting a per-item failure, so the harness does the same. Write
	// being off by default is a project-wide default and a client has to see
	// it the way the real runtime states it.
	if !s.writeEnabled {
		return nil, opcda.NewAdapterError(opcda.CodeWriteDisabled, "write is disabled")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	results := make([]opcda.WriteResult, 0, len(items))
	for _, write := range items {
		results = append(results, s.writeLocked(write))
	}
	return results, nil
}

func (s *scriptedSource) writeLocked(write opcda.WriteItem) opcda.WriteResult {
	item, ok := s.byID[write.ItemID]
	if !ok {
		return opcda.WriteResult{
			ItemID:         write.ItemID,
			HRESULT:        opcda.HRESULT(-1073479673), // OPC_E_UNKNOWNITEMID
			HRESULTPresent: true,
		}
	}
	if !item.rights.Write {
		return opcda.WriteResult{
			ItemID:         write.ItemID,
			HRESULT:        opcda.HRESULT(-1073479674), // OPC_E_BADRIGHTS
			HRESULTPresent: true,
		}
	}
	item.value = write.Value
	item.dirty = true
	return opcda.WriteResult{
		ItemID:         write.ItemID,
		HRESULT:        opcda.SOK,
		HRESULTPresent: true,
	}
}

func (s *scriptedSource) Shutdown(context.Context) error {
	s.mu.Lock()
	subs := make([]*scriptedSubscription, 0, len(s.subs))
	for _, sub := range s.subs {
		subs = append(subs, sub)
	}
	s.subs = make(map[opcda.SubscriptionID]*scriptedSubscription)
	s.mu.Unlock()

	for _, sub := range subs {
		sub.invalidate(fmt.Errorf("the source is shutting down"))
	}
	return nil
}

// sortedItemIDs is used by the harness banner so the printed inventory is
// stable between runs.
func (s *scriptedSource) sortedItemIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.byID))
	for id := range s.byID {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	return ids
}
