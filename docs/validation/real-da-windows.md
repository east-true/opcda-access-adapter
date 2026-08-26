# Reproducible Windows real-DA validation

The `Real OPC DA validation` workflow builds and registers an actual local COM
OPC DA 2.05a server on an ephemeral Windows VM. It runs separately for
`windows/386` with the upstream x86 server and `windows/amd64` with the
upstream x64 server.

This is not a mock test and it is not broad vendor certification. The external
fixture, license, source pin, audit findings, and isolation controls are
recorded in [ADR-0006](../adr/0006-real-da-validation-fixture.md).

## Covered observations

- local ProgID registration and exact CLSID resolution;
- bounded `OPC_DA_20` registration detection with exact ProgID/CLSID and no
  vendor server process activation;
- guided explicit source/frontend/action selection, strict configuration
  output, LocalService SCM installation, service-mode connection and device
  Read through HTTP and gRPC, automatic-start metadata, Stop/uninstall, and
  Event Log cleanup;
- connection, group creation, and capabilities;
- root and nested stateful Browse with exact ItemIDs;
- device Read VARTYPE, raw Quality, timestamp presence, access rights, and raw
  HRESULT;
- ordered partial batch failure with `OPC_E_UNKNOWNITEMID`;
- default-disabled Write, strict VARTYPE mismatch, typed source Write, and a
  source-denied Write to a read-only item;
- gRPC unary Status, root/nested Browse, ordered partial Read, default-disabled
  Write, strict typed Write, source-denied Write, and loopback listener bounds;
- deterministic outage while the server is unregistered, explicit
  unavailable behavior, reconnect count, newer connection generation, and
  lazy re-registration;
- bounded repeated Reads plus adapter/server handle and private-byte bounds;
- strict HTTP request validation for malformed, invalid UTF-8, oversized, and
  excessive-depth inputs;
- incomplete and oversized header handling, configured header timeout, and
  listener recovery;
- 5,000 no-delay Reads, 3,200 mixed requests from 16 concurrent workers, and
  deterministic `QUEUE_FULL` backpressure with 32 occupied request slots;
- three consecutive source outage/recovery cycles with generation and stale
  value checks;
- cleanup of COM server and proxy/stub registrations.

The staged server path intentionally contains no hyphen. The pinned upstream
sample parses the first `-` or `/` in its entire command line as the start of
`RegServer`; a hyphen in the executable path would make registration enter the
normal server loop instead. The harness rejects such a path before execution
and bounds every registration process to 30 seconds.

No frontend exposes Subscribe, so the Subscribe probe drives the DA runtime
directly instead of a listener. It creates one DA group per subscription,
requires real `IOPCDataCallback::OnDataChange` batches from the fixture, and
checks that a drained batch never exceeds the subscription's active item count.

The fixture's `Test` items are static, so the server's initial snapshot is the
only unsolicited notification. To prove notifications are driven by the source
rather than by a one-time snapshot, the probe enables Write **inside its own
process only** and writes distinct values to the fixture's read/write VT_R4
item through the ordinary typed Write path, requiring the source to report each
change.

The probe then repeats subscribe/unsubscribe more times than
`MaxSubscriptions`, so a leaked DA group or advise cookie would exhaust the
limit instead of passing silently. Finally it terminates the fixture process on
purpose to require that source loss invalidates the subscription and discards
its pending values, that reconnect restores nothing implicitly, and that an
explicit resubscribe receives a new generation-scoped identifier and delivers
again. Because it stops the fixture, it runs before the long-lived adapter
starts and leaves no activated server behind.

The OPC UA probe drives the UA frontend against the real source over a TCP
socket: Hello/Acknowledge, OpenSecureChannel, GetEndpoints, CreateSession and
ActivateSession, a Browse walk from Root through Objects to the source folder
and down to a variable, and a Read of that variable.

It asserts that the published endpoint matches the configured endpoint URL and
security policy URI exactly, that the security mode is None, and that an
unsecured endpoint carries **no certificate** and security level 0. The run
supplies placeholder policy and transport profile URIs: the known URIs are
defined by OPC 10000-7, which this project has not transcribed, so the probe
checks only that the server publishes exactly what it was configured with and
makes **no claim that those are the standard URIs**.

Write is never enabled for the OPC UA scenario. The read assertion allows any
status the source's quality produces — a non-Good quality is data, not a failure
— and requires only that a usable status carries a value and a bad one does not.

The HTTP, gRPC, Subscribe, and OPC UA probes do not print or upload process values. Their summaries contain only
operation outcomes, VARTYPE, raw Quality, timestamp presence, HRESULTs,
subscription identifiers, revised update rates, iteration count, and bounded
resource deltas.

## Running

Repository maintainers can dispatch `.github/workflows/real-da-validation.yml`
from GitHub Actions. The default is 200 bounded Read iterations per
architecture, and the explicit maximum is 100,000 per architecture so a
long-running resource check remains bounded. A same-repository pull request
that changes the adapter command, DA runtime, this workflow, validation
scripts, or validation records also runs it. Fork pull requests do not execute
the privileged COM registration job.

Passing the workflow is evidence only after the exact GitHub run URL and
commit SHA are recorded in `docs/compatibility.md`. A workflow definition or
cross-build alone is not a compatibility result.

The first recorded passing result is run
[`32628886186`](https://github.com/east-true/opcda-access-adapter/actions/runs/32628886186)
for adapter head `5267aec6e05f98dff5da4721ded6315e5a2ba990`. See the compatibility
matrix for the observations, source pin, executable hashes, and resource
deltas.

Long-running run
[`32630548279`](https://github.com/east-true/opcda-access-adapter/actions/runs/32630548279)
passed 100,000 device Reads per architecture at adapter head
`995c387cb977a37ab80ecd0fc5deb2f4a98e191d`. Its bounded resource deltas are
also recorded in the compatibility matrix.

Final main run
[`32632091320`](https://github.com/east-true/opcda-access-adapter/actions/runs/32632091320)
passed the same 100,000-Read scenario for both architectures at main SHA
`9e8928d729300a67197da35e7bfee6623a861495`.

Windows stability run
[`32634777223`](https://github.com/east-true/opcda-access-adapter/actions/runs/32634777223)
passed the normal, invalid-input, slow-header, rapid, concurrent, deterministic
backpressure/recovery, three-cycle source failure, and 200-Read soak profiles
for both architectures at adapter head
`ccc28487dfc33e1767e3f42c547a6d59a5ae4ca4`. Exact counts and bounded
resource deltas are recorded in the compatibility matrix.

Final-main run
[`32635274825`](https://github.com/east-true/opcda-access-adapter/actions/runs/32635274825)
repeated and passed the same stability profile for both architectures at merge
SHA `f55b4bb8e8c092fa2a21f4f35089a14703c81a8d`.

Guided-setup run
[`32734190245`](https://github.com/east-true/opcda-access-adapter/actions/runs/32734190245)
passed explicit source/frontend/service selection, strict configuration,
LocalService SCM identity and automatic-start metadata, service-mode local-COM
connection and device Read, bounded Application Event Log output, and complete
service/event-source cleanup on both x86/386 and x64/amd64 at adapter head
`0fc3684128919ff28f94b9257c0dbc30e34ae328`. The same jobs then passed the
complete semantic, failure/reconnect, load/backpressure, and 200-Read soak
regression. See the compatibility matrix for the exact observations and the
limits of this evidence.

Phase 6 gRPC run
[`32752269529`](https://github.com/east-true/opcda-access-adapter/actions/runs/32752269529)
passed the explicit gRPC guided LocalService lifecycle plus unary Status,
root/nested Browse, ordered partial Read, disabled Write, strict typed Write,
source-denied Write, loopback-listener, cleanup, and no-value-logging checks on
both x86/386 and x64/amd64 at adapter head
`b83b7c2b159194cbac94ce66f52b325b9c22031f`. The established HTTP and
failure/reconnect/load/soak regression also passed in both jobs.
