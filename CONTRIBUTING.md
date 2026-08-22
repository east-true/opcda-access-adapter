# Contributing

Read `docs/design.md` and `AGENTS.md` before proposing a change. They define
the source-semantics and product-boundary rules for this repository.

## Development checks

Run the following before opening a pull request:

```text
gofmt -w .
go test ./...
go vet ./...
GOOS=windows GOARCH=386 go build ./cmd/adapter
GOOS=windows GOARCH=amd64 go build ./cmd/adapter
```

Changes to resource limits, defaults, or runtime behavior require an ADR and
an update to `docs/implementation-status.md`. Do not claim an OPC DA server is
compatible without recording the actual environment and result in
`docs/compatibility.md`.

## Pull requests

Keep a PR focused on one implementation phase. Include tests and documentation
for behavior changes. No PR may add a source protocol, remote DCOM,
normalization, process-data persistence, multi-server behavior, or an
unapproved frontend.
