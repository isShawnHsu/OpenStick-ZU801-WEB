#!/bin/sh
# 关闭 Linux 电脑上的共享（与 share-on-linux.sh 对应）
set -e
[ "$(id -u)" = "0" ] || { echo "请用 sudo 运行"; exit 1; }

USB=""
for d in /sys/class/net/*; do
    n=$(basename "$d")
    [ "$n" = "lo" ] && continue
    drv=$(basename "$(readlink -f "$d/device/driver" 2>/dev/null)" 2>/dev/null)
    case "$drv" in
        cdc_ether|rndis_host|cdc_ncm|usbnet) USB="$n"; break ;;
    esac
done

[ -n "$USB" ] && ip addr flush dev "$USB" scope global || true

rm -f /etc/dnsmasq.d/msm8916-share.conf
systemctl restart dnsmasq 2>/dev/null || true

if [ -n "$USB" ]; then
    for WAN in $(ip route show default | awk '/^default/ {print $5}'); do
        iptables -t nat -D POSTROUTING -o "$WAN" -j MASQUERADE 2>/dev/null || true
        iptables -D FORWARD -i "$USB" -o "$WAN" -j ACCEPT 2>/dev/null || true
        iptables -D FORWARD -i "$WAN" -o "$USB" -m state --state RELATED,ESTABLISHED -j ACCEPT 2>/dev/null || true
    done
fi

echo "已关闭共享。"
