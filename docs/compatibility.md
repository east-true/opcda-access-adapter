# Compatibility Matrix and Validation Procedure

## Matrix

The matrix records only executed real local-COM results. Unit doubles,
cross-builds, and Windows ABI tests are not interoperability results.

| DA Server | Version | Windows | Server bitness | Adapter arch | Connect | Browse | Read | Write | Reconnect | Notes |
|---|---|---|---|---|---|---|---|---|---|---|
| OPC Foundation OPC Classic Core Components TestServer | source commit `efe0d1d1` | Server 2025 | x86 | 386 | PASS | PASS | PASS | PASS | PASS | DA 2.05a source-built fixture; [final-main evidence](https://github.com/east-true/opcda-access-adapter/actions/runs/32632091320) |
| OPC Foundation OPC Classic Core Components TestServer | source commit `efe0d1d1` | Server 2025 | x64 | amd64 | PASS | PASS | PASS | PASS | PASS | DA 2.05a source-built fixture; [final-main evidence](https://github.com/east-true/opcda-access-adapter/actions/runs/32632091320) |

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
- Fixture executable SHA-256 for run `32628886186`: x86
  `35B18E2542131907A256929FE1C5A54B204CAB8421AA9F90305E6C8B6583F681`;
  x64 `C715BFA24DE1414D6CC1E8A6B5F61FEE42530EB43A619BC6BD5185A7B0F6DDF7`.

### Long-running resource result

Workflow run
[`32630548279`](https://github.com/east-true/opcda-access-adapter/actions/runs/32630548279)
at adapter head `995c387cb977a37ab80ecd0fc5deb2f4a98e191d` repeated the complete
scenario and then passed 100,000 device Reads per architecture. The x86 job
took 3m04s overall and observed adapter handles `+18`, adapter private bytes
`+5,373,952`, server handles `0`, and server private bytes `-208,896`.
The x64 job took 3m40s overall and observed adapter handles `+18`, adapter
private bytes `+5,787,648`, server handles `+4`, and server private bytes
`+12,288`. All were below the explicit absolute and growth ceilings; no
operation failed and no process value was logged.

### Final main confirmation

Workflow run
[`32632091320`](https://github.com/east-true/opcda-access-adapter/actions/runs/32632091320)
repeated the complete 100,000-Read scenario at main SHA
`9e8928d729300a67197da35e7bfee6623a861495`. Both source and built-output
Defender scans found no threats. The x86 result observed adapter handles
`+16`, adapter private bytes `+5,287,936`, server handles `0`, and server
private bytes `-200,704`; the x64 result observed adapter handles `+18`,
adapter private bytes `+5,730,304`, server handles `+4`, and server private
bytes `+20,480`. VARTYPE, Quality, timestamp presence, HRESULT, Browse,
Write, outage, and reconnect observations matched the recorded result above.

### Windows HTTP and failure stability result

Workflow run
[`32634777223`](https://github.com/east-true/opcda-access-adapter/actions/runs/32634777223)
passed the bounded stability profile on Windows Server 2025 for adapter head
`ccc28487dfc33e1767e3f42c547a6d59a5ae4ca4` on both native x86/386 and
x64/amd64. Each architecture passed:

- normal status, exact nested Browse identity, and ordered partial device Read
  semantics against the real DA fixture;
- malformed/trailing JSON, unknown fields, invalid UTF-8, unpaired surrogate,
  embedded NUL, invalid source/filter/method/path, oversized body/batch/ItemID,
  and excessive Browse-depth rejection;
- 48 incomplete-header connections plus an oversized header. Windows rejected
  the oversized header by closing the connection, and the adapter immediately
  returned a healthy status; all incomplete headers were closed after the
  configured five-second header timeout;
- 5,000 no-delay sequential device Reads and 3,200 mixed requests from 16
  concurrent workers;
- deterministic overload with all 32 HTTP request slots occupied: the next 16
  requests returned bounded frontend `QUEUE_FULL`, and status plus ten device
  Reads succeeded after the blocking connections were closed;
- three consecutive real source unregister/re-register cycles. No successful
  stale value was returned, unavailable state was observed, generations
  advanced through 3, 4, and 5, and the known ItemID was lazy-registered after
  every recovery;
- the normal Browse/Read/Write scenario and a final 200-Read bounded soak.

The x86 HTTP profile completed in 10.873s and observed adapter deltas of `+8`
handles and `+5,640,192` private bytes; server deltas were `0` handles and
`+45,056` private bytes. The x64 profile completed in 10.153s and observed
adapter deltas of `+8` handles and `+6,463,488` private bytes; server deltas
were `+6` handles and `+172,032` private bytes. These samples include the
failure cycles, stability profile, and final soak and remained below the
explicit harness ceilings. Both source and built fixture scans reported no
Microsoft Defender threats. This antivirus result is a bounded observation,
not proof that arbitrary code is harmless. No process value was logged.

Final-main workflow run
[`32635274825`](https://github.com/east-true/opcda-access-adapter/actions/runs/32635274825)
repeated the entire profile at merge SHA
`f55b4bb8e8c092fa2a21f4f35089a14703c81a8d`. The x86 profile completed in
9.322s with adapter deltas of `+16` handles and `+6,725,632` private bytes;
server deltas were `0` handles and `+32,768` private bytes. The x64 profile
completed in 10.26s with adapter deltas of `+16` handles and `+5,763,072`
private bytes; server deltas were `+4` handles and `+143,360` private bytes.
Both architectures passed all semantic, abnormal-input, timeout, rapid,
concurrent, backpressure/recovery, source-failure, and soak assertions, and
both source/output Defender scans found no threats.

### Local registration detection result

PR #24 workflow run
[`32706109366`](https://github.com/east-true/opcda-access-adapter/actions/runs/32706109366)
executed at adapter head `387ba9c269848035980b544cb816dafdef92d2d1`
on ephemeral GitHub-hosted Windows Server 2025 VMs. On both x86/386 and
x64/amd64, the matching detector returned the pinned fixture's exact registered
ProgID and CLSID exactly once. The fixture process count was zero immediately
before and after the command, so registration detection did not activate that
vendor server. Native repeated COM lifecycle tests also passed on both
architectures in CI run
[`32706109315`](https://github.com/east-true/opcda-access-adapter/actions/runs/32706109315).

This observation confirms bounded local component-category enumeration for
the pinned fixture only. A detected registration is not evidence that the
class can activate, that its COM permissions are sufficient, or that its DA
behavior is compatible, so it does not add or upgrade a matrix PASS row.

### Guided setup and Windows Service result

PR #26 workflow run
[`32734190245`](https://github.com/east-true/opcda-access-adapter/actions/runs/32734190245)
executed at adapter head `0fc3684128919ff28f94b9257c0dbc30e34ae328`
on ephemeral GitHub-hosted Windows Server 2025 VMs. Both native x86/386 and
x64/amd64 jobs passed the following assertions against the pinned fixture:

- guided setup displayed the registered fixture and required an explicit
  numeric source selection even though it was the intended candidate;
- HTTP/JSON and Windows Service were selected explicitly, followed by a final
  confirmation before mutation;
- the bounded version 1 configuration contained the fixture's exact CLSID,
  loopback HTTP listener, and `writeEnabled: false`;
- SCM reported automatic start, the `NT AUTHORITY\LocalService` account, and
  an image path using the internal `service run` entry point;
- the service reached connected state through local COM and completed a device
  Read of the known fixture ItemID without enabling Write;
- at least one bounded lifecycle record appeared under the service's Windows
  Application Event Log source; and
- uninstall stopped and deleted the service, removed the Event Log source, and
  the harness removed its temporary configuration directory.

The same jobs then passed the complete foreground semantic/failure regression,
including Browse, ordered partial Read, disabled/typed/denied Write, three real
source outage/reconnect cycles, 5,000 rapid Reads, 3,200 mixed concurrent
requests, bounded overload recovery, and a final 200-Read soak. This proves
that the pinned fixture accepted LocalService under the runner's local COM
permissions. It does not prove that another vendor's AppID launch/access or
RunAs policy will accept that identity, and setup did not modify DCOM security
or fall back to a more privileged account.

This result validates the scoped v0 path against this specific official test
fixture only. Third-party/vendor servers and non-Good Quality observations
remain untested; compatibility must not be inferred for them.

## Real-DA validation procedure

1. On the same Windows machine as an installed DA server, build the matching
   adapter architecture (`windows/386` for a 32-bit-only registration,
   `windows/amd64` where applicable).
2. Run `opcda-access-adapter detect` and record whether the intended local
   `OPC_DA_20` registration appears with its exact CLSID and ProgID. Confirm
   that detection alone does not start the vendor server process. Registration
   detection is not a compatibility PASS.
3. Set exactly one source identifier:

   ```powershell
   $env:OPCDA_SOURCE_PROG_ID = "Vendor.Server.1"
   # or
   $env:OPCDA_SOURCE_CLSID = "{00000000-0000-0000-0000-000000000000}"
   ```

4. Start the adapter with its default loopback listener. Check `GET
   /v1/status`, then Browse root and a nested branch if Browse is supported.
5. Read a known item, a batch with at least one invalid ItemID, and an item
   with non-Good Quality where available. Record exact VARTYPE, Quality,
   Timestamp presence, and HRESULT.
6. Confirm Write is rejected while disabled. Enable it only against a safe
   test item, verify type mismatch and access-denied behavior, then a typed
   value Write.
7. Restart the DA server, observe an unavailable result during disconnect,
   then verify `disconnected` during backoff/`reconnecting` during an attempt,
   an increased reconnect count, a strictly newer connection generation, and
   lazy re-registration rather than old-handle reuse. Do not accept any
   last-good value during the outage.
8. Repeat the required scenarios for each applicable adapter architecture and
   record actual PASS/FAIL/BLOCKED results in the table above.
9. For soak validation, repeatedly exercise a bounded safe Read batch while
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
