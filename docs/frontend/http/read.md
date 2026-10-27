# Device Read

[Frontend overview](README.md) · [Documentation](../../README.md)

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
| `VT_ARRAY` of a supported element type | Shape and elements with `valueEncoding: array` |

`VT_CY`, `VT_DATE`, `VT_DECIMAL`, `VT_VARIANT`, byref values, invalid UTF-16
BSTRs, and unknown types are explicit item-level errors. Arrays retain their
element type, dimensions, and lower bounds; see the [array format](write.md#array-values).
No real DA source has yet validated the array path. Returned VARIANTs are
cleared even when their value type is unsupported.
