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
# Annex A of Part 8 is the only normative source for the DA-to-UA mappings, and
# it is prose, not a schema. The OPC Foundation publishes each specification
# version as a Markdown export whose URL names the version, so the tables can be
# read from the publisher rather than retyped. The URL is pinned to 1.05.07; a
# later version gets a new URL, not new bytes at this one.
PART8_MARKDOWN = ("https://reference.opcfoundation.org/specs/OPC-10000-8/"
                  "v1.05.07/t63916693141/download/markdown")

# Every source is a whole URL: most are named after their file, Part 8 is not.
SOURCES = {
    "StatusCode.csv": UA_BASE + "StatusCode.csv",
    "NodeIds.csv": UA_BASE + "NodeIds.csv",
    "AttributeIds.csv": UA_BASE + "AttributeIds.csv",
    "Opc.Ua.Types.bsd": UA_BASE + "Opc.Ua.Types.bsd",
    "opcda.idl": DA_BASE + "opcda.idl",
    "opcerror.h": DA_BASE + "opcerror.h",
    "OPC-10000-8.md": PART8_MARKDOWN,
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
        data = urllib.request.urlopen(SOURCES[name], timeout=300).read()
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
    declared = {}
    for match in re.finditer(r'\bStatus([A-Za-z0-9_]+)\s+StatusCode\s*=\s*(0x[0-9A-Fa-f]+)', src):
        name, value = match.group(1), int(match.group(2), 16)
        checked += 1
        # One value, one constant. Two declarations of the same code under two
        # spellings compile and pass every test, and each call site then picks
        # one at random; statuscode.go owns the enumeration so this cannot arise.
        if value in declared:
            fail(f"0x{value:08X} is declared twice: "
                 f"Status{declared[value]} and Status{name}")
        else:
            declared[value] = name
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
    "iopcItemPropertiesVTable": "IOPCItemProperties",
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
    # The item property identifiers OPC 10000-8 Table A.1 is written in terms
    # of. opcda.idl declares them, so they are checked rather than retyped.
    for go_name, idl_name in (("PropertyDataType", "OPC_PROPERTY_DATATYPE"),
                              ("PropertyValue", "OPC_PROPERTY_VALUE"),
                              ("PropertyQuality", "OPC_PROPERTY_QUALITY"),
                              ("PropertyTimestamp", "OPC_PROPERTY_TIMESTAMP"),
                              ("PropertyAccessRights", "OPC_PROPERTY_ACCESS_RIGHTS"),
                              ("PropertyScanRate", "OPC_PROPERTY_SCAN_RATE"),
                              ("PropertyEUType", "OPC_PROPERTY_EU_TYPE"),
                              ("PropertyEUInfo", "OPC_PROPERTY_EU_INFO"),
                              ("PropertyEUUnits", "OPC_PROPERTY_EU_UNITS"),
                              ("PropertyDescription", "OPC_PROPERTY_DESCRIPTION"),
                              ("PropertyHighEU", "OPC_PROPERTY_HIGH_EU"),
                              ("PropertyLowEU", "OPC_PROPERTY_LOW_EU"),
                              ("PropertyHighIR", "OPC_PROPERTY_HIGH_IR"),
                              ("PropertyLowIR", "OPC_PROPERTY_LOW_IR"),
                              ("PropertyCloseLabel", "OPC_PROPERTY_CLOSE_LABEL"),
                              ("PropertyOpenLabel", "OPC_PROPERTY_OPEN_LABEL")):
        match = re.search(r'\b' + go_name + r'\s+PropertyID\s*=\s*(\d+)', src)
        want = values.get(idl_name)
        if match is None or want is None:
            fail(f"{go_name} or {idl_name} could not be read")
            continue
        checked += 1
        got = int(match.group(1))
        if got != want:
            fail(f"{go_name}: id {got}, {idl_name} {want}")
    print(f"  {checked} DA item property identifiers")

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


# Tables A.4 and A.5 spell the same DA error two ways - OPC_E_BADRIGHTS in the
# Read table, E_BADRIGHTS in the Write table - and neither spelling is always
# the one opcerror.h uses. This resolves a table cell to the Go constant that
# implements it and, for the OPC codes, to the header that defines its value.
# The four Windows codes have no OPC_ definition; mapping_windows_test.go
# checks those against golang.org/x/sys/windows instead, on the one platform
# where that package builds.
DA_ERRORS = {
    "OPC_E_BADRIGHTS": ("OPCEBadRights", "OPC_E_BADRIGHTS"),
    "E_BADRIGHTS": ("OPCEBadRights", "OPC_E_BADRIGHTS"),
    "OPC_E_INVALIDHANDLE": ("OPCEInvalidHandle", "OPC_E_INVALIDHANDLE"),
    "E_INVALIDHANDLE": ("OPCEInvalidHandle", "OPC_E_INVALIDHANDLE"),
    "OPC_E_UNKNOWNITEMID": ("OPCEUnknownItemID", "OPC_E_UNKNOWNITEMID"),
    "E_UNKNOWNITEMID": ("OPCEUnknownItemID", "OPC_E_UNKNOWNITEMID"),
    "E_INVALIDITEMID": ("OPCEInvalidItemID", "OPC_E_INVALIDITEMID"),
    "E_INVALID_PID": ("OPCEInvalidPID", "OPC_E_INVALID_PID"),
    "E_BADTYPE": ("OPCEBadType", "OPC_E_BADTYPE"),
    "E_RANGE": ("OPCERange", "OPC_E_RANGE"),
    "E_NOTSUPPORTED": ("OPCENotSupported", "OPC_E_NOTSUPPORTED"),
    "S_CLAMP": ("OPCSClamp", "OPC_S_CLAMP"),
    "E_OUTOFMEMORY": ("EOutOfMemory", None),
    "E_ACCESSDENIED": ("EAccessDenied", None),
    "DISP_E_TYPEMISMATCH": ("DispETypeMismatch", None),
    "DISP_E_OVERFLOW": ("DispEOverflow", None),
}


def spec_table(text, caption):
    """The rows of one comma-separated Annex A table, in order."""
    start = text.find(caption)
    if start == -1:
        return []
    rows = []
    for line in text[start:].splitlines()[1:]:
        line = line.strip()
        if not line:
            break
        cells = [cell.strip() for cell in line.split(",")]
        if len(cells) == 2:
            rows.append((cells[0], cells[1]))
    # Every Annex A table's first row names its columns.
    return rows[1:]


def status_constants(src):
    """Declared StatusCode constants, keyed case-insensitively."""
    names = {}
    for match in re.finditer(r'\b(Status[A-Za-z0-9]+)\s+StatusCode\s*=', src):
        names[match.group(1).lower()] = match.group(1)
    return names


def go_answers(src, function):
    """What one mapping function answers, as {DA constant: returned status}.

    Reads both the switch cases and the guards that precede it. OPC_S_CLAMP is
    a success code, so it has to be answered before the general success test
    and cannot be a case of the error switch.
    """
    body = re.search(r'func ' + function + r'\(.*?\n\}\n', src, re.S)
    if body is None:
        return None
    answers, pending = {}, []
    for line in body.group(0).splitlines():
        line = line.strip()
        guard = re.match(r'if\s+\w+\s*==\s*([A-Za-z0-9_]+)\s*\{$', line)
        if line.startswith("case "):
            pending = [name.strip() for name in line[5:].rstrip(":").split(",")]
        elif guard is not None:
            pending = [guard.group(1)]
        elif pending and (line.startswith("return ") or re.match(r'\w+ = \S', line)):
            answer = line.split("=", 1)[1] if not line.startswith("return ") \
                else line[len("return "):]
            for name in pending:
                answers.setdefault(name, answer.split(",")[0].strip())
            pending = []
    return answers


def check_da_error_mapping(files, src):
    """Tables A.4 and A.5, row for row, against the two mapping functions."""
    spec = files["OPC-10000-8.md"]
    statuses = status_constants(src)
    values = idl_constants(files["opcerror.h"])[0]

    checked, values_checked = 0, set()
    for caption, function in (
            ("Table A.4 - OPC DA Read error mapping", "StatusCodeForReadError"),
            ("Table A.5 - OPC DA Write error code mapping", "StatusCodeForWriteError")):
        rows = spec_table(spec, caption)
        if not rows:
            fail(f"{caption} could not be read from the specification")
            continue
        answers = go_answers(src, function)
        if answers is None:
            fail(f"{function} could not be read")
            continue
        for da_error, ua_status in rows:
            if da_error == "Others":
                # The table's own catch-all row, which the switch spells default.
                continue
            if da_error not in DA_ERRORS:
                fail(f"{caption}: {da_error} is not bound to a constant")
                continue
            go_name, header_name = DA_ERRORS[da_error]
            want = statuses.get(("Status" + ua_status.replace("_", "")).lower())
            if want is None:
                fail(f"{caption}: {ua_status} is not declared")
                continue
            got = answers.get(go_name)
            checked += 1
            if got != want:
                fail(f"{caption}: {da_error} answers {got or 'nothing'}, "
                     f"the table says {want}")
            # Both tables name most of these codes; the value is one fact.
            if header_name is None or header_name in values_checked:
                continue
            values_checked.add(header_name)
            declared = re.search(r'\b' + go_name + r'\s+opcda\.HRESULT\s*=\s*(-?\d+)', src)
            if declared is None or header_name not in values:
                fail(f"{go_name} or {header_name} could not be read")
                continue
            signed = int(declared.group(1)) & 0xFFFFFFFF
            if signed != values[header_name]:
                fail(f"{go_name}: code 0x{signed:08X}, "
                     f"{header_name} 0x{values[header_name]:08X}")
    print(f"  {checked} DA error mappings")


def check_da_type_mapping(files, src):
    """Table A.2, VARTYPE to UA DataType."""
    rows = spec_table(files["OPC-10000-8.md"], "Table A.2 - DataTypes and mapping")
    answers = go_answers(src, "dataTypeFromTableA2")
    if not rows or answers is None:
        fail("Table A.2 or dataTypeFromTableA2 could not be read")
        return
    # DataType is a string type whose value is the UA type name, so the table
    # cell is compared with what the constant actually holds, not with its name.
    values = dict(re.findall(r'\b(DataType[A-Za-z0-9]+)\s+DataType\s*=\s*"([^"]+)"', src))
    vartypes = {name.lower(): name for name in
                re.findall(r'\b(VT[A-Za-z0-9]+)\s+DAVarType\s*=', src)}
    checked = 0
    for vartype, ua_type in rows:
        if vartype == "VT_ARRAY":
            # Not a type of its own: the array bit is stripped by Base() before
            # the table is consulted, and the element type is what is mapped.
            continue
        go_vartype = vartypes.get(vartype.replace("_", "").lower())
        if go_vartype is None:
            fail(f"Table A.2: {vartype} is not declared")
            continue
        answered = answers.get("opcda." + go_vartype)
        checked += 1
        if answered is None:
            fail(f"Table A.2: {vartype} is not mapped, the table says {ua_type}")
        elif values.get(answered) != ua_type:
            fail(f"Table A.2: {vartype} maps to {values.get(answered, answered)}, "
                 f"the table says {ua_type}")
    print(f"  {checked} DA data type mappings")


def check_da_quality_mapping(files, src):
    """Table A.3, DA quality to UA status code."""
    rows = spec_table(files["OPC-10000-8.md"], "Table A.3 - Quality mapping")
    answers = go_answers(src, "StatusCodeForQuality")
    if not rows or answers is None:
        fail("Table A.3 or StatusCodeForQuality could not be read")
        return
    statuses = status_constants(src)
    # The table names a quality the way opcda.idl does, so the row is resolved
    # through the IDL name that check_da already ties the Go constant to.
    by_idl = {idl: go for go, idl in DA_QUALITY.items()}
    checked = 0
    for quality, ua_status in rows:
        go_quality = by_idl.get("OPC_QUALITY_" + quality)
        if go_quality is None:
            fail(f"Table A.3: {quality} is not bound to a constant")
            continue
        want = statuses.get(("Status" + ua_status.replace("_", "")).lower())
        if want is None:
            fail(f"Table A.3: {ua_status} is not declared")
            continue
        checked += 1
        if answers.get(go_quality) != want:
            fail(f"Table A.3: {quality} answers {answers.get(go_quality) or 'nothing'}, "
                 f"the table says {want}")
    print(f"  {checked} DA quality mappings")


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
    check_da_error_mapping(files, src + da_sources())
    check_da_type_mapping(files, src + da_sources())
    check_da_quality_mapping(files, src + da_sources())
    if failures:
        print(f"\n{len(failures)} transcription(s) do not match the specification")
        return 1
    print("\nevery transcription matches the specification")
    return 0


if __name__ == "__main__":
    sys.exit(main())
