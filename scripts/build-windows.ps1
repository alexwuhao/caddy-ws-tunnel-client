# Build tunnel-agent for Windows (amd64 by default, optionally arm64).
# Usage:
#   powershell -File scripts\build-windows.ps1
#   powershell -File scripts\build-windows.ps1 -Arch arm64 -Version v1.1.0
param(
    [ValidateSet("amd64", "arm64")]
    [string]$Arch = "amd64",
    [string]$Version = "dev"
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Push-Location $root
try {
    $env:CGO_ENABLED = "0"
    $env:GOOS = "windows"
    $env:GOARCH = $Arch
    $out = "tunnel-agent-windows-$Arch.exe"
    Write-Host "building $out (version=$Version)"
    go build -trimpath -ldflags "-s -w -X main.version=$Version" -o $out ./cmd/tunnel-agent
    if ($LASTEXITCODE -ne 0) { throw "go build failed ($LASTEXITCODE)" }
    $item = Get-Item $out
    Write-Host ("ok: {0} ({1:N0} bytes)" -f $item.FullName, $item.Length)
    & ".\$out" --version
}
finally {
    Pop-Location
}
