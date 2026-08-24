# ADR-0010: local OPC DA registration detection

- Status: Accepted
- Date: 2026-08-24

## Context

Operators should not need Registry Editor or a third-party browser merely to
find candidate source identifiers. Windows component categories can enumerate
COM classes registered as OPC DA 2.0 without instantiating each vendor class.
Registration alone does not prove that a server can be activated, that its
permissions are correct, or that its DA behavior is compatible.

The design permits local discovery as a convenience but prohibits remote DCOM,
automatic source selection, and multi-server aggregation. Detection must not
quietly turn into a probe or introduce an unauthenticated inventory endpoint.

Authoritative definitions are the Microsoft Windows SDK `ComCat.idl` for
`ICatInformation`/`IEnumGUID`, Microsoft COM documentation for component
category enumeration and `ProgIDFromCLSID`, and the OPC Foundation DA IDL for
`CATID_OPCDAServer20`. No SDK or specification file is copied into this
repository.

## Decision

- Add `opcda-access-adapter detect` as a local CLI command with JSON output.
- Enumerate only `CATID_OPCDAServer20` through the standard Windows Component
  Categories Manager using `ICatInformation::EnumClassesOfCategories`.
- Instantiate the category manager with `CLSCTX_INPROC_SERVER` only. Never
  instantiate a detected vendor CLSID during detection.
- Own the category manager and enumerator on a dedicated locked STA thread;
  balance COM initialization, release every interface, and free every returned
  ProgID with `CoTaskMemFree`.
- Return exact CLSID and an optional registry-resolved ProgID. Do not infer a
  name, architecture, availability, permission state, or compatibility.
- Bound results to 256 by default (hard ceiling 4096), ProgID strings to 1024
  UTF-16 code units by default (hard ceiling 65536), and caller wait to ten
  seconds by default (hard ceiling 24 hours). CLI flags make these reversible.
- Sort results deterministically. An empty registration set is successful.
- Do not auto-select a single candidate, change source configuration, add an
  HTTP endpoint, accept a machine name, or perform remote detection.

An expired context stops the caller from waiting but cannot safely cancel an
already executing COM call. No thread termination is attempted; the CLI exits
and an embedded caller must apply the existing operator/process-restart policy
if the standard COM call never returns.

## Consequences

Users can obtain exact source identifiers without activating the DA server.
They must still explicitly configure exactly one ProgID or CLSID before
running the adapter. A class can be registered yet unusable, and architecture
views can differ, so detection output is candidate inventory rather than a
compatibility claim.

The feature does not add process-value access, persistence, remote DCOM,
source routing, or multi-server operation. A future supervisor remains a
separate product decision.

## References

- [Microsoft `ICatInformation::EnumClassesOfCategories`](https://learn.microsoft.com/en-us/windows/win32/api/comcat/nf-comcat-icatinformation-enumclassesofcategories)
- [Microsoft `ProgIDFromCLSID`](https://learn.microsoft.com/en-us/windows/win32/api/combaseapi/nf-combaseapi-progidfromclsid)
- [Microsoft Windows SDK `ComCat.idl` mirror at the reviewed revision](https://github.com/microsoft/win32metadata/blob/d0f363037c6987790b3548b7f82f382034d86bf2/generation/WinSDK/RecompiledIdlHeaders/um/ComCat.Idl)
- [Pinned OPC Foundation `opcda.idl`](https://github.com/OPCF-Members/OPC-Classic-CoreComponents/blob/efe0d1d1ea86a8a727bf26a501a261765e836766/Source/DataAccess/ProxyStub/opcda.idl)
