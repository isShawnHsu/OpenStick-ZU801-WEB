# UZ801 Manager（uz801d）

> 把 UZ801 / OpenStick 随身 Wi-Fi 变成一台可以本地管理的 4G 路由器。

UZ801 Manager 是一个运行在 **OpenStick Debian** 上的轻量级 Web 管理后台，用来替代 UZ801
随身 Wi-Fi 的官方网页界面。它直接调用设备上的 NetworkManager、ModemManager、hostapd、
dnsmasq、iw、nftables 和 systemd，提供蜂窝网络、APN、短信、Wi-Fi 热点、客户端管理、
局域网、流量统计和系统运维等一整套功能。

- **本地优先**：不依赖厂商云服务，管理页面、配置和流量记录都在设备本地
- **单文件部署**：Go 静态二进制，前端全部内嵌，不需要 Node.js、数据库或额外 Web 服务
- **低资源占用**：适合 512 MB 内存级别的 OpenStick
- **移动端优先**：默认中文界面，支持中/英切换，手机浏览器可直接操作
- **额外能力**：RaspAP 风格的 Wi-Fi 桥接（AP-STA 中继），未插卡时也能当无线路由器使用

> 本项目只提供管理程序，**不是刷机固件**。使用前设备需要已经刷好 OpenStick Debian，
> 并且可以通过 SSH 登录。

---

## 目录

- [它适合用来做什么](#它适合用来做什么)
- [功能一览](#功能一览)
- [快速开始](#快速开始)
- [部署详解](#部署详解)
- [使用指南](#使用指南)
- [重要注意事项](#重要注意事项)
- [配置与状态文件](#配置与状态文件)
- [安全建议](#安全建议)
- [常见问题](#常见问题)
- [开发与构建](#开发与构建)
- [HTTP API](#http-api)
- [目录结构](#目录结构)
- [反馈与贡献](#反馈与贡献)
- [使用许可与免责声明](#使用许可与免责声明)

---

## 它适合用来做什么

典型使用场景：

- **替代官方后台**：官方界面功能有限或已经不可用，希望统一在本地管理设备
- **随身 Wi-Fi / 备用网络**：插卡当 4G 路由器，查看信号、流量、短信和在线设备
- **家庭或办公室备用线路**：主网络故障时用蜂窝数据临时顶替
- **Wi-Fi 中继**：没有插卡时，连接一个上游 Wi-Fi，同时继续发射自己的热点，让下游设备
  通过 NAT 共享上游网络
- **流量管理**：按月统计流量、保留历史、设置配额，避免超量
- **远程设备维护**：配合 frp 等内网穿透工具，远程查看设备状态和日志

适合的人群：

| 适合 | 不适合 |
| --- | --- |
| 已经刷好 OpenStick Debian 的 UZ801 用户 | 仍在使用官方 Android/厂商固件的设备 |
| 希望数据留在本地、不要云账号的人 | 需要厂商云管理、远程定位等官方服务的人 |
| 需要短信、APN、热点、桥接等完整功能的用户 | 期待开箱即用的成品固件、不想接触 Linux 的用户 |
| 愿意通过 SSH 安装和排查问题的用户 | 要求 5 GHz 热点或双频并发的场景（UZ801 硬件不支持） |

---

## 功能一览

| 模块 | 说明 |
| --- | --- |
| 登录鉴权 | pbkdf2-sha256 密码散列、会话 Cookie、CSRF 校验、登录限流、安全响应头 |
| 状态总览 | 蜂窝、SIM、热点、桥接、系统资源、流量、接口一览（并发采集） |
| 蜂窝网络 | 流量开关、飞行模式（关闭蜂窝射频但保留 Wi-Fi）、APN 增删改与保存即连接、网络模式、运营商扫描与注册、USSD、PIN/PUK 解锁、模块重置 |
| 短信 | 列表/详情/发送/删除、全部已读、未读计数 |
| 热点 | SSID、密码、加密方式、信道（仅 2.4 GHz）、隐藏网络、最大客户端、国家代码；支持 nmcli 或 hostapd 后端 |
| Wi-Fi 桥接 | 未启用蜂窝数据时连接上游 Wi-Fi，同时继续发射本机热点，下游设备经 NAT 共享上游网络 |
| 客户端 | 在线/离线、信号、收发流量、备注、拉黑（nftables MAC 过滤）、清除离线记录 |
| 局域网 | 路由器 IP、子网掩码、DHCP 范围、DNS、租期 |
| 流量 | 按账期（月）统计，保留最近 36 个月历史，当前账期按日细分，实时速率、配额与超限断网 |
| 系统 | 设备信息、系统日志、可配置路径的 FRPC 日志、LED 控制、Ping/DNS 诊断、改密、会话时长、重启/关机/恢复出厂 |

---

## 快速开始

### 1. 确认设备环境

需要满足：

- UZ801 / OpenStick，已刷好 **OpenStick Debian**
- 可以通过 SSH 以 root 登录
- 设备可以访问互联网（安装依赖时需要）

查看 CPU 架构，决定下载哪个二进制：

```sh
uname -m
```

| 输出 | 使用 |
| --- | --- |
| `aarch64` | `uz801d-linux-arm64`（最常见） |
| `armv7l` | `uz801d-linux-armv7` |
| `x86_64` | `uz801d-linux-amd64` |

### 2. 获取二进制

从项目的 GitHub Releases 下载对应架构文件；如果没有 Release，也可以自行编译：

```sh
git clone <你的仓库地址>
cd UZ801
./build.sh 0.1.0
```

产物在 `dist/`：

- `dist/uz801d-linux-arm64`
- `dist/uz801d-linux-armv7`
- `dist/uz801d-linux-amd64`
- `dist/uz801d-windows-amd64.exe`（仅用于 Windows 预览界面，不用于设备部署）

### 3. 安装到设备

```sh
scp dist/uz801d-linux-arm64 root@192.168.68.1:/tmp/uz801d
scp -r deploy root@192.168.68.1:/tmp/
ssh root@192.168.68.1

cd /tmp
sh deploy/install.sh /tmp/uz801d --install-deps
```

`--install-deps` 会用 apt 安装缺失的系统组件。如果设备已经装好依赖，可以去掉这个参数。
安装脚本默认会立即启动服务；只想安装、不启动时加 `--no-start`。

### 4. 登录后台

浏览器打开：

```text
http://192.168.68.1
```

默认监听 **80 端口**。初始账号信息在：

```sh
cat /etc/uz801d/initial-password
```

默认用户名是 `admin`（可在后台修改），密码是安装时生成的随机密码。

> **第一次登录后请立即修改密码**：系统 → 修改管理密码。

---

## 部署详解

### 系统要求

OpenStick Debian 建议安装以下组件：

```sh
sudo apt-get update
sudo apt-get install -y network-manager modemmanager hostapd dnsmasq iw nftables iproute2 rfkill
```

| 组件 | 用途 |
| --- | --- |
| NetworkManager（`nmcli`） | 热点、上游 Wi-Fi、蜂窝连接 |
| ModemManager（`mmcli`） | 蜂窝模块、SIM、短信、USSD |
| hostapd | hostapd 后端热点与桥接 AP |
| dnsmasq | DHCP / DNS |
| iw | 无线扫描、虚拟 AP 接口、能力检测 |
| nftables（`nft`） | NAT 与 MAC 过滤 |
| systemd | 开机自启与桥接 AP 托管 |

缺少某个组件时，只有对应功能不可用，后台仍可启动。依赖缺失或关键命令执行失败时，
相关操作会返回错误，不会只改界面状态假装成功。

### 安装脚本做了什么

`deploy/install.sh` 会：

1. 检查并可选安装系统依赖；
2. 安装程序到 `/usr/local/bin/uz801d`；
3. 首次安装时生成 `/etc/uz801d/config.json` 和随机管理密码
   （写入 `/etc/uz801d/initial-password`，修改密码后自动删除）；
4. 安装 `uz801d.service`、`uz801d-ap.service`、`uz801d-dhcp.service`；
5. 设置 `uz801d.service` 开机自启并启动。

`uz801d-ap.service` 和 `uz801d-dhcp.service` 只在桥接或 hostapd 热点需要时由主程序拉起，
**不要手动设置开机自启**。

### 默认路径与端口

| 内容 | 路径 / 值 |
| --- | --- |
| 程序 | `/usr/local/bin/uz801d` |
| 配置 | `/etc/uz801d/config.json`（0600，写入时保留 `.bak`） |
| 状态 | `/var/lib/uz801d/state.json` |
| 初始密码 | `/etc/uz801d/initial-password` |
| 监听地址 | `:80`（可在配置中修改） |
| 服务 | `uz801d.service` |

### 升级

升级只需要替换二进制并重启服务。已有配置和流量/短信状态会保留：

```sh
scp dist/uz801d-linux-arm64 root@192.168.68.1:/tmp/uz801d
scp -r deploy root@192.168.68.1:/tmp/
ssh root@192.168.68.1
cd /tmp && sh deploy/install.sh /tmp/uz801d
```

或者手动替换二进制后执行：

```sh
systemctl restart uz801d
```

> **注意**：前端页面是编译进二进制的。只刷新浏览器不会更新界面，必须替换二进制并重启服务。
>
> 旧版本默认端口是 `8080`。新版本启动时会自动把 `":8080"` 迁移为 `":80"`，`install.sh`
> 也会做同样的迁移；自定义的其他端口不会被改动。**旧二进制本身不会迁移**，所以必须部署新构建。

### 卸载

```sh
sh deploy/uninstall.sh          # 停止服务，删除程序；保留配置与状态
sh deploy/uninstall.sh --purge  # 连同 /etc/uz801d 和 /var/lib/uz801d 一起删除
```

### 手动运行（调试）

```sh
uz801d -config /etc/uz801d/config.json -state /var/lib/uz801d/state.json -listen :80 -debug
```

常用命令：

```sh
systemctl status uz801d
journalctl -u uz801d -f
uz801d -version
uz801d -config /etc/uz801d/config.json -reset-password   # 生成新的随机密码
```

---

## 使用指南

### 状态总览

首页汇总蜂窝注册状态、SIM、热点、桥接、系统资源、流量和网络接口。数据并发采集，
适合快速判断设备当前是否正常。

### 蜂窝网络与 APN

- **APN**：在“蜂窝网络 → APN”中新增或编辑，填写名称、APN、用户名/密码（可选）。
  “保存并连接”会先保存配置，再立即拨号。
- **流量开关**：开启时使用当前 APN 拨号，关闭时断开 PDP，不改变 APN 选择。
- **网络模式**：可限制为 4G/3G/2G 等组合，具体取决于模块支持。
- **USSD / PIN / PUK / 模块重置**：在蜂窝网络页对应区域操作。
- **飞行模式**：
  - 开启后依次执行 `nmcli radio wwan off`、`rfkill block wwan`、`mmcli --disable`
    （存在哪个就用哪个），彻底关闭蜂窝射频以降低发热，**Wi-Fi 热点不受影响**。
  - 退出时依次执行 `rfkill unblock wwan`、`nmcli radio wwan on`、`mmcli --enable`。
  - 进入飞行模式时如果流量处于开启状态，退出后会自动用当前 APN 拨号；进入时流量是关闭的，
    退出后只恢复信号、不拨号。该状态保存在配置中，重启后仍保持飞行模式。
  - 飞行模式期间点击“开启流量”会提示先退出飞行模式。
  - 系统里 `nmcli`、`rfkill`、`mmcli` 全都不存在，或存在的工具全部执行失败时，操作会返回
    错误并保持飞行模式关闭，不会假装成功。

### 短信

支持短信列表、详情、发送、删除、全部已读和未读计数。短信依赖 ModemManager；
如果短信页为空，先在设备上确认：

```sh
mmcli -L
```

部分模块需要先解锁 PIN 才能收发短信。

### Wi-Fi 热点

- UZ801 的板载无线网卡只支持 **2.4 GHz**。页面固定显示 2.4 GHz，信道范围 `0–14`
  （`0` 表示自动）。旧配置中的 5 GHz 设置会在加载时自动改回 2.4 GHz / 自动。
- 可设置 SSID、密码、加密方式、隐藏网络、最大客户端数和国家代码。
- 后端可选 `nmcli`、`hostapd` 或自动检测。
- **停止热点**会先关闭 NetworkManager 热点连接并禁用其自动重连，再停止 hostapd/dnsmasq
  和桥接单元。只有确认 AP 真正停止后才提示成功，仍在运行时会返回具体错误。

### Wi-Fi 桥接（AP-STA 中继）

适用场景：**没有启用蜂窝数据时**（不插卡，或插卡但未拨号），设备连接一个上游 Wi-Fi
（例如家里的路由器），同时继续发射自己的热点。下游设备连接 UZ801 的热点后，通过 NAT
共享上游网络；配合 frp 还可以远程访问下游设备。

实现方式与 RaspAP 的 AP-STA 中继一致：

1. `wlan0` 作为 STA 通过 NetworkManager 连接上游 Wi-Fi，获取 IP 与默认路由；
2. 在同一物理网卡上创建虚拟 AP 接口 `uap0`
   （`iw dev wlan0 interface add uap0 type __ap`），或使用检测到的独立无线网卡；
3. hostapd 在 AP 接口上起热点；单网卡时必须与上游同信道，程序会自动跟随；
4. dnsmasq 在 AP 接口上提供 DHCP；
5. nftables 的 `inet uz801d_bridge` 表做转发放行与 `masquerade`。

桥接页面支持：

- 上游 SSID、密码、加密方式、附近 Wi-Fi 扫描
- AP 网卡选择（自动 / `uap0` / 外接网卡）
- AP 信道（`0` 跟随上游）与路由优先级（metric）
- 未启用蜂窝数据时自动启用桥接
- **流量优先**：一旦开启蜂窝流量，桥接立即停用；数据开启期间也不能手动启用桥接
- **5 分钟上游超时**：桥接运行中上游 Wi-Fi 连续 5 分钟无法获得地址时，自动停用桥接并恢复正常热点
- **强制启用**：驱动未在 `iw list` 中声明 managed+AP 并发时仍可尝试
- 启动失败自动回滚并恢复原热点，避免设备失联
- 运行时 watchdog：AP 进程退出自动重启，上游掉线自动重连
- 自动启用失败或上游超时停用后，5 分钟内不再自动重试；手动点击“启用桥接”会立即重试
- 重启后自动恢复上次已启用的桥接（若蜂窝数据已启用则跳过）

> **插拔 SIM 卡本身不会影响桥接状态**；只有蜂窝数据真正开启时才会停用桥接。这是固定行为。

### 客户端管理

- 查看在线/离线客户端、信号强度、收发流量；
- 给客户端设置备注；
- 拉黑/解黑（通过 nftables MAC 过滤）；
- 清除离线记录。

### 局域网 / DHCP

可修改路由器 IP、子网掩码、DHCP 地址范围、DNS 和租期。

> 修改局域网 IP 后，后台管理地址也会变化。请确保新地址可达，并提前保留 SSH 或 USB 网络
> 作为恢复手段。

### 流量统计

- 账期按“每月重置日”划分（默认 1 号，即自然月），重置日可设为 1–28。
- 当前账期的总流量、上下行、实时速率和配额显示在流量页顶部。
- **月度历史**保留最近 36 个已结束账期；账期切换时自动归档并清零当前账期。
- **本账期每日流量**按本地自然日拆分，从账期开始到今天逐日列出，没有流量的日期显示 0。
- 日/月数据随 `/var/lib/uz801d/state.json` 持久化。
  - “重置本账期”只清当前账期和每日明细，月度历史保留；
  - “重置累计流量”会连同历史一起清除。
- 可设置流量配额并在超限时断网（`data.enforce_quota`）。

### 系统与诊断

- 查看设备信息、系统日志；
- **FRPC 日志**：填写 frpc 客户端日志文件的绝对路径（例如 `/var/log/frpc.log`）并保存，
  页面即可读取最近 N 行（默认 200 行，最新日志在最上方）。路径保存在
  `system.frpc_log_path`，重启后保留。
- **Ping / DNS 诊断**：网络页的 Ping 默认目标是 `www.baidu.com`，进入网络页会自动执行一次；
  也可以改成其他地址。DNS 解析使用系统解析器。
- LED 控制、修改密码、会话时长、重启/关机/恢复出厂。

> 恢复出厂设置默认**保留管理密码**，避免设备变成无凭证状态。

---

## 重要注意事项

1. **必须先有 OpenStick Debian**
   本项目不是刷机包，不负责解锁、刷写引导或安装 Debian。

2. **UZ801 只支持 2.4 GHz Wi-Fi**
   页面不会提供 5 GHz 选项。旧配置里的 5 GHz 频段和 5G 信道会自动改回 2.4 GHz / 自动。

3. **单网卡 AP+STA 取决于无线驱动**
   UZ801 常见的 WCN3620/WCN36xx 是否能同时做 STA 和 AP 并不确定。页面会显示 `iw list`
   的能力检测结果。若驱动不支持：
   - 优先使用外接 USB 无线网卡（程序会自动优先把它作为 AP）；
   - 或勾选“强制启用”后自行验证（失败会自动恢复原热点）。

4. **桥接与蜂窝数据互斥**
   蜂窝数据开启时不能使用桥接；SIM 卡插入/拔出本身不会改变桥接状态。

5. **默认端口 80，未启用 TLS**
   后台只适合在局域网使用。需要公网访问时，请通过 frp、VPN 或带 TLS 的反向代理，
   不要把 80 端口直接暴露到公网。

6. **程序需要 root 权限**
   它会修改网络配置、hostapd/dnsmasq 配置、nftables 规则和 systemd 服务。请只从可信来源
   获取二进制，并在升级前备份配置。

7. **不要把辅助服务设为开机自启**
   `uz801d-ap.service` 和 `uz801d-dhcp.service` 由主程序按需拉起，手动 enable 可能与
   普通热点/桥接冲突。

8. **修改网络配置可能导致失联**
   修改局域网 IP、启用桥接或强制 AP+STA 时，管理页面可能短暂断开。请提前准备 SSH、
   USB 网络或串口作为恢复通道。

9. **Windows 版仅用于预览**
   Windows 构建可以查看界面、调用部分 API，但蜂窝、热点、桥接等依赖 Linux 组件，
   无法真正工作。DNS 诊断依赖 `getent`，Windows 上不可用。

10. **旧二进制不会自动迁移端口**
    升级到 80 端口必须部署新构建；只改 `config.json` 而不重启也不会生效。

---

## 配置与状态文件

默认配置 `/etc/uz801d/config.json`（0600）。主要字段：

```jsonc
{
  "listen": ":80",
  "admin_user": "admin",
  "password_hash": "pbkdf2-sha256$...",
  "session_hours": 12,
  "language": "zh",
  "modem": {
    "backend": "auto",              // auto | mmcli | nmcli
    "connection_manager": "auto",
    "interface": "wwan0",
    "allowed_modes": "auto",        // auto | 4g | 3g | 2g | 4g3g
    "ip_type": "ipv4",
    "apn_profiles": [],
    "active_profile": "cmcc",
    "flight_mode": false,
    "flight_resume_data": false
  },
  "wifi": {
    "backend": "auto",              // auto | nmcli | hostapd | none
    "interface": "wlan0",
    "ssid": "4G-UFI-XX",
    "password": "1234567890",
    "security": "wpa2",             // wpa2 | wpa3 | open
    "band": "bg",                   // UZ801 仅支持 2.4 GHz
    "channel": 6,
    "bridge": {
      "auto": true,
      "force": false,
      "ap_interface": "auto",
      "upstream_ssid": "",
      "upstream_password": "",
      "upstream_security": "wpa2",
      "route_metric": 50,
      "channel": 0
    }
  },
  "lan": {
    "ip": "192.168.68.1",
    "netmask": "255.255.255.0",
    "dhcp_start": "192.168.68.100",
    "dhcp_end": "192.168.68.200",
    "dns": ["223.5.5.5", "119.29.29.29"],
    "lease_time": "12h"
  },
  "data": {
    "interface": "wwan0",
    "quota_bytes": 0,
    "reset_day": 1,
    "enforce_quota": false
  },
  "system": {
    "allow_reboot": true,
    "allow_poweroff": true,
    "allow_factory_reset": true,
    "log_lines": 200,
    "frpc_log_path": "/var/log/frpc.log"
  }
}
```

状态文件 `/var/lib/uz801d/state.json` 保存流量累计、月度历史、当前账期每日明细、短信已读
标记、客户端历史和 MAC 黑名单。

备份与迁移时，建议同时备份：

```sh
tar czf uz801d-backup.tgz /etc/uz801d /var/lib/uz801d
```

### 修改管理密码

- 后台：系统 → 修改管理密码；
- 命令行：

  ```sh
  uz801d -config /etc/uz801d/config.json -reset-password
  ```

  该命令会生成随机密码并写入 `/etc/uz801d/initial-password`。

通过后台修改密码成功后，`/etc/uz801d/initial-password` 会被自动删除。

---

## 安全建议

- 第一次登录后立即修改默认随机密码，并考虑修改默认管理员用户名；
- 不要直接把 80 端口暴露到公网，使用 VPN、frp + HTTPS 或反向代理；
- 定期备份 `/etc/uz801d` 和 `/var/lib/uz801d`；
- 不要从不可信来源下载二进制，升级前核对文件来源；
- 如果设备丢失或转手，执行恢复出厂或 `uninstall.sh --purge` 清除配置和流量/短信状态。

程序本身已包含：

- 登录失败按 IP 限流；
- 会话默认 12 小时，可在后台调整；
- 所有写接口要求 CSRF Token；
- Cookie 为 `HttpOnly` + `SameSite=Strict`；
- 响应头启用 CSP、`X-Frame-Options: DENY`、`nosniff`、`no-referrer` 等；
- 密码使用 pbkdf2-sha256（210000 次迭代）散列存储；
- 外部命令参数经过白名单/格式校验，避免注入。

---

## 常见问题

**后台打不开？**

```sh
systemctl status uz801d
journalctl -u uz801d -b
ss -lntp | grep :80
```

确认 80 端口没有被其他服务占用，设备防火墙没有拦截，并且浏览器访问的是设备当前 IP。

**升级后界面没变化？**

前端是编译进二进制的。必须替换 `/usr/local/bin/uz801d` 并重启服务，然后强制刷新浏览器
（Ctrl+F5 / 清除缓存）。只执行 `git pull` 或刷新页面不会更新界面。

**配置里还是 8080？**

新版本会在启动时把旧的默认 `":8080"` 自动迁移为 `":80"`；`install.sh` 也会迁移。
但**旧二进制不会迁移**，必须部署新构建。自定义端口不会被自动修改。

**热点起不来？**

确认 `hostapd` 配置和网卡名（默认 `wlan0`）。`nmcli` 后端要求 NetworkManager 管理该网卡，
可以切换到 hostapd 后端排除 NetworkManager 干扰：

```sh
journalctl -u NetworkManager -u uz801d -f
```

**点击“停止热点”没有效果？**

新版本会先关闭 NetworkManager 连接并禁用自动重连，再停止 hostapd/dnsmasq 和桥接单元，
最后逐项验证。如果仍然失败，页面会显示具体是哪一项还在运行（例如
`hostapd.service 仍在运行`），根据提示处理即可。

**桥接提示驱动不支持？**

单网卡 AP+STA 需要驱动声明并发能力。先查看：

```sh
iw list
```

确认 interface combinations 是否包含 managed + AP。若没有，优先使用外接 USB 无线网卡，
或勾选“强制启用”并自行验证（失败会自动回滚）。

**桥接后管理页面打不开？**

桥接会短暂重建热点。若上游连接失败，程序会自动恢复原热点。若设备彻底失联，可通过
USB 网络或串口登录，停止桥接：

```sh
systemctl stop uz801d
# 编辑 /etc/uz801d/config.json，把 wifi.bridge.auto 改为 false
systemctl start uz801d
```

**流量统计为 0？**

统计基于 `/sys/class/net/<iface>/statistics`。到“流量 → 统计网卡”确认选择的是实际上网
网卡（蜂窝通常是 `wwan0`）。

**短信 / 模块不可用？**

确认 ModemManager 正在运行，并且能看到模块：

```sh
systemctl status ModemManager
mmcli -L
```

部分模块需要先解锁 PIN。

**忘记管理密码？**

```sh
uz801d -config /etc/uz801d/config.json -reset-password
cat /etc/uz801d/initial-password
```

然后重启服务，用新密码登录。

---

## 开发与构建

要求：

- Go **1.23+**
- Linux 或 Windows 开发机
- 交叉编译不需要目标设备上的 Go 环境

构建全部目标：

```sh
./build.sh 0.1.0
```

Windows PowerShell：

```powershell
.\build.ps1 -Version 0.1.0 -Go "C:\Program Files\Go\bin\go.exe"
```

产物：

```text
dist/uz801d-linux-arm64
dist/uz801d-linux-armv7
dist/uz801d-linux-amd64
dist/uz801d-windows-amd64.exe
```

运行测试：

```sh
go vet ./...
go test ./...
```

Windows 下预览界面与 API：

```powershell
.\dist\uz801d-windows-amd64.exe -config "$env:TEMP\uz801d\config.json" -state "$env:TEMP\uz801d\state.json" -init -password "ChangeMe123"
.\dist\uz801d-windows-amd64.exe -config "$env:TEMP\uz801d\config.json" -state "$env:TEMP\uz801d\state.json" -listen 127.0.0.1:80
```

前端位于 `web/static/`，是普通 HTML/CSS/JavaScript，通过 `go:embed` 打进二进制，
没有构建步骤。修改前端后必须重新编译 Go 程序。

---

## HTTP API

统一响应格式：

```json
{"ok": true, "data": {}}
{"ok": false, "error": "错误信息"}
```

除登录、会话和健康检查外，均需要会话 Cookie `uz801d_session`；所有写操作还需要
`X-CSRF-Token`（值来自 `/api/session` 或登录响应的 `data.csrf`）。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/health` | 健康检查（公开） |
| GET | `/api/session` | 会话与 CSRF（公开） |
| POST | `/api/login` / `/api/logout` | 登录 / 退出 |
| GET | `/api/status` | 聚合状态 |
| GET | `/api/network` | 接口、路由、DNS |
| GET/POST | `/api/modem`、`/api/modem/connect`、`/api/modem/disconnect` | 蜂窝状态与拨号 |
| POST/DELETE | `/api/modem/apn`、`/api/modem/apn/{id}/activate`、`/api/modem/apn/{id}` | APN 管理（保存时 `activate=true` 会先保存再立即拨号，拨号失败通过 `connect_error` 返回，配置仍然保留） |
| POST | `/api/modem/modes`、`/api/modem/scan`、`/api/modem/register`、`/api/modem/ussd`、`/api/modem/pin`、`/api/modem/reset`、`/api/modem/flight` | 模块操作与飞行模式 |
| GET/POST/DELETE | `/api/sms`、`/api/sms/{id}`、`/api/sms/send`、`/api/sms/read` | 短信（`DELETE /api/sms` 删除全部） |
| GET/POST | `/api/wifi`、`/api/wifi/scan`、`/api/wifi/restart`、`/api/wifi/stop` | 热点 |
| GET/POST | `/api/bridge`、`/api/bridge/enable`、`/api/bridge/disable`、`/api/bridge/scan` | Wi-Fi 桥接 |
| GET/POST | `/api/clients`、`/api/clients/block`、`/api/clients/unblock`、`/api/clients/rename`、`/api/clients/forget` | 客户端 |
| GET/POST | `/api/lan` | 局域网 / DHCP |
| GET/POST | `/api/traffic`、`/api/traffic/reset`、`/api/traffic/lifetime` | 流量 |
| GET/POST | `/api/system`、`/api/system/logs`、`/api/system/frpc-log`、`/api/system/leds`、`/api/system/led`、`/api/system/password`、`/api/system/language`、`/api/system/admin-user`、`/api/system/session-hours`、`/api/system/reboot`、`/api/system/poweroff`、`/api/system/factory-reset`、`/api/system/ping`、`/api/system/dns` | 系统 |
| GET | `/api/config` | 导出配置（不含密码散列） |

---

## 目录结构

```text
cmd/uz801d/          程序入口与命令行
internal/auth        密码、会话、登录限流
internal/config      配置模型、校验、原子保存
internal/parse       mmcli/nmcli/iw/hostapd/dnsmasq 输出解析
internal/server      HTTP 路由、鉴权、CSRF、静态资源
internal/svc         业务实现（modem/wifi/bridge/clients/traffic/system）
internal/state       持久化运行状态
internal/sysinfo     /proc、/sys 系统信息
internal/run         外部命令执行、超时与输出解码
web/static           内嵌前端（index/style/i18n/pages/app）
deploy               systemd 单元与安装/卸载脚本
```

---

## 反馈与贡献

提交 Issue 时建议附上：

- UZ801 / OpenStick 的具体型号和系统版本（`uname -a`、`cat /etc/os-release`）
- 相关日志：

  ```sh
  journalctl -u uz801d -b --no-pager | tail -n 100
  ```

- 模块信息（如与蜂窝相关）：

  ```sh
  mmcli -L
  mmcli -m 0
  ```

- 无线能力（如与热点/桥接相关）：

  ```sh
  iw list
  ```

- 复现步骤、预期结果和实际结果。

欢迎提交 Pull Request。修改前端后请重新编译并说明测试方式；涉及网络/桥接的改动请先在
真机上验证，或在 PR 中明确说明未验证的部分。

---

## 使用许可与免责声明

本项目仅用于个人设备管理与学习用途。使用前请确认当地关于热点共享、流量转发和无线电
使用的合规要求。

- 本项目不是 UZ801 厂商官方产品，与厂商无关；
- 修改网络、热点、防火墙和 systemd 配置可能导致设备失联，请自行做好备份和恢复准备；
- 软件按“现状”提供，不对因使用造成的设备损坏、数据丢失或网络问题承担责任。

除仓库中另有 `LICENSE` 文件外，本项目当前未声明正式开源许可证；如需分发或商用，请先与作者确认。
