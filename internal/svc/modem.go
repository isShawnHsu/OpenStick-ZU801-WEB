package svc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"uz801d/internal/config"
	"uz801d/internal/parse"
	"uz801d/internal/state"
)

// ModemStatus is the aggregated cellular state shown by the dashboard.
type ModemStatus struct {
	Service           bool               `json:"service"`
	Available         bool               `json:"available"`
	ModemFound        bool               `json:"modem_found"`
	SIMPresent        bool               `json:"sim_present"`
	Index             string             `json:"index"`
	Info              *parse.ModemInfo   `json:"info,omitempty"`
	Signal            parse.SignalValues `json:"signal"`
	SIM               *parse.SIMInfo     `json:"sim,omitempty"`
	IP                string             `json:"ip"`
	Gateway           string             `json:"gateway"`
	DNS               []string           `json:"dns"`
	Connected         bool               `json:"connected"`
	UnreadSMS         int                `json:"unread_sms"`
	Profile           string             `json:"profile"`
	ConnectionManager string             `json:"connection_manager"`
	FlightMode        bool               `json:"flight_mode"`
	Error             string             `json:"error,omitempty"`
}

// Modem presents the modem details.
func (m *Manager) ModemStatus(ctx context.Context) *ModemStatus {
	cfg := m.Config()
	st := &ModemStatus{Index: cfg.Modem.Index, FlightMode: cfg.Modem.FlightMode}
	if st.Index == "" {
		st.Index = "0"
	}
	st.ConnectionManager = m.modemManagerKind()
	if !m.Available("mmcli") {
		st.Error = "未安装 mmcli（ModemManager）"
		return st
	}
	st.Service = true
	idx, err := m.modemIndex(ctx)
	if err != nil {
		st.Error = "未检测到蜂窝模块"
		return st
	}
	st.Index = idx
	st.ModemFound = true
	info, err := m.modemInfo(ctx, idx)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Available = true
	st.Info = info
	st.Connected = modemConnected(info)
	st.Profile = cfg.Modem.ActiveProfile

	// Signal is optional; not every modem reports it while disconnected.
	if sig, err := m.modemSignal(ctx, idx); err == nil {
		st.Signal = sig
	}

	simIdx := simIndexFromPath(info.SimPath)
	if simIdx != "" {
		if sim, err := m.simInfo(ctx, simIdx); err == nil {
			st.SIM = sim
			st.SIMPresent = sim != nil
		} else {
			st.SIMPresent = info.SimPath != ""
		}
	}

	if cfg.Modem.Interface != "" {
		if out, err := m.Run(ctx, 5*time.Second, "ip", "-o", "-4", "addr", "show", "dev", cfg.Modem.Interface); err == nil {
			if addrs := parse.ParseIPAddr(out); len(addrs) > 0 {
				st.IP = fmt.Sprintf("%s/%d", addrs[0].Address, addrs[0].Prefix)
			}
		}
		if out, err := m.Run(ctx, 5*time.Second, "ip", "-4", "route", "show", "dev", cfg.Modem.Interface); err == nil {
			gw, _ := parse.ParseDefaultRoute(out)
			if gw == "" {
				for _, line := range strings.Split(out, "\n") {
					f := strings.Fields(line)
					if len(f) >= 3 && f[0] == "default" && f[1] == "via" {
						gw = f[2]
						break
					}
				}
			}
			st.Gateway = gw
		}
		if st.IP != "" {
			st.Connected = true
		}
	}
	if data, err := os.ReadFile("/etc/resolv.conf"); err == nil {
		st.DNS = parse.ParseResolvConf(string(data))
	}
	return st
}

// modemManagerKind decides whether mmcli or nmcli drives the data connection.
func (m *Manager) modemManagerKind() string {
	v := m.Config().Modem.ConnectionManager
	switch v {
	case "mmcli", "nmcli":
		return v
	}
	if m.Available("mmcli") {
		return "mmcli"
	}
	if m.Available("nmcli") {
		return "nmcli"
	}
	return "none"
}

func modemConnected(info *parse.ModemInfo) bool {
	if info == nil {
		return false
	}
	if strings.EqualFold(info.State, "connected") {
		return true
	}
	return strings.EqualFold(info.PacketService, "connected")
}

// modemIndex returns a working modem index, preferring the configured one.
func (m *Manager) modemIndex(ctx context.Context) (string, error) {
	want := m.Config().Modem.Index
	if want == "" {
		want = "0"
	}
	if _, err := m.modemInfo(ctx, want); err == nil {
		return want, nil
	}
	out, err := m.mmcliJSON(ctx, 10*time.Second, "-L")
	if err != nil {
		return "", err
	}
	paths, err := parse.ParseModemList(out)
	if err != nil || len(paths) == 0 {
		return "", fmt.Errorf("没有可用的调制解调器")
	}
	idx := pathIndex(paths[0])
	if idx == "" {
		idx = "0"
	}
	return idx, nil
}

func pathIndex(path string) string {
	re := regexp.MustCompile(`/(\d+)\s*$`)
	if mm := re.FindStringSubmatch(strings.TrimSpace(path)); mm != nil {
		return mm[1]
	}
	return ""
}

func simIndexFromPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return pathIndex(path)
}

func (m *Manager) mmcliJSON(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	out, err := m.Run(ctx, timeout, "mmcli", append(args, "-J")...)
	if err != nil {
		return nil, err
	}
	return []byte(out), nil
}

func (m *Manager) mmcli(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	return m.Run(ctx, timeout, "mmcli", args...)
}

func (m *Manager) modemInfo(ctx context.Context, idx string) (*parse.ModemInfo, error) {
	out, err := m.mmcliJSON(ctx, 10*time.Second, "-m", idx)
	if err != nil {
		return nil, err
	}
	info, err := parse.ParseModem(out)
	if err != nil {
		return nil, err
	}
	info.Index = idx
	return info, nil
}

func (m *Manager) modemSignal(ctx context.Context, idx string) (parse.SignalValues, error) {
	out, err := m.mmcliJSON(ctx, 10*time.Second, "-m", idx, "--signal-get")
	if err != nil {
		return parse.SignalValues{}, err
	}
	return parse.ParseSignal(out), nil
}

func (m *Manager) simInfo(ctx context.Context, idx string) (*parse.SIMInfo, error) {
	out, err := m.mmcliJSON(ctx, 10*time.Second, "-i", idx)
	if err != nil {
		return nil, err
	}
	return parse.ParseSIM(out)
}

// SIMPresent reports whether a SIM is currently inserted.
func (m *Manager) SIMPresent(ctx context.Context) bool {
	st := m.ModemStatus(ctx)
	return st.SIMPresent
}

// CellularDataActive reports whether the modem currently has an active data
// connection. SIM presence alone does not count: a SIM may be inserted while
// cellular data stays disconnected.
func (m *Manager) CellularDataActive(ctx context.Context) bool {
	if m.ModemStatus(ctx).Connected {
		return true
	}
	// Fallback for systems that drive the modem through NetworkManager without
	// a usable ModemManager: treat an IPv4 address on the modem interface as
	// an active data connection.
	iface := m.Config().Modem.Interface
	if iface == "" {
		return false
	}
	out, err := m.Run(ctx, 3*time.Second, "ip", "-4", "addr", "show", "dev", iface)
	return err == nil && strings.Contains(out, "inet ")
}

// reactToCellularState triggers the bridge state machine right after an
// explicit connect/disconnect instead of waiting for the next poll tick. The
// request context is detached because the HTTP handler returns immediately.
func (m *Manager) reactToCellularState(ctx context.Context) {
	go func() {
		// Give ModemManager a moment to report the new packet-service state.
		if !m.slept(5 * time.Second) {
			return
		}
		m.autoStep(context.WithoutCancel(ctx))
	}()
}

func (m *Manager) modemIndexOrErr(ctx context.Context) (string, error) {
	if !m.Available("mmcli") {
		return "", fmt.Errorf("未安装 mmcli（ModemManager），无法管理蜂窝模块")
	}
	idx, err := m.modemIndex(ctx)
	if err != nil {
		return "", fmt.Errorf("未检测到蜂窝模块，请检查 ModemManager 与硬件: %w", err)
	}
	return idx, nil
}

// FlightMode reports whether the cellular radio is intentionally disabled.
func (m *Manager) FlightMode() bool { return m.Config().Modem.FlightMode }

// SetFlightMode turns the cellular radio off (true) or back on (false). While
// flight mode is active the modem is powered down and only WiFi remains. On
// exit the modem is re-enabled and data is restored only when it was connected
// at the moment flight mode was entered.
func (m *Manager) SetFlightMode(ctx context.Context, enable bool) error {
	m.flightMu.Lock()
	defer m.flightMu.Unlock()

	cfg := m.Config()
	if enable == cfg.Modem.FlightMode {
		return nil
	}
	if enable {
		if m.noRadioTool() {
			return fmt.Errorf("未找到 nmcli、rfkill 或 mmcli，无法关闭蜂窝射频")
		}
		resume := m.CellularDataActive(ctx)
		if err := m.Update(func(c *config.Config) error {
			c.Modem.FlightMode = true
			c.Modem.FlightResumeData = resume
			return nil
		}); err != nil {
			return err
		}
		if resume {
			if err := m.DisconnectAPN(ctx); err != nil {
				m.log.Printf("进入飞行模式前断开蜂窝数据失败: %v", err)
			}
		}
		if err := m.cellularRadioOff(ctx); err != nil {
			// Do not claim flight mode is on when the radio could not be
			// powered down. Keep the state consistent so data can be retried.
			_ = m.Update(func(c *config.Config) error {
				c.Modem.FlightMode = false
				c.Modem.FlightResumeData = false
				return nil
			})
			return err
		}
		if resume {
			m.log.Printf("已开启飞行模式，蜂窝射频已关闭（退出后自动拨号）")
		} else {
			m.log.Printf("已开启飞行模式，蜂窝射频已关闭（退出后保持流量关闭）")
		}
		m.reactToCellularState(ctx)
		return nil
	}

	resume := cfg.Modem.FlightResumeData
	if err := m.Update(func(c *config.Config) error {
		c.Modem.FlightMode = false
		c.Modem.FlightResumeData = false
		return nil
	}); err != nil {
		return err
	}
	if err := m.cellularRadioOn(ctx, resume); err != nil {
		return fmt.Errorf("已退出飞行模式，但%v", err)
	}
	m.log.Printf("已退出飞行模式，蜂窝射频已恢复")
	if resume {
		if err := m.ConnectAPN(ctx, cfg.Modem.ActiveProfile); err != nil {
			return fmt.Errorf("已退出飞行模式，但自动拨号失败: %w", err)
		}
	} else {
		m.reactToCellularState(ctx)
	}
	return nil
}

// noRadioTool reports whether none of the tools that can power the WWAN radio
// down are installed. Without one, flight mode cannot actually do anything.
func (m *Manager) noRadioTool() bool {
	return !m.Available("nmcli") && !m.Available("rfkill") && !m.Available("mmcli")
}

// cellularRadioOff powers the WWAN radio down with every tool available. It
// returns an error when no tool exists or every attempted command failed, so
// callers never report flight mode as active without a real effect.
func (m *Manager) cellularRadioOff(ctx context.Context) error {
	attempted, succeeded := 0, 0
	var lastErr error
	record := func(err error, label string) {
		attempted++
		if err == nil {
			succeeded++
			return
		}
		lastErr = err
		m.log.Printf("%s 关闭蜂窝射频失败: %v", label, err)
	}
	if m.Available("nmcli") {
		_, err := m.Run(ctx, 15*time.Second, "nmcli", "radio", "wwan", "off")
		record(err, "nmcli")
	}
	if m.Available("rfkill") {
		_, err := m.Run(ctx, 10*time.Second, "rfkill", "block", "wwan")
		record(err, "rfkill")
	}
	if m.Available("mmcli") {
		if idx, err := m.modemIndex(ctx); err == nil {
			_, merr := m.mmcli(ctx, 30*time.Second, "-m", idx, "--disable")
			record(merr, "mmcli")
		} else {
			record(err, "mmcli")
		}
	}
	if attempted == 0 {
		return fmt.Errorf("未找到 nmcli、rfkill 或 mmcli，无法关闭蜂窝射频")
	}
	if succeeded == 0 {
		return fmt.Errorf("关闭蜂窝射频失败: %v", lastErr)
	}
	return nil
}

// cellularRadioOn reverses cellularRadioOff. When waitReady is set it also
// waits briefly for the modem to register, because simple-connect usually fails
// while the radio is still searching for a network. It returns an error when no
// tool exists or every attempted command failed.
func (m *Manager) cellularRadioOn(ctx context.Context, waitReady bool) error {
	attempted, succeeded := 0, 0
	var lastErr error
	record := func(err error, label string) {
		attempted++
		if err == nil {
			succeeded++
			return
		}
		lastErr = err
		m.log.Printf("%s 恢复蜂窝射频失败: %v", label, err)
	}
	if m.Available("rfkill") {
		_, err := m.Run(ctx, 10*time.Second, "rfkill", "unblock", "wwan")
		record(err, "rfkill")
	}
	if m.Available("nmcli") {
		_, err := m.Run(ctx, 15*time.Second, "nmcli", "radio", "wwan", "on")
		record(err, "nmcli")
	}
	if m.Available("mmcli") {
		idx := strings.TrimSpace(m.Config().Modem.Index)
		if idx == "" {
			idx = "0"
		}
		_, err := m.mmcli(ctx, 30*time.Second, "-m", idx, "--enable")
		record(err, "mmcli")
		if waitReady && succeeded > 0 {
			deadline := time.Now().Add(45 * time.Second)
			for time.Now().Before(deadline) {
				info, ierr := m.modemInfo(ctx, idx)
				if ierr != nil {
					if found, ferr := m.modemIndex(ctx); ferr == nil && found != idx {
						idx = found
						_, _ = m.mmcli(ctx, 30*time.Second, "-m", idx, "--enable")
					}
				} else {
					reg := strings.ToLower(info.Registration)
					if reg == "home" || reg == "roaming" {
						break
					}
				}
				if !sleepCtx(ctx, 2*time.Second) {
					break
				}
			}
		}
	}
	if attempted == 0 {
		return fmt.Errorf("未找到 nmcli、rfkill 或 mmcli，无法恢复蜂窝射频")
	}
	if succeeded == 0 {
		return fmt.Errorf("恢复蜂窝射频失败: %v", lastErr)
	}
	return nil
}

// applyFlightModeOnStart re-applies flight mode after a reboot.
func (m *Manager) applyFlightModeOnStart(ctx context.Context) {
	if !m.slept(10 * time.Second) {
		return
	}
	if !m.Config().Modem.FlightMode {
		return
	}
	m.log.Printf("飞行模式已开启，启动时关闭蜂窝射频")
	if err := m.cellularRadioOff(ctx); err != nil {
		m.log.Printf("启动时关闭蜂窝射频失败: %v", err)
	}
}

// ConnectAPN brings the cellular data connection up.
func (m *Manager) ConnectAPN(ctx context.Context, profileID string) error {
	if m.Config().Modem.FlightMode {
		return fmt.Errorf("飞行模式已开启，请先退出飞行模式")
	}
	cfg := m.Config()
	var prof *config.APNProfile
	if profileID != "" {
		prof = cfg.FindProfile(profileID)
		if prof == nil {
			return fmt.Errorf("APN 配置不存在: %s", profileID)
		}
	} else if prof = cfg.ActiveAPN(); prof == nil && len(cfg.Modem.Profiles) > 0 {
		prof = &cfg.Modem.Profiles[0]
	}
	if prof == nil || strings.TrimSpace(prof.APN) == "" {
		return fmt.Errorf("请先在 APN 页面选择或添加一个接入点")
	}
	// Keep the stored choice in sync even when no profile was activated yet,
	// so the data switch always dials the APN shown as current.
	if cfg.Modem.ActiveProfile != prof.ID {
		if err := m.Update(func(c *config.Config) error {
			c.Modem.ActiveProfile = prof.ID
			return nil
		}); err != nil {
			return err
		}
	}
	// Let the bridge follow the new data state without waiting for the poll tick.
	defer m.reactToCellularState(ctx)

	if strings.TrimSpace(cfg.Modem.Interface) != "" {
		m.disconnectNMDevice(ctx, cfg.Modem.Interface)
	}

	kind := m.modemManagerKind()
	if kind == "nmcli" {
		if err := m.connectNM(ctx, prof, cfg); err != nil {
			return err
		}
	} else if kind == "mmcli" {
		if err := m.connectMM(ctx, prof, cfg); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("未找到 mmcli 或 nmcli，无法拨号")
	}

	// Wait for an address on wwan0 or a connected packet service.
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if cfg.Modem.Interface != "" {
			if out, err := m.Run(ctx, 3*time.Second, "ip", "-4", "addr", "show", "dev", cfg.Modem.Interface); err == nil && strings.Contains(out, "inet ") {
				return nil
			}
		}
		if st := m.ModemStatus(ctx); st.Connected {
			return nil
		}
		if !sleepCtx(ctx, 2*time.Second) {
			return ctx.Err()
		}
	}
	return fmt.Errorf("拨号超时：请检查 APN、SIM 卡余额与信号")
}

func (m *Manager) connectMM(ctx context.Context, prof *config.APNProfile, cfg *config.Config) error {
	idx, err := m.modemIndexOrErr(ctx)
	if err != nil {
		return err
	}
	_, _ = m.mmcli(ctx, 8*time.Second, "-m", idx, "--enable")
	_, _ = m.mmcli(ctx, 15*time.Second, "-m", idx, "--simple-disconnect")
	if cfg.Modem.AllowedModes != "" && cfg.Modem.AllowedModes != "auto" {
		_, _ = m.mmcli(ctx, 15*time.Second, "-m", idx, "--set-allowed-modes="+mmModes(cfg.Modem.AllowedModes))
	}
	ipType := prof.IPType
	if ipType == "" {
		ipType = cfg.Modem.IPType
	}
	parts := []string{"apn=" + prof.APN}
	if prof.User != "" {
		parts = append(parts, "user="+escapeMM(prof.User))
	}
	if prof.Password != "" {
		parts = append(parts, "password="+escapeMM(prof.Password))
	}
	parts = append(parts, "ip-type="+mmIPType(ipType))
	if cfg.Modem.AllowRoaming {
		parts = append(parts, "allow-roaming=yes")
	} else {
		parts = append(parts, "allow-roaming=no")
	}
	if _, err := m.mmcli(ctx, 90*time.Second, "-m", idx, "--simple-connect="+strings.Join(parts, ",")); err != nil {
		return fmt.Errorf("拨号失败: %w", err)
	}
	// Best effort: remember the APN as the modem's initial bearer.
	_, _ = m.mmcli(ctx, 15*time.Second, "-m", idx, "--3gpp-set-initial-eps-bearer-settings=apn="+prof.APN+",ip-type="+mmIPType(ipType))
	return nil
}

func (m *Manager) connectNM(ctx context.Context, prof *config.APNProfile, cfg *config.Config) error {
	if !m.Available("nmcli") {
		return fmt.Errorf("未安装 nmcli")
	}
	name := cfg.Modem.ConnectionName
	if name == "" {
		name = modemNMConnection
	}
	_, _ = m.Run(ctx, 10*time.Second, "nmcli", "connection", "delete", name)
	if _, err := m.Run(ctx, 15*time.Second, "nmcli", "connection", "add", "type", "gsm",
		"ifname", cfg.Modem.Interface, "con-name", name, "apn", prof.APN); err != nil {
		return fmt.Errorf("创建移动网络连接失败: %w", err)
	}
	ipType := prof.IPType
	if ipType == "" {
		ipType = cfg.Modem.IPType
	}
	args := []string{"connection", "modify", name,
		"connection.autoconnect", "yes",
		"ipv4.method", "auto",
		"ipv6.method", "ignore",
	}
	if ipType == "ipv6" {
		args = []string{"connection", "modify", name, "connection.autoconnect", "yes", "ipv4.method", "disabled", "ipv6.method", "auto"}
	}
	if prof.User != "" {
		args = append(args, "gsm.username", prof.User)
	}
	if prof.Password != "" {
		args = append(args, "gsm.password", prof.Password)
	}
	if prof.Auth != "" && prof.Auth != "none" {
		args = append(args, "gsm.password-flags", "0")
	}
	if _, err := m.Run(ctx, 15*time.Second, "nmcli", args...); err != nil {
		return fmt.Errorf("配置移动网络连接失败: %w", err)
	}
	if _, err := m.Run(ctx, 90*time.Second, "nmcli", "connection", "up", name); err != nil {
		return fmt.Errorf("拨号失败: %w", err)
	}
	return nil
}

func (m *Manager) disconnectNMDevice(ctx context.Context, iface string) {
	if !m.Available("nmcli") || iface == "" {
		return
	}
	out, err := m.Run(ctx, 8*time.Second, "nmcli", "-t", "-f", "NAME,DEVICE", "connection", "show", "--active")
	if err != nil {
		return
	}
	for _, line := range strings.Split(out, "\n") {
		f := parse.SplitTerse(line)
		if len(f) >= 2 && f[1] == iface {
			_, _ = m.Run(ctx, 15*time.Second, "nmcli", "connection", "down", f[0])
		}
	}
}

// DisconnectAPN tears the cellular connection down.
func (m *Manager) DisconnectAPN(ctx context.Context) error {
	defer m.reactToCellularState(ctx)
	if !m.Available("nmcli") && !m.Available("mmcli") {
		return fmt.Errorf("未找到 mmcli 或 nmcli，无法断开蜂窝连接")
	}
	cfg := m.Config()
	var firstErr error
	if m.Available("nmcli") {
		name := cfg.Modem.ConnectionName
		if name == "" {
			name = modemNMConnection
		}
		if _, err := m.Run(ctx, 20*time.Second, "nmcli", "connection", "down", name); err != nil {
			// Inactive connections make "down" fail harmlessly; verification
			// below decides whether data is really still up.
			firstErr = err
			m.log.Printf("nmcli 断开连接 %s 失败: %v", name, err)
		}
	}
	if m.Available("mmcli") {
		if idx, err := m.modemIndex(ctx); err == nil {
			if _, err := m.mmcli(ctx, 30*time.Second, "-m", idx, "--simple-disconnect"); err != nil {
				return fmt.Errorf("断开失败: %w", err)
			}
		} else if firstErr == nil {
			firstErr = err
		}
	}
	// Do not report success while the modem still has an active connection.
	// ModemManager needs a moment to tear the packet service down.
	for i := 0; i < 5; i++ {
		if !m.CellularDataActive(ctx) {
			return nil
		}
		if i < 4 && !sleepCtx(ctx, 2*time.Second) {
			break
		}
	}
	if firstErr != nil {
		return fmt.Errorf("断开失败: %w", firstErr)
	}
	return fmt.Errorf("断开失败：蜂窝连接仍然存在")
}

// SetAllowedModes applies and then persists the modem's allowed access technologies.
func (m *Manager) SetAllowedModes(ctx context.Context, modes string) error {
	switch modes {
	case "auto", "4g", "3g", "2g", "4g3g":
	default:
		return fmt.Errorf("无效的网络模式: %s", modes)
	}
	// "auto" maps to "ANY", which resets a modem previously pinned to
	// 4G/3G/2G, so it must be sent to the modem instead of only being saved.
	idx, err := m.modemIndexOrErr(ctx)
	if err != nil {
		return err
	}
	if _, err := m.mmcli(ctx, 30*time.Second, "-m", idx, "--set-allowed-modes="+mmModes(modes)); err != nil {
		return fmt.Errorf("设置网络模式失败: %w", err)
	}
	return m.Update(func(c *config.Config) error {
		c.Modem.AllowedModes = modes
		return nil
	})
}

func mmModes(v string) string {
	switch v {
	case "auto":
		return "ANY"
	case "4g":
		return "4G"
	case "3g":
		return "3G"
	case "2g":
		return "2G"
	case "4g3g":
		return "4G|3G"
	}
	return ""
}

func mmIPType(v string) string {
	switch v {
	case "ipv6":
		return "ipv6"
	case "ipv4v6":
		return "ipv4v6"
	}
	return "ipv4"
}

func escapeMM(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, ",", "\\,")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}

// SaveAPN upserts an APN profile and optionally activates it. It returns the
// final profile ID so the caller can dial with the stored profile directly.
func (m *Manager) SaveAPN(profile config.APNProfile, activate bool) (string, error) {
	profile.ID = strings.TrimSpace(profile.ID)
	if profile.ID == "" {
		base := strings.ToLower(strings.TrimSpace(profile.APN))
		base = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(base, "-")
		if base == "" {
			base = "apn"
		}
		profile.ID = fmt.Sprintf("%s-%d", base, time.Now().Unix()%100000)
	}
	err := m.Update(func(c *config.Config) error {
		found := false
		for i := range c.Modem.Profiles {
			if c.Modem.Profiles[i].ID == profile.ID {
				c.Modem.Profiles[i] = profile
				found = true
				break
			}
		}
		if !found {
			c.Modem.Profiles = append(c.Modem.Profiles, profile)
		}
		if activate {
			c.Modem.ActiveProfile = profile.ID
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return profile.ID, nil
}

// DeleteAPN removes an APN profile.
func (m *Manager) DeleteAPN(id string) error {
	return m.Update(func(c *config.Config) error {
		out := c.Modem.Profiles[:0]
		for _, p := range c.Modem.Profiles {
			if p.ID != id {
				out = append(out, p)
			}
		}
		if len(out) == len(c.Modem.Profiles) {
			return fmt.Errorf("APN 配置不存在")
		}
		c.Modem.Profiles = out
		if c.Modem.ActiveProfile == id {
			c.Modem.ActiveProfile = ""
		}
		return nil
	})
}

// ActivateAPN selects the profile used for dialling.
func (m *Manager) ActivateAPN(id string) error {
	return m.Update(func(c *config.Config) error {
		if id != "" && c.FindProfile(id) == nil {
			return fmt.Errorf("APN 配置不存在")
		}
		c.Modem.ActiveProfile = id
		return nil
	})
}

// ScanOperators runs a 3GPP network scan.
func (m *Manager) ScanOperators(ctx context.Context) ([]parse.Operator, error) {
	idx, err := m.modemIndexOrErr(ctx)
	if err != nil {
		return nil, err
	}
	out, err := m.mmcliJSON(ctx, 120*time.Second, "-m", idx, "--3gpp-scan", "--timeout=90")
	if err != nil {
		return nil, fmt.Errorf("运营商扫描失败: %w", err)
	}
	ops := parse.ParseOperatorScan(out)
	if len(ops) == 0 {
		return nil, fmt.Errorf("未扫描到运营商，请检查 SIM 卡与天线")
	}
	return ops, nil
}

// RegisterOperator forces registration on a network, or home when code is empty.
func (m *Manager) RegisterOperator(ctx context.Context, code string) error {
	idx, err := m.modemIndexOrErr(ctx)
	if err != nil {
		return err
	}
	var arg string
	if code == "" {
		arg = "--3gpp-register-home"
	} else {
		for _, r := range code {
			if r < '0' || r > '9' {
				return fmt.Errorf("运营商代码必须是 MCCMNC 数字")
			}
		}
		arg = "--3gpp-register-in-operator=" + code
	}
	if _, err := m.mmcli(ctx, 120*time.Second, "-m", idx, arg); err != nil {
		return fmt.Errorf("网络注册失败: %w", err)
	}
	return nil
}

// USSD executes an USSD action.
func (m *Manager) USSD(ctx context.Context, action, code string) (string, error) {
	idx, err := m.modemIndexOrErr(ctx)
	if err != nil {
		return "", err
	}
	switch action {
	case "", "initiate", "send":
		code = strings.TrimSpace(code)
		if code == "" || len(code) > 180 {
			return "", fmt.Errorf("USSD 代码无效")
		}
		if strings.ContainsAny(code, "\r\n;|&`$") {
			return "", fmt.Errorf("USSD 代码包含非法字符")
		}
		out, err := m.mmcli(ctx, 60*time.Second, "-m", idx, "--3gpp-ussd-initiate="+code)
		if err != nil {
			return "", fmt.Errorf("USSD 执行失败: %w", err)
		}
		return out, nil
	case "respond":
		if code == "" {
			return "", fmt.Errorf("回复内容不能为空")
		}
		out, err := m.mmcli(ctx, 30*time.Second, "-m", idx, "--3gpp-ussd-respond="+escapeMM(code))
		if err != nil {
			return "", fmt.Errorf("USSD 回复失败: %w", err)
		}
		return out, nil
	case "cancel":
		out, err := m.mmcli(ctx, 30*time.Second, "-m", idx, "--3gpp-ussd-cancel")
		if err != nil {
			return "", fmt.Errorf("USSD 取消失败: %w", err)
		}
		return out, nil
	default:
		return "", fmt.Errorf("未知的 USSD 操作")
	}
}

// SetSIMPin unlocks a locked SIM with a PIN or PUK.
func (m *Manager) SetSIMPin(ctx context.Context, kind, pin, puk string) error {
	idx, err := m.modemIndexOrErr(ctx)
	if err != nil {
		return err
	}
	if !regexp.MustCompile(`^[0-9]{4,8}$`).MatchString(pin) {
		return fmt.Errorf("PIN 必须为 4-8 位数字")
	}
	if kind == "puk" {
		if !regexp.MustCompile(`^[0-9]{8}$`).MatchString(puk) {
			return fmt.Errorf("PUK 必须为 8 位数字")
		}
		if _, err := m.mmcli(ctx, 30*time.Second, "-m", idx, "--sim-puk="+puk+","+pin); err != nil {
			return fmt.Errorf("PUK 解锁失败: %w", err)
		}
		return nil
	}
	if _, err := m.mmcli(ctx, 30*time.Second, "-m", idx, "--sim-pin="+pin); err != nil {
		return fmt.Errorf("PIN 解锁失败: %w", err)
	}
	return nil
}

// SMS is one message with panel-local read state.
type SMS struct {
	ID        string `json:"id"`
	Number    string `json:"number"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp"`
	State     string `json:"state"`
	Storage   string `json:"storage"`
	Data      bool   `json:"data"`
	Read      bool   `json:"read"`
}

// ListSMS enumerates all messages stored on the modem.
func (m *Manager) ListSMS(ctx context.Context) ([]SMS, error) {
	idx, err := m.modemIndexOrErr(ctx)
	if err != nil {
		return nil, err
	}
	out, err := m.mmcliJSON(ctx, 20*time.Second, "-m", idx, "--messaging-list-sms")
	if err != nil {
		return nil, fmt.Errorf("读取短信列表失败: %w", err)
	}
	paths, err := parse.ParseSMSList(out)
	if err != nil {
		return nil, err
	}
	snap := m.store.Snapshot()
	var list []SMS
	for _, p := range paths {
		id := pathIndex(p)
		if id == "" {
			continue
		}
		info, err := m.smsInfo(ctx, id)
		if err != nil {
			continue
		}
		s := SMS{
			ID:        id,
			Number:    info.Number,
			Text:      info.Text,
			Timestamp: info.Timestamp,
			State:     info.State,
			Storage:   info.Storage,
			Data:      info.Data,
		}
		s.Read = snap.SMSRead[id] > 0
		list = append(list, s)
	}
	// Newest first when timestamps are parseable, otherwise keep modem order.
	sortSMS(list)
	return list, nil
}

func sortSMS(list []SMS) {
	if len(list) < 2 {
		return
	}
	// Modem indices grow with arrival; newest last. Reverse for display.
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
}

func (m *Manager) smsInfo(ctx context.Context, id string) (*parse.SMSInfo, error) {
	out, err := m.mmcliJSON(ctx, 10*time.Second, "-s", id)
	if err != nil {
		return nil, err
	}
	return parse.ParseSMS(out)
}

// GetSMS returns one message and marks it read.
func (m *Manager) GetSMS(ctx context.Context, id string) (*SMS, error) {
	if !regexp.MustCompile(`^[0-9]{1,6}$`).MatchString(id) {
		return nil, fmt.Errorf("短信 ID 无效")
	}
	info, err := m.smsInfo(ctx, id)
	if err != nil {
		return nil, err
	}
	m.store.Update(func(s *state.State) { s.SMSRead[id] = time.Now().Unix() })
	s := &SMS{ID: id, Number: info.Number, Text: info.Text, Timestamp: info.Timestamp, State: info.State, Storage: info.Storage, Data: info.Data, Read: true}
	return s, nil
}

// MarkSMSRead updates the read marker for one or all messages.
func (m *Manager) MarkSMSRead(ids []string) {
	now := time.Now().Unix()
	m.store.Update(func(s *state.State) {
		for _, id := range ids {
			if regexp.MustCompile(`^[0-9]{1,6}$`).MatchString(id) {
				s.SMSRead[id] = now
			}
		}
	})
}

// SendSMS creates and sends a text message.
func (m *Manager) SendSMS(ctx context.Context, number, text string) (string, error) {
	number = parse.NormalizePhone(number)
	if len(number) < 5 || len(number) > 20 {
		return "", fmt.Errorf("手机号格式无效")
	}
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("短信内容不能为空")
	}
	if len([]rune(text)) > 1000 {
		return "", fmt.Errorf("短信内容过长（最多 1000 字）")
	}
	idx, err := m.modemIndexOrErr(ctx)
	if err != nil {
		return "", err
	}
	// mmcli parses the property list by commas; escape separators.
	textArg := escapeMM(strings.TrimSpace(text))
	out, err := m.mmcli(ctx, 30*time.Second, "-m", idx, "--messaging-create-sms=number="+number+",text="+textArg)
	if err != nil {
		return "", fmt.Errorf("创建短信失败: %w", err)
	}
	smsID := ""
	if mm := regexp.MustCompile(`SMS/([0-9]+)`).FindStringSubmatch(out); mm != nil {
		smsID = mm[1]
	}
	if smsID == "" {
		// Fall back to the newest message in the list.
		if list, err := m.ListSMS(ctx); err == nil && len(list) > 0 {
			smsID = list[0].ID
		}
	}
	if smsID == "" {
		return "", fmt.Errorf("短信已创建但无法获取 ID")
	}
	if _, err := m.mmcli(ctx, 60*time.Second, "-s", smsID, "--send"); err != nil {
		return smsID, fmt.Errorf("发送短信失败: %w", err)
	}
	return smsID, nil
}

// DeleteSMS removes one message from the modem.
func (m *Manager) DeleteSMS(ctx context.Context, id string) error {
	if !regexp.MustCompile(`^[0-9]{1,6}$`).MatchString(id) {
		return fmt.Errorf("短信 ID 无效")
	}
	idx, err := m.modemIndexOrErr(ctx)
	if err != nil {
		return err
	}
	if _, err := m.mmcli(ctx, 15*time.Second, "-m", idx, "--messaging-delete-sms="+id); err != nil {
		return fmt.Errorf("删除短信失败: %w", err)
	}
	m.store.Update(func(s *state.State) { delete(s.SMSRead, id) })
	return nil
}

// DeleteAllSMS removes every message from the modem.
func (m *Manager) DeleteAllSMS(ctx context.Context) error {
	list, err := m.ListSMS(ctx)
	if err != nil {
		return err
	}
	var last error
	for _, s := range list {
		if err := m.DeleteSMS(ctx, s.ID); err != nil {
			last = err
		}
	}
	if last != nil {
		return last
	}
	return nil
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func jsonBytes(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
