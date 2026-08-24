# Local OPC DA server detection

`opcda-access-adapter detect` enumerates COM classes registered in the local
OPC DA 2.0 component category. It is a configuration convenience command, not
a DA operation or compatibility probe.

```powershell
.\opcda-access-adapter.exe detect
```

Example shape:

```json
{
  "scope": "local",
  "category": "OPC_DA_20",
  "categoryId": "{63D5F432-CFE4-11D1-B2C8-0060083BA1FB}",
  "detectorArchitecture": "amd64",
  "servers": [
    {
      "clsid": "{00000000-0000-0000-0000-000000000000}",
      "progId": "Vendor.Server.1"
    }
  ]
}
```

`clsid` is always present. `progId` is omitted when Windows cannot resolve it;
the adapter does not invent one. `detectorArchitecture` describes the detector
executable, not an inferred server architecture. Results are sorted by ProgID
then CLSID. `servers: []` means no matching registration was visible and is a
successful response.

The command uses the standard in-process Windows Component Categories Manager
on a dedicated locked STA thread. It does not instantiate any returned CLSID,
connect to a DA server, change source configuration, or expose the inventory
over HTTP. A listed registration can still be stale, inaccessible, licensed,
incompatible, or unable to start.

## Bounds

```text
--max-results 256
--max-progid-code-units 1024
--timeout 10s
```

Hard ceilings are 4096 results, 65536 UTF-16 ProgID code units, and 24 hours.
Exceeding the result bound fails explicitly with
`DETECTION_RESULT_LIMIT_EXCEEDED`; results are not truncated. A timeout stops
the CLI from waiting but does not forcibly cancel or terminate an in-flight
COM call.

## Architecture and scope

Run the 386 detector for registrations visible to the 32-bit build and the
amd64 detector for registrations visible to the 64-bit build when both are
relevant. Do not treat duplicate visibility as two source servers without
checking the exact CLSID.

Detection is local only. There is no machine-name option, OPCEnum network
request, remote COM activation, automatic source selection, or multi-server
runtime. After choosing one candidate, explicitly set `OPCDA_SOURCE_PROG_ID`
or `OPCDA_SOURCE_CLSID` and start that adapter instance.

See [ADR-0010](adr/0010-local-da-registration-detection.md) for the interface,
ownership, and scope decision.
