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
- connection, group creation, and capabilities;
- root and nested stateful Browse with exact ItemIDs;
- device Read VARTYPE, raw Quality, timestamp presence, access rights, and raw
  HRESULT;
- ordered partial batch failure with `OPC_E_UNKNOWNITEMID`;
- default-disabled Write, strict VARTYPE mismatch, typed source Write, and a
  source-denied Write to a read-only item;
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

The harness does not print or upload process values. Its summary contains only
operation outcomes, VARTYPE, raw Quality, timestamp presence, HRESULTs,
iteration count, and bounded resource deltas.

## Running

Repository maintainers can dispatch `.github/workflows/real-da-validation.yml`
from GitHub Actions. The default is 200 bounded Read iterations per
architecture, and the explicit maximum is 100,000 per architecture so a
long-running resource check remains bounded. A same-repository pull request
that changes this workflow, validation scripts, or validation records also
runs it. Fork pull requests do not execute the privileged COM registration
job.

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
