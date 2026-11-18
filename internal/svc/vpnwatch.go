package svc

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"msm8916d/internal/config"
)

const (
	// vpnWatchInterval is how often the tunnel is probed.
	vpnWatchInterval = 60 * time.Second
	// vpnWatchDowntime is how long the tunnel may stay broken before the VPN
	// is stopped. Only the transparent mode can take the whole network down,
	// so the watchdog is limited to that case.
	vpnWatchDowntime = 5 * time.Minute
)

// vpnWatchLoop probes a running VPN with the transparent proxy on. A dead node
// keeps the redirects in place, which would leave every client without
// internet, so after five minutes the VPN is stopped and direct access returns.
func (m *Manager) vpnWatchLoop(ctx context.Context) {
	if !m.slept(45 * time.Second) {
		return
	}
	ticker := time.NewTicker(vpnWatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.vpnWatchStep(ctx)
		}
	}
}

func (m *Manager) vpnWatchStep(ctx context.Context) {
	cfg := m.Config().VPN
	if !cfg.Transparent || !m.unitActive(ctx, cfg.ServiceName) {
		m.resetVPNDown()
		return
	}
	tunnelOK := m.vpnTunnelReachable(cfg)
	uplinkOK := m.vpnUplinkReachable(ctx)
	if !uplinkOK {
		// The uplink itself is down; stopping the VPN would not help.
		m.resetVPNDown()
		return
	}
	if tunnelOK {
		if !m.vpnDownSinceIsZero() {
			m.log.Printf("VPN 已恢复，重置自动停止计时")
		}
		m.resetVPNDown()
		return
	}

	m.vpnWatchMu.Lock()
	if m.vpnDownSince.IsZero() {
		m.vpnDownSince = time.Now()
	}
	down := time.Since(m.vpnDownSince)
	m.vpnWatchMu.Unlock()

	if down < vpnWatchDowntime {
		m.log.Printf("VPN 健康检查失败，已持续 %s（超过 %s 自动停止）", down.Round(time.Second), vpnWatchDowntime)
		return
	}

	m.log.Printf("VPN 连续 %s 不可用，自动停止以免断网", down.Round(time.Minute))
	if err := m.ControlVPN(ctx, "stop"); err != nil {
		m.log.Printf("自动停止 VPN 失败: %v", err)
		return
	}
	m.vpnWatchMu.Lock()
	m.vpnAutoStopNote = fmt.Sprintf("%s 自动停止：节点连续 %s 不可用",
		time.Now().Format("01-02 15:04"), down.Round(time.Minute))
	m.vpnDownSince = time.Time{}
	m.vpnWatchMu.Unlock()
}

// vpnTunnelReachable dials a target through the local proxy. A plain SOCKS5
// CONNECT is enough to prove the node works and costs a few bytes; the HTTP
// inbound is only used when the config has no SOCKS port.
func (m *Manager) vpnTunnelReachable(cfg config.VPNConfig) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if cfg.SocksPort > 0 {
		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.SocksPort))
		d := &net.Dialer{Timeout: 6 * time.Second}
		if conn, err := d.DialContext(ctx, "tcp", addr); err == nil {
			_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
			err = socks5Handshake(conn, "www.gstatic.com:443")
			conn.Close()
			if err == nil {
				return true
			}
		}
	}
	if client := proxyClientHTTP(cfg.HTTPPort); client != nil {
		defer client.CloseIdleConnections()
		if _, err := fetchSmall(ctx, client, []string{"https://www.gstatic.com/generate_204"}); err == nil {
			return true
		}
	}
	return false
}

// vpnUplinkReachable checks the plain uplink with a marked socket, so the
// transparent redirect does not route the probe back into the tunnel.
func (m *Manager) vpnUplinkReachable(ctx context.Context) bool {
	d := &net.Dialer{Timeout: 5 * time.Second, Control: controlMark}
	conn, err := d.DialContext(ctx, "tcp", "223.5.5.5:443")
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func (m *Manager) resetVPNDown() {
	m.vpnWatchMu.Lock()
	m.vpnDownSince = time.Time{}
	m.vpnWatchMu.Unlock()
}

func (m *Manager) vpnDownSinceIsZero() bool {
	m.vpnWatchMu.Lock()
	defer m.vpnWatchMu.Unlock()
	return m.vpnDownSince.IsZero()
}

// VPNAutoStopNote reports why the watchdog stopped the VPN (empty when none).
func (m *Manager) VPNAutoStopNote() string {
	m.vpnWatchMu.Lock()
	defer m.vpnWatchMu.Unlock()
	return m.vpnAutoStopNote
}

// clearVPNAutoStopNote is called when the operator starts the VPN again.
func (m *Manager) clearVPNAutoStopNote() {
	m.vpnWatchMu.Lock()
	m.vpnAutoStopNote = ""
	m.vpnDownSince = time.Time{}
	m.vpnWatchMu.Unlock()
}
