#!/bin/sh
# MSM8916 Manager installer for OpenStick Debian.
# Usage: ./install.sh [/path/to/uz801d] [--install-deps] [--no-start]
set -eu

BIN_SRC=""
INSTALL_DEPS=0
START=1
ENABLE_EMMC_LED_OFF=0
FIX_FIRMWARE=0
for arg in "$@"; do
  case "$arg" in
    --install-deps) INSTALL_DEPS=1 ;;
    --no-start) START=0 ;;
    --emmc-led-off) ENABLE_EMMC_LED_OFF=1 ;;
    --fix-firmware) FIX_FIRMWARE=1 ;;
    -*) echo "未知参数: $arg" >&2; exit 1 ;;
    *) BIN_SRC="$arg" ;;
  esac
done

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

if [ "$(id -u)" -ne 0 ]; then
  echo "请使用 root 运行: sudo sh install.sh" >&2
  exit 1
fi

if [ -z "$BIN_SRC" ]; then
  for cand in ./uz801d ./dist/uz801d ./uz801d-linux-arm64 ./uz801d-linux-armv7; do
    if [ -f "$cand" ]; then BIN_SRC="$cand"; break; fi
  done
fi
if [ -z "$BIN_SRC" ] || [ ! -f "$BIN_SRC" ]; then
  echo "找不到 uz801d 可执行文件，请把对应架构的二进制放在当前目录或作为参数传入。" >&2
  exit 1
fi

if [ "$INSTALL_DEPS" -eq 1 ] && command -v apt-get >/dev/null 2>&1; then
  echo "==> 安装依赖软件包"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y --no-install-recommends \
    network-manager modemmanager hostapd dnsmasq iw nftables iproute2 systemd rfkill \
    ca-certificates
fi

echo "==> 检查依赖"
missing=""
for pkg in nmcli mmcli hostapd dnsmasq iw nft ip systemctl; do
  if ! command -v "$pkg" >/dev/null 2>&1; then
    missing="$missing $pkg"
  fi
done

# https 通知（Bark / 钉钉 / 飞书）都靠系统 CA 证书做 TLS 校验。精简 rootfs 里常常
# 没有 ca-certificates，Go 会报 "x509: certificate signed by unknown authority"。
if [ ! -f /etc/ssl/certs/ca-certificates.crt ]; then
  missing="$missing ca-certificates"
fi
if [ -n "$missing" ]; then
  echo "    缺少依赖:$missing"
  echo "    可重新运行: sudo sh install.sh $BIN_SRC --install-deps"
  echo "    缺少 nmcli/hostapd/dnsmasq/iw/nft 时对应功能不可用。"
  echo "    缺少 ca-certificates 时所有 https 通知（Bark/钉钉/飞书）都会报 x509 错误。"
fi

# ---------------------------------------------------------------------------
# 基带固件检查
#
# 部分 OpenStick 镜像自带的是 M8936 的 MPSS（MPSS.DPM.2.0.2.c1-00178-M8936...）。
# 装到 M8916 的 UFI 板子上时，SIM 能读（AT+CPIN? = READY），但射频永远起不来：
#   qmicli --dms-get-operating-mode  -> offline
#   qmicli --dms-set-operating-mode=online -> QMI error 52 DeviceNotReady
#   AT+CFUN=1 -> Phone failure, AT+CSQ -> 99,99
# 正确的固件是 MPSS.DPM.1.0.c7-00193-M8916EAAAANVZM-1（可从原厂 modem 分区
# 的 FAT16 镜像里 7z 解出 image/modem.*，或在仓库 firmware/modem-m8916/ 里）。
# ---------------------------------------------------------------------------
MODEM_MDT_SHA_M8916=568515ab3b8e40ba0666315555ccd3561929797aef617349d2142115b04b6524
MBA_SHA_M8916=5d635f58206bbb47e73c763720a3b365af93d9bf179340158312909893f2bc70
WCNSS_MDT_SHA_M8916=2bc719366ea204207f6fe3645930d1511276504ecb51d4a32d013e4ed440846c

check_firmware() {
  _mdt=/lib/firmware/modem.mdt
  _mba=/lib/firmware/mba.mbn
  _wcn=/lib/firmware/wcnss.mdt
  if [ ! -f "$_mdt" ]; then
    echo "    没有 /lib/firmware/modem.mdt —— 4G 不可用"
    return 1
  fi
  _sum=$(sha256sum "$_mdt" 2>/dev/null | awk '{print $1}')
  _msum=$(sha256sum "$_mba" 2>/dev/null | awk '{print $1}')
  _wsum=$(sha256sum "$_wcn" 2>/dev/null | awk '{print $1}')
  if [ "$_sum" = "$MODEM_MDT_SHA_M8916" ] && [ "$_msum" = "$MBA_SHA_M8916" ] && [ "$_wsum" = "$WCNSS_MDT_SHA_M8916" ]; then
    echo "    基带固件: M8916（modem + mba + wcnss 匹配）"
    return 0
  fi
  _ver=$(grep -aho 'MPSS[A-Za-z0-9._-]*' /lib/firmware/modem.b* 2>/dev/null | head -n1)
  [ "$_sum" = "$MODEM_MDT_SHA_M8916" ] || \
    echo "    modem.mdt 不匹配: ${_ver:-未知版本}"
  [ "$_msum" = "$MBA_SHA_M8916" ] || \
    echo "    mba.mbn 不匹配 —— MBA 和 MPSS 版本必须成对，否则能注册但数据通道起不来"
  [ "$_wsum" = "$WCNSS_MDT_SHA_M8916" ] || \
    echo "    wcnss.mdt 不匹配 —— 无线固件也来自同一套镜像"
  echo "    这块板需要 MPSS.DPM.1.0.c7-00193-M8916EAAAANVZM-1 那套（modem + mba + wcnss）"
  return 1
}

echo "==> 检查基带固件"
if ! check_firmware && [ "$FIX_FIRMWARE" -eq 1 ]; then
  if [ -d "$SCRIPT_DIR/firmware/modem-m8916" ]; then
    echo "==> 替换为 M8916 固件（旧固件备份到 /root/fw-backup-*）"
    _bk="/root/fw-backup-$(date +%Y%m%d-%H%M%S)"
    mkdir -p "$_bk"
    cp -a /lib/firmware/modem.* /lib/firmware/mba.mbn /lib/firmware/wcnss.* "$_bk"/ 2>/dev/null || true
    rm -f /lib/firmware/modem.b* /lib/firmware/modem.mdt
    rm -f /lib/firmware/wcnss.b* /lib/firmware/wcnss.mdt
    cp "$SCRIPT_DIR"/firmware/modem-m8916/modem.* /lib/firmware/
    # MBA 必须和 MPSS 同版本：只换 modem.* 会出现"能注册、拿不到 IP"的怪状态
    cp "$SCRIPT_DIR"/firmware/modem-m8916/mba.mbn /lib/firmware/mba.mbn
    cp "$SCRIPT_DIR"/firmware/modem-m8916/wcnss.* /lib/firmware/
    check_firmware || true
    echo "    已替换，需要重启后生效: reboot"
  else
    echo "    找不到 $SCRIPT_DIR/firmware/modem-m8916/，无法自动修复" >&2
    echo "    固件不入库（约 40 MB）。取一份放到 firmware/modem-m8916/ 即可：" >&2
    echo "      A) 从原厂 modem 分区解：7z x modem.bin -o firmware-stock \"image/modem.*\"" >&2
    echo "      B) 从已有的刷机包/备份里拷 firmware/modem-m8916/modem.*" >&2
  fi
elif [ "$FIX_FIRMWARE" -eq 0 ]; then
  check_firmware >/dev/null 2>&1 || echo "    提示: 重新运行并加 --fix-firmware 可自动换成正确固件"
fi

echo "==> 安装 uz801d"
install -m 0755 "$BIN_SRC" /usr/local/bin/uz801d
mkdir -p /etc/uz801d /var/lib/uz801d /var/lib/misc
chmod 0755 /etc/uz801d /var/lib/uz801d

CONFIG=/etc/uz801d/config.json
if [ ! -f "$CONFIG" ]; then
  echo "==> 生成默认配置与随机管理密码"
  /usr/local/bin/uz801d -config "$CONFIG" -state /var/lib/uz801d/state.json -init
  echo
  echo "    初始账号信息保存在 /etc/uz801d/initial-password"
  cat /etc/uz801d/initial-password
else
  echo "==> 保留已有配置 $CONFIG"
  # 老版本默认监听 :8080；升级到默认 80 端口时迁移一次，保留备份。
  _listen=$(sed -n 's/.*"listen"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$CONFIG" | head -n1)
  if [ "$_listen" = ":8080" ]; then
    cp -p "$CONFIG" "$CONFIG.pre-80.bak"
    sed -i 's/"listen"[[:space:]]*:[[:space:]]*":8080"/"listen": ":80"/' "$CONFIG"
    _listen=":80"
    echo "    监听地址已从 :8080 迁移为 :80（原配置备份: $CONFIG.pre-80.bak）"
  fi
  if [ "$_listen" != ":80" ]; then
    echo "    当前监听地址: ${_listen:-未设置（默认 :80）}"
    echo "    如需改为 80 端口，请编辑 $CONFIG 的 listen 字段后重启 uz801d"
  fi
fi

echo "==> 安装 systemd 单元"
UNIT_SRC="$SCRIPT_DIR"
if [ ! -f "$UNIT_SRC/uz801d.service" ]; then
  UNIT_SRC="$SCRIPT_DIR/deploy"
fi
for unit in uz801d.service uz801d-ap.service uz801d-dhcp.service uz801d-emmc-led.service; do
  if [ -f "$UNIT_SRC/$unit" ]; then
    install -m 0644 "$UNIT_SRC/$unit" "/etc/systemd/system/$unit"
  else
    echo "    警告: 找不到 $unit，跳过" >&2
  fi
done
if [ -f "$UNIT_SRC/emmc-led-off.sh" ]; then
  install -m 0755 "$UNIT_SRC/emmc-led-off.sh" /usr/local/bin/uz801d-emmc-led-off
fi

systemctl daemon-reload
systemctl enable uz801d.service
if [ "$ENABLE_EMMC_LED_OFF" -eq 1 ]; then
  echo "==> 启用 eMMC 活动灯关闭服务（有触发重启的风险）"
  systemctl enable uz801d-emmc-led.service
fi

if [ "$START" -eq 1 ]; then
  systemctl restart uz801d.service
  sleep 1
  systemctl --no-pager --lines=0 status uz801d.service || true
fi

IP=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") print $(i+1)}' | head -n1)
[ -n "$IP" ] || IP=$(hostname -I 2>/dev/null | awk '{print $1}')
[ -n "$IP" ] || IP="192.168.68.1"

cat <<EOF

安装完成。
  管理地址: http://$IP
  配置文件: $CONFIG
  状态文件: /var/lib/uz801d/state.json
  服务管理: systemctl status uz801d
  查看日志: journalctl -u uz801d -f
  修改密码: uz801d -config $CONFIG -reset-password

WiFi 桥接说明:
  - 桥接需要 NetworkManager(nmcli)、iw、hostapd、dnsmasq、nftables。
  - 单网卡 AP-STA 取决于无线驱动是否支持并发；不支持时请使用外接 USB 无线网卡并
    在“WiFi 桥接”页面选择该网卡作为 AP。
EOF
