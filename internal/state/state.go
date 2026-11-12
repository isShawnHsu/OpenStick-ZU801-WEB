// Package state persists mutable runtime data that must survive restarts.
package state

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const currentVersion = 1

// State is the mutable runtime data.
type State struct {
	Version    int                    `json:"version"`
	InstallID  string                 `json:"install_id"`
	Traffic    TrafficState           `json:"traffic"`
	SMSRead    map[string]int64       `json:"sms_read"`
	SMSForward SMSForwardState        `json:"sms_forward"`
	Clients    map[string]ClientEntry `json:"clients"`
	Blocked    []string               `json:"blocked_macs"`
	// QuotaDisconnectedPeriod prevents repeatedly disconnecting within one period.
	QuotaDisconnectedPeriod string `json:"quota_disconnected_period,omitempty"`
	UpdatedAt               int64  `json:"updated_at"`
}

// SMSForwardState remembers which received messages were already forwarded.
type SMSForwardState struct {
	// Since is the Unix time when forwarding was enabled; older messages are
	// ignored so the backlog is not pushed to the operator on first start.
	Since int64 `json:"since,omitempty"`
	// Primed is set after the messages that already existed when forwarding
	// was enabled have been recorded, so they are never forwarded.
	Primed bool `json:"primed,omitempty"`
	// Sent maps a message fingerprint to the Unix time it was forwarded.
	Sent map[string]int64 `json:"sent,omitempty"`
}

// TrafficState stores cumulative counters and the last raw kernel counters.
type TrafficState struct {
	Period         string `json:"period"`
	RX             int64  `json:"rx"`
	TX             int64  `json:"tx"`
	LastRX         int64  `json:"last_rx"`
	LastTX         int64  `json:"last_tx"`
	LifetimeRX     int64  `json:"lifetime_rx"`
	LifetimeTX     int64  `json:"lifetime_tx"`
	LastSampleUnix int64  `json:"last_sample_unix"`
	// Days holds the per-day breakdown of the current billing period,
	// keyed by local date (YYYY-MM-DD).
	Days map[string]TrafficBucket `json:"days,omitempty"`
	// History keeps completed billing periods, oldest first.
	History []TrafficMonth `json:"history,omitempty"`
}

// TrafficBucket is a RX/TX byte pair.
type TrafficBucket struct {
	RX int64 `json:"rx"`
	TX int64 `json:"tx"`
}

// TrafficMonth is one archived billing period.
type TrafficMonth struct {
	Period string `json:"period"`
	RX     int64  `json:"rx"`
	TX     int64  `json:"tx"`
}

// ClientEntry remembers a client across reboots.
type ClientEntry struct {
	FirstSeen int64  `json:"first_seen"`
	LastSeen  int64  `json:"last_seen"`
	Hostname  string `json:"hostname,omitempty"`
}

// New returns an empty state with a fresh install ID.
func New() *State {
	return &State{
		Version:    currentVersion,
		InstallID:  newID(),
		SMSRead:    map[string]int64{},
		SMSForward: SMSForwardState{Sent: map[string]int64{}},
		Clients:    map[string]ClientEntry{},
		Blocked:    []string{},
		UpdatedAt:  time.Now().Unix(),
	}
}

func newID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// Load reads state from path. A missing file yields a fresh state.
func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return New(), nil
		}
		return nil, err
	}
	st := New()
	if err := json.Unmarshal(data, st); err != nil {
		return nil, fmt.Errorf("解析状态文件 %s 失败: %w", path, err)
	}
	if st.Version == 0 {
		st.Version = currentVersion
	}
	if st.InstallID == "" {
		st.InstallID = newID()
	}
	if st.SMSRead == nil {
		st.SMSRead = map[string]int64{}
	}
	if st.SMSForward.Sent == nil {
		st.SMSForward.Sent = map[string]int64{}
	}
	if st.Clients == nil {
		st.Clients = map[string]ClientEntry{}
	}
	if st.Blocked == nil {
		st.Blocked = []string{}
	}
	if st.Traffic.Days == nil {
		st.Traffic.Days = map[string]TrafficBucket{}
	}
	if st.Traffic.History == nil {
		st.Traffic.History = []TrafficMonth{}
	}
	return st, nil
}

// Save writes state atomically with mode 0600.
func Save(path string, st *State) error {
	st.Version = currentVersion
	st.UpdatedAt = time.Now().Unix()
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp-%d", path, time.Now().UnixNano())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	_ = os.Chmod(path, 0o600)
	return nil
}

// Store guards a State and tracks whether it needs saving.
type Store struct {
	mu    sync.Mutex
	path  string
	state *State
	dirty bool
}

// Open loads or creates state at path.
func Open(path string) (*Store, error) {
	st, err := Load(path)
	if err != nil {
		return nil, err
	}
	return &Store{path: path, state: st}, nil
}

// Snapshot returns a deep copy of the current state.
func (s *Store) Snapshot() *State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return clone(s.state)
}

// Update mutates state under lock and marks it dirty.
func (s *Store) Update(fn func(*State)) {
	s.mu.Lock()
	fn(s.state)
	s.state.UpdatedAt = time.Now().Unix()
	s.dirty = true
	s.mu.Unlock()
}

// Blocked returns a copy of the blocked MAC list.
func (s *Store) Blocked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.state.Blocked))
	copy(out, s.state.Blocked)
	return out
}

// FindClient returns a client history entry.
func (s *Store) FindClient(mac string) (ClientEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.state.Clients[mac]
	return e, ok
}

// TouchClient updates first/last seen and hostname for a MAC.
func (s *Store) TouchClient(mac, hostname string) {
	now := time.Now().Unix()
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.state.Clients[mac]
	if !ok {
		e = ClientEntry{FirstSeen: now}
	}
	e.LastSeen = now
	if hostname != "" && hostname != "*" {
		e.Hostname = hostname
	}
	s.state.Clients[mac] = e
	if len(s.state.Clients) > 256 {
		// Drop the least recently seen entries.
		var oldestMAC string
		var oldest int64 = 1<<62 - 1
		for m, c := range s.state.Clients {
			if c.LastSeen < oldest {
				oldest = c.LastSeen
				oldestMAC = m
			}
		}
		if oldestMAC != "" && oldestMAC != mac {
			delete(s.state.Clients, oldestMAC)
		}
	}
	s.dirty = true
}

// Dirty reports whether unsaved changes exist.
func (s *Store) Dirty() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dirty
}

// Save writes the state if it has changed.
func (s *Store) Save() error {
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return nil
	}
	cp := clone(s.state)
	s.dirty = false
	s.mu.Unlock()
	if err := Save(s.path, cp); err != nil {
		s.mu.Lock()
		s.dirty = true
		s.mu.Unlock()
		return err
	}
	return nil
}

// Path returns the backing file path.
func (s *Store) Path() string { return s.path }

// Reset replaces all persisted state with a fresh install state.
func (s *Store) Reset() error {
	s.mu.Lock()
	s.state = New()
	s.dirty = true
	s.mu.Unlock()
	return s.Save()
}

func clone(in *State) *State {
	out := *in
	out.SMSRead = make(map[string]int64, len(in.SMSRead))
	for k, v := range in.SMSRead {
		out.SMSRead[k] = v
	}
	out.SMSForward.Sent = make(map[string]int64, len(in.SMSForward.Sent))
	for k, v := range in.SMSForward.Sent {
		out.SMSForward.Sent[k] = v
	}
	out.Clients = make(map[string]ClientEntry, len(in.Clients))
	for k, v := range in.Clients {
		out.Clients[k] = v
	}
	out.Blocked = make([]string, len(in.Blocked))
	copy(out.Blocked, in.Blocked)
	out.Traffic.Days = make(map[string]TrafficBucket, len(in.Traffic.Days))
	for k, v := range in.Traffic.Days {
		out.Traffic.Days[k] = v
	}
	out.Traffic.History = make([]TrafficMonth, len(in.Traffic.History))
	copy(out.Traffic.History, in.Traffic.History)
	return &out
}
