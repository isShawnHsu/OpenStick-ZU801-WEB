// Package svc implements the privileged operations behind the web UI.
package svc

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"uz801d/internal/config"
	"uz801d/internal/run"
	"uz801d/internal/state"
)

const (
	// bridgeHostapdConf is written by the AP-STA relay, independent of the
	// normal access point configuration so a failed bridge can be rolled back.
	bridgeHostapdConf = "/etc/hostapd/uz801d-ap.conf"
	bridgeDNSMasqConf = "/etc/dnsmasq.d/uz801d-ap.conf"
	bridgeLeaseFile   = "/var/lib/misc/dnsmasq.leases"
	bridgeAPUnit      = "uz801d-ap.service"
	bridgeDHCPUnit    = "uz801d-dhcp.service"
	bridgeHostapdPID  = "/run/uz801d-ap.pid"
	bridgeDHCPPID     = "/run/uz801d-dhcp.pid"
	nftBridgeTable    = "uz801d_bridge"
	nftFilterTable    = "uz801d_filter"
	apNMConnection    = "uz801d-ap"
	modemNMConnection = "uz801d-modem"
)

// Options configures a Manager.
type Options struct {
	ConfigPath string
	StatePath  string
	Runner     *run.Runner
	Logger     *log.Logger
	Store      *state.Store
}

// Manager owns configuration, persisted state and all privileged services.
type Manager struct {
	mu      sync.RWMutex
	cfg     *config.Config
	cfgPath string

	store  *state.Store
	runner *run.Runner
	log    *log.Logger

	bridgeMu sync.Mutex
	bridge   bridgeRuntime

	// flightMu serialises cellular radio power changes.
	flightMu sync.Mutex

	trafficMu sync.Mutex
	traffic   trafficRuntime

	// autoMu serialises the cellular-data/bridge automatic state machine.
	autoMu sync.Mutex
	// bridgeRetryAfter blocks automatic bridge retries until the given time
	// after a failed start. Guarded by autoMu.
	bridgeRetryAfter time.Time
	// autoSuppressed is set after an explicit "stop hotspot" so the automatic
	// state machine does not bring the AP or the bridge back on the next tick.
	// Guarded by autoMu.
	autoSuppressed bool

	// smsForwardMu serialises SMS forwarding and guards the error-log throttle.
	smsForwardMu      sync.Mutex
	smsForwardLastErr string
	smsForwardErrAt   time.Time

	stopOnce sync.Once
	stopCh   chan struct{}
}

type bridgeRuntime struct {
	Active            bool      `json:"active"`
	APInterface       string    `json:"ap_interface"`
	UpstreamIface     string    `json:"upstream_interface"`
	UpstreamSSID      string    `json:"upstream_ssid"`
	Channel           int       `json:"channel"`
	StartedAt         time.Time `json:"started_at"`
	UpstreamDownSince time.Time `json:"upstream_down_since"`
	LastError         string    `json:"last_error"`
	FallbackMode      bool      `json:"fallback_mode"`
}

type trafficRuntime struct {
	lastRX   int64
	lastTX   int64
	lastAt   time.Time
	rxRate   float64
	txRate   float64
	rxTotal  int64
	txTotal  int64
	period   string
	sampled  bool
	lastSave time.Time
}

// New creates a Manager. The store may be nil, in which case a memory-only
// state is used (useful for tests).
func New(cfg *config.Config, opts Options) (*Manager, error) {
	if cfg == nil {
		return nil, errors.New("配置不能为空")
	}
	if opts.ConfigPath == "" {
		opts.ConfigPath = config.DefaultConfigPath
	}
	if opts.StatePath == "" {
		opts.StatePath = config.DefaultStatePath
	}
	if opts.Runner == nil {
		opts.Runner = run.New()
	}
	if opts.Logger == nil {
		opts.Logger = log.New(os.Stderr, "uz801d: ", log.LstdFlags)
	}
	store := opts.Store
	if store == nil {
		var err error
		store, err = state.Open(opts.StatePath)
		if err != nil {
			return nil, err
		}
	}
	m := &Manager{
		cfg:     cfg,
		cfgPath: opts.ConfigPath,
		store:   store,
		runner:  opts.Runner,
		log:     opts.Logger,
		stopCh:  make(chan struct{}),
	}
	return m, nil
}

// Config returns the current configuration. The returned value is immutable;
// callers must not mutate it.
func (m *Manager) Config() *config.Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// ConfigClone returns a deep copy safe for mutation.
func (m *Manager) ConfigClone() *config.Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.Clone()
}

// ConfigPath returns the path of the persisted configuration.
func (m *Manager) ConfigPath() string { return m.cfgPath }

// Store returns the state store.
func (m *Manager) Store() *state.Store { return m.store }

// Logger returns the manager logger.
func (m *Manager) Logger() *log.Logger { return m.log }

// Update applies fn to a copy of the configuration, validates and saves it,
// then swaps it in. On any error the previous configuration stays active.
func (m *Manager) Update(fn func(*config.Config) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	next := m.cfg.Clone()
	if err := fn(next); err != nil {
		return err
	}
	if err := config.Save(m.cfgPath, next); err != nil {
		return err
	}
	m.cfg = next
	return nil
}

// SetConfig replaces the whole configuration.
func (m *Manager) SetConfig(next *config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := config.Save(m.cfgPath, next); err != nil {
		return err
	}
	m.cfg = next
	return nil
}

// Run executes a command with a timeout.
func (m *Manager) Run(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	return m.runner.Output(ctx, timeout, name, args...)
}

// RunInput executes a command with stdin.
func (m *Manager) RunInput(ctx context.Context, timeout time.Duration, input, name string, args ...string) (string, error) {
	return m.runner.RunInput(ctx, timeout, input, name, args...)
}

// RunTry executes a command and returns its output, ignoring errors.
func (m *Manager) RunTry(ctx context.Context, timeout time.Duration, name string, args ...string) string {
	out, _ := m.runner.Output(ctx, timeout, name, args...)
	return out
}

// Available reports whether an executable is present in PATH.
func (m *Manager) Available(name string) bool { return run.Available(name) }

// WriteFile writes a configuration file atomically with the given mode.
func (m *Manager) WriteFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp-%d", path, time.Now().UnixNano())
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Chmod(path, mode)
}

// unitExists reports whether a systemd unit is installed.
func (m *Manager) unitExists(ctx context.Context, unit string) bool {
	if !m.Available("systemctl") {
		return false
	}
	res := m.runner.Run(ctx, 5*time.Second, "systemctl", "cat", unit)
	return res.Err == nil
}

// serviceActive reports whether a systemd unit is active.
func (m *Manager) serviceActive(ctx context.Context, unit string) bool {
	if !m.Available("systemctl") {
		return false
	}
	res := m.runner.Run(ctx, 5*time.Second, "systemctl", "is-active", "--quiet", unit)
	return res.Err == nil
}

// serviceAction runs a systemctl action, returning a friendly error.
func (m *Manager) serviceAction(ctx context.Context, action, unit string) error {
	_, err := m.Run(ctx, 25*time.Second, "systemctl", action, unit)
	return err
}

// Start begins background accounting and the automatic bridge monitor.
func (m *Manager) Start(ctx context.Context) {
	go m.trafficLoop(ctx)
	go m.autoLoop(ctx)
	go m.watchdogLoop(ctx)
	go m.smsForwardLoop(ctx)
	go m.resumeBridge(ctx)
	go m.applyFlightModeOnStart(ctx)
}

// Stop terminates background workers and flushes state.
func (m *Manager) Stop() {
	m.stopOnce.Do(func() { close(m.stopCh) })
	if err := m.store.Save(); err != nil {
		m.log.Printf("保存状态文件失败: %v", err)
	}
}

func (m *Manager) slept(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-m.stopCh:
		return false
	case <-t.C:
		return true
	}
}

func (m *Manager) trafficLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			if err := m.SampleTraffic(ctx); err != nil {
				m.log.Printf("流量统计失败: %v", err)
			}
			if time.Since(m.traffic.lastSave) >= 60*time.Second {
				if err := m.store.Save(); err != nil {
					m.log.Printf("保存状态文件失败: %v", err)
				}
				m.traffic.lastSave = time.Now()
			}
		}
	}
}

func (m *Manager) autoLoop(ctx context.Context) {
	// Wait for ModemManager to settle after boot.
	if !m.slept(15 * time.Second) {
		return
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.autoStep(ctx)
		}
	}
}

func (m *Manager) watchdogLoop(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.watchdogStep(ctx)
		}
	}
}

// PIDRunning reports whether the process in a pidfile is alive.
func PIDRunning(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	pid := strings.TrimSpace(string(data))
	if pid == "" {
		return false
	}
	_, err = os.Stat("/proc/" + pid)
	return err == nil
}

// KillPIDFile terminates the process referenced by a pidfile.
func KillPIDFile(ctx context.Context, r *run.Runner, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	pid := strings.TrimSpace(string(data))
	if pid == "" {
		return
	}
	_, _ = r.Output(ctx, 5*time.Second, "kill", pid)
	_ = os.Remove(path)
}
