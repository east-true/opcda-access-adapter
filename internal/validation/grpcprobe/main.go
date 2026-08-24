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
	if err := validateScenario(ctx, client, *expectedCLSID, *writeEnabled); err != nil {
		fail("validate gRPC DA scenario", err)
	}
	fmt.Printf("GRPC_REAL_DA_PASS frontend=grpc source=exact-clsid browse=root+nested read=partial writeEnabled=%t valuesLogged=false\n", *writeEnabled)
}

func validateScenario(ctx context.Context, client opcdav1.OPCDAAccessClient, expectedCLSID string, writeEnabled bool) error {
	statusResponse, err := waitConnected(ctx, client)
	if err != nil {
		return err
	}
	if statusResponse.Source == nil || !strings.EqualFold(statusResponse.Source.Clsid, expectedCLSID) {
		return fmt.Errorf("Status source CLSID did not match the selected source")
	}
	if statusResponse.Capabilities == nil || statusResponse.Capabilities.Browse != "supported" || !statusResponse.Capabilities.Read || !statusResponse.Capabilities.Write {
		return fmt.Errorf("Status omitted DA Browse/Read/Write capabilities")
	}
	if statusResponse.Frontend == nil || !statusResponse.Frontend.Listening || statusResponse.WriteEnabled != writeEnabled {
		return fmt.Errorf("Status frontend or Write state did not match the scenario")
	}

	root, err := client.Browse(ctx, &opcdav1.DABrowseRequest{})
	if err != nil {
		return fmt.Errorf("root Browse: %w", err)
	}
	testBranches := 0
	for _, entry := range root.Entries {
		if entry.Kind == opcdav1.DABrowseEntryKind_DA_BROWSE_ENTRY_KIND_BRANCH && entry.Name == "Test" && !entry.ItemIdPresent {
			testBranches++
		}
	}
	if testBranches != 1 {
		return fmt.Errorf("root Browse did not return the Test branch exactly once")
	}

	nested, err := client.Browse(ctx, &opcdav1.DABrowseRequest{Path: []string{"Test"}})
	if err != nil {
		return fmt.Errorf("nested Browse: %w", err)
	}
	if len(nested.Path) != 1 || nested.Path[0] != "Test" {
		return fmt.Errorf("nested Browse path changed")
	}
	for _, itemID := range []string{"Test/Int32", "Test/Float", "Test/String"} {
		matches := 0
		for _, entry := range nested.Entries {
			if entry.Kind == opcdav1.DABrowseEntryKind_DA_BROWSE_ENTRY_KIND_ITEM && entry.ItemIdPresent && entry.ItemId == itemID {
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("nested Browse did not preserve an exact expected ItemID")
		}
	}

	partial, err := client.Read(ctx, &opcdav1.DAReadRequest{Items: []*opcdav1.DAReadItem{{ItemId: "Test/Int32"}, {ItemId: "__opcda_adapter_invalid_item__"}}})
	if err != nil {
		return fmt.Errorf("partial Read: %w", err)
	}
	if len(partial.Results) != 2 {
		return fmt.Errorf("partial Read result count changed")
	}
	known, unknown := partial.Results[0], partial.Results[1]
	if !known.Ok || known.ItemId != "Test/Int32" || known.DataType == nil || known.DataType.Raw != uint32(opcda.VTI4) || known.DataType.Name != "VT_I4" ||
		known.CanonicalDataType == nil || known.CanonicalDataType.Raw != uint32(opcda.VTI4) || !known.QualityPresent || known.QualityRaw != 192 || known.Hresult == nil || known.Hresult.Raw != 0 {
		return fmt.Errorf("known Read item did not preserve DA type, Quality, or HRESULT")
	}
	if (known.TimestampPresent && known.TimestampUnixSeconds == 0 && known.TimestampNanos == 0) || (!known.TimestampPresent && (known.TimestampUnixSeconds != 0 || known.TimestampNanos != 0)) {
		return fmt.Errorf("Read timestamp presence contradicted its representation")
	}
	if unknown.Ok || unknown.ItemId != "__opcda_adapter_invalid_item__" || unknown.Hresult == nil || unknown.Hresult.Raw != 0xC0040007 || unknown.Hresult.Value >= 0 {
		return fmt.Errorf("invalid ItemID did not preserve ordered OPC_E_UNKNOWNITEMID")
	}

	if !writeEnabled {
		_, err = client.Write(ctx, &opcdav1.DAWriteRequest{Items: []*opcdav1.DAWriteItem{{
			ItemId: "Test/Float", DataType: &opcdav1.DAVarType{Raw: uint32(opcda.VTR4), Name: "VT_R4"},
			Value: &opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_R4Value{}},
		}}})
		if !hasDAError(err, codes.PermissionDenied, string(opcda.CodeWriteDisabled)) {
			return fmt.Errorf("disabled Write was not rejected before source access")
		}
		return nil
	}

	floatRead, err := client.Read(ctx, &opcdav1.DAReadRequest{Items: []*opcdav1.DAReadItem{{ItemId: "Test/Float"}}})
	if err != nil || len(floatRead.Results) != 1 || !floatRead.Results[0].Ok || floatRead.Results[0].CanonicalDataType == nil || floatRead.Results[0].CanonicalDataType.Raw != uint32(opcda.VTR4) {
		return fmt.Errorf("safe Write item was not readable as VT_R4")
	}
	r4, ok := floatRead.Results[0].Value.Value.(*opcdav1.DAScalarValue_R4Value)
	if !ok {
		return fmt.Errorf("VT_R4 Read did not use r4_value")
	}
	mismatch, err := client.Write(ctx, &opcdav1.DAWriteRequest{Items: []*opcdav1.DAWriteItem{{
		ItemId: "Test/Float", DataType: &opcdav1.DAVarType{Raw: uint32(opcda.VTR8), Name: "VT_R8"},
		Value: &opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_R8Value{R8Value: float64(r4.R4Value)}},
	}}})
	if err != nil || len(mismatch.Results) != 1 || mismatch.Results[0].Ok || mismatch.Results[0].ErrorCode != string(opcda.CodeTypeMismatch) {
		return fmt.Errorf("strict typed Write did not preserve the source canonical type mismatch")
	}
	safeWrite, err := client.Write(ctx, &opcdav1.DAWriteRequest{Items: []*opcdav1.DAWriteItem{{
		ItemId: "Test/Float", DataType: floatRead.Results[0].CanonicalDataType, Value: floatRead.Results[0].Value,
	}}})
	if err != nil || len(safeWrite.Results) != 1 || !safeWrite.Results[0].Ok || safeWrite.Results[0].Hresult == nil || safeWrite.Results[0].Hresult.Value != 0 {
		return fmt.Errorf("safe typed Write failed")
	}

	stringRead, err := client.Read(ctx, &opcdav1.DAReadRequest{Items: []*opcdav1.DAReadItem{{ItemId: "Test/String"}}})
	if err != nil || len(stringRead.Results) != 1 || !stringRead.Results[0].Ok || stringRead.Results[0].CanonicalDataType == nil || stringRead.Results[0].CanonicalDataType.Raw != uint32(opcda.VTBSTR) {
		return fmt.Errorf("read-only BSTR item was unavailable")
	}
	denied, err := client.Write(ctx, &opcdav1.DAWriteRequest{Items: []*opcdav1.DAWriteItem{{
		ItemId: "Test/String", DataType: stringRead.Results[0].CanonicalDataType, Value: stringRead.Results[0].Value,
	}}})
	if err != nil || len(denied.Results) != 1 || denied.Results[0].Ok || denied.Results[0].Hresult == nil || denied.Results[0].Hresult.Raw != 0xC0040006 {
		return fmt.Errorf("source-denied Write did not preserve OPC_E_BADRIGHTS")
	}
	return nil
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
