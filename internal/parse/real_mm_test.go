package parse

import (
	"os"
	"testing"
)

// TestParseRealMMJSON validates the parser against a captured `mmcli -m any -J`.
func TestParseRealMMJSON(t *testing.T) {
	p := os.Getenv("MM_JSON")
	if p == "" {
		t.Skip("MM_JSON not set")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	info, err := ParseModem(data)
	if err != nil {
		t.Fatalf("ParseModem: %v", err)
	}
	t.Logf("state=%q ueMode=%q psOnly=%v unlock=%q note=%q",
		info.State, info.UeMode, IsPSOnlyUeMode(info.UeMode), info.UnlockRequired, info.UnlockNote)
	if info.UeMode == "" {
		t.Fatal("UeMode empty: parser path is wrong")
	}
}
