#!/bin/sh
# Uninstall UZ801 Manager. Use --purge to also delete configuration and state.
set -eu

PURGE=0
for arg in "$@"; do
  case "$arg" in
    --purge) PURGE=1 ;;
    *) echo "未知参数: $arg" >&2; exit 1 ;;
  esac
done

if [ "$(id -u)" -ne 0 ]; then
  echo "请使用 root 运行: sudo sh uninstall.sh" >&2
  exit 1
fi

echo "==> 停止并禁用服务"
systemctl stop uz801d-dhcp.service 2>/dev/null || true
systemctl stop uz801d-ap.service 2>/dev/null || true
systemctl stop uz801d.service 2>/dev/null || true
systemctl disable uz801d.service 2>/dev/null || true

echo "==> 删除 systemd 单元"
rm -f /etc/systemd/system/uz801d.service \
      /etc/systemd/system/uz801d-ap.service \
      /etc/systemd/system/uz801d-dhcp.service
systemctl daemon-reload

echo "==> 删除程序"
rm -f /usr/local/bin/uz801d
rm -f /etc/hostapd/uz801d-ap.conf /etc/dnsmasq.d/uz801d-ap.conf
rm -f /run/uz801d-ap.pid /run/uz801d-dhcp.pid
nft delete table inet uz801d_bridge 2>/dev/null || true
nft delete table inet uz801d_filter 2>/dev/null || true

if [ "$PURGE" -eq 1 ]; then
  echo "==> 删除配置与状态"
  rm -rf /etc/uz801d /var/lib/uz801d
else
  echo "==> 保留 /etc/uz801d 与 /var/lib/uz801d（使用 --purge 可一并删除）"
fi

echo "卸载完成。"
