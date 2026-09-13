package grpcfrontend

import (
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// encodeReadResult refuses to encode a Read result that contradicts itself,
// answering INTERNAL_RESULT_MISMATCH instead. It is the frontend's defence
// against the runtime handing it a value whose identity does not hold together
// -- an ItemID that does not match the one asked for, a VARTYPE that disagrees
// with the value's own, a success carrying no value.
//
// The check is one disjunction of seven clauses, and the mutation sweep found
// that turning any of its `||` into `&&` survives. That is what a disjunction
// tested by breaking several things at once always does: with `&&`, only a
// result that violates every clause is refused, and a test that violates
// several still fails for the wrong reason. Nothing said any single clause
// carried its own weight.
//
// Every case below breaks exactly one clause of an otherwise valid result.

func validReadResult() opcda.ReadResult {
	varType := opcda.VTI4
	return opcda.ReadResult{
		ItemID:         "Test/Int32",
		VarType:        &varType,
		HRESULT:        opcda.SOK,
		HRESULTPresent: true,
		Value: &opcda.DAValue{
			ItemID: "Test/Int32", VarType: varType, Value: int32(7),
			QualityRaw: 0x00C0, HRESULT: opcda.SOK,
			Timestamp: time.Unix(1, 0), TimestampPresent: true,
		},
	}
}

func TestEncodingRefusesAResultThatContradictsItself(t *testing.T) {
	if _, err := encodeReadResult(validReadResult()); err != nil {
		t.Fatalf("the baseline result is not valid, so every case below proves "+
			"nothing: %v", err)
	}

	for _, testCase := range []struct {
		clause string
		break_ func(*opcda.ReadResult)
	}{
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
			result := validReadResult()
			testCase.break_(&result)
			if _, err := encodeReadResult(result); err == nil {
				t.Errorf("a result with %s was encoded", testCase.clause)
			}
		})
	}
}

// The other branch: a result with no value at all. It is legitimate only when
// the result says why, either with an error code or with a failed HRESULT, so
// the three ways of saying nothing are each their own clause.
func TestEncodingRefusesASuccessWithNoValue(t *testing.T) {
	withoutValue := func() opcda.ReadResult {
		result := validReadResult()
		result.Value = nil
		return result
	}

	// Valid: a failure that explains itself.
	failed := withoutValue()
	failed.HRESULT = opcda.HRESULT(-1073479673)
	if _, err := encodeReadResult(failed); err != nil {
		t.Fatalf("a failed result without a value was refused: %v", err)
	}
	coded := withoutValue()
	coded.ErrorCode = string(opcda.CodeUnsupportedVarType)
	if _, err := encodeReadResult(coded); err != nil {
		t.Fatalf("a result carrying an error code without a value was refused: %v", err)
	}

	// Invalid: a success with nothing in it, which is the case the clause
	// exists for. The HRESULT is present and succeeded, and no error code
	// explains the absence.
	if _, err := encodeReadResult(withoutValue()); err == nil {
		t.Error("a successful result with no value was encoded")
	}

	// A result with neither a value nor an HRESULT says nothing at all.
	silent := withoutValue()
	silent.HRESULTPresent = false
	if _, err := encodeReadResult(silent); err == nil {
		t.Error("a result with no value and no HRESULT was encoded")
	}
}
