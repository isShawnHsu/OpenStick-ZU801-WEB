// Package netutil contains small helpers for IPv4/LAN validation.
package netutil

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// ParseIPv4 parses a dotted-quad IPv4 address, rejecting IPv6 and zones.
func ParseIPv4(s string) (net.IP, error) {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil {
		return nil, fmt.Errorf("无效的 IPv4 地址: %s", s)
	}
	if ip.To4() == nil {
		return nil, fmt.Errorf("只支持 IPv4 地址: %s", s)
	}
	return ip.To4(), nil
}

// MaskSize returns the prefix length of a contiguous IPv4 netmask.
func MaskSize(mask string) (int, error) {
	ip, err := ParseIPv4(mask)
	if err != nil {
		return 0, fmt.Errorf("无效的子网掩码: %s", mask)
	}
	m := net.IPMask(ip)
	ones, bits := m.Size()
	if bits != 32 || ones == 0 {
		return 0, fmt.Errorf("子网掩码必须连续: %s", mask)
	}
	return ones, nil
}

// Network returns the network address for ip/mask.
func Network(ip net.IP, prefix int) net.IP {
	return ip.Mask(net.CIDRMask(prefix, 32))
}

// Broadcast returns the broadcast address for ip/prefix.
func Broadcast(ip net.IP, prefix int) net.IP {
	ip = ip.To4()
	mask := net.CIDRMask(prefix, 32)
	b := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		b[i] = ip[i] | ^mask[i]
	}
	return b
}

// SameSubnet reports whether a and b are in the same IPv4 subnet.
func SameSubnet(a, b net.IP, prefix int) bool {
	return Network(a, prefix).Equal(Network(b, prefix))
}

// InRange reports start <= ip <= end, comparing IPv4 numerically.
func InRange(ip, start, end net.IP) bool {
	return IPToUint32(ip) >= IPToUint32(start) && IPToUint32(ip) <= IPToUint32(end)
}

// IPToUint32 converts a 4-byte IPv4 address to uint32.
func IPToUint32(ip net.IP) uint32 {
	ip = ip.To4()
	if ip == nil {
		return 0
	}
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

// Uint32ToIP converts uint32 to a 4-byte IPv4 address.
func Uint32ToIP(v uint32) net.IP {
	return net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v)).To4()
}

// ValidateLAN checks a router IP, netmask, DHCP range and DNS servers.
func ValidateLAN(ipStr, maskStr, startStr, endStr string, dns []string) error {
	ip, err := ParseIPv4(ipStr)
	if err != nil {
		return err
	}
	prefix, err := MaskSize(maskStr)
	if err != nil {
		return err
	}
	if prefix < 8 || prefix > 30 {
		return fmt.Errorf("子网掩码长度必须在 /8 到 /30 之间")
	}
	network := Network(ip, prefix)
	broadcast := Broadcast(ip, prefix)
	if ip.Equal(network) || ip.Equal(broadcast) {
		return fmt.Errorf("路由器 IP 不能是网络地址或广播地址")
	}

	start, err := ParseIPv4(startStr)
	if err != nil {
		return fmt.Errorf("DHCP 起始地址: %w", err)
	}
	end, err := ParseIPv4(endStr)
	if err != nil {
		return fmt.Errorf("DHCP 结束地址: %w", err)
	}
	if !SameSubnet(ip, start, prefix) {
		return fmt.Errorf("DHCP 起始地址不在路由器子网内")
	}
	if !SameSubnet(ip, end, prefix) {
		return fmt.Errorf("DHCP 结束地址不在路由器子网内")
	}
	if IPToUint32(start) > IPToUint32(end) {
		return fmt.Errorf("DHCP 起始地址不能大于结束地址")
	}
	if start.Equal(network) || end.Equal(broadcast) {
		return fmt.Errorf("DHCP 地址不能是网络地址或广播地址")
	}
	if ip.Equal(start) || ip.Equal(end) || (InRange(ip, start, end)) {
		return fmt.Errorf("DHCP 地址范围不能包含路由器 IP")
	}

	if len(dns) == 0 {
		return fmt.Errorf("至少需要一个 DNS 服务器")
	}
	if len(dns) > 4 {
		return fmt.Errorf("DNS 服务器最多 4 个")
	}
	for _, d := range dns {
		if _, err := ParseIPv4(d); err != nil {
			return fmt.Errorf("DNS 服务器: %w", err)
		}
	}
	return nil
}

// PrefixFromMask returns the CIDR prefix length as a string.
func PrefixFromMask(mask string) (string, error) {
	n, err := MaskSize(mask)
	if err != nil {
		return "", err
	}
	return strconv.Itoa(n), nil
}

// NormalizeMAC validates and canonicalises a MAC address to aa:bb:cc:dd:ee:ff.
func NormalizeMAC(s string) (string, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.ReplaceAll(s, "-", ":")
	hw, err := net.ParseMAC(s)
	if err != nil || len(hw) != 6 {
		return "", fmt.Errorf("无效的 MAC 地址: %s", s)
	}
	parts := make([]string, 6)
	for i, b := range hw {
		parts[i] = fmt.Sprintf("%02x", b)
	}
	return strings.Join(parts, ":"), nil
}

// IsValidHost validates a hostname or IP for diagnostics commands.
func IsValidHost(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 253 {
		return false
	}
	if net.ParseIP(s) != nil {
		return true
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return !strings.HasPrefix(s, "-") && !strings.HasSuffix(s, "-")
}
