# ADR-0006: real-DA validation fixture and supply-chain controls

- Status: Accepted for test use
- Date: 2026-08-23

## Context

The v0 implementation needs evidence from a real OPC DA server on Windows.
Installing a proprietary simulator would require an external EULA decision,
and treating mocks or ABI tests as interoperability would be dishonest. A
test server is external executable code with COM registration privileges, so
its provenance and execution boundary must be explicit.

## Decision

The reproducible compatibility workflow builds the OPC Foundation's OPC
Classic Core Components DA 2.05a test server from source at commit
`efe0d1d1ea86a8a727bf26a501a261765e836766`. GitHub reports that merge commit
as having a valid signature. The upstream project is licensed under the OPC
Foundation MIT license and describes itself as legacy, unmaintained, and
end-of-life. It is a validation fixture only, not a production dependency or
a claim that the adapter incorporates an OPC SDK.

The workflow does not run upstream installers or upstream PowerShell build
scripts. It invokes the pinned direct CMake targets for the Common, DA, and
Security proxy/stub DLLs and the DA test server. The audited CMake path uses
the Windows SDK `midl` and `mc` generators and the MSVC toolchain; it contains
no `FetchContent`, `ExternalProject`, `execute_process`, download, or install
script operation. The selected source slice links only Windows system
libraries and contained no tracked PE/MSI binary or network/process-persistence
API indicator in the repository audit.

Execution is confined to an ephemeral GitHub-hosted Windows VM. The workflow:

1. pins both upstream source and GitHub Actions by full commit SHA;
2. rejects an unexpected upstream commit or any tracked EXE/DLL/MSI payload;
3. runs Microsoft Defender over source before build and outputs after build;
4. registers only the architecture-matching proxy/stubs and local COM test
   server, never a Windows service;
5. validates the exact registered CLSID before starting the adapter;
6. unregisters the server and proxy/stubs in a `finally` cleanup; and
7. uploads no server binary and preserves no process value or response body.

The small staged configuration is derived from the pinned upstream test
configuration. The String item is marked read-only so the adapter can observe
a genuine per-item source write denial, and the upstream everyone-access
registration option is disabled. It is test data, not a production fake
runtime path.

## Alternatives considered

- OPC Labs Kit Server is available under 0BSD, but its downloadable package is
  a pre-built binary and requires separately installed OPC proxy/stubs. It was
  not executed and is not used by this workflow.
- Proprietary freeware simulators were rejected because unattended install
  would require accepting vendor license terms.
- A Go mock cannot establish COM ABI or real OPC DA interoperability and
  remains limited to unit tests.
- A long-lived local VM would retain registry and binary state across runs;
  the ephemeral runner gives a smaller, auditable blast radius.

## Consequences

The workflow can honestly establish compatibility with this specific OPC
Foundation test server for both x86 and x64, not with every vendor. Static
review and antivirus scanning reduce risk but do not prove that arbitrary code
is harmless. The upstream EOL status is an explicit supply-chain risk, so its
commit remains pinned and any update requires a new review. No output from
this test fixture is shipped with the adapter.

Sources:

- [OPC Classic Core Components repository](https://github.com/OPCF-Members/OPC-Classic-CoreComponents)
- [Pinned validation commit](https://github.com/OPCF-Members/OPC-Classic-CoreComponents/commit/efe0d1d1ea86a8a727bf26a501a261765e836766)
- [OPC Foundation MIT license](https://opcfoundation.org/License/MIT/1.00/)
