package opcua

import (
	"context"
	"testing"
	"time"
)

// OPC 10000-5 8.3.2 places a Server object in every server's address space, and
// a generic UA client depends on it: it reads the NamespaceArray before it does
// anything else, because a namespace index means nothing without the URI it
// stands for, and it reads ServerStatus on a timer to decide whether the server
// is still alive. Without these nodes a third-party client concluded this
// server was dead and tore the connection down, however healthy it was.
func TestStandardServerNodesArePublished(t *testing.T) {
	space := testAddressSpace(t)
	for _, testCase := range []struct {
		name       string
		identifier uint32
		class      NodeClass
	}{
		{"Server", NodeIDServer, NodeClassObject},
		{"ServerArray", NodeIDServerArray, NodeClassVariable},
		{"NamespaceArray", NodeIDNamespaceArray, NodeClassVariable},
		{"ServerStatus", NodeIDServerStatus, NodeClassVariable},
		{"ServerStatus.State", NodeIDServerStatusState, NodeClassVariable},
		{"ServerStatus.CurrentTime", NodeIDServerStatusCurrentTime, NodeClassVariable},
		{"ServerStatus.StartTime", NodeIDServerStatusStartTime, NodeClassVariable},
		{"ServerStatus.BuildInfo", NodeIDServerStatusBuildInfo, NodeClassVariable},
		{"ServiceLevel", NodeIDServerServiceLevel, NodeClassVariable},
		{"Auditing", NodeIDServerAuditing, NodeClassVariable},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			node, ok := space.Node(NumericNodeID(0, testCase.identifier))
			if !ok {
				t.Fatalf("i=%d is not published", testCase.identifier)
			}
			if node.Class != testCase.class {
				t.Fatalf("node class = %v, want %v", node.Class, testCase.class)
			}
		})
	}
}

// The NamespaceArray is the durable name of this adapter's namespace, so it has
// to report the configured URI rather than an index.
func TestNamespaceArrayReportsTheConfiguredURI(t *testing.T) {
	space := testAddressSpace(t)
	node, ok := space.Node(NumericNodeID(0, NodeIDNamespaceArray))
	if !ok {
		t.Fatal("the NamespaceArray is not published")
	}
	value := node.LocalValue(time.Now().UTC())
	if !value.IsArray || value.Type != BuiltInString {
		t.Fatalf("NamespaceArray value = %+v, want a String array", value)
	}
	uris, ok := value.Value.([]string)
	if !ok || len(uris) != 2 {
		t.Fatalf("namespace URIs = %#v", value.Value)
	}
	if uris[0] != "http://opcfoundation.org/UA/" {
		t.Fatalf("namespace 0 = %q, want the OPC UA namespace", uris[0])
	}
	if uris[1] != "urn:example:opcda-access-adapter" {
		t.Fatalf("namespace 1 = %q, want the configured URI", uris[1])
	}
}

// CurrentTime has to be answered as of the read, not fixed when the address
// space was built, because a client uses it to tell a live server from a stale
// one.
func TestServerStatusCurrentTimeIsAnsweredAsOfTheRead(t *testing.T) {
	space := testAddressSpace(t)
	node, ok := space.Node(NumericNodeID(0, NodeIDServerStatusCurrentTime))
	if !ok {
		t.Fatal("CurrentTime is not published")
	}
	first := time.Date(2024, time.March, 14, 15, 9, 26, 0, time.UTC)
	second := first.Add(time.Hour)
	if got := node.LocalValue(first).Value; got != first {
		t.Fatalf("CurrentTime = %v, want the time of the read", got)
	}
	if got := node.LocalValue(second).Value; got != second {
		t.Fatalf("CurrentTime = %v, want the time of the second read", got)
	}
}

// The ServerStatus structure's field order is the NodeSet's: StartTime,
// CurrentTime, State, BuildInfo, SecondsTillShutdown, ShutdownReason. Writing
// them in any other order produces a stream that decodes into the wrong fields
// without any error, which is exactly the kind of fault only a foreign decoder
// catches.
func TestServerStatusEncodesTheNodeSetFieldOrder(t *testing.T) {
	space, err := NewAddressSpace(AddressSpaceConfig{
		NamespaceURI:     "urn:example:opcda-access-adapter",
		SourceFolderName: "Source",
		ProductURI:       "urn:example:product",
		ManufacturerName: "opcda-access-adapter",
		ProductName:      "Adapter",
		SoftwareVersion:  "1.2.3",
		BuildNumber:      "abcdef",
		BuildDate:        time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	node, ok := space.Node(NumericNodeID(0, NodeIDServerStatus))
	if !ok {
		t.Fatal("ServerStatus is not published")
	}
	now := time.Date(2024, time.March, 14, 15, 9, 26, 0, time.UTC)
	value := node.LocalValue(now)
	object, ok := value.Value.(ExtensionObject)
	if !ok {
		t.Fatalf("ServerStatus value = %#v, want an ExtensionObject", value.Value)
	}
	if !object.TypeID.Equal(NumericNodeID(0, NodeIDServerStatusEncodingID)) {
		t.Fatalf("encoding id = %s, want the DefaultBinary encoding", object.TypeID.String())
	}

	decoder, err := NewDecoder(object.Body, DefaultBinaryLimits())
	if err != nil {
		t.Fatal(err)
	}
	startTime, err := decoder.ReadDateTime()
	if err != nil {
		t.Fatal(err)
	}
	// StartTime is when this server began answering, so it is fixed at
	// construction and does not move with the read.
	if startTime.IsZero() || time.Since(startTime) > time.Minute {
		t.Fatalf("StartTime = %v, want the time the address space was built", startTime)
	}
	currentTime, err := decoder.ReadDateTime()
	if err != nil {
		t.Fatal(err)
	}
	if !currentTime.Equal(now) {
		t.Fatalf("CurrentTime = %v, want %v", currentTime, now)
	}
	state, err := decoder.ReadInt32()
	if err != nil {
		t.Fatal(err)
	}
	if state != ServerStateRunning {
		t.Fatalf("State = %d, want Running", state)
	}

	// BuildInfo is nested inline, not as its own ExtensionObject, and its own
	// field order is ProductUri, ManufacturerName, ProductName,
	// SoftwareVersion, BuildNumber, BuildDate.
	for _, field := range []struct {
		name string
		want string
	}{
		{"ProductUri", "urn:example:product"},
		{"ManufacturerName", "opcda-access-adapter"},
		{"ProductName", "Adapter"},
		{"SoftwareVersion", "1.2.3"},
		{"BuildNumber", "abcdef"},
	} {
		got, isNull, readErr := decoder.ReadString()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if isNull {
			t.Fatalf("%s was written as null", field.name)
		}
		if got != field.want {
			t.Fatalf("%s = %q, want %q", field.name, got, field.want)
		}
	}
	buildDate, err := decoder.ReadDateTime()
	if err != nil {
		t.Fatal(err)
	}
	if !buildDate.Equal(time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("BuildDate = %v", buildDate)
	}
	seconds, err := decoder.ReadUInt32()
	if err != nil {
		t.Fatal(err)
	}
	if seconds != 0 {
		t.Fatalf("SecondsTillShutdown = %d, want 0 while running", seconds)
	}
	if _, err := decoder.ReadLocalizedText(); err != nil {
		t.Fatal(err)
	}
	if decoder.Remaining() != 0 {
		t.Fatalf("%d bytes left after the structure", decoder.Remaining())
	}
}

// A Read of a standard Server variable is answered from the address space and
// never reaches the DA source: the server is reporting on itself.
func TestReadingAStandardServerVariableDoesNotReachTheSource(t *testing.T) {
	runtime := &stubRuntime{}
	space := testAddressSpace(t)
	service, err := NewDataAccessService(space, runtime, DefaultDataAccessLimits())
	if err != nil {
		t.Fatal(err)
	}
	response, err := service.Read(context.Background(),
		readRequestFor(readValue(NumericNodeID(0, NodeIDServerStatusState))), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if response.Results[0].Status != StatusGood {
		t.Fatalf("status = %s", response.Results[0].Status.Hex())
	}
	if got := response.Results[0].Value.Value; got != ServerStateRunning {
		t.Fatalf("State = %#v, want Running", got)
	}
	if len(runtime.readRequest.Items) != 0 {
		t.Fatal("a standard Server variable reached the DA source")
	}
}
