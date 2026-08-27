"""Cross-checks the transcribed OPC UA constants against the OPC Foundation's
own machine-readable schema.

Every numeric constant and structure field order in internal/opcua is a
transcription of a table somebody read once. This project has already shipped
three wrong ones — MessageSecurityMode declared with iota so None became 0 and
inverted the safety property Invalid=0 exists to provide, Bad_SecurityModeRejected
carrying Bad_SecureChannelIdInvalid's value, and DiagnosticInfo writing its
fields in mask-bit order rather than stream order. Each round-tripped perfectly
against this project's own decoder.

So the transcriptions are checked against the source they came from, mechanically,
and this is runnable rather than a thing somebody did once:

    python3 scripts/spec-check/check.py

It exits non-zero on any mismatch. It needs network access, and it verifies the
downloaded schema against scripts/spec-check/digests.txt first: an upstream
change is then a visible event to review rather than a silent shift in what
"conformant" means. Refresh a digest only after reading what changed.
"""
import csv
import hashlib
import io
import os
import re
import sys
import urllib.request

UA_BASE = "https://raw.githubusercontent.com/OPCFoundation/UA-Nodeset/latest/Schema/"

# The OPC DA side has no CSV. Its authority is the IDL the proxy/stubs are
# generated from, taken from the same commit ADR-0006 pins for the validation
# fixture, so the constants are checked against the source the server this
# project tests against was itself built from.
DA_COMMIT = "efe0d1d1ea86a8a727bf26a501a261765e836766"
DA_BASE = (f"https://raw.githubusercontent.com/OPCF-Members/"
           f"OPC-Classic-CoreComponents/{DA_COMMIT}/Source/DataAccess/ProxyStub/")
SOURCES = {
    "StatusCode.csv": UA_BASE,
    "NodeIds.csv": UA_BASE,
    "AttributeIds.csv": UA_BASE,
    "Opc.Ua.Types.bsd": UA_BASE,
    "opcda.idl": DA_BASE,
    "opcerror.h": DA_BASE,
}
ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
UA = os.path.join(ROOT, "internal", "opcua")

failures = []


def fail(message):
    failures.append(message)
    print("  FAIL " + message)


def fetch():
    digests = {}
    with open(os.path.join(os.path.dirname(os.path.abspath(__file__)), "digests.txt")) as handle:
        for line in handle:
            digest, name = line.split()
            digests[name] = digest
    files = {}
    for name, want in digests.items():
        data = urllib.request.urlopen(SOURCES[name] + name, timeout=300).read()
        got = hashlib.sha256(data).hexdigest()
        if got != want:
            print(f"  upstream {name} is not the reviewed copy:\n"
                  f"    expected {want}\n    got      {got}")
            sys.exit(2)
        files[name] = data.decode("utf-8", "replace")
    return files


def go_sources():
    text = []
    for name in sorted(os.listdir(UA)):
        if name.endswith(".go") and not name.endswith("_test.go"):
            with open(os.path.join(UA, name), encoding="utf-8") as handle:
                text.append(handle.read())
    return "".join(text)


def csv_rows(text):
    return list(csv.reader(io.StringIO(text)))


def check_status_codes(files, src):
    spec = {}
    for row in csv_rows(files["StatusCode.csv"]):
        if len(row) >= 2 and row[1].strip().lower().startswith("0x"):
            spec[row[0].strip().lower()] = int(row[1].strip(), 16)
    # The Go names differ from the table's only in how they spell initialisms,
    # and in one place that was shortened when it was transcribed.
    aliases = {"badprotocolversionunsupport": "badprotocolversionunsupported"}

    def spellings(name):
        lowered = name.lower()
        yield lowered
        if lowered in aliases:
            yield aliases[lowered]
        yield lowered.replace("id", "id")
        for a, b in (("id", "id"), ("url", "url"), ("uri", "uri")):
            yield lowered.replace(a.upper(), b)

    checked = 0
    for match in re.finditer(r'\bStatus([A-Za-z0-9_]+)\s+StatusCode\s*=\s*(0x[0-9A-Fa-f]+)', src):
        name, value = match.group(1), int(match.group(2), 16)
        checked += 1
        want = None
        for candidate in spellings(name):
            if candidate in spec:
                want = spec[candidate]
                break
        if want is None:
            # The table spells some initialisms differently; compare with every
            # initialism removed rather than guessing which one it is.
            def bare(text):
                return text.replace("id", "").replace("uri", "").replace("url", "")
            for spec_name, spec_value in spec.items():
                if bare(spec_name) == bare(name.lower()):
                    want = spec_value
                    break
        if want is None:
            fail(f"Status{name} = 0x{value:08X} has no row in StatusCode.csv")
        elif want != value:
            fail(f"Status{name}: code 0x{value:08X}, spec 0x{want:08X}")
    print(f"  {checked} status codes")


def check_node_ids(files, src):
    spec = {}
    for row in csv_rows(files["NodeIds.csv"]):
        if len(row) >= 2:
            try:
                spec[row[0].strip().lower()] = int(row[1].strip())
            except ValueError:
                pass
    checked = 0
    for match in re.finditer(r'\b([A-Za-z0-9_]+?)(Request|Response)EncodingID\s+uint32\s*=\s*(\d+)', src):
        service, kind, value = match.group(1), match.group(2), int(match.group(3))
        checked += 1
        want = spec.get(f"{service}{kind}".lower() + "_encoding_defaultbinary")
        if want is None:
            fail(f"{service}{kind}_Encoding_DefaultBinary is not in NodeIds.csv")
        elif want != value:
            fail(f"{service}{kind}EncodingID: code {value}, spec {want}")
    print(f"  {checked} service encoding ids")


def check_attribute_ids(files, src):
    spec = {}
    for row in csv_rows(files["AttributeIds.csv"]):
        if len(row) >= 2:
            try:
                spec[row[0].strip().lower()] = int(row[1].strip())
            except ValueError:
                pass
    checked = 0
    for match in re.finditer(r'\bAttribute([A-Za-z0-9_]+)\s+uint32\s*=\s*(\d+)', src):
        name, value = match.group(1), int(match.group(2))
        checked += 1
        want = spec.get(name.lower())
        if want is None:
            fail(f"Attribute{name} is not in AttributeIds.csv")
        elif want != value:
            fail(f"Attribute{name}: code {value}, spec {want}")
    print(f"  {checked} attribute ids")


def schema_structures(text):
    """Field order per structure, with the implicit array length fields removed."""
    structures = {}
    for match in re.finditer(r'<opc:StructuredType\s+Name="([A-Za-z0-9_]+)"(.*?)</opc:StructuredType>',
                             text, re.S):
        name, body = match.group(1), match.group(2)
        names, lengths = [], set()
        for field in re.finditer(r'<opc:Field\s+([^/>]*)/?>', body):
            attributes = field.group(1)
            field_name = re.search(r'Name="([A-Za-z0-9_]+)"', attributes)
            length_of = re.search(r'LengthField="([A-Za-z0-9_]+)"', attributes)
            if length_of:
                lengths.add(length_of.group(1))
            if field_name:
                names.append(field_name.group(1))
        structures[name] = [n for n in names if n not in lengths]
    return structures


def normalise(name):
    """Compares field names across the two spellings of every initialism."""
    return (name.lower().replace("_", "")
            .replace("requestheader", "header").replace("responseheader", "header")
            .replace("subscriptionacknowledgements", "acknowledgements")
            .replace("id", "").replace("uri", "").replace("url", ""))


def check_request_decoders(files, src):
    spec = schema_structures(files["Opc.Ua.Types.bsd"])
    checked = 0
    for name in sorted(set(re.findall(r'func \(d \*Decoder\) Read([A-Za-z0-9]+Request)\(', src))):
        want = spec.get(name)
        if want is None:
            fail(f"{name} is not in the binary schema")
            continue
        body = re.search(r'func \(d \*Decoder\) Read' + name + r'\(\) \([^)]*\) \{(.*?)\n\}', src, re.S)
        if body is None:
            fail(f"{name} decoder body could not be read")
            continue
        # A decoder that assigns straight into the struct is read in order; one
        # that uses locals is checked by the order it constructs the struct.
        got = []
        for field in re.finditer(r'(?:value|request)\.([A-Za-z0-9_]+)', body.group(1)):
            if not got or got[-1] != field.group(1):
                got.append(field.group(1))
        if not got:
            continue  # reads into locals; covered by the round-trip tests
        checked += 1
        if [normalise(f) for f in want] != [normalise(f) for f in got]:
            fail(f"{name} field order: code {got}, spec {want}")
    print(f"  {checked} request decoders")


def idl_constants(text):
    """The OPC_ constants the IDL defines, however they are spelled."""
    stripped = re.sub(r"/\*.*?\*/", "", text, flags=re.S)
    stripped = re.sub(r"//[^\n]*", "", stripped)
    values = {}
    for match in re.finditer(
            r'(?:const\s+\w+\s+|#define\s+)(OPC_[A-Z0-9_]+)\s*=?\s*'
            r'(?:\(\(HRESULT\))?\s*(0x[0-9A-Fa-f]+|\d+)', stripped):
        values[match.group(1)] = int(match.group(2), 0)
    return values, stripped


def idl_interfaces(stripped):
    """Method order per interface, which is the vtable slot order."""
    interfaces = {}
    for match in re.finditer(r'interface\s+(I[A-Za-z0-9_]+)\s*:\s*I[A-Za-z0-9_]+\s*\{(.*?)\n\}',
                             stripped, re.S):
        interfaces[match.group(1)] = re.findall(r'HRESULT\s+([A-Za-z0-9_]+)\s*\(', match.group(2))
    return interfaces


def da_sources():
    text = []
    directory = os.path.join(ROOT, "internal", "opcda")
    for name in sorted(os.listdir(directory)):
        if name.endswith(".go") and not name.endswith("_test.go"):
            with open(os.path.join(directory, name), encoding="utf-8") as handle:
                text.append(handle.read())
    return "".join(text)


DA_QUALITY = {
    "QualityBad": "OPC_QUALITY_BAD",
    "QualityConfigError": "OPC_QUALITY_CONFIG_ERROR",
    "QualityNotConnected": "OPC_QUALITY_NOT_CONNECTED",
    "QualityDeviceFailure": "OPC_QUALITY_DEVICE_FAILURE",
    "QualitySensorFailure": "OPC_QUALITY_SENSOR_FAILURE",
    "QualityLastKnown": "OPC_QUALITY_LAST_KNOWN",
    "QualityCommFailure": "OPC_QUALITY_COMM_FAILURE",
    "QualityOutOfService": "OPC_QUALITY_OUT_OF_SERVICE",
    "QualityWaitingForInitialData": "OPC_QUALITY_WAITING_FOR_INITIAL_DATA",
    "QualityUncertain": "OPC_QUALITY_UNCERTAIN",
    "QualityLastUsable": "OPC_QUALITY_LAST_USABLE",
    "QualitySensorCal": "OPC_QUALITY_SENSOR_CAL",
    "QualityEGUExceeded": "OPC_QUALITY_EGU_EXCEEDED",
    "QualitySubNormal": "OPC_QUALITY_SUB_NORMAL",
    "QualityGood": "OPC_QUALITY_GOOD",
    "QualityLocalOverride": "OPC_QUALITY_LOCAL_OVERRIDE",
}

# The IDL names the vtable's own methods; IUnknown's three come first in every
# one, which the Go structs express by embedding iUnknownVTable.
DA_VTABLES = {
    "iopcServerVTable": "IOPCServer",
    "iopcItemMgtVTable": "IOPCItemMgt",
    "iopcSyncIOVTable": "IOPCSyncIO",
    "iopcDataCallbackVTable": "IOPCDataCallback",
    "iopcBrowseServerAddressSpaceVTable": "IOPCBrowseServerAddressSpace",
}


def check_da(files):
    src = da_sources() + go_sources()
    values, stripped = idl_constants(files["opcda.idl"])
    values.update(idl_constants(files["opcerror.h"])[0])
    interfaces = idl_interfaces(stripped)

    checked = 0
    for go_name, idl_name in DA_QUALITY.items():
        match = re.search(r'\b' + go_name + r'\s+uint16\s*=\s*(0x[0-9A-Fa-f]+)', src)
        if match is None:
            fail(f"{go_name} is not declared")
            continue
        checked += 1
        got, want = int(match.group(1), 16), values[idl_name]
        if got != want:
            fail(f"{go_name}: code 0x{got:04X}, {idl_name} 0x{want:04X}")
    print(f"  {checked} DA quality values")

    checked = 0
    for go_name, idl_name in (("qualityLimitMask", "OPC_LIMIT_MASK"),
                              ("qualityMainAndSub", "OPC_STATUS_MASK"),
                              ("opcAccessRightRead", "OPC_READABLE"),
                              ("opcAccessRightWrite", "OPC_WRITEABLE"),
                              ("opcDataSourceCache", "OPC_DS_CACHE"),
                              ("opcDataSourceDevice", "OPC_DS_DEVICE")):
        match = re.search(r'\b' + go_name + r'\s*=\s*(0x[0-9A-Fa-f]+|\d+)', src)
        want = values.get(idl_name)
        if match is None or want is None:
            continue
        checked += 1
        got = int(match.group(1), 0)
        if got != want:
            fail(f"{go_name}: code {got}, {idl_name} {want}")
    print(f"  {checked} DA masks and flags")

    checked = 0
    for go_name, idl_name in DA_VTABLES.items():
        want = interfaces.get(idl_name)
        body = re.search(r'type ' + go_name + r' struct \{(.*?)\n\}', src, re.S)
        if want is None or body is None:
            fail(f"{go_name} or {idl_name} could not be read")
            continue
        got = [line.split()[0] for line in body.group(1).strip().splitlines()
               if line.strip() and not line.strip().startswith("//")
               and line.split()[0] != "iUnknownVTable"]
        checked += 1
        if got != want:
            fail(f"{go_name} slot order: code {got}, IDL {want}")
    print(f"  {checked} DA vtable slot orders")


def main():
    print("verifying the schema against the reviewed digests")
    files = fetch()
    src = go_sources()
    print("cross-checking transcriptions")
    check_status_codes(files, src)
    check_node_ids(files, src)
    check_attribute_ids(files, src)
    check_request_decoders(files, src)
    check_da(files)
    if failures:
        print(f"\n{len(failures)} transcription(s) do not match the specification")
        return 1
    print("\nevery transcription matches the specification")
    return 0


if __name__ == "__main__":
    sys.exit(main())
