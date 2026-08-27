package app

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
	"github.com/east-true/opcda-access-adapter/internal/opcua"
)

func testOPCUAEndpoint() OPCUAFrontendConfig {
	return OPCUAFrontendConfig{
		EndpointURL:         "opc.tcp://127.0.0.1:4840",
		ApplicationURI:      "urn:example:opcda-access-adapter",
		ProductURI:          "urn:example:opcda-access-adapter",
		ApplicationName:     "OPC DA Access Adapter",
		SecurityPolicyURI:   "urn:test:security-policy:none",
		TransportProfileURI: "urn:test:transport:uatcp-uasc-uabinary",
		NamespaceURI:        "urn:example:opcda-access-adapter",
		SourceFolderName:    "Source",
	}
}

// The security policy and transport profile URIs identify a deployment and are
// defined by OPC 10000-7, so the adapter requires them rather than inventing
// them.
func TestOPCUAConfigRequiresItsIdentifiers(t *testing.T) {
	source := opcda.SourceConfig{CLSID: "{00000000-0000-0000-0000-000000000001}"}
	if _, err := GuidedSetupOPCUAConfig(source, "127.0.0.1:4840", testOPCUAEndpoint(), false); err != nil {
		t.Fatalf("a complete OPC UA configuration was rejected: %v", err)
	}
	for name, clear := range map[string]func(*OPCUAFrontendConfig){
		"endpoint url":      func(c *OPCUAFrontendConfig) { c.EndpointURL = "" },
		"application uri":   func(c *OPCUAFrontendConfig) { c.ApplicationURI = "" },
		"security policy":   func(c *OPCUAFrontendConfig) { c.SecurityPolicyURI = "" },
		"transport profile": func(c *OPCUAFrontendConfig) { c.TransportProfileURI = "" },
		"namespace uri":     func(c *OPCUAFrontendConfig) { c.NamespaceURI = "" },
		"source folder":     func(c *OPCUAFrontendConfig) { c.SourceFolderName = "" },
	} {
		t.Run(name, func(t *testing.T) {
			endpoint := testOPCUAEndpoint()
			clear(&endpoint)
			if _, err := GuidedSetupOPCUAConfig(source, "127.0.0.1:4840", endpoint, false); err == nil {
				t.Fatal("an incomplete OPC UA configuration was accepted")
			}
		})
	}
	// The generic frontend helper cannot build a UA config, because it has no
	// endpoint settings to work from.
	if _, err := GuidedSetupFrontendConfig(source, FrontendOPCUA, "127.0.0.1:4840", false); err == nil {
		t.Fatal("the generic helper built an OPC UA configuration")
	}
}

func TestOPCUAConfigFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "adapter.json")
	source := opcda.SourceConfig{CLSID: "{00000000-0000-0000-0000-000000000001}"}
	config, err := GuidedSetupOPCUAConfig(source, "127.0.0.1:4840", testOPCUAEndpoint(), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteConfigFileExclusive(path, config); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Version  int `json:"version"`
		Frontend struct {
			Type        string `json:"type"`
			OPCUAListen string `json:"opcuaListen"`
			HTTPListen  string `json:"httpListen"`
			GRPCListen  string `json:"grpcListen"`
			OPCUA       struct {
				SecurityPolicyURI string `json:"securityPolicyUri"`
				NamespaceURI      string `json:"namespaceUri"`
			} `json:"opcua"`
		} `json:"frontend"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Version != ConfigFileVersion {
		t.Fatalf("version = %d, want %d", stored.Version, ConfigFileVersion)
	}
	if stored.Frontend.Type != string(FrontendOPCUA) {
		t.Fatalf("frontend = %q", stored.Frontend.Type)
	}
	// Only the selected frontend's listener is written.
	if stored.Frontend.HTTPListen != "" || stored.Frontend.GRPCListen != "" {
		t.Fatalf("another frontend's listener was written: %+v", stored.Frontend)
	}
	if stored.Frontend.OPCUA.SecurityPolicyURI != testOPCUAEndpoint().SecurityPolicyURI {
		t.Fatalf("security policy = %q", stored.Frontend.OPCUA.SecurityPolicyURI)
	}

	loaded, err := LoadConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Frontend != FrontendOPCUA || loaded.OPCUAListenAddress != "127.0.0.1:4840" {
		t.Fatalf("loaded = %+v", loaded)
	}
	if loaded.OPCUA != testOPCUAEndpoint() {
		t.Fatalf("loaded endpoint = %+v", loaded.OPCUA)
	}
}

// A version 2 file keeps working after the upgrade to version 3.
func TestOlderConfigFilesStillLoad(t *testing.T) {
	directory := t.TempDir()
	cases := map[string]string{
		"version 1 http": `{"version":1,"source":{"clsid":"{A}"},"frontend":{"type":"http","httpListen":"127.0.0.1:8080"},"writeEnabled":false}`,
		"version 2 grpc": `{"version":2,"source":{"clsid":"{A}"},"frontend":{"type":"grpc","grpcListen":"127.0.0.1:50051"},"writeEnabled":false}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(directory, name+".json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfigFile(path); err != nil {
				t.Fatalf("an older configuration stopped loading: %v", err)
			}
		})
	}
}

// The service starts the UA listener, serves a real client through it, and
// stops cleanly.
func TestServiceServesTheOPCUAFrontend(t *testing.T) {
	config := DefaultConfig()
	config.Source = opcda.SourceConfig{CLSID: "{00000000-0000-0000-0000-000000000001}"}
	config.Frontend = FrontendOPCUA
	config.OPCUAListenAddress = "127.0.0.1:0"
	config.OPCUA = testOPCUAEndpoint()

	service, err := New(config, &opcuaTestRuntime{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := service.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := service.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})

	if service.Frontend() != FrontendOPCUA {
		t.Fatalf("frontend = %s", service.Frontend())
	}
	address := service.Address()
	if address == "" {
		t.Fatal("the service reported no listen address")
	}

	// A Hello over the real socket proves the UA listener is serving.
	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	limits := opcua.DefaultBinaryLimits()
	body, err := opcua.EncodeHello(opcua.Hello{
		ProtocolVersion:   opcua.ProtocolVersion,
		ReceiveBufferSize: 65536,
		SendBufferSize:    65536,
		EndpointURL:       config.OPCUA.EndpointURL,
	}, limits)
	if err != nil {
		t.Fatal(err)
	}
	header, err := opcua.EncodeMessageHeader(opcua.MessageTypeHello, opcua.ChunkFinal, len(body), 65536)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(append(header, body...)); err != nil {
		t.Fatal(err)
	}

	var responseHeader [opcua.HeaderSize]byte
	if _, err := readFull(conn, responseHeader[:]); err != nil {
		t.Fatalf("read acknowledge header: %v", err)
	}
	decoded, err := opcua.DecodeMessageHeader(responseHeader[:], 65536)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Type != opcua.MessageTypeAcknowledge {
		t.Fatalf("response type = %s, want an Acknowledge", decoded.Type)
	}
}

func readFull(conn net.Conn, buffer []byte) (int, error) {
	total := 0
	for total < len(buffer) {
		read, err := conn.Read(buffer[total:])
		if err != nil {
			return total, err
		}
		total += read
	}
	return total, nil
}

// opcuaTestRuntime is a DA runtime that reports itself connected without
// touching COM.
type opcuaTestRuntime struct{}

func (opcuaTestRuntime) Status(context.Context) opcda.RuntimeStatus {
	return opcda.RuntimeStatus{State: opcda.RuntimeStateConnected, ConnectionGeneration: 1}
}
func (opcuaTestRuntime) Browse(context.Context, opcda.BrowseRequest) (opcda.BrowseResult, error) {
	return opcda.BrowseResult{}, nil
}
func (opcuaTestRuntime) ReadBatch(context.Context, opcda.ReadRequest) ([]opcda.ReadResult, error) {
	return nil, nil
}
func (opcuaTestRuntime) WriteBatch(context.Context, []opcda.WriteItem) ([]opcda.WriteResult, error) {
	return nil, nil
}
func (opcuaTestRuntime) Subscribe(context.Context, opcda.SubscribeRequest) (opcda.Subscription, error) {
	return nil, opcda.NewAdapterError(opcda.CodeSubscribeUnsupported, "not used here")
}
func (opcuaTestRuntime) Unsubscribe(context.Context, opcda.SubscriptionID) error { return nil }
func (opcuaTestRuntime) Shutdown(context.Context) error                          { return nil }

// This source offers no OPC DA item properties. PROPERTIES_UNSUPPORTED is the
// same answer a real source without IOPCItemProperties gives.
func (opcuaTestRuntime) AvailableItemProperties(context.Context, string) ([]opcda.AvailableProperty, error) {
	return nil, opcda.NewAdapterError(opcda.CodePropertiesUnsupported, "this source offers no item properties")
}

func (opcuaTestRuntime) ItemProperties(context.Context, opcda.ItemPropertiesRequest) ([]opcda.ItemPropertyValue, error) {
	return nil, opcda.NewAdapterError(opcda.CodePropertiesUnsupported, "this source offers no item properties")
}
