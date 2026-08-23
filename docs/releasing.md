# Release procedure

This project has no stable release yet. A maintainer may exercise the exact
packaging path without publishing anything, but creating a version tag is the
explicit publication boundary.

## Dry run

Run the `Release` workflow manually with a v-prefixed semantic version such
as `v0.0.0-dev`. The workflow builds both Windows architectures, verifies the
archives, and retains the artifacts for seven days. A manual run never creates
a GitHub release.

The same packaging path can be exercised locally on Linux:

```bash
./scripts/release/package.sh v0.0.0-dev dist "$(git rev-parse HEAD)"
(cd dist && sha256sum --check SHA256SUMS)
```

The output directory must be absent or empty. Each archive contains only the
architecture-specific executable, `LICENSE`, and `README.md`. Release builds
embed the version and full commit SHA, which can be inspected on Windows:

```powershell
.\opcda-access-adapter.exe --version
```

## Publishing

Before creating a tag:

1. confirm that the target commit is on protected `main`;
2. confirm normal CI and real-DA validation for that exact executable revision;
3. reconcile `docs/implementation-status.md` and `docs/compatibility.md`;
4. review the generated archives and `SHA256SUMS` from a manual dry run; and
5. confirm the release notes do not overstate vendor compatibility or
   production readiness.

Push an annotated `vMAJOR.MINOR.PATCH` tag only after those gates pass. The
tag workflow rejects commits outside `main` history, rebuilds both Windows
archives, publishes their checksums, and creates GitHub artifact attestations.
A version containing a prerelease suffix, for example `v0.0.0-rc.1`, is marked
as a prerelease.

Do not reuse or move a published tag. If an artifact is wrong, publish a new
version and document the superseded release.

## Verifying downloaded artifacts

Download both ZIP archives and `SHA256SUMS` from the same GitHub release, then
verify them before extraction:

```bash
sha256sum --check SHA256SUMS
```

GitHub's artifact attestation can also be verified with the GitHub CLI:

```bash
gh attestation verify opcda-access-adapter_*.zip \
  --repo east-true/opcda-access-adapter
gh attestation verify SHA256SUMS \
  --repo east-true/opcda-access-adapter
```

Checksums and attestations establish artifact integrity and build provenance;
they do not establish compatibility with an untested OPC DA server.
