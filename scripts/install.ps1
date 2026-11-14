$ErrorActionPreference = 'Stop'
$Base = Split-Path $PSScriptRoot -Parent
& "$PSScriptRoot\build.ps1"
& "$Base\bin\packctl.exe" --cache "$Base\runtime\parsers" --bundle-dir "$Base\runtime\bundles" --download-all --verify-all
if ($LASTEXITCODE -ne 0) { throw 'Parser installation or verification failed.' }
