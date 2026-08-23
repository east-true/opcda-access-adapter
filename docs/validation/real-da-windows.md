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
- cleanup of COM server and proxy/stub registrations.

The harness does not print or upload process values. Its summary contains only
operation outcomes, VARTYPE, raw Quality, timestamp presence, HRESULTs,
iteration count, and bounded resource deltas.

## Running

Repository maintainers can dispatch `.github/workflows/real-da-validation.yml`
from GitHub Actions. The default is 200 bounded Read iterations per
architecture. A same-repository pull request that changes this workflow,
validation scripts, or validation records also runs it. Fork pull requests do
not execute the privileged COM registration job.

Passing the workflow is evidence only after the exact GitHub run URL and
commit SHA are recorded in `docs/compatibility.md`. A workflow definition or
cross-build alone is not a compatibility result.
