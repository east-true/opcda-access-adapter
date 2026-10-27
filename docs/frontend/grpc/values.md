# Scalar value representation

[Frontend overview](README.md) · [Documentation](../../README.md)

`DAScalarValue` is a protobuf `oneof`. Its field must match the explicit raw
VARTYPE on Write and the source VARTYPE on Read.

| OPC DA VARTYPE | protobuf field |
|---|---|
| `VT_EMPTY`, `VT_NULL` | `empty_or_null` |
| `VT_I1` | `i1_value` |
| `VT_UI1` | `ui1_value` |
| `VT_I2` | `i2_value` |
| `VT_UI2` | `ui2_value` |
| `VT_I4` | `i4_value` |
| `VT_UI4` | `ui4_value` |
| `VT_I8` | `i8_value` |
| `VT_UI8` | `ui8_value` |
| `VT_R4` | `r4_value` |
| `VT_R8` | `r8_value` |
| `VT_BOOL` | `bool_value` |
| `VT_BSTR` | `bstr_value` |
| `VT_ERROR` | `error_value` |
| `VT_INT` | `int_value` |
| `VT_UINT` | `uint_value` |

The VARTYPE, not the protobuf numeric container, is the source of truth for
the original COM width. Protobuf float and double preserve NaN and infinities
without a JSON-specific encoding. Unsupported scalar types and BYREF values
fail explicitly and are never coerced.

## Array values

A SAFEARRAY travels in `array_value`, a field of its own beside `value`:

```proto
message DAArrayValue {
  DAVarType element_data_type = 1;
  repeated DADimension dimensions = 2;   // lower_bound, length
  repeated DAScalarValue elements = 3;
}
```

Exactly one of `value` and `array_value` is set, and `data_type`'s `array` bit
says which — so a client that only understands scalars can tell an array apart
from a value that was not carried. The field was added rather than folded into
a `oneof` with `value`: renumbering or re-typing a field clients already decode
would break every existing one.

`elements` is flat and ordered so the **last dimension varies fastest**, so a
client can rebuild the array from the description alone. `lower_bound` is the
one the source reported and is never normalised to zero, because a client
writing back to an index it read must reach the element it read. Protobuf
carries NaN and infinities natively, so an array element needs no encoding of
its own.

A Write supplies the same message, with `data_type` naming the array VARTYPE.
`element_data_type` may be given and must then agree; leaving it out is not a
disagreement. A message whose elements and dimensions do not describe each
other is refused rather than reshaped.

[ADR-0019](../../design/adr/0019-safearray-representation.md) records why the shape is
carried rather than flattened. Array support is **not validated against any
real OPC DA source**; see [implementation status](../../implementation-status.md).

Source time uses signed Unix seconds and nanoseconds only when
`timestamp_present` is true. Absence remains absence; the adapter does not
insert its clock.
