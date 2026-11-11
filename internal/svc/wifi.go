package svc

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"uz801d/internal/config"
	"uz801d/internal/netutil"
	"uz801d/internal/parse"
)

const (
	apHostapdPID = "/run/uz801d-hostapd.pid"
	apDHCPPID    = "/run/uz801d-dnsmasq.pid"
)

// WifiStatus describes the access point in use.
type WifiStatus struct {
	Backend     string `json:"backend"`
	Interface   string `json:"interface"`
	APInterface string `json:"ap_interface"`
	Up          bool   `json:"up"`
	Active      bool   `json:"active"`
	SSID        string `json:"ssid"`
	Channel     int    `json:"channel"`
	Clients     int    `json:"clients"`
	Security    string `json:"security"`
	Hidden      bool   `json:"hidden"`
	Bridge      bool   `json:"bridge"`
	State       string `json:"state"`
}

// WifiStatus gathers the current access point state.
func (m *Manager) WifiStatus(ctx context.Context) *WifiStatus {
	cfg := m.Config()
	st := &WifiStatus{
		Backend:     cfg.Wifi.Backend,
		Interface:   cfg.Wifi.Interface,
		APInterface: cfg.Wifi.Interface,
		SSID:        cfg.Wifi.SSID,
		Channel:     cfg.Wifi.Channel,
		Security:    cfg.Wifi.Security,
		Hidden:      cfg.Wifi.Hidden,
	}
	br := m.BridgeSnapshot()
	if br.Active {
		st.Bridge = true
		st.APInterface = br.APInterface
		st.Channel = br.Channel
		st.Backend = "hostapd"
	}
	if st.APInterface == "" {
		return st
	}
	ifm := parse.ParseIWInfo(m.RunTry(ctx, 3*time.Second, "iw", "dev", st.APInterface, "info"))
	if ifm.Interface != "" {
		st.Up = true
		if ifm.SSID != "" {
			st.SSID = ifm.SSID
		}
		if ifm.Channel > 0 {
			st.Channel = ifm.Channel
		}
	}
	if out, err := m.Run(ctx, 3*time.Second, "ip", "link", "show", "dev", st.APInterface); err == nil {
		st.Up = strings.Contains(out, "state UP") || strings.Contains(out, "state UNKNOWN")
	}
	if out, err := m.Run(ctx, 4*time.Second, "iw", "dev", st.APInterface, "station", "dump"); err == nil {
		st.Clients = len(parse.ParseStations(out))
	}
	if m.Available("nmcli") {
		out, err := m.Run(ctx, 5*time.Second, "nmcli", "-t", "-f", "DEVICE,TYPE,STATE,CONNECTION", "device", "status")
		if err == nil {
			if entry, ok := parse.ParseNMDeviceStatus(out)[st.APInterface]; ok {
				st.State = entry[1]
				if entry[2] != "" && entry[2] != "--" {
					st.Active = true
				}
			}
		}
	}
	// The hostapd backend is not tracked by NetworkManager, so a running
	// hostapd instance is the authority there.
	if !st.Active && PIDRunning(apHostapdPID) {
		st.Active = true
		if st.State == "" || st.State == "unmanaged" {
			st.State = "AP"
		}
		st.Backend = "hostapd"
	}
	if br.Active {
		st.Active = true
	}
	return st
}

// ScanWifi returns nearby networks visible by the wireless interface.
func (m *Manager) ScanWifi(ctx context.Context) ([]parse.WifiNetwork, error) {
	cfg := m.Config()
	if m.Available("nmcli") {
		args := []string{"-t", "-f", "SSID,SIGNAL,SECURITY,BSSID,CHAN", "device", "wifi", "list"}
		if cfg.Wifi.Interface != "" {
			args = append(args, "ifname", cfg.Wifi.Interface)
		}
		args = append(args, "--rescan", "yes")
		if out, err := m.Run(ctx, 35*time.Second, "nmcli", args...); err == nil {
			if list := parse.ParseWifiList(out); len(list) > 0 {
				return list, nil
			}
		}
	}
	if !m.Available("iw") {
		return nil, fmt.Errorf("未安装 nmcli 或 iw，无法扫描 WiFi")
	}
	out, err := m.Run(ctx, 30*time.Second, "iw", "dev", cfg.Wifi.Interface, "scan")
	if err != nil {
		return nil, fmt.Errorf("WiFi 扫描失败: %w", err)
	}
	list := parseIWScan(out)
	if len(list) == 0 {
		return nil, fmt.Errorf("未扫描到 WiFi 网络")
	}
	return list, nil
}

func parseIWScan(text string) []parse.WifiNetwork {
	bySSID := map[string]parse.WifiNetwork{}
	var cur *parse.WifiNetwork
	flush := func() {
		if cur != nil && cur.SSID != "" {
			if old, ok := bySSID[cur.SSID]; !ok || cur.Signal > old.Signal {
				bySSID[cur.SSID] = *cur
			}
		}
		cur = nil
	}
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "BSS "):
			flush()
			cur = &parse.WifiNetwork{Security: "open"}
			if f := strings.Fields(line); len(f) > 1 {
				cur.BSSID = strings.ToLower(f[1])
			}
		case cur == nil:
			continue
		case strings.HasPrefix(line, "SSID:"):
			cur.SSID = strings.TrimSpace(strings.TrimPrefix(line, "SSID:"))
		case strings.HasPrefix(line, "signal:"):
			if f := strings.Fields(strings.TrimPrefix(line, "signal:")); len(f) > 0 {
				if v, err := strconv.ParseFloat(f[0], 64); err == nil {
					// dBm -> approximate 0-100 percentage used by nmcli.
					cur.Signal = int((v + 100) * 2)
					if cur.Signal < 0 {
						cur.Signal = 0
					}
					if cur.Signal > 100 {
						cur.Signal = 100
					}
				}
			}
		case strings.Contains(line, "DS Parameter set: channel"):
			if f := strings.Fields(line); len(f) > 0 {
				cur.Channel, _ = strconv.Atoi(f[len(f)-1])
			}
		case strings.Contains(line, "RSN:"):
			cur.Security = "wpa2"
		case strings.Contains(line, "WPA:"):
			if cur.Security == "open" {
				cur.Security = "wpa2"
			}
		case strings.Contains(line, "Authentication suites: SAE"):
			cur.Security = "wpa3"
		}
	}
	flush()
	out := make([]parse.WifiNetwork, 0, len(bySSID))
	for _, n := range bySSID {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Signal > out[j].Signal })
	return out
}

// ApplyWifi writes and activates the configured access point.
func (m *Manager) ApplyWifi(ctx context.Context) error {
	cfg := m.Config()
	if m.BridgeSnapshot().Active {
		return m.EnableBridge(ctx)
	}
	if cfg.Wifi.Backend == "none" {
		return fmt.Errorf("WiFi 后端已禁用")
	}
	backend := cfg.Wifi.Backend
	if backend == "" || backend == "auto" {
		if m.Available("nmcli") {
			backend = "nmcli"
		} else {
			backend = "hostapd"
		}
	}
	if backend == "nmcli" {
		return m.applyWifiNM(ctx, cfg)
	}
	return m.applyWifiHostapd(ctx, cfg)
}

func (m *Manager) applyWifiNM(ctx context.Context, cfg *config.Config) error {
	if !m.Available("nmcli") {
		return fmt.Errorf("未安装 NetworkManager（nmcli）")
	}
	iface := cfg.Wifi.Interface
	conn := cfg.Wifi.Connection
	if conn == "" {
		conn = apNMConnection
	}
	m.stopStandaloneAP(ctx)
	_, _ = m.Run(ctx, 8*time.Second, "nmcli", "device", "set", iface, "managed", "yes")

	exists := false
	if out, err := m.Run(ctx, 8*time.Second, "nmcli", "-t", "-f", "NAME", "connection", "show"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			if parse.SplitTerse(line)[0] == conn {
				exists = true
				break
			}
		}
	}
	if !exists {
		if _, err := m.Run(ctx, 20*time.Second, "nmcli", "connection", "add", "type", "wifi",
			"ifname", iface, "con-name", conn, "ssid", cfg.Wifi.SSID); err != nil {
			return fmt.Errorf("创建热点连接失败: %w", err)
		}
	}
	prefix, err := netutil.MaskSize(cfg.LAN.Netmask)
	if err != nil {
		return err
	}
	args := []string{"connection", "modify", conn,
		"connection.autoconnect", "yes",
		"802-11-wireless.mode", "ap",
		"802-11-wireless.ssid", cfg.Wifi.SSID,
		"802-11-wireless.band", cfg.Wifi.Band,
		"802-11-wireless.hidden", boolYesNo(cfg.Wifi.Hidden),
		"ipv4.method", "shared",
		"ipv4.addresses", fmt.Sprintf("%s/%d", cfg.LAN.IP, prefix),
		"ipv4.dns", strings.Join(cfg.LAN.DNS, ","),
		"ipv6.method", "ignore",
	}
	if cfg.Wifi.Channel > 0 {
		args = append(args, "802-11-wireless.channel", strconv.Itoa(cfg.Wifi.Channel))
	} else {
		args = append(args, "802-11-wireless.channel", "0")
	}
	if cfg.Wifi.Security == "open" {
		args = append(args, "802-11-wireless-security.key-mgmt", "none")
	} else if cfg.Wifi.Security == "wpa3" {
		args = append(args, "802-11-wireless-security.key-mgmt", "sae",
			"802-11-wireless-security.psk", cfg.Wifi.Password)
	} else {
		args = append(args, "802-11-wireless-security.key-mgmt", "wpa-psk",
			"802-11-wireless-security.psk", cfg.Wifi.Password)
	}
	if _, err := m.Run(ctx, 20*time.Second, "nmcli", args...); err != nil {
		return fmt.Errorf("配置热点失败: %w", err)
	}
	// Older NetworkManager versions do not expose this property.
	_, _ = m.Run(ctx, 10*time.Second, "nmcli", "connection", "modify", conn,
		"ipv4.shared-dhcp-range", cfg.LAN.DHCPStart+" "+cfg.LAN.DHCPEnd)
	if _, err := m.Run(ctx, 60*time.Second, "nmcli", "connection", "up", conn); err != nil {
		return fmt.Errorf("启动热点失败: %w", err)
	}
	if err := m.applyAPNAT(ctx, iface, m.defaultRouteIface(ctx)); err != nil {
		// NetworkManager's "ipv4.method shared" already installs its own
		// NAT rules, so our own rule set is a best-effort addition here.
		m.log.Printf("配置热点 NAT 失败: %v", err)
	}
	if err := m.syncBlockedSet(ctx); err != nil {
		m.log.Printf("同步 MAC 黑名单失败: %v", err)
	}
	return nil
}

// stopConflictingServices stops distribution AP/DHCP daemons that would
// conflict with the instances started by uz801d. Best effort only.
func (m *Manager) stopConflictingServices(ctx context.Context) {
	if !m.Available("systemctl") {
		return
	}
	for _, unit := range []string{"hostapd.service", "dnsmasq.service"} {
		if m.serviceActive(ctx, unit) {
			m.log.Printf("停止系统 %s 以避免端口/网卡冲突", unit)
			_ = m.serviceAction(ctx, "stop", unit)
		}
	}
}

func (m *Manager) applyWifiHostapd(ctx context.Context, cfg *config.Config) error {
	if !m.Available("hostapd") {
		return fmt.Errorf("未安装 hostapd")
	}
	m.stopConflictingServices(ctx)
	iface := cfg.Wifi.Interface
	prefix, err := netutil.MaskSize(cfg.LAN.Netmask)
	if err != nil {
		return err
	}
	if m.Available("nmcli") {
		_, _ = m.Run(ctx, 8*time.Second, "nmcli", "device", "set", iface, "managed", "no")
	}
	m.stopStandaloneAP(ctx)
	conf := buildHostapdConfig(cfg, iface, cfg.Wifi.SSID, cfg.Wifi.Password, cfg.Wifi.Security, cfg.Wifi.Band,
		cfg.Wifi.Channel, cfg.Wifi.Hidden, cfg.Wifi.MaxClients, cfg.Wifi.Country)
	if err := m.WriteFile(cfg.Wifi.HostapdConfig, []byte(conf), 0o600); err != nil {
		return fmt.Errorf("写入 hostapd 配置失败: %w", err)
	}
	if err := m.WriteFile(cfg.Wifi.DNSMasqConfig, []byte(buildDNSMasqConfig(cfg, iface, cfg.Wifi.DNSMasqConfig, "/var/lib/misc/dnsmasq.leases")), 0o600); err != nil {
		return fmt.Errorf("写入 dnsmasq 配置失败: %w", err)
	}
	_, _ = m.Run(ctx, 8*time.Second, "ip", "addr", "flush", "dev", iface, "scope", "global")
	if _, err := m.Run(ctx, 8*time.Second, "ip", "addr", "add", fmt.Sprintf("%s/%d", cfg.LAN.IP, prefix), "dev", iface); err != nil {
		return fmt.Errorf("设置热点 IP 失败: %w", err)
	}
	_, _ = m.Run(ctx, 8*time.Second, "ip", "link", "set", iface, "up")
	_, _ = m.Run(ctx, 8*time.Second, "sysctl", "-w", "net.ipv4.ip_forward=1")
	if _, err := m.Run(ctx, 15*time.Second, "hostapd", "-B", "-P", apHostapdPID, cfg.Wifi.HostapdConfig); err != nil {
		return fmt.Errorf("启动 hostapd 失败: %w", err)
	}
	if m.Available("dnsmasq") {
		if _, err := m.Run(ctx, 15*time.Second, "dnsmasq", "--conf-file="+cfg.Wifi.DNSMasqConfig, "--pid-file="+apDHCPPID); err != nil {
			KillPIDFile(ctx, m.runner, apHostapdPID)
			return fmt.Errorf("启动 dnsmasq 失败: %w", err)
		}
	}
	if err := m.applyAPNAT(ctx, iface, m.defaultRouteIface(ctx)); err != nil {
		// Without NAT the AP advertises itself but clients cannot reach the
		// internet, so this must not be reported as a successful apply.
		KillPIDFile(ctx, m.runner, apHostapdPID)
		KillPIDFile(ctx, m.runner, apDHCPPID)
		return fmt.Errorf("配置热点 NAT 失败: %w", err)
	}
	if err := m.syncBlockedSet(ctx); err != nil {
		m.log.Printf("同步 MAC 黑名单失败: %v", err)
	}
	return nil
}

// StopWifi stops the access point. When the relay is running it is torn down
// without restoring the normal AP, because the caller explicitly wants the
// radio idle. The automatic state machine is paused until the AP is started
// again, otherwise it would bring the bridge or hotspot back within 30s.
func (m *Manager) StopWifi(ctx context.Context) error {
	cfg := m.Config()
	// Pause the automatic state machine first so it cannot re-enable the
	// bridge while the AP is being torn down.
	m.SuppressAutoBridge()
	apIface := cfg.Wifi.Interface
	if br := m.BridgeSnapshot(); br.Active {
		if br.APInterface != "" {
			apIface = br.APInterface
		}
		// Do not restore the normal AP here: stopStandaloneAP below would kill
		// it right away. Tear the relay down and continue with the stop path.
		if err := m.disableBridge(ctx, false); err != nil {
			return err
		}
	}
	conn := cfg.Wifi.Connection
	if conn == "" {
		conn = apNMConnection
	}
	if m.Available("nmcli") {
		// NetworkManager reconnects profiles with autoconnect enabled, so the
		// AP would be back within seconds and the stop would look like a no-op.
		_, _ = m.Run(ctx, 10*time.Second, "nmcli", "connection", "modify", conn, "connection.autoconnect", "no")
		// The active profile is not necessarily the configured name (the AP
		// may have been created outside this app), so stop whatever is active
		// on the AP interface as well.
		if out, err := m.Run(ctx, 5*time.Second, "nmcli", "-t", "-f", "NAME,DEVICE", "connection", "show", "--active"); err == nil {
			for _, line := range strings.Split(out, "\n") {
				f := parse.SplitTerse(line)
				if len(f) < 2 || f[0] == "" || f[1] != apIface {
					continue
				}
				if _, err := m.Run(ctx, 20*time.Second, "nmcli", "connection", "down", f[0]); err != nil {
					m.log.Printf("nmcli 断开 %s 失败: %v", f[0], err)
				}
			}
		}
		if _, err := m.Run(ctx, 20*time.Second, "nmcli", "connection", "down", conn); err != nil {
			// The connection may already be down; only treat this as fatal
			// if the verification below still sees it active.
			m.log.Printf("nmcli 停止热点连接 %s 失败: %v", conn, err)
		}
	}
	m.stopStandaloneAP(ctx)
	m.stopAPServices(ctx)
	m.nftDelete(ctx, nftBridgeTable)
	// Do not report success while the AP is still running. Checking pidfiles,
	// systemd units and NetworkManager connections is more reliable than the
	// interface type, which can legitimately stay "AP" after hostapd exits.
	if PIDRunning(apHostapdPID) {
		return fmt.Errorf("hostapd 仍在运行，停止热点失败")
	}
	if PIDRunning(apDHCPPID) {
		return fmt.Errorf("dnsmasq 仍在运行，停止热点失败")
	}
	if m.Available("systemctl") {
		for _, unit := range []string{"uz801d-ap.service", "uz801d-dhcp.service", "hostapd.service"} {
			if m.serviceActive(ctx, unit) {
				return fmt.Errorf("%s 仍在运行，停止热点失败", unit)
			}
		}
	}
	if m.Available("nmcli") {
		if out, err := m.Run(ctx, 5*time.Second, "nmcli", "-t", "-f", "NAME,DEVICE", "connection", "show", "--active"); err == nil {
			for _, line := range strings.Split(out, "\n") {
				f := parse.SplitTerse(line)
				if len(f) >= 2 && f[0] != "" && f[1] == apIface {
					return fmt.Errorf("热点连接 %s 仍处于活动状态，停止失败", f[0])
				}
			}
		}
	}
	return nil
}

// stopAPServices stops the distribution/relay units that keep an access point
// running. Best effort: failures are reported by the caller's verification.
func (m *Manager) stopAPServices(ctx context.Context) {
	if !m.Available("systemctl") {
		return
	}
	for _, unit := range []string{"uz801d-ap.service", "uz801d-dhcp.service", "hostapd.service", "dnsmasq.service"} {
		if m.serviceActive(ctx, unit) {
			m.log.Printf("停止 %s", unit)
			_ = m.serviceAction(ctx, "stop", unit)
		}
	}
}

func (m *Manager) stopStandaloneAP(ctx context.Context) {
	KillPIDFile(ctx, m.runner, apHostapdPID)
	KillPIDFile(ctx, m.runner, apDHCPPID)
	if m.Available("pkill") {
		_, _ = m.Run(ctx, 3*time.Second, "pkill", "-f", "hostapd .*uz801d")
	}
}

func (m *Manager) defaultRouteIface(ctx context.Context) string {
	out := m.RunTry(ctx, 5*time.Second, "ip", "route", "show", "default")
	_, iface := parse.ParseDefaultRoute(out)
	if iface == "" {
		iface = m.Config().Modem.Interface
	}
	return iface
}

func boolYesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

// buildHostapdConfig renders a hostapd.conf for the given AP interface.
func buildHostapdConfig(cfg *config.Config, iface, ssid, password, security, band string, channel int, hidden bool, maxClients int, country string) string {
	hwMode := "g"
	if band == "a" {
		hwMode = "a"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# generated by uz801d\n")
	fmt.Fprintf(&b, "interface=%s\n", iface)
	fmt.Fprintf(&b, "driver=nl80211\n")
	fmt.Fprintf(&b, "ctrl_interface=/var/run/hostapd\n")
	fmt.Fprintf(&b, "ctrl_interface_group=0\n")
	fmt.Fprintf(&b, "ssid=%s\n", ssid)
	fmt.Fprintf(&b, "hw_mode=%s\n", hwMode)
	if channel > 0 {
		fmt.Fprintf(&b, "channel=%d\n", channel)
	}
	if band != "a" {
		fmt.Fprintf(&b, "ieee80211n=1\n")
	} else {
		fmt.Fprintf(&b, "ieee80211n=1\nieee80211ac=1\n")
	}
	fmt.Fprintf(&b, "wmm_enabled=1\n")
	fmt.Fprintf(&b, "auth_algs=1\n")
	fmt.Fprintf(&b, "macaddr_acl=0\n")
	fmt.Fprintf(&b, "ignore_broadcast_ssid=%d\n", boolInt(hidden))
	if maxClients > 0 {
		fmt.Fprintf(&b, "max_num_sta=%d\n", maxClients)
	}
	if country != "" {
		fmt.Fprintf(&b, "country_code=%s\n", strings.ToUpper(country))
		fmt.Fprintf(&b, "ieee80211d=1\n")
	}
	switch security {
	case "open":
		fmt.Fprintf(&b, "wpa=0\n")
	case "wpa3":
		fmt.Fprintf(&b, "wpa=2\nwpa_key_mgmt=SAE\nwpa_pairwise=CCMP\nrsn_pairwise=CCMP\nieee80211w=2\nsae_require_mfp=1\n")
		fmt.Fprintf(&b, "wpa_passphrase=%s\n", password)
	default:
		fmt.Fprintf(&b, "wpa=2\nwpa_key_mgmt=WPA-PSK\nwpa_pairwise=CCMP\nrsn_pairwise=CCMP\n")
		fmt.Fprintf(&b, "wpa_passphrase=%s\n", password)
	}
	return b.String()
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// buildDNSMasqConfig renders a dnsmasq configuration for the AP interface.
func buildDNSMasqConfig(cfg *config.Config, iface, selfPath, leaseFile string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# generated by uz801d\n")
	fmt.Fprintf(&b, "interface=%s\n", iface)
	fmt.Fprintf(&b, "bind-dynamic\n")
	fmt.Fprintf(&b, "except-interface=lo\n")
	fmt.Fprintf(&b, "dhcp-range=%s,%s,%s,%s\n", cfg.LAN.DHCPStart, cfg.LAN.DHCPEnd, cfg.LAN.Netmask, cfg.LAN.LeaseTime)
	fmt.Fprintf(&b, "dhcp-option=3,%s\n", cfg.LAN.IP)
	fmt.Fprintf(&b, "dhcp-option=6,%s\n", strings.Join(cfg.LAN.DNS, ","))
	fmt.Fprintf(&b, "domain-needed\nbogus-priv\n")
	if leaseFile != "" {
		fmt.Fprintf(&b, "dhcp-leasefile=%s\n", leaseFile)
	}
	for _, dns := range cfg.LAN.DNS {
		fmt.Fprintf(&b, "server=%s\n", dns)
	}
	return b.String()
}

var ifaceNameRe = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,32}$`)
