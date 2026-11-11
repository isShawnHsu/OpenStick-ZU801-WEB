package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaultValid(t *testing.T) {
	c := Default()
	c.Normalize()
	if err := c.Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	c := Default()
	c.Wifi.Bridge.UpstreamSSID = "HomeWiFi"
	c.Wifi.Bridge.UpstreamPassword = "supersecret"
	c.Wifi.Bridge.Auto = true
	if err := Save(path, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Errorf("perm = %o, want 600", st.Mode().Perm())
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Wifi.Bridge.UpstreamSSID != "HomeWiFi" || !got.Wifi.Bridge.Auto {
		t.Fatalf("bridge not restored: %#v", got.Wifi.Bridge)
	}
	if got.FindProfile("cmcc") == nil {
		t.Fatal("default APN profiles missing")
	}
}

func TestCloneIsDeep(t *testing.T) {
	c := Default()
	cp := c.Clone()
	cp.Wifi.SSID = "changed"
	cp.Modem.Profiles[0].APN = "changed"
	if c.Wifi.SSID == "changed" {
		t.Error("clone shares wifi SSID")
	}
	if c.Modem.Profiles[0].APN == "changed" {
		t.Error("clone shares profiles slice")
	}
}

func TestValidateRejectsBadValues(t *testing.T) {
	cases := []struct {
		name string
		fn   func(*Config)
	}{
		{"empty ssid", func(c *Config) { c.Wifi.SSID = "" }},
		{"short password", func(c *Config) { c.Wifi.Password = "123" }},
		{"bad channel", func(c *Config) { c.Wifi.Channel = 40 }},
		{"bad netmask", func(c *Config) { c.LAN.Netmask = "255.0.255.0" }},
		{"dhcp outside", func(c *Config) { c.LAN.DHCPStart = "10.0.0.2"; c.LAN.DHCPEnd = "10.0.0.5" }},
		{"bad bridge iface", func(c *Config) { c.Wifi.Bridge.APInterface = "bad iface" }},
		{"short upstream password", func(c *Config) {
			c.Wifi.Bridge.UpstreamSSID = "up"
			c.Wifi.Bridge.UpstreamPassword = "123"
		}},
		{"bad listen", func(c *Config) { c.Listen = "nope" }},
	}
	for _, tc := range cases {
		c := Default()
		tc.fn(c)
		c.Normalize()
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected validation error", tc.name)
		}
	}
}

func TestNormalizeForces24GHzBand(t *testing.T) {
	c := Default()
	c.Wifi.Band = "a"
	c.Wifi.Channel = 149
	c.Normalize()
	if c.Wifi.Band != "bg" {
		t.Errorf("band = %q, want bg", c.Wifi.Band)
	}
	if c.Wifi.Channel != 0 {
		t.Errorf("channel = %d, want 0", c.Wifi.Channel)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("coerced config invalid: %v", err)
	}
}

func TestValidateRejectsLanguage(t *testing.T) {
	c := Default()
	c.Language = "fr"
	if err := c.Validate(); err == nil {
		t.Fatal("expected validation error for unknown language")
	}
}

func TestFrpcLogPathValidation(t *testing.T) {
	c := Default()
	c.System.FrpcLogPath = filepath.Join(t.TempDir(), "frpc.log")
	c.Normalize()
	if err := c.Validate(); err != nil {
		t.Fatalf("absolute path rejected: %v", err)
	}

	c.System.FrpcLogPath = "relative/frpc.log"
	if err := c.Validate(); err == nil {
		t.Fatal("relative path accepted")
	}

	c.System.FrpcLogPath = filepath.Join(t.TempDir(), "bad\npath.log")
	if err := c.Validate(); err == nil {
		t.Fatal("newline path accepted")
	}
}

func TestNormalizeBridgeDefaults(t *testing.T) {
	c := Default()
	c.Wifi.Bridge.APInterface = ""
	c.Wifi.Bridge.UpstreamSecurity = ""
	c.Wifi.Bridge.RouteMetric = 0
	c.Normalize()
	if c.Wifi.Bridge.APInterface != "auto" {
		t.Errorf("ap_interface = %q", c.Wifi.Bridge.APInterface)
	}
	if c.Wifi.Bridge.UpstreamSecurity != "wpa2" {
		t.Errorf("upstream_security = %q", c.Wifi.Bridge.UpstreamSecurity)
	}
	if c.Wifi.Bridge.RouteMetric != 50 {
		t.Errorf("route_metric = %d", c.Wifi.Bridge.RouteMetric)
	}
}
