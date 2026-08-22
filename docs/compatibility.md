# Compatibility Matrix and Validation Procedure

## Matrix

No real OPC DA server has been tested. A build or mock test is not an
interoperability result.

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
   then verify reconnect and that old handles are not reused.
7. Repeat the required scenarios for each applicable adapter architecture and
   record actual PASS/FAIL/BLOCKED results in the table above.

Do not place process values in this document.
