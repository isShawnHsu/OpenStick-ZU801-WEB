package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"uz801d/internal/config"
	"uz801d/internal/svc"
)

type dashboard struct {
	Time     int64             `json:"time"`
	System   *svc.SystemInfo   `json:"system"`
	Modem    *svc.ModemStatus  `json:"modem"`
	Wifi     *svc.WifiStatus   `json:"wifi"`
	Bridge   *svc.BridgeStatus `json:"bridge"`
	Traffic  *svc.TrafficStats `json:"traffic"`
	Clients  []svc.Client      `json:"clients"`
	Network  *svc.NetworkInfo  `json:"network"`
	Language string            `json:"language"`
	Version  string            `json:"version"`
}

func (s *Server) apiStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var (
		wg      sync.WaitGroup
		system  *svc.SystemInfo
		modem   *svc.ModemStatus
		wifi    *svc.WifiStatus
		bridge  *svc.BridgeStatus
		traffic *svc.TrafficStats
		clients []svc.Client
		network *svc.NetworkInfo
	)
	wg.Add(7)
	go func() { defer wg.Done(); system = s.m.SystemInfo(ctx) }()
	go func() { defer wg.Done(); modem = s.m.ModemStatus(ctx) }()
	go func() { defer wg.Done(); wifi = s.m.WifiStatus(ctx) }()
	go func() { defer wg.Done(); bridge = s.m.BridgeStatus(ctx) }()
	go func() { defer wg.Done(); traffic = s.m.TrafficStats(ctx) }()
	go func() { defer wg.Done(); clients = s.m.Clients(ctx) }()
	go func() { defer wg.Done(); network = s.m.Network(ctx) }()
	wg.Wait()
	cfg := s.m.Config()
	ok(w, dashboard{
		Time:     time.Now().Unix(),
		System:   system,
		Modem:    modem,
		Wifi:     wifi,
		Bridge:   bridge,
		Traffic:  traffic,
		Clients:  clients,
		Network:  network,
		Language: cfg.Language,
		Version:  s.sessionPayload(nil).Version,
	})
}

func (s *Server) apiModem(w http.ResponseWriter, r *http.Request) {
	cfg := s.m.Config()
	ok(w, map[string]any{
		"status":             s.m.ModemStatus(r.Context()),
		"profiles":           cfg.Modem.Profiles,
		"active_profile":     cfg.Modem.ActiveProfile,
		"allowed_modes":      cfg.Modem.AllowedModes,
		"allow_roaming":      cfg.Modem.AllowRoaming,
		"ip_type":            cfg.Modem.IPType,
		"interface":          cfg.Modem.Interface,
		"connection_manager": cfg.Modem.ConnectionManager,
		"flight_mode":        cfg.Modem.FlightMode,
	})
}

func (s *Server) apiModemConnect(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Profile string `json:"profile"`
	}
	if err := decodeJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.m.ConnectAPN(r.Context(), body.Profile); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, s.m.ModemStatus(r.Context()))
}

func (s *Server) apiModemDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := s.m.DisconnectAPN(r.Context()); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, s.m.ModemStatus(r.Context()))
}

func (s *Server) apiAPNSave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Profile  config.APNProfile `json:"profile"`
		Activate bool              `json:"activate"`
	}
	if err := decodeJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := s.m.SaveAPN(body.Profile, body.Activate)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	resp := map[string]any{
		"profiles":   s.m.Config().Modem.Profiles,
		"profile_id": id,
		"connected":  false,
	}
	// "激活并连接" must actually dial after storing the profile. A dial
	// failure is reported separately so the UI can tell the user that the
	// profile itself was saved successfully.
	if body.Activate {
		if err := s.m.ConnectAPN(r.Context(), id); err != nil {
			resp["connect_error"] = err.Error()
		} else {
			resp["connected"] = true
		}
	}
	ok(w, resp)
}

func (s *Server) apiAPNDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.m.DeleteAPN(r.PathValue("id")); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, s.m.Config().Modem.Profiles)
}

func (s *Server) apiAPNActivate(w http.ResponseWriter, r *http.Request) {
	if err := s.m.ActivateAPN(r.PathValue("id")); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]any{"active_profile": s.m.Config().Modem.ActiveProfile})
}

func (s *Server) apiModemModes(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Modes string `json:"modes"`
	}
	if err := decodeJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.m.SetAllowedModes(r.Context(), body.Modes); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, s.m.ModemStatus(r.Context()))
}

func (s *Server) apiModemScan(w http.ResponseWriter, r *http.Request) {
	ops, err := s.m.ScanOperators(r.Context())
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, ops)
}

func (s *Server) apiModemRegister(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.m.RegisterOperator(r.Context(), strings.TrimSpace(body.Code)); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, s.m.ModemStatus(r.Context()))
}

func (s *Server) apiUSSD(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string `json:"action"`
		Code   string `json:"code"`
	}
	if err := decodeJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := s.m.USSD(r.Context(), body.Action, body.Code)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]string{"output": out})
}

func (s *Server) apiSIMPin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind string `json:"kind"`
		Pin  string `json:"pin"`
		PUK  string `json:"puk"`
	}
	if err := decodeJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.m.SetSIMPin(r.Context(), body.Kind, body.Pin, body.PUK); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, s.m.ModemStatus(r.Context()))
}

func (s *Server) apiModemReset(w http.ResponseWriter, r *http.Request) {
	if err := s.m.ResetModem(r.Context()); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]bool{"resetting": true})
}

// apiModemFlight toggles flight mode: the cellular radio is powered down while
// WiFi keeps running. Leaving flight mode restores the cellular-data state that
// was active when flight mode was entered.
func (s *Server) apiModemFlight(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enable *bool `json:"enable"`
	}
	if err := decodeJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Enable == nil {
		fail(w, http.StatusBadRequest, "缺少 enable 参数")
		return
	}
	if err := s.m.SetFlightMode(r.Context(), *body.Enable); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, s.m.ModemStatus(r.Context()))
}

// ---------------------------------------------------------------- SMS

func (s *Server) apiSMSList(w http.ResponseWriter, r *http.Request) {
	list, err := s.m.ListSMS(r.Context())
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	unread := 0
	for _, m := range list {
		if !m.Read {
			unread++
		}
	}
	ok(w, map[string]any{"messages": list, "unread": unread})
}

func (s *Server) apiSMSGet(w http.ResponseWriter, r *http.Request) {
	msg, err := s.m.GetSMS(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, msg)
}

func (s *Server) apiSMSSend(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Number string `json:"number"`
		Text   string `json:"text"`
	}
	if err := decodeJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := s.m.SendSMS(r.Context(), body.Number, body.Text)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]string{"id": id})
}

func (s *Server) apiSMSRead(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
		All bool     `json:"all"`
	}
	if err := decodeJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.All {
		list, err := s.m.ListSMS(r.Context())
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		for _, m := range list {
			body.IDs = append(body.IDs, m.ID)
		}
	}
	s.m.MarkSMSRead(body.IDs)
	ok(w, map[string]bool{"saved": true})
}

func (s *Server) apiSMSDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.m.DeleteSMS(r.Context(), r.PathValue("id")); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]bool{"deleted": true})
}

func (s *Server) apiSMSDeleteAll(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("confirm") != "yes" {
		fail(w, http.StatusBadRequest, "请确认删除全部短信")
		return
	}
	if err := s.m.DeleteAllSMS(r.Context()); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]bool{"deleted": true})
}

// ---------------------------------------------------------------- WiFi

func (s *Server) apiWifi(w http.ResponseWriter, r *http.Request) {
	cfg := s.m.Config()
	ok(w, map[string]any{
		"status": s.m.WifiStatus(r.Context()),
		"config": cfg.Wifi,
	})
}

func (s *Server) apiWifiSave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Backend      string `json:"backend"`
		Interface    string `json:"interface"`
		Connection   string `json:"connection"`
		SSID         string `json:"ssid"`
		Password     string `json:"password"`
		Security     string `json:"security"`
		Band         string `json:"band"`
		Channel      int    `json:"channel"`
		Hidden       bool   `json:"hidden"`
		MaxClients   int    `json:"max_clients"`
		Country      string `json:"country"`
		ApplyOnStart bool   `json:"apply_on_start"`
	}
	if err := decodeJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	err := s.m.Update(func(c *config.Config) error {
		if body.Backend != "" {
			c.Wifi.Backend = body.Backend
		}
		if strings.TrimSpace(body.Interface) != "" {
			c.Wifi.Interface = strings.TrimSpace(body.Interface)
		}
		c.Wifi.Connection = strings.TrimSpace(body.Connection)
		if body.SSID != "" {
			c.Wifi.SSID = body.SSID
		}
		if body.Password != "" {
			c.Wifi.Password = body.Password
		}
		if body.Security != "" {
			c.Wifi.Security = body.Security
		}
		if body.Band != "" {
			c.Wifi.Band = body.Band
		}
		c.Wifi.Channel = body.Channel
		c.Wifi.Hidden = body.Hidden
		if body.MaxClients > 0 {
			c.Wifi.MaxClients = body.MaxClients
		}
		c.Wifi.Country = body.Country
		c.Wifi.ApplyOnStart = body.ApplyOnStart
		return nil
	})
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	s.m.ResumeAutoBridge()
	if err := s.m.ApplyWifi(r.Context()); err != nil {
		fail(w, http.StatusBadRequest, "配置已保存，但应用失败: "+err.Error())
		return
	}
	ok(w, s.m.WifiStatus(r.Context()))
}

func (s *Server) apiWifiRestart(w http.ResponseWriter, r *http.Request) {
	s.m.ResumeAutoBridge()
	if err := s.m.ApplyWifi(r.Context()); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, s.m.WifiStatus(r.Context()))
}

func (s *Server) apiWifiStop(w http.ResponseWriter, r *http.Request) {
	if err := s.m.StopWifi(r.Context()); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]bool{"stopped": true})
}

func (s *Server) apiWifiScan(w http.ResponseWriter, r *http.Request) {
	list, err := s.m.ScanWifi(r.Context())
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, list)
}

// ---------------------------------------------------------------- bridge

func (s *Server) apiBridge(w http.ResponseWriter, r *http.Request) {
	ok(w, map[string]any{
		"status": s.m.BridgeStatus(r.Context()),
		"config": s.m.Config().Wifi.Bridge,
	})
}

func (s *Server) apiBridgeSave(w http.ResponseWriter, r *http.Request) {
	var body config.BridgeConfig
	if err := decodeJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	err := s.m.Update(func(c *config.Config) error {
		old := c.Wifi.Bridge
		if body.UpstreamPassword == "" && body.UpstreamSSID == old.UpstreamSSID {
			body.UpstreamPassword = old.UpstreamPassword
		}
		if body.UpstreamConnection == "" {
			body.UpstreamConnection = old.UpstreamConnection
		}
		if body.UpstreamSecurity == "" {
			body.UpstreamSecurity = old.UpstreamSecurity
		}
		if body.RouteMetric == 0 {
			body.RouteMetric = old.RouteMetric
		}
		if body.APInterface == "" {
			body.APInterface = old.APInterface
		}
		body.Enabled = old.Enabled
		body.PreviousAPConnection = old.PreviousAPConnection
		c.Wifi.Bridge = body
		return nil
	})
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, s.m.Config().Wifi.Bridge)
}

func (s *Server) apiBridgeEnable(w http.ResponseWriter, r *http.Request) {
	if s.m.CellularDataActive(r.Context()) {
		fail(w, http.StatusBadRequest, "蜂窝数据已开启，请先关闭流量再启用桥接")
		return
	}
	s.m.AllowBridgeRetry()
	s.m.ResumeAutoBridge()
	if err := s.m.EnableBridge(r.Context()); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, s.m.BridgeStatus(r.Context()))
}

func (s *Server) apiBridgeDisable(w http.ResponseWriter, r *http.Request) {
	if err := s.m.DisableBridge(r.Context()); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, s.m.BridgeStatus(r.Context()))
}

func (s *Server) apiBridgeScan(w http.ResponseWriter, r *http.Request) {
	list, err := s.m.BridgeScan(r.Context())
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, list)
}

// ---------------------------------------------------------------- clients

func (s *Server) apiClients(w http.ResponseWriter, r *http.Request) {
	ok(w, map[string]any{
		"clients": s.m.Clients(r.Context()),
		"blocked": s.m.Store().Blocked(),
	})
}

func (s *Server) apiClientBlock(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MAC string `json:"mac"`
	}
	if err := decodeJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.m.BlockClient(r.Context(), body.MAC); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, s.m.Store().Blocked())
}

func (s *Server) apiClientUnblock(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MAC string `json:"mac"`
	}
	if err := decodeJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.m.UnblockClient(r.Context(), body.MAC); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, s.m.Store().Blocked())
}

func (s *Server) apiClientRename(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MAC  string `json:"mac"`
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.m.RenameClient(body.MAC, body.Name); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]bool{"saved": true})
}

func (s *Server) apiClientsForget(w http.ResponseWriter, r *http.Request) {
	s.m.ClearClientHistory(r.Context())
	ok(w, map[string]bool{"cleared": true})
}

// ---------------------------------------------------------------- LAN

func (s *Server) apiLAN(w http.ResponseWriter, r *http.Request) {
	ok(w, s.m.Config().LAN)
}

func (s *Server) apiLANSave(w http.ResponseWriter, r *http.Request) {
	var body config.LANConfig
	if err := decodeJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	err := s.m.Update(func(c *config.Config) error {
		c.LAN = body
		return nil
	})
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.m.ApplyWifi(r.Context()); err != nil {
		fail(w, http.StatusBadRequest, "配置已保存，但应用失败: "+err.Error())
		return
	}
	ok(w, s.m.Config().LAN)
}

// ---------------------------------------------------------------- traffic

func (s *Server) apiTraffic(w http.ResponseWriter, r *http.Request) {
	ok(w, s.m.TrafficStats(r.Context()))
}

func (s *Server) apiTrafficSave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Interface    string `json:"interface"`
		QuotaBytes   int64  `json:"quota_bytes"`
		ResetDay     int    `json:"reset_day"`
		EnforceQuota bool   `json:"enforce_quota"`
	}
	if err := decodeJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.m.SetTrafficConfig(body.Interface, body.QuotaBytes, body.ResetDay, body.EnforceQuota); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, s.m.TrafficStats(r.Context()))
}

func (s *Server) apiTrafficReset(w http.ResponseWriter, r *http.Request) {
	s.m.ResetTraffic()
	ok(w, s.m.TrafficStats(r.Context()))
}

func (s *Server) apiTrafficLifetime(w http.ResponseWriter, r *http.Request) {
	s.m.ResetLifetime()
	ok(w, s.m.TrafficStats(r.Context()))
}

// ---------------------------------------------------------------- system

func (s *Server) apiSystem(w http.ResponseWriter, r *http.Request) {
	ok(w, s.m.SystemInfo(r.Context()))
}

func (s *Server) apiLogs(w http.ResponseWriter, r *http.Request) {
	lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	unit := r.URL.Query().Get("unit")
	text, err := s.m.Logs(r.Context(), lines, unit)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]string{"logs": text})
}

func (s *Server) apiFrpcLog(w http.ResponseWriter, r *http.Request) {
	lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	path := s.m.Config().System.FrpcLogPath
	text, err := s.m.ReadLogFile(path, lines)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]string{"path": path, "logs": text})
}

func (s *Server) apiFrpcLogSave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.m.SetFrpcLogPath(body.Path); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]string{"path": s.m.Config().System.FrpcLogPath})
}

func (s *Server) apiLEDs(w http.ResponseWriter, r *http.Request) {
	ok(w, s.m.ListLEDs())
}

func (s *Server) apiLEDSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name       string `json:"name"`
		Trigger    string `json:"trigger"`
		Brightness *int   `json:"brightness"`
	}
	if err := decodeJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	b := -1
	if body.Brightness != nil {
		b = *body.Brightness
	}
	if err := s.m.SetLED(body.Name, body.Trigger, b); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, s.m.ListLEDs())
}

func (s *Server) apiPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Old string `json:"old"`
		New string `json:"new"`
	}
	if err := decodeJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.m.ChangePassword(body.Old, body.New); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if sess := sessionFromContext(r); sess != nil {
		s.sessions.DeleteOthers(sess.Token)
	}
	ok(w, map[string]bool{"changed": true})
}

func (s *Server) apiLanguage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Language string `json:"language"`
	}
	if err := decodeJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.m.SetLanguage(body.Language); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]string{"language": body.Language})
}

func (s *Server) apiAdminUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		User string `json:"user"`
	}
	if err := decodeJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.m.SetAdminUser(body.User); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]string{"user": s.m.Config().AdminUser})
}

func (s *Server) apiSessionHours(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Hours int `json:"hours"`
	}
	if err := decodeJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.m.SetSessionHours(body.Hours); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]int{"hours": s.m.Config().SessionHours})
}

func (s *Server) apiReboot(w http.ResponseWriter, r *http.Request) {
	ok(w, map[string]bool{"restarting": true})
	go func() {
		time.Sleep(1500 * time.Millisecond)
		if err := s.m.Reboot(context.Background()); err != nil {
			s.log.Printf("重启失败: %v", err)
		}
	}()
}

func (s *Server) apiPoweroff(w http.ResponseWriter, r *http.Request) {
	ok(w, map[string]bool{"powering_off": true})
	go func() {
		time.Sleep(1500 * time.Millisecond)
		if err := s.m.Poweroff(context.Background()); err != nil {
			s.log.Printf("关机失败: %v", err)
		}
	}()
}

func (s *Server) apiFactoryReset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		KeepPassword *bool `json:"keep_password"`
	}
	if err := decodeJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	keep := true
	if body.KeepPassword != nil {
		keep = *body.KeepPassword
	}
	if err := s.m.FactoryReset(r.Context(), keep); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if !keep {
		s.sessions.DeleteOthers("")
	}
	ok(w, map[string]bool{"reset": true, "keep_password": keep})
}

func (s *Server) apiPing(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Host  string `json:"host"`
		Count int    `json:"count"`
	}
	if err := decodeJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	out, err := s.m.Ping(r.Context(), body.Host, body.Count)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]string{"output": out})
}

func (s *Server) apiDNS(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Host string `json:"host"`
	}
	if err := decodeJSON(r, &body); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	addrs, err := s.m.DNSLookup(r.Context(), body.Host)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, map[string]any{"addresses": addrs})
}

var _ = strconv.Itoa
