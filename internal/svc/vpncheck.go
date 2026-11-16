package svc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// VPNTestResult reports whether the proxy really works end to end.
type VPNTestResult struct {
	OK        bool   `json:"ok"`
	LatencyMS int64  `json:"latency_ms"`
	ExitIP    string `json:"exit_ip"`
	Country   string `json:"country"`
	Via       string `json:"via"`
	DirectOK  bool   `json:"direct_ok"`
	DirectMS  int64  `json:"direct_ms"`
	Error     string `json:"error,omitempty"`
}

// vpnTestTargets are queried through the proxy. Cloudflare trace returns the
// exit IP and country in a tiny plain-text body, ipify is the fallback.
var vpnTestTargets = []string{
	"https://www.cloudflare.com/cdn-cgi/trace",
	"https://api.ipify.org",
}

// VPNTest asks the local proxy for a page and reports the exit IP plus the
// round trip time, so "is the tunnel actually working" needs no SSH.
func (m *Manager) VPNTest(ctx context.Context) *VPNTestResult {
	cfg := m.Config().VPN
	res := &VPNTestResult{}
	running := m.unitActive(ctx, cfg.ServiceName)
	if !running {
		res.Error = "VPN 服务未运行"
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Prefer the HTTP inbound (1081) and fall back to SOCKS5 (1080), because a
	// config pasted from v2rayN may only expose one of them.
	type attempt struct {
		name  string
		build func() *http.Client
	}
	attempts := []attempt{
		{"HTTP", func() *http.Client { return proxyClientHTTP(cfg.HTTPPort) }},
		{"SOCKS5", func() *http.Client { return proxyClientSocks(cfg.SocksPort) }},
	}
	var lastErr error
	for _, a := range attempts {
		if !running {
			break
		}
		client := a.build()
		if client == nil {
			continue
		}
		// A 204 from gstatic measures reachability without downloading a body,
		// which keeps the latency number meaningful through a slow node.
		start := time.Now()
		if _, err := fetchSmall(ctx, client, []string{"https://www.gstatic.com/generate_204"}); err != nil {
			lastErr = err
			continue
		}
		res.OK = true
		res.Via = a.name
		res.LatencyMS = time.Since(start).Milliseconds()
		// The exit IP is informational; a failure here does not fail the test.
		if body, err := fetchSmall(ctx, client, vpnTestTargets); err == nil {
			res.ExitIP, res.Country = parseTrace(body)
		}
		client.CloseIdleConnections()
		break
	}
	if !res.OK && lastErr != nil {
		res.Error = lastErr.Error()
	}

	// Baseline: the device's own uplink (no proxy). This says whether a failure
	// is the tunnel's fault or the uplink's. The socket is marked so the
	// transparent proxy rules skip it, otherwise the probe would be proxied too.
	start := time.Now()
	dialer := &net.Dialer{Timeout: 8 * time.Second, Control: controlMark}
	direct := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{DialContext: dialer.DialContext},
	}
	if resp, err := direct.Get("https://www.baidu.com"); err == nil {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		res.DirectOK = resp.StatusCode == http.StatusOK
		res.DirectMS = time.Since(start).Milliseconds()
	}
	return res
}

// fetchSmall tries each target through the given client and returns the body.
func fetchSmall(ctx context.Context, client *http.Client, targets []string) (string, error) {
	var lastErr error
	for _, target := range targets {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", "msm8916d-vpntest")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
			lastErr = fmt.Errorf("%s 返回 HTTP %d", target, resp.StatusCode)
			continue
		}
		return string(body), nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("没有可用的测试地址")
	}
	return "", lastErr
}

// proxyClientHTTP routes requests through the local HTTP proxy inbound.
func proxyClientHTTP(port int) *http.Client {
	if port <= 0 {
		return nil
	}
	u := &url.URL{Scheme: "http", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(port))}
	return &http.Client{
		Timeout:   18 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyURL(u)},
	}
}

// proxyClientSocks routes requests through the local SOCKS5 inbound.
func proxyClientSocks(port int) *http.Client {
	if port <= 0 {
		return nil
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	dial := func(ctx context.Context, network, target string) (net.Conn, error) {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
		if err := socks5Handshake(conn, target); err != nil {
			conn.Close()
			return nil, err
		}
		return conn, nil
	}
	return &http.Client{
		Timeout: 18 * time.Second,
		Transport: &http.Transport{
			DialContext: dial,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}
}

// socks5Handshake performs a minimal CONNECT on an already open connection.
func socks5Handshake(conn net.Conn, target string) error {
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return err
	}
	buf := make([]byte, 2)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return err
	}
	if buf[0] != 0x05 || buf[1] != 0x00 {
		return fmt.Errorf("SOCKS5 认证方式不被接受")
	}
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return err
	}
	req := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			req = append(req, 0x01)
			req = append(req, v4...)
		} else {
			req = append(req, 0x04)
			req = append(req, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return fmt.Errorf("域名太长")
		}
		req = append(req, 0x03, byte(len(host)))
		req = append(req, host...)
	}
	req = append(req, byte(port>>8), byte(port))
	if _, err := conn.Write(req); err != nil {
		return err
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return err
	}
	if head[1] != 0x00 {
		return fmt.Errorf("SOCKS5 连接失败（错误码 %d）", head[1])
	}
	var skip int
	switch head[3] {
	case 0x01:
		skip = 4
	case 0x04:
		skip = 16
	case 0x03:
		one := make([]byte, 1)
		if _, err := io.ReadFull(conn, one); err != nil {
			return err
		}
		skip = int(one[0])
	default:
		return fmt.Errorf("SOCKS5 返回了未知地址类型")
	}
	rest := make([]byte, skip+2)
	_, err = io.ReadFull(conn, rest)
	return err
}

// parseTrace extracts ip= and loc= from the Cloudflare trace body, or plain
// text from ipify. It also accepts ipinfo style JSON.
func parseTrace(body string) (string, string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", ""
	}
	if strings.HasPrefix(body, "{") {
		var v struct {
			IP      string `json:"ip"`
			Country string `json:"country"`
		}
		if err := json.Unmarshal([]byte(body), &v); err == nil && v.IP != "" {
			return v.IP, strings.ToUpper(v.Country)
		}
		return "", ""
	}
	var ip, loc string
	for _, line := range strings.Split(body, "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "ip":
			ip = val
		case "loc":
			loc = val
		}
	}
	if ip == "" {
		// ipify answers with the bare address.
		if net.ParseIP(body) != nil {
			return body, ""
		}
	}
	return ip, loc
}
