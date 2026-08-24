#!/usr/bin/env bash

set -euo pipefail

if [[ $# -lt 2 || $# -gt 3 ]]; then
  echo "usage: $0 VERSION OUTPUT_DIRECTORY [COMMIT_SHA]" >&2
  exit 2
fi

release_version=$1
output_directory=$2
repository_root=$(git rev-parse --show-toplevel)
release_commit=${3:-$(git -C "$repository_root" rev-parse HEAD)}

if [[ ! $release_version =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z][0-9A-Za-z.-]*)?$ ]]; then
  echo "VERSION must be a v-prefixed semantic version without build metadata" >&2
  exit 2
fi
if [[ ! $release_commit =~ ^[0-9a-f]{40}$ ]]; then
  echo "COMMIT_SHA must be a full lowercase Git commit SHA" >&2
  exit 2
fi
if [[ -e $output_directory ]] && [[ -n $(find "$output_directory" -mindepth 1 -maxdepth 1 -print -quit) ]]; then
  echo "OUTPUT_DIRECTORY must be absent or empty" >&2
  exit 2
fi

mkdir -p "$output_directory"
output_directory=$(cd "$output_directory" && pwd)
source_date_epoch=${SOURCE_DATE_EPOCH:-$(git -C "$repository_root" show -s --format=%ct "$release_commit")}
if [[ ! $source_date_epoch =~ ^[0-9]+$ ]]; then
  echo "SOURCE_DATE_EPOCH must be an unsigned integer" >&2
  exit 2
fi

staging_root=$(mktemp -d)
cleanup() {
  rm -rf -- "$staging_root"
}
trap cleanup EXIT
cd "$repository_root"

archive_version=${release_version#v}
for architecture in 386 amd64; do
  archive_base="opcda-access-adapter_${archive_version}_windows_${architecture}"
  package_directory="$staging_root/$archive_base"
  mkdir -p "$package_directory/api/opcda/v1"

  env GOOS=windows GOARCH="$architecture" CGO_ENABLED=0 \
    go build -trimpath -buildvcs=false \
    -ldflags "-s -w -X main.version=$release_version -X main.commit=$release_commit" \
    -o "$package_directory/opcda-access-adapter.exe" ./cmd/adapter
  cp "$repository_root/LICENSE" "$repository_root/README.md" \
    "$repository_root/THIRD_PARTY_NOTICES.md" "$package_directory/"
  cp "$repository_root/api/opcda/v1/opcda_access.proto" "$package_directory/api/opcda/v1/"
  touch -d "@$source_date_epoch" \
    "$package_directory" "$package_directory"/* \
    "$package_directory/api/opcda" "$package_directory/api/opcda/v1" \
    "$package_directory/api/opcda/v1/opcda_access.proto"

  (
    cd "$staging_root"
    TZ=UTC zip -X -q "$output_directory/$archive_base.zip" \
      "$archive_base/LICENSE" \
      "$archive_base/README.md" \
      "$archive_base/THIRD_PARTY_NOTICES.md" \
      "$archive_base/api/opcda/v1/opcda_access.proto" \
      "$archive_base/opcda-access-adapter.exe"
  )
done

(
  cd "$output_directory"
  LC_ALL=C sha256sum ./*.zip | sed 's|  \./|  |' > SHA256SUMS
  sha256sum --check SHA256SUMS
)
