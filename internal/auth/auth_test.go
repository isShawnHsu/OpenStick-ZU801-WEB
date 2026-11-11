package auth

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(hash, "pbkdf2-sha256$") {
		t.Fatalf("unexpected hash format: %s", hash)
	}
	if !VerifyPassword("correct horse battery", hash) {
		t.Fatal("correct password rejected")
	}
	if VerifyPassword("wrong password", hash) {
		t.Fatal("wrong password accepted")
	}
	if VerifyPassword("", "") {
		t.Fatal("empty hash accepted")
	}
}

func TestPasswordHashesAreSalted(t *testing.T) {
	a, _ := HashPassword("same-password")
	b, _ := HashPassword("same-password")
	if a == b {
		t.Fatal("expected different salts")
	}
}

func TestRandomPassword(t *testing.T) {
	p, err := RandomPassword(16)
	if err != nil {
		t.Fatalf("random: %v", err)
	}
	if len(p) < 16 {
		t.Fatalf("length = %d, want at least 16", len(p))
	}
	hash, err := HashPassword(p)
	if err != nil || !VerifyPassword(p, hash) {
		t.Fatalf("generated password does not round-trip: %v", err)
	}
	q, _ := RandomPassword(16)
	if p == q {
		t.Fatal("expected distinct passwords")
	}
}

func TestSessionLifecycle(t *testing.T) {
	s := NewSessionStore(time.Hour)
	sess, err := s.Create("admin", "127.0.0.1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if sess.Token == "" || sess.CSRF == "" {
		t.Fatal("missing tokens")
	}
	got := s.Get(sess.Token)
	if got == nil || got.User != "admin" {
		t.Fatalf("get returned %#v", got)
	}
	s.Delete(sess.Token)
	if s.Get(sess.Token) != nil {
		t.Fatal("session survived delete")
	}
}

func TestSessionExpiry(t *testing.T) {
	s := NewSessionStore(time.Millisecond)
	sess, _ := s.Create("admin", "")
	time.Sleep(10 * time.Millisecond)
	if s.Get(sess.Token) != nil {
		t.Fatal("expired session accepted")
	}
}

func TestDeleteOthers(t *testing.T) {
	s := NewSessionStore(time.Hour)
	a, _ := s.Create("admin", "")
	b, _ := s.Create("admin", "")
	s.DeleteOthers(a.Token)
	if s.Get(a.Token) == nil {
		t.Fatal("kept session missing")
	}
	if s.Get(b.Token) != nil {
		t.Fatal("other session kept")
	}
}

func TestLoginLimiter(t *testing.T) {
	l := NewLoginLimiter(2, time.Minute)
	if ok, _ := l.Allowed("1.2.3.4"); !ok {
		t.Fatal("first attempt blocked")
	}
	l.Fail("1.2.3.4")
	l.Fail("1.2.3.4")
	if ok, wait := l.Allowed("1.2.3.4"); ok {
		t.Fatal("expected limiter to block")
	} else if wait <= 0 {
		t.Fatal("expected positive wait")
	}
	l.Reset("1.2.3.4")
	if ok, _ := l.Allowed("1.2.3.4"); !ok {
		t.Fatal("reset did not clear limiter")
	}
}
