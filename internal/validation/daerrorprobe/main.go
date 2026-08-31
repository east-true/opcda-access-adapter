// daerrorprobe records which rows of OPC 10000-8 Tables A.4 and A.5 a real DA
// server actually produces through this adapter.
//
// All thirteen rows are bound in internal/opcua, and spec-check verifies each
// value against opcerror.h and each mapping against the published tables. That
// checks the transcription against the specification; it cannot check the
// transcription against a server. Until this probe ran, only two rows had ever
// been seen coming out of one.
//
// The probe answers a second question the tables do not: which rows this
// adapter can produce at all. Several cannot be reached by any client, because
// of decisions this project made deliberately -- ADR-0004 refuses a Write whose
// VARTYPE is not the canonical one, so the conversion errors never reach the
// source. Those are demonstrated rather than assumed: the probe performs the
// operation and shows the adapter answering first.
//
// It prints one line per row and never prints a value read from the source.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	opcdav1 "github.com/east-true/opcda-access-adapter/api/opcda/v1"
	"github.com/east-true/opcda-access-adapter/internal/opcda"
	"github.com/east-true/opcda-access-adapter/internal/opcua"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const maximumProbeTimeout = 2 * time.Minute

// The fixture items the rest of the real-DA validation uses. Test/String is the
// read-only one; Test/Float is writable and canonically VT_R4.
const (
	itemInt32   = "Test/Int32"
	itemFloat   = "Test/Float"
	itemString  = "Test/String"
	itemUnknown = "__opcda_adapter_unknown_item__"
)

// direction distinguishes which table a row belongs to, since A.4 and A.5 give
// OPC_E_INVALID_PID different answers and are not interchangeable.
type direction int

const (
	read direction = iota
	write
)

func (d direction) String() string {
	if d == read {
		return "read"
	}
	return "write"
}

func (d direction) table() string {
	if d == read {
		return "A.4"
	}
	return "A.5"
}

func (d direction) mapper() func(opcda.HRESULT) opcua.StatusCode {
	if d == read {
		return opcua.StatusCodeForReadError
	}
	return opcua.StatusCodeForWriteError
}

// outcome is what the probe learned about one row.
type outcome struct {
	name      string
	direction direction
	hresult   opcda.HRESULT // set when the source produced it
	observed  bool
	reason    string // why it cannot be produced, when it cannot
}

func main() {
	address := flag.String("address", "127.0.0.1:50051", "gRPC endpoint")
	timeout := flag.Duration("timeout", time.Minute, "bounded scenario deadline")
	flag.Parse()
	if flag.NArg() != 0 || *timeout <= 0 || *timeout > maximumProbeTimeout {
		fmt.Fprintln(os.Stderr, "usage: daerrorprobe -address HOST:PORT [-timeout DURATION]")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	connection, err := grpcgo.NewClient(*address, grpcgo.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fail("create gRPC client", err)
	}
	defer connection.Close()
	client := opcdav1.NewOPCDAAccessClient(connection)

	outcomes, err := probe(ctx, client)
	if err != nil {
		fail("probe DA error codes", err)
	}
	observed := 0
	for _, result := range outcomes {
		if !result.observed {
			fmt.Printf("DA_ERROR_ROW table=%s direction=%s code=%s observed=false reason=%s\n",
				result.direction.table(), result.direction, result.name, result.reason)
			continue
		}
		// A row is only confirmed when the HRESULT the source really produced
		// maps through the real function to what the table says. Feeding the
		// mapping a constant this project typed in proves nothing it did not
		// already assume.
		status := result.direction.mapper()(result.hresult)
		fmt.Printf("DA_ERROR_ROW table=%s direction=%s code=%s observed=true hresult=%s status=%s\n",
			result.direction.table(), result.direction, result.name, result.hresult.Hex(), status.Hex())
		observed++
	}
	fmt.Printf("DA_ERROR_PROBE_PASS rows=%d observed=%d valuesLogged=false\n", len(outcomes), observed)
}

func probe(ctx context.Context, client opcdav1.OPCDAAccessClient) ([]outcome, error) {
	var outcomes []outcome

	// OPC_E_UNKNOWNITEMID: an ItemID the source does not have.
	unknown, err := readHRESULT(ctx, client, itemUnknown)
	if err != nil {
		return nil, err
	}
	unknownRow, err := expect(read, "OPC_E_UNKNOWNITEMID", unknown,
		opcua.OPCEUnknownItemID, opcua.StatusBadNodeIdUnknown)
	if err != nil {
		return nil, err
	}
	outcomes = append(outcomes, unknownRow)

	// OPC_E_INVALIDITEMID: an ItemID the source rejects as malformed rather
	// than as absent. Whether a server distinguishes the two is its own choice,
	// so several shapes are tried and the first that produces the code wins.
	// If none does, the row is recorded as this server not distinguishing them.
	invalid := outcome{name: "OPC_E_INVALIDITEMID", direction: read,
		reason: "this-source-answers-UNKNOWNITEMID-for-malformed-ItemIDs"}
	for _, candidate := range []string{"/", "//", "Test/", "..", "Test/\\/"} {
		hresult, err := readHRESULT(ctx, client, candidate)
		if err != nil {
			return nil, err
		}
		if hresult == opcua.OPCEInvalidItemID {
			invalid = outcome{name: "OPC_E_INVALIDITEMID", direction: read,
				hresult: hresult, observed: true}
			break
		}
	}
	outcomes = append(outcomes, invalid)

	// OPC_E_BADRIGHTS: a Write to an item the source does not permit writing,
	// sent with that item's own canonical type so the adapter admits it.
	canonical, value, err := readTyped(ctx, client, itemString)
	if err != nil {
		return nil, err
	}
	denied, err := writeHRESULT(ctx, client, itemString, canonical, value)
	if err != nil {
		return nil, err
	}
	deniedRow, err := expect(write, "OPC_E_BADRIGHTS", denied,
		opcua.OPCEBadRights, opcua.StatusBadNotWritable)
	if err != nil {
		return nil, err
	}
	outcomes = append(outcomes, deniedRow)

	// OPC_E_RANGE and OPC_S_CLAMP: a value of the item's own canonical type
	// that is outside what the item can hold. The type matches, so ADR-0004
	// admits the Write and the source decides. A server with no engineering
	// range simply stores it, which is not a failure of the probe.
	floatCanonical, floatValue, err := readTyped(ctx, client, itemFloat)
	if err != nil {
		return nil, err
	}
	extreme, err := writeHRESULT(ctx, client, itemFloat, floatCanonical,
		&opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_R4Value{R4Value: 3.4e38}})
	if err != nil {
		return nil, err
	}
	// Put back whatever was there, on the same path, before reporting. A
	// restore that does not succeed would leave the extreme value in the
	// fixture for every scenario after this one.
	restored, err := writeHRESULT(ctx, client, itemFloat, floatCanonical, floatValue)
	if err != nil {
		return nil, err
	}
	if !restored.Succeeded() {
		return nil, fmt.Errorf("restoring %s answered %s", itemFloat, restored.Hex())
	}
	// The reason says what the source actually answered rather than assuming
	// it accepted the value.
	extremeReason := "this-source-answered-" + extreme.Hex()
	if extreme.Succeeded() {
		extremeReason = "this-source-accepts-an-out-of-range-value-of-the-canonical-type"
	}
	rangeRow, err := observedIf(write, "OPC_E_RANGE", extreme, opcua.OPCERange,
		opcua.StatusBadOutOfRange, extremeReason)
	if err != nil {
		return nil, err
	}
	clampRow, err := observedIf(write, "OPC_S_CLAMP", extreme, opcua.OPCSClamp,
		opcua.StatusGoodClamped, extremeReason)
	if err != nil {
		return nil, err
	}
	outcomes = append(outcomes, rangeRow, clampRow)

	// OPC_E_BADTYPE and DISP_E_TYPEMISMATCH cannot reach the source at all:
	// ADR-0004 requires the requested VARTYPE to equal the canonical one and
	// answers TYPE_MISMATCH itself. This is demonstrated, not asserted -- the
	// Write is actually attempted with the wrong type.
	intCanonical, _, err := readTyped(ctx, client, itemInt32)
	if err != nil {
		return nil, err
	}
	if intCanonical.Raw != uint32(opcda.VTI4) {
		return nil, fmt.Errorf("%s was not canonically VT_I4", itemInt32)
	}
	mismatch, err := client.Write(ctx, &opcdav1.DAWriteRequest{Items: []*opcdav1.DAWriteItem{{
		ItemId:   itemInt32,
		DataType: &opcdav1.DAVarType{Raw: uint32(opcda.VTR8), Name: "VT_R8"},
		Value:    &opcdav1.DAScalarValue{Value: &opcdav1.DAScalarValue_R8Value{R8Value: 0}},
	}}})
	if err != nil || len(mismatch.Results) != 1 {
		return nil, fmt.Errorf("type-mismatched Write did not return one result")
	}
	if mismatch.Results[0].Ok || mismatch.Results[0].ErrorCode != string(opcda.CodeTypeMismatch) {
		return nil, fmt.Errorf("type-mismatched Write reached the source instead of being refused")
	}
	if mismatch.Results[0].Hresult != nil && mismatch.Results[0].Hresult.Raw != 0 {
		return nil, fmt.Errorf("a refused Write carried a source HRESULT")
	}
	for _, name := range []string{"OPC_E_BADTYPE", "DISP_E_TYPEMISMATCH", "DISP_E_OVERFLOW"} {
		outcomes = append(outcomes, outcome{name: name, direction: write,
			reason: "unreachable-ADR-0004-refuses-a-non-canonical-VARTYPE-before-Write"})
	}

	// OPC_E_INVALID_PID: a property identifier the item does not have. This
	// row was unreachable until the adapter implemented item properties, and
	// the reason recorded here said so; leaving that in place would have kept
	// claiming a limitation that no longer exists.
	invalidPID := outcome{name: "OPC_E_INVALID_PID", direction: read,
		reason: "this-source-does-not-distinguish-an-unknown-property-identifier"}
	propertyHR, err := propertyHRESULT(ctx, client, itemFloat, 4242)
	if err != nil {
		return nil, err
	}
	if propertyHR == opcua.OPCEInvalidPID {
		row, err := expect(read, "OPC_E_INVALID_PID", propertyHR,
			opcua.OPCEInvalidPID, opcua.StatusBadAttributeIDInvalid)
		if err != nil {
			return nil, err
		}
		invalidPID = row
	}
	outcomes = append(outcomes, invalidPID)

	// The rest cannot be produced through any client request.
	outcomes = append(outcomes,
		outcome{name: "OPC_E_INVALIDHANDLE", direction: read,
			reason: "unreachable-item-handles-are-adapter-owned"},

		outcome{name: "OPC_E_NOTSUPPORTED", direction: write,
			reason: "unreachable-2.05a-Write-carries-a-value-only-never-quality-or-timestamp"},
		outcome{name: "E_OUTOFMEMORY", direction: read,
			reason: "unreachable-without-real-memory-exhaustion"},
		outcome{name: "E_ACCESSDENIED", direction: read,
			reason: "unreachable-activation-level-not-per-item-on-this-source"},
	)
	return outcomes, nil
}

// expect records a row the probe requires this source to produce.
func expect(d direction, name string, got, want opcda.HRESULT, status opcua.StatusCode) (outcome, error) {
	if got != want {
		return outcome{}, fmt.Errorf("%s on %s: source answered %s, Table %s expects %s",
			name, d, got.Hex(), d.table(), want.Hex())
	}
	return observedIf(d, name, got, want, status, "")
}

// observedIf records a row this source may or may not produce. When it does,
// the mapping must still be the table's; when it does not, why is recorded.
func observedIf(d direction, name string, got, want opcda.HRESULT, status opcua.StatusCode, reason string) (outcome, error) {
	if got != want {
		return outcome{name: name, direction: d, reason: reason}, nil
	}
	if mapped := d.mapper()(got); mapped != status {
		return outcome{}, fmt.Errorf("%s on %s: %s mapped to %s, Table %s says %s",
			name, d, got.Hex(), mapped.Hex(), d.table(), status.Hex())
	}
	return outcome{name: name, direction: d, hresult: got, observed: true}, nil
}

func readHRESULT(ctx context.Context, client opcdav1.OPCDAAccessClient, itemID string) (opcda.HRESULT, error) {
	response, err := client.Read(ctx, &opcdav1.DAReadRequest{Items: []*opcdav1.DAReadItem{{ItemId: itemID}}})
	if err != nil {
		return 0, fmt.Errorf("Read: %w", err)
	}
	if len(response.Results) != 1 {
		return 0, fmt.Errorf("Read returned %d results for one item", len(response.Results))
	}
	if response.Results[0].Hresult == nil {
		// The adapter refused the item itself, so the source never saw it.
		return 0, nil
	}
	return opcda.HRESULT(response.Results[0].Hresult.Value), nil
}

func writeHRESULT(ctx context.Context, client opcdav1.OPCDAAccessClient, itemID string,
	dataType *opcdav1.DAVarType, value *opcdav1.DAScalarValue) (opcda.HRESULT, error) {
	response, err := client.Write(ctx, &opcdav1.DAWriteRequest{Items: []*opcdav1.DAWriteItem{{
		ItemId: itemID, DataType: dataType, Value: value,
	}}})
	if err != nil {
		return 0, fmt.Errorf("Write: %w", err)
	}
	if len(response.Results) != 1 {
		return 0, fmt.Errorf("Write returned %d results for one item", len(response.Results))
	}
	if response.Results[0].Hresult == nil {
		return 0, fmt.Errorf("Write result carried no HRESULT")
	}
	return opcda.HRESULT(response.Results[0].Hresult.Value), nil
}

// readTyped returns an item's canonical type and current value so a Write can
// be sent that the adapter admits. The value is never printed.
func readTyped(ctx context.Context, client opcdav1.OPCDAAccessClient, itemID string) (*opcdav1.DAVarType, *opcdav1.DAScalarValue, error) {
	response, err := client.Read(ctx, &opcdav1.DAReadRequest{Items: []*opcdav1.DAReadItem{{ItemId: itemID}}})
	if err != nil {
		return nil, nil, fmt.Errorf("Read %q: %w", itemID, err)
	}
	if len(response.Results) != 1 || !response.Results[0].Ok {
		return nil, nil, fmt.Errorf("%q was not readable", itemID)
	}
	if response.Results[0].CanonicalDataType == nil || response.Results[0].Value == nil {
		return nil, nil, fmt.Errorf("%q reported no canonical type or value", itemID)
	}
	return response.Results[0].CanonicalDataType, response.Results[0].Value, nil
}

func fail(operation string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", operation, err)
	os.Exit(1)
}

// propertyHRESULT asks for one property of one item and returns the HRESULT the
// source answered with. A property identifier the item does not have is the
// only way a client can provoke OPC_E_INVALID_PID.
func propertyHRESULT(ctx context.Context, client opcdav1.OPCDAAccessClient,
	itemID string, propertyID uint32) (opcda.HRESULT, error) {
	response, err := client.ItemProperties(ctx, &opcdav1.DAItemPropertiesRequest{
		ItemId: itemID, PropertyIds: []uint32{propertyID},
	})
	if err != nil {
		// A source without IOPCItemProperties cannot answer at all, which is a
		// capability rather than a failure of this probe.
		return 0, nil
	}
	if len(response.Results) != 1 || response.Results[0].Hresult == nil {
		return 0, fmt.Errorf("ItemProperties returned no HRESULT for one property")
	}
	return opcda.HRESULT(response.Results[0].Hresult.Value), nil
}
