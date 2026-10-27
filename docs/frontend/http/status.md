# Status

[Frontend overview](README.md) · [Documentation](../../README.md)

```text
GET /v1/status
```

Status reports runtime, source, connection generation, capabilities, Write
enablement, listener state, queue depth, reconnect count, and any degraded
reason. It never contains process values. In `degraded` state the reason tells
the operator to restart the process; the adapter does not terminate a hung COM
thread.

When a connection attempt or an established connection fails at the COM/OPC
method layer, status retains exactly one bounded diagnostic until the next
successful connection:

```json
{
  "state": "disconnected",
  "source": {
    "progId": "Vendor.Server.1",
    "connectionGeneration": 0,
    "lastError": {
      "operation": "CoCreateInstance(IOPCServer)",
      "hresult": {"value": -2147024891, "hex": "0x80070005"}
    }
  }
}
```

This record never contains an ItemID or process value. It is not an error
history and does not replace per-item Read/Write HRESULTs.
