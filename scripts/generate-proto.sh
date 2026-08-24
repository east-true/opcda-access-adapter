#!/usr/bin/env bash
set -euo pipefail

required_protoc="libprotoc 36.0"
required_protoc_gen_go="protoc-gen-go v1.36.12"
required_protoc_gen_go_grpc="protoc-gen-go-grpc 1.6.2"

[[ "$(protoc --version)" == "$required_protoc" ]] || {
  echo "protoc must be $required_protoc" >&2
  exit 1
}
[[ "$(protoc-gen-go --version)" == "$required_protoc_gen_go" ]] || {
  echo "protoc-gen-go must be $required_protoc_gen_go" >&2
  exit 1
}
[[ "$(protoc-gen-go-grpc --version)" == "$required_protoc_gen_go_grpc" ]] || {
  echo "protoc-gen-go-grpc must be $required_protoc_gen_go_grpc" >&2
  exit 1
}

protoc \
  --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  api/opcda/v1/opcda_access.proto
