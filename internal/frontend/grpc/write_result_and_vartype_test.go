package grpcfrontend

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	opcdav1 "github.com/east-true/opcda-access-adapter/api/opcda/v1"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// A Write result carries an ok flag, and the flag is what a client reads to
// decide whether its value landed. It is true only when the result carries no
// error code and its HRESULT succeeded, and the sweep found the && removable:
// with ||, a failed HRESULT would have been reported as ok as long as no error
// code came with it, which is the commonest shape a DA refusal takes.
func TestAWriteIsOnlyOkWhenNothingWentWrong(t *testing.T) {
	const failure = opcda.HRESULT(-1073479674) // OPC_E_BADRIGHTS

	for _, testCase := range []struct {
		name   string
		result opcda.WriteResult
		ok     bool
	}{
		{
			name:   "a plain success",
			result: opcda.WriteResult{ItemID: "Test/Int32", HRESULT: opcda.SOK, HRESULTPresent: true},
			ok:     true,
		},
		{
			name:   "a failed HRESULT with no error code",
			result: opcda.WriteResult{ItemID: "Test/Int32", HRESULT: failure, HRESULTPresent: true},
			ok:     false,
		},
		{
			name: "a succeeding HRESULT beside an error code",
			result: opcda.WriteResult{
				ItemID: "Test/Int32", HRESULT: opcda.SOK, HRESULTPresent: true,
				ErrorCode: string(opcda.CodeUnsupportedVarType),
			},
			ok: false,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runtime := &testRuntime{
				status: opcda.RuntimeStatus{WriteEnabled: true},
				write: func(_ context.Context, _ []opcda.WriteItem) ([]opcda.WriteResult, error) {
					return []opcda.WriteResult{testCase.result}, nil
				},
			}
			server := New(runtime, Config{MaxWriteItems: 4, MaxItemIDBytes: 64})
			response, err := server.Write(context.Background(), &opcdav1.DAWriteRequest{
				Items: []*opcdav1.DAWriteItem{{
					ItemId:   "Test/Int32",
					DataType: &opcdav1.DAVarType{Raw: uint32(opcda.VTI4), Name: opcda.VTI4.String()},
					Value:    &opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_I4Value{I4Value: 1}},
				}},
			})
			if err != nil {
				t.Fatalf("the Write was refused: %v", err)
			}
			if got := response.Results[0].Ok; got != testCase.ok {
				t.Errorf("ok = %v, want %v", got, testCase.ok)
			}
		})
	}
}

// A declared VARTYPE carries a raw number and optionally a name, and the two
// must agree. The name is a convenience for a reader; letting it disagree would
// mean the request said one type in the field a human checks and another in the
// field the adapter uses.
func TestADeclaredVarTypeMustAgreeWithItsName(t *testing.T) {
	if _, err := decodeWriteVarType(&opcdav1.DAVarType{
		Raw: uint32(opcda.VTI4), Name: opcda.VTI4.String(),
	}); err != nil {
		t.Fatalf("a matching name was refused, so the cases below prove nothing: %v", err)
	}
	// The name is optional, so leaving it out is not a disagreement.
	if _, err := decodeWriteVarType(&opcdav1.DAVarType{Raw: uint32(opcda.VTI4)}); err != nil {
		t.Errorf("an omitted name was refused: %v", err)
	}
	if _, err := decodeWriteVarType(&opcdav1.DAVarType{
		Raw: uint32(opcda.VTI4), Name: opcda.VTR8.String(),
	}); err == nil {
		t.Error("a name naming a different type than the raw value was accepted")
	}
}

// A VARTYPE is sixteen bits on the wire it came from, and the field carrying it
// here is wider. The boundary is the largest value that fits.
func TestADeclaredVarTypeMustFitSixteenBits(t *testing.T) {
	// The widest VARTYPE expressible fits, so it must get past the width test.
	// It is still refused -- every flag bit is set, so it is an array of byrefs
	// -- and the reason is what separates the two: a width check tightened by
	// one would refuse it here instead, and asking only whether it was refused
	// could not tell the difference.
	_, widest := decodeWriteVarType(&opcdav1.DAVarType{Raw: math.MaxUint16})
	if widest == nil {
		t.Fatal("a VARTYPE with every flag bit set was accepted")
	}
	if strings.Contains(widest.Error(), "16 bits") {
		t.Errorf("the widest VARTYPE that fits was refused for its width: %v", widest)
	}
	_, err := decodeWriteVarType(&opcdav1.DAVarType{Raw: math.MaxUint16 + 1})
	if err == nil {
		t.Fatal("a VARTYPE past sixteen bits was accepted")
	}
	if !strings.Contains(err.Error(), "16 bits") {
		t.Errorf("a VARTYPE past sixteen bits was refused as %v, not for its width", err)
	}
}

// durationMilliseconds reports a DA revised rate to a client, and both ends of
// its clamp were unpinned. A rate arriving as zero or past the hour it is
// capped to is the source disagreeing with the range the adapter published.
func TestARevisedRateIsReportedInsideItsRange(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		value time.Duration
		want  uint32
	}{
		{"an ordinary rate", 250 * time.Millisecond, 250},
		{"exactly one millisecond", time.Millisecond, 1},
		{"below one millisecond truncates to none", 500 * time.Microsecond, 0},
		{"a negative rate is none", -time.Second, 0},
		{"exactly the maximum", time.Hour, maximumUpdateRateMilliseconds},
		{"past the maximum is clamped", 2 * time.Hour, maximumUpdateRateMilliseconds},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := durationMilliseconds(testCase.value); got != testCase.want {
				t.Errorf("durationMilliseconds(%v) = %d, want %d", testCase.value, got, testCase.want)
			}
		})
	}
}

// Two survivors in durationMilliseconds are equivalent mutants, checked by
// mutating and running rather than by reading. The function clamps, so at each
// boundary both arms produce the same number: `milliseconds < 0` to `<=`
// returns zero for zero either by the guard or by falling through, and
// `milliseconds > maximum` to `>=` returns the maximum either by the clamp or
// by converting a value that already equals it.
