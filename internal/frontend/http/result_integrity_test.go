package http

import (
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// readResultsMatchRequest is the HTTP frontend's half of the same defence the gRPC
// frontend has: the runtime must hand back results that line up with the
// request and hold together internally, or the response fails closed with
// INTERNAL_RESULT_MISMATCH rather than reporting a value whose identity is in
// doubt.
//
// It is one disjunction of seven clauses plus an ordering check, and the
// mutation sweep found every `||` in it removable. A disjunction tested by
// breaking several things at once cannot distinguish its clauses: with `&&`,
// only a result violating all of them is refused, and the test still fails --
// for the wrong reason. Each case here breaks exactly one.

func validHTTPResult(itemID opcda.DAItemID) opcda.ReadResult {
	varType := opcda.VTI4
	return opcda.ReadResult{
		ItemID:         itemID,
		VarType:        &varType,
		HRESULT:        opcda.SOK,
		HRESULTPresent: true,
		Value: &opcda.DAValue{
			ItemID: itemID, VarType: varType, Value: int32(7),
			QualityRaw: 0x00C0, HRESULT: opcda.SOK,
			Timestamp: time.Unix(1, 0), TimestampPresent: true,
		},
	}
}

func TestReadResultsMustLineUpWithTheRequest(t *testing.T) {
	items := []opcda.DAItemID{"Test/Int32"}
	if !readResultsMatchRequest(items, []opcda.ReadResult{validHTTPResult("Test/Int32")}) {
		t.Fatal("the baseline result does not match, so every case below proves nothing")
	}

	for _, testCase := range []struct {
		clause string
		break_ func(*opcda.ReadResult)
	}{
		{"a result for a different item", func(r *opcda.ReadResult) {
			r.ItemID = "Test/Other"
			r.Value.ItemID = "Test/Other"
		}},
		{"an error code beside a value", func(r *opcda.ReadResult) {
			r.ErrorCode = string(opcda.CodeUnsupportedVarType)
		}},
		{"no HRESULT at all", func(r *opcda.ReadResult) {
			r.HRESULTPresent = false
		}},
		{"a failed HRESULT beside a value", func(r *opcda.ReadResult) {
			r.HRESULT = opcda.HRESULT(-2147024891)
			r.Value.HRESULT = opcda.HRESULT(-2147024891)
		}},
		{"no VARTYPE beside a value", func(r *opcda.ReadResult) {
			r.VarType = nil
		}},
		{"a value naming a different ItemID", func(r *opcda.ReadResult) {
			r.Value.ItemID = "Test/Other"
		}},
		{"a value whose VARTYPE disagrees with the result's", func(r *opcda.ReadResult) {
			r.Value.VarType = opcda.VTR8
		}},
		{"a value whose HRESULT disagrees with the result's", func(r *opcda.ReadResult) {
			r.Value.HRESULT = opcda.HRESULT(-2147024891)
		}},
	} {
		t.Run(testCase.clause, func(t *testing.T) {
			result := validHTTPResult("Test/Int32")
			testCase.break_(&result)
			if readResultsMatchRequest(items, []opcda.ReadResult{result}) {
				t.Errorf("a result with %s was accepted", testCase.clause)
			}
		})
	}
}

// Order is part of the contract: the reference says results preserve request
// order, and a client reads them positionally. Two results that are each
// individually valid still do not match if they arrive the other way round.
func TestReadResultsMustPreserveRequestOrder(t *testing.T) {
	items := []opcda.DAItemID{"Test/A", "Test/B"}
	inOrder := []opcda.ReadResult{validHTTPResult("Test/A"), validHTTPResult("Test/B")}
	if !readResultsMatchRequest(items, inOrder) {
		t.Fatal("results in request order did not match")
	}
	swapped := []opcda.ReadResult{validHTTPResult("Test/B"), validHTTPResult("Test/A")}
	if readResultsMatchRequest(items, swapped) {
		t.Error("results returned out of request order were accepted")
	}
}

// A result with no value is legitimate only when it says why.
func TestAResultWithoutAValueMustExplainItself(t *testing.T) {
	items := []opcda.DAItemID{"Test/Int32"}
	withoutValue := func() opcda.ReadResult {
		result := validHTTPResult("Test/Int32")
		result.Value = nil
		return result
	}

	failed := withoutValue()
	failed.HRESULT = opcda.HRESULT(-1073479673)
	if !readResultsMatchRequest(items, []opcda.ReadResult{failed}) {
		t.Error("a failed result without a value was rejected")
	}
	coded := withoutValue()
	coded.ErrorCode = string(opcda.CodeUnsupportedVarType)
	if !readResultsMatchRequest(items, []opcda.ReadResult{coded}) {
		t.Error("a result carrying an error code without a value was rejected")
	}

	if readResultsMatchRequest(items, []opcda.ReadResult{withoutValue()}) {
		t.Error("a successful result with no value was accepted")
	}
	silent := withoutValue()
	silent.HRESULTPresent = false
	if readResultsMatchRequest(items, []opcda.ReadResult{silent}) {
		t.Error("a result with no value and no HRESULT was accepted")
	}
}
