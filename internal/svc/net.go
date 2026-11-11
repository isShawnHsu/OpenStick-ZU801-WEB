package svc

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"uz801d/internal/parse"
)

// NetInterface is one IPv4 interface summary.
type NetInterface struct {
	Name     string `json:"name"`
	Address  string `json:"address,omitempty"`
	Prefix   int    `json:"prefix,omitempty"`
	MAC      string `json:"mac,omitempty"`
	State    string `json:"state,omitempty"`
	RXBytes  int64  `json:"rx_bytes"`
	TXBytes  int64  `json:"tx_bytes"`
	Default  bool   `json:"default"`
	Wireless bool   `json:"wireless"`
}

// NetworkInfo summarises interfaces, routes and DNS.
type NetworkInfo struct {
	Interfaces   []NetInterface `json:"interfaces"`
	Gateway      string         `json:"gateway"`
	DefaultIface string         `json:"default_iface"`
	DNS          []string       `json:"dns"`
}

// Network collects network state from iproute2 and sysfs.
func (m *Manager) Network(ctx context.Context) *NetworkInfo {
	out := &NetworkInfo{}
	addrs := map[string]parse.IfaceAddr{}
	if text, err := m.Run(ctx, 5*time.Second, "ip", "-o", "-4", "addr", "show"); err == nil {
		for _, a := range parse.ParseIPAddr(text) {
			if _, ok := addrs[a.Iface]; !ok {
				addrs[a.Iface] = a
			}
		}
	}
	if text, err := m.Run(ctx, 5*time.Second, "ip", "-o", "link", "show"); err == nil {
		for _, line := range strings.Split(text, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			name := strings.TrimSuffix(fields[1], ":")
			if name == "" || name == "lo" {
				continue
			}
			iface := NetInterface{Name: name}
			if a, ok := addrs[name]; ok {
				iface.Address = a.Address
				iface.Prefix = a.Prefix
			}
			iface.MAC = sysinfoRead(filepath.Join("/sys/class/net", name, "address"))
			iface.State = sysinfoRead(filepath.Join("/sys/class/net", name, "operstate"))
			if _, err := os.Stat(filepath.Join("/sys/class/net", name, "wireless")); err == nil {
				iface.Wireless = true
			}
			iface.RXBytes, _ = readIntFile(filepath.Join("/sys/class/net", name, "statistics", "rx_bytes"))
			iface.TXBytes, _ = readIntFile(filepath.Join("/sys/class/net", name, "statistics", "tx_bytes"))
			out.Interfaces = append(out.Interfaces, iface)
		}
	}
	if text, err := m.Run(ctx, 5*time.Second, "ip", "route", "show", "default"); err == nil {
		out.Gateway, out.DefaultIface = parse.ParseDefaultRoute(text)
	}
	for i := range out.Interfaces {
		if out.Interfaces[i].Name == out.DefaultIface {
			out.Interfaces[i].Default = true
		}
	}
	sort.Slice(out.Interfaces, func(i, j int) bool {
		if out.Interfaces[i].Default != out.Interfaces[j].Default {
			return out.Interfaces[i].Default
		}
		return out.Interfaces[i].Name < out.Interfaces[j].Name
	})
	if data, err := os.ReadFile("/etc/resolv.conf"); err == nil {
		out.DNS = parse.ParseResolvConf(string(data))
	}
	return out
}

func sysinfoRead(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
