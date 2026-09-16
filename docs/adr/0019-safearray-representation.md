# ADR-0019: carrying a SAFEARRAY without losing its shape

- Status: **Accepted**, representation decided; implementation staged
- Date: 2026-09-16
- Relates to: [ADR-0003](0003-read-types-and-filetime-presence.md),
  [ADR-0016](0016-opcua-frontend-scope-and-mapping.md),
  [ADR-0018](0018-da-item-properties.md)

## Context

A DA item's value is a `VARIANT`, and a `VARIANT` may carry a `SAFEARRAY`. The
adapter carries none: `decodeVariant` answers `UNSUPPORTED_VARTYPE` for any
`VT_ARRAY` or `VT_BYREF` value, so an item whose source reports an array cannot
be read at all through any frontend.

That is not a defect. `docs/design.md` §20.4 permits it in as many words --
"v0에서 완전한 SAFEARRAY support가 없다면 명시적으로 unsupported 처리한다" --
and INV-10 requires the refusal to be explicit rather than a coercion. The
support matrix row says the same conditionally: `SAFEARRAY | v0 unsupported
unless full dimensional metadata preserved`.

What §20.4 forbids is a *lossy* implementation. Five things must survive:

- element VARTYPE
- dimension count
- lower bound per dimension
- length per dimension
- element values

and it names the shortcut that loses them: "따라서 'JSON array로 flat하게
만들기'는 허용되지 않는다."

The cost of carrying none of it is already visible elsewhere in this repository.
`docs/implementation-status.md` records that one of OPC 10000-8 A.3.1.4's rules
is unapplied because an array-valued DA property cannot be exposed, and that
`MultiStateDiscreteType` is deliberately not claimed because its `EnumStrings`
is an array-valued property the DA layer does not carry. Both are consequences
of this gap rather than of anything the sources do.

`docs/design.md`'s non-goal list is sometimes read as ruling arrays out. It does
not. The entry is "full SAFEARRAY support**를 검증 전 약속**" -- what is out of
scope is *promising* the support before it has been validated, which is a
promise-discipline item alongside `production auth/RBAC`. The open-questions
list carries "SAFEARRAY support timing" explicitly. The timing was left open;
this ADR decides the representation so the implementation can proceed, and
decides how the unvalidated status is to be stated.

## Decision

### 1. The DA layer carries an array as a described value, not as a slice

A decoded array is a value of its own type rather than a Go slice, because a
slice carries lengths and nothing else:

```go
type DAArray struct {
	ElementType DAVarType       // the VARTYPE without VT_ARRAY/VT_BYREF
	Dimensions  []DADimension   // in COM dimension order, 1..cDims
	Elements    []any           // the Go scalars decodeVariant already produces
}

type DADimension struct {
	LowerBound int32
	Length     uint32
}
```

`Elements` holds exactly `product(Dimensions[i].Length)` values, each one the
same Go type `decodeVariant` produces for that VARTYPE as a scalar. Nothing is
widened, narrowed, or re-typed on the way in, for the same reason ADR-0003 gives
for scalars.

### 2. Elements are addressed, never walked

The Windows decoder reads elements with `SafeArrayGetElement` and an explicit
index vector, not with `SafeArrayAccessData` and pointer arithmetic.

This is the decision most likely to be second-guessed, so the reason is
recorded. A `SAFEARRAY`'s in-memory element order and the meaning of
`rgsabound`'s index are both easy to state backwards, and a decoder that walks
raw memory bakes whichever way it was written into every value it ever returns
-- silently, and identically for every test that compares the adapter against
itself. Addressing each element by its own index vector cannot get the order
wrong, because the order is never assumed.

The price is one COM call per element. A bound on element count belongs with the
other configured bounds rather than in this decision; the implementation adds
one.

### 3. Element order on the wire is defined, not inherited

`Elements` is ordered so that the **last** dimension in `Dimensions` varies
fastest, with `Dimensions[i]` being COM dimension `i+1` as
`SafeArrayGetLBound`/`GetUBound` number them.

Stating it here means a frontend never has to know what a `SAFEARRAY` is, and a
client can reconstruct the array from the description alone.

### 4. Lower bounds are carried, and never normalised away

A DA array may begin at any index. Re-basing it to zero and saying nothing
would be exactly the loss §20.4 rules out: two sources that disagree about
where their arrays start would become indistinguishable, and a client writing
back to an index it read would address the wrong element.

Every representation below carries the lower bound per dimension. Where a
protocol cannot carry it, the adapter refuses rather than re-bases -- see
decision 7.

### 5. HTTP/JSON: a described object under its own encoding

`valueEncoding` becomes `array`, and `value` is an object:

```json
{
  "valueEncoding": "array",
  "value": {
    "elementDataType": "VT_I4",
    "dimensions": [
      {"lowerBound": 1, "length": 3},
      {"lowerBound": 0, "length": 2}
    ],
    "elements": [10, 11, 12, 13, 14, 15]
  }
}
```

`elements` is flat and ordered by decision 3, which is not the flattening §20.4
forbids: that one discards the shape, and this one is published beside it.

Elements use the same per-type JSON rules the scalar encoding uses, so nothing
new has to be learned to read one:

- `VT_I8`/`VT_UI8` elements are decimal strings, as scalars are
- `VT_R4`/`VT_R8` elements are JSON numbers when finite and one of `"NaN"`,
  `"+Infinity"`, `"-Infinity"` when not

The scalar encoding names non-finite floats through a sibling `valueEncoding`
field, which an element does not have. Spelling them in place keeps the array
self-describing: a string in a float array is exactly one of those three names,
and a number is a number.

### 6. gRPC: a new field beside the scalar one

```proto
message DADimension {
  sint32 lower_bound = 1;
  uint32 length = 2;
}

message DAArrayValue {
  DAVarType element_data_type = 1;
  repeated DADimension dimensions = 2;
  repeated DAScalarValue elements = 3;
}
```

carried in a **new** field alongside the existing `DAScalarValue value`, not in
a `oneof` replacing it. Renumbering or re-typing a field that clients already
decode would break every existing one to add a feature none of them asked for.

Exactly one of the two is set. `DAVarType` already carries `array` and `byref`
booleans, so a client that only understands scalars can tell an array apart from
a missing value without knowing this message exists.

### 7. OPC UA: arrays that start at zero, and an explicit refusal otherwise

A UA Variant carries an array as the array bit in the encoding mask, a length
prefix, and an optional ArrayDimensions field of Int32 lengths (OPC 10000-6
Tables 25 and 26). **There is no lower bound anywhere in it.** Table 26's rules
-- all dimensions specified, each greater than zero, and the product consistent
with the array length -- are about lengths alone; `readArrayDimensions` already
enforces them.

So a DA array whose lower bound is not zero has no lossless UA representation.
The adapter does not invent one:

- every lower bound zero: published as a UA array, with ArrayDimensions written
  for more than one dimension
- any lower bound non-zero: the Read answers an explicit Bad status for that
  value, in the same shape INV-10 requires everywhere else

Re-basing to zero was rejected. A UA client cannot tell a re-based array from
one that was always zero-based, which makes the adapter the only party that
knows the value it published is not the value the source holds.

How often real sources use non-zero lower bounds is not something this adapter
can assert. It is recorded as unknown rather than guessed, and the UA mapping
document carries the limit.

### 8. Write is symmetric, and stays strictly typed

A Write of an array supplies the same description it would read back, and the
adapter builds the `SAFEARRAY` from it rather than inferring a shape. An element
whose Go type does not match the declared element VARTYPE is refused, as a
scalar is. Write stays disabled by default and is never retried or replayed;
nothing here changes that.

### 9. The status is stated as unvalidated until a real source says otherwise

Implementing this does not make it validated, and `design.md`'s non-goal is
precisely the promise. Until a third-party vendor DA server has exercised it
(ADR-0017, still `Proposed`), `docs/implementation-status.md` records array
support as implemented and **not** validated against any real source, naming
what that leaves open: element order and lower-bound reporting are exactly the
properties a self-round-trip cannot check, because the adapter would be
agreeing with itself.

## Consequences

The A.3.1.4 rule that `implementation-status.md` records as unapplied becomes
applicable, and `MultiStateDiscreteType` becomes claimable -- but only once its
`EnumStrings` array has been read from a real source, by the same rule as above.

`UNSUPPORTED_VARTYPE` keeps its meaning for `VT_BYREF`, for array element types
the scalar matrix does not carry, and for UA reads whose lower bounds cannot be
represented. The code stays the honest answer it was; it simply covers less.

Three encodings now describe the same shape, which is three places for it to
drift. The frontends are held to the DA layer's description rather than to each
other, and the interop suite gains an array case per frontend.
