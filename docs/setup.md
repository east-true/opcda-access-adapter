# Guided setup and Windows Service

The explicit guided setup command turns the bounded local registration
inventory into one reviewed adapter configuration:

```powershell
.\opcda-access-adapter.exe setup
```

It asks for:

1. one locally registered OPC DA 2.0 source;
2. one frontend (`HTTP/JSON` is the only v0 choice);
3. foreground, Windows Service, or save-only execution;
4. final confirmation.

A single detected source is still shown as a choice and is never selected
automatically. Setup records the selected class's exact CLSID. Candidate
enumeration does not activate or probe the vendor server.

## Safety defaults

- configuration: `opcda-access-adapter.json` in the current directory;
- HTTP listener: `127.0.0.1:8080`;
- typed value Write: disabled;
- Windows Service name: `OPCDAAccessAdapter`;
- service account: `NT AUTHORITY\LocalService`.

Change the reversible setup defaults explicitly when required:

```powershell
.\opcda-access-adapter.exe setup `
  --config C:\ProgramData\OPCDAAccessAdapter\line-a.json `
  --listen 127.0.0.1:18080 `
  --service-name OPCDAAccessAdapter_LineA
```

`--enable-write` is deliberately separate and visible in the confirmation.
The adapter still performs strict typed value-only Write with no automatic
retry, replay, or rollback.

Setup refuses to overwrite an existing file or replace an existing service.
To change a deployment, preserve the old file for rollback, uninstall the old
service if applicable, and create a newly reviewed configuration.

## Configuration contract

The generated file is versioned, bounded to 64 KiB, and contains configuration
only—never credentials, ItemIDs, process values, last-good values, or Browse
state.

```json
{
  "version": 1,
  "source": {
    "clsid": "{00000000-0000-0000-0000-000000000000}"
  },
  "frontend": {
    "type": "http",
    "httpListen": "127.0.0.1:8080"
  },
  "writeEnabled": false
}
```

Unknown or duplicate fields, multiple JSON values, unsupported versions,
invalid listener/bounds, and zero or multiple sources fail closed. Environment
variables are not merged into file-based execution, so a Windows Service runs
the exact file that was reviewed.

Run a saved configuration in the current terminal:

```powershell
.\opcda-access-adapter.exe run --config .\opcda-access-adapter.json
```

The original environment-variable, no-argument startup remains supported for
automation.

## Windows Service lifecycle

The background option installs and starts a real SCM-managed service with
automatic boot startup. Equivalent explicit commands are:

```powershell
.\opcda-access-adapter.exe service install `
  --config C:\ProgramData\OPCDAAccessAdapter\line-a.json `
  --name OPCDAAccessAdapter_LineA

Get-Service OPCDAAccessAdapter_LineA
Invoke-RestMethod http://127.0.0.1:18080/v1/status

.\opcda-access-adapter.exe service uninstall `
  --name OPCDAAccessAdapter_LineA
```

Install and uninstall require an elevated terminal. The service uses
LocalService rather than LocalSystem and stores no password. Lifecycle,
configuration-load, listener, and shutdown errors are written to the Windows
Application Event Log under the configured service name; process values are
not logged. DA connection HRESULTs remain in the bounded `/v1/status` source
diagnostic.

The SCM command records absolute executable and configuration paths. Put both
in a stable location readable by LocalService before installing, and do not
move them while the service exists.

## COM/DCOM identity caveat

Foreground execution uses the interactive user's identity. Background service
execution uses LocalService, so a vendor server can allow the former and deny
the latter. Setup does not change AppID LaunchPermission, AccessPermission,
RunAs, machine-wide DCOM policy, firewall state, or vendor registration.

If the HTTP status reports a COM activation/access failure, use the exact
HRESULT and operation together with [Windows COM security and
permissions](security-windows.md). Do not weaken machine-wide DCOM defaults.
Some vendor servers require an interactive desktop or a vendor-supported
service identity and may be unsuitable for LocalService operation.

## Scope

Each configuration and service still owns exactly one local OPC DA source.
Different service names can operate independent adapter processes, but there
is no aggregation, shared routing, automatic source selection, remote DCOM,
or remote discovery. See [ADR-0011](adr/0011-guided-setup-and-windows-service.md).
