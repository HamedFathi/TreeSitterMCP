#!/bin/sh
set -eu
base=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
sh "$base/scripts/build.sh"
"$base/bin/packctl" --cache "$base/runtime/parsers" --bundle-dir "$base/runtime/bundles" --download-all --verify-all
