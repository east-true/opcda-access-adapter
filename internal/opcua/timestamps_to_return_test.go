package opcua

import (
	"context"
	"testing"
	"time"

	"github.com/east-true/opcda-access-adapter/internal/opcda"
)

// TimestampsToReturn decides which of the two timestamps a Read answers with.
// Until this file existed the only thing checked about it was that all four
// values were accepted -- nothing checked what any of them returned, and every
// other test asked for Both.
//
// A mutation sweep found it: changing `timestamps == TimestampsBoth` to `!=`
// survived in four places. That mutation makes the Server branch fire for
// Source and Neither as well, so a client asking for neither timestamp would
// have been given one, and a client asking only for the source timestamp would
// have been given the adapter's clock alongside it. Nothing failed.
//
// This matters more here than the clause alone would suggest. A source
// timestamp that the adapter did not receive must never be invented, which is
// this project's own invariant, and Neither is the request where inventing one
// is easiest to do unnoticed.

// timestampExpectation is what each of the four values must produce.
type timestampExpectation struct {
	timestamps TimestampsToReturn
	name       string
	wantSource bool
	wantServer bool
}

var timestampExpectations = []timestampExpectation{
	{TimestampsSource, "Source", true, false},
	{TimestampsServer, "Server", false, true},
	{TimestampsBoth, "Both", true, true},
	{TimestampsNeither, "Neither", false, false},
}

func TestReadReturnsOnlyTheTimestampsAskedFor(t *testing.T) {
	sourceTime := time.Date(2026, time.September, 12, 1, 2, 3, 400, time.UTC)
	now := time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)

	for _, expect := range timestampExpectations {
		t.Run(expect.name, func(t *testing.T) {
			runtime := &stubRuntime{}
			service, _ := testDataService(t, runtime)
			varTypeI4 := opcda.VTI4
			runtime.readResults = []opcda.ReadResult{{
				ItemID: "Test/Int32", VarType: &varTypeI4, HRESULT: opcda.SOK, HRESULTPresent: true,
				Value: &opcda.DAValue{
					ItemID: "Test/Int32", VarType: varTypeI4, Value: int32(4242),
					QualityRaw: QualityGood, Timestamp: sourceTime, TimestampPresent: true,
				},
			}}

			request := readRequestFor(readValue(ItemNodeID("Test/Int32")))
			request.TimestampsToReturn = expect.timestamps
			response, err := service.Read(context.Background(), request, now)
			if err != nil {
				t.Fatal(err)
			}
			assertTimestamps(t, response.Results[0], expect, sourceTime, now)
		})
	}
}

// The same contract on the address space's own variables, which take a
// different path: they have no DA value behind them, so the source timestamp
// is the adapter's read time rather than the source's.
func TestLocalDataValueReturnsOnlyTheTimestampsAskedFor(t *testing.T) {
	now := time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)
	for _, expect := range timestampExpectations {
		t.Run(expect.name, func(t *testing.T) {
			value := localDataValue(Variant{Type: BuiltInInt32, Value: int32(7)}, expect.timestamps, now)
			if got := !value.SourceTimestamp.IsZero(); got != expect.wantSource {
				t.Errorf("source timestamp present = %v, want %v (%s)", got, expect.wantSource, expect.name)
			}
			if got := !value.ServerTimestamp.IsZero(); got != expect.wantServer {
				t.Errorf("server timestamp present = %v, want %v (%s)", got, expect.wantServer, expect.name)
			}
		})
	}
}

// dataValueForRead is where the absence of a DA timestamp is preserved, so the
// two rules meet here: a request may ask for the source timestamp and the
// source may have supplied none, and the answer is still no timestamp rather
// than the adapter's clock.
func TestDataValueForReadNeverInventsASourceTimestamp(t *testing.T) {
	now := time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)
	for _, expect := range timestampExpectations {
		t.Run(expect.name, func(t *testing.T) {
			result := opcda.ReadResult{
				ItemID: "Test/Int32", HRESULT: opcda.SOK, HRESULTPresent: true,
				Value: &opcda.DAValue{
					ItemID: "Test/Int32", VarType: opcda.VTI4, Value: int32(1),
					QualityRaw: QualityGood,
					// The source reported no timestamp.
					TimestampPresent: false,
				},
			}
			value := dataValueForRead(result, expect.timestamps, now)
			if !value.SourceTimestamp.IsZero() {
				t.Errorf("a source timestamp was invented for %s: %s", expect.name, value.SourceTimestamp)
			}
			if got := !value.ServerTimestamp.IsZero(); got != expect.wantServer {
				t.Errorf("server timestamp present = %v, want %v (%s)", got, expect.wantServer, expect.name)
			}
		})
	}
}

// A non-Value attribute is answered from the address space and carries no
// source timestamp of its own, so only the server timestamp is in question.
func TestReadAttributeReturnsOnlyTheServerTimestampAskedFor(t *testing.T) {
	now := time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)
	for _, expect := range timestampExpectations {
		t.Run(expect.name, func(t *testing.T) {
			runtime := &stubRuntime{}
			service, _ := testDataService(t, runtime)
			request := readRequestFor(ReadValueID{
				NodeID:      ItemNodeID("Test/Int32"),
				AttributeID: AttributeBrowseName,
			})
			request.TimestampsToReturn = expect.timestamps
			response, err := service.Read(context.Background(), request, now)
			if err != nil {
				t.Fatal(err)
			}
			result := response.Results[0]
			if !result.SourceTimestamp.IsZero() {
				t.Errorf("a non-Value attribute carried a source timestamp for %s: %s",
					expect.name, result.SourceTimestamp)
			}
			if got := !result.ServerTimestamp.IsZero(); got != expect.wantServer {
				t.Errorf("server timestamp present = %v, want %v (%s)", got, expect.wantServer, expect.name)
			}
		})
	}
}

// An item property is metadata the adapter read now, so it carries a server
// timestamp and never a source one: the source reports no timestamp for a
// property, and inventing one would make metadata look like a process value
// that was sampled.
func TestReadItemPropertyReturnsOnlyTheServerTimestampAskedFor(t *testing.T) {
	now := time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)
	for _, expect := range timestampExpectations {
		t.Run(expect.name, func(t *testing.T) {
			runtime := &stubRuntime{
				available: map[string][]opcda.AvailableProperty{
					"Test/Float": {{ID: opcda.PropertyEUUnits}},
				},
				propertyValues: map[opcda.PropertyID]opcda.ItemPropertyValue{
					opcda.PropertyEUUnits: {OK: true, Value: "degC", ValuePresent: true},
				},
			}
			service, space := testDataService(t, runtime)
			if err := space.AttachItemProperties("Test/Float",
				runtime.available["Test/Float"], opcda.EUTypeNoEnum, testNodeBudget); err != nil {
				t.Fatalf("AttachItemProperties: %v", err)
			}

			request := readRequestFor(readValue(ItemPropertyNodeID("Test/Float", "EngineeringUnits")))
			request.TimestampsToReturn = expect.timestamps
			response, err := service.Read(context.Background(), request, now)
			if err != nil {
				t.Fatal(err)
			}
			result := response.Results[0]
			if result.Status != StatusGood {
				t.Fatalf("status = %s; the property read did not succeed, so this "+
					"test would pass without exercising anything", result.Status.Hex())
			}
			if !result.SourceTimestamp.IsZero() {
				t.Errorf("a property carried a source timestamp for %s: %s",
					expect.name, result.SourceTimestamp)
			}
			if got := !result.ServerTimestamp.IsZero(); got != expect.wantServer {
				t.Errorf("server timestamp present = %v, want %v (%s)", got, expect.wantServer, expect.name)
			}
		})
	}
}

// A monitored item carries its own TimestampsToReturn, chosen when it was
// created, and an invalidation notification has to honour it like any other
// value. Every subscription test asks for Both -- monitorAt hardcodes it -- so
// this path had the same gap as Read did.
//
// The value reported here is Bad_NotConnected with a null value, and a client
// that asked for no timestamps must not receive one attached to it.
func TestInvalidationHonoursTheItemsTimestampsToReturn(t *testing.T) {
	for _, expect := range timestampExpectations {
		t.Run(expect.name, func(t *testing.T) {
			runtime := &subscribingRuntime{revisedRate: 100 * time.Millisecond}
			service, _ := testSubscriptionService(t, runtime)
			id := createSubscription(t, service)

			response, err := service.CreateMonitoredItems(context.Background(), testSession,
				CreateMonitoredItemsRequest{
					Header:             RequestHeader{RequestHandle: 2, AdditionalHeader: NullExtensionObject()},
					SubscriptionID:     id,
					TimestampsToReturn: expect.timestamps,
					ItemsToCreate: []MonitoredItemCreateRequest{{
						ItemToMonitor:  ReadValueID{NodeID: ItemNodeID("Test/Int32"), AttributeID: AttributeValue},
						MonitoringMode: MonitoringModeReporting,
						RequestedParameters: MonitoringParameters{
							ClientHandle: 1, SamplingInterval: 0, QueueSize: 1,
							Filter: NullExtensionObject(),
						},
					}},
				}, channelEpoch)
			if err != nil {
				t.Fatal(err)
			}
			if len(response.Results) != 1 || response.Results[0].StatusCode != StatusGood {
				t.Fatalf("the monitored item was not created, so this test would "+
					"pass without exercising anything: %+v", response.Results)
			}

			runtime.latest().invalidate()
			lost := publishOnce(t, service, channelEpoch.Add(2*time.Second))
			if len(lost) != 1 || lost[0].Value.Status != StatusBadNotConnected {
				t.Fatalf("invalidation reported %v", lost)
			}
			value := lost[0].Value
			if !value.SourceTimestamp.IsZero() {
				t.Errorf("an invalidation carried a source timestamp for %s: %s",
					expect.name, value.SourceTimestamp)
			}
			if got := !value.ServerTimestamp.IsZero(); got != expect.wantServer {
				t.Errorf("server timestamp present = %v, want %v (%s)", got, expect.wantServer, expect.name)
			}
		})
	}
}

func assertTimestamps(t *testing.T, result DataValue, expect timestampExpectation, sourceTime, now time.Time) {
	t.Helper()
	if expect.wantSource {
		if !result.SourceTimestamp.Equal(sourceTime) {
			t.Errorf("source timestamp = %s, want the DA timestamp %s", result.SourceTimestamp, sourceTime)
		}
	} else if !result.SourceTimestamp.IsZero() {
		t.Errorf("%s asked for no source timestamp and received %s", expect.name, result.SourceTimestamp)
	}
	if expect.wantServer {
		if !result.ServerTimestamp.Equal(now) {
			t.Errorf("server timestamp = %s, want the adapter's time %s", result.ServerTimestamp, now)
		}
	} else if !result.ServerTimestamp.IsZero() {
		t.Errorf("%s asked for no server timestamp and received %s", expect.name, result.ServerTimestamp)
	}
}
