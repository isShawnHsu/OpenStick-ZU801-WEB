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

	"msm8916d/internal/config"
	"msm8916d/internal/state"
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
	// QuotaBlocked reports that the current period is capped: the cellular
	// connection is kept down until the period rolls over or the cap is lifted.
	QuotaBlocked bool  `json:"quota_blocked"`
	// QuotaUnblocked reports that the cap was lifted manually for this period.
	QuotaUnblocked bool  `json:"quota_unblocked"`
	LastSample     int64 `json:"last_sample"`
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
	curRX, curTX, src, err := m.readTrafficCounters(ctx, iface)
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
	// A source change (or the first sample) only rebases the counters: taking a
	// delta between two different sources would produce garbage.
	if tr.sampled && !tr.lastAt.IsZero() && src == tr.src {
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
	tr.src = src
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
		m.enforceQuota(ctx, cfg, period)
	}
	return nil
}

// enforceQuota keeps the cellular connection capped once the billing period's
// quota is used up.
//
// The block is a persisted state, not a one-shot event: a manual reconnect is
// torn down again on the next sample and ConnectAPN refuses to dial at all,
// otherwise the cap would only last until the next click. The block is cleared
// by a new billing period, a traffic reset, raising the quota or an explicit
// unblock from the UI.
func (m *Manager) enforceQuota(ctx context.Context, cfg *config.Config, period string) {
	snap := m.store.Snapshot()
	total := snap.Traffic.RX + snap.Traffic.TX
	blocked := snap.QuotaBlockedPeriod == period

	// An explicit "解除封顶" wins for the rest of the period, otherwise clearing
	// the flag would only last until the next 10 second sample.
	if snap.QuotaUnblockedPeriod == period {
		if blocked {
			m.store.Update(func(s *state.State) { s.QuotaBlockedPeriod = "" })
		}
		return
	}

	if cfg.Data.QuotaBytes <= 0 || !cfg.Data.EnforceQuota || total < cfg.Data.QuotaBytes {
		if blocked {
			m.clearQuotaBlock()
			m.log.Printf("流量回到配额以内，已解除封顶")
		}
		return
	}

	if !blocked {
		m.store.Update(func(s *state.State) { s.QuotaBlockedPeriod = period })
		m.log.Printf("流量已达配额（%.1f MB / %.1f MB），已封顶并断开蜂窝连接",
			float64(total)/1048576, float64(cfg.Data.QuotaBytes)/1048576)
	}

	// While the cap is active nothing should dial the link: NetworkManager
	// autoconnect would otherwise connect and get torn down again in a loop,
	// and every attempt spends a minute of radio time.
	m.keepQuotaAutoconnectOff(ctx)

	// Something (NetworkManager autoconnect, a manual click, a service restart)
	// may dial the link back up; kick it down again, but throttle so a broken
	// modem cannot turn this into a busy loop.
	m.trafficMu.Lock()
	last := m.traffic.lastQuotaKick
	if !last.IsZero() && time.Since(last) < 30*time.Second {
		m.trafficMu.Unlock()
		return
	}
	m.traffic.lastQuotaKick = time.Now()
	m.trafficMu.Unlock()

	if !m.CellularDataActive(ctx) {
		return
	}
	if err := m.DisconnectAPN(ctx); err != nil {
		m.log.Printf("配额封顶：断开蜂窝连接失败: %v", err)
		return
	}
	m.log.Printf("配额封顶：蜂窝连接再次被断开")
}

// QuotaBlocked reports whether the current billing period is capped.
func (m *Manager) QuotaBlocked() bool {
	cfg := m.Config()
	if cfg.Data.QuotaBytes <= 0 || !cfg.Data.EnforceQuota {
		return false
	}
	period := billingPeriod(time.Now(), cfg.Data.ResetDay)
	snap := m.store.Snapshot()
	if snap.QuotaUnblockedPeriod == period {
		return false
	}
	return snap.QuotaBlockedPeriod == period
}

// quotaExceeded reports whether this period's usage has reached the quota.
func (m *Manager) quotaExceeded() bool {
	cfg := m.Config()
	if cfg.Data.QuotaBytes <= 0 || !cfg.Data.EnforceQuota {
		return false
	}
	snap := m.store.Snapshot()
	return snap.Traffic.RX+snap.Traffic.TX >= cfg.Data.QuotaBytes
}

// UnblockQuota lifts the cap for the current billing period as an explicit user
// choice. It also records an override so the next sample does not re-block while
// usage is still above the quota; the override expires with the period.
func (m *Manager) UnblockQuota() {
	period := billingPeriod(time.Now(), m.Config().Data.ResetDay)
	m.store.Update(func(s *state.State) {
		s.QuotaBlockedPeriod = ""
		s.QuotaUnblockedPeriod = period
	})
	m.resetQuotaKick()
}

// clearQuotaBlock removes the block and any override, used when the quota is
// raised, enforcement is turned off or the counters are reset.
func (m *Manager) clearQuotaBlock() {
	m.store.Update(func(s *state.State) {
		s.QuotaBlockedPeriod = ""
		s.QuotaUnblockedPeriod = ""
	})
	m.resetQuotaKick()
}

func (m *Manager) resetQuotaKick() {
	m.trafficMu.Lock()
	m.traffic.lastQuotaKick = time.Time{}
	m.traffic.quotaAutoconnectOff = false
	m.trafficMu.Unlock()
}

// keepQuotaAutoconnectOff disables NetworkManager autoconnect once per process
// while the quota cap is active.
func (m *Manager) keepQuotaAutoconnectOff(ctx context.Context) {
	m.trafficMu.Lock()
	done := m.traffic.quotaAutoconnectOff
	m.traffic.quotaAutoconnectOff = true
	m.trafficMu.Unlock()
	if done || !m.Available("nmcli") {
		return
	}
	name := m.Config().Modem.ConnectionName
	if name == "" {
		name = modemNMConnection
	}
	if _, err := m.Run(ctx, 10*time.Second, "nmcli", "connection", "modify", name,
		"connection.autoconnect", "no"); err != nil {
		m.log.Printf("配额封顶：关闭 %s 自动重连失败: %v", name, err)
	}
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
	st.QuotaBlocked = snap.QuotaBlockedPeriod != "" && snap.QuotaBlockedPeriod == st.Period
	st.QuotaUnblocked = snap.QuotaUnblockedPeriod != "" && snap.QuotaUnblockedPeriod == st.Period
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
		s.QuotaBlockedPeriod = ""
		s.QuotaUnblockedPeriod = ""
	})
	m.trafficMu.Lock()
	m.traffic.lastRX = 0
	m.traffic.lastTX = 0
	m.traffic.sampled = false
	m.traffic.src = ""
	m.traffic.rxRate = 0
	m.traffic.txRate = 0
	m.traffic.lastQuotaKick = time.Time{}
	m.traffic.quotaAutoconnectOff = false
	m.trafficMu.Unlock()
}

// ResetLifetime clears lifetime counters as well as the monthly history.
func (m *Manager) ResetLifetime() {
	m.store.Update(func(s *state.State) {
		s.Traffic = state.TrafficState{
			Period: billingPeriod(time.Now(), m.Config().Data.ResetDay),
			Days:   map[string]state.TrafficBucket{},
		}
		s.QuotaBlockedPeriod = ""
		s.QuotaUnblockedPeriod = ""
	})
	m.trafficMu.Lock()
	m.traffic = trafficRuntime{}
	m.trafficMu.Unlock()
}

// SetTrafficConfig persists accounting and quota settings.
func (m *Manager) SetTrafficConfig(iface string, quota int64, resetDay int, enforce bool) error {
	if err := m.Update(func(c *config.Config) error {
		if strings.TrimSpace(iface) != "" {
			c.Data.Interface = strings.TrimSpace(iface)
		}
		c.Data.QuotaBytes = quota
		c.Data.ResetDay = resetDay
		c.Data.EnforceQuota = enforce
		return nil
	}); err != nil {
		return err
	}
	// Raising the quota, turning enforcement off or switching the reset day can
	// all lift an existing block; make that take effect immediately instead of
	// waiting for the next sample to clear it.
	if !m.quotaExceeded() {
		m.clearQuotaBlock()
	}
	return nil
}

// readTrafficCounters returns byte counters for the cellular data path.
//
// The kernel netdev counters are useless on MSM8916 + bam-dmux: data goes over
// the BAM pipes and /sys/class/net/wwan0/statistics stays at zero. Reading the
// modem's own QMI counters is not an option either, because bam-dmux allows a
// single QMI client and opening one kicks ModemManager off the port (which
// makes the modem re-enumerate in a loop).
//
// So we count packets with two iptables rules instead:
//
//	mangle PREROUTING  -i <iface>  (everything the modem delivers)
//	mangle POSTROUTING -o <iface>  (everything sent to the modem)
//
// That covers both the device's own traffic and forwarded hotspot traffic, and
// it does not interfere with ModemManager. Fall back to the netdev counters
// when iptables is unavailable.
func (m *Manager) readTrafficCounters(ctx context.Context, iface string) (rx, tx int64, src string, err error) {
	if iface != "" && m.Available("iptables") {
		m.ensureTrafficRules(ctx, iface)
		if r, t, ok := m.readIptablesCounters(ctx); ok {
			return r, t, "iptables", nil
		}
	}
	r, t, nerr := readNetCounters(iface)
	if nerr != nil {
		return 0, 0, "net", nerr
	}
	return r, t, "net", nil
}

// Traffic accounting rules live in the mangle table so they never influence
// forwarding decisions.
const (
	trafficRXComment = "msm8916d-rx"
	trafficTXComment = "msm8916d-tx"
)

func (m *Manager) ensureTrafficRules(ctx context.Context, iface string) {
	specs := [][]string{
		{"-t", "mangle", "PREROUTING", "-i", iface, "-m", "comment", "--comment", trafficRXComment, "-j", "RETURN"},
		{"-t", "mangle", "POSTROUTING", "-o", iface, "-m", "comment", "--comment", trafficTXComment, "-j", "RETURN"},
	}
	for _, spec := range specs {
		check := append([]string{spec[0], spec[1], "-C"}, spec[2:]...)
		if _, err := m.Run(ctx, 5*time.Second, "iptables", check...); err == nil {
			continue
		}
		add := append([]string{spec[0], spec[1], "-A"}, spec[2:]...)
		_, _ = m.Run(ctx, 5*time.Second, "iptables", add...)
	}
}

func (m *Manager) readIptablesCounters(ctx context.Context) (int64, int64, bool) {
	rx, okRX := m.readIptablesChain(ctx, "PREROUTING", trafficRXComment)
	tx, okTX := m.readIptablesChain(ctx, "POSTROUTING", trafficTXComment)
	return rx, tx, okRX && okTX
}

func (m *Manager) readIptablesChain(ctx context.Context, chain, comment string) (int64, bool) {
	out, err := m.Run(ctx, 5*time.Second, "iptables", "-t", "mangle", "-L", chain, "-v", "-x", "-n")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, comment) {
			continue
		}
		fields := strings.Fields(line)
		// pkts bytes target prot opt in out source destination /* comment */
		if len(fields) < 2 {
			continue
		}
		if v, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
			return v, true
		}
	}
	return 0, false
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
