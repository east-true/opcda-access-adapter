package opcda

import (
	"errors"
	"testing"
)

// An *AdapterError is carried in an error interface, and a typed nil pointer
// in an interface is not a nil interface: errors.As will hand one back, and
// the caller then calls Error on it. The guard is what stops that being a
// panic in the middle of reporting some other failure -- the worst moment for
// one, because the original problem is lost with it.
//
// Inverting the guard is the exact swap: a real error reports nothing and a
// nil one panics. Both halves are asserted, because a case that only called
// the nil receiver would pass against a method that returned "" for
// everything.
func TestANilAdapterErrorReportsNothingRatherThanPanicking(t *testing.T) {
	var absent *AdapterError
	if message := absent.Error(); message != "" {
		t.Errorf("a nil adapter error reported %q", message)
	}

	present := NewAdapterError(CodeInvalidValue, "the value did not fit its VARTYPE")
	if message := present.Error(); message != "the value did not fit its VARTYPE" {
		t.Errorf("a real adapter error reported %q", message)
	}

	// The shape this guard exists for: a typed nil reached through the error
	// interface, which is what a caller gets from a helper that returns
	// (*AdapterError)(nil) as an error.
	var asInterface error = absent
	if asInterface == nil {
		t.Fatal("a typed nil pointer compared equal to a nil interface, so this case proves nothing")
	}
	if message := asInterface.Error(); message != "" {
		t.Errorf("a typed nil adapter error reported %q", message)
	}

	// And it stays usable through the errors package, which is how callers
	// actually reach it.
	wrapped := errors.Join(present, nil)
	var found *AdapterError
	if !errors.As(wrapped, &found) || found.Code != CodeInvalidValue {
		t.Errorf("a wrapped adapter error was not recovered: %v", wrapped)
	}
}

// Two survivors in this package join the ones already written down, for the
// same reasons recorded elsewhere:
//
//   - reconnectDelay's final `delay > maximum` to `>=` is the clamp pattern:
//     at exactly the maximum both arms answer the maximum.
//   - the timing ring's sort comparator to its inclusive form reorders only
//     samples that are equal to each other, and a percentile drawn from them
//     is the same duration either way.
//
// One is a gap that this platform cannot reach. DetectLocalServers refuses a
// result set larger than the configured maximum, and exactly the maximum must
// come back -- but the detection that produces it is Windows-only, and on any
// other platform the call fails before the bound is consulted. It is covered
// by the Windows detection scenarios rather than here.
