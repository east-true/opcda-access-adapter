# Item properties

[Frontend overview](README.md) · [Documentation](../../README.md)

```text
POST /v1/properties/available
POST /v1/properties
Content-Type: application/json
```

```json
{ "itemId": "Random.Real8" }
```

```json
{ "itemId": "Random.Real8", "propertyIds": [100, 102, 103] }
```

These are `IOPCItemProperties::QueryAvailableProperties` and
`::GetItemProperties`, passed through. The frontend is DA-native: it reports the
source's own property identifiers, its own description text, its own VARTYPEs
and its own HRESULTs, and maps none of them onto anything. OPC 10000-8 Table A.1
is applied by the OPC UA frontend, not here.

Every available property reports the VARTYPE the source stated for it.
`VT_EMPTY` is a stated type, not an absent one, so `dataType` is always present
in that list. In a value result `dataType` is omitted when the source refused
the property and there is genuinely none.

A source can answer a property **successfully and give nothing**. `valuePresent`
says whether a value is carried, for the same reason a Read carries
`timestampPresent`: absence is absence, and it is never reported as a refusal
the source did not make.

A per-property HRESULT is a **result, not a failure**. A source may offer a
property for one item and refuse it for another, so the request succeeds and the
refusal is reported against that property, carrying the exact HRESULT and no
substituted value.

The item's value, quality and timestamp are properties 2, 3 and 4, and naming
any of them is **not** a per-property refusal of that kind. It fails the whole
request with HTTP 400 `INVALID_REQUEST`, even alongside valid identifiers and
wherever in the list it appears: a source declining a property is a result, but
asking this endpoint for a value is a mistake in the request, and a request
built on that mistake should not half-succeed.

Read and Subscribe deliver a value together with its timestamp and its raw
quality, and answering the same question a second way without them could produce
a different answer.

`IOPCItemProperties` is optional. A source that does not implement it reports
`capabilities.properties` as `unsupported` and answers these endpoints with HTTP
422 `PROPERTIES_UNSUPPORTED` — the same shape as a source without
`IOPCBrowseServerAddressSpace`. It is a capability, not a fault: the source is
working correctly and has no properties to offer.
