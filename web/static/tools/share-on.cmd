@echo off
setlocal
net session >nul 2>&1
if errorlevel 1 (
    echo Requesting administrator rights...
    powershell -NoProfile -Command "Start-Process -Verb RunAs -FilePath '%~f0'" >nul 2>&1
    exit /b
)
powershell -NoProfile -ExecutionPolicy Bypass -Command "$t = Get-Content -LiteralPath '%~f0' -Encoding UTF8; $i = [array]::IndexOf($t, '#PS1#'); if ($i -lt 0) { Write-Host 'embedded script missing'; exit 1 }; $s = ($t[($i+1)..($t.Count-1)]) -join [Environment]::NewLine; $p = Join-Path $env:TEMP 'msm8916d-share-on.ps1'; [IO.File]::WriteAllText($p, $s, (New-Object Text.UTF8Encoding($true))); try { & $p } finally { Remove-Item $p -Force -ErrorAction SilentlyContinue }"
exit /b
#PS1#
# 一键把电脑的网络共享给设备的 USB 网卡（ICS）
# 用法：右键 share-on.cmd → 以管理员身份运行
#       powershell -ExecutionPolicy Bypass -File share-on.ps1 -DryRun   只查看，不改动

[CmdletBinding()]
param([switch]$DryRun)

$ErrorActionPreference = 'Stop'

$isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin -and -not $DryRun) {
    Write-Host "需要管理员权限：请运行 share-on.cmd（会自动提权），或者以管理员身份打开 PowerShell 再执行本脚本。" -ForegroundColor Yellow
    exit 1
}

function Get-IcsConnections {
    $mgr = New-Object -ComObject HNetCfg.HNetShare
    foreach ($conn in $mgr.EnumEveryConnection) {
        $props = $mgr.NetConnectionProps($conn)
        [pscustomobject]@{
            Conn   = $conn
            Name   = $props.Name
            Device = $props.DeviceName
            Guid   = $props.Guid
            Cfg    = $mgr.INetSharingConfigurationForINetConnection($conn)
        }
    }
}

Write-Host "== 枚举网络连接 ==" -ForegroundColor Cyan
$all = @(Get-IcsConnections)
foreach ($c in $all) {
    $state = if ($c.Cfg.SharingEnabled) { if ($c.Cfg.SharingConnectionType -eq 0) { '共享中(公网)' } else { '共享中(私网)' } } else { '-' }
    Write-Host ("  {0,-22} {1,-45} {2}" -f $c.Name, $c.Device, $state)
}

# 1) 找设备的 USB 网卡
$gadget = $all | Where-Object { $_.Device -match 'UsbNcm|RNDIS|Remote NDIS|CDC-ECM|CDC ECM|USB Ethernet|UsbNcm Host' } | Select-Object -First 1
if (-not $gadget) {
    throw "没找到设备的 USB 网卡（UsbNcm / RNDIS）。请确认设备已插好，并且网页上开着共享。"
}

# 2) 找有默认路由的那块网卡（上网用的）
$routes = Get-NetRoute -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue |
    Where-Object { $_.NextHop -ne '0.0.0.0' } |
    Sort-Object RouteMetric, ifIndex
$internetIface = $null
foreach ($r in $routes) {
    $ad = Get-NetAdapter -InterfaceIndex $r.ifIndex -ErrorAction SilentlyContinue
    if (-not $ad) { continue }
    if ($ad.InterfaceDescription -match 'UsbNcm|RNDIS|Remote NDIS|CDC-ECM') { continue }
    $internetIface = $ad
    break
}
if (-not $internetIface) { throw "没找到上网用的网卡（没有默认路由）" }

$public = $all | Where-Object { $_.Guid -ieq $internetIface.InterfaceGuid } | Select-Object -First 1
if (-not $public) {
    $public = $all | Where-Object { $_.Name -eq $internetIface.Name } | Select-Object -First 1
}
if (-not $public) { throw "无法把默认路由网卡映射到共享接口：$($internetIface.Name)" }

Write-Host ""
Write-Host "上网网卡 : $($public.Name)  ($($public.Device))" -ForegroundColor Green
Write-Host "共享到   : $($gadget.Name)  ($($gadget.Device))" -ForegroundColor Green

if ($DryRun) {
    Write-Host ""
    Write-Host "DryRun 模式：未做任何修改。去掉 -DryRun 即可实际开启。" -ForegroundColor Yellow
    exit 0
}

Write-Host ""
Write-Host "== 启动 ICS 服务 ==" -ForegroundColor Cyan
$svc = Get-Service SharedAccess -ErrorAction SilentlyContinue
if ($svc) {
    if ($svc.StartType -ne 'Automatic') { Set-Service SharedAccess -StartupType Automatic }
    if ($svc.Status -ne 'Running') { Start-Service SharedAccess }
} else {
    Write-Host "警告：SharedAccess 服务不存在，ICS 可能不可用" -ForegroundColor Yellow
}

Write-Host "== 先关闭已有的共享 ==" -ForegroundColor Cyan
foreach ($c in $all) {
    if ($c.Cfg.SharingEnabled) {
        $c.Cfg.DisableSharing()
        Write-Host "  关闭 $($c.Name)"
    }
}

Write-Host "== 开启共享（公网 → 私网）==" -ForegroundColor Cyan
$public.Cfg.EnableSharing(0) | Out-Null
Start-Sleep -Milliseconds 500
$gadget.Cfg.EnableSharing(1) | Out-Null
Start-Sleep -Seconds 2

Write-Host ""
Write-Host "== 结果 ==" -ForegroundColor Cyan
foreach ($c in (Get-IcsConnections)) {
    if ($c.Cfg.SharingEnabled) {
        $t = if ($c.Cfg.SharingConnectionType -eq 0) { '公网' } else { '私网' }
        Write-Host "  $($c.Name) → 共享 $t"
    }
}
$usb = Get-NetIPAddress -InterfaceAlias $gadget.Name -AddressFamily IPv4 -ErrorAction SilentlyContinue
if ($usb) { Write-Host ("设备 USB 网卡现在的 IP: " + ($usb.IPAddress -join ', ')) }

Write-Host ""
Write-Host "完成。现在到设备网页（手机连热点用 http://192.168.69.1/）选 USB → WiFi 并启用。" -ForegroundColor Green
Write-Host "停止共享：运行 share-off.cmd" -ForegroundColor DarkGray
