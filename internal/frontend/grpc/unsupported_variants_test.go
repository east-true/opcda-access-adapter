package grpcfrontend

import (
	"context"
	"testing"

	opcdav1 "github.com/east-true/opcda-access-adapter/api/opcda/v1"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// INV-10 says an unsupported VARTYPE fails explicitly rather than being coerced
// into something that looks like it worked, and the gRPC reference repeats it:
// "Unsupported scalar types, SAFEARRAY, and BYREF values fail explicitly and
// are never coerced." Both directions of the wire enforce it -- encodeScalar on
// the way out, decodeWriteVarType on the way in.
//
// Each guard is `varType.IsArray() || varType.IsByRef()`, and the mutation
// sweep found both removable. Turning the || into && leaves only a VARTYPE that
// is array *and* byref refused, so VT_ARRAY|VT_I4 on its own -- a plain
// SAFEARRAY, the commonest of the two -- would have been accepted. Nothing
// failed, because no test had ever offered one flag without the other.

func variantFlags() []struct {
	name    string
	varType opcda.DAVarType
} {
	return []struct {
		name    string
		varType opcda.DAVarType
	}{
		{"an array", opcda.VTArray | opcda.VTI4},
		{"a byref", opcda.VTByRef | opcda.VTI4},
		{"an array of byrefs", opcda.VTArray | opcda.VTByRef | opcda.VTI4},
	}
}

func TestReadingRefusesEveryArrayAndByrefVariant(t *testing.T) {
	// The plain scalar is the control: without it a guard that refused
	// everything would pass every case below.
	if _, err := encodeScalar(opcda.VTI4, int32(1)); err != nil {
		t.Fatalf("a plain scalar was refused, so the cases below prove nothing: %v", err)
	}
	for _, flagged := range variantFlags() {
		t.Run(flagged.name, func(t *testing.T) {
			if _, err := encodeScalar(flagged.varType, int32(1)); err == nil {
				t.Errorf("%s VARTYPE (0x%04X) was encoded", flagged.name, uint16(flagged.varType))
			}
		})
	}
}

func TestWritingRefusesEveryArrayAndByrefVariant(t *testing.T) {
	plain := &opcdav1.DAVarType{Raw: uint32(opcda.VTI4), Name: opcda.VTI4.String()}
	if _, err := decodeWriteVarType(plain); err != nil {
		t.Fatalf("a plain scalar VARTYPE was refused, so the cases below prove nothing: %v", err)
	}
	for _, flagged := range variantFlags() {
		t.Run(flagged.name, func(t *testing.T) {
			declared := &opcdav1.DAVarType{
				Raw:   uint32(flagged.varType),
				Array: flagged.varType.IsArray(),
				Byref: flagged.varType.IsByRef(),
			}
			if _, err := decodeWriteVarType(declared); err == nil {
				t.Errorf("%s VARTYPE (0x%04X) was accepted for a Write",
					flagged.name, uint16(flagged.varType))
			}
		})
	}
}

// A Write item has three things that must all be present, and the check is one
// disjunction, so each absence needs its own case or the || is removable.
func TestAWriteItemMustCarryAllThreeOfItsParts(t *testing.T) {
	complete := func() *opcdav1.DAWriteItem {
		return &opcdav1.DAWriteItem{
			ItemId:   "Test/Int32",
			DataType: &opcdav1.DAVarType{Raw: uint32(opcda.VTI4), Name: opcda.VTI4.String()},
			Value:    &opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_I4Value{I4Value: 1}},
		}
	}
	runtime := &testRuntime{status: opcda.RuntimeStatus{WriteEnabled: true},
		write: func(_ context.Context, items []opcda.WriteItem) ([]opcda.WriteResult, error) {
			results := make([]opcda.WriteResult, len(items))
			for i, item := range items {
				results[i] = opcda.WriteResult{ItemID: item.ItemID, HRESULT: opcda.SOK, HRESULTPresent: true}
			}
			return results, nil
		}}
	server := New(runtime, Config{MaxWriteItems: 4, MaxItemIDBytes: 64})

	write := func(item *opcdav1.DAWriteItem) error {
		_, err := server.Write(context.Background(), &opcdav1.DAWriteRequest{
			Items: []*opcdav1.DAWriteItem{item},
		})
		return err
	}
	if err := write(complete()); err != nil {
		t.Fatalf("a complete Write item was refused, so the cases below prove nothing: %v", err)
	}

	for _, testCase := range []struct {
		missing string
		item    *opcdav1.DAWriteItem
	}{
		{"the item itself", nil},
		{"its declared type", func() *opcdav1.DAWriteItem {
			item := complete()
			item.DataType = nil
			return item
		}()},
		{"its value", func() *opcdav1.DAWriteItem {
			item := complete()
			item.Value = nil
			return item
		}()},
	} {
		t.Run(testCase.missing, func(t *testing.T) {
			if err := write(testCase.item); err == nil {
				t.Errorf("a Write item missing %s was accepted", testCase.missing)
			}
		})
	}
}

// Strings crossing this frontend have to be valid UTF-8 in both directions,
// and each check pairs that with a type assertion. Turning the || into &&
// leaves only a value that is both the wrong type and invalid UTF-8 refused,
// so a well-typed string carrying invalid bytes would have passed -- and
// invalid UTF-8 is exactly what a peer sends when it is probing.
func TestStringsMustBeValidUTF8InBothDirections(t *testing.T) {
	// A lone continuation byte: well-formed as a Go string, not valid UTF-8.
	invalidUTF8 := string([]byte{0x80})

	t.Run("reading a BSTR from the source", func(t *testing.T) {
		if _, err := encodeScalar(opcda.VTBSTR, "ok"); err != nil {
			t.Fatalf("a valid BSTR was refused, so the cases below prove nothing: %v", err)
		}
		if _, err := encodeScalar(opcda.VTBSTR, invalidUTF8); err == nil {
			t.Error("a BSTR carrying invalid UTF-8 was encoded")
		}
		if _, err := encodeScalar(opcda.VTBSTR, int32(1)); err == nil {
			t.Error("a non-string value was encoded as a BSTR")
		}
	})

	t.Run("writing a BSTR from a client", func(t *testing.T) {
		bstr := func(v string) *opcdav1.DAScalarValue {
			return &opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_BstrValue{BstrValue: v}}
		}
		if _, err := decodeWriteValue(opcda.VTBSTR, bstr("ok")); err != nil {
			t.Fatalf("a valid BSTR was refused, so the cases below prove nothing: %v", err)
		}
		if _, err := decodeWriteValue(opcda.VTBSTR, bstr(invalidUTF8)); err == nil {
			t.Error("a BSTR carrying invalid UTF-8 was accepted for a Write")
		}
		wrongField := &opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_I4Value{I4Value: 1}}
		if _, err := decodeWriteValue(opcda.VTBSTR, wrongField); err == nil {
			t.Error("a value in another type's field was accepted as a BSTR")
		}
	})

	t.Run("an empty or null value", func(t *testing.T) {
		set := &opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_EmptyOrNull{EmptyOrNull: true}}
		if _, err := decodeWriteValue(opcda.VTEmpty, set); err != nil {
			t.Fatalf("an EmptyOrNull of true was refused: %v", err)
		}
		// The field carries a bool, and false does not mean "empty": it means
		// the client filled the field in without saying anything.
		unset := &opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_EmptyOrNull{EmptyOrNull: false}}
		if _, err := decodeWriteValue(opcda.VTEmpty, unset); err == nil {
			t.Error("an EmptyOrNull of false was accepted")
		}
		wrongField := &opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_I4Value{I4Value: 0}}
		if _, err := decodeWriteValue(opcda.VTEmpty, wrongField); err == nil {
			t.Error("a value in another type's field was accepted as empty")
		}
	})
}

// A Browse entry's name comes from the source, and the frontend refuses to
// relay one that is empty or not valid UTF-8. Two conditions, one disjunction,
// so each needs its own entry.
func TestABrowseEntryNameMustBeNonEmptyAndValidUTF8(t *testing.T) {
	browseWith := func(name string) error {
		runtime := &testRuntime{browse: func(_ context.Context, request opcda.BrowseRequest) (opcda.BrowseResult, error) {
			itemID := opcda.DAItemID("Test/Item")
			return opcda.BrowseResult{Path: request.Path, Entries: []opcda.BrowseEntry{
				{Name: name, Kind: opcda.BrowseEntryItem, ItemID: &itemID},
			}}, nil
		}}
		server := New(runtime, Config{MaxBrowseDepth: 4, MaxBrowseEntries: 8, MaxItemIDBytes: 64})
		_, err := server.Browse(context.Background(), &opcdav1.DABrowseRequest{})
		return err
	}

	if err := browseWith("Item"); err != nil {
		t.Fatalf("a valid entry name was refused, so the cases below prove nothing: %v", err)
	}
	if err := browseWith(""); err == nil {
		t.Error("an empty Browse entry name was relayed")
	}
	if err := browseWith(string([]byte{0x80})); err == nil {
		t.Error("a Browse entry name carrying invalid UTF-8 was relayed")
	}
}
