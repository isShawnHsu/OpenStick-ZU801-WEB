// Package parse contains pure parsers for the command output used by uz801d.
package parse

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"uz801d/internal/netutil"
)

// ---------------------------------------------------------------- ModemManager

// ModemInfo is a flattened view of `mmcli -m N -J`.
type ModemInfo struct {
	Index            string   `json:"index"`
	Manufacturer     string   `json:"manufacturer"`
	Model            string   `json:"model"`
	Revision         string   `json:"revision"`
	IMEI             string   `json:"imei"`
	State            string   `json:"state"`
	FailedReason     string   `json:"failed_reason"`
	OperatorName     string   `json:"operator_name"`
	OperatorCode     string   `json:"operator_code"`
	Registration     string   `json:"registration"`
	PacketService    string   `json:"packet_service"`
	AccessTech       []string `json:"access_tech"`
	SignalQuality    int      `json:"signal_quality"`
	CurrentModes     string   `json:"current_modes"`
	CurrentCaps      []string `json:"current_capabilities"`
	SupportedModes   []string `json:"supported_modes"`
	UnlockRequired   string   `json:"unlock_required"`
	UnlockRetries    string   `json:"unlock_retries"`
	SimPath          string   `json:"sim_path"`
	OwnNumbers       []string `json:"own_numbers"`
	Bearers          []string `json:"bearers"`
	APN              string   `json:"apn"`
	Device           string   `json:"device"`
	PowerState       string   `json:"power_state"`
	HardwareRevision string   `json:"hardware_revision"`
	Plugin           string   `json:"plugin"`
}

// SignalValues contains extended signal measurements.
type SignalValues struct {
	Tech string  `json:"tech"`
	RSSI float64 `json:"rssi"`
	RSRP float64 `json:"rsrp"`
	RSRQ float64 `json:"rsrq"`
	SNR  float64 `json:"snr"`
}

// SIMInfo is a flattened view of `mmcli -i N -J`.
type SIMInfo struct {
	Present      bool   `json:"present"`
	Active       bool   `json:"active"`
	IMSI         string `json:"imsi"`
	ICCID        string `json:"iccid"`
	OperatorName string `json:"operator_name"`
	OperatorCode string `json:"operator_code"`
}

// SMSInfo is a flattened view of `mmcli -s N -J`.
type SMSInfo struct {
	ID        string `json:"id"`
	Number    string `json:"number"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp"`
	State     string `json:"state"`
	Storage   string `json:"storage"`
	PDUType   string `json:"pdu_type"`
	Data      bool   `json:"data"`
}

func decodeMap(data []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case json.Number:
		return t.String()
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "yes"
		}
		return "no"
	}
	return ""
}

func asFloat(v any) float64 {
	s := asString(v)
	if s == "" {
		return 0
	}
	f, _ := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(s, " dBm")), 64)
	return f
}

func asInt(v any) int {
	f := asFloat(v)
	return int(f)
}

func asStringSlice(v any) []string {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			s := asString(e)
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return t
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	}
	return nil
}

func get(m map[string]any, path ...string) any {
	var cur any = m
	for _, key := range path {
		mm := asMap(cur)
		if mm == nil {
			return nil
		}
		cur = mm[key]
	}
	return cur
}

// findKey recursively searches maps for the first matching key.
func findKey(v any, keys ...string) any {
	var walk func(any, int) any
	walk = func(cur any, depth int) any {
		if depth > 8 || cur == nil {
			return nil
		}
		if m, ok := cur.(map[string]any); ok {
			for _, k := range keys {
				if val, ok := m[k]; ok {
					if val != nil {
						return val
					}
				}
			}
			for _, val := range m {
				if found := walk(val, depth+1); found != nil {
					return found
				}
			}
		}
		if arr, ok := cur.([]any); ok {
			for _, val := range arr {
				if found := walk(val, depth+1); found != nil {
					return found
				}
			}
		}
		return nil
	}
	return walk(v, 0)
}

// ParseModemList parses `mmcli -L -J`.
func ParseModemList(data []byte) ([]string, error) {
	m, err := decodeMap(data)
	if err != nil {
		return nil, err
	}
	if v, ok := m["modem-list"]; ok {
		return asStringSlice(v), nil
	}
	return nil, fmt.Errorf("mmcli 输出中没有 modem-list")
}

// ParseModem parses `mmcli -m N -J`.
func ParseModem(data []byte) (*ModemInfo, error) {
	m, err := decodeMap(data)
	if err != nil {
		return nil, err
	}
	modem := asMap(m["modem"])
	if modem == nil {
		return nil, fmt.Errorf("mmcli 输出中没有 modem 对象")
	}
	g := asMap(modem["generic"])
	t := asMap(modem["3gpp"])
	if g == nil {
		return nil, fmt.Errorf("mmcli 输出中没有 modem.generic")
	}
	info := &ModemInfo{
		Manufacturer:     asString(g["manufacturer"]),
		Model:            asString(g["model"]),
		Revision:         asString(g["revision"]),
		HardwareRevision: asString(g["hardware-revision"]),
		State:            asString(g["state"]),
		FailedReason:     asString(g["state-failed-reason"]),
		OperatorName:     asString(t["operator-name"]),
		OperatorCode:     asString(t["operator-code"]),
		Registration:     asString(t["registration-state"]),
		PacketService:    asString(t["packet-service-state"]),
		AccessTech:       asStringSlice(g["access-technologies"]),
		SignalQuality:    asInt(get(g, "signal-quality", "value")),
		CurrentCaps:      asStringSlice(g["current-capabilities"]),
		SupportedModes:   asStringSlice(g["supported-modes"]),
		UnlockRequired:   asString(g["unlock-required"]),
		SimPath:          asString(g["sim"]),
		OwnNumbers:       asStringSlice(g["own-numbers"]),
		Bearers:          asStringSlice(g["bearers"]),
		Device:           asString(g["device"]),
		PowerState:       asString(g["power-state"]),
		Plugin:           asString(g["plugin"]),
	}
	info.IMEI = asString(t["imei"])
	if info.IMEI == "" {
		info.IMEI = asString(g["equipment-identifier"])
	}
	// current-modes is a plain string in ModemManager, but be tolerant.
	switch v := g["current-modes"].(type) {
	case string:
		info.CurrentModes = strings.TrimSpace(v)
	case []any:
		info.CurrentModes = strings.Join(asStringSlice(v), ", ")
	}
	// Packet service state has moved between MM versions.
	if info.PacketService == "" {
		info.PacketService = asString(g["packet-service-state"])
	}
	if info.Registration == "" {
		info.Registration = asString(g["registration-state"])
	}
	// Initial bearer settings may expose the APN.
	if apn := asString(get(t, "initial-bearer", "settings", "apn")); apn != "" {
		info.APN = apn
	}
	if info.APN == "" {
		if apn := asString(findKey(modem, "apn")); apn != "" {
			info.APN = apn
		}
	}
	// Unlock retries: "sim-pin (3)".
	if retries := asStringSlice(g["unlock-retries"]); len(retries) > 0 {
		info.UnlockRetries = strings.Join(retries, ", ")
	}
	if len(info.OwnNumbers) > 0 {
		info.OwnNumbers[0] = NormalizePhone(info.OwnNumbers[0])
	}
	return info, nil
}

// ParseSIM parses `mmcli -i N -J`.
func ParseSIM(data []byte) (*SIMInfo, error) {
	m, err := decodeMap(data)
	if err != nil {
		return nil, err
	}
	sim := asMap(m["sim"])
	if sim == nil {
		return nil, fmt.Errorf("mmcli 输出中没有 sim 对象")
	}
	props := asMap(sim["properties"])
	if props == nil {
		props = sim
	}
	info := &SIMInfo{
		Present:      true,
		Active:       asString(props["active"]) == "yes" || asString(props["active"]) == "true",
		IMSI:         asString(props["imsi"]),
		ICCID:        asString(props["iccid"]),
		OperatorName: asString(props["operator-name"]),
		OperatorCode: asString(props["operator-code"]),
	}
	if info.ICCID == "" {
		info.ICCID = asString(props["sim-identifier"])
	}
	if info.IMSI == "" {
		info.IMSI = asString(props["imsi"])
	}
	return info, nil
}

// ParseSignal parses `mmcli -m N --signal-get -J`.
func ParseSignal(data []byte) SignalValues {
	m, err := decodeMap(data)
	if err != nil {
		return SignalValues{}
	}
	sig := asMap(get(m, "modem", "signal"))
	if sig == nil {
		return SignalValues{}
	}
	// Prefer the most advanced technology available.
	order := []string{"5gnr", "lte", "umts", "gsm", "cdma"}
	for _, tech := range order {
		tm := asMap(sig[tech])
		if tm == nil {
			continue
		}
		sv := SignalValues{
			Tech: tech,
			RSSI: asFloat(tm["rssi"]),
			RSRP: asFloat(tm["rsrp"]),
			RSRQ: asFloat(tm["rsrq"]),
			SNR:  asFloat(tm["snr"]),
		}
		if sv.RSSI != 0 || sv.RSRP != 0 {
			return sv
		}
	}
	return SignalValues{}
}

// ParseSMSList parses `mmcli -m N --messaging-list-sms -J`.
func ParseSMSList(data []byte) ([]string, error) {
	m, err := decodeMap(data)
	if err != nil {
		return nil, err
	}
	// Newer mmcli uses a flat key, older versions nest it.
	if v, ok := m["modem.messaging.sms"]; ok {
		return asStringSlice(v), nil
	}
	if v := get(m, "modem", "messaging", "sms"); v != nil {
		return asStringSlice(v), nil
	}
	// An empty list is represented by a missing/empty key.
	return nil, nil
}

// ParseSMS parses `mmcli -s N -J`.
func ParseSMS(data []byte) (*SMSInfo, error) {
	m, err := decodeMap(data)
	if err != nil {
		return nil, err
	}
	sms := asMap(m["sms"])
	if sms == nil {
		return nil, fmt.Errorf("mmcli 输出中没有 sms 对象")
	}
	content := asMap(sms["content"])
	props := asMap(sms["properties"])
	if content == nil {
		content = map[string]any{}
	}
	if props == nil {
		props = map[string]any{}
	}
	getAny := func(keys ...string) string {
		for _, k := range keys {
			if v := asString(content[k]); v != "" {
				return v
			}
			if v := asString(props[k]); v != "" {
				return v
			}
			if v := asString(sms[k]); v != "" {
				return v
			}
		}
		return ""
	}
	info := &SMSInfo{
		Number:    getAny("number"),
		Text:      getAny("text"),
		Timestamp: getAny("timestamp", "discharge-timestamp"),
		State:     getAny("state"),
		Storage:   getAny("storage"),
		PDUType:   getAny("pdu-type"),
	}
	if info.Text == "" {
		if dataArr := sms["data"]; dataArr != nil {
			info.Data = true
		}
	}
	info.Number = NormalizePhone(info.Number)
	return info, nil
}

// NormalizePhone removes spaces/dashes and keeps a leading plus.
func NormalizePhone(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for i, r := range s {
		if r == '+' && i == 0 {
			b.WriteRune(r)
			continue
		}
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Operator is one result of `mmcli --3gpp-scan -J`.
type Operator struct {
	Name   string   `json:"name"`
	Code   string   `json:"code"`
	Status string   `json:"status"`
	Tech   []string `json:"tech"`
}

// ParseOperatorScan parses the (inconsistently shaped) network scan output.
func ParseOperatorScan(data []byte) []Operator {
	m, err := decodeMap(data)
	if err != nil {
		return nil
	}
	var raw any
	for _, path := range [][]string{
		{"modem", "3gpp", "scan"},
		{"modem", "3gpp", "network-scan"},
	} {
		if v := get(m, path...); v != nil {
			raw = v
			break
		}
	}
	if raw == nil {
		if v, ok := m["modem.3gpp.scan"]; ok {
			raw = v
		}
	}
	var list []any
	switch t := raw.(type) {
	case []any:
		list = t
	case map[string]any:
		list = []any{t}
	}
	var out []Operator
	for _, item := range list {
		im := asMap(item)
		if im == nil {
			continue
		}
		op := Operator{
			Name:   asString(im["operator-name"]),
			Code:   asString(im["operator-code"]),
			Status: asString(im["status"]),
			Tech:   asStringSlice(im["access-technologies"]),
		}
		if op.Name == "" && op.Code == "" {
			continue
		}
		out = append(out, op)
	}
	return out
}

// ---------------------------------------------------------------- hostapd

// HostapdConfig is a line-preserving hostapd.conf editor.
type HostapdConfig struct {
	Lines []string
}

// ParseHostapd splits a hostapd config into lines.
func ParseHostapd(data []byte) *HostapdConfig {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return &HostapdConfig{}
	}
	return &HostapdConfig{Lines: strings.Split(text, "\n")}
}

// Get returns the first value for key.
func (h *HostapdConfig) Get(key string) string {
	for _, line := range h.Lines {
		k, v, ok := splitKV(line)
		if ok && k == key {
			return v
		}
	}
	return ""
}

// Set replaces the first value for key, removing later duplicates, or appends it.
func (h *HostapdConfig) Set(key, value string) {
	replaced := false
	out := h.Lines[:0]
	for _, line := range h.Lines {
		k, _, ok := splitKV(line)
		if ok && k == key {
			if replaced {
				continue
			}
			out = append(out, key+"="+value)
			replaced = true
			continue
		}
		out = append(out, line)
	}
	if !replaced {
		out = append(out, key+"="+value)
	}
	h.Lines = out
}

// Delete removes all lines for key.
func (h *HostapdConfig) Delete(key string) {
	out := h.Lines[:0]
	for _, line := range h.Lines {
		if k, _, ok := splitKV(line); ok && k == key {
			continue
		}
		out = append(out, line)
	}
	h.Lines = out
}

// Bytes renders the config with a trailing newline.
func (h *HostapdConfig) Bytes() []byte {
	if len(h.Lines) == 0 {
		return nil
	}
	return []byte(strings.Join(h.Lines, "\n") + "\n")
}

func splitKV(line string) (string, string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", false
	}
	idx := strings.Index(trimmed, "=")
	if idx <= 0 {
		return "", "", false
	}
	return strings.TrimSpace(trimmed[:idx]), strings.TrimSpace(trimmed[idx+1:]), true
}

// ---------------------------------------------------------------- DHCP leases

// Lease is one dnsmasq lease entry.
type Lease struct {
	Expiry   int64  `json:"expiry"`
	MAC      string `json:"mac"`
	IP       string `json:"ip"`
	Hostname string `json:"hostname"`
	ClientID string `json:"client_id"`
}

// ParseLeases parses a dnsmasq leases file.
func ParseLeases(data []byte) []Lease {
	var out []Lease
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		mac, err := netutil.NormalizeMAC(fields[1])
		if err != nil {
			continue
		}
		exp, _ := strconv.ParseInt(fields[0], 10, 64)
		host := fields[3]
		if host == "*" {
			host = ""
		}
		l := Lease{Expiry: exp, MAC: mac, IP: fields[2], Hostname: host}
		if len(fields) > 4 && fields[4] != "*" {
			l.ClientID = fields[4]
		}
		out = append(out, l)
	}
	return out
}

// ---------------------------------------------------------------- iw

// Station is one wireless station from `iw dev wlan0 station dump`.
type Station struct {
	MAC              string  `json:"mac"`
	Signal           float64 `json:"signal"`
	SignalAvg        float64 `json:"signal_avg"`
	InactiveMS       int64   `json:"inactive_ms"`
	RXBytes          int64   `json:"rx_bytes"`
	TXBytes          int64   `json:"tx_bytes"`
	RXPackets        int64   `json:"rx_packets"`
	TXPackets        int64   `json:"tx_packets"`
	ConnectedSeconds int64   `json:"connected_seconds"`
	Authorized       bool    `json:"authorized"`
	Authenticated    bool    `json:"authenticated"`
	Associated       bool    `json:"associated"`
	TXBitrateMbps    float64 `json:"tx_bitrate_mbps"`
	RXBitrateMbps    float64 `json:"rx_bitrate_mbps"`
}

var stationStartRe = regexp.MustCompile(`^Station\s+([0-9a-fA-F:]{17})`)

// ParseStations parses `iw dev <if> station dump`.
func ParseStations(output string) map[string]Station {
	out := map[string]Station{}
	var cur *Station
	flush := func() {
		if cur != nil && cur.MAC != "" {
			out[cur.MAC] = *cur
		}
		cur = nil
	}
	for _, raw := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			flush()
			continue
		}
		if m := stationStartRe.FindStringSubmatch(line); m != nil {
			flush()
			mac, err := netutil.NormalizeMAC(m[1])
			if err != nil {
				continue
			}
			cur = &Station{MAC: mac}
			continue
		}
		if cur == nil {
			continue
		}
		key, val, ok := splitIW(line)
		if !ok {
			continue
		}
		switch key {
		case "signal":
			cur.Signal = firstFloat(val)
		case "signal avg":
			cur.SignalAvg = firstFloat(val)
		case "inactive time":
			cur.InactiveMS = firstInt(val)
		case "rx bytes":
			cur.RXBytes = firstInt(val)
		case "tx bytes":
			cur.TXBytes = firstInt(val)
		case "rx packets":
			cur.RXPackets = firstInt(val)
		case "tx packets":
			cur.TXPackets = firstInt(val)
		case "connected time":
			cur.ConnectedSeconds = firstInt(val)
		case "authorized":
			cur.Authorized = val == "yes"
		case "authenticated":
			cur.Authenticated = val == "yes"
		case "associated":
			cur.Associated = val == "yes"
		case "tx bitrate":
			cur.TXBitrateMbps = bitrateMbps(val)
		case "rx bitrate":
			cur.RXBitrateMbps = bitrateMbps(val)
		}
	}
	flush()
	return out
}

func splitIW(line string) (string, string, bool) {
	idx := strings.Index(line, ":")
	if idx <= 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx+1:]), true
}

func firstFloat(s string) float64 {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return 0
	}
	f, _ := strconv.ParseFloat(fields[0], 64)
	return f
}

func firstInt(s string) int64 {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return 0
	}
	v, _ := strconv.ParseInt(fields[0], 10, 64)
	return v
}

func bitrateMbps(s string) float64 {
	fields := strings.Fields(s)
	if len(fields) < 2 {
		return 0
	}
	v, _ := strconv.ParseFloat(fields[0], 64)
	switch strings.ToLower(fields[1]) {
	case "gbps":
		return v * 1000
	case "kbps":
		return v / 1000
	default:
		return v
	}
}

// ---------------------------------------------------------------- ip

// IfaceAddr is one IPv4 address from `ip -o -4 addr`.
type IfaceAddr struct {
	Index     int    `json:"index"`
	Iface     string `json:"iface"`
	Address   string `json:"address"`
	Prefix    int    `json:"prefix"`
	Broadcast string `json:"broadcast"`
	Scope     string `json:"scope"`
}

var ipAddrRe = regexp.MustCompile(`^\d+:\s+(\S+)\s+inet\s+([0-9.]+)/(\d+)(?:\s+brd\s+([0-9.]+))?`)

// ParseIPAddr parses `ip -o -4 addr show`.
func ParseIPAddr(output string) []IfaceAddr {
	var out []IfaceAddr
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		m := ipAddrRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		prefix, _ := strconv.Atoi(m[3])
		a := IfaceAddr{Iface: m[1], Address: m[2], Prefix: prefix, Broadcast: m[4]}
		if idx := strings.Index(line, "scope "); idx >= 0 {
			if f := strings.Fields(line[idx+6:]); len(f) > 0 {
				a.Scope = f[0]
			}
		}
		out = append(out, a)
	}
	return out
}

// ParseDefaultRoute extracts the default gateway and interface.
func ParseDefaultRoute(output string) (gateway, iface string) {
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 5 && fields[0] == "default" && fields[1] == "via" {
			gateway = fields[2]
			for i := 3; i+1 < len(fields); i++ {
				if fields[i] == "dev" {
					iface = fields[i+1]
					break
				}
			}
			return
		}
	}
	return
}

// ---------------------------------------------------------------- iw list / iw dev

// IfaceCombo is one "valid interface combinations" entry from `iw list`.
type IfaceCombo struct {
	Raw      string
	AP       bool
	Managed  bool
	Channels int // 0 = unspecified
	Total    int
}

// ParseIfaceCombos parses the valid interface combinations block of `iw list`.
func ParseIfaceCombos(text string) []IfaceCombo {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	start := -1
	for i, line := range lines {
		if strings.Contains(line, "valid interface combinations:") {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return nil
	}
	var chunks []string
	var cur strings.Builder
	flush := func() {
		if strings.TrimSpace(cur.String()) != "" {
			chunks = append(chunks, cur.String())
		}
		cur.Reset()
	}
	for _, line := range lines[start:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if cur.Len() > 0 {
				flush()
			}
			continue
		}
		if strings.HasPrefix(trimmed, "*") {
			flush()
			cur.WriteString(trimmed)
			continue
		}
		if cur.Len() > 0 && !strings.Contains(trimmed, ":") {
			cur.WriteString(" ")
			cur.WriteString(trimmed)
			continue
		}
		if cur.Len() > 0 {
			flush()
		}
		break
	}
	flush()

	groupRe := regexp.MustCompile(`#\{\s*([^}]+)\s*\}\s*<=\s*(\d+)`)
	chanRe := regexp.MustCompile(`#channels\s*<=\s*(\d+)`)
	totalRe := regexp.MustCompile(`total\s*<=\s*(\d+)`)
	var out []IfaceCombo
	for _, chunk := range chunks {
		c := IfaceCombo{Raw: chunk}
		for _, m := range groupRe.FindAllStringSubmatch(chunk, -1) {
			names := strings.Split(m[1], ",")
			for _, n := range names {
				n = strings.TrimSpace(n)
				switch n {
				case "AP":
					c.AP = true
				case "managed", "station":
					c.Managed = true
				}
			}
		}
		if m := chanRe.FindStringSubmatch(chunk); m != nil {
			c.Channels, _ = strconv.Atoi(m[1])
		}
		if m := totalRe.FindStringSubmatch(chunk); m != nil {
			c.Total, _ = strconv.Atoi(m[1])
		}
		if c.AP || c.Managed {
			out = append(out, c)
		}
	}
	return out
}

// SupportsAPSTA reports whether the combinations allow managed + AP concurrency.
func SupportsAPSTA(combos []IfaceCombo) (bool, string) {
	if len(combos) == 0 {
		return false, "无法读取无线网卡的接口组合（iw list 无输出）"
	}
	for _, c := range combos {
		if c.AP && c.Managed {
			return true, ""
		}
	}
	return false, "无线网卡未声明 managed+AP 并发组合，单网卡 AP-STA 可能不可用"
}

// IWDev is one interface from `iw dev`.
type IWDev struct {
	Name string
	Type string
	Phy  string
}

// ParseIWDev parses `iw dev` output.
func ParseIWDev(text string) []IWDev {
	var out []IWDev
	phy := ""
	var cur *IWDev
	flush := func() {
		if cur != nil && cur.Name != "" {
			out = append(out, *cur)
		}
		cur = nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "phy#"):
			phy = strings.TrimPrefix(trimmed, "phy#")
		case strings.HasPrefix(trimmed, "Interface "):
			flush()
			cur = &IWDev{Name: strings.TrimSpace(strings.TrimPrefix(trimmed, "Interface ")), Phy: phy}
		case strings.HasPrefix(trimmed, "type ") && cur != nil:
			cur.Type = strings.TrimSpace(strings.TrimPrefix(trimmed, "type "))
		}
	}
	flush()
	return out
}

// IWInfo is the parsed `iw dev <if> info`.
type IWInfo struct {
	Interface string
	SSID      string
	Type      string
	Channel   int
	FreqMHz   int
	Address   string
}

// ParseIWInfo parses `iw dev <if> info`.
func ParseIWInfo(text string) IWInfo {
	var out IWInfo
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "Interface "):
			out.Interface = strings.TrimSpace(strings.TrimPrefix(trimmed, "Interface "))
		case strings.HasPrefix(trimmed, "ssid "):
			out.SSID = strings.TrimSpace(strings.TrimPrefix(trimmed, "ssid "))
		case strings.HasPrefix(trimmed, "type "):
			out.Type = strings.TrimSpace(strings.TrimPrefix(trimmed, "type "))
		case strings.HasPrefix(trimmed, "addr "):
			out.Address = strings.TrimSpace(strings.TrimPrefix(trimmed, "addr "))
		case strings.HasPrefix(trimmed, "channel "):
			fields := strings.Fields(strings.TrimPrefix(trimmed, "channel "))
			if len(fields) > 0 {
				out.Channel, _ = strconv.Atoi(fields[0])
			}
			if idx := strings.Index(trimmed, "("); idx > 0 {
				if idx2 := strings.Index(trimmed[idx:], " MHz"); idx2 > 0 {
					out.FreqMHz, _ = strconv.Atoi(strings.TrimSpace(trimmed[idx+1 : idx+idx2]))
				}
			}
		}
	}
	return out
}

// ---------------------------------------------------------------- nmcli

// WifiNetwork is one scan result.
type WifiNetwork struct {
	SSID     string `json:"ssid"`
	Signal   int    `json:"signal"`
	Security string `json:"security"` // open | wpa2 | wpa3 | wep | enterprise
	BSSID    string `json:"bssid"`
	Channel  int    `json:"channel"`
}

// SplitTerse splits nmcli -t output, honouring backslash escapes.
func SplitTerse(line string) []string {
	var out []string
	var cur strings.Builder
	escaped := false
	for _, r := range line {
		if escaped {
			cur.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == ':' {
			out = append(out, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteRune(r)
	}
	out = append(out, cur.String())
	return out
}

// ParseWifiList parses `nmcli -t -f SSID,SIGNAL,SECURITY,BSSID,CHAN device wifi list`.
func ParseWifiList(text string) []WifiNetwork {
	bySSID := map[string]WifiNetwork{}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := SplitTerse(line)
		if len(f) < 3 {
			continue
		}
		ssid := strings.TrimSpace(f[0])
		if ssid == "" || ssid == "--" {
			continue
		}
		signal, _ := strconv.Atoi(strings.TrimSpace(f[1]))
		sec := normalizeSecurity(f[2])
		n := WifiNetwork{SSID: ssid, Signal: signal, Security: sec}
		if len(f) > 3 {
			n.BSSID = strings.ReplaceAll(strings.TrimSpace(f[3]), "\\", "")
		}
		if len(f) > 4 {
			n.Channel, _ = strconv.Atoi(strings.TrimSpace(f[4]))
		}
		if old, ok := bySSID[ssid]; !ok || n.Signal > old.Signal {
			bySSID[ssid] = n
		}
	}
	out := make([]WifiNetwork, 0, len(bySSID))
	for _, n := range bySSID {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Signal > out[j].Signal })
	return out
}

func normalizeSecurity(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	switch {
	case s == "" || s == "--":
		return "open"
	case strings.Contains(s, "WPA3") || strings.Contains(s, "SAE"):
		return "wpa3"
	case strings.Contains(s, "802.1X") || strings.Contains(s, "EAP"):
		return "enterprise"
	case strings.Contains(s, "WEP"):
		return "wep"
	case strings.Contains(s, "WPA"):
		return "wpa2"
	default:
		return "unknown"
	}
}

// NMWifiConn is one NetworkManager WiFi connection.
type NMWifiConn struct {
	Name   string
	UUID   string
	Type   string
	Device string
	Mode   string
	SSID   string
	Active bool
}

// ParseNMConnections parses `nmcli -t -f NAME,UUID,TYPE,DEVICE connection show`.
func ParseNMConnections(text string) []NMWifiConn {
	var out []NMWifiConn
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := SplitTerse(line)
		if len(f) < 3 {
			continue
		}
		out = append(out, NMWifiConn{Name: f[0], UUID: f[1], Type: f[2], Device: field(f, 3)})
	}
	return out
}

func field(f []string, i int) string {
	if i < len(f) {
		return f[i]
	}
	return ""
}

// ParseNMDeviceStatus parses `nmcli -t -f DEVICE,TYPE,STATE,CONNECTION device status`.
func ParseNMDeviceStatus(text string) map[string][3]string {
	out := map[string][3]string{}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := SplitTerse(line)
		if len(f) < 4 {
			continue
		}
		out[f[0]] = [3]string{f[1], f[2], f[3]}
	}
	return out
}

// ParseResolvConf extracts nameserver addresses.
func ParseResolvConf(text string) []string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "nameserver" {
			out = append(out, fields[1])
		}
	}
	return out
}
