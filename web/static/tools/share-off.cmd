@echo off
setlocal
net session >nul 2>&1
if errorlevel 1 (
    echo Requesting administrator rights...
    powershell -NoProfile -Command "Start-Process -Verb RunAs -FilePath '%~f0'" >nul 2>&1
    exit /b
)
powershell -NoProfile -ExecutionPolicy Bypass -Command "$t = Get-Content -LiteralPath '%~f0' -Encoding UTF8; $i = [array]::IndexOf($t, '#PS1#'); if ($i -lt 0) { Write-Host 'embedded script missing'; exit 1 }; $s = ($t[($i+1)..($t.Count-1)]) -join [Environment]::NewLine; $p = Join-Path $env:TEMP 'msm8916d-share-off.ps1'; [IO.File]::WriteAllText($p, $s, (New-Object Text.UTF8Encoding($true))); try { & $p } finally { Remove-Item $p -Force -ErrorAction SilentlyContinue }"
exit /b
#PS1#
# 关闭电脑上的网络共享（ICS）
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'

$mgr = New-Object -ComObject HNetCfg.HNetShare
$count = 0
foreach ($conn in $mgr.EnumEveryConnection) {
    $props = $mgr.NetConnectionProps($conn)
    $cfg = $mgr.INetSharingConfigurationForINetConnection($conn)
    if ($cfg.SharingEnabled) {
        $cfg.DisableSharing()
        $count++
        Write-Host "已关闭共享: $($props.Name)"
    }
}
if ($count -eq 0) { Write-Host "当前没有开启中的网络共享。" }
