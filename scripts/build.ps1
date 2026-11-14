# Builds bin\TreeSitterMCP.exe and bin\packctl.exe and places the native FFI DLL
# next to them. Windows loads DLLs from the executable's folder first, so the
# binaries run directly from bin\ (no launcher needed).
#
# Safe to run while the MCP server is running: an in-use binary is moved aside
# (Windows allows renaming a running executable) and an unchanged DLL is kept.
$ErrorActionPreference = 'Stop'
$Base = Split-Path $PSScriptRoot -Parent
$Platform = if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq 'Arm64') { 'windows-aarch64' } else { 'windows-x86_64' }

# cgo needs a C compiler. Honor CC (CI sets CC=clang on Windows ARM64);
# otherwise use gcc/clang from PATH, then common MSYS2/MinGW-w64 locations.
$arm64 = $Platform -eq 'windows-aarch64'
$cc = if ($env:CC) { $env:CC } elseif ($arm64) { 'clang' } else { 'gcc' }
if (-not (Get-Command $cc -ErrorAction SilentlyContinue)) {
    $candidates = if ($arm64) {
        'C:\msys64\clangarm64\bin'
    } else {
        'C:\msys64\ucrt64\bin', 'C:\msys64\mingw64\bin', 'C:\msys64\clang64\bin', 'C:\TDM-GCC-64\bin', 'C:\ProgramData\mingw64\mingw64\bin'
    }
    $found = $candidates | Where-Object { Test-Path (Join-Path $_ "$cc.exe") } | Select-Object -First 1
    if (-not $found) {
        $hint = if ($arm64) { 'pacman -S mingw-w64-clang-aarch64-clang' } else { 'pacman -S mingw-w64-ucrt-x86_64-gcc' }
        throw "C compiler '$cc' was not found. Install MSYS2 ($hint) or add it to PATH."
    }
    $env:PATH = "$found;$env:PATH"
}
$env:CC = $cc

function Set-Replaceable([string]$Path) {
    if (-not (Test-Path $Path)) { return }
    try {
        [System.IO.File]::Open($Path, 'Open', 'ReadWrite', 'None').Dispose()
    } catch {
        $old = "$Path.$([DateTime]::UtcNow.Ticks).old"
        Move-Item -Force $Path $old
    }
}

Push-Location $Base
try {
    $env:CGO_ENABLED = '1'
    New-Item -ItemType Directory -Force bin | Out-Null
    # Remove binaries moved aside by earlier builds once nothing uses them.
    Get-ChildItem bin -File -ErrorAction SilentlyContinue | Where-Object { $_.Name -like '*.old' -or $_.Name -like '*~' } | ForEach-Object { Remove-Item -Force $_.FullName -ErrorAction SilentlyContinue }

    go run ./cmd/native-setup --root $Base
    if ($LASTEXITCODE -ne 0) { throw 'Native library preparation failed.' }

    $dllSrc = "third_party\languagepack\.lib\$Platform\ts_pack_core_ffi.dll"
    $dllDst = 'bin\ts_pack_core_ffi.dll'
    if (-not (Test-Path $dllDst) -or (Get-FileHash $dllSrc).Hash -ne (Get-FileHash $dllDst).Hash) {
        Set-Replaceable $dllDst
        Copy-Item -Force $dllSrc $dllDst
    }

    Set-Replaceable 'bin\TreeSitterMCP.exe'
    go build -buildvcs=false -trimpath -o bin/TreeSitterMCP.exe ./cmd/TreeSitterMCP
    if ($LASTEXITCODE -ne 0) { throw 'Server build failed.' }
    Set-Replaceable 'bin\packctl.exe'
    go build -buildvcs=false -trimpath -o bin/packctl.exe ./cmd/packctl
    if ($LASTEXITCODE -ne 0) { throw 'Parser utility build failed.' }
} finally { Pop-Location }
