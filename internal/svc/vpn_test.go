package svc

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"msm8916d/internal/config"
	"msm8916d/internal/state"
)

func b64(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

func TestParseVMessLink(t *testing.T) {
	payload := `{"v":"2","ps":"香港 01","add":"hk.example.com","port":"443",` +
		`"id":"b831381d-6324-4d53-ad4f-8cda48b30811","aid":"0","scy":"auto",` +
		`"net":"ws","type":"none","host":"hk.example.com","path":"/ws",` +
		`"tls":"tls","sni":"hk.example.com"}`
	node, err := parseShareLink("vmess://" + b64(payload))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if node.Protocol != "vmess" || node.Address != "hk.example.com" || node.Port != 443 {
		t.Fatalf("unexpected node: %+v", node)
	}
	if node.UUID != "b831381d-6324-4d53-ad4f-8cda48b30811" {
		t.Fatalf("uuid = %q", node.UUID)
	}
	if !node.TLS || node.Network != "ws" || node.Path != "/ws" || node.Host != "hk.example.com" {
		t.Fatalf("tls/ws fields wrong: %+v", node)
	}
	if node.Name != "香港 01" {
		t.Fatalf("name = %q", node.Name)
	}
}

func TestParseVLESSAndTrojan(t *testing.T) {
	vless := "vless://11111111-2222-3333-4444-555555555555@v.example.com:443" +
		"?encryption=none&security=tls&sni=v.example.com&type=ws&path=%2Fvless&host=v.example.com#node-v"
	node, err := parseShareLink(vless)
	if err != nil {
		t.Fatalf("vless: %v", err)
	}
	if node.Protocol != "vless" || node.Port != 443 || node.Path != "/vless" || !node.TLS {
		t.Fatalf("unexpected vless node: %+v", node)
	}
	if node.Security != "none" {
		t.Fatalf("encryption = %q", node.Security)
	}

	trojan := "trojan://secretpass@t.example.com:8443?security=tls&type=grpc&serviceName=grpcsvc#node-t"
	node, err = parseShareLink(trojan)
	if err != nil {
		t.Fatalf("trojan: %v", err)
	}
	if node.Protocol != "trojan" || node.UUID != "secretpass" || node.Port != 8443 {
		t.Fatalf("unexpected trojan node: %+v", node)
	}

	// security=none must not enable TLS.
	plain := "vless://11111111-2222-3333-4444-555555555555@1.2.3.4:80?security=none&type=tcp#plain"
	node, err = parseShareLink(plain)
	if err != nil {
		t.Fatalf("plain vless: %v", err)
	}
	if node.TLS {
		t.Fatalf("security=none should not set TLS: %+v", node)
	}
}

func TestParseSSLink(t *testing.T) {
	userinfo := b64("aes-256-gcm:passw0rd")
	link := "ss://" + userinfo + "@s.example.com:8388#ss-node"
	node, err := parseShareLink(link)
	if err != nil {
		t.Fatalf("ss: %v", err)
	}
	if node.Protocol != "ss" || node.Security != "aes-256-gcm" || node.UUID != "passw0rd" {
		t.Fatalf("unexpected ss node: %+v", node)
	}
	if node.Address != "s.example.com" || node.Port != 8388 || node.Name != "ss-node" {
		t.Fatalf("ss address/name wrong: %+v", node)
	}
}

func TestParseShareLinkRejectsGarbage(t *testing.T) {
	for _, link := range []string{"", "http://example.com", "vmess://not-base64!!", "vless://"} {
		if _, err := parseShareLink(link); err == nil {
			t.Errorf("expected error for %q", link)
		}
	}
}

func TestBuildV2RayConfig(t *testing.T) {
	cfg := config.Default().VPN
	cfg.SocksPort = 1080
	cfg.HTTPPort = 1081
	cfg.AllowLAN = false
	cfg.LogPath = "/tmp/v2ray-error.log"

	node := state.VPNNode{
		Protocol: "vmess",
		Address:  "hk.example.com",
		Port:     443,
		UUID:     "b831381d-6324-4d53-ad4f-8cda48b30811",
		Network:  "ws",
		Path:     "/ws",
		Host:     "hk.example.com",
		TLS:      true,
		SNI:      "hk.example.com",
	}
	raw, err := buildV2RayConfig(cfg, &node)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("generated config is not valid JSON: %v", err)
	}

	inbounds := doc["inbounds"].([]any)
	if len(inbounds) != 2 {
		t.Fatalf("expected socks+http inbounds, got %d", len(inbounds))
	}
	socks := inbounds[0].(map[string]any)
	if socks["protocol"] != "socks" || socks["listen"] != "127.0.0.1" {
		t.Fatalf("socks inbound wrong: %+v", socks)
	}

	outbounds := doc["outbounds"].([]any)
	proxy := outbounds[0].(map[string]any)
	if proxy["protocol"] != "vmess" || proxy["tag"] != "proxy" {
		t.Fatalf("proxy outbound wrong: %+v", proxy)
	}
	stream := proxy["streamSettings"].(map[string]any)
	if stream["network"] != "ws" || stream["security"] != "tls" {
		t.Fatalf("stream settings wrong: %+v", stream)
	}
	ws := stream["wsSettings"].(map[string]any)
	if ws["path"] != "/ws" {
		t.Fatalf("ws path wrong: %+v", ws)
	}

	// No node means direct-only: the config must still be valid.
	raw, err = buildV2RayConfig(cfg, nil)
	if err != nil {
		t.Fatalf("build without node: %v", err)
	}
	if !strings.Contains(string(raw), `"direct"`) {
		t.Fatalf("expected direct outbound in %s", raw)
	}
}

// TestDumpVPNConfigMatrix writes one config per protocol/transport combination
// so they can be checked with "v2ray test" on the device. It is skipped unless
// VPN_DUMP_DIR is set.
func TestDumpVPNConfigMatrix(t *testing.T) {
	dir := os.Getenv("VPN_DUMP_DIR")
	if dir == "" {
		t.Skip("set VPN_DUMP_DIR to dump the config matrix")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default().VPN
	cfg.RoutingMode = "global"
	cfg.AllowLAN = false

	const uuid = "b831381d-6324-4d53-ad4f-8cda48b30811"
	// 43 base64url chars = the 32-byte x25519 public key REALITY expects.
	const pbk = "jNXHt1yRo0vDuchQlIP6Z0ZvjT3KtzVI-T4E7RoLJS0"
	cases := []struct {
		name string
		node state.VPNNode
	}{
		{"vmess-tcp-tls", state.VPNNode{Protocol: "vmess", Address: "a.example.com", Port: 443, UUID: uuid, Network: "tcp", TLS: true, SNI: "a.example.com"}},
		{"vmess-ws-tls", state.VPNNode{Protocol: "vmess", Address: "a.example.com", Port: 443, UUID: uuid, Network: "ws", Path: "/ws", Host: "a.example.com", TLS: true, SNI: "a.example.com"}},
		{"vmess-h2-tls", state.VPNNode{Protocol: "vmess", Address: "a.example.com", Port: 443, UUID: uuid, Network: "h2", Path: "/h2", Host: "a.example.com", TLS: true, SNI: "a.example.com"}},
		{"vless-ws-tls", state.VPNNode{Protocol: "vless", Address: "b.example.com", Port: 443, UUID: uuid, Security: "none", Flow: "xtls-rprx-vision", Network: "ws", Path: "/vl", Host: "b.example.com", TLS: true, SNI: "b.example.com"}},
		{"vless-grpc-tls", state.VPNNode{Protocol: "vless", Address: "b.example.com", Port: 443, UUID: uuid, Security: "none", Network: "grpc", Path: "gsvc", TLS: true, SNI: "b.example.com"}},
		{"vless-ws-reality", state.VPNNode{Protocol: "vless", Address: "c.example.com", Port: 443, UUID: uuid, Security: "none", Network: "ws", Path: "/r", Host: "c.example.com", TLS: true, SNI: "www.microsoft.com", RealityPublicKey: pbk, RealityShortID: "ab12"}},
		{"trojan-ws-tls", state.VPNNode{Protocol: "trojan", Address: "d.example.com", Port: 443, UUID: "secretpass", Network: "ws", Path: "/tj", Host: "d.example.com", TLS: true, SNI: "d.example.com"}},
		{"trojan-tcp-tls", state.VPNNode{Protocol: "trojan", Address: "d.example.com", Port: 8443, UUID: "secretpass", Network: "tcp", TLS: true, SNI: "d.example.com"}},
		{"ss-tcp", state.VPNNode{Protocol: "ss", Address: "e.example.com", Port: 8388, UUID: "passw0rd", Security: "aes-256-gcm", Network: "tcp"}},
		{"ss-ws", state.VPNNode{Protocol: "ss", Address: "e.example.com", Port: 8389, UUID: "passw0rd", Security: "chacha20-poly1305", Network: "ws", Path: "/ss", Host: "e.example.com"}},
	}
	for _, c := range cases {
		node := c.node
		doc, err := buildV2RayConfig(cfg, &node)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, c.name+".json"), doc, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("wrote %d configs to %s", len(cases), dir)
}

// TestParseVPNLive checks the "active config" summary, including a v2rayN-style
// file the page did not generate itself.
func TestParseVPNLive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	doc := `{
	  "log": {"loglevel": "warning"},
	  "inbounds": [
	    {"tag": "socks", "listen": "0.0.0.0", "port": 10808, "protocol": "mixed"}
	  ],
	  "outbounds": [
	    {"tag": "proxy", "protocol": "vmess",
	     "settings": {"vnext": [{"address": "hk.example.com", "port": 443,
	       "users": [{"id": "x", "alterId": 0, "security": "auto"}]}]},
	     "streamSettings": {"network": "ws", "security": "tls",
	       "tlsSettings": {"serverName": "hk.example.com"},
	       "wsSettings": {"path": "/ws", "headers": {"Host": "si.example.com"}}}},
	    {"tag": "direct", "protocol": "freedom"},
	    {"tag": "block", "protocol": "blackhole"}
	  ],
	  "routing": {"rules": [
	    {"type": "field", "outboundTag": "proxy", "domain": ["geosite:google"]},
	    {"type": "field", "outboundTag": "direct", "ip": ["geoip:private"]}
	  ]}
	}`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	live := parseVPNLive(path)
	if !live.Valid {
		t.Fatalf("expected valid, got %+v", live)
	}
	if len(live.Inbounds) != 1 || live.Inbounds[0].Port != 10808 || live.Inbounds[0].Protocol != "mixed" {
		t.Fatalf("inbounds = %+v", live.Inbounds)
	}
	o := live.Outbound
	if o == nil {
		t.Fatal("outbound not parsed")
	}
	if o.Protocol != "vmess" || o.Address != "hk.example.com" || o.Port != 443 {
		t.Fatalf("outbound = %+v", o)
	}
	if o.Network != "ws" || o.Security != "tls" || o.Path != "/ws" || o.Host != "si.example.com" || o.SNI != "hk.example.com" {
		t.Fatalf("stream = %+v", o)
	}
	if !live.Smart || live.RuleCount != 2 {
		t.Fatalf("routing: smart=%v rules=%d", live.Smart, live.RuleCount)
	}
}

func TestParseVPNLiveReality(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	doc := `{
	  "inbounds": [{"port": 1080, "protocol": "socks"}],
	  "outbounds": [
	    {"tag": "proxy", "protocol": "vless",
	     "settings": {"vnext": [{"address": "1.2.3.4", "port": 443,
	       "users": [{"id": "u", "encryption": "none", "flow": "xtls-rprx-vision"}]}]},
	     "streamSettings": {"network": "tcp", "security": "reality",
	       "realitySettings": {"serverName": "www.microsoft.com", "publicKey": "pk"}}}
	  ]
	}`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	live := parseVPNLive(path)
	if live.Outbound == nil || live.Outbound.Security != "reality" || live.Outbound.SNI != "www.microsoft.com" {
		t.Fatalf("reality outbound = %+v", live.Outbound)
	}
	if live.Outbound.Flow != "xtls-rprx-vision" {
		t.Fatalf("flow = %q", live.Outbound.Flow)
	}
}

// TestNodeFromLiveConfig ensures a config pasted from v2rayN can be adopted into
// the node list, including the UUID and the "host" form of wsSettings.
func TestNodeFromLiveConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	doc := `{
	  "inbounds": [{"port": 1080, "protocol": "socks"}],
	  "outbounds": [
	    {"tag": "proxy", "protocol": "vmess",
	     "settings": {"vnext": [{"address": "node.example.net", "port": 443,
	       "users": [{"id": "11111111-2222-3333-4444-555555555555", "alterId": 0, "security": "auto"}]}]},
	     "streamSettings": {"network": "ws", "security": "tls",
	       "tlsSettings": {"serverName": "node.example.net"},
	       "wsSettings": {"path": "/ws", "host": "cdn.example.net"}}},
	    {"tag": "direct", "protocol": "freedom"}
	  ]
	}`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	n, ok := nodeFromLiveConfig(path)
	if !ok {
		t.Fatal("nodeFromLiveConfig returned false")
	}
	if n.Protocol != "vmess" || n.Address != "node.example.net" || n.Port != 443 {
		t.Fatalf("node = %+v", n)
	}
	if n.UUID != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("uuid = %q", n.UUID)
	}
	if !n.TLS || n.Network != "ws" || n.Path != "/ws" || n.Host != "cdn.example.net" || n.SNI != "node.example.net" {
		t.Fatalf("stream fields = %+v", n)
	}
	if err := normalizeVPNNode(&n); err != nil {
		t.Fatalf("adopted node failed validation: %v", err)
	}
}

func TestMaskSecret(t *testing.T) {
	if got := maskSecret(""); got != "" {
		t.Errorf("empty = %q", got)
	}
	if got := maskSecret("12345678"); got != "****" {
		t.Errorf("short = %q", got)
	}
	got := maskSecret("b831381d-6324-4d53-ad4f-8cda48b30811")
	if strings.Contains(got, "63324") || !strings.Contains(got, "…") {
		t.Errorf("mask = %q", got)
	}
}

// TestNormalizeVPNNode covers the manual "add server" form validation.
func TestNormalizeVPNNode(t *testing.T) {
	ok := state.VPNNode{Protocol: "VMESS", Address: " a.example.com ", Port: 443, UUID: "uuid-1"}
	if err := normalizeVPNNode(&ok); err != nil {
		t.Fatalf("valid node rejected: %v", err)
	}
	if ok.Protocol != "vmess" || ok.Address != "a.example.com" || ok.Network != "tcp" {
		t.Fatalf("normalize wrong: %+v", ok)
	}
	if ok.Name != "a.example.com:443" {
		t.Fatalf("default name = %q", ok.Name)
	}

	bad := []state.VPNNode{
		{Protocol: "http", Address: "a.com", Port: 1, UUID: "u"},
		{Protocol: "vmess", Address: "", Port: 1, UUID: "u"},
		{Protocol: "vmess", Address: "a b", Port: 1, UUID: "u"},
		{Protocol: "vmess", Address: "a.com", Port: 0, UUID: "u"},
		{Protocol: "vmess", Address: "a.com", Port: 70000, UUID: "u"},
		{Protocol: "vmess", Address: "a.com", Port: 1},
		{Protocol: "vmess", Address: "a.com", Port: 1, UUID: "u", Network: "quic"},
	}
	for i, n := range bad {
		if err := normalizeVPNNode(&n); err == nil {
			t.Errorf("case %d should have failed: %+v", i, n)
		}
	}
}
