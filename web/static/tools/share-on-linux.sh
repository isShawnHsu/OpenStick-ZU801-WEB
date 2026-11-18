#!/bin/sh
# 在 Linux 电脑上把网络共享给设备的 USB 网卡（USB -> WiFi 模式用）
# 用法：sudo sh share-on-linux.sh
set -e

[ "$(id -u)" = "0" ] || { echo "请用 sudo 运行"; exit 1; }

# 1) 找设备的 USB 网卡
USB=""
for d in /sys/class/net/*; do
    n=$(basename "$d")
    [ "$n" = "lo" ] && continue
    drv=$(basename "$(readlink -f "$d/device/driver" 2>/dev/null)" 2>/dev/null)
    case "$drv" in
        cdc_ether|rndis_host|cdc_ncm|usbnet) USB="$n"; break ;;
    esac
done
[ -n "$USB" ] || { echo "找不到 USB 网卡（cdc_ether / rndis_host）"; exit 1; }

# 2) 找上网用的网卡
WAN=$(ip route show default | awk '/^default/ {print $5; exit}')
[ -n "$WAN" ] || WAN=$(ip route | awk '/^default/ {print $5; exit}')
[ -n "$WAN" ] || { echo "找不到默认路由（电脑没联网？）"; exit 1; }
[ "$WAN" != "$USB" ] || { echo "USB 网卡就是默认路由，请先断开再运行"; exit 1; }

echo "USB 网卡: $USB"
echo "上网网卡: $WAN"

# 3) 给 USB 网卡配地址
ip addr flush dev "$USB" scope global || true
ip addr add 192.168.137.1/24 dev "$USB"
ip link set "$USB" up

# 4) 转发 + NAT
sysctl -w net.ipv4.ip_forward=1 >/dev/null
iptables -t nat -C POSTROUTING -o "$WAN" -j MASQUERADE 2>/dev/null ||
    iptables -t nat -A POSTROUTING -o "$WAN" -j MASQUERADE
iptables -C FORWARD -i "$USB" -o "$WAN" -j ACCEPT 2>/dev/null ||
    iptables -I FORWARD -i "$USB" -o "$WAN" -j ACCEPT
iptables -C FORWARD -i "$WAN" -o "$USB" -m state --state RELATED,ESTABLISHED -j ACCEPT 2>/dev/null ||
    iptables -I FORWARD -i "$WAN" -o "$USB" -m state --state RELATED,ESTABLISHED -j ACCEPT

# 5) DHCP/DNS
if command -v dnsmasq >/dev/null 2>&1; then
    cat > /etc/dnsmasq.d/msm8916-share.conf <<EOF
interface=$USB
bind-interfaces
dhcp-range=192.168.137.50,192.168.137.150,12h
dhcp-option=3,192.168.137.1
dhcp-option=6,223.5.5.5,119.29.29.29
EOF
    systemctl restart dnsmasq 2>/dev/null || service dnsmasq restart 2>/dev/null || dnsmasq --conf-file=/etc/dnsmasq.d/msm8916-share.conf
else
    echo "未安装 dnsmasq，设备可能拿不到 IP：apt install -y dnsmasq"
fi

echo ""
echo "完成。USB 网卡现在是 192.168.137.1"
echo "到设备网页（手机连热点 http://192.168.69.1/）选 USB → WiFi 并启用。"
echo "关闭共享：sudo sh share-off-linux.sh"
