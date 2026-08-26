#!/usr/bin/env bash
# Runs the third-party OPC UA client interoperability suite.
#
# The adapter's UA frontend is served over a scripted DA source by
# internal/validation/uainterop, so this runs on any platform: it exercises the
# UA layer, which is where a foreign client's judgement is needed. The DA side
# is validated separately against a real DA server on Windows.
#
# asyncua is used as an interop client only, never as an implementation
# dependency. Design §5.2 allows exactly that.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../.." && pwd)"
workdir="${INTEROP_WORKDIR:-$(mktemp -d)}"
python="${INTEROP_PYTHON:-$workdir/venv/bin/python}"

if [ ! -x "$python" ]; then
    echo "== preparing the interop client =="
    python3 -m venv "$workdir/venv" 2>/dev/null || python3 -m venv --without-pip "$workdir/venv"
    if ! "$workdir/venv/bin/python" -m pip --version >/dev/null 2>&1; then
        curl -sSLo "$workdir/get-pip.py" https://bootstrap.pypa.io/get-pip.py
        "$workdir/venv/bin/python" "$workdir/get-pip.py" -q
    fi
    "$workdir/venv/bin/python" -m pip -q install asyncua
fi

echo "== building the harness =="
go build -o "$workdir/uainterop" "$root/internal/validation/uainterop"
# The real-DA probe is built too, so a change to the UA wire format that would
# break the Windows validation run is caught here rather than in CI.
go build -o "$workdir/opcuaprobe" "$root/internal/validation/opcuaprobe"

# Each configuration gets its own harness, because a source either implements
# the optional DA interfaces or it does not, and write is on or off.
declare -a pids=()
cleanup() { for pid in "${pids[@]:-}"; do kill "$pid" 2>/dev/null || true; done; }
trap cleanup EXIT

start() { # start <port> <flags...>
    local port="$1"; shift
    "$workdir/uainterop" -listen "127.0.0.1:$port" "$@" >"$workdir/harness-$port.log" 2>&1 &
    pids+=($!)
}

start 48411
start 48412 -browse=false
start 48413 -write-enabled
sleep 2

status=0
echo; echo "== a source implementing every optional DA interface =="
"$python" "$here/ua_client_conformance.py" "opc.tcp://127.0.0.1:48411" || status=1
echo; echo "== the standard Server object =="
"$python" "$here/ua_server_nodes.py" "opc.tcp://127.0.0.1:48411" || status=1
echo; echo "== a source that does not implement Browse =="
"$python" "$here/ua_client_conformance.py" "opc.tcp://127.0.0.1:48412" --browseless || status=1
echo; echo "== write enabled =="
"$python" "$here/ua_client_conformance.py" "opc.tcp://127.0.0.1:48413" --write || status=1

echo; echo "== the real-DA probe against the same frontend =="
"$workdir/opcuaprobe" \
    -address 127.0.0.1:48413 \
    -endpoint-url "opc.tcp://127.0.0.1:48413" \
    -security-policy-uri "http://opcfoundation.org/UA/SecurityPolicy#None" \
    -write-enabled || status=1

echo
if [ "$status" -ne 0 ]; then
    echo "INTEROP FAILED"
else
    echo "INTEROP PASSED"
fi
exit "$status"
