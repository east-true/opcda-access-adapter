# Configuration

[Frontend overview](README.md) · [Documentation](../../README.md)

Guided setup lists HTTP/JSON, gRPC and OPC UA, and requires an explicit
choice:

```powershell
.\opcda-access-adapter.exe setup --grpc-listen 127.0.0.1:50051
```

Select `gRPC`, then foreground, Windows Service, or save only. New setup files
use strict configuration version 4:

```json
{
  "version": 4,
  "source": {
    "clsid": "{00000000-0000-0000-0000-000000000000}"
  },
  "frontend": {
    "type": "grpc",
    "grpcListen": "127.0.0.1:50051"
  },
  "writeEnabled": false
}
```

Versions 1–3 remain readable within their supported frontend scope (gRPC starts
at version 2). A gRPC configuration contains no HTTP or
OPC UA listener and a single adapter process still owns one runtime and one
source.

The original environment workflow can select gRPC explicitly:

| Environment variable | Default | Purpose |
|---|---:|---|
| `OPCDA_FRONTEND` | `http` | select `http`, `grpc`, `opcua`, or `mcp` |
| `OPCDA_GRPC_LISTEN` | `127.0.0.1:50051` | gRPC bind address |
| `OPCDA_MAX_GRPC_RECEIVE_BYTES` | `1048576` | inbound message bound |
| `OPCDA_MAX_GRPC_SEND_BYTES` | `4194304` | outbound message bound |
| `OPCDA_MAX_GRPC_CONNECTIONS` | `16` | accepted TCP connection bound |
| `OPCDA_MAX_CONCURRENT_GRPC_RPCS` | `32` | process-wide admitted unary RPC bound |
| `OPCDA_MAX_GRPC_STREAMS` | `16` | HTTP/2 concurrent streams per connection |
| `OPCDA_MAX_GRPC_METADATA_BYTES` | `32768` | request metadata/header-list bound |
| `OPCDA_GRPC_CONNECTION_TIMEOUT` | `5s` | HTTP/2 connection handshake timeout |
| `OPCDA_GRPC_MAX_CONNECTION_IDLE` | `2m` | idle connection lifetime |
| `OPCDA_GRPC_MAX_CONNECTION_AGE` | `30m` | maximum connection lifetime |
| `OPCDA_GRPC_MAX_CONNECTION_AGE_GRACE` | `30s` | bounded grace after maximum age |
| `OPCDA_GRPC_KEEPALIVE_MIN_TIME` | `30s` | minimum accepted client ping interval |
| `OPCDA_REQUEST_DEADLINE` | `10s` | server-side operation deadline |
| `OPCDA_MAX_SUBSCRIPTIONS` | `16` | concurrent `Subscribe` stream bound |
| `OPCDA_MAX_SUBSCRIPTION_ITEMS` | `100` | items in one `Subscribe` request |

DA batch, Browse, ItemID, queue, reconnect, BSTR, and COM watchdog variables
are shared with HTTP and documented in the HTTP reference.
