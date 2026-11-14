package svc

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"uz801d/internal/config"
	"uz801d/internal/netutil"
	"uz801d/internal/parse"
)

// BridgeCapability describes whether the radio can do managed + AP at once.
type BridgeCapability struct {
	Supported  bool               `json:"supported"`
	Reason     string             `json:"reason"`
	ExternalAP string             `json:"external_ap,omitempty"`
	Interfaces []string           `json:"interfaces"`
	Combos     []parse.IfaceCombo `json:"combos"`
}

// BridgeStatus is the AP-STA relay state.
type BridgeStatus struct {
	Enabled             bool             `json:"enabled"`
	Active              bool             `json:"active"`
	Mode                string           `json:"mode"`
	Auto                bool             `json:"auto"`
	Force               bool             `json:"force"`
	APInterface         string           `json:"ap_interface"`
	DownstreamInterface string           `json:"downstream_interface"`
	DownstreamIP        string           `json:"downstream_ip"`
	UpstreamIface       string           `json:"upstream_interface"`
	UpstreamSSID        string           `json:"upstream_ssid"`
	Channel             int              `json:"channel"`
	StartedAt           int64            `json:"started_at"`
	LastError           string           `json:"last_error"`
	Capability          BridgeCapability `json:"capability"`
	UpstreamIP          string           `json:"upstream_ip"`
}

// BridgeSnapshot returns a copy of the relay runtime state.
func (m *Manager) BridgeSnapshot() bridgeRuntime {
	m.bridgeMu.Lock()
	defer m.bridgeMu.Unlock()
	return m.bridge
}

// BridgeActive reports whether the relay is currently enabled.
func (m *Manager) BridgeActive() bool {
	return m.BridgeSnapshot().Active
}

// BridgeStatus gathers capability and runtime state.
func (m *Manager) BridgeStatus(ctx context.Context) *BridgeStatus {
	cfg := m.Config()
	br := m.BridgeSnapshot()
	st := &BridgeStatus{
		Enabled:             cfg.Wifi.Bridge.Enabled,
		Active:              br.Active,
		Mode:                cfg.Wifi.Bridge.Mode,
		Auto:                cfg.Wifi.Bridge.Auto,
		Force:               cfg.Wifi.Bridge.Force,
		APInterface:         br.APInterface,
		DownstreamInterface: cfg.Wifi.Bridge.DownstreamInterface,
		UpstreamIface:       br.UpstreamIface,
		UpstreamSSID:        br.UpstreamSSID,
		Channel:             br.Channel,
		StartedAt:           br.StartedAt.Unix(),
		LastError:           br.LastError,
		Capability:          m.BridgeCapability(ctx),
	}
	if cfg.Wifi.Bridge.Mode == "usb" {
		st.APInterface = cfg.Wifi.Bridge.DownstreamInterface
		st.Channel = 0
		if st.DownstreamInterface != "" {
			st.DownstreamIP = m.ifaceIPv4(ctx, st.DownstreamInterface)
		}
	}
	if st.UpstreamIface != "" {
		st.UpstreamIP = m.ifaceIPv4(ctx, st.UpstreamIface)
	}
	if !br.StartedAt.IsZero() {
		st.StartedAt = br.StartedAt.Unix()
	} else {
		st.StartedAt = 0
	}
	return st
}

// BridgeCapability inspects `iw list` and available interfaces.
func (m *Manager) BridgeCapability(ctx context.Context) BridgeCapability {
	cfg := m.Config()
	caps := BridgeCapability{}
	if cfg.Wifi.Bridge.Mode == "" || cfg.Wifi.Bridge.Mode == "usb" {
		caps.Supported = true
		caps.Reason = "USB 共享模式：wlan0 连接上游 WiFi，USB 下游接口给其他设备提供网络"
		for _, name := range []string{cfg.Wifi.Interface, cfg.Wifi.Bridge.DownstreamInterface} {
			if name != "" {
				caps.Interfaces = append(caps.Interfaces, name)
			}
		}
		return caps
	}
	if !m.Available("iw") {
		caps.Reason = "未安装 iw，无法检测无线接口组合"
		return caps
	}
	upstream := cfg.Wifi.Interface
	devs := parse.ParseIWDev(m.RunTry(ctx, 8*time.Second, "iw", "dev"))
	upPhy := ""
	for _, d := range devs {
		if d.Name == upstream {
			upPhy = d.Phy
		}
		if d.Name != "" {
			caps.Interfaces = append(caps.Interfaces, d.Name)
		}
	}
	for _, d := range devs {
		if d.Name == upstream || d.Name == "" {
			continue
		}
		if d.Phy != "" && upPhy != "" && d.Phy != upPhy {
			caps.ExternalAP = d.Name
			break
		}
	}
	if caps.ExternalAP != "" {
		caps.Supported = true
		caps.Reason = "检测到独立无线网卡 " + caps.ExternalAP + "，可同时作为 AP"
		return caps
	}
	out := m.RunTry(ctx, 10*time.Second, "iw", "list")
	caps.Combos = parse.ParseIfaceCombos(out)
	ok, reason := parse.SupportsAPSTA(caps.Combos)
	caps.Supported = ok
	caps.Reason = reason
	if !ok && len(caps.Combos) == 0 {
		caps.Reason = "无线驱动没有上报 AP+STA 并发能力。MSM8916 的 wcn36xx 实测只支持单 vif：" +
			"wlan0 作 STA 时再建 uap0 会被降级成 managed，hostapd 起不来。" +
			"请改用「WiFi 转 USB 共享」（wlan0 作 STA + usb0 作 LAN），或外接一块 USB 无线网卡做 AP。"
	}
	return caps
}

// bridgeAutoRetryDelay is the cool-down before the automatic state machine
// retries a failed bridge start.
const bridgeAutoRetryDelay = 5 * time.Minute

// EnableBridge starts the RaspAP-style AP-STA relay. On failure the previous
// access point is restored so the device does not disappear from the LAN.
func (m *Manager) EnableBridge(ctx context.Context) error {
	m.bridgeMu.Lock()
	var err error
	if mode := m.Config().Wifi.Bridge.Mode; mode == "" || mode == "usb" {
		err = m.enableUSBBridgeLocked(ctx)
	} else {
		err = m.enableBridgeLocked(ctx)
	}
	cfg := m.Config().Clone()
	m.bridgeMu.Unlock()
	if err != nil {
		m.bridgeMu.Lock()
		m.bridge.LastError = err.Error()
		m.bridgeMu.Unlock()
		m.log.Printf("桥接启用失败: %v，正在恢复原热点", err)
		if rerr := m.restoreNormalAP(ctx, cfg); rerr != nil {
			m.log.Printf("恢复原热点失败: %v", rerr)
		}
	}
	return err
}

// enableUSBBridgeLocked shares the upstream WiFi connection with the USB
// downstream interface. It does not need AP+STA concurrency: wlan0 is a
// station and usb0 stays the LAN interface.
func (m *Manager) enableUSBBridgeLocked(ctx context.Context) error {
	cfg := m.Config()
	if m.CellularDataActive(ctx) {
		return fmt.Errorf("蜂窝数据已开启，请先关闭流量再启用 WiFi 转 USB 共享")
	}
	if strings.TrimSpace(cfg.Wifi.Bridge.UpstreamSSID) == "" {
		return fmt.Errorf("请先填写要连接的上游 WiFi 名称")
	}
	upstream := cfg.Wifi.Interface
	downstream := cfg.Wifi.Bridge.DownstreamInterface
	if downstream == "" {
		downstream = "usb0"
	}
	if upstream == "" || downstream == upstream {
		return fmt.Errorf("上游无线网卡和下游接口不能相同")
	}
	if !m.Available("nmcli") {
		return fmt.Errorf("WiFi 转 USB 共享需要 NetworkManager（nmcli）")
	}

	// The downstream USB interface normally already has an address from
	// NetworkManager. Do not tear down the working hotspot if it is missing.
	if m.ifaceIPv4(ctx, downstream) == "" {
		_, _ = m.Run(ctx, 30*time.Second, "nmcli", "device", "connect", downstream)
	}
	if m.ifaceIPv4(ctx, downstream) == "" {
		return fmt.Errorf("下游接口 %s 没有 IPv4 地址", downstream)
	}

	m.log.Printf("USB 共享: 上游=%s 下游=%s", upstream, downstream)

	// Remember the normal AP connection so it can be restored later.
	if cfg.Wifi.Bridge.PreviousAPConnection == "" {
		if prev := m.activeConnectionFor(ctx, upstream); prev != "" {
			_ = m.Update(func(c *config.Config) error {
				c.Wifi.Bridge.PreviousAPConnection = prev
				return nil
			})
			cfg = m.Config()
		}
	}
	if m.bridge.Active {
		m.teardownBridgeLocked(ctx, cfg)
	}
	m.stopStandaloneAP(ctx)
	prev := cfg.Wifi.Bridge.PreviousAPConnection
	if prev != "" {
		_, _ = m.Run(ctx, 10*time.Second, "nmcli", "connection", "modify", prev, "connection.autoconnect", "no")
		_, _ = m.Run(ctx, 20*time.Second, "nmcli", "connection", "down", prev)
	} else if active := m.activeConnectionFor(ctx, upstream); active != "" {
		_, _ = m.Run(ctx, 20*time.Second, "nmcli", "connection", "down", active)
	}
	_, _ = m.Run(ctx, 8*time.Second, "nmcli", "device", "set", upstream, "managed", "yes")

	if err := m.connectUpstream(ctx, cfg, upstream); err != nil {
		m.teardownBridgeLocked(ctx, cfg)
		return err
	}
	if ip := m.ifaceIPv4(ctx, upstream); ip == "" {
		m.teardownBridgeLocked(ctx, cfg)
		return fmt.Errorf("上游 WiFi 已连接但没有获得 IPv4 地址，请检查上游网络")
	}

	_, _ = m.Run(ctx, 8*time.Second, "sysctl", "-w", "net.ipv4.ip_forward=1")
	if err := m.applyAPNAT(ctx, downstream, upstream); err != nil {
		m.teardownBridgeLocked(ctx, cfg)
		return fmt.Errorf("配置 USB 共享 NAT 失败: %w", err)
	}

	// The system dnsmasq instance serves DHCP/DNS on the USB LAN. It is
	// independent of the AP's dnsmasq and must stay running in this mode.
	if m.Available("systemctl") && m.unitExists(ctx, "dnsmasq.service") && !m.serviceActive(ctx, "dnsmasq.service") {
		if err := m.serviceAction(ctx, "start", "dnsmasq.service"); err != nil {
			m.log.Printf("启动 dnsmasq.service 失败: %v", err)
		}
	}

	m.bridge = bridgeRuntime{
		Active:        true,
		Mode:          "usb",
		APInterface:   downstream,
		UpstreamIface: upstream,
		UpstreamSSID:  cfg.Wifi.Bridge.UpstreamSSID,
		StartedAt:     time.Now(),
	}
	if err := m.Update(func(c *config.Config) error {
		c.Wifi.Bridge.Enabled = true
		return nil
	}); err != nil {
		m.log.Printf("保存 USB 共享状态失败: %v", err)
	}
	m.log.Printf("USB 共享已启用: %s -> %s", downstream, upstream)
	return nil
}

func (m *Manager) enableBridgeLocked(ctx context.Context) error {
	cfg := m.Config()
	// Cellular data takes precedence over the relay: never start the bridge
	// while the modem has an active data connection.
	if m.CellularDataActive(ctx) {
		return fmt.Errorf("蜂窝数据已开启，请先关闭流量再启用桥接")
	}
	if strings.TrimSpace(cfg.Wifi.Bridge.UpstreamSSID) == "" {
		return fmt.Errorf("请先填写要连接的上游 WiFi 名称")
	}
	if cfg.Wifi.Interface == "" {
		return fmt.Errorf("未配置无线网卡")
	}
	if !m.Available("iw") {
		return fmt.Errorf("未安装 iw，无法创建虚拟 AP 接口")
	}
	if !m.Available("nmcli") {
		return fmt.Errorf("桥接需要 NetworkManager（nmcli）来连接上游 WiFi")
	}

	caps := m.BridgeCapability(ctx)
	apIface, external, apErr := m.resolveAPInterface(cfg, caps)
	if apErr != nil {
		return apErr
	}
	if !external && !caps.Supported && !cfg.Wifi.Bridge.Force {
		return fmt.Errorf("%s；如确认驱动可用，请在桥接页面勾选“强制启用”", caps.Reason)
	}
	upstream := cfg.Wifi.Interface
	m.log.Printf("桥接: 上游=%s AP=%s 外部网卡=%v", upstream, apIface, external)

	// Remember the normal AP connection so it can be restored later.
	if cfg.Wifi.Bridge.PreviousAPConnection == "" {
		if prev := m.activeConnectionFor(ctx, upstream); prev != "" {
			_ = m.Update(func(c *config.Config) error {
				c.Wifi.Bridge.PreviousAPConnection = prev
				return nil
			})
			cfg = m.Config()
		}
	}

	// Re-applying: tear the running relay down first.
	if m.bridge.Active {
		m.teardownBridgeLocked(ctx, cfg)
	}
	m.bridge.APInterface = apIface
	m.bridge.UpstreamIface = upstream

	m.stopStandaloneAP(ctx)
	if prev := cfg.Wifi.Bridge.PreviousAPConnection; prev != "" {
		_, _ = m.Run(ctx, 20*time.Second, "nmcli", "connection", "down", prev)
	} else if active := m.activeConnectionFor(ctx, upstream); active != "" {
		_, _ = m.Run(ctx, 20*time.Second, "nmcli", "connection", "down", active)
	}
	_, _ = m.Run(ctx, 8*time.Second, "nmcli", "device", "set", upstream, "managed", "yes")

	if err := m.connectUpstream(ctx, cfg, upstream); err != nil {
		m.teardownBridgeLocked(ctx, cfg)
		return err
	}
	if ip := m.ifaceIPv4(ctx, upstream); ip == "" {
		m.teardownBridgeLocked(ctx, cfg)
		return fmt.Errorf("上游 WiFi 已连接但没有获得 IPv4 地址，请检查上游网络")
	}

	channel := m.upstreamChannel(ctx, upstream)
	if channel == 0 {
		channel = cfg.Wifi.Bridge.Channel
	}
	if channel == 0 {
		channel = cfg.Wifi.Channel
	}
	if channel == 0 {
		channel = 6
	}
	band := "bg"
	if channel > 14 {
		band = "a"
	}

	if err := m.prepareAPInterface(ctx, cfg, apIface, external); err != nil {
		m.teardownBridgeLocked(ctx, cfg)
		return err
	}
	prefix, err := netutil.MaskSize(cfg.LAN.Netmask)
	if err != nil {
		m.teardownBridgeLocked(ctx, cfg)
		return err
	}
	_, _ = m.Run(ctx, 8*time.Second, "ip", "addr", "flush", "dev", apIface, "scope", "global")
	if _, err := m.Run(ctx, 8*time.Second, "ip", "addr", "add", fmt.Sprintf("%s/%d", cfg.LAN.IP, prefix), "dev", apIface); err != nil {
		m.teardownBridgeLocked(ctx, cfg)
		return fmt.Errorf("设置 AP 地址失败: %w", err)
	}
	_, _ = m.Run(ctx, 8*time.Second, "ip", "link", "set", apIface, "up")

	conf := buildHostapdConfig(cfg, apIface, cfg.Wifi.SSID, cfg.Wifi.Password, cfg.Wifi.Security, band,
		channel, cfg.Wifi.Hidden, cfg.Wifi.MaxClients, cfg.Wifi.Country)
	if err := m.WriteFile(bridgeHostapdConf, []byte(conf), 0o600); err != nil {
		m.teardownBridgeLocked(ctx, cfg)
		return fmt.Errorf("写入 hostapd 配置失败: %w", err)
	}
	if err := m.WriteFile(bridgeDNSMasqConf, []byte(buildDNSMasqConfig(cfg, apIface, bridgeDNSMasqConf, bridgeLeaseFile)), 0o600); err != nil {
		m.teardownBridgeLocked(ctx, cfg)
		return fmt.Errorf("写入 dnsmasq 配置失败: %w", err)
	}
	_, _ = m.Run(ctx, 8*time.Second, "sysctl", "-w", "net.ipv4.ip_forward=1")
	if err := m.applyBridgeNAT(ctx, apIface, upstream); err != nil {
		m.teardownBridgeLocked(ctx, cfg)
		return err
	}
	if err := m.startBridgeAP(ctx); err != nil {
		m.teardownBridgeLocked(ctx, cfg)
		return err
	}
	if !sleepCtx(ctx, 2*time.Second) {
		m.teardownBridgeLocked(ctx, cfg)
		return ctx.Err()
	}
	if !m.bridgeAPRunning(ctx) {
		m.teardownBridgeLocked(ctx, cfg)
		return fmt.Errorf("hostapd 启动失败，桥接已回滚")
	}

	m.bridge = bridgeRuntime{
		Active:        true,
		Mode:          "ap_sta",
		APInterface:   apIface,
		UpstreamIface: upstream,
		UpstreamSSID:  cfg.Wifi.Bridge.UpstreamSSID,
		Channel:       channel,
		StartedAt:     time.Now(),
	}
	if err := m.Update(func(c *config.Config) error {
		c.Wifi.Bridge.Enabled = true
		c.Wifi.Bridge.APInterface = apIface
		return nil
	}); err != nil {
		m.log.Printf("保存桥接状态失败: %v", err)
	}
	if err := m.syncBlockedSet(ctx); err != nil {
		m.log.Printf("同步 MAC 黑名单失败: %v", err)
	}
	m.log.Printf("桥接已启用: %s -> %s 信道 %d", apIface, upstream, channel)
	return nil
}

func (m *Manager) resolveAPInterface(cfg *config.Config, caps BridgeCapability) (string, bool, error) {
	upstream := cfg.Wifi.Interface
	requested := strings.TrimSpace(cfg.Wifi.Bridge.APInterface)
	if requested == "auto" || requested == "" {
		if caps.ExternalAP != "" {
			return caps.ExternalAP, true, nil
		}
		return "uap0", false, nil
	}
	if requested == upstream {
		return "", false, fmt.Errorf("桥接 AP 网卡不能与上游网卡相同")
	}
	if !ifaceNameRe.MatchString(requested) {
		return "", false, fmt.Errorf("桥接 AP 网卡名称无效")
	}
	if requested == "uap0" {
		return "uap0", false, nil
	}
	return requested, true, nil
}

func (m *Manager) activeConnectionFor(ctx context.Context, iface string) string {
	out, err := m.Run(ctx, 8*time.Second, "nmcli", "-t", "-f", "NAME,DEVICE", "connection", "show", "--active")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		f := parse.SplitTerse(line)
		if len(f) >= 2 && f[1] == iface && f[0] != "" {
			return f[0]
		}
	}
	return ""
}

func (m *Manager) connectUpstream(ctx context.Context, cfg *config.Config, upstream string) error {
	name := cfg.Wifi.Bridge.UpstreamConnection
	if name == "" {
		name = "uz801d-upstream"
	}
	_, _ = m.Run(ctx, 12*time.Second, "nmcli", "connection", "delete", name)
	if _, err := m.Run(ctx, 20*time.Second, "nmcli", "connection", "add", "type", "wifi",
		"ifname", upstream, "con-name", name, "ssid", cfg.Wifi.Bridge.UpstreamSSID); err != nil {
		return fmt.Errorf("创建上游 WiFi 连接失败: %w", err)
	}
	args := []string{"connection", "modify", name,
		"connection.autoconnect", "yes",
		"connection.autoconnect-priority", "100",
		"802-11-wireless.mode", "infrastructure",
		"802-11-wireless.ssid", cfg.Wifi.Bridge.UpstreamSSID,
		"802-11-wireless.hidden", boolYesNo(cfg.Wifi.Bridge.UpstreamHidden),
		"ipv4.method", "auto",
		"ipv4.route-metric", fmt.Sprintf("%d", cfg.Wifi.Bridge.RouteMetric),
		"ipv6.method", "ignore",
	}
	sec := cfg.Wifi.Bridge.UpstreamSecurity
	pass := cfg.Wifi.Bridge.UpstreamPassword
	switch {
	case sec == "open" || pass == "":
		args = append(args, "802-11-wireless-security.key-mgmt", "none")
	case sec == "wpa3":
		args = append(args, "802-11-wireless-security.key-mgmt", "sae",
			"802-11-wireless-security.psk", pass)
	default:
		args = append(args, "802-11-wireless-security.key-mgmt", "wpa-psk",
			"802-11-wireless-security.psk", pass)
	}
	if _, err := m.Run(ctx, 20*time.Second, "nmcli", args...); err != nil {
		return fmt.Errorf("配置上游 WiFi 失败: %w", err)
	}
	if _, err := m.Run(ctx, 75*time.Second, "nmcli", "connection", "up", name); err != nil {
		return fmt.Errorf("连接上游 WiFi 失败: %w", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if m.ifaceIPv4(ctx, upstream) != "" {
			return nil
		}
		if !sleepCtx(ctx, 1*time.Second) {
			return ctx.Err()
		}
	}
	return nil
}

func (m *Manager) ifaceIPv4(ctx context.Context, iface string) string {
	out, err := m.Run(ctx, 4*time.Second, "ip", "-o", "-4", "addr", "show", "dev", iface)
	if err != nil {
		return ""
	}
	addrs := parse.ParseIPAddr(out)
	if len(addrs) == 0 {
		return ""
	}
	return fmt.Sprintf("%s/%d", addrs[0].Address, addrs[0].Prefix)
}

func (m *Manager) upstreamChannel(ctx context.Context, iface string) int {
	info := parse.ParseIWInfo(m.RunTry(ctx, 4*time.Second, "iw", "dev", iface, "info"))
	return info.Channel
}

func (m *Manager) prepareAPInterface(ctx context.Context, cfg *config.Config, apIface string, external bool) error {
	if m.Available("nmcli") {
		_, _ = m.Run(ctx, 8*time.Second, "nmcli", "device", "set", apIface, "managed", "no")
	}
	if external {
		_, _ = m.Run(ctx, 8*time.Second, "ip", "link", "set", apIface, "down")
		_, _ = m.Run(ctx, 8*time.Second, "ip", "link", "set", apIface, "up")
		return nil
	}
	// Virtual AP on the same physical radio.
	_, _ = m.Run(ctx, 8*time.Second, "ip", "link", "set", apIface, "down")
	_, _ = m.Run(ctx, 8*time.Second, "iw", "dev", apIface, "del")
	_, _ = m.Run(ctx, 8*time.Second, "ip", "link", "delete", apIface)
	if _, err := m.Run(ctx, 10*time.Second, "iw", "dev", cfg.Wifi.Interface, "interface", "add", apIface, "type", "__ap"); err != nil {
		return fmt.Errorf("创建虚拟 AP 接口 %s 失败（驱动可能不支持 AP+STA 并发）: %w", apIface, err)
	}
	// 有些驱动（MSM8916 的 wcn36xx）会接受请求但静默建成 managed 接口，
	// 之后 hostapd 只会报一句难懂的 "Could not set interface ... flags (UP):
	// Device or resource busy"。这里提前抓出来。
	if info, err := m.Run(ctx, 5*time.Second, "iw", "dev", apIface, "info"); err == nil {
		if !strings.Contains(info, "type AP") {
			_, _ = m.Run(ctx, 5*time.Second, "iw", "dev", apIface, "del")
			return fmt.Errorf("驱动把 %s 建成了非 AP 接口（wcn36xx 单 vif 限制，AP+STA 不可用）；请改用「WiFi 转 USB 共享」模式或外接 USB 无线网卡", apIface)
		}
	}
	if _, err := m.Run(ctx, 8*time.Second, "ip", "link", "set", apIface, "up"); err != nil {
		return fmt.Errorf("启用 AP 接口失败: %w", err)
	}
	return nil
}

func (m *Manager) startBridgeAP(ctx context.Context) error {
	m.stopBridgeServices(ctx)
	m.stopConflictingServices(ctx)
	if m.unitExists(ctx, bridgeAPUnit) {
		if err := m.serviceAction(ctx, "restart", bridgeAPUnit); err != nil {
			return fmt.Errorf("启动 uz801d-ap.service 失败: %w", err)
		}
		if m.unitExists(ctx, bridgeDHCPUnit) {
			if err := m.serviceAction(ctx, "restart", bridgeDHCPUnit); err != nil {
				_ = m.serviceAction(ctx, "stop", bridgeAPUnit)
				return fmt.Errorf("启动 uz801d-dhcp.service 失败: %w", err)
			}
		}
		return nil
	}
	if !m.Available("hostapd") {
		return fmt.Errorf("未安装 hostapd")
	}
	if _, err := m.Run(ctx, 20*time.Second, "hostapd", "-B", "-P", bridgeHostapdPID, bridgeHostapdConf); err != nil {
		return fmt.Errorf("启动 hostapd 失败: %w", err)
	}
	if m.Available("dnsmasq") {
		if _, err := m.Run(ctx, 15*time.Second, "dnsmasq", "--conf-file="+bridgeDNSMasqConf, "--pid-file="+bridgeDHCPPID); err != nil {
			KillPIDFile(ctx, m.runner, bridgeHostapdPID)
			return fmt.Errorf("启动 dnsmasq 失败: %w", err)
		}
	}
	return nil
}

func (m *Manager) stopBridgeServices(ctx context.Context) {
	if m.Available("systemctl") {
		if m.unitExists(ctx, bridgeDHCPUnit) {
			_ = m.serviceAction(ctx, "stop", bridgeDHCPUnit)
		}
		if m.unitExists(ctx, bridgeAPUnit) {
			_ = m.serviceAction(ctx, "stop", bridgeAPUnit)
		}
	}
	KillPIDFile(ctx, m.runner, bridgeHostapdPID)
	KillPIDFile(ctx, m.runner, bridgeDHCPPID)
}

func (m *Manager) bridgeAPRunning(ctx context.Context) bool {
	if m.unitExists(ctx, bridgeAPUnit) {
		return m.serviceActive(ctx, bridgeAPUnit)
	}
	return PIDRunning(bridgeHostapdPID)
}

// DisableBridge stops the relay and restores the previous access point.
func (m *Manager) DisableBridge(ctx context.Context) error {
	return m.disableBridge(ctx, true)
}

// disableBridge stops the relay. When restore is false the previous access
// point is left down so the caller can shut the radio down completely instead
// of starting an AP that would be stopped again immediately.
func (m *Manager) disableBridge(ctx context.Context, restore bool) error {
	m.bridgeMu.Lock()
	cfg := m.Config().Clone()
	m.teardownBridgeLocked(ctx, cfg)
	_ = m.Update(func(c *config.Config) error {
		c.Wifi.Bridge.Enabled = false
		return nil
	})
	m.bridgeMu.Unlock()
	if !restore {
		return nil
	}
	var err error
	if m.Config().Wifi.Backend != "none" {
		err = m.restoreNormalAP(ctx, cfg)
	}
	if err == nil {
		m.log.Printf("桥接已停用，正常热点已恢复")
	}
	return err
}

// teardownBridgeLocked must be called with bridgeMu held.
func (m *Manager) teardownBridgeLocked(ctx context.Context, cfg *config.Config) {
	br := m.bridge
	usbMode := br.Mode == "usb" || (br.Mode == "" && cfg.Wifi.Bridge.Mode == "usb")
	m.stopBridgeServices(ctx)
	m.nftDelete(ctx, nftBridgeTable)
	if cfg.Wifi.Bridge.UpstreamConnection != "" {
		_, _ = m.Run(ctx, 10*time.Second, "nmcli", "connection", "modify",
			cfg.Wifi.Bridge.UpstreamConnection, "connection.autoconnect", "no")
		_, _ = m.Run(ctx, 20*time.Second, "nmcli", "connection", "down", cfg.Wifi.Bridge.UpstreamConnection)
	}
	if usbMode {
		// usb0 is the LAN side in USB sharing mode; never flush its address.
		m.bridge = bridgeRuntime{LastError: br.LastError}
		return
	}
	apIface := br.APInterface
	if apIface == "" {
		apIface = cfg.Wifi.Bridge.APInterface
	}
	if apIface != "" && apIface != cfg.Wifi.Interface {
		_, _ = m.Run(ctx, 5*time.Second, "ip", "addr", "flush", "dev", apIface, "scope", "global")
	}
	if apIface == "uap0" {
		_, _ = m.Run(ctx, 5*time.Second, "ip", "link", "set", "uap0", "down")
		_, _ = m.Run(ctx, 5*time.Second, "iw", "dev", "uap0", "del")
		_, _ = m.Run(ctx, 5*time.Second, "ip", "link", "delete", "uap0")
	}
	m.bridge = bridgeRuntime{LastError: br.LastError}
}

func (m *Manager) restoreNormalAP(ctx context.Context, cfg *config.Config) error {
	prev := cfg.Wifi.Bridge.PreviousAPConnection
	if prev != "" && m.Available("nmcli") {
		_, _ = m.Run(ctx, 10*time.Second, "nmcli", "connection", "modify", prev, "connection.autoconnect", "yes")
		if _, err := m.Run(ctx, 45*time.Second, "nmcli", "connection", "up", prev); err == nil {
			// Switching back to the NM AP profile does not recreate the
			// standalone NAT/MAC-filter rules removed above, so re-apply them.
			if err := m.applyAPNAT(ctx, cfg.Wifi.Interface, m.defaultRouteIface(ctx)); err != nil {
				m.log.Printf("恢复热点后应用 NAT 失败: %v", err)
			}
			if err := m.syncBlockedSet(ctx); err != nil {
				m.log.Printf("恢复热点后同步 MAC 黑名单失败: %v", err)
			}
			return nil
		} else {
			m.log.Printf("恢复原热点连接 %s 失败: %v", prev, err)
		}
	}
	if cfg.Wifi.Backend == "none" {
		return nil
	}
	return m.ApplyWifi(ctx)
}

// resumeBridge restores a relay that was enabled before the last shutdown.
func (m *Manager) resumeBridge(ctx context.Context) {
	// Let NetworkManager and ModemManager settle after boot.
	if !m.slept(10 * time.Second) {
		return
	}
	cfg := m.Config()
	if !cfg.Wifi.Bridge.Enabled {
		return
	}
	if strings.TrimSpace(cfg.Wifi.Bridge.UpstreamSSID) == "" {
		m.log.Printf("上次启用的 WiFi 桥接缺少上游 SSID，跳过恢复")
		_ = m.Update(func(c *config.Config) error {
			c.Wifi.Bridge.Enabled = false
			return nil
		})
		return
	}
	if m.CellularDataActive(ctx) {
		m.log.Printf("蜂窝数据已启用，不恢复 WiFi 桥接")
		_ = m.Update(func(c *config.Config) error {
			c.Wifi.Bridge.Enabled = false
			return nil
		})
		return
	}
	m.log.Printf("恢复上次启用的 WiFi 桥接（上游 %s）", cfg.Wifi.Bridge.UpstreamSSID)
	if err := m.EnableBridge(ctx); err != nil {
		m.log.Printf("恢复 WiFi 桥接失败: %v", err)
	}
}

// autoStep enables the relay while cellular data is off and always stops it
// when cellular data becomes active. A failed automatic start backs off for
// bridgeAutoRetryDelay instead of retrying on every tick.
func (m *Manager) autoStep(ctx context.Context) {
	m.autoMu.Lock()
	defer m.autoMu.Unlock()
	cfg := m.Config()
	dataActive := m.CellularDataActive(ctx)
	active := m.BridgeActive()

	// Cellular data always wins over the relay.
	if dataActive {
		if active {
			m.log.Printf("检测到蜂窝数据已启用，自动停用 WiFi 桥接")
			if err := m.DisableBridge(ctx); err != nil {
				m.log.Printf("自动停用桥接失败: %v", err)
			}
		}
		return
	}

	// The operator explicitly stopped the hotspot; do not re-enable the
	// bridge until the AP is started again (restart/save/start bridge).
	if m.autoSuppressed {
		return
	}

	if !cfg.Wifi.Bridge.Auto {
		return
	}
	if strings.TrimSpace(cfg.Wifi.Bridge.UpstreamSSID) == "" {
		return
	}
	if !active {
		if time.Now().Before(m.bridgeRetryAfter) {
			return
		}
		m.log.Printf("蜂窝数据未启用，自动启用 WiFi 桥接")
		if err := m.EnableBridge(ctx); err != nil {
			m.log.Printf("自动启用桥接失败: %v，%s 后重试", err, bridgeAutoRetryDelay)
			m.bridgeRetryAfter = time.Now().Add(bridgeAutoRetryDelay)
		}
	}
}

// AllowBridgeRetry clears the automatic back-off so a manual start is
// attempted immediately, even right after a failed automatic start.
func (m *Manager) AllowBridgeRetry() {
	m.autoMu.Lock()
	m.bridgeRetryAfter = time.Time{}
	m.autoMu.Unlock()
}

// SuppressAutoBridge pauses automatic bridge/hotspot starts after the operator
// explicitly stops the access point.
func (m *Manager) SuppressAutoBridge() {
	m.autoMu.Lock()
	m.autoSuppressed = true
	m.autoMu.Unlock()
}

// ResumeAutoBridge re-enables the automatic state machine when the AP is
// started again.
func (m *Manager) ResumeAutoBridge() {
	m.autoMu.Lock()
	m.autoSuppressed = false
	m.autoMu.Unlock()
}

func (m *Manager) watchdogStep(ctx context.Context) {
	m.autoMu.Lock()
	defer m.autoMu.Unlock()
	cfg := m.Config()
	br := m.BridgeSnapshot()
	if !br.Active {
		return
	}
	if br.Mode != "usb" {
		if !m.bridgeAPRunning(ctx) {
			m.log.Printf("检测到桥接 AP 已退出，尝试重启")
			if err := m.startBridgeAP(ctx); err != nil {
				m.bridgeMu.Lock()
				m.bridge.LastError = err.Error()
				m.bridgeMu.Unlock()
			}
		}
	} else if br.APInterface != "" && m.ifaceIPv4(ctx, br.APInterface) == "" {
		m.log.Printf("USB 下游接口 %s 没有地址，尝试重新连接", br.APInterface)
		_, _ = m.Run(ctx, 30*time.Second, "nmcli", "device", "connect", br.APInterface)
	}
	if br.UpstreamIface == "" {
		return
	}
	if m.ifaceIPv4(ctx, br.UpstreamIface) != "" {
		m.bridgeMu.Lock()
		m.bridge.UpstreamDownSince = time.Time{}
		m.bridgeMu.Unlock()
		return
	}

	now := time.Now()
	m.bridgeMu.Lock()
	if m.bridge.UpstreamDownSince.IsZero() {
		m.bridge.UpstreamDownSince = now
	}
	downFor := now.Sub(m.bridge.UpstreamDownSince)
	m.bridgeMu.Unlock()

	// Keep the relay up and keep retrying the upstream connection however long
	// it has been down; only the operator or cellular data may stop the bridge.
	m.log.Printf("上游 WiFi 无地址（已 %s），尝试重新连接", downFor.Round(time.Second))
	_, _ = m.Run(ctx, 45*time.Second, "nmcli", "connection", "up", cfg.Wifi.Bridge.UpstreamConnection)
}

// PortalProbe is the result of checking whether the upstream WiFi is behind
// a captive portal.
type PortalProbe struct {
	Online     bool   `json:"online"`
	PortalURL  string `json:"portal_url,omitempty"`
	Status     int    `json:"status,omitempty"`
	CheckedURL string `json:"checked_url,omitempty"`
	Error      string `json:"error,omitempty"`
}

// ProbePortal performs a small HTTP connectivity check through the current
// default route and reports the redirect target when a captive portal is
// present.
func (m *Manager) ProbePortal(ctx context.Context) PortalProbe {
	cfg := m.Config()
	if !m.BridgeActive() || (cfg.Wifi.Bridge.Mode != "" && cfg.Wifi.Bridge.Mode != "usb") {
		return PortalProbe{Error: "请先启用 WiFi 共享"}
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	client := &http.Client{
		Timeout: 8 * time.Second,
		Transport: &http.Transport{
			Proxy: nil,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				// Captive portals are usually IPv4-only.
				return dialer.DialContext(ctx, "tcp4", addr)
			},
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	probe := func(rawURL string) PortalProbe {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return PortalProbe{CheckedURL: rawURL, Error: err.Error()}
		}
		req.Header.Set("User-Agent", "uz801d-portal-probe")
		resp, err := client.Do(req)
		if err != nil {
			return PortalProbe{CheckedURL: rawURL, Error: err.Error()}
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		res := PortalProbe{CheckedURL: rawURL, Status: resp.StatusCode}
		if resp.StatusCode == http.StatusNoContent {
			res.Online = true
			return res
		}
		if loc, err := resp.Location(); err == nil && loc.String() != "" {
			res.PortalURL = loc.String()
			return res
		}
		// Apple's hotspot probe returns "Success" in the body when online.
		if resp.StatusCode == http.StatusOK && strings.Contains(strings.ToLower(string(body)), "success") {
			res.Online = true
			return res
		}
		res.PortalURL = resp.Request.URL.String()
		return res
	}
	res := probe("http://connectivitycheck.gstatic.com/generate_204")
	if res.Error != "" {
		// Chinese networks sometimes block Google's probe; try a domestic one.
		res = probe("http://connectivitycheck.platform.hicloud.com/generate_204")
	}
	if res.Error != "" {
		res = probe("http://captive.apple.com/hotspot-detect.html")
	}
	return res
}

// BridgeScan scans for upstream networks using the configured radio.
func (m *Manager) BridgeScan(ctx context.Context) ([]parse.WifiNetwork, error) {
	return m.ScanWifi(ctx)
}

// BridgeConfigPatch replaces the relay settings without starting it.
func (m *Manager) BridgeConfigPatch(patch config.BridgeConfig) error {
	return m.Update(func(c *config.Config) error {
		c.Wifi.Bridge = patch
		return nil
	})
}
