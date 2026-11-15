#!/bin/sh
# Uninstall MSM8916 Manager. Use --purge to also delete configuration and state.
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
systemctl stop msm8916d-dhcp.service 2>/dev/null || true
systemctl stop msm8916d-ap.service 2>/dev/null || true
systemctl stop msm8916d.service 2>/dev/null || true
systemctl disable msm8916d.service 2>/dev/null || true

echo "==> 删除 systemd 单元"
rm -f /etc/systemd/system/msm8916d.service \
      /etc/systemd/system/msm8916d-ap.service \
      /etc/systemd/system/msm8916d-dhcp.service
systemctl daemon-reload

echo "==> 删除程序"
rm -f /usr/local/bin/msm8916d
rm -f /etc/hostapd/msm8916d-ap.conf /etc/dnsmasq.d/msm8916d-ap.conf
rm -f /run/msm8916d-ap.pid /run/msm8916d-dhcp.pid
nft delete table inet msm8916d_bridge 2>/dev/null || true
nft delete table inet msm8916d_filter 2>/dev/null || true

if [ "$PURGE" -eq 1 ]; then
  echo "==> 删除配置与状态"
  rm -rf /etc/msm8916d /var/lib/msm8916d
else
  echo "==> 保留 /etc/msm8916d 与 /var/lib/msm8916d（使用 --purge 可一并删除）"
fi

echo "卸载完成。"
