package parse

import (
	"strings"
	"testing"
)

func TestParseModemList(t *testing.T) {
	data := []byte(`{"modem-list":["/org/freedesktop/ModemManager1/Modem/0"]}`)
	got, err := ParseModemList(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || !strings.HasSuffix(got[0], "/Modem/0") {
		t.Fatalf("unexpected list: %#v", got)
	}
}

func TestParseModem(t *testing.T) {
	data := []byte(`{
	  "modem": {
	    "generic": {
	      "manufacturer": "QUALCOMM",
	      "model": "UZ801",
	      "revision": "UZ801V1",
	      "state": "connected",
	      "signal-quality": {"value": 64},
	      "access-technologies": ["lte"],
	      "sim": "/org/freedesktop/ModemManager1/SIM/1",
	      "own-numbers": ["+86 138-0013-8000"],
	      "unlock-required": ""
	    },
	    "3gpp": {
	      "imei": "123456789012345",
	      "operator-name": "CHINA MOBILE",
	      "operator-code": "46000",
	      "registration-state": "home",
	      "packet-service-state": "connected",
	      "initial-bearer": {"settings": {"apn": "cmnet"}}
	    }
	  }
	}`)
	info, err := ParseModem(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Model != "UZ801" || info.State != "connected" {
		t.Fatalf("unexpected info: %#v", info)
	}
	if info.SignalQuality != 64 {
		t.Errorf("signal quality = %d, want 64", info.SignalQuality)
	}
	if info.OperatorName != "CHINA MOBILE" || info.OperatorCode != "46000" {
		t.Errorf("operator = %q/%q", info.OperatorName, info.OperatorCode)
	}
	if info.APN != "cmnet" {
		t.Errorf("apn = %q", info.APN)
	}
	if info.IMEI != "123456789012345" {
		t.Errorf("imei = %q", info.IMEI)
	}
	if len(info.OwnNumbers) != 1 || info.OwnNumbers[0] != "+8613800138000" {
		t.Errorf("own numbers = %#v", info.OwnNumbers)
	}
	if len(info.AccessTech) != 1 || info.AccessTech[0] != "lte" {
		t.Errorf("access tech = %#v", info.AccessTech)
	}
}

func TestParseSIMAndSignal(t *testing.T) {
	sim, err := ParseSIM([]byte(`{"sim":{"properties":{"active":"yes","imsi":"460001234567890","iccid":"89860012345678901234","operator-name":"CMCC"}}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sim.Active || sim.IMSI != "460001234567890" || sim.OperatorName != "CMCC" {
		t.Fatalf("unexpected sim: %#v", sim)
	}

	sig := ParseSignal([]byte(`{"modem":{"signal":{"lte":{"rssi":-60.0,"rsrp":-95.0,"rsrq":-12.0,"snr":9.5}}}}`))
	if sig.Tech != "lte" || sig.RSRP != -95 {
		t.Fatalf("unexpected signal: %#v", sig)
	}
}

func TestParseSMS(t *testing.T) {
	list, err := ParseSMSList([]byte(`{"modem":{"messaging":{"sms":["/org/freedesktop/ModemManager1/SMS/3"]}}}`))
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %#v, err = %v", list, err)
	}
	msg, err := ParseSMS([]byte(`{"sms":{"content":{"number":"10086","text":"hello","type":"text","data":null},"properties":{"pdu-type":"deliver","state":"received","storage":"me","timestamp":"2024-01-01T00:00:00+08:00"}}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Number != "10086" || msg.Text != "hello" || msg.Data {
		t.Fatalf("unexpected sms: %#v", msg)
	}
}

func TestParseOperatorScan(t *testing.T) {
	data := []byte(`{"modem":{"3gpp":{"scan":[{"operator-name":"CMCC","operator-code":"46000","status":"available","access-technologies":["lte"]}]}}}`)
	ops := ParseOperatorScan(data)
	if len(ops) != 1 || ops[0].Name != "CMCC" || ops[0].Code != "46000" {
		t.Fatalf("unexpected operators: %#v", ops)
	}
}

func TestNormalizePhone(t *testing.T) {
	cases := map[string]string{
		"+86 138-0013-8000": "+8613800138000",
		"10086":             "10086",
		" 138 0013 8000 ":   "13800138000",
	}
	for in, want := range cases {
		if got := NormalizePhone(in); got != want {
			t.Errorf("NormalizePhone(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseLeases(t *testing.T) {
	data := []byte("1700000000 aa:bb:cc:dd:ee:ff 192.168.68.100 phone 01:aa:bb:cc:dd:ee:ff\n1700000001 bb:cc:dd:ee:ff:00 192.168.68.101 * *\n")
	leases := ParseLeases(data)
	if len(leases) != 2 {
		t.Fatalf("got %d leases", len(leases))
	}
	if leases[0].MAC != "aa:bb:cc:dd:ee:ff" || leases[0].IP != "192.168.68.100" || leases[0].Hostname != "phone" {
		t.Errorf("unexpected lease: %#v", leases[0])
	}
	if leases[1].Hostname != "" {
		t.Errorf("expected empty hostname, got %q", leases[1].Hostname)
	}
}

func TestParseStations(t *testing.T) {
	out := `Station aa:bb:cc:dd:ee:ff (on uap0)
	signal: 	-52 dBm
	signal avg:	-54 dBm
	rx bytes:	1234
	tx bytes:	5678
	connected time:	3600 seconds
Station 11:22:33:44:55:66 (on uap0)
	signal: 	-70 dBm
	rx bytes:	10
	tx bytes:	20
`
	stations := ParseStations(out)
	if len(stations) != 2 {
		t.Fatalf("got %d stations", len(stations))
	}
	st, ok := stations["aa:bb:cc:dd:ee:ff"]
	if !ok {
		t.Fatalf("missing station: %#v", stations)
	}
	if st.Signal != -52 || st.RXBytes != 1234 || st.TXBytes != 5678 || st.ConnectedSeconds != 3600 {
		t.Errorf("unexpected station: %#v", st)
	}
}

func TestParseIPAddrAndRoute(t *testing.T) {
	out := "2: wlan0    inet 192.168.68.1/24 brd 192.168.68.255 scope global wlan0\n3: wwan0    inet 10.0.0.2/30 scope global wwan0\n"
	addrs := ParseIPAddr(out)
	if len(addrs) != 2 {
		t.Fatalf("got %d addrs: %#v", len(addrs), addrs)
	}
	if addrs[0].Iface != "wlan0" || addrs[0].Address != "192.168.68.1" || addrs[0].Prefix != 24 {
		t.Errorf("unexpected addr: %#v", addrs[0])
	}

	gw, iface := ParseDefaultRoute("default via 10.0.0.1 dev wwan0 proto static metric 100\n")
	if gw != "10.0.0.1" || iface != "wwan0" {
		t.Errorf("got %q via %q", gw, iface)
	}
}

func TestParseIfaceCombos(t *testing.T) {
	text := `	valid interface combinations:
		 * #{ managed } <= 2, #{ AP } <= 1,
		   total <= 3, #channels <= 1
`
	combos := ParseIfaceCombos(text)
	if len(combos) != 1 {
		t.Fatalf("got %d combos: %#v", len(combos), combos)
	}
	ok, reason := SupportsAPSTA(combos)
	if !ok {
		t.Fatalf("expected APSTA support, reason: %s", reason)
	}

	combos = ParseIfaceCombos(" * #{ managed } <= 1, total <= 2, #channels <= 1\n")
	if ok, _ := SupportsAPSTA(combos); ok {
		t.Fatal("expected unsupported for managed-only combos")
	}
}

func TestSplitTerse(t *testing.T) {
	got := SplitTerse(`My\:WiFi:wpa2:70:AA\:BB\:CC\:DD\:EE\:FF:6`)
	want := []string{"My:WiFi", "wpa2", "70", "AA:BB:CC:DD:EE:FF", "6"}
	if len(got) != len(want) {
		t.Fatalf("got %#v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("field %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseWifiList(t *testing.T) {
	text := "CMCC-WiFi:83:WPA2:AA\\:BB\\:CC\\:DD\\:EE\\:01:6\nCoffee:40::AA\\:BB\\:CC\\:DD\\:EE\\:02:11\n"
	list := ParseWifiList(text)
	if len(list) != 2 {
		t.Fatalf("got %d networks: %#v", len(list), list)
	}
	if list[0].SSID != "CMCC-WiFi" || list[0].Signal != 83 || list[0].Channel != 6 {
		t.Errorf("unexpected network: %#v", list[0])
	}
	if list[1].Security != "open" {
		t.Errorf("security = %q", list[1].Security)
	}
}

func TestParseIWInfoAndDev(t *testing.T) {
	info := ParseIWInfo("Interface wlan0\n\tssid CMCC-WiFi\n\ttype managed\n\tchannel 11 (2462 MHz), width: 20 MHz\n")
	if info.Interface != "wlan0" || info.SSID != "CMCC-WiFi" || info.Channel != 11 {
		t.Fatalf("unexpected info: %#v", info)
	}

	devs := ParseIWDev("phy#0\n\tInterface wlan0\n\t\ttype managed\n\tInterface uap0\n\t\ttype AP\n")
	if len(devs) != 2 || devs[0].Name != "wlan0" || devs[1].Type != "AP" {
		t.Fatalf("unexpected devs: %#v", devs)
	}
}

func TestParseResolvConf(t *testing.T) {
	got := ParseResolvConf("# comment\nnameserver 223.5.5.5\nnameserver 119.29.29.29\nsearch lan\n")
	if len(got) != 2 || got[0] != "223.5.5.5" {
		t.Fatalf("unexpected dns: %#v", got)
	}
}

func TestHostapdEdit(t *testing.T) {
	c := ParseHostapd([]byte("interface=wlan0\nssid=old\nchannel=6\n"))
	c.Set("ssid", "new")
	c.Set("wpa_passphrase", "12345678")
	if c.Get("ssid") != "new" || c.Get("wpa_passphrase") != "12345678" {
		t.Fatalf("unexpected config: %#v", c)
	}
	c.Delete("channel")
	if c.Get("channel") != "" {
		t.Fatal("channel should be deleted")
	}
	out := string(c.Bytes())
	if !strings.Contains(out, "ssid=new") || strings.Contains(out, "channel=") {
		t.Fatalf("unexpected output: %s", out)
	}
}
