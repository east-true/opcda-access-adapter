package opcda

import (
	"strings"
	"testing"
	"unicode/utf16"
)

// An array is accepted only when its description and its elements agree.
// design.md §20.4 requires five things to survive -- element VARTYPE,
// dimension count, lower bound and length per dimension, and the element
// values -- so a shape that does not describe the elements beside it is not an
// array this adapter can publish: a client indexing it would reach something
// other than what the source holds.

func testArrayBounds() ArrayLimits {
	return DefaultLimits().ArrayLimits()
}

func int32Array(dimensions []DADimension, elements ...int32) DAArray {
	boxed := make([]any, len(elements))
	for index, element := range elements {
		boxed[index] = element
	}
	return DAArray{ElementType: VTI4, Dimensions: dimensions, Elements: boxed}
}

func TestAnArrayMustDescribeTheElementsItCarries(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		array DAArray
		code  ErrorCode
	}{
		{
			name:  "one dimension holding what it says",
			array: int32Array([]DADimension{{LowerBound: 0, Length: 3}}, 1, 2, 3),
		},
		{
			// A lower bound is the source's choice, not a normalisation the
			// adapter applies, so an array starting anywhere is valid.
			name:  "a dimension that does not start at zero",
			array: int32Array([]DADimension{{LowerBound: 1, Length: 3}}, 1, 2, 3),
		},
		{
			name:  "a dimension that starts below zero",
			array: int32Array([]DADimension{{LowerBound: -5, Length: 2}}, 1, 2),
		},
		{
			name: "two dimensions holding their product",
			array: int32Array([]DADimension{
				{LowerBound: 1, Length: 3}, {LowerBound: 0, Length: 2},
			}, 10, 11, 12, 13, 14, 15),
		},
		{
			name:  "fewer elements than the shape describes",
			array: int32Array([]DADimension{{LowerBound: 0, Length: 3}}, 1, 2),
			code:  CodeInvalidValue,
		},
		{
			name:  "more elements than the shape describes",
			array: int32Array([]DADimension{{LowerBound: 0, Length: 2}}, 1, 2, 3),
			code:  CodeInvalidValue,
		},
		{
			name:  "no dimensions at all",
			array: int32Array(nil, 1),
			code:  CodeInvalidValue,
		},
		{
			name:  "a dimension of no length",
			array: int32Array([]DADimension{{LowerBound: 0, Length: 0}}),
			code:  CodeInvalidValue,
		},
		{
			name: "an element of the wrong type",
			array: DAArray{
				ElementType: VTI4,
				Dimensions:  []DADimension{{LowerBound: 0, Length: 2}},
				Elements:    []any{int32(1), int16(2)},
			},
			code: CodeInvalidValue,
		},
		{
			name: "an element type that is itself an array",
			array: DAArray{
				ElementType: VTI4 | VTArray,
				Dimensions:  []DADimension{{LowerBound: 0, Length: 1}},
				Elements:    []any{int32(1)},
			},
			code: CodeUnsupportedVarType,
		},
		{
			name: "an element type this adapter does not carry",
			array: DAArray{
				ElementType: VTDate,
				Dimensions:  []DADimension{{LowerBound: 0, Length: 1}},
				Elements:    []any{float64(1)},
			},
			code: CodeUnsupportedVarType,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.array.Validate(testArrayBounds())
			if testCase.code == "" {
				if err != nil {
					t.Fatalf("a well-described array was refused: %v", err)
				}
				return
			}
			adapterErr, ok := AsAdapterError(err)
			if !ok || adapterErr.Code != testCase.code {
				t.Fatalf("error = %v, want %s", err, testCase.code)
			}
		})
	}
}

// Each array bound is reachable to its last element, and one step past it is
// not. An operator who configures a thousand elements has said a thousand.
func TestEveryArrayBoundIsUsableToItsLimit(t *testing.T) {
	limits := ArrayLimits{
		MaxElements: 6, MaxDimensions: 2,
		MaxBSTRCodeUnits: 4, MaxArrayBSTRCodeUnits: 8,
	}
	elements := func(count int) []int32 {
		out := make([]int32, count)
		return out
	}

	t.Run("exactly the element limit", func(t *testing.T) {
		array := int32Array([]DADimension{{Length: 6}}, elements(6)...)
		if err := array.Validate(limits); err != nil {
			t.Errorf("an array of exactly %d elements was refused: %v", limits.MaxElements, err)
		}
	})
	t.Run("one element past the limit", func(t *testing.T) {
		array := int32Array([]DADimension{{Length: 7}}, elements(7)...)
		assertArrayError(t, array.Validate(limits), CodeArrayTooLarge)
	})
	t.Run("a product past the limit that no single dimension reaches", func(t *testing.T) {
		array := int32Array([]DADimension{{Length: 4}, {Length: 4}}, elements(16)...)
		assertArrayError(t, array.Validate(limits), CodeArrayTooLarge)
	})
	t.Run("exactly the dimension limit", func(t *testing.T) {
		array := int32Array([]DADimension{{Length: 2}, {Length: 3}}, elements(6)...)
		if err := array.Validate(limits); err != nil {
			t.Errorf("an array of exactly %d dimensions was refused: %v", limits.MaxDimensions, err)
		}
	})
	t.Run("one dimension past the limit", func(t *testing.T) {
		array := int32Array([]DADimension{{Length: 1}, {Length: 1}, {Length: 1}}, elements(1)...)
		assertArrayError(t, array.Validate(limits), CodeArrayTooLarge)
	})
}

// A string array is bounded twice: by what one element may be, and by what all
// of them may be together. The second exists because the first two bounds do
// not constrain it between them -- an array at the element limit whose every
// element is at the length limit is orders of magnitude more memory than the
// same count of numbers.
func TestAStringArrayIsBoundedPerElementAndInTotal(t *testing.T) {
	limits := ArrayLimits{
		MaxElements: 8, MaxDimensions: 2,
		MaxBSTRCodeUnits: 4, MaxArrayBSTRCodeUnits: 8,
	}
	stringArray := func(values ...string) DAArray {
		boxed := make([]any, len(values))
		for index, value := range values {
			boxed[index] = value
		}
		return DAArray{
			ElementType: VTBSTR,
			Dimensions:  []DADimension{{Length: uint32(len(values))}},
			Elements:    boxed,
		}
	}

	if err := stringArray("abcd", "abcd").Validate(limits); err != nil {
		t.Errorf("two elements at exactly both limits were refused: %v", err)
	}
	// One element past its own length.
	assertArrayError(t, stringArray("abcde").Validate(limits), CodeBSTRTooLong)
	// Every element inside its own length, past the total.
	assertArrayError(t, stringArray("abcd", "abcd", "a").Validate(limits), CodeBSTRTooLong)
	// Invalid UTF-8 in an element is refused with the element named.
	err := stringArray("ok", string([]byte{0x80})).Validate(limits)
	assertArrayError(t, err, CodeInvalidValue)
	if !strings.Contains(err.Error(), "element 1") {
		t.Errorf("a bad element was reported as %v, which does not say which one", err)
	}
}

// A Write declares a VARTYPE and supplies a value, and the two have to be the
// same kind of thing. An array VARTYPE carrying a scalar is not an adapter
// limitation but a request that contradicts itself.
func TestAWrittenArrayMustMatchItsDeclaredVarType(t *testing.T) {
	limits := testArrayBounds()
	array := int32Array([]DADimension{{Length: 2}}, 1, 2)

	if err := validateWriteValue(VTI4|VTArray, array, limits); err != nil {
		t.Fatalf("an array matching its declared VARTYPE was refused: %v", err)
	}
	assertArrayError(t, validateWriteValue(VTI4|VTArray, int32(1), limits), CodeInvalidValue)
	assertArrayError(t, validateWriteValue(VTI2|VTArray, array, limits), CodeTypeMismatch)
	assertArrayError(t, validateWriteValue(VTI4, array, limits), CodeInvalidValue)
	// byref stays unsupported, and says so rather than borrowing the array
	// path's reason.
	assertArrayError(t, validateWriteValue(VTI4|VTByRef, int32(1), limits), CodeUnsupportedVarType)
	assertArrayError(t, validateWriteValue(VTI4|VTArray|VTByRef, array, limits), CodeUnsupportedVarType)
}

// The element count is a product, and a product of four large dimensions
// overflows a 64-bit count to zero -- which would agree with an array that
// carries nothing. It is refused by the bound instead.
func TestAnElementCountCannotWrapPastItsBound(t *testing.T) {
	array := DAArray{
		ElementType: VTI4,
		Dimensions: []DADimension{
			{Length: 65536}, {Length: 65536}, {Length: 65536}, {Length: 65536},
		},
	}
	count, err := array.ElementCount(1024)
	if err == nil {
		t.Fatalf("four dimensions of 65536 counted to %d", count)
	}
	assertArrayError(t, err, CodeArrayTooLarge)
}

func assertArrayError(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	adapterErr, ok := AsAdapterError(err)
	if !ok || adapterErr.Code != want {
		t.Fatalf("error = %v, want %s", err, want)
	}
}

// The array checks are given bounds by their caller, and a bound that is not
// positive is a caller that has not been configured rather than an array that
// is wrong. Each of the four is checked on its own, because one disjunction
// covering all of them would pass a test that set several at once while any
// single arm could still be removed.
func TestArrayBoundsMustThemselvesBePositive(t *testing.T) {
	valid := int32Array([]DADimension{{Length: 1}}, 1)
	if err := valid.Validate(testArrayBounds()); err != nil {
		t.Fatalf("a valid array under valid bounds was refused: %v", err)
	}

	for _, testCase := range []struct {
		name   string
		break_ func(*ArrayLimits)
	}{
		{"no elements allowed", func(l *ArrayLimits) { l.MaxElements = 0 }},
		{"no dimensions allowed", func(l *ArrayLimits) { l.MaxDimensions = 0 }},
		{"no string length allowed", func(l *ArrayLimits) { l.MaxBSTRCodeUnits = 0 }},
		{"no array string budget", func(l *ArrayLimits) { l.MaxArrayBSTRCodeUnits = 0 }},
		{"a negative element bound", func(l *ArrayLimits) { l.MaxElements = -1 }},
		{"a negative dimension bound", func(l *ArrayLimits) { l.MaxDimensions = -1 }},
		{"a negative string length", func(l *ArrayLimits) { l.MaxBSTRCodeUnits = -1 }},
		{"a negative array string budget", func(l *ArrayLimits) { l.MaxArrayBSTRCodeUnits = -1 }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			limits := testArrayBounds()
			testCase.break_(&limits)
			err := valid.Validate(limits)
			if err == nil {
				t.Fatal("an array was validated against a bound that is not positive")
			}
			// The reason is the whole of it. A bound that is not positive is a
			// configuration that never took, and reporting it as an array that
			// is too large sends whoever reads the message to the array.
			adapterErr, ok := AsAdapterError(err)
			if !ok || adapterErr.Code != CodeInvalidValue {
				t.Errorf("refused as %v, which blames the array rather than the bound", err)
			}
		})
	}
}

// ElementCount takes the bound it counts against, and the same rule applies:
// counting against nothing would report every array as too large or none of
// them, depending on which way the comparison fell.
func TestAnElementCountNeedsAPositiveBound(t *testing.T) {
	array := int32Array([]DADimension{{Length: 2}}, 1, 2)
	if count, err := array.ElementCount(2); err != nil || count != 2 {
		t.Fatalf("ElementCount(2) = %d, %v", count, err)
	}
	for _, maximum := range []int{0, -1} {
		if _, err := array.ElementCount(maximum); err == nil {
			t.Errorf("ElementCount(%d) was answered rather than refused", maximum)
		}
		// An array with no dimensions never enters the loop, so the guard is
		// the only thing that can refuse it: without one, counting against
		// nothing answers "one element" for an array that describes none.
		empty := DAArray{ElementType: VTI4}
		if count, err := empty.ElementCount(maximum); err == nil {
			t.Errorf("ElementCount(%d) on a dimensionless array answered %d", maximum, count)
		}
	}
}

// utf16CodeUnits counts what utf16.Encode would produce without producing it,
// so the two must agree for every shape of text the adapter carries -- an
// undercount would let a BSTR past its bound, and an overcount would refuse one
// inside it.
func TestTheCodeUnitCountAgreesWithEncoding(t *testing.T) {
	for _, value := range []string{
		"",
		"a",
		"Channel1.Device1.Tag0001",
		// Korean, which is three UTF-8 bytes and one UTF-16 unit per character.
		"온도",
		// Outside the basic plane: one rune, two code units. This is the case
		// a rune count alone would get wrong.
		"😀",
		"a😀b",
		strings.Repeat("😀", 64),
		// Mixed widths in one string, so no single rule about the whole of it
		// could be right by accident.
		"A온도😀z",
	} {
		t.Run(value, func(t *testing.T) {
			want := len(utf16.Encode([]rune(value)))
			if got := utf16CodeUnits(value); got != want {
				t.Errorf("utf16CodeUnits(%q) = %d, want %d", value, got, want)
			}
		})
	}
}
