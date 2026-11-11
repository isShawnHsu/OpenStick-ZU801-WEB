// Package auth implements password hashing, login rate limiting and sessions.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	passwordIterations = 210_000
	passwordKeyLen     = 32
	passwordSaltLen    = 16
)

// HashPassword returns a pbkdf2-sha256$<iter>$<salt>$<key> string.
func HashPassword(password string) (string, error) {
	salt := make([]byte, passwordSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := pbkdf2Key([]byte(password), salt, passwordIterations, passwordKeyLen)
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s",
		passwordIterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword checks a password against a stored hash in constant time.
func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 || iter > 5_000_000 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(want) < 16 || len(want) > 128 {
		return false
	}
	got := pbkdf2Key([]byte(password), salt, iter, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// pbkdf2Key implements PBKDF2 (RFC 8018) with HMAC-SHA256.
func pbkdf2Key(password, salt []byte, iter, keyLen int) []byte {
	hLen := sha256.Size
	numBlocks := (keyLen + hLen - 1) / hLen
	out := make([]byte, 0, numBlocks*hLen)
	var block [4]byte
	for n := 1; n <= numBlocks; n++ {
		binary.BigEndian.PutUint32(block[:], uint32(n))
		mac := hmac.New(sha256.New, password)
		mac.Write(salt)
		mac.Write(block[:])
		u := mac.Sum(nil)
		t := make([]byte, len(u))
		copy(t, u)
		for i := 1; i < iter; i++ {
			mac = hmac.New(sha256.New, password)
			mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}

// RandomPassword returns a URL-safe random password with n bytes of entropy.
func RandomPassword(n int) (string, error) {
	if n < 8 {
		n = 8
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Session is an authenticated browser session.
type Session struct {
	Token     string    `json:"-"`
	CSRF      string    `json:"csrf"`
	User      string    `json:"user"`
	IP        string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// SessionStore is a concurrency-safe in-memory session table.
type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]*Session
	ttl      time.Duration
	max      int
}

// NewSessionStore creates a store with the given idle TTL.
func NewSessionStore(ttl time.Duration) *SessionStore {
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	return &SessionStore{sessions: make(map[string]*Session), ttl: ttl, max: 16}
}

// Create makes a new session, evicting the oldest if the table is full.
func (s *SessionStore) Create(user, ip string) (*Session, error) {
	token, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	csrf, err := randomToken(24)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	sess := &Session{
		Token:     token,
		CSRF:      csrf,
		User:      user,
		IP:        ip,
		CreatedAt: now,
		ExpiresAt: now.Add(s.ttl),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sessions) >= s.max {
		var oldest *Session
		for _, v := range s.sessions {
			if oldest == nil || v.CreatedAt.Before(oldest.CreatedAt) {
				oldest = v
			}
		}
		if oldest != nil {
			delete(s.sessions, oldest.Token)
		}
	}
	s.sessions[token] = sess
	return sess, nil
}

// Get returns a live session and slides its expiry forward when more than half gone.
func (s *SessionStore) Get(token string) *Session {
	if token == "" {
		return nil
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[token]
	if !ok {
		return nil
	}
	if now.After(sess.ExpiresAt) {
		delete(s.sessions, token)
		return nil
	}
	if sess.ExpiresAt.Sub(now) < s.ttl/2 {
		sess.ExpiresAt = now.Add(s.ttl)
	}
	cp := *sess
	return &cp
}

// Delete removes one session.
func (s *SessionStore) Delete(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

// DeleteOthers removes all sessions except keepToken.
func (s *SessionStore) DeleteOthers(keepToken string) {
	s.mu.Lock()
	for token := range s.sessions {
		if token != keepToken {
			delete(s.sessions, token)
		}
	}
	s.mu.Unlock()
}

// Cleanup drops expired sessions.
func (s *SessionStore) Cleanup() {
	now := time.Now()
	s.mu.Lock()
	for token, sess := range s.sessions {
		if now.After(sess.ExpiresAt) {
			delete(s.sessions, token)
		}
	}
	s.mu.Unlock()
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

type loginAttempt struct {
	first time.Time
	count int
}

// LoginLimiter blocks an IP after repeated failures within a window.
type LoginLimiter struct {
	mu      sync.Mutex
	entries map[string]*loginAttempt
	max     int
	window  time.Duration
}

// NewLoginLimiter creates a limiter allowing max failures per window.
func NewLoginLimiter(max int, window time.Duration) *LoginLimiter {
	if max <= 0 {
		max = 5
	}
	if window <= 0 {
		window = 5 * time.Minute
	}
	return &LoginLimiter{entries: make(map[string]*loginAttempt), max: max, window: window}
}

// Allowed reports whether ip may try to log in now.
func (l *LoginLimiter) Allowed(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.entries[ip]
	if !ok {
		return true, 0
	}
	now := time.Now()
	if now.Sub(a.first) >= l.window {
		delete(l.entries, ip)
		return true, 0
	}
	if a.count >= l.max {
		return false, a.first.Add(l.window).Sub(now)
	}
	return true, 0
}

// Fail records one failed attempt.
func (l *LoginLimiter) Fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	a, ok := l.entries[ip]
	if !ok || now.Sub(a.first) >= l.window {
		l.entries[ip] = &loginAttempt{first: now, count: 1}
		return
	}
	a.count++
}

// Reset clears failures for an IP after a successful login.
func (l *LoginLimiter) Reset(ip string) {
	l.mu.Lock()
	delete(l.entries, ip)
	l.mu.Unlock()
}

// Cleanup removes stale entries.
func (l *LoginLimiter) Cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	for ip, a := range l.entries {
		if now.Sub(a.first) >= l.window {
			delete(l.entries, ip)
		}
	}
}
