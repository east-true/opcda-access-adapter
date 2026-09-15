package opcua

import (
	"math"
	"strings"
	"testing"
)

// This is the third limit validator in the repository, after the DA runtime's
// Limits and Config, and the sweep found it in the same state both of those
// were: every comparison loosened by one survived, and so did splitting the ||
// chains, because no case had ever moved one field on its own.
//
// What it guards is narrower than the other two and harder to reason about
// from the outside. A UA length field is an Int32, so a bound above math.MaxInt32
// cannot be expressed on the wire at all; a string bound above the message bound
// promises a string that could never arrive in one; and the nesting depth has a
// floor rather than only a ceiling, because a decoder that gives up too shallow
// refuses structures the specification requires it to accept.

type binaryLimitField struct {
	name string
	set  func(*BinaryLimits, int)
}

var binaryLimitFields = []binaryLimitField{
	{"MaxMessageBytes", func(l *BinaryLimits, v int) { l.MaxMessageBytes = v }},
	{"MaxStringBytes", func(l *BinaryLimits, v int) { l.MaxStringBytes = v }},
	{"MaxByteStringBytes", func(l *BinaryLimits, v int) { l.MaxByteStringBytes = v }},
	{"MaxArrayLength", func(l *BinaryLimits, v int) { l.MaxArrayLength = v }},
	{"MaxNestingDepth", func(l *BinaryLimits, v int) { l.MaxNestingDepth = v }},
}

func TestEveryBinaryLimitMustBePositive(t *testing.T) {
	if err := DefaultBinaryLimits().validate(); err != nil {
		t.Fatalf("the shipped defaults do not validate: %v", err)
	}
	for _, field := range binaryLimitFields {
		t.Run(field.name, func(t *testing.T) {
			// One field at a time, which is what separates the clauses. The
			// reason is checked too: MaxNestingDepth at zero is also below its
			// floor, so a case that only asks whether it was refused cannot
			// tell that the positivity clause did any work.
			for _, value := range []int{0, -1} {
				limits := DefaultBinaryLimits()
				field.set(&limits, value)
				err := limits.validate()
				if err == nil {
					t.Fatalf("%s = %d was accepted", field.name, value)
				}
				if !strings.Contains(err.Error(), "must be positive") {
					t.Errorf("%s = %d was refused as %q, not for being non-positive",
						field.name, value, err)
				}
			}
		})
	}
}

// The nesting depth is the one bound with a floor. OPC 10000-6 requires a
// decoder to accept structures nested to a given depth, so a configuration
// below it would refuse messages the specification says are legal.
func TestNestingDepthHasAFloorAsWellAsACeiling(t *testing.T) {
	limits := DefaultBinaryLimits()
	limits.MaxNestingDepth = minimumNestingDepth
	if err := limits.validate(); err != nil {
		t.Errorf("a nesting depth of exactly the minimum was refused: %v", err)
	}
	limits.MaxNestingDepth = minimumNestingDepth - 1
	if err := limits.validate(); err == nil {
		t.Errorf("a nesting depth of %d, one below the minimum, was accepted",
			minimumNestingDepth-1)
	}
}

// A length field is an Int32. A bound above what one can express describes a
// message that could never be encoded, so the ceiling is the width of the
// field rather than an arbitrary choice.
func TestNoBinaryLimitMayExceedAnInt32LengthField(t *testing.T) {
	for _, field := range binaryLimitFields {
		if field.name == "MaxNestingDepth" {
			continue // bounded by its own floor and the recursion guard, not by a length field
		}
		t.Run(field.name, func(t *testing.T) {
			limits := DefaultBinaryLimits()
			// The message bound has to rise with the others, or the
			// string-versus-message rule refuses the configuration first and
			// this case would be testing that instead.
			limits.MaxMessageBytes = math.MaxInt32
			field.set(&limits, math.MaxInt32)
			if err := limits.validate(); err != nil {
				t.Errorf("%s at exactly math.MaxInt32 was refused: %v", field.name, err)
			}

			limits = DefaultBinaryLimits()
			limits.MaxMessageBytes = math.MaxInt32
			field.set(&limits, math.MaxInt32+1)
			err := limits.validate()
			if err == nil {
				t.Fatalf("%s one past math.MaxInt32 was accepted", field.name)
			}
			if !strings.Contains(err.Error(), "Int32") {
				t.Errorf("%s one past math.MaxInt32 was refused as %q, not for the length field",
					field.name, err)
			}
		})
	}
}

// A string bound above the message bound promises a string that could never
// arrive inside one message.
func TestAStringBoundMayEqualButNotExceedTheMessageBound(t *testing.T) {
	for _, field := range []binaryLimitField{
		{"MaxStringBytes", func(l *BinaryLimits, v int) { l.MaxStringBytes = v }},
		{"MaxByteStringBytes", func(l *BinaryLimits, v int) { l.MaxByteStringBytes = v }},
	} {
		t.Run(field.name, func(t *testing.T) {
			limits := DefaultBinaryLimits()
			field.set(&limits, limits.MaxMessageBytes)
			if err := limits.validate(); err != nil {
				t.Errorf("%s equal to the message bound was refused: %v", field.name, err)
			}
			limits = DefaultBinaryLimits()
			field.set(&limits, limits.MaxMessageBytes+1)
			if err := limits.validate(); err == nil {
				t.Errorf("%s one byte past the message bound was accepted", field.name)
			}
		})
	}
}

// A body of no bytes at all is a legal message: several UA-TCP messages carry
// nothing but their header. Only a negative size is a programming error.
func TestAHeaderOnlyMessageIsEncodable(t *testing.T) {
	encoded, err := EncodeMessageHeader(MessageType{'M', 'S', 'G'}, 'F', 0, 8192)
	if err != nil {
		t.Fatalf("a zero-length body was refused: %v", err)
	}
	if len(encoded) != HeaderSize {
		t.Errorf("a header-only message is %d bytes, want %d", len(encoded), HeaderSize)
	}
	if _, err := EncodeMessageHeader(MessageType{'M', 'S', 'G'}, 'F', -1, 8192); err == nil {
		t.Error("a negative body size was accepted")
	}
}
