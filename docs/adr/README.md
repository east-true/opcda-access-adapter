# Architecture decisions

These records explain accepted choices and their constraints. Their context
describes the implementation at the time, not a current feature inventory.
Use [architecture](../architecture.md) and [implementation status](../implementation-status.md)
for the present behavior.

| ADR | Decision |
|---|---|
| [0001](0001-v0-bounds-and-runtime-defaults.md) | v0 bounds and defaults |
| [0002](0002-sta-runtime-and-local-com-activation.md) | Dedicated STA and local COM |
| [0003](0003-read-types-and-filetime-presence.md) | Read types and timestamp presence |
| [0004](0004-strict-typed-value-write.md) | Strict value-only Write |
| [0005](0005-reconnect-and-com-hang-policy.md) | Reconnect and COM hangs |
| [0006](0006-real-da-validation-fixture.md) | Source-built DA fixture |
| [0007](0007-bounded-source-failure-diagnostic.md) | Bounded failure diagnostic |
| [0008](0008-http-origin-and-aggregate-bounds.md) | HTTP browser boundary and limits |
| [0009](0009-request-parser-and-lifecycle-hardening.md) | Strict parsing and lifecycle |
| [0010](0010-local-da-registration-detection.md) | Registration-only detection |
| [0011](0011-guided-setup-and-windows-service.md) | Setup and Windows Service |
| [0012](0012-grpc-da-native-frontend.md) | DA-native unary gRPC |
| [0013](0013-da-native-subscribe-core.md) | DA Subscribe core |
| [0014](0014-grpc-subscribe-streaming.md) | gRPC streaming |
| [0015](0015-subscribe-capability-probe.md) | Callback capability probe |
| [0016](0016-opcua-frontend-scope-and-mapping.md) | UA scope and mapping |
| [0017](0017-third-party-vendor-da-fixture.md) | Vendor validation; option C executed once |
| [0018](0018-da-item-properties.md) | Item Properties |
| [0019](0019-safearray-representation.md) | SAFEARRAY representation |
| [0020](0020-mcp-stdio-frontend.md) | Explicit MCP stdio |
