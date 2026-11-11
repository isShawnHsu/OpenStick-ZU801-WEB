# Cross-compile uz801d on Windows (PowerShell).
param(
  [string]$Version = "0.1.0",
  [string]$Go = "go"
)

$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

$commit = "unknown"
try { $commit = (git rev-parse --short HEAD 2>$null) } catch { }
if (-not $commit) { $commit = "unknown" }
$buildTime = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
$ldflags = "-s -w -X uz801d/internal/buildinfo.Version=$Version -X uz801d/internal/buildinfo.Commit=$commit -X uz801d/internal/buildinfo.BuildTime=$buildTime"

New-Item -ItemType Directory -Force -Path dist | Out-Null
$env:CGO_ENABLED = "0"

Write-Host "==> linux/arm64"
$env:GOOS = "linux"; $env:GOARCH = "arm64"; Remove-Item Env:GOARM -ErrorAction SilentlyContinue
& $Go build -trimpath -ldflags $ldflags -o dist/uz801d-linux-arm64 ./cmd/uz801d

Write-Host "==> linux/arm (armv7)"
$env:GOOS = "linux"; $env:GOARCH = "arm"; $env:GOARM = "7"
& $Go build -trimpath -ldflags $ldflags -o dist/uz801d-linux-armv7 ./cmd/uz801d

Write-Host "==> linux/amd64"
$env:GOOS = "linux"; $env:GOARCH = "amd64"; Remove-Item Env:GOARM -ErrorAction SilentlyContinue
& $Go build -trimpath -ldflags $ldflags -o dist/uz801d-linux-amd64 ./cmd/uz801d

Write-Host "==> windows/amd64 (smoke test only)"
$env:GOOS = "windows"; $env:GOARCH = "amd64"
& $Go build -trimpath -ldflags $ldflags -o dist/uz801d-windows-amd64.exe ./cmd/uz801d

Write-Host "==> done"
Get-ChildItem dist | Select-Object Name, Length, LastWriteTime | Format-Table
