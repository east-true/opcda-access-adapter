# Windows COM security and permissions

This adapter accesses exactly one OPC DA server on the same Windows machine.
It is not a remote DCOM client, tunneler, permission editor, or server
discovery tool.

## Activation boundary

The runtime resolves the configured ProgID or CLSID in the adapter process's
registry view and calls `CoCreateInstance` with only
`CLSCTX_INPROC_SERVER | CLSCTX_LOCAL_SERVER`. It never requests
`CLSCTX_REMOTE_SERVER` or uses `CoCreateInstanceEx`.

Those two permitted contexts have different security consequences:

- `InprocServer32` loads a registered DLL into the adapter process. DCOM
  Launch/Activation ACLs do not provide a process boundary for that DLL.
- `LocalServer32` activates an executable in another process on the same
  computer. The COM Service Control Manager evaluates applicable local launch
  and activation permissions before dispatching activation.

Use only a server registration and binary whose provenance is trusted. An
administrator who can replace COM registration or its executable can cause
code to run in or alongside the adapter.

## Windows Service identity

Guided background setup installs the adapter as
`NT AUTHORITY\LocalService`, not LocalSystem, and stores no password. The SCM
records absolute paths to the adapter executable and reviewed configuration;
both must be in a stable location readable by LocalService. Installation and
uninstallation require Administrator rights, but the running adapter does not
retain those rights.

LocalService can have different COM registration, file, vendor-configuration,
LaunchPermission, AccessPermission, and interactive-desktop access than the
user who ran setup. A successful foreground connection therefore does not
prove service-mode compatibility. The installer does not edit AppID/DCOM ACLs
or retry under a stronger identity. Service lifecycle and startup errors are
recorded in the Windows Application Event Log under the configured service
name without process values.

## Permission layers

A local out-of-process server can fail at several independent layers:

1. **Registry view.** A 386 adapter reads the 32-bit COM registration view and
   an amd64 adapter reads the 64-bit view. A registration in the other view is
   not proof that this process can activate it.
2. **Registry and file access.** The adapter identity needs read access to the
   ProgID/CLSID/AppID registration and the server/proxy binaries. The server
   identity needs the vendor-specific configuration and device resources it
   actually uses.
3. **Launch and Activation permission.** A server-specific AppID
   `LaunchPermission`, when present, overrides the machine default for that
   application. Grant only Local Launch and Local Activation to the dedicated
   adapter identity when the server supports that deployment. Do not grant
   Remote Launch or Remote Activation for this project.
4. **Access permission and process security.** AppID `AccessPermission` can
   control calls for servers that use registry-driven process security. A
   server that calls `CoInitializeSecurity` explicitly can override registry
   defaults; its vendor documentation remains authoritative.
5. **Server identity.** `RunAs`, a COM service identity, and "Interactive
   User" have different session and logon requirements. A named RunAs account
   needs the required batch-logon right and a credential configured through
   supported Windows administration. Do not place passwords in adapter
   environment variables or repository files.
6. **Adapter service identity.** For background mode, test LocalService Local
   Launch, Local Activation, and local Access only where the vendor supports
   that identity. Never add Remote Launch/Activation/Access for this adapter.

Change application-specific permissions through Component Services or the
vendor installer. Do not weaken `DefaultLaunchPermission`,
`DefaultAccessPermission`, DCOM authentication, UAC, or Windows Firewall to
make one server work. Microsoft warns that changing machine-wide defaults can
affect every COM server that relies on them.

## Diagnosing failures

While disconnected, `GET /v1/status` exposes one bounded
`source.lastError` record with the failing operation and raw HRESULT. Common
activation results include:

| HRESULT | Typical investigation |
|---|---|
| `0x80070005` | local Launch/Activation or access denied; also inspect file and registry ACLs |
| `0x80040154` | class not registered in this process architecture's registry view |
| `0x80080005` | registered local server could not start or register its class object |
| `0x8000401A` | configured RunAs identity could not log on |
| `0x800706BA` | RPC server unavailable or an established source process disappeared |

Treat the HRESULT as evidence, not as a complete diagnosis. Correlate the
same time window with Windows System/Application logs and vendor logs without
copying process values into an issue.

Microsoft's DCOM hardening for CVE-2021-26414 has been permanently enabled
since March 2023. Do not attempt to disable it. Events 10036, 10037, and 10038
are relevant when diagnosing remote requests with inadequate authentication;
their absence does not prove that application-specific local ACLs are correct.

References:

- [COM activation contexts](https://learn.microsoft.com/en-us/windows/win32/learnwin32/creating-an-object-in-com)
- [Process-wide security through the registry](https://learn.microsoft.com/en-us/windows/win32/com/setting-processwide-security-through-the-registry)
- [CoInitializeSecurity precedence](https://learn.microsoft.com/en-us/windows/win32/api/combaseapi/nf-combaseapi-coinitializesecurity)
- [RunAs identity requirements](https://learn.microsoft.com/en-us/windows/win32/com/runas)
- [DCOM hardening KB5004442](https://support.microsoft.com/en-us/topic/kb5004442-manage-changes-for-windows-dcom-server-security-feature-bypass-cve-2021-26414-14f8b3d4-bd5f-4c2d-9d01-5a75820c98e2)
