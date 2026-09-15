package opcua

import (
	"encoding/binary"
	"strings"
	"testing"
)

// readLength is the guard every variable-length field on the wire passes
// through -- strings, byte strings and arrays all reach it -- and the mutation
// sweep found its ceiling unpinned. Loosening `raw > bound` to `>=` survived,
// which means nothing said a field of exactly the configured length decodes.
//
// Tightened by one, the server refuses a message the limits it published say
// it accepts, and a client has no way to tell that from its own bug. Loosened
// by one it admits a field past the bound, which is the allocation the bound
// exists to cap.

func lengthPrefixed(length int32, payload []byte) []byte {
	encoded := make([]byte, 4, 4+len(payload))
	binary.LittleEndian.PutUint32(encoded, uint32(length))
	return append(encoded, payload...)
}

func TestAFieldOfExactlyTheLengthLimitDecodes(t *testing.T) {
	const bound = 16
	limits := DefaultBinaryLimits()
	limits.MaxStringBytes = bound
	limits.MaxByteStringBytes = bound
	limits.MaxArrayLength = bound

	t.Run("String", func(t *testing.T) {
		atLimit := lengthPrefixed(bound, []byte(strings.Repeat("s", bound)))
		decoder, err := NewDecoder(atLimit, limits)
		if err != nil {
			t.Fatal(err)
		}
		value, isNull, err := decoder.ReadString()
		if err != nil {
			t.Fatalf("a string of exactly %d bytes was refused: %v", bound, err)
		}
		if isNull || len(value) != bound {
			t.Fatalf("decoded %q (null=%v), want %d bytes", value, isNull, bound)
		}

		overLimit := lengthPrefixed(bound+1, []byte(strings.Repeat("s", bound+1)))
		decoder, err = NewDecoder(overLimit, limits)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := decoder.ReadString(); err == nil {
			t.Fatalf("a string of %d bytes was accepted", bound+1)
		}
	})

	t.Run("ByteString", func(t *testing.T) {
		atLimit := lengthPrefixed(bound, make([]byte, bound))
		decoder, err := NewDecoder(atLimit, limits)
		if err != nil {
			t.Fatal(err)
		}
		value, isNull, err := decoder.ReadByteString()
		if err != nil {
			t.Fatalf("a byte string of exactly %d bytes was refused: %v", bound, err)
		}
		if isNull || len(value) != bound {
			t.Fatalf("decoded %d bytes (null=%v), want %d", len(value), isNull, bound)
		}

		overLimit := lengthPrefixed(bound+1, make([]byte, bound+1))
		decoder, err = NewDecoder(overLimit, limits)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := decoder.ReadByteString(); err == nil {
			t.Fatalf("a byte string of %d bytes was accepted", bound+1)
		}
	})

	// A null field is length -1 and is not a length at all, so the bound does
	// not apply to it. Without this the negative branch and the bound branch
	// could be confused for one another.
	t.Run("a null field is not bounded", func(t *testing.T) {
		decoder, err := NewDecoder(lengthPrefixed(-1, nil), limits)
		if err != nil {
			t.Fatal(err)
		}
		_, isNull, err := decoder.ReadString()
		if err != nil || !isNull {
			t.Fatalf("a null string decoded as (null=%v, err=%v)", isNull, err)
		}
		// Any other negative length is malformed rather than null.
		decoder, err = NewDecoder(lengthPrefixed(-2, nil), limits)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := decoder.ReadString(); err == nil {
			t.Error("a length of -2 was accepted")
		}
	})
}

// A property node identifier is parsed from the wire, so both halves of its
// separator check have to hold: a node that carries the separator but no ItemID
// after it names nothing, and `&&` in place of the `||` would have accepted it.
func TestAPropertyNodeIdentifierNeedsBothHalves(t *testing.T) {
	// The shape a real one has, taken from the constructor rather than spelled
	// out here, so this cannot drift from what the address space builds.
	valid := ItemPropertyNodeID("Test/Float", "EngineeringUnits")
	if _, _, ok := ItemPropertyForNode(valid); !ok {
		t.Fatalf("a well-formed property node was rejected, so the cases below prove nothing: %v", valid)
	}

	for _, testCase := range []struct {
		name     string
		stringID string
	}{
		{"no separator at all", "property:EngineeringUnits"},
		{"a separator with nothing after it", "property:EngineeringUnits" + propertyNodeSeparator},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			id := NodeID{
				Namespace: AdapterNamespaceIndex,
				Type:      NodeIDTypeString,
				StringID:  testCase.stringID,
			}
			if itemID, _, ok := ItemPropertyForNode(id); ok {
				t.Errorf("%q was accepted, naming item %q", testCase.stringID, itemID)
			}
		})
	}
}

// PathForNode compares a prefix by length before slicing, and the boundary is
// an identifier exactly as long as the prefix: it is the prefix and nothing
// else, which names no path.
//
// The length comparison itself is an equivalent mutant, checked rather than
// assumed: tightening `>=` to `>` rejects a bare prefix at the length test
// instead of a few lines later, where the sliced remainder is empty and
// rejected anyway. Both answers are the same, so no test can separate them.
// What is below pins the behaviour a caller sees, which is worth having even
// though it kills nothing.
func TestABranchIdentifierMustCarryMoreThanItsPrefix(t *testing.T) {
	const prefix = "branch:"
	bare := NodeID{Namespace: AdapterNamespaceIndex, Type: NodeIDTypeString, StringID: prefix}
	if path, ok := PathForNode(bare); ok {
		t.Errorf("an identifier that is only the prefix named the path %v", path)
	}
	shorter := NodeID{Namespace: AdapterNamespaceIndex, Type: NodeIDTypeString, StringID: prefix[:len(prefix)-1]}
	if _, ok := PathForNode(shorter); ok {
		t.Error("an identifier shorter than the prefix was accepted")
	}
	// One character more is a path.
	named := NodeID{Namespace: AdapterNamespaceIndex, Type: NodeIDTypeString, StringID: prefix + "A"}
	if _, ok := PathForNode(named); !ok {
		t.Error("an identifier one character past the prefix named no path")
	}
}
