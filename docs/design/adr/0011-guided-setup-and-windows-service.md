# ADR-0011: guided setup and Windows Service lifecycle

- Status: Accepted
- Date: 2026-08-24

## Context

Local registration detection removes the need to find a ProgID or CLSID with
Registry Editor, but operators still have to translate that inventory into
environment variables and choose how the long-running adapter process is
owned. Starting a detached console process would make stop, shutdown, boot
startup, failure reporting, and identity ambiguous. Changing no-argument
startup into an interactive wizard would break existing automation and service
launches.

The design still requires one adapter runtime per explicitly selected local OPC
DA server. A candidate registration is not proof of activation, DCOM permission,
or DA compatibility, and a single candidate must not be selected implicitly.

## Decision

- Add an explicit `setup` command. No-argument execution remains the existing
  environment-configured foreground runtime.
- Enumerate bounded local `OPC_DA_20` registrations and require the operator to
  choose one even when only one candidate exists. Persist its exact CLSID; do
  not activate candidates during the selection step.
- Require a frontend choice. v0 presents only HTTP/JSON and does not advertise
  future or unavailable frontends.
- Keep loopback `127.0.0.1:8080` and Write-disabled as setup defaults. External
  bind and Write enablement require explicit flags and remain visible in the
  final confirmation.
- Write a versioned JSON configuration bounded to 64 KiB. Parsing rejects
  unknown fields, duplicate fields, trailing values, unsupported versions,
  invalid bounds, and any source count other than one. Setup creates a new file
  exclusively and never silently overwrites an existing configuration.
- After review and confirmation, offer foreground execution, save-only, or an
  actual Windows Service. There is no automatic fallback or retry when a
  selected action fails.
- Install services through the local Windows Service Control Manager only.
  Each service has one configuration and therefore one local DA source. A
  configurable service name permits multiple independent adapter processes but
  does not add aggregation, routing, or a multi-server runtime.
- Run the service as `NT AUTHORITY\LocalService`, never LocalSystem and never a
  stored user password. Use automatic boot start, support SCM Stop/Shutdown,
  apply the existing ten-second graceful shutdown, and record bounded lifecycle
  and error messages in the Windows Application Event Log under the service
  name. Process values are not logged.
- Do not modify AppID, LaunchPermission, AccessPermission, RunAs, machine-wide
  DCOM defaults, firewall rules, or vendor registration. A LocalService DCOM
  failure remains explicit and requires a scoped operator/vendor permission
  decision.
- Installation fails if the service or Event Log source already exists. A
  failed start is rolled back where SCM state permits. Uninstall stops the
  service before deletion and removes its Event Log source.

The installed service command contains absolute executable and configuration
paths. Moving or deleting either file breaks the service, so operators must
place them in a stable location readable by LocalService. The configuration
contains no credentials or process values.

## Dependency review

Windows service dispatcher, SCM, Event Log, and error constants use
`golang.org/x/sys/windows` and its `svc`, `svc/mgr`, and `svc/eventlog`
packages at pinned version `v0.47.0`.

- License: BSD-3-Clause, compatible with this repository's Apache-2.0 license.
- Maintenance/provenance: maintained by the Go project and obtained through the
  Go module proxy/checksum mechanism.
- Supply chain: one direct module, no transitive modules, no install script, and
  no cgo/native binary payload.
- Runtime/binary impact: Windows-only low-level wrappers replace a larger custom
  unsafe SCM implementation. Against protected main `1feb1818`, otherwise
  identical stripped release executables grew from 6,411,264 to 6,839,296
  bytes on 386 (+428,032, 6.68%) and from 6,628,864 to 7,093,760 bytes on
  amd64 (+464,896, 7.01%).

This is a general-purpose low-level dependency, not an OPC DA/UA SDK or COM
abstraction. OPC DA interfaces and ownership remain implemented in this
repository.

## Consequences

An operator can choose a registered source and HTTP output, review the exact
configuration, and leave a managed background process without learning COM
identifiers or SCM command syntax. Automation remains stable because setup is
explicit. Service identity can expose vendor-specific DCOM assumptions that
foreground execution under the interactive user did not; setup reports this
risk rather than weakening DCOM security or pretending the source connected.

The feature does not add remote discovery, automatic candidate selection,
source probing during selection, dynamic plugins, frontend routing, process
data persistence, or multi-server aggregation.

## References

- [Go `x/sys/windows/svc` package](https://pkg.go.dev/golang.org/x/sys/windows/svc)
- [Go `x/sys/windows/svc/mgr` package](https://pkg.go.dev/golang.org/x/sys/windows/svc/mgr)
- [Microsoft service user accounts](https://learn.microsoft.com/en-us/windows/win32/services/service-user-accounts)
- [Microsoft service control handler guidance](https://learn.microsoft.com/en-us/windows/win32/services/service-control-handler-function)
