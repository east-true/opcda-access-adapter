package opcda

import (
	"fmt"
	"testing"
)

// Every array a client writes is validated against its own description and the
// configured bounds before it reaches the source, so this cost is paid once per
// written array on the runtime's owning thread -- the one place in the adapter
// where time spent is time no other request can use.
//
// The shape check is separated from the full validation because a frontend runs
// only the first: what a client got wrong and what an operator configured are
// different questions, answered in different places.

func benchmarkArray(elements int) DAArray {
	values := make([]any, elements)
	for index := range values {
		values[index] = float64(index) + 0.5
	}
	return DAArray{
		ElementType: VTR8,
		Dimensions:  []DADimension{{Length: uint32(elements)}},
		Elements:    values,
	}
}

func BenchmarkArrayValidateByElementCount(b *testing.B) {
	limits := DefaultLimits().ArrayLimits()
	for _, elements := range []int{1, 64, 1024} {
		b.Run(fmt.Sprintf("elements=%d", elements), func(b *testing.B) {
			array := benchmarkArray(elements)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := array.Validate(limits); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// A string array is the expensive one: every element is measured in UTF-16
// code units, which is a pass over the string rather than a type assertion.
func BenchmarkArrayValidateStringsByElementCount(b *testing.B) {
	limits := DefaultLimits().ArrayLimits()
	for _, elements := range []int{1, 64, 1024} {
		b.Run(fmt.Sprintf("elements=%d", elements), func(b *testing.B) {
			values := make([]any, elements)
			for index := range values {
				values[index] = fmt.Sprintf("Channel1.Device1.Tag%04d", index)
			}
			array := DAArray{
				ElementType: VTBSTR,
				Dimensions:  []DADimension{{Length: uint32(elements)}},
				Elements:    values,
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := array.Validate(limits); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkArrayShapeByElementCount(b *testing.B) {
	for _, elements := range []int{1, 64, 1024} {
		b.Run(fmt.Sprintf("elements=%d", elements), func(b *testing.B) {
			array := benchmarkArray(elements)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := array.MatchesItsShape(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
