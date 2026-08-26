#!/usr/bin/env bash
# Runs the third-party OPC UA client interoperability suite.
#
# The adapter's UA frontend is served over a scripted DA source by
# internal/validation/uainterop, so this runs on any platform: it exercises the
# UA layer, which is where a foreign client's judgement is needed. The DA side
# is validated separately against a real DA server on Windows.
#
# Three independent clients judge the server, because they disagree with it in
# different ways when it is wrong: asyncua hand-rolls its codec in Python,
# open62541 is C, and the OPC Foundation .NET stack is the Foundation's own
# reference implementation. All three are interop clients only, never
# implementation dependencies; design §5.2 names asyncua and open62541 among
# the projects that may be exactly that.
#
# asyncua is always run. open62541 and the .NET stack are run when their
# toolchains are present, and skipped with a notice when they are not, so this
# stays runnable on a machine with only Python.
#
# INTEROP_O62_CLIENT points at a prebuilt open62541 client binary.
# INTEROP_DOTNET points at a dotnet binary, and INTEROP_NETSTACK_PROJECT at a
# prepared project directory.
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
echo; echo "===== asyncua ====="
echo; echo "== a source implementing every optional DA interface =="
"$python" "$here/ua_client_conformance.py" "opc.tcp://127.0.0.1:48411" || status=1
echo; echo "== the standard Server object =="
"$python" "$here/ua_server_nodes.py" "opc.tcp://127.0.0.1:48411" || status=1
echo; echo "== a source that does not implement Browse =="
"$python" "$here/ua_client_conformance.py" "opc.tcp://127.0.0.1:48412" --browseless || status=1
echo; echo "== write enabled =="
"$python" "$here/ua_client_conformance.py" "opc.tcp://127.0.0.1:48413" --write || status=1

echo; echo "===== open62541 ====="
o62="${INTEROP_O62_CLIENT:-$workdir/o62_conformance}"
if [ ! -x "$o62" ] && [ -n "${INTEROP_O62_ROOT:-}" ]; then
    echo "== building the open62541 client =="
    cc -o "$o62" "$here/open62541/conformance.c" \
        -I"$INTEROP_O62_ROOT/build/src_generated" \
        -I"$INTEROP_O62_ROOT/build/src_generated/open62541" \
        -I"$INTEROP_O62_ROOT/include" -I"$INTEROP_O62_ROOT/plugins/include" \
        -I"$INTEROP_O62_ROOT/arch" \
        "$INTEROP_O62_ROOT/build/bin/libopen62541.a" -lpthread -lm
fi
if [ -x "$o62" ]; then
    echo; echo "== a source implementing every optional DA interface =="
    "$o62" "opc.tcp://127.0.0.1:48411" || status=1
    echo; echo "== a source that does not implement Browse =="
    "$o62" "opc.tcp://127.0.0.1:48412" --browseless || status=1
    echo; echo "== write enabled =="
    "$o62" "opc.tcp://127.0.0.1:48413" --write || status=1
else
    echo "SKIPPED: no open62541 client. Set INTEROP_O62_ROOT to a built"
    echo "         open62541 checkout, or INTEROP_O62_CLIENT to a built binary."
fi

echo; echo "===== OPC Foundation .NET stack ====="
dotnet_bin="${INTEROP_DOTNET:-$(command -v dotnet || true)}"
netstack="${INTEROP_NETSTACK_PROJECT:-$workdir/netstack}"
if [ -n "$dotnet_bin" ] && [ -x "$dotnet_bin" ]; then
    if [ ! -f "$netstack/uaconformance.csproj" ]; then
        echo "== preparing the .NET client =="
        DOTNET_CLI_TELEMETRY_OPTOUT=1 DOTNET_NOLOGO=1 \
            "$dotnet_bin" new console -o "$netstack" --force >/dev/null
        DOTNET_CLI_TELEMETRY_OPTOUT=1 DOTNET_NOLOGO=1 \
            "$dotnet_bin" add "$netstack" package OPCFoundation.NetStandard.Opc.Ua.Client >/dev/null
    fi
    cp "$here/netstack/Program.cs" "$netstack/Program.cs"
    DOTNET_CLI_TELEMETRY_OPTOUT=1 DOTNET_NOLOGO=1 \
        "$dotnet_bin" build "$netstack" -v q --nologo >/dev/null
    run_netstack() {
        DOTNET_CLI_TELEMETRY_OPTOUT=1 DOTNET_NOLOGO=1 \
            "$dotnet_bin" run --project "$netstack" --no-build -- "$@" 2>&1 |
            grep -vE "^(warn|info|trce)"
        return "${PIPESTATUS[0]}"
    }
    echo; echo "== a source implementing every optional DA interface =="
    run_netstack "opc.tcp://127.0.0.1:48411" || status=1
    echo; echo "== a source that does not implement Browse =="
    run_netstack "opc.tcp://127.0.0.1:48412" --browseless || status=1
    echo; echo "== write enabled =="
    run_netstack "opc.tcp://127.0.0.1:48413" --write || status=1
else
    echo "SKIPPED: no dotnet. Set INTEROP_DOTNET to a dotnet binary."
fi

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
