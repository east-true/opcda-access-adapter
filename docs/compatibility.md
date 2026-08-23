# Compatibility Matrix and Validation Procedure

## Matrix

No real OPC DA server has been tested. A build or mock test is not an
interoperability result.

GitHub-hosted Windows VMs execute the Windows-only COM allocation, apartment,
VARIANT, BSTR, queue, watchdog, and lifecycle tests on both 386 and amd64.
Those are platform/ABI results only: the runner has no OPC DA server and is
not listed as a compatible DA implementation.

| DA Server | Version | Windows | Server bitness | Adapter arch | Connect | Browse | Read | Write | Reconnect | Notes |
|---|---|---|---|---|---|---|---|---|---|---|
| _No result yet_ | — | — | — | — | NOT RUN | NOT RUN | NOT RUN | NOT RUN | NOT RUN | Requires an authorized local DA server |

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
