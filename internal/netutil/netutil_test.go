package netutil

import "testing"

func TestValidateLAN(t *testing.T) {
	if err := ValidateLAN("192.168.68.1", "255.255.255.0", "192.168.68.100", "192.168.68.200",
		[]string{"223.5.5.5", "119.29.29.29"}); err != nil {
		t.Fatalf("valid LAN rejected: %v", err)
	}
	cases := []struct {
		name                 string
		ip, mask, start, end string
		dns                  []string
	}{
		{"bad ip", "999.1.1.1", "255.255.255.0", "192.168.68.100", "192.168.68.200", []string{"1.1.1.1"}},
		{"discontiguous mask", "192.168.68.1", "255.0.255.0", "192.168.68.100", "192.168.68.200", []string{"1.1.1.1"}},
		{"start after end", "192.168.68.1", "255.255.255.0", "192.168.68.200", "192.168.68.100", []string{"1.1.1.1"}},
		{"range has router", "192.168.68.100", "255.255.255.0", "192.168.68.100", "192.168.68.200", []string{"1.1.1.1"}},
		{"no dns", "192.168.68.1", "255.255.255.0", "192.168.68.100", "192.168.68.200", nil},
	}
	for _, tc := range cases {
		if err := ValidateLAN(tc.ip, tc.mask, tc.start, tc.end, tc.dns); err == nil {
			t.Errorf("%s: expected error", tc.name)
		}
	}
}

func TestNormalizeMAC(t *testing.T) {
	cases := map[string]string{
		"AA-BB-CC-DD-EE-FF": "aa:bb:cc:dd:ee:ff",
		"aa:bb:cc:dd:ee:ff": "aa:bb:cc:dd:ee:ff",
	}
	for in, want := range cases {
		got, err := NormalizeMAC(in)
		if err != nil || got != want {
			t.Errorf("NormalizeMAC(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "not-a-mac", "aa:bb:cc:dd:ee", "aa:bb:cc:dd:ee:ff:00"} {
		if _, err := NormalizeMAC(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestIsValidHost(t *testing.T) {
	for _, ok := range []string{"223.5.5.5", "example.com", "my-host_1", "::1"} {
		if !IsValidHost(ok) {
			t.Errorf("expected valid: %q", ok)
		}
	}
	for _, bad := range []string{"", "-bad", "bad-", "bad host", "a;rm", "host$"} {
		if IsValidHost(bad) {
			t.Errorf("expected invalid: %q", bad)
		}
	}
}

func TestMaskAndNetwork(t *testing.T) {
	n, err := MaskSize("255.255.255.0")
	if err != nil || n != 24 {
		t.Fatalf("MaskSize = %d, %v", n, err)
	}
	if s, _ := PrefixFromMask("255.255.0.0"); s != "16" {
		t.Errorf("PrefixFromMask = %q", s)
	}
	ip, _ := ParseIPv4("192.168.68.44")
	net := Network(ip, 24)
	if net.String() != "192.168.68.0" {
		t.Errorf("network = %s", net)
	}
	if !SameSubnet(ip, ip, 24) {
		t.Error("SameSubnet failed")
	}
}
