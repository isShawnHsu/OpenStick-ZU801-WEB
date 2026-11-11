#!/bin/sh
# UZ801 Manager installer for OpenStick Debian.
# Usage: ./install.sh [/path/to/uz801d] [--install-deps] [--no-start]
set -eu

BIN_SRC=""
INSTALL_DEPS=0
START=1
for arg in "$@"; do
  case "$arg" in
    --install-deps) INSTALL_DEPS=1 ;;
    --no-start) START=0 ;;
    -*) echo "未知参数: $arg" >&2; exit 1 ;;
    *) BIN_SRC="$arg" ;;
  esac
done

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
    network-manager modemmanager hostapd dnsmasq iw nftables iproute2 systemd rfkill
fi

echo "==> 检查依赖"
missing=""
for pkg in nmcli mmcli hostapd dnsmasq iw nft ip systemctl; do
  if ! command -v "$pkg" >/dev/null 2>&1; then
    missing="$missing $pkg"
  fi
done
if [ -n "$missing" ]; then
  echo "    缺少命令:$missing"
  echo "    可重新运行: sudo sh install.sh $BIN_SRC --install-deps"
  echo "    缺少 nmcli/hostapd/dnsmasq/iw/nft 时对应功能不可用。"
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
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
UNIT_SRC="$SCRIPT_DIR"
if [ ! -f "$UNIT_SRC/uz801d.service" ]; then
  UNIT_SRC="$SCRIPT_DIR/deploy"
fi
for unit in uz801d.service uz801d-ap.service uz801d-dhcp.service; do
  if [ -f "$UNIT_SRC/$unit" ]; then
    install -m 0644 "$UNIT_SRC/$unit" "/etc/systemd/system/$unit"
  else
    echo "    警告: 找不到 $unit，跳过" >&2
  fi
done

systemctl daemon-reload
systemctl enable uz801d.service

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
