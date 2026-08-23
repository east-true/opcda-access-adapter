# Compatibility Matrix and Validation Procedure

## Matrix

The matrix records only executed real local-COM results. Unit doubles,
cross-builds, and Windows ABI tests are not interoperability results.

| DA Server | Version | Windows | Server bitness | Adapter arch | Connect | Browse | Read | Write | Reconnect | Notes |
|---|---|---|---|---|---|---|---|---|---|---|
| OPC Foundation OPC Classic Core Components TestServer | source commit `efe0d1d1` | Server 2025 | x86 | 386 | PASS | PASS | PASS | PASS | PASS | DA 2.05a source-built fixture; [run evidence](https://github.com/east-true/opcda-access-adapter/actions/runs/32628886186) |
| OPC Foundation OPC Classic Core Components TestServer | source commit `efe0d1d1` | Server 2025 | x64 | amd64 | PASS | PASS | PASS | PASS | PASS | DA 2.05a source-built fixture; [run evidence](https://github.com/east-true/opcda-access-adapter/actions/runs/32628886186) |

## Recorded result: OPC Foundation DA 2.05a fixture

- Executed: 2026-08-23 on ephemeral GitHub-hosted Windows Server 2025 VMs.
- Adapter head: `5267aec6e05f98dff5da4721ded6315e5a2ba990`; tested pull-request
  merge tree: `a97fc2d58bdfb4e1470e2f869d3dfbbf6681b61d`.
- Server source: `OPCF-Members/OPC-Classic-CoreComponents` at pinned commit
  `efe0d1d1ea86a8a727bf26a501a261765e836766`.
- Security boundary: source and built outputs both passed Microsoft Defender
  custom scans. The source/build audit and the limits of this evidence are in
  ADR-0006; antivirus and static review do not prove arbitrary code harmless.
- Both architectures connected through local COM, detected Browse, returned
  the exact nested ItemIDs, preserved partial Read ordering, rejected Write by
  default, enforced a strict type mismatch, completed an explicitly enabled
  typed value Write, and preserved a source-denied Write result.
- The recorded Read metadata on both architectures was actual/canonical
  `VT_I4`, raw Quality `192` (`0x00C0`), source timestamp present, successful
  item HRESULT `0x00000000`, and invalid-ItemID HRESULT `0xC0040007`
  (`OPC_E_UNKNOWNITEMID`). The read/write item exposed canonical `VT_R4` and
  access rights raw `3`; the read-only item exposed canonical `VT_BSTR`, access
  rights raw `1`, and `OPC_E_BADRIGHTS` (`0xC0040006`) on Write.
- During a real server stop/unregistration the adapter returned no successful
  stale value, exposed disconnected/reconnecting state, advanced reconnect
  count and connection generation after re-registration, and lazy-registered
  the known ItemID again.
- Each architecture completed 200 bounded device Reads. Observed resource
  deltas were: 386 adapter handles `+8`, private bytes `+1,835,008`, server
  handles `0`, server private bytes `0`; amd64 adapter handles `+8`, private
  bytes `+1,978,368`, server handles `0`, server private bytes `0`.
- Fixture executable SHA-256: x86
  `35B18E2542131907A256929FE1C5A54B204CAB8421AA9F90305E6C8B6583F681`;
  x64 `C715BFA24DE1414D6CC1E8A6B5F61FEE42530EB43A619BC6BD5185A7B0F6DDF7`.

This result validates the scoped v0 path against this specific official test
fixture only. Third-party/vendor servers and non-Good Quality observations
remain untested; compatibility must not be inferred for them.

## Real-DA validation procedure

1. On the same Windows machine as an installed DA server, build the matching
   adapter architecture (`windows/386` for a 32-bit-only registration,
   `windows/amd64` where applicable).
2. Set exactly one source identifier:

   ```powershell
   $env:OPCDA_SOURCE_PROG_ID = "Vendor.Server.1"
   # or
   $env:OPCDA_SOURCE_CLSID = "{00000000-0000-0000-0000-000000000000}"
   ```

3. Start the adapter with its default loopback listener. Check `GET
   /v1/status`, then Browse root and a nested branch if Browse is supported.
4. Read a known item, a batch with at least one invalid ItemID, and an item
   with non-Good Quality where available. Record exact VARTYPE, Quality,
   Timestamp presence, and HRESULT.
5. Confirm Write is rejected while disabled. Enable it only against a safe
   test item, verify type mismatch and access-denied behavior, then a typed
   value Write.
6. Restart the DA server, observe an unavailable result during disconnect,
   then verify `disconnected` during backoff/`reconnecting` during an attempt,
   an increased reconnect count, a strictly newer connection generation, and
   lazy re-registration rather than old-handle reuse. Do not accept any
   last-good value during the outage.
7. Repeat the required scenarios for each applicable adapter architecture and
   record actual PASS/FAIL/BLOCKED results in the table above.
8. For soak validation, repeatedly exercise a bounded safe Read batch while
   monitoring process private bytes, handle count, goroutine count, and
   request errors. Exercise Browse and an authorized safe Write at controlled
   intervals. Do not persist response values; record only duration, counts,
   resource deltas, and failure metadata. A VM without an installed real DA
   server cannot satisfy this step.

Do not place process values in this document.

## Automated isolated fixture

The manual `Real OPC DA validation` GitHub workflow builds the OPC Foundation
DA 2.05a test server from a pinned, reviewed source commit and registers it by
local COM on an ephemeral Windows VM. It covers both native x86/386 and
x64/amd64 paths. See the [execution procedure](validation/real-da-windows.md)
and [fixture/supply-chain decision](adr/0006-real-da-validation-fixture.md).

The workflow file existing in the repository is not a test result. Add a row
to the matrix only after an actual run passes, and include its immutable run
URL and commit SHA. Never copy response values into the matrix or workflow
logs.
