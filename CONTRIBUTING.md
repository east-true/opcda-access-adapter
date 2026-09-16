# Contributing

Thanks for helping improve OPC DA Access Adapter. Focused bug fixes, tests,
documentation, and in-scope design proposals are welcome.

This project deliberately has a narrow boundary: one local OPC DA source, one
explicitly selected frontend, and source semantics preserved end to end. Read
the [design baseline](docs/design.md) and [repository instructions](AGENTS.md)
before proposing behavior changes.

## Before opening an issue

- Search existing issues and the [documentation index](docs/README.md).
- Use the bug form for a reproducible defect and the feature form for an
  in-scope proposal.
- Do not post process values, credentials, proprietary ItemIDs, vendor binaries,
  or private server configuration.
- Report security issues privately through the process in
  [SECURITY.md](SECURITY.md).

Proposals for remote DCOM, non-DA sources, multi-server aggregation, tag
mapping or normalization, process-value persistence, or a plugin framework are
outside the current product definition.

## Development environment

Use the Go version declared in [`go.mod`](go.mod). Most unit tests can run on a
non-Windows development machine, but native COM behavior and real-DA claims
require Windows. Both `windows/386` and `windows/amd64` are supported and must
remain buildable.

Clone the repository and run the baseline checks:

```text
gofmt -w .
go test ./...
go test -race ./...
go vet ./...
GOOS=windows GOARCH=386 go build ./cmd/adapter
GOOS=windows GOARCH=amd64 go build ./cmd/adapter
```

On PowerShell, set `$env:GOARCH` for native architecture-specific tests and
remove it afterward. The race detector is run by Linux CI; do not treat an
unsupported local Windows race configuration as a substitute or a failure.

## Making a change

1. Branch from the current `main`.
2. Keep the pull request focused on one concern.
3. Add tests for behavior changes and regression fixes.
4. Update the user-facing reference and
   [`docs/implementation-status.md`](docs/implementation-status.md).
5. Add or update an ADR when changing a decision, default, resource bound, or
   product boundary.
6. Run the checks that apply to the changed paths.

Additional rules protect the adapter's core guarantees:

- Never synthesize source timestamps or return cached last-good values.
- Keep COM pointer use and cleanup on the dedicated owning thread.
- Preserve per-item identity and failures across every frontend.
- Do not log or persist process values, including Write values.
- Keep Write disabled by default, strictly typed, and free of automatic retry
  or replay.
- Do not claim compatibility without an authorized real-server result in
  [`docs/compatibility.md`](docs/compatibility.md).

If the protobuf contract changes, regenerate checked-in bindings with
[`scripts/generate-proto.sh`](scripts/generate-proto.sh) and include both the
contract and generated output in the same pull request.

## Documentation changes

The root README is the short project entrance. Put detailed operating,
protocol, security, and validation material under `docs/` and link it from the
[documentation index](docs/README.md).

Use relative links, keep commands copyable, distinguish executed evidence from
planned work, and avoid certification or broad compatibility language. A docs
change should at minimum pass `git diff --check` and a local-link review.

## Pull requests and CI

`main` is protected, including for administrators. Every change goes through a
pull request based on current `main`, resolves review conversations, and waits
for all applicable checks to finish.

The current CI gate covers:

- formatting, unit tests, race/fuzz smoke tests, vet, and dependency review;
- OPC specification consistency checks;
- third-party OPC UA client interoperability;
- release-package construction and verification;
- native Windows builds and tests on `386` and `amd64`;
- path-scoped real OPC Foundation DA 2.05a validation on both architectures.

Do not merge while an applicable check is failing or pending. In the pull
request description, state what changed, which risks matter, and the exact
commands or environments used to validate it.
