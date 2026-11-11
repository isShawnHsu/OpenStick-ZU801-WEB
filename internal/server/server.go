// Package server implements the HTTP API and embedded web interface.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"uz801d/internal/auth"
	"uz801d/internal/buildinfo"
	"uz801d/internal/svc"
	"uz801d/web"
)

const sessionCookie = "uz801d_session"

type ctxKey int

const sessionKey ctxKey = 1

// Server wires the HTTP handlers to the service manager.
type Server struct {
	m        *svc.Manager
	sessions *auth.SessionStore
	limiter  *auth.LoginLimiter
	started  time.Time
	handler  http.Handler
	log      *log.Logger
}

// New creates a server. Call Handler to obtain the http.Handler.
func New(m *svc.Manager) *Server {
	cfg := m.Config()
	ttl := time.Duration(cfg.SessionHours) * time.Hour
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	s := &Server{
		m:        m,
		sessions: auth.NewSessionStore(ttl),
		limiter:  auth.NewLoginLimiter(6, 5*time.Minute),
		started:  time.Now(),
		log:      m.Logger(),
	}
	s.handler = s.routes()
	return s
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler { return s.handler }

// Sessions exposes the session store so main can run cleanup.
func (s *Server) Sessions() *auth.SessionStore { return s.sessions }

// Limiter exposes the login limiter so main can run cleanup.
func (s *Server) Limiter() *auth.LoginLimiter { return s.limiter }

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.apiHealth)
	mux.HandleFunc("GET /api/session", s.apiSession)
	mux.HandleFunc("POST /api/login", s.apiLogin)

	mux.Handle("POST /api/logout", s.protect(s.apiLogout))

	mux.Handle("GET /api/status", s.protect(s.apiStatus))
	mux.Handle("GET /api/network", s.protect(s.apiNetwork))

	mux.Handle("GET /api/modem", s.protect(s.apiModem))
	mux.Handle("POST /api/modem/connect", s.protect(s.apiModemConnect))
	mux.Handle("POST /api/modem/disconnect", s.protect(s.apiModemDisconnect))
	mux.Handle("POST /api/modem/apn", s.protect(s.apiAPNSave))
	mux.Handle("DELETE /api/modem/apn/{id}", s.protect(s.apiAPNDelete))
	mux.Handle("POST /api/modem/apn/{id}/activate", s.protect(s.apiAPNActivate))
	mux.Handle("POST /api/modem/modes", s.protect(s.apiModemModes))
	mux.Handle("POST /api/modem/scan", s.protect(s.apiModemScan))
	mux.Handle("POST /api/modem/register", s.protect(s.apiModemRegister))
	mux.Handle("POST /api/modem/ussd", s.protect(s.apiUSSD))
	mux.Handle("POST /api/modem/pin", s.protect(s.apiSIMPin))
	mux.Handle("POST /api/modem/reset", s.protect(s.apiModemReset))
	mux.Handle("POST /api/modem/flight", s.protect(s.apiModemFlight))

	mux.Handle("GET /api/sms", s.protect(s.apiSMSList))
	mux.Handle("GET /api/sms/{id}", s.protect(s.apiSMSGet))
	mux.Handle("POST /api/sms/send", s.protect(s.apiSMSSend))
	mux.Handle("POST /api/sms/read", s.protect(s.apiSMSRead))
	mux.Handle("DELETE /api/sms/{id}", s.protect(s.apiSMSDelete))
	mux.Handle("DELETE /api/sms", s.protect(s.apiSMSDeleteAll))

	mux.Handle("GET /api/wifi", s.protect(s.apiWifi))
	mux.Handle("POST /api/wifi", s.protect(s.apiWifiSave))
	mux.Handle("POST /api/wifi/restart", s.protect(s.apiWifiRestart))
	mux.Handle("POST /api/wifi/stop", s.protect(s.apiWifiStop))
	mux.Handle("GET /api/wifi/scan", s.protect(s.apiWifiScan))

	mux.Handle("GET /api/bridge", s.protect(s.apiBridge))
	mux.Handle("POST /api/bridge", s.protect(s.apiBridgeSave))
	mux.Handle("POST /api/bridge/enable", s.protect(s.apiBridgeEnable))
	mux.Handle("POST /api/bridge/disable", s.protect(s.apiBridgeDisable))
	mux.Handle("GET /api/bridge/scan", s.protect(s.apiBridgeScan))

	mux.Handle("GET /api/clients", s.protect(s.apiClients))
	mux.Handle("POST /api/clients/block", s.protect(s.apiClientBlock))
	mux.Handle("POST /api/clients/unblock", s.protect(s.apiClientUnblock))
	mux.Handle("POST /api/clients/rename", s.protect(s.apiClientRename))
	mux.Handle("POST /api/clients/forget", s.protect(s.apiClientsForget))

	mux.Handle("GET /api/lan", s.protect(s.apiLAN))
	mux.Handle("POST /api/lan", s.protect(s.apiLANSave))

	mux.Handle("GET /api/traffic", s.protect(s.apiTraffic))
	mux.Handle("POST /api/traffic", s.protect(s.apiTrafficSave))
	mux.Handle("POST /api/traffic/reset", s.protect(s.apiTrafficReset))
	mux.Handle("POST /api/traffic/lifetime", s.protect(s.apiTrafficLifetime))

	mux.Handle("GET /api/system", s.protect(s.apiSystem))
	mux.Handle("GET /api/system/logs", s.protect(s.apiLogs))
	mux.Handle("GET /api/system/frpc-log", s.protect(s.apiFrpcLog))
	mux.Handle("POST /api/system/frpc-log", s.protect(s.apiFrpcLogSave))
	mux.Handle("GET /api/system/leds", s.protect(s.apiLEDs))
	mux.Handle("POST /api/system/led", s.protect(s.apiLEDSet))
	mux.Handle("POST /api/system/password", s.protect(s.apiPassword))
	mux.Handle("POST /api/system/language", s.protect(s.apiLanguage))
	mux.Handle("POST /api/system/admin-user", s.protect(s.apiAdminUser))
	mux.Handle("POST /api/system/session-hours", s.protect(s.apiSessionHours))
	mux.Handle("POST /api/system/reboot", s.protect(s.apiReboot))
	mux.Handle("POST /api/system/poweroff", s.protect(s.apiPoweroff))
	mux.Handle("POST /api/system/factory-reset", s.protect(s.apiFactoryReset))
	mux.Handle("POST /api/system/ping", s.protect(s.apiPing))
	mux.Handle("POST /api/system/dns", s.protect(s.apiDNS))

	mux.Handle("GET /api/config", s.protect(s.apiConfigExport))

	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(web.Static()))))
	mux.HandleFunc("/", s.serveSPA)

	return s.security(s.accessLog(mux))
}

// protect requires an authenticated session and a CSRF token on mutations.
func (s *Server) protect(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess := s.sessionFrom(r)
		if sess == nil {
			fail(w, http.StatusUnauthorized, "未登录或会话已过期")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			token := r.Header.Get("X-CSRF-Token")
			if token == "" {
				token = r.Header.Get("X-UZ801D-CSRF")
			}
			if subtle.ConstantTimeCompare([]byte(token), []byte(sess.CSRF)) != 1 {
				fail(w, http.StatusForbidden, "CSRF 校验失败，请刷新页面后重试")
				return
			}
		}
		ctx := contextWithSession(r, sess)
		h(w, r.WithContext(ctx))
	})
}

func (s *Server) sessionFrom(r *http.Request) *auth.Session {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	return s.sessions.Get(c.Value)
}

func contextWithSession(r *http.Request, sess *auth.Session) context.Context {
	return context.WithValue(r.Context(), sessionKey, sess)
}

func sessionFromContext(r *http.Request) *auth.Session {
	sess, _ := r.Context().Value(sessionKey).(*auth.Session)
	return sess
}

func (s *Server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		h.Set("X-Robots-Tag", "noindex, nofollow")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += n
	return n, err
}

func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.status >= 400 || time.Since(start) > 3*time.Second {
			s.log.Printf("%s %s %d %s", r.Method, r.URL.Path, sw.status, time.Since(start).Round(time.Millisecond))
		}
	})
}

func (s *Server) serveSPA(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		fail(w, http.StatusNotFound, "接口不存在")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		fail(w, http.StatusMethodNotAllowed, "方法不允许")
		return
	}
	data, err := web.Static().Open("index.html")
	if err != nil {
		fail(w, http.StatusInternalServerError, "界面文件缺失")
		return
	}
	defer data.Close()
	body, _ := io.ReadAll(data)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(body)
}

// ---------------------------------------------------------------- API helpers

type response struct {
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

func ok(w http.ResponseWriter, data any) {
	writeJSON(w, http.StatusOK, response{OK: true, Data: data})
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, response{OK: false, Error: msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func decodeJSON(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("请求内容为空")
		}
		return errors.New("请求格式无效: " + err.Error())
	}
	return nil
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) apiHealth(w http.ResponseWriter, r *http.Request) {
	ok(w, map[string]any{
		"status":  "ok",
		"version": buildinfo.Version,
		"uptime":  int64(time.Since(s.started).Seconds()),
	})
}

// SessionPayload is returned to the browser for bootstrap.
type SessionPayload struct {
	Authenticated bool   `json:"authenticated"`
	User          string `json:"user,omitempty"`
	CSRF          string `json:"csrf,omitempty"`
	Language      string `json:"language"`
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	BuildTime     string `json:"build_time"`
	PasswordSet   bool   `json:"password_set"`
	InstallID     string `json:"install_id,omitempty"`
	Hostname      string `json:"hostname,omitempty"`
}

func (s *Server) sessionPayload(sess *auth.Session) SessionPayload {
	cfg := s.m.Config()
	p := SessionPayload{
		Language:    cfg.Language,
		Version:     buildinfo.Version,
		Commit:      buildinfo.Commit,
		BuildTime:   buildinfo.BuildTime,
		PasswordSet: cfg.PasswordHash != "",
	}
	if sess != nil {
		p.Authenticated = true
		p.User = sess.User
		p.CSRF = sess.CSRF
		p.InstallID = s.m.Store().Snapshot().InstallID
	}
	return p
}

func (s *Server) apiSession(w http.ResponseWriter, r *http.Request) {
	ok(w, s.sessionPayload(s.sessionFrom(r)))
}

func (s *Server) apiLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if allowed, wait := s.limiter.Allowed(ip); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		fail(w, http.StatusTooManyRequests, "尝试次数过多，请稍后再试")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg := s.m.Config()
	if cfg.PasswordHash == "" {
		fail(w, http.StatusServiceUnavailable, "尚未设置管理密码，请使用 -init 初始化")
		return
	}
	userOK := subtle.ConstantTimeCompare([]byte(strings.TrimSpace(body.Username)), []byte(cfg.AdminUser)) == 1
	passOK := auth.VerifyPassword(body.Password, cfg.PasswordHash)
	if !userOK || !passOK {
		s.limiter.Fail(ip)
		time.Sleep(200 * time.Millisecond)
		fail(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	s.limiter.Reset(ip)
	sess, err := s.sessions.Create(cfg.AdminUser, ip)
	if err != nil {
		fail(w, http.StatusInternalServerError, "创建会话失败")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    sess.Token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int((time.Duration(cfg.SessionHours) * time.Hour).Seconds()),
	})
	ok(w, s.sessionPayload(sess))
}

func (s *Server) apiLogout(w http.ResponseWriter, r *http.Request) {
	sess := sessionFromContext(r)
	if sess != nil {
		s.sessions.Delete(sess.Token)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	ok(w, map[string]bool{"logged_out": true})
}

func (s *Server) apiNetwork(w http.ResponseWriter, r *http.Request) {
	ok(w, s.m.Network(r.Context()))
}

func (s *Server) apiConfigExport(w http.ResponseWriter, r *http.Request) {
	cfg := s.m.Config().Clone()
	cfg.PasswordHash = ""
	ok(w, cfg)
}
