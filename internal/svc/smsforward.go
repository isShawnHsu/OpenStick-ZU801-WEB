package svc

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"uz801d/internal/config"
	"uz801d/internal/state"
)

const (
	smsForwardKeep        = 500
	smsForwardMaxAge      = 30 * 24 * time.Hour
	smsForwardLogInterval = 5 * time.Minute
)

// smsForwardClient bounds every webhook request so a dead endpoint cannot hang
// the polling loop.
var smsForwardClient = &http.Client{Timeout: 10 * time.Second}

// smsForwardLoop polls the modem for new messages and forwards them. The
// interval is read from the live configuration, so changes apply without a
// restart.
func (m *Manager) smsForwardLoop(ctx context.Context) {
	for {
		seconds := m.Config().SMS.PollSeconds
		if seconds < 15 {
			seconds = 60
		}
		if !m.slept(time.Duration(seconds) * time.Second) {
			return
		}
		m.forwardPendingSMS(ctx)
	}
}

// SetSMSForwardConfig replaces the forwarding settings. Enabling forwarding
// (or enabling the first provider) resets the baseline so old messages are not
// pushed to the operator.
func (m *Manager) SetSMSForwardConfig(next config.SMSConfig) error {
	next.Providers = append([]config.SMSForwardTarget(nil), next.Providers...)
	next.Normalize()
	prev := m.Config().SMS
	if err := m.Update(func(c *config.Config) error {
		c.SMS = next
		return nil
	}); err != nil {
		return err
	}
	if next.ForwardEnabled && (!prev.ForwardEnabled || (enabledForwardTargets(prev) == 0 && enabledForwardTargets(next) > 0)) {
		m.resetSMSForwardBaseline()
	}
	return nil
}

func enabledForwardTargets(cfg config.SMSConfig) int {
	n := 0
	for _, t := range cfg.Providers {
		if t.Enabled {
			n++
		}
	}
	return n
}

// resetSMSForwardBaseline skips every message that already exists on the modem.
func (m *Manager) resetSMSForwardBaseline() {
	now := time.Now().Unix()
	m.store.Update(func(s *state.State) {
		s.SMSForward.Since = now
		s.SMSForward.Primed = false
	})
}

// TestSMSForward sends a sample message through one target without saving it.
func (m *Manager) TestSMSForward(ctx context.Context, target config.SMSForwardTarget) error {
	msg := SMS{
		Number:    "10086",
		Text:      "这是一条来自 UZ801 Manager 的测试消息。收到它说明短信转发配置正确。",
		Timestamp: time.Now().Format(time.RFC3339),
		State:     "received",
	}
	return sendSMSForward(ctx, smsForwardClient, target, msg)
}

// forwardPendingSMS forwards every received message that arrived after the
// baseline and was not forwarded before.
func (m *Manager) forwardPendingSMS(ctx context.Context) {
	cfg := m.Config().SMS
	if !cfg.ForwardEnabled {
		return
	}
	targets := make([]config.SMSForwardTarget, 0, len(cfg.Providers))
	for _, t := range cfg.Providers {
		if t.Enabled {
			targets = append(targets, t)
		}
	}
	if len(targets) == 0 {
		return
	}

	m.smsForwardMu.Lock()
	defer m.smsForwardMu.Unlock()

	snap := m.store.Snapshot()
	if snap.SMSForward.Since == 0 {
		m.resetSMSForwardBaseline()
		m.log.Printf("短信转发已启用，从当前时间开始转发新短信，历史短信将被忽略")
		return
	}
	list, err := m.ListSMS(ctx)
	if err != nil {
		m.logSMSForwardError(err)
		return
	}

	now := time.Now().Unix()
	since := snap.SMSForward.Since
	if !snap.SMSForward.Primed {
		// Remember every message that already exists so enabling forwarding
		// never replays the backlog, even when the modem omits timestamps.
		m.store.Update(func(s *state.State) {
			if s.SMSForward.Sent == nil {
				s.SMSForward.Sent = map[string]int64{}
			}
			for _, msg := range list {
				if msg.State != "" && !strings.EqualFold(msg.State, "received") {
					continue
				}
				s.SMSForward.Sent[smsFingerprint(msg)] = now
			}
			pruneSMSForwardSent(s.SMSForward.Sent, now)
			s.SMSForward.Primed = true
		})
		m.log.Printf("短信转发已启用，已跳过当前已有的 %d 条短信", len(list))
		return
	}
	// ListSMS returns newest first; forward in arrival order.
	for i := len(list) - 1; i >= 0; i-- {
		msg := list[i]
		if msg.State != "" && !strings.EqualFold(msg.State, "received") {
			continue
		}
		if ts := parseSMSTime(msg.Timestamp); ts > 0 && ts < since {
			continue
		}
		fp := smsFingerprint(msg)
		if _, done := snap.SMSForward.Sent[fp]; done {
			continue
		}
		sent := false
		for _, target := range targets {
			if err := sendSMSForward(ctx, smsForwardClient, target, msg); err != nil {
				m.log.Printf("短信 %s 转发到 %s 失败: %v", msg.ID, forwardTargetLabel(target), err)
				continue
			}
			sent = true
			m.log.Printf("短信 %s 已转发到 %s", msg.ID, forwardTargetLabel(target))
		}
		if !sent {
			continue
		}
		snap.SMSForward.Sent[fp] = now
		m.store.Update(func(s *state.State) {
			if s.SMSForward.Sent == nil {
				s.SMSForward.Sent = map[string]int64{}
			}
			s.SMSForward.Sent[fp] = now
			pruneSMSForwardSent(s.SMSForward.Sent, now)
		})
	}
}

// logSMSForwardError throttles repeated polling errors (missing modem, no SIM)
// so the log is not flooded every interval.
func (m *Manager) logSMSForwardError(err error) {
	now := time.Now()
	if m.smsForwardLastErr == err.Error() && now.Sub(m.smsForwardErrAt) < smsForwardLogInterval {
		return
	}
	m.smsForwardLastErr = err.Error()
	m.smsForwardErrAt = now
	m.log.Printf("短信转发轮询失败: %v", err)
}

func forwardTargetLabel(t config.SMSForwardTarget) string {
	if strings.TrimSpace(t.Name) != "" {
		return t.Name
	}
	return config.SMSProviderName(t.Type)
}

func smsFingerprint(msg SMS) string {
	sum := sha256.Sum256([]byte(msg.Text))
	return msg.ID + "|" + msg.Timestamp + "|" + hex.EncodeToString(sum[:8])
}

func parseSMSTime(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05-0700",
	} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.Unix()
		}
	}
	return 0
}

func pruneSMSForwardSent(sent map[string]int64, now int64) {
	cutoff := now - int64(smsForwardMaxAge/time.Second)
	for key, ts := range sent {
		if ts < cutoff {
			delete(sent, key)
		}
	}
	if len(sent) <= smsForwardKeep {
		return
	}
	type entry struct {
		key string
		ts  int64
	}
	entries := make([]entry, 0, len(sent))
	for key, ts := range sent {
		entries = append(entries, entry{key: key, ts: ts})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ts < entries[j].ts })
	for _, e := range entries[:len(entries)-smsForwardKeep] {
		delete(sent, e.key)
	}
}

// smsForwardMessage builds a short title and the text body for one message.
func smsForwardMessage(msg SMS) (string, string) {
	number := strings.TrimSpace(msg.Number)
	if number == "" {
		number = "未知号码"
	}
	text := strings.TrimSpace(msg.Text)
	if text == "" {
		if msg.Data {
			text = "（数据短信，无法显示文本）"
		} else {
			text = "（空短信）"
		}
	}
	title := "短信来自 " + number
	body := text
	if ts := strings.TrimSpace(msg.Timestamp); ts != "" {
		body += "\n\n时间: " + ts
	}
	return title, body
}

// sendSMSForward validates a target and dispatches to the provider.
func sendSMSForward(ctx context.Context, client *http.Client, target config.SMSForwardTarget, msg SMS) error {
	target.Normalize()
	if err := target.Validate(); err != nil {
		return err
	}
	title, body := smsForwardMessage(msg)
	switch target.Type {
	case "bark":
		return sendBark(ctx, client, target, title, body)
	case "dingtalk":
		return sendDingTalk(ctx, client, target, title+"\n"+body)
	case "feishu":
		return sendFeishu(ctx, client, target, title+"\n"+body)
	default:
		return fmt.Errorf("不支持的短信转发类型: %s", target.Type)
	}
}

func sendBark(ctx context.Context, client *http.Client, target config.SMSForwardTarget, title, body string) error {
	endpoint := strings.TrimRight(target.URL, "/") + "/" + url.PathEscape(target.Token)
	payload := map[string]any{
		"title": title,
		"body":  body,
	}
	if target.Group != "" {
		payload["group"] = target.Group
	}
	if target.Sound != "" {
		payload["sound"] = target.Sound
	}
	_, respBody, err := postJSON(ctx, client, endpoint, payload)
	if err != nil {
		return err
	}
	var res struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(respBody, &res) == nil && res.Code != 0 && res.Code != http.StatusOK {
		return fmt.Errorf("Bark 返回 %d: %s", res.Code, res.Message)
	}
	return nil
}

func sendDingTalk(ctx context.Context, client *http.Client, target config.SMSForwardTarget, text string) error {
	endpoint := target.URL
	if target.Secret != "" {
		ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
		mac := hmac.New(sha256.New, []byte(target.Secret))
		mac.Write([]byte(ts + "\n" + target.Secret))
		sign := url.QueryEscape(base64.StdEncoding.EncodeToString(mac.Sum(nil)))
		sep := "?"
		if strings.Contains(endpoint, "?") {
			sep = "&"
		}
		endpoint += sep + "timestamp=" + ts + "&sign=" + sign
	}
	payload := map[string]any{
		"msgtype": "text",
		"text":    map[string]string{"content": text},
	}
	_, respBody, err := postJSON(ctx, client, endpoint, payload)
	if err != nil {
		return err
	}
	var res struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if json.Unmarshal(respBody, &res) == nil && res.ErrCode != 0 {
		return fmt.Errorf("钉钉返回 %d: %s", res.ErrCode, res.ErrMsg)
	}
	return nil
}

func sendFeishu(ctx context.Context, client *http.Client, target config.SMSForwardTarget, text string) error {
	payload := map[string]any{
		"msg_type": "text",
		"content":  map[string]string{"text": text},
	}
	if target.Secret != "" {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		stringToSign := ts + "\n" + target.Secret
		mac := hmac.New(sha256.New, []byte(stringToSign))
		payload["timestamp"] = ts
		payload["sign"] = base64.StdEncoding.EncodeToString(mac.Sum(nil))
	}
	_, respBody, err := postJSON(ctx, client, target.URL, payload)
	if err != nil {
		return err
	}
	var res struct {
		Code          int    `json:"code"`
		Msg           string `json:"msg"`
		StatusCode    int    `json:"StatusCode"`
		StatusMessage string `json:"StatusMessage"`
	}
	if json.Unmarshal(respBody, &res) == nil {
		if res.Code != 0 {
			return fmt.Errorf("飞书返回 %d: %s", res.Code, res.Msg)
		}
		if res.StatusCode != 0 {
			return fmt.Errorf("飞书返回 %d: %s", res.StatusCode, res.StatusMessage)
		}
	}
	return nil
}

func postJSON(ctx context.Context, client *http.Client, endpoint string, payload any) (int, []byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return 0, nil, fmt.Errorf("请求地址无效: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, body, fmt.Errorf("HTTP %d: %s", resp.StatusCode, responseSnippet(body))
	}
	return resp.StatusCode, body, nil
}

func responseSnippet(body []byte) string {
	text := strings.TrimSpace(string(body))
	runes := []rune(text)
	if len(runes) > 200 {
		return string(runes[:200]) + "…"
	}
	return text
}
