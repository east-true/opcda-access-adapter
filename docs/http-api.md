# v0 HTTP API

The v0 listener defaults to `127.0.0.1:8080`. All request bodies and batch
sizes are bounded. ItemIDs are passed to OPC DA exactly as decoded; the
adapter does not trim, rename, normalize, or infer them.

## Status

```text
GET /v1/status
```

Status reports runtime, source, connection generation, capabilities, Write
enablement, and listener state. It never contains process values.

## Device Read

```text
POST /v1/read
Content-Type: application/json
```

```json
{
  "source": "device",
  "items": [
    {"itemId": "Random.Int2"},
    {"itemId": "Random.Real8"}
  ]
}
```

`source` defaults to `device`; v0 rejects every other value. Results remain in
request order. A method-level COM failure is a source-layer HTTP error. An
item-level HRESULT, including a partial failure, remains in that item result
and the HTTP response is `200`.

Successful item fields include the actual VARIANT `dataType`, the AddItems
`canonicalDataType`, value encoding, raw 16-bit Quality, source timestamp and
its presence, raw/decoded access rights, and decimal/hex HRESULT. A zero
`FILETIME` is represented as timestamp-absent; adapter time is never
substituted.

## Read value representation

| VARTYPE | v0 HTTP representation |
|---|---|
| `VT_EMPTY`, `VT_NULL` | JSON `null`, distinguished by `dataType` |
| `VT_I1/UI1/I2/UI2/I4/UI4/INT/UINT/ERROR` | JSON integer |
| `VT_I8/UI8` | decimal JSON string |
| finite `VT_R4/R8` | JSON number |
| NaN, positive/negative infinity | string with `valueEncoding: float-special` |
| `VT_BOOL` | JSON boolean |
| valid UTF-16 `VT_BSTR` within the configured bound | JSON string |

`VT_CY`, `VT_DATE`, `VT_DECIMAL`, `VT_VARIANT`, arrays, byref values, invalid
UTF-16 BSTRs, and unknown types are explicit item-level errors in the current
v0 implementation. Arrays are not flattened or silently converted. Returned
VARIANTs are still cleared even when their value type is unsupported.

## Errors

Error bodies distinguish `frontend`, `adapter`, and `source` layers. Source
method errors include the raw HRESULT. Item errors are not replaced by a
generic request error.

## Browse

```text
POST /v1/browse
Content-Type: application/json
```

```json
{
  "path": ["Channel1", "Device1"],
  "filter": "all"
}
```

`path: []` means root. The path is a navigation sequence, not an ItemID and
is never used to infer one. `filter` is `all`, `branch`, or `item` and defaults
to `all`.

The runtime resets a hierarchical server to root and walks each source browse
name serially on the DA thread. Flat namespaces accept only the root path.
Item entries use the exact ItemID returned by `GetItemID`; branch entries do
not invent an ItemID. Canonical type and access rights are omitted when this
Browse interface does not supply them.

`IOPCBrowseServerAddressSpace` is optional. A server returning
`E_NOINTERFACE` reports Browse as `unsupported`, while known-ItemID Read stays
available. Exceeding the hard entry limit fails with
`BROWSE_RESULT_LIMIT_EXCEEDED`; results are never silently truncated.

## Typed value Write

```text
POST /v1/write
Content-Type: application/json
```

```json
{
  "items": [
    {
      "itemId": "Random.Int2",
      "dataType": "VT_I2",
      "valueEncoding": "json",
      "value": 42
    }
  ]
}
```

Write is value-only and disabled by default. Set `OPCDA_WRITE_ENABLED=true`
only for an explicitly authorized source. While disabled, the endpoint returns
HTTP 403 with `WRITE_DISABLED` before admitting source work.

The supplied VARTYPE must exactly equal the canonical VARTYPE obtained during
item registration. The adapter performs no canonical-type conversion. The
v0 Write representations are:

| VARTYPE | Required representation |
|---|---|
| `VT_EMPTY`, `VT_NULL` | JSON `null` |
| `VT_I1/UI1/I2/UI2/I4/UI4/INT/UINT/ERROR` | in-range JSON integer |
| `VT_I8/UI8` | in-range decimal JSON string |
| finite `VT_R4/R8` | JSON number |
| non-finite `VT_R4/R8` | `"NaN"`, `"+Infinity"`, or `"-Infinity"` with `valueEncoding: float-special` |
| `VT_BOOL` | JSON boolean |
| `VT_BSTR` | bounded JSON string |

Other types are explicitly unsupported. The result order matches the request,
and per-item source HRESULTs remain in HTTP 200 responses. `TYPE_MISMATCH` is
an item-level adapter error. A method-level HRESULT is a source-layer error.
The adapter makes one source Write call per admitted batch and never retries,
replays, or promises rollback. If the frontend deadline expires after COM has
started, the source outcome is unknown.

There is no Subscribe endpoint in v0.
