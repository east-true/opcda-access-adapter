# Security Policy

## Supported versions

The project is pre-v0. Security fixes are made on `main`; no release line is
currently supported.

## Reporting a vulnerability

Please do not open a public issue for a suspected vulnerability. Report it to
the repository owner through GitHub's private security advisory flow, with a
minimal reproduction and impact description.

## Security posture

The v0 HTTP listener defaults to loopback and Write is disabled by default.
Loopback mode rejects non-loopback Host values, POST endpoints require
`application/json`, and direct browser Origin requests are rejected. These
controls reduce DNS-rebinding and browser request-forgery exposure but are not
an authentication mechanism.
The parser also rejects duplicate/case-aliased JSON fields, excessive nesting,
encoded or query-bearing endpoint aliases, and compressed request bodies.
The project does not claim a production authentication, authorization, or TLS
model. Exposing it beyond the local machine is an operator decision that must
be protected by an appropriate deployment boundary.

The adapter requests only in-process or same-machine COM activation. It does
not request remote DCOM. Windows AppID Launch/Activation, Access, server
identity, and 32/64-bit registry-view considerations are documented in
[Windows COM security and permissions](docs/security-windows.md). Do not
weaken machine-wide DCOM defaults to troubleshoot one server.

The `detect` command is a local CLI operation, not an HTTP endpoint. It reads
only the bounded OPC DA 2.0 component-category registration inventory through
the Windows Component Categories Manager. It does not activate a detected
vendor class, probe credentials, select a server, or perform remote discovery.
