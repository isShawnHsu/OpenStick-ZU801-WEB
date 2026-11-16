// Package config holds the persistent configuration of msm8916d.
package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"msm8916d/internal/netutil"
)

const (
	DefaultConfigPath = "/etc/msm8916d/config.json"
	DefaultStatePath  = "/var/lib/msm8916d/state.json"
)

// Config is the complete persistent configuration.
type Config struct {
	Listen       string `json:"listen"`
	AdminUser    string `json:"admin_user"`
	PasswordHash string `json:"password_hash"`
	SessionHours int    `json:"session_hours"`
	Language     string `json:"language"`

	Modem  ModemConfig           `json:"modem"`
	Wifi   WifiConfig            `json:"wifi"`
	LAN    LANConfig             `json:"lan"`
	Data   DataConfig            `json:"data"`
	SMS    SMSConfig             `json:"sms"`
	System SystemConfig          `json:"system"`
	VPN    VPNConfig             `json:"vpn"`
	LEDs   map[string]LEDSetting `json:"leds,omitempty"`
}

// VPNConfig controls the v2ray client managed by the VPN page.
type VPNConfig struct {
	// Binary is the v2ray (or xray) executable.
	Binary string `json:"binary"`
	// ConfigPath is where the generated client config is written.
	ConfigPath string `json:"config_path"`
	// ServiceName is the systemd unit that runs the binary.
	ServiceName string `json:"service_name"`
	// SocksPort and HTTPPort are the local proxy inbounds.
	SocksPort int `json:"socks_port"`
	HTTPPort  int `json:"http_port"`
	// AllowLAN makes the proxy ports reachable from the hotspot/USB clients.
	AllowLAN bool `json:"allow_lan"`
	// RoutingMode selects how the generated config routes traffic:
	//   global - everything except private ranges goes through the proxy
	//   smart  - geosite:cn / geoip:cn go direct, the rest through the proxy
	//            (smart needs geoip.dat / geosite.dat next to GeoPath)
	RoutingMode string `json:"routing_mode"`
	// GeoPath is the directory holding geoip.dat / geosite.dat.
	GeoPath string `json:"geo_path"`
	// LogPath is the v2ray error log shown on the page.
	LogPath string `json:"log_path"`
	// Transparent redirects all TCP from the device and from USB/hotspot
	// clients into v2ray, so clients need no proxy settings.
	Transparent bool `json:"transparent"`
	// TransparentPort is the local dokodemo-door port used by the redirect.
	TransparentPort int `json:"transparent_port"`
}

// ModemConfig controls the cellular modem backend.
type ModemConfig struct {
	Backend           string       `json:"backend"`            // auto | mmcli | nmcli
	Index             string       `json:"index"`              // ModemManager modem index, usually "0"
	Interface         string       `json:"interface"`          // wwan0
	ConnectionManager string       `json:"connection_manager"` // auto | mmcli | nmcli
	ConnectionName    string       `json:"connection_name"`    // optional NetworkManager connection name/UUID
	AllowedModes      string       `json:"allowed_modes"`      // auto | 4g | 3g | 2g | 4g3g
	AllowRoaming      bool         `json:"allow_roaming"`
	IPType            string       `json:"ip_type"` // ipv4 | ipv6 | ipv4v6
	Profiles          []APNProfile `json:"apn_profiles"`
	ActiveProfile     string       `json:"active_profile"`
	// FlightMode disables the cellular radio entirely while leaving WiFi up.
	FlightMode bool `json:"flight_mode"`
	// FlightResumeData remembers whether data was connected when flight mode
	// was entered, so leaving flight mode can restore that state.
	FlightResumeData bool `json:"flight_resume_data"`
}

// APNProfile is one carrier APN setting.
type APNProfile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	APN      string `json:"apn"`
	User     string `json:"user"`
	Password string `json:"password"`
	Auth     string `json:"auth"` // none | pap | chap | mschap | mschapv2
	IPType   string `json:"ip_type"`
}

// WifiConfig controls the access point.
type WifiConfig struct {
	Backend           string       `json:"backend"` // auto | nmcli | hostapd | none
	Interface         string       `json:"interface"`
	Connection        string       `json:"connection"` // optional NM connection name/UUID
	SSID              string       `json:"ssid"`
	Password          string       `json:"password"`
	Security          string       `json:"security"` // wpa2 | wpa3 | open
	Band              string       `json:"band"`     // bg (MSM8916 仅支持 2.4 GHz)
	Channel           int          `json:"channel"`  // 0 = auto
	Hidden            bool         `json:"hidden"`
	MaxClients        int          `json:"max_clients"`
	EnforceMaxClients bool         `json:"enforce_max_clients"`
	Country           string       `json:"country"`
	HostapdConfig     string       `json:"hostapd_config"`
	DNSMasqConfig     string       `json:"dnsmasq_config"`
	ApplyOnStart      bool         `json:"apply_on_start"`
	Bridge            BridgeConfig `json:"bridge"`
}

// BridgeConfig controls the RaspAP-style AP-STA WiFi relay.
type BridgeConfig struct {
	Mode                 string `json:"mode"`                 // usb | ap_sta
	DownstreamInterface  string `json:"downstream_interface"` // usb0
	Enabled              bool   `json:"enabled"`
	Auto                 bool   `json:"auto"`
	Force                bool   `json:"force"`
	APInterface          string `json:"ap_interface"` // "" = auto (uap0 or wlan1)
	UpstreamSSID         string `json:"upstream_ssid"`
	UpstreamPassword     string `json:"upstream_password"`
	UpstreamSecurity     string `json:"upstream_security"` // auto | wpa2 | wpa3 | open
	UpstreamHidden       bool   `json:"upstream_hidden"`
	UpstreamConnection   string `json:"upstream_connection"`
	PreviousAPConnection string `json:"previous_ap_connection"`
	RouteMetric          int    `json:"route_metric"`
	Channel              int    `json:"channel"` // 0 = follow upstream channel
}

// LANConfig controls the local network and DHCP server.
type LANConfig struct {
	IP        string   `json:"ip"`
	Netmask   string   `json:"netmask"`
	DHCPStart string   `json:"dhcp_start"`
	DHCPEnd   string   `json:"dhcp_end"`
	DNS       []string `json:"dns"`
	LeaseTime string   `json:"lease_time"`
}

// DataConfig controls traffic accounting and quota.
type DataConfig struct {
	Interface    string `json:"interface"`
	QuotaBytes   int64  `json:"quota_bytes"`
	ResetDay     int    `json:"reset_day"`
	EnforceQuota bool   `json:"enforce_quota"`
}

// SMSConfig controls forwarding of received text messages.
type SMSConfig struct {
	ForwardEnabled bool               `json:"forward_enabled"`
	PollSeconds    int                `json:"poll_seconds"`
	Providers      []SMSForwardTarget `json:"providers"`
}

// SMSForwardTarget is one webhook destination for forwarded SMS.
type SMSForwardTarget struct {
	Name    string `json:"name"`
	Type    string `json:"type"` // bark | dingtalk | feishu
	Enabled bool   `json:"enabled"`
	URL     string `json:"url,omitempty"`
	Token   string `json:"token,omitempty"`
	Secret  string `json:"secret,omitempty"`
	Group   string `json:"group,omitempty"`
	Sound   string `json:"sound,omitempty"`
}

// SystemConfig holds safe-operation switches.
type SystemConfig struct {
	AllowReboot       bool   `json:"allow_reboot"`
	AllowPoweroff     bool   `json:"allow_poweroff"`
	AllowFactoryReset bool   `json:"allow_factory_reset"`
	LogLines          int    `json:"log_lines"`
	FrpcLogPath       string `json:"frpc_log_path"`
	// FrpcConfigPath is the TOML file shown on the frpc page.
	FrpcConfigPath string `json:"frpc_config_path"`

	// LED power control. LEDOff always wins; the schedule only applies when
	// LEDOff is false. Times use the device's local timezone in HH:MM form.
	LEDOff             bool                  `json:"led_off"`
	LEDScheduleEnabled bool                  `json:"led_schedule_enabled"`
	LEDOffTime         string                `json:"led_off_time"`
	LEDOnTime          string                `json:"led_on_time"`
	LEDSnapshot        map[string]LEDSetting `json:"led_snapshot,omitempty"`
}

// LEDSetting is a persisted LED trigger state.
type LEDSetting struct {
	Trigger    string `json:"trigger"`
	Brightness int    `json:"brightness"`
}

// Default returns a complete configuration with safe, OpenStick-friendly values.
func Default() *Config {
	return &Config{
		Listen:       ":80",
		AdminUser:    "admin",
		PasswordHash: "",
		SessionHours: 12,
		Language:     "zh",
		Modem: ModemConfig{
			Backend:           "auto",
			Index:             "0",
			Interface:         "wwan0",
			ConnectionManager: "auto",
			AllowedModes:      "auto",
			AllowRoaming:      true,
			IPType:            "ipv4",
			Profiles: []APNProfile{
				{ID: "cmcc", Name: "中国移动", APN: "cmnet", Auth: "none", IPType: "ipv4"},
				{ID: "unicom", Name: "中国联通", APN: "3gnet", Auth: "none", IPType: "ipv4"},
				{ID: "telecom", Name: "中国电信", APN: "ctnet", Auth: "none", IPType: "ipv4"},
				{ID: "cbn", Name: "中国广电", APN: "cbnet", Auth: "none", IPType: "ipv4"},
				{ID: "generic", Name: "通用 internet", APN: "internet", Auth: "none", IPType: "ipv4"},
			},
			ActiveProfile: "",
		},
		Wifi: WifiConfig{
			Backend:       "auto",
			Interface:     "wlan0",
			SSID:          "4G-UFI-XX",
			Password:      "1234567890",
			Security:      "wpa2",
			Band:          "bg",
			Channel:       6,
			MaxClients:    10,
			Country:       "CN",
			HostapdConfig: "/etc/hostapd/hostapd.conf",
			DNSMasqConfig: "/etc/dnsmasq.d/msm8916d.conf",
			ApplyOnStart:  true,
			Bridge: BridgeConfig{
				Mode:                "usb",
				DownstreamInterface: "usb0",
				Auto:                true,
				UpstreamSecurity:    "wpa2",
				UpstreamConnection:  "msm8916d-upstream",
				RouteMetric:         50,
			},
		},
		LAN: LANConfig{
			IP:        "192.168.68.1",
			Netmask:   "255.255.255.0",
			DHCPStart: "192.168.68.100",
			DHCPEnd:   "192.168.68.200",
			DNS:       []string{"223.5.5.5", "119.29.29.29"},
			LeaseTime: "12h",
		},
		Data: DataConfig{
			Interface:  "wwan0",
			ResetDay:   1,
			QuotaBytes: 0,
		},
		SMS: SMSConfig{
			PollSeconds: 60,
			Providers:   []SMSForwardTarget{},
		},
		System: SystemConfig{
			AllowReboot:       true,
			AllowPoweroff:     true,
			AllowFactoryReset: true,
			LogLines:          200,
			LEDOffTime:        "22:00",
			LEDOnTime:         "07:00",
		},
		VPN: VPNConfig{
			Binary:      "/usr/local/bin/v2ray",
			ConfigPath:  "/etc/v2ray/config.json",
			ServiceName: "v2ray",
			SocksPort:   1080,
			HTTPPort:    1081,
			AllowLAN:    true,
			RoutingMode: "smart",
			GeoPath:     "/usr/lib/v2ray",
			LogPath:     "/var/log/v2ray/error.log",
			TransparentPort: 12345,
		},
		LEDs: map[string]LEDSetting{},
	}
}

var (
	userRe    = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,32}$`)
	ifaceRe   = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,32}$`)
	apnRe     = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	countryRe = regexp.MustCompile(`^[A-Za-z]{2}$`)
	barkKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)
	// smsURLRe loosely matches an http(s) URL; url.Parse does the real check.
	smsURLRe = regexp.MustCompile(`^https?://`)
)

// Load reads cfgPath, starting from defaults so new fields get sane values.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := Default()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件 %s 失败: %w", path, err)
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("配置文件 %s 无效: %w", path, err)
	}
	return cfg, nil
}

// Normalize trims strings and fills purely optional defaults.
func (c *Config) Normalize() {
	c.Listen = strings.TrimSpace(c.Listen)
	c.AdminUser = strings.TrimSpace(c.AdminUser)
	c.Language = strings.ToLower(strings.TrimSpace(c.Language))
	if c.Listen == "" {
		c.Listen = ":80"
	}
	if !strings.Contains(c.Listen, ":") {
		c.Listen = ":" + c.Listen
	}
	if c.AdminUser == "" {
		c.AdminUser = "admin"
	}
	if c.Language != "en" {
		c.Language = "zh"
	}
	if c.SessionHours == 0 {
		c.SessionHours = 12
	}
	if c.Modem.Backend == "" {
		c.Modem.Backend = "auto"
	}
	if c.Modem.ConnectionManager == "" {
		c.Modem.ConnectionManager = "auto"
	}
	if c.Modem.Index == "" {
		c.Modem.Index = "0"
	}
	if c.Modem.Interface == "" {
		c.Modem.Interface = "wwan0"
	}
	if c.Modem.IPType == "" {
		c.Modem.IPType = "ipv4"
	}
	if c.Modem.AllowedModes == "" {
		c.Modem.AllowedModes = "auto"
	}
	if c.Modem.Profiles == nil {
		c.Modem.Profiles = Default().Modem.Profiles
	}
	for i := range c.Modem.Profiles {
		p := &c.Modem.Profiles[i]
		p.ID = strings.TrimSpace(p.ID)
		p.Name = strings.TrimSpace(p.Name)
		p.APN = strings.TrimSpace(p.APN)
		p.User = strings.TrimSpace(p.User)
		p.Auth = strings.ToLower(strings.TrimSpace(p.Auth))
		p.IPType = strings.ToLower(strings.TrimSpace(p.IPType))
		if p.Auth == "" {
			p.Auth = "none"
		}
		if p.IPType == "" {
			p.IPType = "ipv4"
		}
	}
	c.Modem.ActiveProfile = strings.TrimSpace(c.Modem.ActiveProfile)

	if c.Wifi.Backend == "" {
		c.Wifi.Backend = "auto"
	}
	if c.Wifi.Interface == "" {
		c.Wifi.Interface = "wlan0"
	}
	c.Wifi.SSID = strings.TrimSpace(c.Wifi.SSID)
	c.Wifi.Security = strings.ToLower(strings.TrimSpace(c.Wifi.Security))
	c.Wifi.Band = strings.ToLower(strings.TrimSpace(c.Wifi.Band))
	c.Wifi.Country = strings.ToUpper(strings.TrimSpace(c.Wifi.Country))
	if c.Wifi.Security == "" {
		c.Wifi.Security = "wpa2"
	}
	if c.Wifi.Band == "" {
		c.Wifi.Band = "bg"
	}
	// MSM8916 hardware only supports 2.4 GHz. Old configs may still carry "a";
	// coerce them instead of refusing to start.
	if c.Wifi.Band != "bg" {
		c.Wifi.Band = "bg"
		if c.Wifi.Channel > 14 {
			c.Wifi.Channel = 0
		}
	}
	if c.Wifi.Channel < 0 {
		c.Wifi.Channel = 0
	}
	if c.Wifi.MaxClients == 0 {
		c.Wifi.MaxClients = 10
	}
	if c.Wifi.HostapdConfig == "" {
		c.Wifi.HostapdConfig = "/etc/hostapd/hostapd.conf"
	}
	if c.Wifi.DNSMasqConfig == "" {
		c.Wifi.DNSMasqConfig = "/etc/dnsmasq.d/msm8916d.conf"
	}
	c.Wifi.Bridge.Mode = strings.ToLower(strings.TrimSpace(c.Wifi.Bridge.Mode))
	if c.Wifi.Bridge.Mode == "" {
		c.Wifi.Bridge.Mode = "usb"
	}
	c.Wifi.Bridge.DownstreamInterface = strings.TrimSpace(c.Wifi.Bridge.DownstreamInterface)
	if c.Wifi.Bridge.DownstreamInterface == "" {
		c.Wifi.Bridge.DownstreamInterface = "usb0"
	}
	c.Wifi.Bridge.UpstreamSSID = strings.TrimSpace(c.Wifi.Bridge.UpstreamSSID)
	c.Wifi.Bridge.UpstreamSecurity = strings.ToLower(strings.TrimSpace(c.Wifi.Bridge.UpstreamSecurity))
	c.Wifi.Bridge.APInterface = strings.TrimSpace(c.Wifi.Bridge.APInterface)
	c.Wifi.Bridge.UpstreamConnection = strings.TrimSpace(c.Wifi.Bridge.UpstreamConnection)
	if c.Wifi.Bridge.UpstreamSecurity == "" {
		c.Wifi.Bridge.UpstreamSecurity = "wpa2"
	}
	if c.Wifi.Bridge.UpstreamConnection == "" {
		c.Wifi.Bridge.UpstreamConnection = "msm8916d-upstream"
	}
	if c.Wifi.Bridge.RouteMetric == 0 {
		c.Wifi.Bridge.RouteMetric = 50
	}
	if c.Wifi.Bridge.Channel < 0 {
		c.Wifi.Bridge.Channel = 0
	}
	if c.Wifi.Bridge.APInterface == "" {
		c.Wifi.Bridge.APInterface = "auto"
	}

	if c.LAN.IP == "" {
		c.LAN.IP = "192.168.68.1"
	}
	if c.LAN.Netmask == "" {
		c.LAN.Netmask = "255.255.255.0"
	}
	if c.LAN.DHCPStart == "" {
		c.LAN.DHCPStart = "192.168.68.100"
	}
	if c.LAN.DHCPEnd == "" {
		c.LAN.DHCPEnd = "192.168.68.200"
	}
	if c.LAN.LeaseTime == "" {
		c.LAN.LeaseTime = "12h"
	}
	if c.LAN.DNS == nil {
		c.LAN.DNS = []string{"223.5.5.5", "119.29.29.29"}
	}

	if c.Data.Interface == "" {
		c.Data.Interface = c.Modem.Interface
	}
	if c.Data.ResetDay == 0 {
		c.Data.ResetDay = 1
	}
	c.SMS.Normalize()
	if c.System.LogLines == 0 {
		c.System.LogLines = 200
	}
	c.System.FrpcLogPath = strings.TrimSpace(c.System.FrpcLogPath)
	c.System.FrpcConfigPath = strings.TrimSpace(c.System.FrpcConfigPath)
	if c.System.FrpcConfigPath == "" {
		c.System.FrpcConfigPath = "/etc/frp/frpc.toml"
	}
	c.System.LEDOffTime = normalizeClock(c.System.LEDOffTime, "22:00")
	c.System.LEDOnTime = normalizeClock(c.System.LEDOnTime, "07:00")
	c.VPN = normalizeVPN(c.VPN)
	if c.LEDs == nil {
		c.LEDs = map[string]LEDSetting{}
	}
}

// normalizeVPN fills empty fields with the defaults so an older config file
// gains the VPN section without a manual edit.
func normalizeVPN(v VPNConfig) VPNConfig {
	def := Default().VPN
	if strings.TrimSpace(v.Binary) == "" {
		v.Binary = def.Binary
	}
	if strings.TrimSpace(v.ConfigPath) == "" {
		v.ConfigPath = def.ConfigPath
	}
	if strings.TrimSpace(v.ServiceName) == "" {
		v.ServiceName = def.ServiceName
	}
	if v.SocksPort == 0 {
		v.SocksPort = def.SocksPort
	}
	if v.HTTPPort == 0 {
		v.HTTPPort = def.HTTPPort
	}
	if strings.TrimSpace(v.LogPath) == "" {
		v.LogPath = def.LogPath
	}
	if v.RoutingMode != "global" && v.RoutingMode != "smart" {
		v.RoutingMode = def.RoutingMode
	}
	if strings.TrimSpace(v.GeoPath) == "" {
		v.GeoPath = def.GeoPath
	}
	if v.TransparentPort == 0 {
		v.TransparentPort = def.TransparentPort
	}
	v.Binary = strings.TrimSpace(v.Binary)
	v.ConfigPath = strings.TrimSpace(v.ConfigPath)
	v.ServiceName = strings.TrimSpace(v.ServiceName)
	v.LogPath = strings.TrimSpace(v.LogPath)
	v.GeoPath = strings.TrimSpace(v.GeoPath)
	return v
}

func normalizeClock(s, def string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	if t, err := time.Parse("15:04", s); err == nil {
		return t.Format("15:04")
	}
	return s
}

// Validate checks all values that eventually reach shell commands or config files.
func (c *Config) Validate() error {
	if err := validateListen(c.Listen); err != nil {
		return err
	}
	if !userRe.MatchString(c.AdminUser) {
		return fmt.Errorf("管理员用户名只能包含字母、数字、下划线、点和短横线（1-32 位）")
	}
	if c.PasswordHash != "" && !strings.HasPrefix(c.PasswordHash, "pbkdf2-sha256$") {
		return fmt.Errorf("密码哈希格式无效")
	}
	if c.SessionHours < 1 || c.SessionHours > 720 {
		return fmt.Errorf("会话时长必须在 1-720 小时之间")
	}
	if c.Language != "zh" && c.Language != "en" {
		return fmt.Errorf("语言必须是 zh 或 en")
	}

	if c.Modem.Backend != "auto" && c.Modem.Backend != "mmcli" && c.Modem.Backend != "nmcli" {
		return fmt.Errorf("modem.backend 必须是 auto/mmcli/nmcli")
	}
	if c.Modem.ConnectionManager != "auto" && c.Modem.ConnectionManager != "mmcli" && c.Modem.ConnectionManager != "nmcli" {
		return fmt.Errorf("modem.connection_manager 必须是 auto/mmcli/nmcli")
	}
	if !regexp.MustCompile(`^[0-9]{1,3}$`).MatchString(c.Modem.Index) {
		return fmt.Errorf("modem.index 无效")
	}
	if !ifaceRe.MatchString(c.Modem.Interface) {
		return fmt.Errorf("modem.interface 名称无效")
	}
	switch c.Modem.AllowedModes {
	case "auto", "4g", "3g", "2g", "4g3g":
	default:
		return fmt.Errorf("modem.allowed_modes 无效")
	}
	switch c.Modem.IPType {
	case "ipv4", "ipv6", "ipv4v6":
	default:
		return fmt.Errorf("modem.ip_type 无效")
	}
	seen := map[string]bool{}
	for i, p := range c.Modem.Profiles {
		if p.ID == "" {
			return fmt.Errorf("第 %d 个 APN 配置缺少 ID", i+1)
		}
		if seen[p.ID] {
			return fmt.Errorf("APN 配置 ID 重复: %s", p.ID)
		}
		seen[p.ID] = true
		if p.Name == "" || len([]rune(p.Name)) > 40 {
			return fmt.Errorf("APN 配置名称无效")
		}
		if !apnRe.MatchString(p.APN) {
			return fmt.Errorf("APN 接入点格式无效: %s", p.APN)
		}
		if strings.ContainsAny(p.User+p.Password, ",\r\n") {
			return fmt.Errorf("APN 用户名/密码不能包含逗号或换行")
		}
		switch p.Auth {
		case "none", "pap", "chap", "mschap", "mschapv2":
		default:
			return fmt.Errorf("APN 认证方式无效: %s", p.Auth)
		}
		switch p.IPType {
		case "ipv4", "ipv6", "ipv4v6":
		default:
			return fmt.Errorf("APN IP 类型无效: %s", p.IPType)
		}
	}
	if c.Modem.ActiveProfile != "" && !seen[c.Modem.ActiveProfile] {
		return fmt.Errorf("当前 APN 配置不存在")
	}

	if c.Wifi.Backend != "auto" && c.Wifi.Backend != "nmcli" && c.Wifi.Backend != "hostapd" && c.Wifi.Backend != "none" {
		return fmt.Errorf("wifi.backend 必须是 auto/nmcli/hostapd/none")
	}
	if !ifaceRe.MatchString(c.Wifi.Interface) {
		return fmt.Errorf("wifi.interface 名称无效")
	}
	if l := len([]byte(c.Wifi.SSID)); l < 1 || l > 32 {
		return fmt.Errorf("WiFi 名称长度必须为 1-32 字节")
	}
	if strings.ContainsAny(c.Wifi.SSID, "\r\n\x00") {
		return fmt.Errorf("WiFi 名称不能包含控制字符")
	}
	switch c.Wifi.Security {
	case "wpa2", "wpa3":
		if l := len([]byte(c.Wifi.Password)); l < 8 || l > 63 {
			return fmt.Errorf("WiFi 密码长度必须为 8-63 个字符")
		}
		if strings.ContainsAny(c.Wifi.Password, "\r\n\x00") {
			return fmt.Errorf("WiFi 密码不能包含控制字符")
		}
	case "open":
		// password ignored
	default:
		return fmt.Errorf("WiFi 加密方式无效")
	}
	if c.Wifi.Band != "bg" {
		return fmt.Errorf("MSM8916 仅支持 2.4 GHz 频段")
	}
	if c.Wifi.Channel < 0 || c.Wifi.Channel > 14 {
		return fmt.Errorf("2.4G 信道必须为 0-14（0 为自动）")
	}
	if c.Wifi.MaxClients < 1 || c.Wifi.MaxClients > 64 {
		return fmt.Errorf("客户端上限必须为 1-64")
	}
	if c.Wifi.Country != "" && !countryRe.MatchString(c.Wifi.Country) {
		return fmt.Errorf("国家代码必须是两位字母")
	}
	switch c.Wifi.Bridge.Mode {
	case "usb", "ap_sta":
	default:
		return fmt.Errorf("桥接模式无效")
	}
	if c.Wifi.Bridge.DownstreamInterface != "" {
		if !ifaceRe.MatchString(c.Wifi.Bridge.DownstreamInterface) {
			return fmt.Errorf("下游接口名称无效")
		}
		if c.Wifi.Bridge.DownstreamInterface == c.Wifi.Interface {
			return fmt.Errorf("下游接口不能与上游无线网卡相同")
		}
	}
	if c.Wifi.Bridge.APInterface != "" && c.Wifi.Bridge.APInterface != "uap0" && !ifaceRe.MatchString(c.Wifi.Bridge.APInterface) {
		return fmt.Errorf("桥接 AP 网卡名称无效")
	}
	if c.Wifi.Bridge.UpstreamSSID != "" {
		if l := len([]byte(c.Wifi.Bridge.UpstreamSSID)); l < 1 || l > 32 {
			return fmt.Errorf("上游 WiFi 名称长度必须为 1-32 字节")
		}
		if strings.ContainsAny(c.Wifi.Bridge.UpstreamSSID, "\r\n\x00") {
			return fmt.Errorf("上游 WiFi 名称不能包含控制字符")
		}
	}
	switch c.Wifi.Bridge.UpstreamSecurity {
	case "auto", "wpa2", "wpa3", "open":
	default:
		return fmt.Errorf("上游 WiFi 加密方式无效")
	}
	if c.Wifi.Bridge.UpstreamSecurity != "open" && c.Wifi.Bridge.UpstreamSSID != "" {
		if l := len([]byte(c.Wifi.Bridge.UpstreamPassword)); l < 8 || l > 63 {
			return fmt.Errorf("上游 WiFi 密码长度必须为 8-63 个字符")
		}
	}
	if strings.ContainsAny(c.Wifi.Bridge.UpstreamPassword, "\r\n\x00") {
		return fmt.Errorf("上游 WiFi 密码不能包含控制字符")
	}
	if c.Wifi.Bridge.UpstreamConnection == "" || len(c.Wifi.Bridge.UpstreamConnection) > 64 || strings.ContainsAny(c.Wifi.Bridge.UpstreamConnection, "\r\n") {
		return fmt.Errorf("上游 NetworkManager 连接名无效")
	}
	if c.Wifi.Bridge.RouteMetric < 0 || c.Wifi.Bridge.RouteMetric > 99999 {
		return fmt.Errorf("上游路由优先级无效")
	}
	if c.Wifi.Bridge.Channel < 0 || c.Wifi.Bridge.Channel > 14 {
		return fmt.Errorf("桥接 AP 信道必须为 0-14（0 为跟随上游）")
	}

	if err := netutil.ValidateLAN(c.LAN.IP, c.LAN.Netmask, c.LAN.DHCPStart, c.LAN.DHCPEnd, c.LAN.DNS); err != nil {
		return err
	}
	if c.LAN.LeaseTime == "" || len(c.LAN.LeaseTime) > 16 || strings.ContainsAny(c.LAN.LeaseTime, "\r\n") {
		return fmt.Errorf("DHCP 租期无效")
	}

	if !ifaceRe.MatchString(c.Data.Interface) {
		return fmt.Errorf("流量统计网卡名称无效")
	}
	if c.Data.QuotaBytes < 0 || c.Data.QuotaBytes > 1_000_000_000_000_000 {
		return fmt.Errorf("流量配额无效")
	}
	if c.Data.ResetDay < 1 || c.Data.ResetDay > 28 {
		return fmt.Errorf("流量重置日必须为 1-28")
	}

	if err := c.SMS.Validate(); err != nil {
		return err
	}

	if c.System.LogLines < 10 || c.System.LogLines > 1000 {
		return fmt.Errorf("日志行数必须为 10-1000")
	}
	if _, err := time.Parse("15:04", c.System.LEDOffTime); err != nil {
		return fmt.Errorf("LED 关闭时间格式无效，应为 HH:MM")
	}
	if _, err := time.Parse("15:04", c.System.LEDOnTime); err != nil {
		return fmt.Errorf("LED 开启时间格式无效，应为 HH:MM")
	}
	if p := c.System.FrpcLogPath; p != "" {
		if len(p) > 300 || strings.ContainsAny(p, "\r\n\x00") {
			return fmt.Errorf("FRPC 日志路径无效")
		}
		if !filepath.IsAbs(p) {
			return fmt.Errorf("FRPC 日志路径必须是绝对路径")
		}
	}
	if p := c.System.FrpcConfigPath; p != "" {
		if len(p) > 300 || strings.ContainsAny(p, "\r\n\x00") {
			return fmt.Errorf("FRPC 配置路径无效")
		}
		// The device is Linux: require a POSIX absolute path here instead of
		// filepath.IsAbs, which is host-dependent and rejects "/etc/..." on
		// Windows when the tests run there.
		if !strings.HasPrefix(p, "/") {
			return fmt.Errorf("FRPC 配置路径必须是绝对路径")
		}
	}
	for name, led := range c.LEDs {
		if strings.ContainsAny(name, "/\\\r\n") || len(name) > 64 {
			return fmt.Errorf("LED 名称无效: %s", name)
		}
		if strings.ContainsAny(led.Trigger, "/\\\r\n") || len(led.Trigger) > 64 {
			return fmt.Errorf("LED 触发器无效: %s", led.Trigger)
		}
	}

	if v := c.VPN; true {
		if len(v.Binary) > 300 || strings.ContainsAny(v.Binary, "\r\n\x00") {
			return fmt.Errorf("VPN 程序路径无效")
		}
		if !strings.HasPrefix(v.Binary, "/") {
			return fmt.Errorf("VPN 程序路径必须是绝对路径")
		}
		if len(v.ConfigPath) > 300 || strings.ContainsAny(v.ConfigPath, "\r\n\x00") {
			return fmt.Errorf("VPN 配置路径无效")
		}
		if !strings.HasPrefix(v.ConfigPath, "/") {
			return fmt.Errorf("VPN 配置路径必须是绝对路径")
		}
		if len(v.LogPath) > 300 || strings.ContainsAny(v.LogPath, "\r\n\x00") {
			return fmt.Errorf("VPN 日志路径无效")
		}
		if !strings.HasPrefix(v.LogPath, "/") {
			return fmt.Errorf("VPN 日志路径必须是绝对路径")
		}
		if !ifaceRe.MatchString(v.ServiceName) {
			return fmt.Errorf("VPN 服务名无效")
		}
		if v.SocksPort < 1 || v.SocksPort > 65535 {
			return fmt.Errorf("VPN SOCKS 端口必须在 1-65535 之间")
		}
		if v.HTTPPort < 1 || v.HTTPPort > 65535 {
			return fmt.Errorf("VPN HTTP 端口必须在 1-65535 之间")
		}
		if v.SocksPort == v.HTTPPort {
			return fmt.Errorf("VPN SOCKS 和 HTTP 端口不能相同")
		}
		if v.SocksPort == 80 || v.HTTPPort == 80 {
			return fmt.Errorf("VPN 端口不能占用管理页面的 80")
		}
		if v.RoutingMode != "global" && v.RoutingMode != "smart" {
			return fmt.Errorf("VPN 分流模式只能是 global 或 smart")
		}
		if v.TransparentPort < 1 || v.TransparentPort > 65535 {
			return fmt.Errorf("VPN 透明代理端口必须在 1-65535 之间")
		}
		if v.TransparentPort == v.SocksPort || v.TransparentPort == v.HTTPPort || v.TransparentPort == 80 {
			return fmt.Errorf("VPN 透明代理端口不能和代理端口/管理页面冲突")
		}
		if len(v.GeoPath) > 300 || strings.ContainsAny(v.GeoPath, "\r\n\x00") {
			return fmt.Errorf("VPN geo 文件目录无效")
		}
		if !strings.HasPrefix(v.GeoPath, "/") {
			return fmt.Errorf("VPN geo 文件目录必须是绝对路径")
		}
	}
	return nil
}

// Normalize trims SMS forwarding settings and applies safe defaults.
func (c *SMSConfig) Normalize() {
	if c.PollSeconds == 0 {
		c.PollSeconds = 60
	}
	if c.Providers == nil {
		c.Providers = []SMSForwardTarget{}
	}
	for i := range c.Providers {
		c.Providers[i].Normalize()
	}
}

// Normalize trims one forwarding target and clears fields that do not apply.
func (t *SMSForwardTarget) Normalize() {
	t.Name = strings.TrimSpace(t.Name)
	t.Type = strings.ToLower(strings.TrimSpace(t.Type))
	t.URL = strings.TrimRight(strings.TrimSpace(t.URL), "/")
	t.Token = strings.TrimSpace(t.Token)
	t.Secret = strings.TrimSpace(t.Secret)
	t.Group = strings.TrimSpace(t.Group)
	t.Sound = strings.TrimSpace(t.Sound)
	if t.Name == "" {
		t.Name = SMSProviderName(t.Type)
	}
	switch t.Type {
	case "bark":
		if t.URL == "" {
			t.URL = "https://api.day.app"
		}
		t.Secret = ""
	case "dingtalk", "feishu":
		t.Token = ""
		t.Group = ""
		t.Sound = ""
	}
}

// SMSProviderName returns the display name of a provider type.
func SMSProviderName(typ string) string {
	switch typ {
	case "bark":
		return "Bark"
	case "dingtalk":
		return "钉钉"
	case "feishu":
		return "飞书"
	default:
		return "Webhook"
	}
}

// Validate checks forwarding interval and every configured target.
func (c *SMSConfig) Validate() error {
	if c.PollSeconds < 15 || c.PollSeconds > 3600 {
		return fmt.Errorf("短信转发轮询间隔必须为 15-3600 秒")
	}
	if len(c.Providers) > 8 {
		return fmt.Errorf("短信转发渠道最多 8 个")
	}
	for i := range c.Providers {
		if err := c.Providers[i].Validate(); err != nil {
			return fmt.Errorf("第 %d 个短信转发渠道: %w", i+1, err)
		}
	}
	return nil
}

// Validate checks one forwarding target.
func (t *SMSForwardTarget) Validate() error {
	switch t.Type {
	case "bark", "dingtalk", "feishu":
	default:
		return fmt.Errorf("渠道类型必须是 bark/dingtalk/feishu")
	}
	if n := len([]rune(t.Name)); n < 1 || n > 40 {
		return fmt.Errorf("渠道名称长度必须为 1-40 个字符")
	}
	if len(t.URL) > 500 || strings.ContainsAny(t.URL, "\r\n\x00") || !smsURLRe.MatchString(t.URL) {
		return fmt.Errorf("地址必须是有效的 http/https URL")
	}
	u, err := url.Parse(t.URL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("地址必须是有效的 http/https URL")
	}
	if t.Type == "bark" && !barkKeyRe.MatchString(t.Token) {
		return fmt.Errorf("Bark 设备 Key 无效")
	}
	for _, item := range []struct{ name, value string }{
		{"签名密钥", t.Secret},
		{"分组", t.Group},
		{"铃声", t.Sound},
	} {
		if len(item.value) > 200 || strings.ContainsAny(item.value, "\r\n\x00") {
			return fmt.Errorf("%s格式无效", item.name)
		}
	}
	return nil
}

func validateListen(addr string) error {
	host, port, err := splitHostPort(addr)
	if err != nil {
		return fmt.Errorf("监听地址无效: %w", err)
	}
	if host != "" && host != "0.0.0.0" && host != "::" && !netutil.IsValidHost(host) {
		return fmt.Errorf("监听地址无效: %s", host)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("监听端口必须在 1-65535 之间")
	}
	return nil
}

// splitHostPort is a tiny local wrapper to keep the error messages friendly.
func splitHostPort(addr string) (string, int, error) {
	addr = strings.TrimSpace(addr)
	idx := strings.LastIndex(addr, ":")
	if idx < 0 {
		return "", 0, fmt.Errorf("缺少端口")
	}
	host := strings.Trim(addr[:idx], "[]")
	port, err := strconv.Atoi(addr[idx+1:])
	if err != nil {
		return "", 0, fmt.Errorf("端口不是数字")
	}
	return host, port, nil
}

// Save writes the config atomically with mode 0600 and keeps a .bak copy.
func Save(path string, c *Config) error {
	c.Normalize()
	if err := c.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if old, err := os.ReadFile(path); err == nil {
		_ = os.WriteFile(path+".bak", old, 0o600)
	}
	tmp := fmt.Sprintf("%s.tmp-%d", path, time.Now().UnixNano())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	_ = os.Chmod(tmp, 0o600)
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	_ = os.Chmod(path, 0o600)
	return nil
}

// Clone returns a deep copy via JSON round-trip.
func (c *Config) Clone() *Config {
	data, _ := json.Marshal(c)
	out := Default()
	_ = json.Unmarshal(data, out)
	return out
}

// FindProfile returns the profile with id, or nil.
func (c *Config) FindProfile(id string) *APNProfile {
	for i := range c.Modem.Profiles {
		if c.Modem.Profiles[i].ID == id {
			return &c.Modem.Profiles[i]
		}
	}
	return nil
}

// ActiveAPN returns the active APN profile, or nil.
func (c *Config) ActiveAPN() *APNProfile {
	if c.Modem.ActiveProfile == "" {
		return nil
	}
	return c.FindProfile(c.Modem.ActiveProfile)
}
