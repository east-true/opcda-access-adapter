# Item properties

[Frontend overview](README.md) · [Documentation](../../README.md)

`AvailableItemProperties` and `ItemProperties` are
`IOPCItemProperties::QueryAvailableProperties` and `::GetItemProperties`, passed
through. Two calls because they are two questions: what does this source offer
for this item, and what are those properties' values.

The frontend is DA-native — the source's own property identifiers, description
text, VARTYPEs and HRESULTs are reported and mapped onto nothing. OPC 10000-8
Table A.1 is applied by the OPC UA frontend, not here.

A per-property HRESULT is a result rather than a failure: the request succeeds
and a refused property carries its exact HRESULT with no substituted value.

The item's value, quality and timestamp are properties 2, 3 and 4, and naming
any of them is **not** a refusal of that kind. It fails the whole call with
`InvalidArgument` and `INVALID_REQUEST`, even alongside valid identifiers and
wherever in the list it appears: a source declining a property is a result, but
asking this method for a value is a mistake in the request. Read and Subscribe
deliver a value with its timestamp and raw quality together, and answering the
same question a second way without them could produce a different answer.

A source that does not implement `IOPCItemProperties` reports
`capabilities.properties` as `unsupported` and answers `Unimplemented` with
`PROPERTIES_UNSUPPORTED`, the same way a source without Browse does.
