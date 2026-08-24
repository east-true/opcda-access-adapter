package opcdav1

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestProtocolUsesDANativeVocabularyAndUnaryPhaseSixSurface(t *testing.T) {
	services := File_api_opcda_v1_opcda_access_proto.Services()
	if services.Len() != 1 {
		t.Fatalf("services = %d", services.Len())
	}
	service := services.Get(0)
	if string(service.Name()) != "OPCDAAccess" {
		t.Fatalf("service = %s", service.Name())
	}
	wantMethods := []string{"Status", "Browse", "Read", "Write"}
	if service.Methods().Len() != len(wantMethods) {
		t.Fatalf("methods = %d", service.Methods().Len())
	}
	for index, want := range wantMethods {
		method := service.Methods().Get(index)
		if string(method.Name()) != want || method.IsStreamingClient() || method.IsStreamingServer() {
			t.Fatalf("method %d = %s streaming=%t/%t", index, method.Name(), method.IsStreamingClient(), method.IsStreamingServer())
		}
	}

	forbidden := []string{"asset", "metric", "telemetry", "normalized", "signal", "point", "device"}
	messages := File_api_opcda_v1_opcda_access_proto.Messages()
	for index := 0; index < messages.Len(); index++ {
		name := strings.ToLower(string(messages.Get(index).Name()))
		for _, word := range forbidden {
			if strings.Contains(name, word) {
				t.Fatalf("non-DA model vocabulary %q in message %q", word, name)
			}
		}
	}
}

func TestProtocolCarriesRawDAIdentityAndSemantics(t *testing.T) {
	read := (&DAReadResult{}).ProtoReflect().Descriptor().Fields()
	for _, name := range []string{"item_id", "data_type", "canonical_data_type", "quality_raw", "timestamp_present", "hresult", "access_rights"} {
		if read.ByName(protoreflectName(name)) == nil {
			t.Fatalf("DAReadResult missing %s", name)
		}
	}
	hresult := (&DAHRESULT{}).ProtoReflect().Descriptor().Fields()
	for _, name := range []string{"value", "raw", "hex"} {
		if hresult.ByName(protoreflectName(name)) == nil {
			t.Fatalf("DAHRESULT missing %s", name)
		}
	}
}

func protoreflectName(value string) protoreflect.Name { return protoreflect.Name(value) }
