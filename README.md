# OPC DA Access Adapter

[![CI](https://github.com/east-true/opcda-access-adapter/actions/workflows/ci.yml/badge.svg)](https://github.com/east-true/opcda-access-adapter/actions/workflows/ci.yml)
[![Real DA validation](https://github.com/east-true/opcda-access-adapter/actions/workflows/real-da-validation.yml/badge.svg)](https://github.com/east-true/opcda-access-adapter/actions/workflows/real-da-validation.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

A Windows adapter that gives modern clients access to one local OPC DA server
through [HTTP/JSON](docs/frontend/http/README.md),
[typed gRPC](docs/frontend/grpc/README.md),
[OPC UA](docs/frontend/opcua/README.md), or [MCP stdio](docs/frontend/mcp/README.md).
COM stays on the DA host;
source identifiers, types, Quality, timestamps, and errors retain their meaning.

The adapter supports Browse, device Read, strict typed Write, item properties,
and DA-native subscriptions. Each process selects one source and one frontend.
HTTP is the default, MCP runs only when selected, and Write is disabled by default.

This is a pre-1.0, source-only project for controlled local Windows deployments.
There is no stable binary release or broad vendor compatibility claim. OPC UA
supports `SecurityPolicy None` only and is not production ready. Review the
[executed compatibility evidence](docs/validation/compatibility.md) before deployment.

## Get started

Follow the [quick start](docs/quick-start.md) to build the adapter, select a
local source, and read a known ItemID. Windows `386` and `amd64` are supported;
the executable must match the server's COM registration.

## Documentation

- [Documentation index](docs/README.md)
- [Architecture](docs/architecture.md)
- [Frontends: HTTP, gRPC, OPC UA, MCP](docs/frontend/README.md)
- [Implementation status](docs/implementation-status.md)

## Contributing and security

See [CONTRIBUTING.md](CONTRIBUTING.md) for the development workflow. Report
vulnerabilities privately through [SECURITY.md](SECURITY.md).

## License

[Apache License 2.0](LICENSE). Dependency licenses are in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
