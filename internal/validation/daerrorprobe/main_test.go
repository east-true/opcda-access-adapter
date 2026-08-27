package main

import (
	"testing"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
	"github.com/east-true/opcda-access-adapter/internal/opcua"
)

// The probe runs only on Windows against a real server, so its decision logic
// is tested here instead: what it records, and what it refuses to record.

// A row is confirmed only when the HRESULT the source produced maps through the
// real function to the table's answer. Both halves have to hold.
func TestExpectConfirmsOnlyWhenSourceAndMappingBothAgree(t *testing.T) {
	row, err := expect(write, "OPC_E_BADRIGHTS", opcua.OPCEBadRights,
		opcua.OPCEBadRights, opcua.StatusBadNotWritable)
	if err != nil {
		t.Fatalf("a matching row was refused: %v", err)
	}
	if !row.observed || row.hresult != opcua.OPCEBadRights {
		t.Fatalf("row = %+v, want it recorded as observed", row)
	}

	if _, err := expect(write, "OPC_E_BADRIGHTS", opcua.OPCEUnknownItemID,
		opcua.OPCEBadRights, opcua.StatusBadNotWritable); err == nil {
		t.Fatal("a source answering a different HRESULT was accepted")
	}
	// The status a row claims must be the one the mapping actually produces.
	if _, err := expect(write, "OPC_E_BADRIGHTS", opcua.OPCEBadRights,
		opcua.OPCEBadRights, opcua.StatusBadOutOfRange); err == nil {
		t.Fatal("a row whose mapping contradicts the table was accepted")
	}
}

// A row this source simply does not produce is recorded with its reason, not
// treated as a failure and not silently dropped.
func TestUnproducedRowIsRecordedWithItsReason(t *testing.T) {
	row, err := observedIf(write, "OPC_S_CLAMP", opcda.SOK, opcua.OPCSClamp,
		opcua.StatusGoodClamped, "this-source-does-not-clamp")
	if err != nil {
		t.Fatalf("an unproduced row failed the probe: %v", err)
	}
	if row.observed {
		t.Fatal("an unproduced row was recorded as observed")
	}
	if row.reason == "" {
		t.Fatal("an unproduced row was recorded without a reason")
	}
}

// Tables A.4 and A.5 answer OPC_E_INVALID_PID differently, so a row must be
// checked against its own table. Using one mapper for both would hide that.
func TestEachDirectionUsesItsOwnTable(t *testing.T) {
	if read.table() != "A.4" || write.table() != "A.5" {
		t.Fatalf("tables = %s/%s", read.table(), write.table())
	}
	if got := read.mapper()(opcua.OPCEInvalidPID); got != opcua.StatusBadAttributeIDInvalid {
		t.Fatalf("read mapper answered %s", got.Hex())
	}
	if got := write.mapper()(opcua.OPCEInvalidPID); got != opcua.StatusBadNodeIdInvalid {
		t.Fatalf("write mapper answered %s", got.Hex())
	}
}
