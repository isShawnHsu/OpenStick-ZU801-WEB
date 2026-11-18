package svc

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"
)

// bestAPChannel scans the 2.4 GHz neighbourhood and returns the least crowded
// of channels 1/6/11. The caller decides what to do when it returns 0 (scan
// failed), e.g. fall back to a fixed default.
func (m *Manager) bestAPChannel(ctx context.Context, iface string) int {
	if iface == "" || !m.Available("iw") {
		return 0
	}
	// Scanning needs a managed, up interface; hostapd sets AP mode later.
	_, _ = m.Run(ctx, 5*time.Second, "ip", "link", "set", iface, "down")
	_, _ = m.Run(ctx, 5*time.Second, "iw", "dev", iface, "set", "type", "managed")
	_, _ = m.Run(ctx, 5*time.Second, "ip", "link", "set", iface, "up")
	out := m.RunTry(ctx, 25*time.Second, "iw", "dev", iface, "scan")
	if strings.TrimSpace(out) == "" {
		return 0
	}

	counts := countAPChannels(out)
	if len(counts) == 0 {
		return 0
	}
	// Overlap on 2.4 GHz is roughly +-4 channels; weight the direct channel
	// highest and neighbours progressively less.
	score := func(ch int) float64 {
		s := float64(counts[ch]) * 5
		for d := 1; d <= 4; d++ {
			w := float64(5-d) / 2
			s += float64(counts[ch-d]+counts[ch+d]) * w
		}
		return s
	}
	candidates := []int{1, 6, 11}
	best, bestScore := 6, math.MaxFloat64
	for _, ch := range candidates {
		if sc := score(ch); sc < bestScore {
			best, bestScore = ch, sc
		}
	}
	// If the band is completely empty prefer 6 (works worldwide).
	if bestScore == 0 {
		best = 6
	}
	m.log.Printf("自动信道：扫描到 %d 个 AP（1:%d 6:%d 11:%d），选择 %d",
		totalAPs(counts), counts[1], counts[6], counts[11], best)
	return best
}

// countAPChannels counts how many BSSes sit on each channel.
func countAPChannels(scan string) map[int]int {
	counts := map[int]int{}
	cur := 0
	flush := func() {
		if cur > 0 && cur <= 14 {
			counts[cur]++
		}
		cur = 0
	}
	for _, raw := range strings.Split(scan, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "BSS "):
			flush()
		case strings.HasPrefix(line, "DS Parameter set: channel "):
			if cur == 0 {
				if v, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "DS Parameter set: channel "))); err == nil {
					cur = v
				}
			}
		case strings.HasPrefix(line, "primary channel:"):
			if cur == 0 {
				if v, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "primary channel:"))); err == nil {
					cur = v
				}
			}
		case strings.HasPrefix(line, "freq:"):
			if cur == 0 {
				if f, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(line, "freq:")), 64); err == nil {
					cur = freqToChannel(f)
				}
			}
		}
	}
	flush()
	return counts
}

func totalAPs(counts map[int]int) int {
	n := 0
	for _, v := range counts {
		n += v
	}
	return n
}

func freqToChannel(freq float64) int {
	switch {
	case freq == 2484:
		return 14
	case freq >= 2412 && freq <= 2472:
		return int((freq-2407)/5 + 0.5)
	}
	return 0
}
