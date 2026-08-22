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
The project does not claim a production authentication, authorization, or TLS
model. Exposing it beyond the local machine is an operator decision that must
be protected by an appropriate deployment boundary.
