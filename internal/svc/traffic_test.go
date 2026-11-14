package svc

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"uz801d/internal/state"
)

func TestParseIptablesCounters(t *testing.T) {
	// Output of: iptables -t mangle -L PREROUTING -v -x -n
	out := `Chain PREROUTING (policy ACCEPT 0 packets, 0 bytes)
    pkts      bytes target     prot opt in     out     source               destination
      12     34567 RETURN     all  --  wwan0  *       0.0.0.0/0            0.0.0.0/0            /* uz801d-rx */
       3      1234 ACCEPT     all  --  *      *       0.0.0.0/0            0.0.0.0/0
`
	var got int64
	var ok bool
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "uz801d-rx") {
			continue
		}
		f := strings.Fields(line)
		got, _ = strconv.ParseInt(f[1], 10, 64)
		ok = true
	}
	if !ok || got != 34567 {
		t.Fatalf("counters = %d ok=%v, want 34567", got, ok)
	}
}

func TestPeriodStart(t *testing.T) {
	loc := time.Local
	cases := []struct {
		name     string
		now      time.Time
		resetDay int
		want     string
	}{
		{"before reset day", time.Date(2026, 11, 3, 9, 0, 0, 0, loc), 5, "2026-10-05"},
		{"on reset day", time.Date(2026, 11, 5, 0, 30, 0, 0, loc), 5, "2026-11-05"},
		{"after reset day", time.Date(2026, 11, 10, 12, 0, 0, 0, loc), 5, "2026-11-05"},
		{"calendar month", time.Date(2026, 11, 10, 12, 0, 0, 0, loc), 1, "2026-11-01"},
		{"january rollover", time.Date(2026, 1, 2, 8, 0, 0, 0, loc), 5, "2025-12-05"},
		{"invalid reset day", time.Date(2026, 11, 10, 12, 0, 0, 0, loc), 0, "2026-11-01"},
	}
	for _, c := range cases {
		if got := periodStart(c.now, c.resetDay).Format("2006-01-02"); got != c.want {
			t.Errorf("%s: periodStart = %s, want %s", c.name, got, c.want)
		}
		if got := billingPeriod(c.now, c.resetDay); got != c.want[:7] {
			t.Errorf("%s: billingPeriod = %s, want %s", c.name, got, c.want[:7])
		}
	}
}

func TestArchiveTrafficPeriod(t *testing.T) {
	tr := state.TrafficState{
		Period: "2026-10",
		RX:     100,
		TX:     200,
		Days:   map[string]state.TrafficBucket{"2026-10-01": {RX: 100, TX: 200}},
	}
	archiveTrafficPeriod(&tr)
	if len(tr.History) != 1 || tr.History[0].Period != "2026-10" || tr.History[0].RX != 100 || tr.History[0].TX != 200 {
		t.Fatalf("unexpected history: %+v", tr.History)
	}
	if len(tr.Days) != 0 {
		t.Fatalf("days not cleared: %+v", tr.Days)
	}

	// Re-archiving the same period updates instead of duplicating.
	tr.Period = "2026-10"
	tr.RX = 150
	tr.TX = 250
	archiveTrafficPeriod(&tr)
	if len(tr.History) != 1 || tr.History[0].RX != 150 {
		t.Fatalf("expected updated history, got %+v", tr.History)
	}

	// Oldest months are trimmed once the cap is exceeded.
	for i := 0; i < trafficHistoryLimit+5; i++ {
		tr.Period = time.Date(2020, time.January, 1, 0, 0, 0, 0, time.Local).AddDate(0, i, 0).Format("2006-01")
		tr.RX, tr.TX = 1, 1
		archiveTrafficPeriod(&tr)
	}
	if len(tr.History) != trafficHistoryLimit {
		t.Fatalf("history length = %d, want %d", len(tr.History), trafficHistoryLimit)
	}
}

func TestTrafficDayStats(t *testing.T) {
	days := map[string]state.TrafficBucket{
		"2026-11-10": {RX: 10, TX: 20},
		"2026-11-08": {RX: 1, TX: 2},
	}
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local)

	out := trafficDayStats(days, now, 1)
	if len(out) != 10 {
		t.Fatalf("calendar month: got %d rows, want 10", len(out))
	}
	if out[0].Date != "2026-11-10" || out[0].RX != 10 || out[0].Total != 30 {
		t.Fatalf("unexpected first row: %+v", out[0])
	}
	if out[1].Date != "2026-11-09" || out[1].Total != 0 {
		t.Fatalf("expected zero-filled row for 11-09, got %+v", out[1])
	}
	if out[2].Date != "2026-11-08" || out[2].Total != 3 {
		t.Fatalf("unexpected 11-08 row: %+v", out[2])
	}
	if out[len(out)-1].Date != "2026-11-01" {
		t.Fatalf("last row = %s, want 2026-11-01", out[len(out)-1].Date)
	}

	custom := trafficDayStats(nil, now, 5)
	if len(custom) != 6 || custom[len(custom)-1].Date != "2026-11-05" {
		t.Fatalf("custom reset day rows = %d, last = %s", len(custom), custom[len(custom)-1].Date)
	}
}

func TestTrafficMonthStats(t *testing.T) {
	tr := state.TrafficState{
		RX: 5,
		TX: 6,
		History: []state.TrafficMonth{
			{Period: "2026-08", RX: 1, TX: 1},
			{Period: "2026-09", RX: 2, TX: 2},
		},
	}
	out := trafficMonthStats(tr, "2026-11")
	if len(out) != 3 {
		t.Fatalf("got %d months, want 3", len(out))
	}
	if !out[0].Current || out[0].Period != "2026-11" || out[0].Total != 11 {
		t.Fatalf("unexpected current row: %+v", out[0])
	}
	if out[1].Period != "2026-09" || out[2].Period != "2026-08" {
		t.Fatalf("history order: %+v", out)
	}
}
