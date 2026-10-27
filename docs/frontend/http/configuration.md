# Configuration

[Frontend overview](README.md) · [Documentation](../../README.md)

Exactly one of `OPCDA_SOURCE_PROG_ID` and `OPCDA_SOURCE_CLSID` identifies the
source; neither has a default, because there is no source this adapter may pick
on an operator's behalf.

`opcda-access-adapter setup` can create a versioned, bounded configuration for
`run --config` or Windows Service execution. File-based execution does not
merge ambient environment variables. See [guided setup](../../operations/setup.md). The
existing no-argument mode reads the environment variables below. New files use
configuration version 4; versions 1, 2, and 3 remain readable.

| Environment variable | Default | Purpose |
|---|---:|---|
| `OPCDA_SOURCE_PROG_ID` | *one of two* | the local OPC DA source's ProgID |
| `OPCDA_SOURCE_CLSID` | *one of two* | the same source's exact CLSID, when there is no ProgID |
| `OPCDA_FRONTEND` | `http` | select `http`, `grpc`, `opcua`, or `mcp` |
| `OPCDA_HTTP_LISTEN` | `127.0.0.1:8080` | HTTP bind address |
| `OPCDA_WRITE_ENABLED` | `false` | enable value Write explicitly |
| `OPCDA_MAX_HTTP_BODY_BYTES` | `1048576` | request body bound |
| `OPCDA_MAX_HTTP_CONNECTIONS` | `64` | accepted TCP connection bound |
| `OPCDA_MAX_CONCURRENT_REQUESTS` | `32` | HTTP concurrency bound |
| `OPCDA_MAX_HTTP_HEADER_BYTES` | `32768` | request header bound |
| `OPCDA_MAX_JSON_DEPTH` | `64` | JSON object/array nesting bound |
| `OPCDA_HTTP_READ_HEADER_TIMEOUT` | `5s` | incomplete-header timeout |
| `OPCDA_HTTP_READ_TIMEOUT` | `15s` | complete request read timeout |
| `OPCDA_HTTP_WRITE_TIMEOUT` | `15s` | response write timeout |
| `OPCDA_HTTP_IDLE_TIMEOUT` | `30s` | keep-alive idle timeout |
| `OPCDA_REQUEST_DEADLINE` | `10s` | frontend deadline |
| `OPCDA_RECONNECT_INITIAL` | `1s` | initial reconnect backoff |
| `OPCDA_RECONNECT_MAX` | `30s` | maximum reconnect backoff |
| `OPCDA_COM_CALL_WATCHDOG` | `30s` | threshold before fail-fast degraded state |
| `OPCDA_COMMAND_QUEUE` | `64` | serialized DA command queue bound |
| `OPCDA_MAX_READ_ITEMS` | `100` | Read batch bound |
| `OPCDA_MAX_WRITE_ITEMS` | `100` | Write batch bound |
| `OPCDA_MAX_BROWSE_ENTRIES` | `1000` | Browse result hard limit |
| `OPCDA_MAX_BROWSE_DEPTH` | `64` | Browse navigation depth bound |
| `OPCDA_MAX_REGISTERED_ITEMS` | `1024` | lazy item-registration cache bound |
| `OPCDA_MAX_ITEM_ID_BYTES` | `1024` | exact ItemID UTF-8 byte bound |
| `OPCDA_MAX_BSTR_CODE_UNITS` | `65536` | source/request BSTR UTF-16 bound |
| `OPCDA_MAX_ITEM_PROPERTIES` | `64` | DA item properties per item bound |
| `OPCDA_MAX_ARRAY_ELEMENTS` | `1024` | elements one SAFEARRAY value may carry |
| `OPCDA_MAX_ARRAY_DIMENSIONS` | `4` | dimensions one SAFEARRAY value may have |
| `OPCDA_MAX_ARRAY_BSTR_CODE_UNITS` | `65536` | UTF-16 code units all of one array's string elements carry together |

gRPC-specific listener and transport bounds are documented in the
[gRPC API reference](../grpc/README.md), and the OPC UA endpoint settings in the
[OPC UA configuration](../opcua/README.md#configuration). Only one frontend is
selected per adapter process; all frontends call the same DA runtime
semantics.

The runtime also applies the hard per-batch, Browse, ItemID, BSTR, command
queue, and registration limits recorded in ADR-0001. No recent operation or
process-value history is retained in memory, so there is no unbounded
diagnostic ring. Logs go directly to the configured process output and never
include process values; deployment-level collection and rotation remain the
operator's responsibility.

"By default" would be the wrong hedge here: there is no setting that turns
value logging on. The guarantee is structural. The packages that handle values
-- `internal/opcda`, `internal/opcua` and the frontends -- do not log at all.
Every logging call in the adapter is in `cmd/adapter`, and none carries a value:
they log an address, a frontend name, a CLI argument, and errors. So the only
route a value could take into a log is inside an error message, and no error
message carries one -- they carry the VARTYPE, which is the part worth
reporting.

`TestValueHandlingPackagesDoNotLog` fails the build if a value-handling package
acquires a logging import **or writes anything out directly** — a `fmt.Print`
or a use of `os.Stdout` would break the guarantee just as completely as a `log`
call, and the reasoning above depends on neither existing. Building a string
with `fmt.Errorf` or `fmt.Sprintf` stays allowed: that hands it to a caller to
decide about rather than emitting it. This is what keeps the reasoning true
rather than merely currently accurate.

Individually valid settings must also fit the aggregate memory ceilings in
[ADR-0008](../../design/adr/0008-http-origin-and-aggregate-bounds.md). This prevents, for
example, a maximum body size multiplied by maximum concurrency from admitting
a multi-gigabyte in-flight workload. Unsafe combinations fail startup.
