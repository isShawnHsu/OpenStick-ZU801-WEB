package svc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"uz801d/internal/config"
	"uz801d/internal/state"
)

// trafficHistoryLimit caps archived billing periods kept in the state file.
const trafficHistoryLimit = 36

// TrafficStats is the dashboard traffic view.
type TrafficStats struct {
	Interface    string  `json:"interface"`
	Period       string  `json:"period"`
	RX           int64   `json:"rx"`
	TX           int64   `json:"tx"`
	Total        int64   `json:"total"`
	LifetimeRX   int64   `json:"lifetime_rx"`
	LifetimeTX   int64   `json:"lifetime_tx"`
	RXRate       float64 `json:"rx_rate"`
	TXRate       float64 `json:"tx_rate"`
	QuotaBytes   int64   `json:"quota_bytes"`
	QuotaPercent float64 `json:"quota_percent"`
	ResetDay     int     `json:"reset_day"`
	EnforceQuota bool    `json:"enforce_quota"`
	LastSample   int64   `json:"last_sample"`
	// Months lists the current billing period first, then archived periods.
	Months []TrafficMonthStat `json:"months"`
	// Days is the per-day breakdown of the current period, newest first.
	Days  []TrafficDayStat `json:"days"`
	Error string           `json:"error,omitempty"`
}

// TrafficMonthStat is one row of the monthly history table.
type TrafficMonthStat struct {
	Period  string `json:"period"`
	RX      int64  `json:"rx"`
	TX      int64  `json:"tx"`
	Total   int64  `json:"total"`
	Current bool   `json:"current"`
}

// TrafficDayStat is one day inside the current billing period.
type TrafficDayStat struct {
	Date  string `json:"date"`
	RX    int64  `json:"rx"`
	TX    int64  `json:"tx"`
	Total int64  `json:"total"`
}

// SampleTraffic reads kernel counters and folds deltas into persistent totals.
func (m *Manager) SampleTraffic(ctx context.Context) error {
	cfg := m.Config()
	iface := cfg.Data.Interface
	if iface == "" {
		iface = cfg.Modem.Interface
	}
	curRX, curTX, err := readNetCounters(iface)
	if err != nil {
		m.trafficMu.Lock()
		m.traffic.lastAt = time.Now()
		m.trafficMu.Unlock()
		return nil
	}
	now := time.Now()
	period := billingPeriod(now, cfg.Data.ResetDay)

	m.trafficMu.Lock()
	tr := &m.traffic
	var dRX, dTX int64
	if tr.sampled && !tr.lastAt.IsZero() {
		dRX = curRX - tr.lastRX
		dTX = curTX - tr.lastTX
		if dRX < 0 {
			dRX = 0
		}
		if dTX < 0 {
			dTX = 0
		}
		secs := now.Sub(tr.lastAt).Seconds()
		if secs > 0 {
			tr.rxRate = float64(dRX) / secs
			tr.txRate = float64(dTX) / secs
		}
	}
	tr.lastRX = curRX
	tr.lastTX = curTX
	tr.lastAt = now
	tr.sampled = true
	tr.period = period
	m.trafficMu.Unlock()

	m.store.Update(func(s *state.State) {
		if s.Traffic.Days == nil {
			s.Traffic.Days = map[string]state.TrafficBucket{}
		}
		if s.Traffic.Period == "" {
			s.Traffic.Period = period
		} else if s.Traffic.Period != period {
			archiveTrafficPeriod(&s.Traffic)
			s.Traffic.Period = period
			s.Traffic.RX = 0
			s.Traffic.TX = 0
		}
		day := now.Format("2006-01-02")
		b := s.Traffic.Days[day]
		b.RX += dRX
		b.TX += dTX
		s.Traffic.Days[day] = b
		s.Traffic.RX += dRX
		s.Traffic.TX += dTX
		s.Traffic.LifetimeRX += dRX
		s.Traffic.LifetimeTX += dTX
		s.Traffic.LastRX = curRX
		s.Traffic.LastTX = curTX
		s.Traffic.LastSampleUnix = now.Unix()
	})

	if cfg.Data.QuotaBytes > 0 && cfg.Data.EnforceQuota {
		snap := m.store.Snapshot()
		total := snap.Traffic.RX + snap.Traffic.TX
		if total >= cfg.Data.QuotaBytes && snap.QuotaDisconnectedPeriod != period {
			if err := m.DisconnectAPN(ctx); err == nil {
				m.log.Printf("流量配额已用尽，已断开蜂窝连接")
				m.store.Update(func(s *state.State) { s.QuotaDisconnectedPeriod = period })
			} else {
				m.log.Printf("流量配额已用尽，但断开蜂窝连接失败: %v", err)
			}
		}
	}
	return nil
}

// TrafficStats returns the current accounting view.
func (m *Manager) TrafficStats(ctx context.Context) *TrafficStats {
	cfg := m.Config()
	_ = m.SampleTraffic(ctx)
	snap := m.store.Snapshot()
	now := time.Now()
	m.trafficMu.Lock()
	rxRate, txRate := m.traffic.rxRate, m.traffic.txRate
	m.trafficMu.Unlock()
	st := &TrafficStats{
		Interface:    cfg.Data.Interface,
		Period:       snap.Traffic.Period,
		RX:           snap.Traffic.RX,
		TX:           snap.Traffic.TX,
		Total:        snap.Traffic.RX + snap.Traffic.TX,
		LifetimeRX:   snap.Traffic.LifetimeRX,
		LifetimeTX:   snap.Traffic.LifetimeTX,
		RXRate:       rxRate,
		TXRate:       txRate,
		QuotaBytes:   cfg.Data.QuotaBytes,
		ResetDay:     cfg.Data.ResetDay,
		EnforceQuota: cfg.Data.EnforceQuota,
		LastSample:   snap.Traffic.LastSampleUnix,
	}
	if st.QuotaBytes > 0 {
		st.QuotaPercent = float64(st.Total) / float64(st.QuotaBytes) * 100
	}
	if st.Period == "" {
		st.Period = billingPeriod(now, cfg.Data.ResetDay)
	}
	st.Months = trafficMonthStats(snap.Traffic, st.Period)
	st.Days = trafficDayStats(snap.Traffic.Days, now, cfg.Data.ResetDay)
	return st
}

// ResetTraffic clears the billing-period counters and the daily breakdown.
// Archived months are kept so the monthly history survives a manual reset.
func (m *Manager) ResetTraffic() {
	now := time.Now()
	period := billingPeriod(now, m.Config().Data.ResetDay)
	m.store.Update(func(s *state.State) {
		if s.Traffic.Period != "" && s.Traffic.Period != period {
			archiveTrafficPeriod(&s.Traffic)
		}
		s.Traffic.RX = 0
		s.Traffic.TX = 0
		s.Traffic.Days = map[string]state.TrafficBucket{}
		s.Traffic.Period = period
	})
	m.trafficMu.Lock()
	m.traffic.lastRX = 0
	m.traffic.lastTX = 0
	m.traffic.sampled = false
	m.traffic.rxRate = 0
	m.traffic.txRate = 0
	m.trafficMu.Unlock()
}

// ResetLifetime clears lifetime counters as well as the monthly history.
func (m *Manager) ResetLifetime() {
	m.store.Update(func(s *state.State) {
		s.Traffic = state.TrafficState{
			Period: billingPeriod(time.Now(), m.Config().Data.ResetDay),
			Days:   map[string]state.TrafficBucket{},
		}
	})
	m.trafficMu.Lock()
	m.traffic = trafficRuntime{}
	m.trafficMu.Unlock()
}

// SetTrafficConfig persists accounting and quota settings.
func (m *Manager) SetTrafficConfig(iface string, quota int64, resetDay int, enforce bool) error {
	return m.Update(func(c *config.Config) error {
		if strings.TrimSpace(iface) != "" {
			c.Data.Interface = strings.TrimSpace(iface)
		}
		c.Data.QuotaBytes = quota
		c.Data.ResetDay = resetDay
		c.Data.EnforceQuota = enforce
		return nil
	})
}

func readNetCounters(iface string) (int64, int64, error) {
	if iface == "" {
		return 0, 0, fmt.Errorf("未配置统计网卡")
	}
	base := filepath.Join("/sys/class/net", iface, "statistics")
	rx, err := readIntFile(filepath.Join(base, "rx_bytes"))
	if err != nil {
		return 0, 0, err
	}
	tx, err := readIntFile(filepath.Join(base, "tx_bytes"))
	if err != nil {
		return 0, 0, err
	}
	return rx, tx, nil
}

func readIntFile(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, err
	}
	return v, nil
}

func billingPeriod(now time.Time, resetDay int) string {
	if resetDay < 1 || resetDay > 28 {
		resetDay = 1
	}
	y, mo, d := now.Date()
	if d < resetDay {
		mo--
		if mo == 0 {
			mo = 12
			y--
		}
	}
	return fmt.Sprintf("%04d-%02d", y, mo)
}

// periodStart returns the local start date of the billing period containing now.
func periodStart(now time.Time, resetDay int) time.Time {
	if resetDay < 1 || resetDay > 28 {
		resetDay = 1
	}
	y, mo, _ := now.Date()
	start := time.Date(y, mo, resetDay, 0, 0, 0, 0, now.Location())
	if now.Before(start) {
		start = start.AddDate(0, -1, 0)
	}
	return start
}

// archiveTrafficPeriod moves the current period totals into History and clears
// the daily breakdown, keeping the newest trafficHistoryLimit months.
func archiveTrafficPeriod(tr *state.TrafficState) {
	defer func() { tr.Days = map[string]state.TrafficBucket{} }()
	if tr.Period == "" || (tr.RX == 0 && tr.TX == 0) {
		return
	}
	entry := state.TrafficMonth{Period: tr.Period, RX: tr.RX, TX: tr.TX}
	for i := range tr.History {
		if tr.History[i].Period == entry.Period {
			tr.History[i] = entry
			return
		}
	}
	tr.History = append(tr.History, entry)
	sort.Slice(tr.History, func(i, j int) bool { return tr.History[i].Period < tr.History[j].Period })
	if len(tr.History) > trafficHistoryLimit {
		tr.History = append([]state.TrafficMonth(nil), tr.History[len(tr.History)-trafficHistoryLimit:]...)
	}
}

// trafficMonthStats lists the current period first, then archived months newest first.
func trafficMonthStats(tr state.TrafficState, current string) []TrafficMonthStat {
	out := make([]TrafficMonthStat, 0, len(tr.History)+1)
	out = append(out, TrafficMonthStat{
		Period:  current,
		RX:      tr.RX,
		TX:      tr.TX,
		Total:   tr.RX + tr.TX,
		Current: true,
	})
	for i := len(tr.History) - 1; i >= 0; i-- {
		h := tr.History[i]
		if h.Period == current {
			continue
		}
		out = append(out, TrafficMonthStat{
			Period: h.Period,
			RX:     h.RX,
			TX:     h.TX,
			Total:  h.RX + h.TX,
		})
	}
	return out
}

// trafficDayStats zero-fills every day of the current period up to today,
// newest first, so the UI shows a complete calendar.
func trafficDayStats(days map[string]state.TrafficBucket, now time.Time, resetDay int) []TrafficDayStat {
	start := periodStart(now, resetDay)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	out := make([]TrafficDayStat, 0, 32)
	for d := today; !d.Before(start); d = d.AddDate(0, 0, -1) {
		key := d.Format("2006-01-02")
		b := days[key]
		out = append(out, TrafficDayStat{
			Date:  key,
			RX:    b.RX,
			TX:    b.TX,
			Total: b.RX + b.TX,
		})
	}
	return out
}
