package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	opcdav1 "github.com/east-true/opcda-access-adapter/api/opcda/v1"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const maximumProbeTimeout = 2 * time.Minute

func main() {
	address := flag.String("address", "127.0.0.1:50051", "gRPC endpoint")
	expectedCLSID := flag.String("expected-clsid", "", "exact configured source CLSID")
	writeEnabled := flag.Bool("write-enabled", false, "expect and exercise enabled typed Write")
	timeout := flag.Duration("timeout", time.Minute, "bounded scenario deadline")
	flag.Parse()
	if flag.NArg() != 0 || *expectedCLSID == "" || *timeout <= 0 || *timeout > maximumProbeTimeout {
		fmt.Fprintln(os.Stderr, "usage: grpcprobe -address HOST:PORT -expected-clsid CLSID [-write-enabled] [-timeout DURATION]")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	connection, err := grpcgo.NewClient(
		*address,
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
		grpcgo.WithDefaultCallOptions(grpcgo.MaxCallRecvMsgSize(4<<20), grpcgo.MaxCallSendMsgSize(1<<20)),
	)
	if err != nil {
		fail("create gRPC client", err)
	}
	defer connection.Close()
	client := opcdav1.NewOPCDAAccessClient(connection)
	properties, err := validateScenario(ctx, client, *expectedCLSID, *writeEnabled)
	if err != nil {
		fail("validate gRPC DA scenario", err)
	}
	// Whether this source implements IOPCItemProperties is reported, not
	// asserted. It is a property of the server, and OPC 10000-8 Table A.1 can
	// only be validated against a source that has one.
	fmt.Printf("GRPC_REAL_DA_PASS frontend=grpc source=exact-clsid browse=root+nested read=partial writeEnabled=%t subscribeStream=%t itemProperties=%s valuesLogged=false\n",
		*writeEnabled, *writeEnabled, properties)
}

// validateScenario returns the source's item-property capability alongside its
// verdict, so the run records what the server offers rather than assuming it.
func validateScenario(ctx context.Context, client opcdav1.OPCDAAccessClient, expectedCLSID string, writeEnabled bool) (string, error) {
	statusResponse, err := waitConnected(ctx, client)
	if err != nil {
		return "", err
	}
	if statusResponse.Source == nil || !strings.EqualFold(statusResponse.Source.Clsid, expectedCLSID) {
		return "", fmt.Errorf("Status source CLSID did not match the selected source")
	}
	if statusResponse.Capabilities == nil || statusResponse.Capabilities.Browse != "supported" || !statusResponse.Capabilities.Read || !statusResponse.Capabilities.Write {
		return "", fmt.Errorf("Status omitted DA Browse/Read/Write capabilities")
	}
	// Reported, not asserted: whether a source implements IOPCItemProperties
	// is a property of the server, and Table A.1 can only be validated against
	// one that does.
	properties := statusResponse.Capabilities.Properties
	if properties == "" {
		return "", fmt.Errorf("Status omitted the DA item-property capability")
	}
	if statusResponse.Frontend == nil || !statusResponse.Frontend.Listening || statusResponse.WriteEnabled != writeEnabled {
		return "", fmt.Errorf("Status frontend or Write state did not match the scenario")
	}

	root, err := client.Browse(ctx, &opcdav1.DABrowseRequest{})
	if err != nil {
		return "", fmt.Errorf("root Browse: %w", err)
	}
	testBranches := 0
	for _, entry := range root.Entries {
		if entry.Kind == opcdav1.DABrowseEntryKind_DA_BROWSE_ENTRY_KIND_BRANCH && entry.Name == "Test" && !entry.ItemIdPresent {
			testBranches++
		}
	}
	if testBranches != 1 {
		return "", fmt.Errorf("root Browse did not return the Test branch exactly once")
	}

	nested, err := client.Browse(ctx, &opcdav1.DABrowseRequest{Path: []string{"Test"}})
	if err != nil {
		return "", fmt.Errorf("nested Browse: %w", err)
	}
	if len(nested.Path) != 1 || nested.Path[0] != "Test" {
		return "", fmt.Errorf("nested Browse path changed")
	}
	for _, itemID := range []string{"Test/Int32", "Test/Float", "Test/String"} {
		matches := 0
		for _, entry := range nested.Entries {
			if entry.Kind == opcdav1.DABrowseEntryKind_DA_BROWSE_ENTRY_KIND_ITEM && entry.ItemIdPresent && entry.ItemId == itemID {
				matches++
			}
		}
		if matches != 1 {
			return "", fmt.Errorf("nested Browse did not preserve an exact expected ItemID")
		}
	}

	partial, err := client.Read(ctx, &opcdav1.DAReadRequest{Items: []*opcdav1.DAReadItem{{ItemId: "Test/Int32"}, {ItemId: "__opcda_adapter_invalid_item__"}}})
	if err != nil {
		return "", fmt.Errorf("partial Read: %w", err)
	}
	if len(partial.Results) != 2 {
		return "", fmt.Errorf("partial Read result count changed")
	}
	known, unknown := partial.Results[0], partial.Results[1]
	if !known.Ok || known.ItemId != "Test/Int32" || known.DataType == nil || known.DataType.Raw != uint32(opcda.VTI4) || known.DataType.Name != "VT_I4" ||
		known.CanonicalDataType == nil || known.CanonicalDataType.Raw != uint32(opcda.VTI4) || !known.QualityPresent || known.QualityRaw != 192 || known.Hresult == nil || known.Hresult.Raw != 0 {
		return "", fmt.Errorf("known Read item did not preserve DA type, Quality, or HRESULT")
	}
	if (known.TimestampPresent && known.TimestampUnixSeconds == 0 && known.TimestampNanos == 0) || (!known.TimestampPresent && (known.TimestampUnixSeconds != 0 || known.TimestampNanos != 0)) {
		return "", fmt.Errorf("Read timestamp presence contradicted its representation")
	}
	if unknown.Ok || unknown.ItemId != "__opcda_adapter_invalid_item__" || unknown.Hresult == nil || unknown.Hresult.Raw != 0xC0040007 || unknown.Hresult.Value >= 0 {
		return "", fmt.Errorf("invalid ItemID did not preserve ordered OPC_E_UNKNOWNITEMID")
	}

	if !writeEnabled {
		_, err = client.Write(ctx, &opcdav1.DAWriteRequest{Items: []*opcdav1.DAWriteItem{{
			ItemId: "Test/Float", DataType: &opcdav1.DAVarType{Raw: uint32(opcda.VTR4), Name: "VT_R4"},
			Value: &opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_R4Value{}},
		}}})
		if !hasDAError(err, codes.PermissionDenied, string(opcda.CodeWriteDisabled)) {
			return "", fmt.Errorf("disabled Write was not rejected before source access")
		}
		return properties, nil
	}

	floatRead, err := client.Read(ctx, &opcdav1.DAReadRequest{Items: []*opcdav1.DAReadItem{{ItemId: "Test/Float"}}})
	if err != nil || len(floatRead.Results) != 1 || !floatRead.Results[0].Ok || floatRead.Results[0].CanonicalDataType == nil || floatRead.Results[0].CanonicalDataType.Raw != uint32(opcda.VTR4) {
		return "", fmt.Errorf("safe Write item was not readable as VT_R4")
	}
	r4, ok := floatRead.Results[0].Value.Value.(*opcdav1.DAScalarValue_R4Value)
	if !ok {
		return "", fmt.Errorf("VT_R4 Read did not use r4_value")
	}
	mismatch, err := client.Write(ctx, &opcdav1.DAWriteRequest{Items: []*opcdav1.DAWriteItem{{
		ItemId: "Test/Float", DataType: &opcdav1.DAVarType{Raw: uint32(opcda.VTR8), Name: "VT_R8"},
		Value: &opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_R8Value{R8Value: float64(r4.R4Value)}},
	}}})
	if err != nil || len(mismatch.Results) != 1 || mismatch.Results[0].Ok || mismatch.Results[0].ErrorCode != string(opcda.CodeTypeMismatch) {
		return "", fmt.Errorf("strict typed Write did not preserve the source canonical type mismatch")
	}
	safeWrite, err := client.Write(ctx, &opcdav1.DAWriteRequest{Items: []*opcdav1.DAWriteItem{{
		ItemId: "Test/Float", DataType: floatRead.Results[0].CanonicalDataType, Value: floatRead.Results[0].Value,
	}}})
	if err != nil || len(safeWrite.Results) != 1 || !safeWrite.Results[0].Ok || safeWrite.Results[0].Hresult == nil || safeWrite.Results[0].Hresult.Value != 0 {
		return "", fmt.Errorf("safe typed Write failed")
	}

	stringRead, err := client.Read(ctx, &opcdav1.DAReadRequest{Items: []*opcdav1.DAReadItem{{ItemId: "Test/String"}}})
	if err != nil || len(stringRead.Results) != 1 || !stringRead.Results[0].Ok || stringRead.Results[0].CanonicalDataType == nil || stringRead.Results[0].CanonicalDataType.Raw != uint32(opcda.VTBSTR) {
		return "", fmt.Errorf("read-only BSTR item was unavailable")
	}
	denied, err := client.Write(ctx, &opcdav1.DAWriteRequest{Items: []*opcdav1.DAWriteItem{{
		ItemId: "Test/String", DataType: stringRead.Results[0].CanonicalDataType, Value: stringRead.Results[0].Value,
	}}})
	if err != nil || len(denied.Results) != 1 || denied.Results[0].Ok || denied.Results[0].Hresult == nil || denied.Results[0].Hresult.Raw != 0xC0040006 {
		return "", fmt.Errorf("source-denied Write did not preserve OPC_E_BADRIGHTS")
	}
	if err := validateItemProperties(ctx, client); err != nil {
		return "", err
	}
	return properties, validateSubscribeStream(ctx, client)
}

var subscribeItems = []string{"Test/Int32", "Test/Float", "Test/String"}

// validateSubscribeStream exercises the server-streaming Subscribe RPC against
// the real source. The fixture's Test items are static, so the probe writes
// distinct values through the ordinary typed Write path to require
// change-driven notifications rather than only the server's initial snapshot.
func validateSubscribeStream(ctx context.Context, client opcdav1.OPCDAAccessClient) error {
	statusBefore, err := client.Status(ctx, &opcdav1.DAStatusRequest{})
	if err != nil {
		return fmt.Errorf("Status before Subscribe: %w", err)
	}
	if statusBefore.Capabilities == nil || !statusBefore.Capabilities.Subscribe {
		return fmt.Errorf("Status did not report the Subscribe capability")
	}
	if statusBefore.Runtime == nil || statusBefore.Runtime.SubscriptionCount != 0 {
		return fmt.Errorf("runtime already held a subscription before Subscribe")
	}

	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	items := make([]*opcdav1.DASubscribeItem, len(subscribeItems))
	for index, itemID := range subscribeItems {
		items[index] = &opcdav1.DASubscribeItem{ItemId: itemID}
	}
	stream, err := client.Subscribe(streamCtx, &opcdav1.DASubscribeRequest{
		Items: items, RequestedUpdateRateMs: 250,
	})
	if err != nil {
		return fmt.Errorf("open Subscribe stream: %w", err)
	}

	first, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("receive the created message: %w", err)
	}
	created := first.GetCreated()
	if created == nil {
		return fmt.Errorf("the first stream message was not the created message")
	}
	if created.SubscriptionId == "" || created.ConnectionGeneration == 0 {
		return fmt.Errorf("created message omitted the subscription identity")
	}
	if created.RevisedUpdateRateMs == 0 {
		return fmt.Errorf("created message omitted the source revised update rate")
	}
	if int(created.ActiveItemCount) != len(subscribeItems) || len(created.Items) != len(subscribeItems) {
		return fmt.Errorf("created message did not activate every requested item")
	}
	for index, item := range created.Items {
		if item.ItemId != subscribeItems[index] {
			return fmt.Errorf("created message changed the exact requested ItemID order")
		}
		if !item.Active || item.CanonicalDataType == nil || item.AccessRights == nil {
			return fmt.Errorf("created item %q lost its DA metadata", item.ItemId)
		}
	}
	fmt.Printf(
		"grpc subscribe created id=%s generation=%d requestedRate=%dms revisedRate=%dms activeItems=%d\n",
		created.SubscriptionId, created.ConnectionGeneration,
		created.RequestedUpdateRateMs, created.RevisedUpdateRateMs, created.ActiveItemCount,
	)

	// The server's initial snapshot proves the stream carries real callbacks.
	if err := receiveSubscriptionValue(stream, ""); err != nil {
		return fmt.Errorf("initial notification: %w", err)
	}

	const changes = 2
	for change := 0; change < changes; change++ {
		written := float32(change+1) + 0.25
		result, err := client.Write(ctx, &opcdav1.DAWriteRequest{Items: []*opcdav1.DAWriteItem{{
			ItemId:   "Test/Float",
			DataType: &opcdav1.DAVarType{Raw: uint32(opcda.VTR4), Name: "VT_R4"},
			Value:    &opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_R4Value{R4Value: written}},
		}}})
		if err != nil || len(result.Results) != 1 || !result.Results[0].Ok {
			return fmt.Errorf("write to induce change %d failed", change+1)
		}
		if err := receiveSubscriptionValue(stream, "Test/Float"); err != nil {
			return fmt.Errorf("change %d was not streamed: %w", change+1, err)
		}
	}
	fmt.Printf("grpc subscribe change-driven notifications verified changes=%d\n", changes)

	statusDuring, err := client.Status(ctx, &opcdav1.DAStatusRequest{})
	if err != nil {
		return fmt.Errorf("Status during Subscribe: %w", err)
	}
	if statusDuring.Runtime == nil || statusDuring.Runtime.SubscriptionCount != 1 {
		return fmt.Errorf("runtime did not report the open subscription")
	}

	// Closing the stream must release the DA group without an explicit RPC.
	cancelStream()
	deadline := time.Now().Add(30 * time.Second)
	for {
		statusAfter, err := client.Status(ctx, &opcdav1.DAStatusRequest{})
		if err == nil && statusAfter.Runtime != nil && statusAfter.Runtime.SubscriptionCount == 0 {
			fmt.Print("grpc subscribe stream close released the subscription\n")
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("closing the stream did not release the subscription")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// receiveSubscriptionValue reads until an update carries a usable value, and
// requires the exact ItemID when one is named. Process values are never printed.
func receiveSubscriptionValue(stream opcdav1.OPCDAAccess_SubscribeClient, requiredItemID string) error {
	known := make(map[string]struct{}, len(subscribeItems))
	for _, itemID := range subscribeItems {
		known[itemID] = struct{}{}
	}
	for {
		message, err := stream.Recv()
		if err != nil {
			return err
		}
		update := message.GetUpdate()
		if update == nil {
			return fmt.Errorf("the stream sent a second created message")
		}
		// The pending set holds at most one entry per active item.
		if len(update.Values) > len(subscribeItems) {
			return fmt.Errorf("an update carried %d values, more than the %d subscribed items", len(update.Values), len(subscribeItems))
		}
		for _, value := range update.Values {
			if _, ok := known[value.ItemId]; !ok {
				return fmt.Errorf("an update carried unexpected ItemID %q", value.ItemId)
			}
			if value.Hresult == nil {
				return fmt.Errorf("notification for %q carried no HRESULT", value.ItemId)
			}
			if !value.Ok {
				continue
			}
			if value.DataType == nil || value.CanonicalDataType == nil || value.AccessRights == nil || value.Value == nil {
				return fmt.Errorf("notification for %q lost DA metadata", value.ItemId)
			}
			if !value.QualityPresent {
				return fmt.Errorf("notification for %q carried no raw Quality", value.ItemId)
			}
			if !value.TimestampPresent && (value.TimestampUnixSeconds != 0 || value.TimestampNanos != 0) {
				return fmt.Errorf("notification for %q contradicted its timestamp presence", value.ItemId)
			}
			if requiredItemID == "" || value.ItemId == requiredItemID {
				return nil
			}
		}
	}
}

func waitConnected(ctx context.Context, client opcdav1.OPCDAAccessClient) (*opcdav1.DAStatusResponse, error) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		response, err := client.Status(ctx, &opcdav1.DAStatusRequest{})
		if err == nil && response.RuntimeState == string(opcda.RuntimeStateConnected) && response.Source != nil && response.Source.ConnectionGeneration >= 1 {
			return response, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for connected gRPC Status: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func hasDAError(err error, code codes.Code, errorCode string) bool {
	if status.Code(err) != code {
		return false
	}
	for _, raw := range status.Convert(err).Details() {
		if detail, ok := raw.(*opcdav1.DAOperationError); ok && detail.Code == errorCode {
			return true
		}
	}
	return false
}

func fail(operation string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", operation, err)
	os.Exit(1)
}

// validateItemProperties exercises the DA item property path against the real
// source. Nothing else does: the COM marshalling in properties_windows.go --
// the VARIANT array GetItemProperties returns, the per-property HRESULT array,
// and freeing both -- runs nowhere but here.
//
// What a source offers is its own decision, so which properties come back is
// reported rather than required. What is required is that the adapter reports
// them faithfully and that reading them works.
func validateItemProperties(ctx context.Context, client opcdav1.OPCDAAccessClient) error {
	available, err := client.AvailableItemProperties(ctx,
		&opcdav1.DAAvailableItemPropertiesRequest{ItemId: "Test/Float"})
	if err != nil {
		return fmt.Errorf("AvailableItemProperties: %w", err)
	}
	if available.ItemId != "Test/Float" {
		return fmt.Errorf("AvailableItemProperties answered for a different ItemID")
	}
	ids := make([]uint32, 0, len(available.Properties))
	for _, property := range available.Properties {
		if property.PropertyId == 0 {
			return fmt.Errorf("the source reported a property with identifier 0")
		}
		// The value, quality and timestamp have a path of their own. A source
		// offering them as properties is normal; asking for them is refused,
		// which is checked below.
		if property.PropertyId <= 4 {
			continue
		}
		ids = append(ids, property.PropertyId)
	}
	if len(ids) == 0 {
		fmt.Print("grpc item properties offered=none valuesLogged=false\n")
		return validateItemPropertyRefusals(ctx, client)
	}

	values, err := client.ItemProperties(ctx, &opcdav1.DAItemPropertiesRequest{
		ItemId: "Test/Float", PropertyIds: ids,
	})
	if err != nil {
		return fmt.Errorf("ItemProperties: %w", err)
	}
	if len(values.Results) != len(ids) {
		return fmt.Errorf("ItemProperties returned %d results for %d identifiers", len(values.Results), len(ids))
	}
	for index, result := range values.Results {
		if result.PropertyId != ids[index] {
			return fmt.Errorf("ItemProperties returned results out of request order")
		}
		if result.Hresult == nil {
			return fmt.Errorf("property %d carried no HRESULT", result.PropertyId)
		}
		if result.Ok {
			if result.Value == nil || result.DataType == nil {
				return fmt.Errorf("property %d succeeded without a value or type", result.PropertyId)
			}
			if result.Hresult.Value < 0 {
				return fmt.Errorf("property %d succeeded with a failed HRESULT", result.PropertyId)
			}
			continue
		}
		// A property the source refuses keeps its HRESULT and carries nothing.
		if result.Value != nil {
			return fmt.Errorf("property %d carried a value behind a failure", result.PropertyId)
		}
	}
	// The identifiers are recorded, not just the counts: "offered=4 read=3"
	// leaves the next reader unable to tell what this source did. A property
	// identifier is metadata, not process data. No value is printed -- the
	// probes claim valuesLogged=false and that claim is kept whole rather than
	// argued about per field.
	// A property the source refused and one the adapter cannot represent are
	// different facts, and the first run of this conflated them: a successful
	// HRESULT appeared under "refused" because the value was an array the
	// adapter does not carry.
	granted := make([]string, 0, len(ids))
	refused := make([]string, 0, len(ids))
	unrepresentable := make([]string, 0, len(ids))
	for index, result := range values.Results {
		switch {
		case result.Ok:
			granted = append(granted, fmt.Sprintf("%d", ids[index]))
		case result.Hresult.Value < 0:
			refused = append(refused, fmt.Sprintf("%d:%s", ids[index], result.Hresult.Hex))
		default:
			// The source answered; the adapter could not represent what it
			// said, and reports that against the property alone.
			if result.ErrorCode == "" {
				return fmt.Errorf("property %d failed with a successful HRESULT and no error code", ids[index])
			}
			unrepresentable = append(unrepresentable, fmt.Sprintf("%d:%s", ids[index], result.ErrorCode))
		}
	}
	fmt.Printf("grpc item properties offered=%s read=%s sourceRefused=%s unrepresentable=%s valuesLogged=false\n",
		joinOrNone(offeredNames(ids)), joinOrNone(granted),
		joinOrNone(refused), joinOrNone(unrepresentable))
	return validateItemPropertyRefusals(ctx, client)
}

// validateItemPropertyRefusals checks the two rules that are the adapter's
// rather than the source's.
func validateItemPropertyRefusals(ctx context.Context, client opcdav1.OPCDAAccessClient) error {
	// The item's value, quality and timestamp are properties 2, 3 and 4 and are
	// refused here, because Read and Subscribe deliver a value together with
	// its timestamp and its raw quality.
	for _, id := range []uint32{2, 3, 4} {
		if _, err := client.ItemProperties(ctx, &opcdav1.DAItemPropertiesRequest{
			ItemId: "Test/Float", PropertyIds: []uint32{id},
		}); !hasDAError(err, codes.InvalidArgument, string(opcda.CodeInvalidRequest)) {
			return fmt.Errorf("property %d was readable as a property", id)
		}
	}
	// An ItemID the source does not have is the source's answer, not a crash.
	unknown, err := client.AvailableItemProperties(ctx,
		&opcdav1.DAAvailableItemPropertiesRequest{ItemId: "__opcda_adapter_unknown_item__"})
	if err == nil && len(unknown.Properties) != 0 {
		return fmt.Errorf("an unknown ItemID reported %d properties", len(unknown.Properties))
	}
	return nil
}

func offeredNames(ids []uint32) []string {
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		names = append(names, fmt.Sprintf("%d", id))
	}
	return names
}

func joinOrNone(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ",")
}
