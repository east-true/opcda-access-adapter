## Summary

<!-- Describe the user-visible change and its scope. -->

## Checks

- [ ] `gofmt -w .` and `git diff --check`
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] Windows 386 and amd64 builds/tests (when applicable)
- [ ] Documentation and `docs/implementation-status.md` updated
- [ ] No process values or secrets included in logs, fixtures, or screenshots
- [ ] Scope/invariants in `docs/design.md` remain unchanged, or an ADR is included

## Validation notes

<!-- Link to relevant CI runs or authorized real-DA evidence. -->
