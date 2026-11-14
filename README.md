# MSM8916 Manager（uz801d）

> 把高通 8916（MSM8916）方案的 OpenStick 随身 Wi-Fi，变成一台可以本地管理的 4G 路由器。

MSM8916 Manager 是一个运行在 **OpenStick Debian** 上的轻量级 Web 管理后台，面向所有使用
**高通 8916（MSM8916 / MSM8936 同族）** 芯片的 4G 随身 Wi-Fi，用来替代各家厂商自带的
网页界面。它直接调用设备上的 NetworkManager、ModemManager、hostapd、dnsmasq、iw、
nftables 和 systemd，提供蜂窝网络、APN、短信、Wi-Fi 热点、客户端管理、局域网、流量统计
和系统运维等一整套功能。

- **本地优先**：不依赖厂商云服务，管理页面、配置和流量记录都在设备本地
- **单文件部署**：Go 静态二进制，前端全部内嵌，不需要 Node.js、数据库或额外 Web 服务
- **低资源占用**：适合 512 MB 内存级别的高通 8916 设备
- **移动端优先**：默认中文界面，支持中/英切换，手机浏览器可直接操作
- **额外能力**：WiFi 转 USB 共享，未插卡时也能把上游无线网络通过 USB 口共享给其他设备

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
| 需要短信、APN、热点、WiFi 共享等完整功能的用户 | 期待开箱即用的成品固件、不想接触 Linux 的用户 |
| 愿意通过 SSH 安装和排查问题的用户 | 要求 5 GHz 热点或双频并发的场景（UZ801 硬件不支持） |

---

## 功能一览

| 模块 | 说明 |
| --- | --- |
| 登录鉴权 | pbkdf2-sha256 密码散列、会话 Cookie、CSRF 校验、登录限流、安全响应头 |
| 状态总览 | 蜂窝、SIM、热点、WiFi 共享、系统资源、流量、接口一览（并发采集） |
| 蜂窝网络 | 流量开关、飞行模式（关闭蜂窝射频但保留 Wi-Fi）、APN 增删改与保存即连接、网络模式、运营商扫描与注册、USSD、PIN/PUK 解锁、模块重置 |
| 短信 | 列表/详情/发送/删除、全部已读、未读计数、新短信转发到 Bark / 钉钉 / 飞书 |
| 热点 | SSID、密码、加密方式、信道（仅 2.4 GHz）、隐藏网络、最大客户端、国家代码；支持 nmcli 或 hostapd 后端 |
| WiFi 共享（无线转 USB） | 未启用蜂窝数据时连接上游 Wi-Fi，通过 USB 口给其他设备提供网络；不需要 AP+STA 并发 |
| 客户端 | 在线/离线、信号、收发流量、备注、拉黑（nftables MAC 过滤）、清除离线记录 |
| 局域网 | 路由器 IP、子网掩码、DHCP 范围、DNS、租期 |
| 流量 | 按账期（月）统计，保留最近 36 个月历史，当前账期按日细分，实时速率、配额与超限断网 |
| 系统 | 设备信息、系统日志、可配置路径的 FRPC 日志、LED 控制（完全关闭或夜间定时熄灭）、Ping/DNS 诊断、改密、会话时长、重启/关机/恢复出厂 |

---

## 快速开始

### 1. 确认设备环境

需要满足：

- 高通 8916（MSM8916）设备，已刷好 **OpenStick Debian**
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

`uz801d-ap.service` 和 `uz801d-dhcp.service` 只在旧的 AP-STA 中继或 hostapd 热点需要时由主程序拉起，
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

首页汇总蜂窝注册状态、SIM、热点、WiFi 共享、系统资源、流量和网络接口。数据并发采集，
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

#### 4G 永远拨不上号？先看基带固件

有些 OpenStick 镜像自带的是 **M8936** 的 MPSS 固件，装到 M8916 的 UFI 板子上会这样：

| 现象 | 说明 |
|---|---|
| `mmcli -m any` 显示 `failed / sim-missing` 或 `disabled` | SIM 明明插着 |
| `AT+CPIN?` = `READY`，能读到 IMSI/ICCID | UIM 正常，只是射频起不来 |
| `qmicli -d /dev/wwan0qmi0 --dms-get-operating-mode` → `offline` | |
| `--dms-set-operating-mode=online` → `QMI error 52 DeviceNotReady` | |
| `AT+CFUN=1` → `Phone failure`，`AT+CSQ` → `99,99` | |

正确的固件版本是 `MPSS.DPM.1.0.c7-00193-M8916EAAAANVZM-1`。
安装脚本会自动检查 `/lib/firmware/modem.mdt` 的 sha256，不匹配就备份旧固件到
`/root/fw-backup-*` 并换成 `firmware/modem-m8916/` 里的正确版本，然后提示重启：

```sh
# 先把固件放到仓库根目录的 firmware/modem-m8916/（约 40 MB，不入库，见下）
sudo sh install.sh ./dist/uz801d-linux-arm64 --fix-firmware
```

固件**不在仓库里**（40+ MB，已在 `.gitignore` 中忽略），自己取一份即可：

```sh
# 方式 A：从原厂 modem 分区解（它是 FAT16，里面是 image/ 下的 modem.* / mba.mbn / wcnss.*）
7z x modem.bin -o firmware-stock "image/modem.*" "image/mba.mbn" "image/wcnss.*"
mkdir -p firmware/modem-m8916
cp firmware-stock/image/modem.* firmware-stock/image/mba.mbn firmware-stock/image/wcnss.* firmware/modem-m8916/

# 方式 B：从已有的刷机包/备份里拷
cp /path/to/openstick-flash-kit/firmware/modem-m8916/* firmware/modem-m8916/
```

校验是否放对（三套必须同源）：

```sh
sha256sum firmware/modem-m8916/modem.mdt   # 568515ab…6524
sha256sum firmware/modem-m8916/mba.mbn     # 5d635f58…bc70
sha256sum firmware/modem-m8916/wcnss.mdt   # 2bc71936…846c
```

### 短信

支持短信列表、详情、发送、删除、全部已读和未读计数。短信依赖 ModemManager；
如果短信页为空，先在设备上确认：

```sh
mmcli -L
```

部分模块需要先解锁 PIN 才能收发短信。

页面布局：**发送短信 → 短信列表 → 短信转发**。即使没插 SIM 卡 / ModemManager 不可用，
发送框、列表和转发设置也都会照常显示，只在顶部加一条提示。

### 短信转发

在「短信 → 短信转发」中可以把新收到的短信推送到手机或群机器人，支持三种渠道：

| 渠道 | 需要填写 | 说明 |
| --- | --- | --- |
| Bark | 服务器地址（默认 `https://api.day.app`）、设备 Key | 可选分组和铃声；支持自建 Bark 服务 |
| 钉钉 | 机器人 Webhook | 机器人启用「加签」时填写签名密钥 |
| 飞书 | 机器人 Webhook | 机器人启用「签名校验」时填写签名密钥 |

页面会按所选渠道显示对应的配置步骤，并只展示该渠道需要的字段：Bark 显示设备 Key、分组和铃声；钉钉/飞书显示 Webhook 与签名密钥。切换渠道类型时，地址栏只在仍为默认值时自动切换（Bark 默认 `https://api.day.app`，钉钉/飞书留空等待填写）。

行为说明：

- 只有**保存配置并启用转发后新到达**的短信会被转发，不会补发设备里已有的历史短信；
- 转发配置与短信列表相互独立：即使模块/SIM 异常导致短信列表或发件箱加载失败，转发设置仍可正常打开、保存和测试；
- 后台按设定间隔（默认 60 秒，范围 15–3600 秒）轮询模块，通过 `state.json` 中的指纹去重，重启后不会重复推送；
- 只转发接收方向（`received`）的短信；发送记录不会转发；
- 每条短信会发往所有「已启用」的渠道，单个渠道失败只记录日志，不影响其他渠道；
- 「测试」按钮会用表单里当前填写的内容发送一条示例消息（无需先保存），便于先验证 Webhook；
- 三个渠道都是 HTTPS 请求，依赖系统 CA 证书。精简 rootfs 里常常没有
  `/etc/ssl/certs/ca-certificates.crt`，这时「测试」会报
  `x509: certificate signed by unknown authority`。装 `ca-certificates` 后**重启 uz801d**
  （Go 会缓存证书池）即可；`install.sh --install-deps` 已经包含这个包。
- Webhook、设备 Key 和签名密钥以明文保存在 `/etc/uz801d/config.json`（0600），备份和导出配置时请注意。

### Wi-Fi 热点

- UZ801 的板载无线网卡只支持 **2.4 GHz**。页面固定显示 2.4 GHz，信道范围 `0–14`
  （`0` 表示自动）。旧配置中的 5 GHz 设置会在加载时自动改回 2.4 GHz / 自动。
- 可设置 SSID、密码、加密方式、隐藏网络、最大客户端数和国家代码。
- 后端可选 `nmcli`、`hostapd` 或自动检测。
- **停止热点**会先关闭 NetworkManager 热点连接并禁用其自动重连，再停止 hostapd/dnsmasq
  和桥接单元。只有确认 AP 真正停止后才提示成功，仍在运行时会返回具体错误。

### WiFi 共享（无线转 USB）

适用场景：**没有启用蜂窝数据时**，设备连接一个上游 WiFi（例如家里的路由器），通过 USB
口给电脑、路由器等下游设备提供网络。`wlan0` 只作为 STA，**不需要 AP+STA 并发**，适合
高通 8916 这类单无线接口设备。

实现方式：

1. `wlan0` 通过 NetworkManager 连接上游 WiFi，获取 IP 与默认路由；
2. `usb0` 保持 `192.168.68.1/16`，系统 dnsmasq 继续提供 DHCP/DNS；
3. 开启 IPv4 转发；
4. nftables 的 `inet uz801d_bridge` 表对 `usb0 <-> wlan0` 做转发放行和 `masquerade`。

页面支持：

- 上游 SSID、密码、加密方式、附近 WiFi 扫描
- 下游接口（默认 `usb0`）与上游路由优先级（metric）
- **网页认证检测**：探测上游是否被 captive portal 拦截，并给出认证页面地址；
  在连接本设备的电脑浏览器里打开该地址完成登录即可
- 未启用蜂窝数据时自动启用共享
- **流量优先**：一旦开启蜂窝流量，共享立即停用并恢复本机热点；数据开启期间不能手动启用共享
- **上游断线保持运行**：上游 WiFi 失去地址时不会自动停用，而是持续尝试重连
- 启动失败自动回滚并恢复原热点，避免设备失联
- 重启后自动恢复上次已启用的共享（若蜂窝数据已启用则跳过）

> 启用 WiFi 共享会关闭本机热点，`wlan0` 改为客户端模式。此时管理地址为
> `http://192.168.68.1`（USB）；停用共享后会恢复热点。
>
> **热点和 WiFi 共享互斥**：在热点页点击“启用热点”会先退出 WiFi 共享；
> 在共享页点击“启用共享”会先关闭热点。两边页面都只显示当前可用的那个操作。
>
> 本机 wcn36xx 无线驱动不支持 AP+STA 并发，因此不再创建 `uap0`。如果需要“上游 WiFi +
> 本机热点”同时工作，请使用外接 USB 无线网卡，或插 SIM 卡走 4G + 热点方案。

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
- **LED 控制**：可以一键完全关闭所有指示灯，也可以设置夜间自动熄灭时间段
  （例如 22:00–07:00，支持跨午夜，使用设备本地时区）。eMMC 活动灯可以通过
  `install.sh --emmc-led-off` 启用独立的安全开机服务；该服务带有重启保护，
  如果上一次尝试未完成会自动禁用，避免重启循环。
- 修改密码、会话时长、重启/关机/恢复出厂。

> 恢复出厂设置默认**保留管理密码**，避免设备变成无凭证状态。

---

## 重要注意事项

1. **必须先有 OpenStick Debian**
   本项目不是刷机包，不负责解锁、刷写引导或安装 Debian。

2. **UZ801 只支持 2.4 GHz Wi-Fi**
   页面不会提供 5 GHz 选项。旧配置里的 5 GHz 频段和 5G 信道会自动改回 2.4 GHz / 自动。

3. **单网卡 AP+STA 取决于无线驱动**
   高通 8916 常见的 WCN3620/WCN36xx 在本项目使用的内核/固件下只能有一个活动接口，
   无法同时做 STA 和 AP，创建 `uap0` 后 `ip link set up` 会返回 `Device or resource busy`。
   因此 WiFi 共享改为 **无线转 USB**：`wlan0` 只做 STA，`usb0` 作为下游 LAN。
   如果确实需要“上游 WiFi + 本机热点”同时工作，请使用外接 USB 无线网卡，或插 SIM 卡
   走 4G + 热点方案。

4. **WiFi 共享与蜂窝数据互斥**
   蜂窝数据开启时不能使用 WiFi 转 USB 共享；SIM 卡插入/拔出本身不会改变共享状态。

5. **默认端口 80，未启用 TLS**
   后台只适合在局域网使用。需要公网访问时，请通过 frp、VPN 或带 TLS 的反向代理，
   不要把 80 端口直接暴露到公网。

6. **程序需要 root 权限**
   它会修改网络配置、hostapd/dnsmasq 配置、nftables 规则和 systemd 服务。请只从可信来源
   获取二进制，并在升级前备份配置。

7. **不要把辅助服务设为开机自启**
   `uz801d-ap.service` 和 `uz801d-dhcp.service` 只用于旧的 AP-STA 中继，WiFi 转 USB
   共享不需要它们。手动 enable 可能与普通热点冲突。

8. **修改网络配置可能导致失联**
   修改局域网 IP 或启用 WiFi 共享时，管理页面可能短暂断开。请提前准备 SSH、USB 网络
   或串口作为恢复通道。

9. **Windows 版仅用于预览**
   Windows 构建可以查看界面、调用部分 API，但蜂窝、热点、WiFi 共享等依赖 Linux 组件，
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
      "mode": "usb",                  // usb | ap_sta
      "downstream_interface": "usb0",
      "auto": true,
      "upstream_ssid": "",
      "upstream_password": "",
      "upstream_security": "wpa2",
      "route_metric": 50
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
  "sms": {
    "forward_enabled": false,
    "poll_seconds": 60,
    "providers": [
      {
        "name": "我的手机",
        "type": "bark",              // bark | dingtalk | feishu
        "enabled": true,
        "url": "https://api.day.app",
        "token": "Bark 设备 Key",
        "secret": "",                // 钉钉/飞书签名密钥（可选）
        "group": "",                 // Bark 分组（可选）
        "sound": ""                  // Bark 铃声（可选）
      }
    ]
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

**WiFi 共享提示驱动不支持？**

旧版 AP-STA 中继需要驱动声明 managed + AP 并发。高通 8916 的 wcn36xx 在本项目使用的
内核/固件下只能有一个活动接口，创建 `uap0` 后会返回 `Device or resource busy`。当前版本
已改为 **WiFi 转 USB 共享**，不再需要 AP+STA。如果页面仍显示旧版“AP 网卡/信道/强制启用”
字段，说明设备上还是旧二进制，请重新部署并强制刷新浏览器。

**启用 WiFi 共享后管理页面打不开？**

启用共享会关闭本机热点，`wlan0` 改为 STA。此时请通过 USB 地址访问：

```text
http://192.168.68.1
```

若上游连接失败，程序会自动恢复原热点。若设备彻底失联，可通过 USB 网络或串口登录，
停止共享：

```sh
systemctl stop uz801d
# 编辑 /etc/uz801d/config.json，把 wifi.bridge.enabled 改为 false
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
| GET/POST/DELETE | `/api/sms`、`/api/sms/{id}`、`/api/sms/send`、`/api/sms/read` | 短信（`DELETE /api/sms` 删除全部）；转发配置见 `GET/POST /api/sms/forward`、`POST /api/sms/forward/test` |
| GET/POST | `/api/wifi`、`/api/wifi/scan`、`/api/wifi/restart`、`/api/wifi/stop` | 热点 |
| GET/POST | `/api/bridge`、`/api/bridge/enable`、`/api/bridge/disable`、`/api/bridge/scan`、`/api/bridge/portal` | WiFi 转 USB 共享；`portal` 检测网页认证页面 |
| GET/POST | `/api/clients`、`/api/clients/block`、`/api/clients/unblock`、`/api/clients/rename`、`/api/clients/forget` | 客户端 |
| GET/POST | `/api/lan` | 局域网 / DHCP |
| GET/POST | `/api/traffic`、`/api/traffic/reset`、`/api/traffic/lifetime` | 流量 |
| GET/POST | `/api/system`、`/api/system/logs`、`/api/system/frpc-log`、`/api/system/leds`、`/api/system/led`、`/api/system/led-control`、`/api/system/password`、`/api/system/language`、`/api/system/admin-user`、`/api/system/session-hours`、`/api/system/reboot`、`/api/system/poweroff`、`/api/system/factory-reset`、`/api/system/ping`、`/api/system/dns` | 系统 |
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

- 设备的具体型号和系统版本（`uname -a`、`cat /etc/os-release`）
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
