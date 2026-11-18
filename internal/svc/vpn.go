package svc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"msm8916d/internal/config"
	"msm8916d/internal/state"
)

// VPNNodeView is the node information exposed to the page. The UUID/password is
// masked so the value does not end up in the browser history or a screenshot.
type VPNNodeView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Network  string `json:"network,omitempty"`
	TLS      bool   `json:"tls,omitempty"`
	Host     string `json:"host,omitempty"`
	Path     string `json:"path,omitempty"`
	Secret   string `json:"secret"`
	Active   bool   `json:"active"`
	// FromConfig marks a node that was read from the live config file instead of
	// being imported, so the page can offer to save it into the list.
	FromConfig bool `json:"from_config,omitempty"`
}

// VPNStatus is the VPN page payload.
type VPNStatus struct {
	Installed       bool   `json:"installed"`
	Version         string `json:"version"`
	Binary          string `json:"binary"`
	ConfigPath      string `json:"config_path"`
	ServiceName     string `json:"service_name"`
	Active          bool   `json:"active"`
	Enabled         bool   `json:"enabled"`
	SocksPort       int    `json:"socks_port"`
	HTTPPort        int    `json:"http_port"`
	AllowLAN        bool   `json:"allow_lan"`
	LogPath         string `json:"log_path"`
	RoutingMode     string `json:"routing_mode"`
	GeoPath         string `json:"geo_path"`
	GeoReady        bool   `json:"geo_ready"`
	Transparent     bool   `json:"transparent"`
	TransparentPort int    `json:"transparent_port"`
	TransparentOn   bool   `json:"transparent_on"`
	// AutoStopNote explains why the health watchdog stopped the VPN.
	AutoStopNote string        `json:"auto_stop_note,omitempty"`
	GeoIPSize    int64         `json:"geoip_bytes"`
	GeoSiteSize  int64         `json:"geosite_bytes"`
	ConfigExists bool          `json:"config_exists"`
	Live         *VPNLive      `json:"live,omitempty"`
	Nodes        []VPNNodeView `json:"nodes"`
	Error        string        `json:"error,omitempty"`
}

// VPNLiveInbound is one inbound entry of the active config file.
type VPNLiveInbound struct {
	Tag      string `json:"tag"`
	Listen   string `json:"listen"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
}

// VPNLiveOutbound summarises the proxy outbound of the active config file.
type VPNLiveOutbound struct {
	Tag      string `json:"tag"`
	Protocol string `json:"protocol"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Network  string `json:"network"`
	Security string `json:"security"`
	SNI      string `json:"sni,omitempty"`
	Path     string `json:"path,omitempty"`
	Host     string `json:"host,omitempty"`
	Flow     string `json:"flow,omitempty"`
	AlterID  int    `json:"alter_id,omitempty"`
	Method   string `json:"method,omitempty"`
}

// VPNLive is what the config file on disk actually contains, independent of the
// settings the page keeps in its own config.
type VPNLive struct {
	Valid     bool             `json:"valid"`
	Inbounds  []VPNLiveInbound `json:"inbounds"`
	Outbound  *VPNLiveOutbound `json:"outbound,omitempty"`
	Smart     bool             `json:"smart"`
	RuleCount int              `json:"rule_count"`
	Error     string           `json:"error,omitempty"`
}

// VPNStatus collects the current v2ray state.
func (m *Manager) VPNStatus(ctx context.Context) *VPNStatus {
	cfg := m.Config().VPN
	st := &VPNStatus{
		Binary:          cfg.Binary,
		ConfigPath:      cfg.ConfigPath,
		ServiceName:     cfg.ServiceName,
		SocksPort:       cfg.SocksPort,
		HTTPPort:        cfg.HTTPPort,
		AllowLAN:        cfg.AllowLAN,
		LogPath:         cfg.LogPath,
		RoutingMode:     cfg.RoutingMode,
		GeoPath:         cfg.GeoPath,
		Transparent:     cfg.Transparent,
		TransparentPort: cfg.TransparentPort,
		TransparentOn:   m.TransparentActive(),
		AutoStopNote:    m.VPNAutoStopNote(),
	}
	if _, err := os.Stat(cfg.ConfigPath); err == nil {
		st.ConfigExists = true
		st.Live = parseVPNLive(cfg.ConfigPath)
	}
	st.GeoReady = geoReady(cfg.GeoPath)
	if fi, err := os.Stat(filepath.Join(cfg.GeoPath, "geoip.dat")); err == nil {
		st.GeoIPSize = fi.Size()
	}
	if fi, err := os.Stat(filepath.Join(cfg.GeoPath, "geosite.dat")); err == nil {
		st.GeoSiteSize = fi.Size()
	}
	if _, err := os.Stat(cfg.Binary); err == nil {
		st.Installed = true
		if out := m.RunTry(ctx, 5*time.Second, cfg.Binary, "version"); out != "" {
			st.Version = firstVersionLine(out)
		}
	}
	if st.Installed && st.Version == "" {
		st.Version = "已安装"
	}
	st.Active = m.unitActive(ctx, cfg.ServiceName)
	st.Enabled = m.unitEnabled(ctx, cfg.ServiceName)

	snap := m.store.Snapshot()
	for _, n := range snap.VPN.Nodes {
		st.Nodes = append(st.Nodes, vpnNodeView(n, n.ID == snap.VPN.ActiveID))
	}
	// If the live config runs a server that is not in the list (for example a
	// config pasted from v2rayN), show it so the page reflects reality and the
	// node can be saved with one click.
	if st.Live != nil && st.Live.Valid && st.Live.Outbound != nil {
		o := st.Live.Outbound
		if !hasMatchingNode(snap.VPN.Nodes, o.Protocol, o.Address, o.Port) {
			st.Nodes = append(st.Nodes, VPNNodeView{
				ID:         liveNodeID,
				Name:       fmt.Sprintf("%s:%d（当前配置）", o.Address, o.Port),
				Protocol:   o.Protocol,
				Address:    o.Address,
				Port:       o.Port,
				Network:    o.Network,
				TLS:        o.Security == "tls" || o.Security == "reality",
				Host:       o.Host,
				Path:       o.Path,
				Secret:     "（在配置文件中）",
				Active:     true,
				FromConfig: true,
			})
		}
	}
	return st
}

// liveNodeID is the synthetic id of the "current config" row.
const liveNodeID = "live"

func hasMatchingNode(nodes []state.VPNNode, protocol, address string, port int) bool {
	for _, n := range nodes {
		if strings.EqualFold(n.Protocol, protocol) && strings.EqualFold(n.Address, address) && n.Port == port {
			return true
		}
	}
	return false
}

// AdoptVPNNode saves the server found in the live config into the node list and
// marks it active without rewriting the config (it is already what runs).
func (m *Manager) AdoptVPNNode(ctx context.Context) (*VPNNodeView, error) {
	cfg := m.Config().VPN
	node, ok := nodeFromLiveConfig(cfg.ConfigPath)
	if !ok {
		return nil, fmt.Errorf("当前配置里没有可保存的服务器")
	}
	if err := normalizeVPNNode(&node); err != nil {
		return nil, err
	}
	view, err := m.saveVPNNode(node)
	if err != nil {
		return nil, err
	}
	m.store.Update(func(s *state.State) {
		for _, n := range s.VPN.Nodes {
			if strings.EqualFold(n.Address, node.Address) && n.Port == node.Port &&
				strings.EqualFold(n.Protocol, node.Protocol) {
				s.VPN.ActiveID = n.ID
				view.ID = n.ID
				return
			}
		}
	})
	view.Active = true
	return view, nil
}

// VPNNodes returns the raw nodes (internal use).
func (m *Manager) VPNNodes() []state.VPNNode {
	return m.store.Snapshot().VPN.Nodes
}

// AddVPNNode imports a share link (vmess/vless/trojan/ss) or an explicit node.
func (m *Manager) AddVPNNode(ctx context.Context, link string) (*VPNNodeView, error) {
	link = strings.TrimSpace(link)
	if link == "" {
		return nil, fmt.Errorf("请粘贴节点分享链接")
	}
	node, err := parseShareLink(link)
	if err != nil {
		return nil, err
	}
	return m.saveVPNNode(node)
}

// AddVPNNodeManual stores a node entered by hand in the form.
func (m *Manager) AddVPNNodeManual(ctx context.Context, n state.VPNNode) (*VPNNodeView, error) {
	if err := normalizeVPNNode(&n); err != nil {
		return nil, err
	}
	return m.saveVPNNode(n)
}

// normalizeVPNNode validates a hand-entered node and fills the safe defaults.
func normalizeVPNNode(n *state.VPNNode) error {
	n.Protocol = strings.ToLower(strings.TrimSpace(n.Protocol))
	switch n.Protocol {
	case "vmess", "vless", "trojan", "ss":
	default:
		return fmt.Errorf("协议只能是 vmess / vless / trojan / ss")
	}
	n.Address = strings.TrimSpace(n.Address)
	if n.Address == "" || strings.ContainsAny(n.Address, " \t\r\n") {
		return fmt.Errorf("请填写有效的服务器地址")
	}
	if n.Port < 1 || n.Port > 65535 {
		return fmt.Errorf("端口必须在 1-65535 之间")
	}
	n.UUID = strings.TrimSpace(n.UUID)
	if n.UUID == "" {
		return fmt.Errorf("请填写凭据（vmess/vless 是 UUID，trojan/ss 是密码）")
	}
	n.Network = strings.ToLower(nonEmpty(strings.TrimSpace(n.Network), "tcp"))
	switch n.Network {
	case "tcp", "ws", "grpc", "h2", "http":
	default:
		return fmt.Errorf("传输方式只能是 tcp / ws / grpc / h2")
	}
	n.Name = strings.TrimSpace(n.Name)
	if n.Name == "" {
		n.Name = fmt.Sprintf("%s:%d", n.Address, n.Port)
	}
	if n.Protocol == "vless" {
		n.Security = nonEmpty(strings.TrimSpace(n.Security), "none")
	}
	return nil
}

// saveVPNNode stores a node, replacing an identical endpoint instead of
// duplicating it, and selects it when nothing is selected yet.
func (m *Manager) saveVPNNode(node state.VPNNode) (*VPNNodeView, error) {
	if node.Name == "" {
		node.Name = fmt.Sprintf("%s:%d", node.Address, node.Port)
	}
	node.AddedAt = time.Now().Unix()

	var saved state.VPNNode
	m.store.Update(func(s *state.State) {
		// Update an existing endpoint instead of stacking duplicates.
		for i, old := range s.VPN.Nodes {
			if strings.EqualFold(old.Address, node.Address) && old.Port == node.Port &&
				strings.EqualFold(old.Protocol, node.Protocol) && old.UUID == node.UUID {
				node.ID = old.ID
				s.VPN.Nodes[i] = node
				saved = node
				return
			}
		}
		node.ID = newLocalID()
		s.VPN.Nodes = append(s.VPN.Nodes, node)
		if s.VPN.ActiveID == "" {
			s.VPN.ActiveID = node.ID
		}
		saved = node
	})
	view := vpnNodeView(saved, false)
	return &view, nil
}

// VPNNodeByID returns one stored node including its credential (admin page only).
func (m *Manager) VPNNodeByID(id string) (*state.VPNNode, error) {
	for _, n := range m.store.Snapshot().VPN.Nodes {
		if n.ID == id {
			node := n
			return &node, nil
		}
	}
	return nil, fmt.Errorf("节点不存在")
}

// UpdateVPNNode replaces a stored node in place and reapplies the config when it
// is the one currently in use.
func (m *Manager) UpdateVPNNode(ctx context.Context, id string, n state.VPNNode) error {
	if err := normalizeVPNNode(&n); err != nil {
		return err
	}
	found := false
	m.store.Update(func(s *state.State) {
		for i := range s.VPN.Nodes {
			if s.VPN.Nodes[i].ID != id {
				continue
			}
			n.ID = id
			n.AddedAt = s.VPN.Nodes[i].AddedAt
			s.VPN.Nodes[i] = n
			found = true
			return
		}
	})
	if !found {
		return fmt.Errorf("节点不存在")
	}
	if m.store.Snapshot().VPN.ActiveID == id {
		return m.ApplyVPN(ctx)
	}
	return nil
}

// DeleteVPNNode removes a node and clears the selection if it was active.
func (m *Manager) DeleteVPNNode(id string) error {
	found := false
	m.store.Update(func(s *state.State) {
		out := s.VPN.Nodes[:0]
		for _, n := range s.VPN.Nodes {
			if n.ID == id {
				found = true
				continue
			}
			out = append(out, n)
		}
		s.VPN.Nodes = out
		if s.VPN.ActiveID == id {
			s.VPN.ActiveID = ""
			if len(s.VPN.Nodes) > 0 {
				s.VPN.ActiveID = s.VPN.Nodes[0].ID
			}
		}
	})
	if !found {
		return fmt.Errorf("节点不存在")
	}
	return nil
}

// SelectVPNNode marks a node as active and regenerates the config.
func (m *Manager) SelectVPNNode(ctx context.Context, id string) error {
	found := false
	m.store.Update(func(s *state.State) {
		for _, n := range s.VPN.Nodes {
			if n.ID == id {
				s.VPN.ActiveID = id
				found = true
				return
			}
		}
	})
	if !found {
		return fmt.Errorf("节点不存在")
	}
	return m.ApplyVPN(ctx)
}

// ApplyVPN writes the generated client config and restarts the unit when it is
// already running so the new node takes effect immediately.
func (m *Manager) ApplyVPN(ctx context.Context) error {
	cfg := m.Config().VPN
	snap := m.store.Snapshot()
	var node *state.VPNNode
	for i := range snap.VPN.Nodes {
		if snap.VPN.Nodes[i].ID == snap.VPN.ActiveID {
			node = &snap.VPN.Nodes[i]
			break
		}
	}
	doc, err := buildV2RayConfig(cfg, node)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.ConfigPath), 0o755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	if dir := filepath.Dir(cfg.LogPath); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	if err := os.WriteFile(cfg.ConfigPath, doc, 0o600); err != nil {
		return fmt.Errorf("写入 v2ray 配置失败: %w", err)
	}
	if m.unitActive(ctx, cfg.ServiceName) {
		if _, err := m.Run(ctx, 30*time.Second, "systemctl", "restart", cfg.ServiceName); err != nil {
			return fmt.Errorf("配置已写入，但重启 %s 失败: %w", cfg.ServiceName, err)
		}
		if err := m.applyTransparentProxy(ctx); err != nil {
			return err
		}
	} else {
		m.removeTransparentProxy(ctx)
	}
	return nil
}

// ControlVPN starts, stops or restarts the unit.
func (m *Manager) ControlVPN(ctx context.Context, action string) error {
	cfg := m.Config().VPN
	switch action {
	case "start", "stop", "restart":
	default:
		return fmt.Errorf("无效的操作: %s", action)
	}
	if action != "stop" {
		if _, err := os.Stat(cfg.Binary); err != nil {
			return fmt.Errorf("未找到 v2ray 程序（%s），请先安装", cfg.Binary)
		}
		if action == "start" {
			if err := m.ApplyVPN(ctx); err != nil {
				return err
			}
		}
	}
	if _, err := m.Run(ctx, 40*time.Second, "systemctl", action, cfg.ServiceName); err != nil {
		return fmt.Errorf("%s %s 失败: %w", action, cfg.ServiceName, err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		active := m.unitActive(ctx, cfg.ServiceName)
		if action == "stop" {
			if !active {
				m.removeTransparentProxy(ctx)
				m.setVPNIntent(false)
				return nil
			}
		} else if active {
			// 启动/重启成功后如果日志提示证书无法验证，自动补中间证书再拉起一次。
			m.maybeFixVPNCert(ctx)
			if err := m.applyTransparentProxy(ctx); err != nil {
				return err
			}
			m.setVPNIntent(true)
			m.clearVPNAutoStopNote()
			return nil
		}
		if time.Now().After(deadline) {
			if action == "stop" {
				return fmt.Errorf("已执行停止，但服务仍在运行")
			}
			// 服务没起来就别留着重定向，否则整机断网。
			m.removeTransparentProxy(ctx)
			return fmt.Errorf("服务已重启但当前未运行，请查看日志")
		}
		if !sleepCtx(ctx, 500*time.Millisecond) {
			return ctx.Err()
		}
	}
}

// setVPNIntent persists whether the operator left the VPN running so the same
// state can be restored after a reboot.
func (m *Manager) setVPNIntent(running bool) {
	m.store.Update(func(s *state.State) {
		s.VPN.Running = running
		s.VPN.RunningKnown = true
	})
	if err := m.store.Save(); err != nil {
		m.log.Printf("保存 VPN 状态失败: %v", err)
	}
}

// resumeVPN restores the state recorded by the last start/stop, so a reboot
// neither silently starts a VPN the user stopped nor leaves a wanted one off.
func (m *Manager) resumeVPN(ctx context.Context) {
	if !m.slept(8 * time.Second) {
		return
	}
	snap := m.store.Snapshot()
	if !snap.VPN.RunningKnown {
		// First run after the upgrade: adopt whatever the unit is doing now so
		// later reboots follow the same intent.
		m.setVPNIntent(m.unitActive(ctx, m.Config().VPN.ServiceName))
		return
	}
	cfg := m.Config().VPN
	if snap.VPN.Running {
		if m.unitActive(ctx, cfg.ServiceName) {
			return
		}
		if _, err := m.Run(ctx, 40*time.Second, "systemctl", "start", cfg.ServiceName); err != nil {
			m.log.Printf("按上次状态启动 VPN 失败: %v", err)
			return
		}
		m.log.Printf("按上次状态启动 VPN: %s", cfg.ServiceName)
		return
	}
	if m.unitActive(ctx, cfg.ServiceName) {
		if _, err := m.Run(ctx, 40*time.Second, "systemctl", "stop", cfg.ServiceName); err != nil {
			m.log.Printf("按上次状态停止 VPN 失败: %v", err)
			return
		}
		m.log.Printf("上次 VPN 为关闭状态，已停止 %s", cfg.ServiceName)
	}
	m.removeTransparentProxy(ctx)
}

// VPNLogs returns the tail of the v2ray error log.
func (m *Manager) VPNLogs(lines int) (string, error) {
	return m.ReadLogFile(m.Config().VPN.LogPath, lines)
}

// SetVPNConfig persists proxy ports and paths, then regenerates the config.
func (m *Manager) SetVPNConfig(ctx context.Context, binary, configPath, logPath string, socksPort, httpPort int, allowLAN bool, routingMode string, transparent bool, transparentPort int) error {
	if err := m.Update(func(c *config.Config) error {
		if strings.TrimSpace(binary) != "" {
			c.VPN.Binary = strings.TrimSpace(binary)
		}
		if strings.TrimSpace(configPath) != "" {
			c.VPN.ConfigPath = strings.TrimSpace(configPath)
		}
		if strings.TrimSpace(logPath) != "" {
			c.VPN.LogPath = strings.TrimSpace(logPath)
		}
		if socksPort > 0 {
			c.VPN.SocksPort = socksPort
		}
		if httpPort > 0 {
			c.VPN.HTTPPort = httpPort
		}
		if routingMode == "global" || routingMode == "smart" {
			c.VPN.RoutingMode = routingMode
		}
		if transparentPort > 0 {
			c.VPN.TransparentPort = transparentPort
		}
		c.VPN.Transparent = transparent
		c.VPN.AllowLAN = allowLAN
		return nil
	}); err != nil {
		return err
	}
	return m.ApplyVPN(ctx)
}

// unitActive reports whether a systemd unit is running.
func (m *Manager) unitActive(ctx context.Context, name string) bool {
	if !m.Available("systemctl") {
		return false
	}
	return strings.TrimSpace(m.RunTry(ctx, 5*time.Second, "systemctl", "is-active", name)) == "active"
}

func (m *Manager) unitEnabled(ctx context.Context, name string) bool {
	if !m.Available("systemctl") {
		return false
	}
	return strings.TrimSpace(m.RunTry(ctx, 5*time.Second, "systemctl", "is-enabled", name)) == "enabled"
}

func vpnNodeView(n state.VPNNode, active bool) VPNNodeView {
	return VPNNodeView{
		ID:       n.ID,
		Name:     n.Name,
		Protocol: n.Protocol,
		Address:  n.Address,
		Port:     n.Port,
		Network:  n.Network,
		TLS:      n.TLS,
		Host:     n.Host,
		Path:     n.Path,
		Secret:   maskSecret(n.UUID),
		Active:   active,
	}
}

// maskSecret keeps a hint of the credential so nodes can be told apart.
func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "****"
	}
	return s[:4] + "…" + s[len(s)-2:]
}

func firstVersionLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// "V2Ray 5.16.1 (go1.21.5 linux/arm64)" -> keep the first two words.
		f := strings.Fields(line)
		if len(f) >= 2 {
			return f[0] + " " + f[1]
		}
		return line
	}
	return ""
}

func newLocalID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b)
}

// buildV2RayConfig renders a client config with SOCKS/HTTP inbounds plus the
// active node as the default outbound.
func buildV2RayConfig(cfg config.VPNConfig, node *state.VPNNode) ([]byte, error) {
	listen := "127.0.0.1"
	if cfg.AllowLAN {
		listen = "0.0.0.0"
	}
	sniff := map[string]any{
		"enabled":      true,
		"destOverride": []string{"http", "tls"},
	}
	inbounds := []any{
		map[string]any{
			"tag":      "socks",
			"listen":   listen,
			"port":     cfg.SocksPort,
			"protocol": "socks",
			"settings": map[string]any{"auth": "noauth", "udp": true},
			"sniffing": sniff,
		},
		map[string]any{
			"tag":      "http",
			"listen":   listen,
			"port":     cfg.HTTPPort,
			"protocol": "http",
			"settings": map[string]any{},
			"sniffing": sniff,
		},
	}

	outbounds := []any{}
	if node != nil {
		ob, err := v2rayOutbound(*node)
		if err != nil {
			return nil, err
		}
		outbounds = append(outbounds, ob)
	}
	outbounds = append(outbounds,
		map[string]any{"tag": "direct", "protocol": "freedom"},
		map[string]any{"tag": "block", "protocol": "blackhole"},
	)

	if cfg.Transparent {
		port := cfg.TransparentPort
		if port <= 0 {
			port = 12345
		}
		// dokodemo-door receives the redirected connections; sniffing restores
		// the domain so smart routing still matches geosite rules.
		inbounds = append(inbounds, map[string]any{
			"tag":      "transparent",
			"listen":   "0.0.0.0",
			"port":     port,
			"protocol": "dokodemo-door",
			"settings": map[string]any{
				"network":        "tcp",
				"followRedirect": true,
			},
			"sniffing": map[string]any{
				"enabled":      true,
				"destOverride": []string{"http", "tls", "quic"},
			},
		})
		// Mark our own sockets so the redirect chains never capture the tunnel.
		for _, ob := range outbounds {
			o, ok := ob.(map[string]any)
			if !ok {
				continue
			}
			ss, ok := o["streamSettings"].(map[string]any)
			if !ok {
				ss = map[string]any{}
				o["streamSettings"] = ss
			}
			ss["sockopt"] = map[string]any{"mark": transparentMark}
		}
	}

	doc := map[string]any{
		"log": map[string]any{
			"loglevel": "warning",
			"error":    cfg.LogPath,
		},
		"inbounds":  inbounds,
		"outbounds": outbounds,
	}
	strategy, rules := vpnRouting(cfg)
	doc["routing"] = map[string]any{"domainStrategy": strategy, "rules": rules}
	return json.MarshalIndent(doc, "", "  ")
}

// privateRanges always bypass the proxy so LAN/USB management keeps working.
var privateRanges = []string{
	"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	"169.254.0.0/16", "100.64.0.0/10", "::1/128", "fc00::/7", "fe80::/10",
}

// vpnRouting returns the domain strategy and rules for the configured mode.
// "smart" needs geoip.dat / geosite.dat and silently falls back to "global"
// when they are missing, so a half-configured device still starts.
func vpnRouting(cfg config.VPNConfig) (string, []any) {
	if cfg.RoutingMode == "smart" && geoReady(cfg.GeoPath) {
		ips := append([]string{"geoip:cn"}, privateRanges...)
		return "IPIfNonMatch", []any{
			map[string]any{"type": "field", "outboundTag": "direct",
				"domain": []string{"geosite:cn", "geosite:private"}},
			map[string]any{"type": "field", "outboundTag": "direct", "ip": ips},
		}
	}
	return "AsIs", []any{
		map[string]any{"type": "field", "ip": privateRanges, "outboundTag": "direct"},
	}
}

// geoReady reports whether geoip.dat and geosite.dat are present.
func geoReady(dir string) bool {
	if strings.TrimSpace(dir) == "" {
		return false
	}
	for _, name := range []string{"geoip.dat", "geosite.dat"} {
		st, err := os.Stat(filepath.Join(dir, name))
		if err != nil || st.Size() == 0 {
			return false
		}
	}
	return true
}

// VPNGeoResult reports what a geo sync downloaded.
type VPNGeoResult struct {
	GeoIP   int64  `json:"geoip_bytes"`
	GeoSite int64  `json:"geosite_bytes"`
	Source  string `json:"source"`
}

// geoAssets lists the files "smart" routing needs. The first URL that answers
// wins; the gh-proxy mirror is first because GitHub is often unreachable here.
var geoAssets = []struct {
	Name string
	URLs []string
}{
	{"geoip.dat", []string{
		"https://gh-proxy.com/https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat",
		"https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat",
		"https://ghfast.top/https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat",
	}},
	{"geosite.dat", []string{
		"https://gh-proxy.com/https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat",
		"https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat",
		"https://ghfast.top/https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat",
	}},
}

// VPNGeoState reports the background geo sync progress.
type VPNGeoState struct {
	Running    bool     `json:"running"`
	Done       bool     `json:"done"`
	Step       string   `json:"step"`
	Percent    int      `json:"percent"`
	Downloaded int64    `json:"downloaded"`
	Total      int64    `json:"total"`
	Error      string   `json:"error,omitempty"`
	Logs       []string `json:"logs"`
}

// VPNGeoState returns a copy of the current geo sync progress.
func (m *Manager) VPNGeoState() VPNGeoState {
	m.geoMu.Lock()
	defer m.geoMu.Unlock()
	out := m.geoVPN
	out.Logs = append([]string(nil), m.geoVPN.Logs...)
	return out
}

// StartVPNGeoSync launches the download in the background so the page can poll
// for progress instead of waiting on one long request.
func (m *Manager) StartVPNGeoSync() error {
	m.geoMu.Lock()
	if m.geoVPN.Running {
		m.geoMu.Unlock()
		return fmt.Errorf("正在同步中，请稍候")
	}
	m.geoVPN = VPNGeoState{
		Running: true,
		Step:    "准备中",
		Logs:    []string{fmt.Sprintf("%s 开始同步 geo 文件", time.Now().Format("15:04:05"))},
	}
	m.geoMu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		m.runGeoSync(ctx)
	}()
	return nil
}

func (m *Manager) geoLog(format string, args ...any) {
	line := fmt.Sprintf("%s %s", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	m.geoMu.Lock()
	m.geoVPN.Logs = append(m.geoVPN.Logs, line)
	if len(m.geoVPN.Logs) > 60 {
		m.geoVPN.Logs = m.geoVPN.Logs[len(m.geoVPN.Logs)-60:]
	}
	m.geoVPN.Step = fmt.Sprintf(format, args...)
	m.geoMu.Unlock()
}

func (m *Manager) geoProgress(asset string, index int, downloaded, total int64) {
	m.geoMu.Lock()
	m.geoVPN.Downloaded = downloaded
	m.geoVPN.Total = total
	if total > 0 {
		base := index * 50
		m.geoVPN.Percent = base + int(float64(downloaded)/float64(total)*50)
	}
	m.geoVPN.Step = fmt.Sprintf("%s %.1f/%.1f MB", asset, float64(downloaded)/1048576, float64(total)/1048576)
	m.geoMu.Unlock()
}

func (m *Manager) geoFinish(err error) {
	m.geoMu.Lock()
	defer m.geoMu.Unlock()
	m.geoVPN.Running = false
	m.geoVPN.Done = true
	if err != nil {
		m.geoVPN.Error = err.Error()
		m.geoVPN.Step = "失败：" + err.Error()
	} else {
		m.geoVPN.Percent = 100
		m.geoVPN.Step = "完成"
	}
}

// runGeoSync downloads every asset, logging each step for the page.
func (m *Manager) runGeoSync(ctx context.Context) {
	cfg := m.Config().VPN
	dir := cfg.GeoPath
	if strings.TrimSpace(dir) == "" {
		m.geoLog("没有配置 geo 文件目录")
		m.geoFinish(fmt.Errorf("没有配置 geo 文件目录"))
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		m.geoLog("创建目录失败: %v", err)
		m.geoFinish(err)
		return
	}
	m.geoLog("目标目录 %s", dir)

	for i, asset := range geoAssets {
		target := filepath.Join(dir, asset.Name)
		tmp := target + ".tmp"
		done := false
		for _, u := range asset.URLs {
			host := u
			if h, err := url.Parse(u); err == nil && h.Host != "" {
				host = h.Host
			}
			m.geoLog("下载 %s <- %s", asset.Name, host)
			n, err := downloadToFile(ctx, u, tmp, func(d, t int64) {
				m.geoProgress(asset.Name, i, d, t)
			})
			if err != nil {
				m.geoLog("  %s 失败: %v", host, err)
				_ = os.Remove(tmp)
				continue
			}
			if n < 100*1024 {
				m.geoLog("  %s 只有 %d 字节，不像有效文件", host, n)
				_ = os.Remove(tmp)
				continue
			}
			if err := os.Rename(tmp, target); err != nil {
				m.geoLog("  替换 %s 失败: %v", asset.Name, err)
				_ = os.Remove(tmp)
				m.geoFinish(err)
				return
			}
			m.geoLog("  %s 完成 %.1f MB", asset.Name, float64(n)/1048576)
			done = true
			break
		}
		if !done {
			m.geoFinish(fmt.Errorf("下载 %s 失败，所有镜像都不可用", asset.Name))
			return
		}
	}

	if m.unitActive(ctx, cfg.ServiceName) {
		m.geoLog("重启 %s 让新 geo 生效", cfg.ServiceName)
		if _, err := m.Run(ctx, 30*time.Second, "systemctl", "restart", cfg.ServiceName); err != nil {
			m.geoLog("重启失败: %v", err)
			m.geoFinish(fmt.Errorf("geo 已更新，但重启 %s 失败: %w", cfg.ServiceName, err))
			return
		}
	}
	m.geoLog("全部完成")
	m.geoFinish(nil)
}

// downloadToFile streams a URL into dst, reporting progress as it goes.
func downloadToFile(ctx context.Context, u, dst string, progress func(downloaded, total int64)) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "msm8916d-geo-sync")
	resp, err := geoClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	total := resp.ContentLength
	var written int64
	buf := make([]byte, 64*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return written, werr
			}
			written += int64(n)
			if progress != nil {
				progress(written, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return written, rerr
		}
	}
	return written, nil
}

// geoClient bounds the download so a stuck mirror cannot hang forever.
var geoClient = &http.Client{Timeout: 150 * time.Second}

// parseVPNLive reads the config file and summarises what v2ray will actually do.
func parseVPNLive(path string) *VPNLive {
	live := &VPNLive{}
	data, err := os.ReadFile(path)
	if err != nil {
		live.Error = "读取配置失败"
		return live
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		live.Error = "配置文件不是合法 JSON"
		return live
	}
	live.Valid = true

	if arr, ok := doc["inbounds"].([]any); ok {
		for _, it := range arr {
			m := asMap(it)
			if m == nil {
				continue
			}
			live.Inbounds = append(live.Inbounds, VPNLiveInbound{
				Tag:      strOf(m["tag"]),
				Listen:   strOf(m["listen"]),
				Port:     intOf(m["port"]),
				Protocol: strOf(m["protocol"]),
			})
		}
	}

	if arr, ok := doc["outbounds"].([]any); ok {
		for _, it := range arr {
			m := asMap(it)
			if m == nil {
				continue
			}
			p := strings.ToLower(strOf(m["protocol"]))
			if p == "freedom" || p == "blackhole" {
				continue
			}
			live.Outbound = summariseOutbound(m, p)
			break
		}
	}

	if routing := asMap(doc["routing"]); routing != nil {
		if rules, ok := routing["rules"].([]any); ok {
			live.RuleCount = len(rules)
		}
		if b, err := json.Marshal(routing); err == nil {
			live.Smart = strings.Contains(string(b), "geosite:") || strings.Contains(string(b), "geoip:cn")
		}
	}
	return live
}

// summariseOutbound flattens the first proxy outbound for display.
func summariseOutbound(m map[string]any, protocol string) *VPNLiveOutbound {
	out := &VPNLiveOutbound{Tag: strOf(m["tag"]), Protocol: protocol}
	settings := asMap(m["settings"])
	if settings != nil {
		switch protocol {
		case "vmess", "vless":
			if vnext, ok := settings["vnext"].([]any); ok && len(vnext) > 0 {
				server := asMap(vnext[0])
				if server != nil {
					out.Address = strOf(server["address"])
					out.Port = intOf(server["port"])
					if users, ok := server["users"].([]any); ok && len(users) > 0 {
						user := asMap(users[0])
						if user != nil {
							out.AlterID = intOf(user["alterId"])
							out.Flow = strOf(user["flow"])
						}
					}
				}
			}
		case "trojan", "shadowsocks", "ss":
			if servers, ok := settings["servers"].([]any); ok && len(servers) > 0 {
				server := asMap(servers[0])
				if server != nil {
					out.Address = strOf(server["address"])
					out.Port = intOf(server["port"])
					out.Method = strOf(server["method"])
				}
			}
		}
	}
	stream := asMap(m["streamSettings"])
	if stream != nil {
		out.Network = strOf(stream["network"])
		out.Security = strOf(stream["security"])
		if tls := asMap(stream["tlsSettings"]); tls != nil {
			out.SNI = strOf(tls["serverName"])
		}
		if reality := asMap(stream["realitySettings"]); reality != nil {
			if out.SNI == "" {
				out.SNI = strOf(reality["serverName"])
			}
			out.Security = "reality"
		}
		if ws := asMap(stream["wsSettings"]); ws != nil {
			out.Path = strOf(ws["path"])
			if headers := asMap(ws["headers"]); headers != nil {
				out.Host = strOf(headers["Host"])
			}
			if out.Host == "" {
				// v2rayN 6.x writes the host directly instead of in headers.
				out.Host = strOf(ws["host"])
			}
		}
		if grpc := asMap(stream["grpcSettings"]); grpc != nil {
			out.Path = strOf(grpc["serviceName"])
		}
		if h2 := asMap(stream["httpSettings"]); h2 != nil && out.Path == "" {
			out.Path = strOf(h2["path"])
		}
	}
	return out
}

// nodeFromLiveConfig parses the proxy outbound of the active config into a node.
func nodeFromLiveConfig(path string) (state.VPNNode, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return state.VPNNode{}, false
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return state.VPNNode{}, false
	}
	outbounds, _ := doc["outbounds"].([]any)
	for _, it := range outbounds {
		m := asMap(it)
		if m == nil {
			continue
		}
		p := strings.ToLower(strOf(m["protocol"]))
		settings := asMap(m["settings"])
		switch p {
		case "vmess", "vless":
			vnext, _ := settings["vnext"].([]any)
			if len(vnext) == 0 {
				continue
			}
			server := asMap(vnext[0])
			if server == nil {
				continue
			}
			n := state.VPNNode{
				Protocol: p,
				Address:  strOf(server["address"]),
				Port:     intOf(server["port"]),
			}
			if users, ok := server["users"].([]any); ok && len(users) > 0 {
				user := asMap(users[0])
				if user != nil {
					n.UUID = strOf(user["id"])
					n.AlterID = intOf(user["alterId"])
					n.Security = strOf(user["security"])
					n.Flow = strOf(user["flow"])
				}
			}
			fillStreamFields(&n, m)
			return n, n.UUID != ""
		case "trojan", "shadowsocks", "ss":
			servers, _ := settings["servers"].([]any)
			if len(servers) == 0 {
				continue
			}
			server := asMap(servers[0])
			if server == nil {
				continue
			}
			proto := p
			if proto == "shadowsocks" {
				proto = "ss"
			}
			n := state.VPNNode{
				Protocol: proto,
				Address:  strOf(server["address"]),
				Port:     intOf(server["port"]),
				UUID:     strOf(server["password"]),
				Security: strOf(server["method"]),
			}
			fillStreamFields(&n, m)
			return n, n.UUID != ""
		}
	}
	return state.VPNNode{}, false
}

// fillStreamFields copies transport/TLS details from an outbound's streamSettings.
func fillStreamFields(n *state.VPNNode, outbound map[string]any) {
	stream := asMap(outbound["streamSettings"])
	if stream == nil {
		return
	}
	n.Network = nonEmpty(strings.ToLower(strOf(stream["network"])), "tcp")
	switch strings.ToLower(strOf(stream["security"])) {
	case "tls", "reality":
		n.TLS = true
	}
	if tls := asMap(stream["tlsSettings"]); tls != nil {
		n.SNI = strOf(tls["serverName"])
	}
	if r := asMap(stream["realitySettings"]); r != nil {
		n.RealityPublicKey = strOf(r["publicKey"])
		n.RealityShortID = strOf(r["shortId"])
		n.RealitySpiderX = strOf(r["spiderX"])
		if n.SNI == "" {
			n.SNI = strOf(r["serverName"])
		}
	}
	if ws := asMap(stream["wsSettings"]); ws != nil {
		n.Path = strOf(ws["path"])
		if headers := asMap(ws["headers"]); headers != nil {
			n.Host = strOf(headers["Host"])
		}
		if n.Host == "" {
			n.Host = strOf(ws["host"])
		}
	}
	if grpc := asMap(stream["grpcSettings"]); grpc != nil {
		n.Path = strOf(grpc["serviceName"])
	}
	if h2 := asMap(stream["httpSettings"]); h2 != nil {
		if n.Path == "" {
			n.Path = strOf(h2["path"])
		}
		if hosts, ok := h2["host"].([]any); ok && len(hosts) > 0 && n.Host == "" {
			n.Host = strOf(hosts[0])
		}
	}
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func strOf(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.Itoa(int(t))
	}
	return ""
}

func intOf(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(t)
		return n
	}
	return 0
}

// VPNCertResult reports what the automatic certificate fix did.
type VPNCertResult struct {
	Host      string   `json:"host"`
	Installed []string `json:"installed"`
	Verified  bool     `json:"verified"`
	Message   string   `json:"message"`
}

// VPNCertFix walks the TLS chain of the active node, downloads the missing
// intermediate certificates from their AIA URL and installs them into the system
// trust store. Servers that only send the leaf certificate (a common nginx
// misconfiguration) then verify without manual work.
func (m *Manager) VPNCertFix(ctx context.Context) (*VPNCertResult, error) {
	node, ok := nodeFromLiveConfig(m.Config().VPN.ConfigPath)
	if !ok {
		return nil, fmt.Errorf("当前配置里没有可检测的服务器")
	}
	if !node.TLS {
		return nil, fmt.Errorf("当前节点没有启用 TLS，不需要修复证书")
	}
	addr := net.JoinHostPort(node.Address, strconv.Itoa(node.Port))
	sni := node.SNI
	if sni == "" {
		sni = node.Host
	}
	if sni == "" {
		sni = node.Address
	}
	res := &VPNCertResult{Host: addr}

	if err := verifyTLSChain(addr, sni); err == nil {
		res.Verified = true
		res.Message = "证书链完整，无需修复"
		return res, nil
	}

	leaf, err := peerLeafCertificate(addr, sni)
	if err != nil {
		return nil, fmt.Errorf("连接 %s 获取证书失败: %w", addr, err)
	}
	cert := leaf
	for depth := 0; depth < 4 && cert != nil; depth++ {
		if len(cert.IssuingCertificateURL) == 0 {
			break
		}
		var issuer *x509.Certificate
		for _, u := range cert.IssuingCertificateURL {
			c, raw, ferr := fetchCertificate(ctx, u)
			if ferr != nil {
				continue
			}
			if c.Subject.String() != cert.Issuer.String() {
				continue
			}
			if trustedCertDigests()[certDigest(raw)] {
				issuer = c // 信任库里已有，继续往上走
				break
			}
			name, ierr := installTrustedCertificate(c, raw)
			if ierr != nil {
				return nil, ierr
			}
			res.Installed = append(res.Installed, name)
			issuer = c
			break
		}
		if issuer == nil {
			break
		}
		if issuer.CheckSignatureFrom(issuer) == nil {
			break // reached a self-signed root
		}
		cert = issuer
	}

	if len(res.Installed) == 0 {
		res.Message = "没能从服务器下载到缺失的中间证书（AIA），需要在服务器上配置 fullchain.pem"
		return res, fmt.Errorf("%s", res.Message)
	}
	if _, err := m.Run(ctx, 60*time.Second, "update-ca-certificates"); err != nil {
		res.Message = fmt.Sprintf("已安装 %d 张证书，但刷新信任库失败", len(res.Installed))
		return res, fmt.Errorf("%s: %w", res.Message, err)
	}
	if err := verifyTLSChain(addr, sni); err != nil {
		res.Message = fmt.Sprintf("已安装 %d 张证书，但验证仍失败", len(res.Installed))
		return res, fmt.Errorf("%s: %w", res.Message, err)
	}
	res.Verified = true
	res.Message = fmt.Sprintf("已安装 %d 张缺失证书（%s），证书链验证通过", len(res.Installed), strings.Join(res.Installed, "、"))
	return res, nil
}

// maybeFixVPNCert restarts v2ray after fixing the chain when the log shows a
// verification failure. Called after start/restart so the user does not have to.
func (m *Manager) maybeFixVPNCert(ctx context.Context) {
	cfg := m.Config().VPN
	text, err := m.ReadLogFile(cfg.LogPath, 60)
	if err != nil || text == "" {
		return
	}
	if !strings.Contains(text, "unknown authority") && !strings.Contains(text, "failed to verify certificate") {
		return
	}
	m.log.Printf("VPN 证书链不完整，尝试自动补中间证书")
	res, ferr := m.VPNCertFix(ctx)
	if ferr != nil {
		m.log.Printf("自动补证书失败: %v", ferr)
		return
	}
	m.log.Printf("自动补证书: %s", res.Message)
	if res.Verified {
		_, _ = m.Run(ctx, 30*time.Second, "systemctl", "restart", cfg.ServiceName)
	}
}

// verifyTLSChain dials the endpoint and verifies the chain against the trust
// bundle on disk. SystemCertPool is cached per process, so after installing a
// new certificate it would still report the old result.
func verifyTLSChain(addr, sni string) error {
	d := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", addr, &tls.Config{
		ServerName:         sni,
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
	})
	if err != nil {
		return err
	}
	defer conn.Close()
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return fmt.Errorf("服务器没有返回证书")
	}
	roots, err := loadTrustPool()
	if err != nil {
		return err
	}
	inter := x509.NewCertPool()
	for _, c := range certs[1:] {
		inter.AddCert(c)
	}
	_, err = certs[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter, DNSName: sni})
	return err
}

// loadTrustPool reads the CA bundle from disk so verification sees certificates
// installed after the process started.
func loadTrustPool() (*x509.CertPool, error) {
	for _, path := range []string{
		"/etc/ssl/certs/ca-certificates.crt",
		"/etc/pki/tls/certs/ca-bundle.crt",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		pool := x509.NewCertPool()
		if pool.AppendCertsFromPEM(data) {
			return pool, nil
		}
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		return x509.NewCertPool(), err
	}
	return pool, nil
}

func certDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// trustedCertDigests lists the certificates already in the CA bundle.
func trustedCertDigests() map[string]bool {
	out := map[string]bool{}
	data, err := os.ReadFile("/etc/ssl/certs/ca-certificates.crt")
	if err != nil {
		return out
	}
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			return out
		}
		if block.Type == "CERTIFICATE" {
			out[certDigest(block.Bytes)] = true
		}
	}
}

// peerLeafCertificate returns the leaf certificate the server presents.
func peerLeafCertificate(addr, sni string) (*x509.Certificate, error) {
	d := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", addr, &tls.Config{
		ServerName:         sni,
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
	})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, fmt.Errorf("服务器没有返回证书")
	}
	return certs[0], nil
}

// fetchCertificate downloads and parses a DER or PEM certificate.
func fetchCertificate(ctx context.Context, u string) (*x509.Certificate, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "msm8916d-certfix")
	resp, err := certClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, nil, err
	}
	if cert, err := x509.ParseCertificate(body); err == nil {
		return cert, body, nil
	}
	if block, _ := pem.Decode(body); block != nil {
		if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
			return cert, block.Bytes, nil
		}
	}
	return nil, nil, fmt.Errorf("不是有效的证书")
}

// installTrustedCertificate writes a certificate into the local trust store.
func installTrustedCertificate(cert *x509.Certificate, raw []byte) (string, error) {
	sum := sha256.Sum256(raw)
	name := fmt.Sprintf("msm8916-%s.crt", hex.EncodeToString(sum[:4]))
	dir := "/usr/local/share/ca-certificates"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw})
	if err := os.WriteFile(path, pemBytes, 0o644); err != nil {
		return "", err
	}
	label := cert.Subject.CommonName
	if label == "" {
		label = name
	}
	return label, nil
}

var certClient = &http.Client{Timeout: 20 * time.Second}

// VPNRawConfig returns the current config file content so the page can show and
// edit it (for configs pasted from v2rayN or hand written).
func (m *Manager) VPNRawConfig() (string, error) {
	data, err := os.ReadFile(m.Config().VPN.ConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("读取 v2ray 配置失败: %w", err)
	}
	return string(data), nil
}

// SaveVPNRawConfig validates and installs a hand-edited config. The validation
// uses "v2ray test", so a syntactically fine but semantically broken file is
// rejected before the running service is touched.
func (m *Manager) SaveVPNRawConfig(ctx context.Context, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("配置内容为空")
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(text), &probe); err != nil {
		return fmt.Errorf("不是合法的 JSON: %w", err)
	}
	cfg := m.Config().VPN
	// When the transparent proxy is on, the pasted config needs the dokodemo
	// inbound and the socket marks, otherwise the redirects would have no
	// listener and the tunnel itself would be captured.
	if ensureTransparentConfig(probe, cfg) {
		buf, err := json.MarshalIndent(probe, "", "  ")
		if err != nil {
			return fmt.Errorf("整理配置失败: %w", err)
		}
		text = string(buf)
	}
	if _, err := os.Stat(cfg.Binary); err != nil {
		return fmt.Errorf("未找到 v2ray 程序，无法校验配置")
	}
	tmp, err := os.CreateTemp("", "msm8916-v2ray-*.json")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	if _, err := m.Run(ctx, 20*time.Second, cfg.Binary, "test", "-c", tmpName); err != nil {
		return fmt.Errorf("配置校验未通过: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.ConfigPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(cfg.ConfigPath, []byte(text+"\n"), 0o600); err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}
	if m.unitActive(ctx, cfg.ServiceName) {
		if _, err := m.Run(ctx, 30*time.Second, "systemctl", "restart", cfg.ServiceName); err != nil {
			return fmt.Errorf("配置已保存，但重启 %s 失败: %w", cfg.ServiceName, err)
		}
		if cfg.Transparent {
			if err := m.applyTransparentProxy(ctx); err != nil {
				return err
			}
		} else {
			m.removeTransparentProxy(ctx)
		}
	}
	return nil
}

// ensureTransparentConfig adds the dokodemo-door inbound and the socket marks a
// config needs to work with the transparent proxy. It returns whether the
// document was changed.
func ensureTransparentConfig(doc map[string]any, cfg config.VPNConfig) bool {
	if doc == nil || !cfg.Transparent {
		return false
	}
	port := cfg.TransparentPort
	if port <= 0 {
		port = 12345
	}
	changed := false

	inbounds, _ := doc["inbounds"].([]any)
	hasDoko := false
	for _, in := range inbounds {
		if mm, ok := in.(map[string]any); ok && strings.EqualFold(strOf(mm["protocol"]), "dokodemo-door") {
			hasDoko = true
			break
		}
	}
	if !hasDoko {
		inbounds = append(inbounds, map[string]any{
			"tag":      "transparent",
			"listen":   "0.0.0.0",
			"port":     port,
			"protocol": "dokodemo-door",
			"settings": map[string]any{
				"network":        "tcp",
				"followRedirect": true,
			},
			"sniffing": map[string]any{
				"enabled":      true,
				"destOverride": []string{"http", "tls", "quic"},
			},
		})
		doc["inbounds"] = inbounds
		changed = true
	}

	if outbounds, ok := doc["outbounds"].([]any); ok {
		for _, ob := range outbounds {
			mm, ok := ob.(map[string]any)
			if !ok {
				continue
			}
			ss, ok := mm["streamSettings"].(map[string]any)
			if !ok {
				ss = map[string]any{}
				mm["streamSettings"] = ss
			}
			sock, ok := ss["sockopt"].(map[string]any)
			if !ok {
				sock = map[string]any{}
				ss["sockopt"] = sock
			}
			if intOf(sock["mark"]) != transparentMark {
				sock["mark"] = transparentMark
				changed = true
			}
		}
	}
	return changed
}

func v2rayOutbound(n state.VPNNode) (map[string]any, error) {
	stream := map[string]any{"network": nonEmpty(n.Network, "tcp")}
	if n.TLS {
		tlsSettings := map[string]any{}
		if n.SNI != "" {
			tlsSettings["serverName"] = n.SNI
		} else if n.Host != "" && !isIPAddress(n.Host) {
			tlsSettings["serverName"] = n.Host
		}
		switch {
		case n.RealityPublicKey != "":
			// VLESS + REALITY: no certificate is involved, the public key
			// replaces the CA chain and SNI is mandatory.
			stream["security"] = "reality"
			reality := map[string]any{
				"publicKey":   n.RealityPublicKey,
				"fingerprint": "chrome",
			}
			if n.RealityShortID != "" {
				reality["shortId"] = n.RealityShortID
			}
			if n.RealitySpiderX != "" {
				reality["spiderX"] = n.RealitySpiderX
			}
			if sn, _ := tlsSettings["serverName"]; sn != nil {
				reality["serverName"] = sn
			}
			stream["realitySettings"] = reality
		default:
			stream["security"] = "tls"
			stream["tlsSettings"] = tlsSettings
		}
	}
	switch strings.ToLower(n.Network) {
	case "ws":
		ws := map[string]any{}
		if n.Path != "" {
			ws["path"] = n.Path
		}
		if n.Host != "" {
			ws["headers"] = map[string]any{"Host": n.Host}
		}
		stream["wsSettings"] = ws
	case "grpc":
		gs := map[string]any{}
		if n.Path != "" {
			gs["serviceName"] = n.Path
		}
		stream["grpcSettings"] = gs
	case "h2", "http":
		stream["network"] = "h2"
		hs := map[string]any{}
		if n.Path != "" {
			hs["path"] = n.Path
		}
		if n.Host != "" {
			hs["host"] = []string{n.Host}
		}
		stream["httpSettings"] = hs
	case "", "tcp":
		stream["network"] = "tcp"
	}

	switch strings.ToLower(n.Protocol) {
	case "vmess":
		sec := nonEmpty(n.Security, "auto")
		return map[string]any{
			"tag":      "proxy",
			"protocol": "vmess",
			"settings": map[string]any{
				"vnext": []any{map[string]any{
					"address": n.Address,
					"port":    n.Port,
					"users": []any{map[string]any{
						"id":       n.UUID,
						"alterId":  n.AlterID,
						"security": sec,
					}},
				}},
			},
			"streamSettings": stream,
		}, nil
	case "vless":
		user := map[string]any{"id": n.UUID, "encryption": nonEmpty(n.Security, "none")}
		if n.Flow != "" {
			user["flow"] = n.Flow
		}
		return map[string]any{
			"tag":      "proxy",
			"protocol": "vless",
			"settings": map[string]any{
				"vnext": []any{map[string]any{
					"address": n.Address,
					"port":    n.Port,
					"users":   []any{user},
				}},
			},
			"streamSettings": stream,
		}, nil
	case "trojan":
		return map[string]any{
			"tag":      "proxy",
			"protocol": "trojan",
			"settings": map[string]any{
				"servers": []any{map[string]any{
					"address":  n.Address,
					"port":     n.Port,
					"password": n.UUID,
				}},
			},
			"streamSettings": stream,
		}, nil
	case "ss", "shadowsocks":
		return map[string]any{
			"tag":      "proxy",
			"protocol": "shadowsocks",
			"settings": map[string]any{
				"servers": []any{map[string]any{
					"address":  n.Address,
					"port":     n.Port,
					"method":   nonEmpty(n.Security, "aes-256-gcm"),
					"password": n.UUID,
				}},
			},
			"streamSettings": stream,
		}, nil
	}
	return nil, fmt.Errorf("暂不支持的协议: %s", n.Protocol)
}

// ---------------------------------------------------------------- share links

// parseShareLink accepts the link formats used by v2rayN/Clash style clients.
func parseShareLink(link string) (state.VPNNode, error) {
	switch {
	case strings.HasPrefix(link, "vmess://"):
		return parseVMessLink(link)
	case strings.HasPrefix(link, "vless://"):
		return parseURIProtocol(link, "vless")
	case strings.HasPrefix(link, "trojan://"):
		return parseURIProtocol(link, "trojan")
	case strings.HasPrefix(link, "ss://"):
		return parseSSLink(link)
	}
	return state.VPNNode{}, fmt.Errorf("无法识别的链接，目前支持 vmess:// / vless:// / trojan:// / ss://")
}

func parseVMessLink(link string) (state.VPNNode, error) {
	raw := strings.TrimSpace(strings.TrimPrefix(link, "vmess://"))
	if i := strings.IndexAny(raw, "#?"); i >= 0 {
		raw = raw[:i]
	}
	data, err := decodeBase64Loose(raw)
	if err != nil {
		return state.VPNNode{}, fmt.Errorf("vmess 链接不是有效的 Base64: %w", err)
	}
	var v struct {
		PS   string `json:"ps"`
		Add  string `json:"add"`
		Port any    `json:"port"`
		ID   string `json:"id"`
		Aid  any    `json:"aid"`
		Scy  string `json:"scy"`
		Net  string `json:"net"`
		Type string `json:"type"`
		Host string `json:"host"`
		Path string `json:"path"`
		TLS  string `json:"tls"`
		SNI  string `json:"sni"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return state.VPNNode{}, fmt.Errorf("vmess 链接内容解析失败: %w", err)
	}
	port := anyToInt(v.Port)
	if v.Add == "" || port <= 0 || port > 65535 {
		return state.VPNNode{}, fmt.Errorf("vmess 链接缺少有效的地址或端口")
	}
	if v.ID == "" {
		return state.VPNNode{}, fmt.Errorf("vmess 链接缺少 id")
	}
	node := state.VPNNode{
		Name:     strings.TrimSpace(v.PS),
		Protocol: "vmess",
		Address:  strings.TrimSpace(v.Add),
		Port:     port,
		UUID:     v.ID,
		AlterID:  anyToInt(v.Aid),
		Security: nonEmpty(v.Scy, "auto"),
		Network:  nonEmpty(strings.ToLower(v.Net), "tcp"),
		Host:     strings.TrimSpace(v.Host),
		Path:     strings.TrimSpace(v.Path),
		TLS:      strings.EqualFold(v.TLS, "tls") || strings.EqualFold(v.TLS, "reality"),
		SNI:      strings.TrimSpace(v.SNI),
	}
	return node, nil
}

func parseURIProtocol(link, proto string) (state.VPNNode, error) {
	u, err := url.Parse(link)
	if err != nil {
		return state.VPNNode{}, fmt.Errorf("链接解析失败: %w", err)
	}
	if u.User == nil || u.Hostname() == "" {
		return state.VPNNode{}, fmt.Errorf("%s 链接缺少凭证或地址", proto)
	}
	port, _ := strconv.Atoi(u.Port())
	if port <= 0 || port > 65535 {
		return state.VPNNode{}, fmt.Errorf("%s 链接端口无效", proto)
	}
	q := u.Query()
	secret := u.User.Username()
	if pass, ok := u.User.Password(); ok && proto == "trojan" {
		secret = pass
	}
	if secret == "" {
		return state.VPNNode{}, fmt.Errorf("%s 链接缺少凭证", proto)
	}
	node := state.VPNNode{
		Name:     strings.TrimSpace(strings.TrimPrefix(u.Fragment, "#")),
		Protocol: proto,
		Address:  u.Hostname(),
		Port:     port,
		UUID:     secret,
		Network:  nonEmpty(strings.ToLower(q.Get("type")), "tcp"),
		Path:     q.Get("path"),
		Host:     q.Get("host"),
		SNI:      q.Get("sni"),
		Flow:     q.Get("flow"),
	}
	// TLS: an explicit security= decides; without it, trojan is always TLS while
	// vless defaults to plaintext unless the link asks for ws+http sniffing.
	switch sec := strings.ToLower(q.Get("security")); sec {
	case "none", "":
		node.TLS = proto == "trojan"
	default:
		node.TLS = true
	}
	if strings.EqualFold(q.Get("security"), "reality") {
		node.RealityPublicKey = q.Get("pbk")
		node.RealityShortID = q.Get("sid")
		node.RealitySpiderX = q.Get("spx")
	}
	if name, err := url.QueryUnescape(node.Name); err == nil {
		node.Name = name
	}
	if proto == "vless" {
		node.Security = nonEmpty(q.Get("encryption"), "none")
	}
	return node, nil
}

func parseSSLink(link string) (state.VPNNode, error) {
	raw := strings.TrimSpace(strings.TrimPrefix(link, "ss://"))
	name := ""
	if i := strings.Index(raw, "#"); i >= 0 {
		if n, err := url.QueryUnescape(raw[i+1:]); err == nil {
			name = n
		}
		raw = raw[:i]
	}
	if i := strings.Index(raw, "?"); i >= 0 {
		raw = raw[:i]
	}
	var userinfo, hostport string
	if i := strings.LastIndex(raw, "@"); i >= 0 {
		userinfo, hostport = raw[:i], raw[i+1:]
	} else {
		// Legacy form: base64(method:password@host:port)
		decoded, err := decodeBase64Loose(raw)
		if err != nil {
			return state.VPNNode{}, fmt.Errorf("ss 链接格式无效")
		}
		s := string(decoded)
		i := strings.LastIndex(s, "@")
		if i < 0 {
			return state.VPNNode{}, fmt.Errorf("ss 链接格式无效")
		}
		userinfo, hostport = s[:i], s[i+1:]
	}
	cred, err := decodeBase64Loose(userinfo)
	if err == nil && strings.Contains(string(cred), ":") {
		userinfo = string(cred)
	}
	parts := strings.SplitN(userinfo, ":", 2)
	if len(parts) != 2 {
		return state.VPNNode{}, fmt.Errorf("ss 链接缺少加密方式或密码")
	}
	host, portStr, err := splitHostPortLoose(hostport)
	if err != nil {
		return state.VPNNode{}, err
	}
	port, _ := strconv.Atoi(portStr)
	if port <= 0 || port > 65535 {
		return state.VPNNode{}, fmt.Errorf("ss 端口无效")
	}
	return state.VPNNode{
		Name:     name,
		Protocol: "ss",
		Address:  host,
		Port:     port,
		UUID:     parts[1],
		Security: parts[0],
		Network:  "tcp",
	}, nil
}

func splitHostPortLoose(s string) (string, string, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[") {
		i := strings.LastIndex(s, "]")
		if i < 0 || i+2 > len(s) {
			return "", "", fmt.Errorf("地址格式无效")
		}
		return s[1:i], s[i+2:], nil
	}
	i := strings.LastIndex(s, ":")
	if i <= 0 || i+1 >= len(s) {
		return "", "", fmt.Errorf("地址格式无效")
	}
	return s[:i], s[i+1:], nil
}

func decodeBase64Loose(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, "=")
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.RawStdEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("不是有效的 Base64")
}

func anyToInt(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(t))
		return n
	case int:
		return t
	}
	return 0
}

func nonEmpty(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func isIPAddress(s string) bool {
	return strings.Count(s, ".") == 3 && !strings.ContainsAny(s, "abcdefghijklmnopqrstuvwxyz")
}
