package opcua

import (
	"encoding/binary"
	"testing"
)

// readSoftwareCertificates decodes the certificate array an ActivateSession
// request carries, so what it reads is whatever a client sent. The sweep found
// its first line unpinned: `if err != nil || isNull` survived becoming `&&`,
// which means neither the error path nor the null path had a test.
//
// The null path is the interesting half. A null array and an empty array are
// different values on the wire -- the mapping document says so under "Null is
// not empty" -- and with `&&` a null array falls through to the loop and comes
// back as an allocated empty slice instead of nothing at all. The request then
// says the client sent zero certificates where it actually sent none, which is
// a different statement.

func arrayLength(length int32) []byte {
	encoded := make([]byte, 4)
	binary.LittleEndian.PutUint32(encoded, uint32(length))
	return encoded
}

func byteStringField(payload []byte) []byte {
	return append(arrayLength(int32(len(payload))), payload...)
}

func TestACertificateArrayDistinguishesNullFromEmpty(t *testing.T) {
	limits := DefaultBinaryLimits()

	t.Run("a null array is nothing at all", func(t *testing.T) {
		decoder, err := NewDecoder(arrayLength(-1), limits)
		if err != nil {
			t.Fatal(err)
		}
		values, err := decoder.readSoftwareCertificates()
		if err != nil {
			t.Fatalf("a null certificate array was refused: %v", err)
		}
		if values != nil {
			t.Errorf("a null array decoded as %#v, want nothing; null and empty "+
				"are different values on the wire", values)
		}
	})

	t.Run("an empty array is an array of none", func(t *testing.T) {
		decoder, err := NewDecoder(arrayLength(0), limits)
		if err != nil {
			t.Fatal(err)
		}
		values, err := decoder.readSoftwareCertificates()
		if err != nil {
			t.Fatalf("an empty certificate array was refused: %v", err)
		}
		if values == nil || len(values) != 0 {
			t.Errorf("an empty array decoded as %#v, want an allocated array of none", values)
		}
	})
}

func TestACertificateArrayRefusesWhatItCannotDecode(t *testing.T) {
	limits := DefaultBinaryLimits()

	for _, testCase := range []struct {
		name string
		data []byte
	}{
		{
			// Each entry needs at least two length prefixes, so a count this
			// large cannot be backed by the bytes that follow.
			name: "more entries than the remaining bytes could hold",
			data: arrayLength(1000),
		},
		{
			name: "an entry whose certificate data is truncated",
			data: append(arrayLength(1), arrayLength(64)...),
		},
		{
			// The data decodes and the signature's length prefix is missing.
			name: "an entry whose signature is missing",
			data: append(arrayLength(1), byteStringField([]byte("cert"))...),
		},
		{
			name: "a negative length that is not the null sentinel",
			data: arrayLength(-2),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			decoder, err := NewDecoder(testCase.data, limits)
			if err != nil {
				t.Fatal(err)
			}
			if values, err := decoder.readSoftwareCertificates(); err == nil {
				t.Errorf("%s was accepted, decoding %d entries", testCase.name, len(values))
			}
		})
	}
}

// A well-formed array decodes, and a null field inside an entry stays empty
// rather than becoming the bytes of the next field.
func TestACertificateArrayDecodesItsEntries(t *testing.T) {
	data := arrayLength(2)
	data = append(data, byteStringField([]byte("first-cert"))...)
	data = append(data, byteStringField([]byte("first-signature"))...)
	data = append(data, byteStringField([]byte("second-cert"))...)
	data = append(data, arrayLength(-1)...) // a null signature

	decoder, err := NewDecoder(data, DefaultBinaryLimits())
	if err != nil {
		t.Fatal(err)
	}
	values, err := decoder.readSoftwareCertificates()
	if err != nil {
		t.Fatalf("a well-formed certificate array was refused: %v", err)
	}
	if len(values) != 2 {
		t.Fatalf("decoded %d entries, want 2", len(values))
	}
	if string(values[0].CertificateData) != "first-cert" ||
		string(values[0].Signature) != "first-signature" {
		t.Errorf("the first entry decoded as %+v", values[0])
	}
	if string(values[1].CertificateData) != "second-cert" {
		t.Errorf("the second entry's data decoded as %q", values[1].CertificateData)
	}
	if values[1].Signature != nil {
		t.Errorf("a null signature decoded as %#v, want nothing", values[1].Signature)
	}
}
