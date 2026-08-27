#!/usr/bin/env bash
# Runs every fuzz target in the repository for a short burst.
#
# The targets used to be listed here one per line, so a target added later ran
# nowhere until somebody remembered to add it. Thirteen of the OPC UA service
# decoders reached production that way with no fuzzing at all. Discovering the
# targets instead means a new one is smoke-tested the moment it exists.
set -euo pipefail

executions="${FUZZTIME:-10000x}"
found=0

while read -r package; do
    # go test -list prints the matching function names, then a summary line.
    while read -r target; do
        case "$target" in
            Fuzz*) ;;
            *) continue ;;
        esac
        found=$((found + 1))
        echo "== $package $target =="
        go test "$package" -run='^$' -fuzz="^${target}$" -fuzztime="$executions"
    done < <(go test "$package" -list='^Fuzz' 2>/dev/null)
done < <(go list ./...)

if [ "$found" -eq 0 ]; then
    echo "no fuzz targets were found, which means this script stopped working"
    exit 1
fi
echo "ran $found fuzz targets"
