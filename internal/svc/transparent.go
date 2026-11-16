package svc

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// transparentMark is put on v2ray's own outbound sockets (sockopt.mark) so the
// redirect chains never capture the tunnel traffic itself.
const transparentMark = 255

// This kernel (5.15 handsomekernel) has CONFIG_NFT_REDIR / CONFIG_NFT_TPROXY
// disabled, so the redirect is done with iptables (legacy, xt_REDIRECT).
const (
	tproxyPreChain  = "MSM8916D_PRE"  // nat PREROUTING: USB/hotspot clients
	tproxyOutChain  = "MSM8916D_OUT"  // nat OUTPUT: the device itself
	tproxyInChain   = "MSM8916D_PIN"  // filter INPUT: dokodemo port protection
	tproxyQuicChain = "MSM8916D_QUIC" // filter FORWARD: reject QUIC
	tproxyQOutChain = "MSM8916D_QOUT" // filter OUTPUT: reject QUIC
	tproxyV6Chain   = "MSM8916D_V6"   // filter FORWARD/OUTPUT: block IPv6
)

// tproxyBypass is the destination set that never goes through the proxy, so
// LAN/USB management, SSH and the web UI keep working.
var tproxyBypass = []string{
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8",
	"169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/4", "240.0.0.0/4",
}

// ipt runs one iptables command.
func (m *Manager) ipt(ctx context.Context, table string, args ...string) error {
	full := append([]string{"-t", table}, args...)
	_, err := m.Run(ctx, 10*time.Second, "iptables", full...)
	return err
}

// iptDropChain flushes and deletes a chain, detaching it from its parents.
func (m *Manager) iptDropChain(ctx context.Context, table, chain string, parents ...string) {
	for _, p := range parents {
		for i := 0; i < 3; i++ {
			if err := m.ipt(ctx, table, "-D", p, "-j", chain); err != nil {
				break
			}
		}
	}
	_ = m.ipt(ctx, table, "-F", chain)
	_ = m.ipt(ctx, table, "-X", chain)
}

// ipt6 runs one ip6tables command; it is a no-op when ip6tables is absent.
func (m *Manager) ipt6(ctx context.Context, table string, args ...string) error {
	if !m.Available("ip6tables") {
		return fmt.Errorf("未安装 ip6tables")
	}
	full := append([]string{"-t", table}, args...)
	_, err := m.Run(ctx, 10*time.Second, "ip6tables", full...)
	return err
}

// applyIPv6Block keeps IPv6 traffic from bypassing the IPv4-only tunnel. The
// proxy node has no usable IPv6 exit here, so v6 is rejected and clients fall
// back to IPv4, which is redirected into v2ray.
func (m *Manager) applyIPv6Block(ctx context.Context) error {
	if !m.Available("ip6tables") {
		return nil
	}
	m.dropIPv6Block(ctx)
	if err := m.ipt6(ctx, "filter", "-N", tproxyV6Chain); err != nil {
		return fmt.Errorf("创建 IPv6 链失败: %w", err)
	}
	for _, net := range []string{"::1/128", "fc00::/7", "fe80::/10", "ff00::/8"} {
		m.ipt6(ctx, "filter", "-A", tproxyV6Chain, "-d", net, "-j", "RETURN")
	}
	m.ipt6(ctx, "filter", "-A", tproxyV6Chain, "-m", "mark", "--mark", strconv.Itoa(transparentMark), "-j", "RETURN")
	m.ipt6(ctx, "filter", "-A", tproxyV6Chain, "-p", "tcp", "-j", "REJECT", "--reject-with", "tcp-reset")
	m.ipt6(ctx, "filter", "-A", tproxyV6Chain, "-p", "udp", "-j", "REJECT", "--reject-with", "icmp6-port-unreachable")
	if err := m.ipt6(ctx, "filter", "-I", "FORWARD", "1", "-j", tproxyV6Chain); err != nil {
		m.dropIPv6Block(ctx)
		return fmt.Errorf("挂载 IPv6 转发规则失败: %w", err)
	}
	if err := m.ipt6(ctx, "filter", "-I", "OUTPUT", "1", "-j", tproxyV6Chain); err != nil {
		m.dropIPv6Block(ctx)
		return fmt.Errorf("挂载 IPv6 本机规则失败: %w", err)
	}
	return nil
}

func (m *Manager) dropIPv6Block(ctx context.Context) {
	if !m.Available("ip6tables") {
		return
	}
	for _, p := range []string{"FORWARD", "OUTPUT"} {
		for i := 0; i < 3; i++ {
			if err := m.ipt6(ctx, "filter", "-D", p, "-j", tproxyV6Chain); err != nil {
				break
			}
		}
	}
	_ = m.ipt6(ctx, "filter", "-F", tproxyV6Chain)
	_ = m.ipt6(ctx, "filter", "-X", tproxyV6Chain)
}

// applyTransparentProxy installs (or removes) the redirect rules. It runs after
// every VPN start/restart and on config changes, so the firewall always matches
// the "transparent" switch and the running unit.
func (m *Manager) applyTransparentProxy(ctx context.Context) error {
	cfg := m.Config().VPN
	if !cfg.Transparent || !m.unitActive(ctx, cfg.ServiceName) {
		m.removeTransparentProxy(ctx)
		return nil
	}
	if !m.Available("iptables") {
		return fmt.Errorf("未安装 iptables，无法启用透明代理")
	}
	port := cfg.TransparentPort
	if port <= 0 {
		port = 12345
	}
	target := strconv.Itoa(port)
	proxyPorts := fmt.Sprintf("%d,%d", cfg.SocksPort, cfg.HTTPPort)

	// Start from a clean slate so a failed attempt never leaves half a rule set.
	m.removeTransparentProxy(ctx)

	if err := m.ipt(ctx, "nat", "-N", tproxyPreChain); err != nil {
		return fmt.Errorf("创建透明代理链失败: %w", err)
	}
	if err := m.ipt(ctx, "nat", "-N", tproxyOutChain); err != nil {
		m.removeTransparentProxy(ctx)
		return fmt.Errorf("创建透明代理链失败: %w", err)
	}
	for _, net := range tproxyBypass {
		if err := m.ipt(ctx, "nat", "-A", tproxyPreChain, "-d", net, "-j", "RETURN"); err != nil {
			m.removeTransparentProxy(ctx)
			return fmt.Errorf("添加绕过网段失败: %w", err)
		}
	}
	m.ipt(ctx, "nat", "-A", tproxyPreChain, "-p", "tcp", "-m", "multiport", "--dports", proxyPorts, "-j", "RETURN")
	if err := m.ipt(ctx, "nat", "-A", tproxyPreChain, "-p", "tcp", "-j", "REDIRECT", "--to-ports", target); err != nil {
		m.removeTransparentProxy(ctx)
		return fmt.Errorf("添加重定向规则失败: %w", err)
	}
	if err := m.ipt(ctx, "nat", "-I", "PREROUTING", "1", "-j", tproxyPreChain); err != nil {
		m.removeTransparentProxy(ctx)
		return fmt.Errorf("挂载 PREROUTING 失败: %w", err)
	}

	// The device itself: skip v2ray's own sockets (mark 255).
	m.ipt(ctx, "nat", "-A", tproxyOutChain, "-m", "mark", "--mark", strconv.Itoa(transparentMark), "-j", "RETURN")
	for _, net := range tproxyBypass {
		m.ipt(ctx, "nat", "-A", tproxyOutChain, "-d", net, "-j", "RETURN")
	}
	m.ipt(ctx, "nat", "-A", tproxyOutChain, "-p", "tcp", "-m", "multiport", "--dports", proxyPorts, "-j", "RETURN")
	if err := m.ipt(ctx, "nat", "-A", tproxyOutChain, "-p", "tcp", "-j", "REDIRECT", "--to-ports", target); err != nil {
		m.removeTransparentProxy(ctx)
		return fmt.Errorf("添加本机重定向规则失败: %w", err)
	}
	if err := m.ipt(ctx, "nat", "-I", "OUTPUT", "1", "-j", tproxyOutChain); err != nil {
		m.removeTransparentProxy(ctx)
		return fmt.Errorf("挂载 OUTPUT 失败: %w", err)
	}

	// HTTP/3 cannot be proxied by a TCP redirect, so reject QUIC and let the
	// clients fall back to TCP. Without this half of the traffic escapes.
	// The filter table has no PREROUTING chain, so client traffic is caught in
	// FORWARD and the device's own traffic in OUTPUT.
	if err := m.ipt(ctx, "filter", "-N", tproxyQuicChain); err != nil {
		m.removeTransparentProxy(ctx)
		return fmt.Errorf("创建 QUIC 链失败: %w", err)
	}
	m.ipt(ctx, "filter", "-A", tproxyQuicChain, "-p", "udp", "--dport", "443", "-j", "REJECT", "--reject-with", "icmp-port-unreachable")
	if err := m.ipt(ctx, "filter", "-I", "FORWARD", "1", "-j", tproxyQuicChain); err != nil {
		m.removeTransparentProxy(ctx)
		return fmt.Errorf("挂载 QUIC 规则失败: %w", err)
	}
	if err := m.ipt(ctx, "filter", "-N", tproxyQOutChain); err != nil {
		m.removeTransparentProxy(ctx)
		return fmt.Errorf("创建本机 QUIC 链失败: %w", err)
	}
	m.ipt(ctx, "filter", "-A", tproxyQOutChain, "-m", "mark", "--mark", strconv.Itoa(transparentMark), "-j", "RETURN")
	m.ipt(ctx, "filter", "-A", tproxyQOutChain, "-p", "udp", "--dport", "443", "-j", "REJECT", "--reject-with", "icmp-port-unreachable")
	if err := m.ipt(ctx, "filter", "-I", "OUTPUT", "1", "-j", tproxyQOutChain); err != nil {
		m.removeTransparentProxy(ctx)
		return fmt.Errorf("挂载本机 QUIC 规则失败: %w", err)
	}

	// The dokodemo port only accepts connections that were redirected.
	if err := m.ipt(ctx, "filter", "-N", tproxyInChain); err != nil {
		m.removeTransparentProxy(ctx)
		return fmt.Errorf("创建端口保护链失败: %w", err)
	}
	m.ipt(ctx, "filter", "-A", tproxyInChain, "-p", "tcp", "--dport", target, "-m", "conntrack", "--ctstate", "DNAT", "-j", "ACCEPT")
	m.ipt(ctx, "filter", "-A", tproxyInChain, "-p", "tcp", "--dport", target, "-j", "DROP")
	if err := m.ipt(ctx, "filter", "-I", "INPUT", "1", "-j", tproxyInChain); err != nil {
		m.removeTransparentProxy(ctx)
		return fmt.Errorf("挂载端口保护失败: %w", err)
	}

	// IPv6 must not slip past the tunnel; failure here is logged but does not
	// disable the working IPv4 redirect.
	if err := m.applyIPv6Block(ctx); err != nil {
		m.log.Printf("屏蔽 IPv6 失败: %v", err)
	}

	m.setTproxy(true)
	return nil
}

// removeTransparentProxy deletes the rules, used when the VPN stops.
func (m *Manager) removeTransparentProxy(ctx context.Context) {
	if !m.Available("iptables") {
		m.setTproxy(false)
		return
	}
	m.iptDropChain(ctx, "nat", tproxyPreChain, "PREROUTING")
	m.iptDropChain(ctx, "nat", tproxyOutChain, "OUTPUT")
	m.iptDropChain(ctx, "filter", tproxyQuicChain, "FORWARD")
	m.iptDropChain(ctx, "filter", tproxyQOutChain, "OUTPUT")
	m.iptDropChain(ctx, "filter", tproxyInChain, "INPUT")
	m.dropIPv6Block(ctx)
	m.setTproxy(false)
}

func (m *Manager) setTproxy(on bool) {
	m.tproxyMu.Lock()
	m.tproxyOn = on
	m.tproxyMu.Unlock()
}

// hasActiveVPNNode reports whether the state has a node selected, i.e. whether
// the config on disk is generated (and safe to regenerate) or pasted by hand.
func (m *Manager) hasActiveVPNNode() bool {
	snap := m.store.Snapshot()
	if strings.TrimSpace(snap.VPN.ActiveID) == "" {
		return false
	}
	for _, n := range snap.VPN.Nodes {
		if n.ID == snap.VPN.ActiveID {
			return true
		}
	}
	return false
}

// TransparentActive reports whether the redirect rules are currently installed.
func (m *Manager) TransparentActive() bool {
	m.tproxyMu.Lock()
	defer m.tproxyMu.Unlock()
	return m.tproxyOn
}

// resumeTransparent restores the rules after a reboot. The v2ray config is
// regenerated first, because the dokodemo inbound only exists while the
// transparent switch is on. If the unit is not up yet it waits a bit, because
// the rules must not exist without a listener.
func (m *Manager) resumeTransparent(ctx context.Context) {
	if !m.Config().VPN.Transparent {
		m.removeTransparentProxy(ctx)
		return
	}
	for i := 0; i < 12; i++ {
		if m.unitActive(ctx, m.Config().VPN.ServiceName) {
			// ApplyVPN rewrites /etc/v2ray/config.json (adding the dokodemo
			// inbound) and applies the redirect rules. A config pasted by hand
			// has no node, so it is left untouched and only the rules are set.
			if m.hasActiveVPNNode() {
				if err := m.ApplyVPN(ctx); err != nil {
					m.log.Printf("恢复透明代理规则失败: %v", err)
				}
				return
			}
			if err := m.applyTransparentProxy(ctx); err != nil {
				m.log.Printf("恢复透明代理规则失败: %v", err)
			}
			return
		}
		if !m.slept(5 * time.Second) {
			return
		}
	}
	m.log.Printf("VPN 未运行，跳过透明代理规则")
	m.removeTransparentProxy(ctx)
}

// transparentRuleSummary renders iptables-save style counters for the page.
func (m *Manager) transparentRuleSummary(ctx context.Context) string {
	if !m.Available("iptables") {
		return ""
	}
	out := m.RunTry(ctx, 5*time.Second, "iptables", "-t", "nat", "-L", tproxyPreChain, "-n", "-v")
	return strings.TrimSpace(out)
}
