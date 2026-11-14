#!/bin/sh
# Builds bin/TreeSitterMCP and bin/packctl and places the native FFI library
# next to them, so the binaries run directly from bin/ (no launcher needed).
set -eu
base=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$base"
: "${GO:=go}"
case "$(uname -s):$(uname -m)" in
 Linux:x86_64) platform=linux-x86_64; rpath='$ORIGIN' ;;
 Linux:aarch64|Linux:arm64) platform=linux-aarch64; rpath='$ORIGIN' ;;
 Darwin:arm64) platform=macos-arm64; rpath='@executable_path' ;;
 Darwin:x86_64) platform=macos-x86_64; rpath='@executable_path' ;;
 *) echo "Unsupported platform; use build.ps1 on Windows." >&2; exit 1 ;;
esac
export CGO_ENABLED=1
# Resolve the FFI library relative to the executable itself.
export CGO_LDFLAGS="-Wl,-rpath,$rpath${CGO_LDFLAGS:+ $CGO_LDFLAGS}"
"$GO" run ./cmd/native-setup --root "$base"
mkdir -p bin
cp -f third_party/languagepack/.lib/"$platform"/*ts_pack_core_ffi* bin/
"$GO" build -buildvcs=false -trimpath -o bin/TreeSitterMCP ./cmd/TreeSitterMCP
"$GO" build -buildvcs=false -trimpath -o bin/packctl ./cmd/packctl
