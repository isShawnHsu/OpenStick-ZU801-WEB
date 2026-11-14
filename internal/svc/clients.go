package svc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"uz801d/internal/netutil"
	"uz801d/internal/parse"
	"uz801d/internal/state"
)

var (
	errNftRequired = fmt.Errorf("未安装 nftables（nft），无法启用 MAC 过滤")
	errNameTooLong = fmt.Errorf("备注名称最多 64 个字符")
)

// Client is one downstream device.
type Client struct {
	MAC              string  `json:"mac"`
	IP               string  `json:"ip"`
	Hostname         string  `json:"hostname"`
	Online           bool    `json:"online"`
	Blocked          bool    `json:"blocked"`
	Signal           float64 `json:"signal"`
	RXBytes          int64   `json:"rx_bytes"`
	TXBytes          int64   `json:"tx_bytes"`
	ConnectedSeconds int64   `json:"connected_seconds"`
	FirstSeen        int64   `json:"first_seen"`
	LastSeen         int64   `json:"last_seen"`
}

// Clients merges wireless stations, DHCP leases and remembered devices.
func (m *Manager) Clients(ctx context.Context) []Client {
	cfg := m.Config()
	apIface := cfg.Wifi.Interface
	if br := m.BridgeSnapshot(); br.Active && br.APInterface != "" {
		apIface = br.APInterface
	}
	stations := map[string]parse.Station{}
	if out, err := m.Run(ctx, 6*time.Second, "iw", "dev", apIface, "station", "dump"); err == nil {
		stations = parse.ParseStations(out)
	}
	leases := m.readLeases()
	neighbors := m.readNeighbors(ctx)
	now := time.Now().Unix()
	snap := m.store.Snapshot()
	blocked := map[string]bool{}
	for _, mac := range snap.Blocked {
		blocked[mac] = true
	}

	byMAC := map[string]*Client{}
	add := func(mac string) *Client {
		mac, err := netutil.NormalizeMAC(mac)
		if err != nil {
			return nil
		}
		if c, ok := byMAC[mac]; ok {
			return c
		}
		c := &Client{MAC: mac, Blocked: blocked[mac]}
		if e, ok := snap.Clients[mac]; ok {
			c.FirstSeen = e.FirstSeen
			c.LastSeen = e.LastSeen
			c.Hostname = e.Hostname
		}
		byMAC[mac] = c
		return c
	}

	for mac, st := range stations {
		c := add(mac)
		if c == nil {
			continue
		}
		c.Online = true
		c.Signal = st.Signal
		c.RXBytes = st.RXBytes
		c.TXBytes = st.TXBytes
		c.ConnectedSeconds = st.ConnectedSeconds
	}
	for _, l := range leases {
		lmac, err := netutil.NormalizeMAC(l.MAC)
		if err != nil {
			continue
		}
		_, stationOnline := stations[lmac]
		online := stationOnline || neighbors[lmac]
		// A plain lease without a live station/neighbour is only a memory of
		// an old device. Do not resurrect it after "clear offline records";
		// only remembered clients are allowed to appear offline.
		if _, remembered := snap.Clients[lmac]; !online && !remembered {
			continue
		}
		c := add(lmac)
		if c == nil {
			continue
		}
		if l.IP != "" {
			c.IP = l.IP
		}
		if l.Hostname != "" {
			c.Hostname = l.Hostname
		}
		if online {
			c.Online = true
		}
	}

	out := make([]Client, 0, len(byMAC))
	for _, c := range byMAC {
		if c.Online {
			c.LastSeen = now
		}
		if c.Hostname == "" {
			c.Hostname = c.IP
		}
		out = append(out, *c)
		if c.Online {
			mac, host := c.MAC, c.Hostname
			m.store.TouchClient(mac, host)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Online != out[j].Online {
			return out[i].Online
		}
		if out[i].LastSeen != out[j].LastSeen {
			return out[i].LastSeen > out[j].LastSeen
		}
		return out[i].MAC < out[j].MAC
	})
	return out
}

// readNeighbors returns MACs that currently have an ARP/neighbour entry.
// This makes USB-sharing clients visible as online even though there is no
// wireless station table for usb0.
func (m *Manager) readNeighbors(ctx context.Context) map[string]bool {
	out, err := m.Run(ctx, 4*time.Second, "ip", "neigh", "show")
	if err != nil {
		return nil
	}
	res := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		mac := ""
		for i := 0; i+1 < len(fields); i++ {
			if fields[i] == "lladdr" {
				mac = fields[i+1]
				break
			}
		}
		if mac == "" {
			continue
		}
		state := fields[len(fields)-1]
		if state == "FAILED" || state == "INCOMPLETE" {
			continue
		}
		if normalized, err := netutil.NormalizeMAC(mac); err == nil {
			res[normalized] = true
		}
	}
	return res
}

func (m *Manager) readLeases() []parse.Lease {
	candidates := []string{
		bridgeLeaseFile,
		"/var/lib/misc/dnsmasq.leases",
		"/var/lib/dnsmasq/dnsmasq.leases",
		"/tmp/dnsmasq.leases",
	}
	patterns := []string{
		"/var/lib/NetworkManager/dnsmasq-*.leases",
		"/var/lib/misc/dnsmasq*.leases",
	}
	seen := map[string]bool{}
	var out []parse.Lease
	read := func(path string) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		data, err := os.ReadFile(path)
		if err != nil {
			return
		}
		out = append(out, parse.ParseLeases(data)...)
	}
	for _, p := range candidates {
		read(p)
	}
	for _, pat := range patterns {
		matches, _ := filepath.Glob(pat)
		for _, p := range matches {
			read(p)
		}
	}
	return out
}

// BlockClient adds a MAC to the filter set and kicks it off the AP.
func (m *Manager) BlockClient(ctx context.Context, mac string) error {
	mac, err := netutil.NormalizeMAC(mac)
	if err != nil {
		return err
	}
	if !m.nftAvailable() {
		return errNftRequired
	}
	already := false
	m.store.Update(func(s *state.State) {
		for _, v := range s.Blocked {
			if v == mac {
				already = true
				return
			}
		}
		s.Blocked = append(s.Blocked, mac)
	})
	if err := m.syncBlockedSet(ctx); err != nil {
		return err
	}
	m.kickClient(ctx, mac)
	if already {
		return nil
	}
	return nil
}

// UnblockClient removes a MAC from the filter set.
func (m *Manager) UnblockClient(ctx context.Context, mac string) error {
	mac, err := netutil.NormalizeMAC(mac)
	if err != nil {
		return err
	}
	if m.nftAvailable() {
		if err := m.delBlockedElement(ctx, mac); err != nil {
			return fmt.Errorf("解除拉黑失败: %w", err)
		}
	}
	m.store.Update(func(s *state.State) {
		out := s.Blocked[:0]
		for _, v := range s.Blocked {
			if v != mac {
				out = append(out, v)
			}
		}
		s.Blocked = out
	})
	return nil
}

func (m *Manager) kickClient(ctx context.Context, mac string) {
	if !m.Available("hostapd_cli") {
		return
	}
	apIface := m.Config().Wifi.Interface
	if br := m.BridgeSnapshot(); br.Active && br.APInterface != "" {
		apIface = br.APInterface
	}
	_, _ = m.Run(ctx, 5*time.Second, "hostapd_cli", "-p", "/var/run/hostapd", "-i", apIface, "deauthenticate", mac)
}

// RenameClient stores a friendly name for a MAC.
func (m *Manager) RenameClient(mac, name string) error {
	mac, err := netutil.NormalizeMAC(mac)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if len([]rune(name)) > 64 {
		return errNameTooLong
	}
	m.store.Update(func(s *state.State) {
		e := s.Clients[mac]
		if e.FirstSeen == 0 {
			e.FirstSeen = time.Now().Unix()
		}
		e.Hostname = name
		e.LastSeen = time.Now().Unix()
		s.Clients[mac] = e
	})
	return nil
}

// ClearClientHistory forgets remembered clients except currently online ones.
func (m *Manager) ClearClientHistory(ctx context.Context) {
	online := map[string]bool{}
	for _, c := range m.Clients(ctx) {
		if c.Online {
			online[c.MAC] = true
		}
	}
	m.store.Update(func(s *state.State) {
		for mac := range s.Clients {
			if !online[mac] {
				delete(s.Clients, mac)
			}
		}
	})
}
