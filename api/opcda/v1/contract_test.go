package opcdav1

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestProtocolUsesDANativeVocabularyAndPhaseSevenSurface(t *testing.T) {
	services := File_api_opcda_v1_opcda_access_proto.Services()
	if services.Len() != 1 {
		t.Fatalf("services = %d", services.Len())
	}
	service := services.Get(0)
	if string(service.Name()) != "OPCDAAccess" {
		t.Fatalf("service = %s", service.Name())
	}
	// Subscribe is the only stream, and it is server-streaming only: the client
	// never pushes into an open subscription.
	wantMethods := []struct {
		name            string
		streamingServer bool
	}{
		{"Status", false},
		{"Browse", false},
		{"Read", false},
		{"Write", false},
		{"Subscribe", true},
		// OPC DA item properties, which OPC 10000-8 Table A.1 is mapped from.
		// Two calls because they are two questions: what does this source
		// offer for this item, and what are those properties' values.
		{"AvailableItemProperties", false},
		{"ItemProperties", false},
	}
	if service.Methods().Len() != len(wantMethods) {
		t.Fatalf("methods = %d", service.Methods().Len())
	}
	for index, want := range wantMethods {
		method := service.Methods().Get(index)
		if string(method.Name()) != want.name {
			t.Fatalf("method %d = %s, want %s", index, method.Name(), want.name)
		}
		if method.IsStreamingClient() {
			t.Fatalf("method %s is client-streaming", method.Name())
		}
		if method.IsStreamingServer() != want.streamingServer {
			t.Fatalf("method %s server-streaming = %t, want %t", method.Name(), method.IsStreamingServer(), want.streamingServer)
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

	// A subscription reports the server's revised rate, not just the request.
	created := (&DASubscriptionCreated{}).ProtoReflect().Descriptor().Fields()
	for _, name := range []string{"subscription_id", "connection_generation", "requested_update_rate_ms", "revised_update_rate_ms", "percent_deadband", "active_item_count"} {
		if created.ByName(protoreflectName(name)) == nil {
			t.Fatalf("DASubscriptionCreated missing %s", name)
		}
	}
	item := (&DASubscriptionItemStatus{}).ProtoReflect().Descriptor().Fields()
	for _, name := range []string{"item_id", "active", "canonical_data_type", "access_rights", "hresult", "error_code"} {
		if item.ByName(protoreflectName(name)) == nil {
			t.Fatalf("DASubscriptionItemStatus missing %s", name)
		}
	}
	// Notification values reuse DAReadResult so a subscribed value and a device
	// Read value cannot drift apart.
	values := (&DASubscriptionUpdate{}).ProtoReflect().Descriptor().Fields().ByName(protoreflectName("values"))
	if values == nil || values.Message() == nil || values.Message().FullName() != (&DAReadResult{}).ProtoReflect().Descriptor().FullName() {
		t.Fatal("DASubscriptionUpdate values do not reuse DAReadResult")
	}
}

func protoreflectName(value string) protoreflect.Name { return protoreflect.Name(value) }
