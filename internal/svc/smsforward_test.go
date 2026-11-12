package svc

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"uz801d/internal/config"
)

func TestSMSForwardMessage(t *testing.T) {
	title, body := smsForwardMessage(SMS{
		Number:    "10086",
		Text:      "hello",
		Timestamp: "2026-11-12T10:00:00+08:00",
	})
	if title != "短信来自 10086" {
		t.Errorf("title = %q", title)
	}
	if !strings.Contains(body, "hello") || !strings.Contains(body, "2026-11-12T10:00:00+08:00") {
		t.Errorf("body = %q", body)
	}

	if _, body := smsForwardMessage(SMS{Number: "10086", Data: true}); !strings.Contains(body, "数据短信") {
		t.Errorf("data message body = %q", body)
	}
}

func TestParseSMSTime(t *testing.T) {
	if got := parseSMSTime("2026-11-12T10:00:00+08:00"); got == 0 {
		t.Error("RFC3339 timestamp not parsed")
	}
	if got := parseSMSTime("2026-11-12 10:00:00"); got == 0 {
		t.Error("local timestamp not parsed")
	}
	if got := parseSMSTime(""); got != 0 {
		t.Errorf("empty timestamp = %d, want 0", got)
	}
	if got := parseSMSTime("not-a-time"); got != 0 {
		t.Errorf("invalid timestamp = %d, want 0", got)
	}
}

func TestSendBarkForward(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Path != "/devkey" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode: %v", err)
		}
		_, _ = io.WriteString(w, `{"code":200,"message":"success"}`)
	}))
	defer srv.Close()

	target := config.SMSForwardTarget{
		Type:  "bark",
		URL:   srv.URL,
		Token: "devkey",
		Group: "uz801d",
		Sound: "bell",
	}
	msg := SMS{Number: "10086", Text: "hello", State: "received"}
	if err := sendSMSForward(context.Background(), srv.Client(), target, msg); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got["title"] != "短信来自 10086" {
		t.Errorf("title = %v", got["title"])
	}
	if !strings.Contains(got["body"].(string), "hello") {
		t.Errorf("body = %v", got["body"])
	}
	if got["group"] != "uz801d" || got["sound"] != "bell" {
		t.Errorf("group/sound = %v/%v", got["group"], got["sound"])
	}
}

func TestSendDingTalkForwardSignature(t *testing.T) {
	const secret = "SECtest123"
	var payload struct {
		MsgType string `json:"msgtype"`
		Text    struct {
			Content string `json:"content"`
		} `json:"text"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ts := r.URL.Query().Get("timestamp")
		sign := r.URL.Query().Get("sign")
		if ts == "" || sign == "" {
			t.Errorf("missing signature params: %s", r.URL.RawQuery)
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(ts + "\n" + secret))
		want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
		if sign != want {
			t.Errorf("sign = %q, want %q", sign, want)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode: %v", err)
		}
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer srv.Close()

	target := config.SMSForwardTarget{
		Type:   "dingtalk",
		URL:    srv.URL + "/robot/send?access_token=abc",
		Secret: secret,
	}
	msg := SMS{Number: "10086", Text: "hello", State: "received"}
	if err := sendSMSForward(context.Background(), srv.Client(), target, msg); err != nil {
		t.Fatalf("send: %v", err)
	}
	if payload.MsgType != "text" || !strings.Contains(payload.Text.Content, "hello") {
		t.Errorf("unexpected payload: %+v", payload)
	}
}

func TestSendFeishuForwardSignature(t *testing.T) {
	const secret = "feishuSecret"
	var payload struct {
		Timestamp string `json:"timestamp"`
		Sign      string `json:"sign"`
		MsgType   string `json:"msg_type"`
		Content   struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode: %v", err)
		}
		stringToSign := payload.Timestamp + "\n" + secret
		mac := hmac.New(sha256.New, []byte(stringToSign))
		want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
		if payload.Sign != want {
			t.Errorf("sign = %q, want %q", payload.Sign, want)
		}
		_, _ = io.WriteString(w, `{"code":0,"msg":"success"}`)
	}))
	defer srv.Close()

	target := config.SMSForwardTarget{Type: "feishu", URL: srv.URL, Secret: secret}
	msg := SMS{Number: "10086", Text: "hello", State: "received"}
	if err := sendSMSForward(context.Background(), srv.Client(), target, msg); err != nil {
		t.Fatalf("send: %v", err)
	}
	if payload.MsgType != "text" || !strings.Contains(payload.Content.Text, "hello") {
		t.Errorf("unexpected payload: %+v", payload)
	}
	if payload.Timestamp == "" || payload.Sign == "" {
		t.Errorf("missing signature fields: %+v", payload)
	}
}

func TestSendForwardProviderErrors(t *testing.T) {
	cases := []struct {
		name   string
		target config.SMSForwardTarget
		body   string
	}{
		{
			name:   "bark",
			target: config.SMSForwardTarget{Type: "bark", Token: "devkey"},
			body:   `{"code":400,"message":"bad key"}`,
		},
		{
			name:   "dingtalk",
			target: config.SMSForwardTarget{Type: "dingtalk"},
			body:   `{"errcode":310000,"errmsg":"invalid token"}`,
		},
		{
			name:   "feishu",
			target: config.SMSForwardTarget{Type: "feishu"},
			body:   `{"code":19021,"msg":"sign match fail"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			target := tc.target
			target.URL = srv.URL
			if target.Type == "bark" {
				target.URL = srv.URL
			}
			msg := SMS{Number: "10086", Text: "hello"}
			if err := sendSMSForward(context.Background(), srv.Client(), target, msg); err == nil {
				t.Fatalf("%s: expected error", tc.name)
			}
		})
	}
}

func TestPruneSMSForwardSent(t *testing.T) {
	now := time.Now().Unix()
	sent := map[string]int64{
		"expired": now - int64((31*24*time.Hour)/time.Second),
	}
	for i := 0; i < smsForwardKeep+10; i++ {
		sent[string(rune('a'+i%26))+string(rune('0'+i/26))] = now
	}
	pruneSMSForwardSent(sent, now)
	if _, ok := sent["expired"]; ok {
		t.Error("expired entry was kept")
	}
	if len(sent) > smsForwardKeep {
		t.Errorf("sent = %d entries, want <= %d", len(sent), smsForwardKeep)
	}
}

func TestSMSForwardTargetNormalize(t *testing.T) {
	bark := config.SMSForwardTarget{Type: "Bark", Token: " devkey ", Secret: "unused"}
	bark.Normalize()
	if bark.URL != "https://api.day.app" || bark.Token != "devkey" || bark.Secret != "" {
		t.Errorf("bark normalize = %+v", bark)
	}
	ding := config.SMSForwardTarget{Type: "dingtalk", Token: "ignored", Group: "g", Sound: "s"}
	ding.Normalize()
	if ding.Token != "" || ding.Group != "" || ding.Sound != "" {
		t.Errorf("dingtalk normalize = %+v", ding)
	}
	if ding.Name != "钉钉" {
		t.Errorf("name = %q", ding.Name)
	}
}
