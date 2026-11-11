package svc

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"uz801d/internal/auth"
	"uz801d/internal/buildinfo"
	"uz801d/internal/config"
	"uz801d/internal/netutil"
	"uz801d/internal/sysinfo"
)

// SystemInfo is the system page payload.
type SystemInfo struct {
	sysinfo.Snapshot
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	BuildTime   string `json:"build_time"`
	DiskTotal   uint64 `json:"disk_total"`
	DiskUsed    uint64 `json:"disk_used"`
	DiskFree    uint64 `json:"disk_free"`
	LoadText    string `json:"load_text"`
	UptimeText  string `json:"uptime_text"`
	FrpcLogPath string `json:"frpc_log_path"`
}

// SystemInfo collects a fresh snapshot with disk and build metadata.
func (m *Manager) SystemInfo(ctx context.Context) *SystemInfo {
	s := sysinfo.Collect()
	out := &SystemInfo{
		Snapshot:    s,
		Version:     buildinfo.Version,
		Commit:      buildinfo.Commit,
		BuildTime:   buildinfo.BuildTime,
		LoadText:    fmt.Sprintf("%.2f / %.2f / %.2f", s.Load1, s.Load5, s.Load15),
		UptimeText:  formatDuration(s.UptimeSec),
		FrpcLogPath: m.Config().System.FrpcLogPath,
	}
	if res := m.runner.Run(ctx, 6*time.Second, "df", "-Pk", "/"); res.Err == nil {
		if total, used, free, ok := sysinfo.ParseDF(res.Stdout); ok {
			out.DiskTotal, out.DiskUsed, out.DiskFree = total, used, free
		}
	}
	return out
}

func formatDuration(sec int64) string {
	if sec <= 0 {
		return "-"
	}
	d := time.Duration(sec) * time.Second
	days := int64(d.Hours()) / 24
	hours := int64(d.Hours()) % 24
	mins := int64(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%d 天 %d 小时 %d 分", days, hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%d 小时 %d 分", hours, mins)
	}
	return fmt.Sprintf("%d 分", mins)
}

// Reboot restarts the device.
func (m *Manager) Reboot(ctx context.Context) error {
	cfg := m.Config()
	if !cfg.System.AllowReboot {
		return fmt.Errorf("设备重启已被配置禁用")
	}
	m.store.Save()
	if _, err := m.Run(ctx, 10*time.Second, "systemctl", "reboot"); err != nil {
		if _, err2 := m.Run(ctx, 10*time.Second, "reboot"); err2 != nil {
			return fmt.Errorf("重启失败: %w", err)
		}
	}
	return nil
}

// Poweroff shuts the device down.
func (m *Manager) Poweroff(ctx context.Context) error {
	cfg := m.Config()
	if !cfg.System.AllowPoweroff {
		return fmt.Errorf("设备关机已被配置禁用")
	}
	m.store.Save()
	if _, err := m.Run(ctx, 10*time.Second, "systemctl", "poweroff"); err != nil {
		if _, err2 := m.Run(ctx, 10*time.Second, "poweroff"); err2 != nil {
			return fmt.Errorf("关机失败: %w", err)
		}
	}
	return nil
}

// ResetModem performs a soft reset of the cellular modem.
func (m *Manager) ResetModem(ctx context.Context) error {
	idx, err := m.modemIndexOrErr(ctx)
	if err != nil {
		return err
	}
	if _, err := m.mmcli(ctx, 30*time.Second, "-m", idx, "--reset"); err != nil {
		return fmt.Errorf("重置模块失败: %w", err)
	}
	return nil
}

// FactoryReset restores defaults and clears persisted state. The admin
// password is preserved by default so the device is never left with an
// unknown credential.
func (m *Manager) FactoryReset(ctx context.Context, keepPassword bool) error {
	cfg := m.Config()
	if !cfg.System.AllowFactoryReset {
		return fmt.Errorf("恢复出厂设置已被配置禁用")
	}
	if m.BridgeActive() {
		_ = m.DisableBridge(ctx)
	}
	next := config.Default()
	if keepPassword {
		next.AdminUser = cfg.AdminUser
		next.PasswordHash = cfg.PasswordHash
		next.SessionHours = cfg.SessionHours
		next.Listen = cfg.Listen
	}
	if err := m.SetConfig(next); err != nil {
		return err
	}
	if err := m.store.Reset(); err != nil {
		return fmt.Errorf("清除状态失败: %w", err)
	}
	m.stopStandaloneAP(ctx)
	_ = m.syncBlockedSet(ctx)
	return nil
}

// ChangePassword verifies and replaces the admin password.
func (m *Manager) ChangePassword(oldPassword, newPassword string) error {
	cfg := m.Config()
	if cfg.PasswordHash != "" && !auth.VerifyPassword(oldPassword, cfg.PasswordHash) {
		return fmt.Errorf("当前密码不正确")
	}
	if len(newPassword) < 8 {
		return fmt.Errorf("新密码至少需要 8 个字符")
	}
	if len(newPassword) > 128 {
		return fmt.Errorf("新密码过长")
	}
	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := m.Update(func(c *config.Config) error {
		c.PasswordHash = hash
		return nil
	}); err != nil {
		return err
	}
	// The initial password is no longer valid once the operator changed it.
	_ = os.Remove(filepath.Join(filepath.Dir(m.cfgPath), "initial-password"))
	return nil
}

// SetLanguage persists the UI language.
func (m *Manager) SetLanguage(lang string) error {
	if lang != "zh" && lang != "en" {
		return fmt.Errorf("语言必须是 zh 或 en")
	}
	return m.Update(func(c *config.Config) error {
		c.Language = lang
		return nil
	})
}

// SetAdminUser renames the administrator account.
func (m *Manager) SetAdminUser(user string) error {
	return m.Update(func(c *config.Config) error {
		c.AdminUser = strings.TrimSpace(user)
		return nil
	})
}

// SetSessionHours changes the session lifetime (applies to new logins).
func (m *Manager) SetSessionHours(h int) error {
	return m.Update(func(c *config.Config) error {
		c.SessionHours = h
		return nil
	})
}

// Logs returns recent journal entries.
func (m *Manager) Logs(ctx context.Context, lines int, unit string) (string, error) {
	cfg := m.Config()
	if lines < 10 {
		lines = cfg.System.LogLines
	}
	if lines < 10 {
		lines = 200
	}
	if lines > 2000 {
		lines = 2000
	}
	unit = strings.TrimSpace(unit)
	if unit != "" && !regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,64}$`).MatchString(unit) {
		return "", fmt.Errorf("日志单元名称无效")
	}
	if m.Available("journalctl") {
		args := []string{"--no-pager", "-r", "-o", "short-iso", "-n", strconv.Itoa(lines)}
		if unit != "" {
			args = append(args, "-u", unit)
		}
		if out, err := m.Run(ctx, 15*time.Second, "journalctl", args...); err == nil {
			return out, nil
		}
	}
	for _, path := range []string{"/var/log/syslog", "/var/log/messages", "/var/log/daemon.log"} {
		if text, err := tailFile(path, lines); err == nil {
			return text, nil
		}
	}
	return "", fmt.Errorf("无法读取系统日志（未安装 journalctl 且没有 syslog）")
}

// SetFrpcLogPath stores the path of the frpc log file shown on the system page.
func (m *Manager) SetFrpcLogPath(path string) error {
	path = strings.TrimSpace(path)
	return m.Update(func(c *config.Config) error {
		c.System.FrpcLogPath = path
		return nil
	})
}

// ReadLogFile returns the last lines of an absolute log file path, newest first.
func (m *Manager) ReadLogFile(path string, lines int) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("请先填写日志文件路径")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("日志路径必须是绝对路径")
	}
	if lines < 10 {
		lines = m.Config().System.LogLines
	}
	if lines < 10 {
		lines = 200
	}
	if lines > 2000 {
		lines = 2000
	}
	st, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("无法读取日志文件: %w", err)
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("日志路径不是普通文件: %s", path)
	}
	text, err := tailFile(path, lines)
	if err != nil {
		return "", fmt.Errorf("读取日志失败: %w", err)
	}
	return text, nil
}

func tailFile(path string, lines int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	const maxRead = 4 << 20
	start := st.Size() - maxRead
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, 0); err != nil {
		return "", err
	}
	var all []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		all = append(all, sc.Text())
	}
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	// Newest entries first so the UI shows the latest line at the top.
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	return strings.Join(all, "\n"), nil
}

// LEDInfo is one controllable LED.
type LEDInfo struct {
	Name       string   `json:"name"`
	Trigger    string   `json:"trigger"`
	Brightness int      `json:"brightness"`
	Max        int      `json:"max_brightness"`
	Triggers   []string `json:"triggers"`
}

// ListLEDs enumerates /sys/class/leds.
func (m *Manager) ListLEDs() []LEDInfo {
	dirs, _ := filepath.Glob("/sys/class/leds/*")
	var out []LEDInfo
	for _, d := range dirs {
		name := filepath.Base(d)
		info := LEDInfo{Name: name, Max: 255}
		trigger := sysinfo.ReadText(filepath.Join(d, "trigger"), 4096)
		info.Triggers = parseTriggerList(trigger)
		info.Trigger = activeTrigger(trigger)
		if v, err := strconv.Atoi(sysinfo.ReadText(filepath.Join(d, "brightness"), 32)); err == nil {
			info.Brightness = v
		}
		if v, err := strconv.Atoi(sysinfo.ReadText(filepath.Join(d, "max_brightness"), 32)); err == nil && v > 0 {
			info.Max = v
		}
		out = append(out, info)
	}
	return out
}

func parseTriggerList(s string) []string {
	var out []string
	for _, f := range strings.Fields(s) {
		f = strings.Trim(f, "[]")
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func activeTrigger(s string) string {
	for _, f := range strings.Fields(s) {
		if strings.HasPrefix(f, "[") && strings.HasSuffix(f, "]") {
			return strings.Trim(f, "[]")
		}
	}
	return ""
}

// SetLED changes a LED trigger and/or brightness.
func (m *Manager) SetLED(name, trigger string, brightness int) error {
	if !regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`).MatchString(name) {
		return fmt.Errorf("LED 名称无效")
	}
	dir := filepath.Join("/sys/class/leds", name)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("LED 不存在: %s", name)
	}
	if trigger != "" && !regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`).MatchString(trigger) {
		return fmt.Errorf("触发器名称无效")
	}
	if trigger != "" {
		if err := os.WriteFile(filepath.Join(dir, "trigger"), []byte(trigger), 0o644); err != nil {
			return fmt.Errorf("设置触发器失败: %w", err)
		}
	}
	if brightness >= 0 {
		max := 255
		if v, err := strconv.Atoi(sysinfo.ReadText(filepath.Join(dir, "max_brightness"), 32)); err == nil && v > 0 {
			max = v
		}
		if brightness > max {
			brightness = max
		}
		if err := os.WriteFile(filepath.Join(dir, "brightness"), []byte(strconv.Itoa(brightness)), 0o644); err != nil {
			return fmt.Errorf("设置亮度失败: %w", err)
		}
	}
	return m.Update(func(c *config.Config) error {
		if c.LEDs == nil {
			c.LEDs = map[string]config.LEDSetting{}
		}
		c.LEDs[name] = config.LEDSetting{Trigger: trigger, Brightness: brightness}
		return nil
	})
}

// ApplyLEDs restores persisted LED settings at startup.
func (m *Manager) ApplyLEDs() {
	for name, s := range m.Config().LEDs {
		if err := m.SetLED(name, s.Trigger, s.Brightness); err != nil {
			m.log.Printf("恢复 LED %s 失败: %v", name, err)
		}
	}
}

// Ping runs a bounded ICMP test.
func (m *Manager) Ping(ctx context.Context, host string, count int) (string, error) {
	host = strings.TrimSpace(host)
	if !netutil.IsValidHost(host) {
		return "", fmt.Errorf("目标地址无效")
	}
	if count < 1 || count > 10 {
		count = 4
	}
	timeout := time.Duration(count)*3*time.Second + 8*time.Second
	// Windows ping uses different flags: -c means "compartment" there and
	// fails with "access denied", while the count/timeout options are -n/-w.
	args := []string{"-c", strconv.Itoa(count), "-W", "2", host}
	if runtime.GOOS == "windows" {
		args = []string{"-n", strconv.Itoa(count), "-w", "2000", host}
	}
	out, err := m.Run(ctx, timeout, "ping", args...)
	if err != nil {
		if out == "" {
			return "", fmt.Errorf("ping 失败: %w", err)
		}
		return out, nil
	}
	return out, nil
}

// DNSLookup resolves a hostname using the system resolver.
func (m *Manager) DNSLookup(ctx context.Context, host string) ([]string, error) {
	if !netutil.IsValidHost(host) {
		return nil, fmt.Errorf("目标地址无效")
	}
	out, err := m.Run(ctx, 10*time.Second, "getent", "ahosts", host)
	if err != nil {
		return nil, fmt.Errorf("解析失败: %w", err)
	}
	var out2 []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 1 {
			out2 = append(out2, f[0])
		}
	}
	return out2, nil
}
