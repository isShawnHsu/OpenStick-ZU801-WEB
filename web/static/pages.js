/* UZ801 Manager - page renderers */
function get(o, path, def) {
  try {
    var v = path.split('.').reduce(function (a, k) { return a == null ? undefined : a[k]; }, o);
    return v === undefined || v === null ? def : v;
  } catch (e) { return def; }
}

function card(title, body, extra) {
  return '<section class="card">' +
    (title ? '<div class="card-head"><h2>' + esc(title) + '</h2>' + (extra || '') + '</div>' : '') +
    '<div class="card-body">' + body + '</div></section>';
}

function pageHead(title, subtitle, actions) {
  return '<div class="page-head"><div><h1>' + esc(title) + '</h1>' +
    (subtitle ? '<p class="muted">' + esc(subtitle) + '</p>' : '') + '</div>' +
    '<div class="head-actions">' + (actions || '') + '</div></div>';
}

function kv(label, value, kind) {
  return '<div class="kv"><span class="k">' + esc(label) + '</span><span class="v ' + (kind || '') + '">' + value + '</span></div>';
}

function field(label, inner, hint) {
  return '<label class="field"><span>' + esc(label) + '</span>' + inner +
    (hint ? '<small class="muted">' + esc(hint) + '</small>' : '') + '</label>';
}

function txt(name, value, ph, type) {
  return '<input type="' + (type || 'text') + '" name="' + attr(name) + '" value="' + attr(value == null ? '' : value) + '" placeholder="' + attr(ph || '') + '">';
}

function sel(name, value, options) {
  var html = '<select name="' + attr(name) + '">';
  options.forEach(function (o) {
    html += '<option value="' + attr(o[0]) + '"' + (String(o[0]) === String(value) ? ' selected' : '') + '>' + esc(o[1]) + '</option>';
  });
  return html + '</select>';
}

function check(name, checked, label) {
  return '<label class="check"><input type="checkbox" name="' + attr(name) + '"' + (checked ? ' checked' : '') + '><span>' + esc(label) + '</span></label>';
}

// dataSwitch renders the single cellular-data on/off control. Turning it on
// dials the current APN; turning it off disconnects without touching the APN
// selection or the bridge configuration. It is disabled while flight mode is
// on because the modem is powered down.
function dataSwitch(connected, flightMode) {
  if (flightMode) {
    return '<button class="ghost" type="button" disabled title="' + attr(t('cell.flightActive')) + '">' + esc(t('cell.dataOn')) + '</button>';
  }
  return connected
    ? '<button class="ghost" data-action="modem-disconnect">' + esc(t('cell.dataOff')) + '</button>'
    : '<button class="primary" data-action="modem-connect">' + esc(t('cell.dataOn')) + '</button>';
}

// flightButton toggles the cellular radio while leaving WiFi running.
function flightButton(on) {
  return on
    ? '<button class="primary" data-action="flight-off">' + esc(t('cell.flightDisable')) + '</button>'
    : '<button class="ghost" data-action="flight-on">' + esc(t('cell.flightEnable')) + '</button>';
}

function ifaceTable(net) {
  var list = get(net, 'interfaces', []);
  if (!list.length) return '<p class="muted">' + esc(t('common.none')) + '</p>';
  var rows = list.map(function (i) {
    var addr = i.address ? i.address + '/' + i.prefix : '-';
    return '<tr><td><strong>' + esc(i.name) + '</strong>' + (i.default ? ' ' + pill(t('net.default'), 'ok') : '') +
      (i.wireless ? ' ' + pill(t('net.wireless'), '') : '') + '</td>' +
      '<td>' + esc(addr) + '</td><td>' + esc(i.mac || '-') + '</td><td>' + esc(i.state || '-') + '</td>' +
      '<td>' + esc(bytes(i.rx_bytes)) + '<br><span class="muted">' + esc(bytes(i.tx_bytes)) + '</span></td></tr>';
  }).join('');
  return '<div class="table-wrap"><table><thead><tr><th>' + esc(t('common.interface')) + '</th><th>IP</th><th>MAC</th><th>' + esc(t('net.state')) + '</th><th>RX / TX</th></tr></thead><tbody>' + rows + '</tbody></table></div>';
}

/* ---------------------------------------------------------------- login */

function loginView() {
  var noPass = state.session && state.session.password_set === false;
  return '<div class="login-wrap"><div class="login-card">' +
    '<div class="brand big"><span class="dot ok"></span> UZ801 Manager</div>' +
    '<p class="muted">' + esc(t('login.subtitle')) + '</p>' +
    (noPass ? '<div class="note bad">' + esc(t('login.no_password')) + '</div>' : '') +
    '<form data-form="login" class="stack">' +
    field(t('login.username'), '<input type="text" name="username" autocomplete="username" required>') +
    field(t('login.password'), '<input type="password" name="password" autocomplete="current-password" required autofocus>') +
    '<div class="note bad" id="login-error" role="alert" hidden></div>' +
    '<button class="primary wide" type="submit">' + esc(t('login.submit')) + '</button>' +
    '</form>' +
    '<p class="muted small">' + esc(t('login.secure')) + '</p>' +
    '<button class="ghost" data-action="toggle-lang">' + (state.lang === 'zh' ? 'English' : '中文') + '</button>' +
    '</div></div>';
}

/* ---------------------------------------------------------------- dashboard */

function dashboardView(d) {
  var modem = d.modem || {};
  var info = modem.info || {};
  var wifi = d.wifi || {};
  var bridge = d.bridge || {};
  var sys = d.system || {};
  var tr = d.traffic || {};
  var net = d.network || {};
  var clients = d.clients || [];
  var online = clients.filter(function (c) { return c.online; }).length;
  var q = Number(info.signal_quality || 0);
  var cellBody;
  if (modem.flight_mode) {
    cellBody = '<div class="note warn">' + esc(t('cell.flightActive')) + '</div>' +
      '<p class="muted small">' + esc(t('cell.flightHint')) + '</p>' +
      '<div class="btn-row">' + flightButton(true) + '</div>';
  } else if (modem.modem_found) {
    cellBody = kv(t('dash.operator'), esc(info.operator_name || '-')) +
      kv(t('dash.registration'), esc(info.registration || '-')) +
      kv(t('dash.tech'), esc((info.access_tech || []).join(', ') || '-')) +
      kv(t('dash.signal'), bar(q, signalKind(q)) + '<span class="small">' + q + '%</span>') +
      kv('IP', esc(modem.ip || '-')) +
      kv('APN', esc(modem.profile || info.apn || '-')) +
      (modem.connected ? pill(t('common.connected'), 'ok') : pill(t('common.disconnected'), 'bad')) +
      '<div class="btn-row">' + dataSwitch(modem.connected) + flightButton(false) + '</div>';
  } else {
    cellBody = '<div class="note warn">' + esc(t('dash.noModem')) + '</div>' +
      '<div class="btn-row">' + flightButton(false) + '</div>';
  }

  var simBody = modem.sim_present
    ? kv('IMSI', esc(shortMask(get(modem, 'sim.imsi', ''), 8))) +
      kv('ICCID', esc(shortMask(get(modem, 'sim.iccid', ''), 10))) +
      kv(t('dash.operator'), esc(get(modem, 'sim.operator_name', '-') || '-')) +
      (info.unlock_required ? '<div class="note warn">' + esc(t('dash.unlock')) + ': ' + esc(info.unlock_required) + '</div>' : '')
    : '<div class="note warn">' + esc(t('dash.noSim')) + '</div>' +
      (bridge.active ? '<p class="small muted">' + esc(t('bridge.active')) + '</p>' : '');

  var wifiBody = kv(t('wifi.ssid'), esc(wifi.ssid || '-')) +
    kv(t('common.channel'), esc(wifi.channel || '-')) +
    kv(t('dash.connections'), esc(online)) +
    (wifi.bridge ? pill(t('wifi.bridgeActive'), 'warn') : (wifi.active ? pill(t('common.enabled'), 'ok') : pill(t('common.disabled'), 'bad'))) +
    '<div class="btn-row"><button class="ghost" data-action="wifi-restart">' + esc(t('dash.restartWifi')) + '</button></div>';

  var bridgeBody = (bridge.active ? pill(t('dash.bridgeOn'), 'ok') : pill(t('dash.bridgeOff'), 'bad')) +
    kv(t('dash.upstream'), esc(bridge.upstream_ssid || get(bridge, 'config.upstream_ssid', '-') || '-')) +
    kv(t('bridge.ap'), esc(bridge.ap_interface || wifi.ap_interface || '-')) +
    kv(t('common.channel'), esc(bridge.channel || '-')) +
    (bridge.last_error ? '<div class="note bad small">' + esc(bridge.last_error) + '</div>' : '') +
    '<div class="btn-row"><button class="' + (bridge.active ? 'ghost' : 'primary') + '" data-action="bridge-toggle">' +
    esc(bridge.active ? t('dash.closeBridge') : t('dash.openBridge')) + '</button>' +
    '<a class="btn ghost" href="#/bridge">' + esc(t('common.details')) + '</a></div>';

  var temp = Number(get(sys, 'cpu.temp_c', 0));
  var tempKind = temp >= 75 ? 'bad' : temp >= 60 ? 'warn' : 'ok';
  var memUsed = Number(get(sys, 'memory.used', 0));
  var memTotal = Number(get(sys, 'memory.total', 0));
  var memPct = memTotal ? (memUsed / memTotal * 100) : 0;
  var sysBody =
    kv(t('dash.temp'), '<span class="' + tempKind + '">' + temp.toFixed(1) + ' °C</span>') +
    kv(t('dash.freq'), esc(get(sys, 'cpu.freq_mhz', '-') + ' MHz')) +
    kv(t('dash.governor'), esc(get(sys, 'cpu.governor', '-'))) +
    kv(t('common.memory'), bar(memPct) + '<span class="small">' + esc(bytes(memUsed)) + ' / ' + esc(bytes(memTotal)) + '</span>') +
    kv(t('common.load'), esc(get(sys, 'load_text', '-') || (Number(get(sys, 'load1', 0)).toFixed(2)))) +
    kv(t('dash.uptime'), esc(sys.uptime_text || '-')) +
    kv(t('common.disk'), esc(bytes(sys.disk_used || 0)) + ' / ' + esc(bytes(sys.disk_total || 0)));

  var quota = Number(tr.quota_bytes || 0);
  var quotaPct = Number(tr.quota_percent || 0);
  var trBody = kv(t('traffic.total'), '<strong>' + esc(bytes(tr.total || 0)) + '</strong>') +
    kv(t('traffic.rx'), esc(bytes(tr.rx || 0))) +
    kv(t('traffic.tx'), esc(bytes(tr.tx || 0))) +
    kv(t('dash.now'), esc(rate(tr.rx_rate || 0)) + ' / ' + esc(rate(tr.tx_rate || 0))) +
    (quota > 0 ? kv(t('common.quota'), bar(quotaPct, quotaPct >= 90 ? 'bad' : quotaPct >= 70 ? 'warn' : '') + '<span class="small">' + esc(pct(quotaPct)) + '</span>') : '') +
    '<div class="btn-row"><a class="btn ghost" href="#/traffic">' + esc(t('common.details')) + '</a></div>';

  return pageHead(t('dash.title'), sys.hostname || '', '<button class="ghost" data-action="reload">' + esc(t('dash.refresh')) + '</button>') +
    '<div class="grid">' +
    card(t('dash.cellular'), cellBody) +
    card(t('dash.sim'), simBody) +
    card(t('dash.wifi'), wifiBody) +
    card(t('dash.bridge'), bridgeBody) +
    card(t('dash.system'), sysBody) +
    card(t('dash.traffic'), trBody) +
    '</div>' +
    card(t('dash.interfaces'), ifaceTable(net), '<a class="btn ghost small" href="#/network">' + esc(t('common.details')) + '</a>') +
    card(t('dash.quick'), '<div class="quick">' +
      (modem.flight_mode ? '' : '<button data-action="' + (modem.connected ? 'modem-disconnect' : 'modem-connect') + '">' + esc(modem.connected ? t('cell.dataOff') : t('cell.dataOn')) + '</button>') +
      '<button data-action="' + (modem.flight_mode ? 'flight-off' : 'flight-on') + '">' + esc(modem.flight_mode ? t('cell.flightDisable') : t('cell.flightEnable')) + '</button>' +
      '<button data-action="wifi-restart">' + esc(t('dash.restartWifi')) + '</button>' +
      '<button data-action="bridge-toggle">' + esc(bridge.active ? t('dash.closeBridge') : t('dash.openBridge')) + '</button>' +
      '<a class="btn ghost" href="#/clients">' + esc(t('nav.clients')) + '</a>' +
      '<a class="btn ghost" href="#/system">' + esc(t('nav.system')) + '</a></div>');
}

/* ---------------------------------------------------------------- network */

function networkView(net) {
  var dns = (net.dns || []).map(function (d) { return '<code>' + esc(d) + '</code>'; }).join(' ') || '-';
  var out = state.cache.toolOutput || '';
  return pageHead(t('net.title'), '', '<button class="ghost" data-action="reload">' + esc(t('common.refresh')) + '</button>') +
    card(t('net.interfaces'), ifaceTable(net)) +
    card(t('net.routes'), kv(t('net.gateway'), esc(net.gateway || '-')) + kv(t('net.default'), esc(net.default_iface || '-')) + kv(t('net.dns'), dns)) +
    card(t('net.tools'),
      '<div class="tools-grid">' +
      '<form data-form="ping" class="stack">' +
      field(t('net.target'), '<input type="text" name="host" value="' + attr(state.cache.pingHost || DEFAULT_PING_HOST) + '" placeholder="www.baidu.com" required>') +
      '<button class="primary" type="submit">' + esc(t('net.ping')) + '</button></form>' +
      '<form data-form="dns" class="stack">' +
      field(t('net.target'), '<input type="text" name="host" value="' + attr(state.cache.dnsHost || '') + '" placeholder="example.com" required>') +
      '<button class="primary" type="submit">' + esc(t('net.dnsLookup')) + '</button></form>' +
      '</div>' +
      '<h3>' + esc(t('net.output')) + '</h3><pre class="logs">' + esc(out || t('net.noOutput')) + '</pre>');
}

/* ---------------------------------------------------------------- cellular */

function apnForm(p) {
  p = p || {};
  return '<form data-form="apn" class="stack form-box">' +
    '<input type="hidden" name="id" value="' + attr(p.id || '') + '">' +
    '<div class="form-grid">' +
    field(t('cell.profileName'), txt('name', p.name || '', '中国移动')) +
    field('APN', txt('apn', p.apn || '', 'cmnet')) +
    field(t('cell.user'), txt('user', p.user || '', '')) +
    field(t('cell.password'), txt('password', p.password || '', '', 'password')) +
    field(t('cell.auth'), sel('auth', p.auth || 'none', [['none', 'None'], ['pap', 'PAP'], ['chap', 'CHAP'], ['mschap', 'MSCHAP'], ['mschapv2', 'MSCHAPv2']])) +
    field(t('cell.ipType'), sel('ip_type', p.ip_type || 'ipv4', [['ipv4', 'IPv4'], ['ipv4v6', 'IPv4/IPv6'], ['ipv6', 'IPv6']])) +
    '</div>' +
    check('activate', !p.id, t('cell.saveAndConnect')) +
    '<div class="btn-row"><button class="primary" type="submit">' + esc(t('common.save')) + '</button>' +
    '<button class="ghost" type="button" data-action="apn-cancel">' + esc(t('common.cancel')) + '</button></div></form>';
}

function cellularView(r) {
  var st = r.status || {};
  var info = st.info || {};
  var profiles = r.profiles || [];
  var active = r.active_profile || '';
  var editing = state.editingAPN;
  var editProfile = null;
  if (editing && editing !== 'new') {
    profiles.forEach(function (p) { if (p.id === editing) editProfile = p; });
  }
  var rows = profiles.map(function (p) {
    return '<tr><td><strong>' + esc(p.name) + '</strong>' + (p.id === active ? ' ' + pill(t('common.active'), 'ok') : '') + '</td>' +
      '<td><code>' + esc(p.apn) + '</code></td><td>' + esc(p.auth || 'none') + '</td><td>' + esc(p.ip_type || 'ipv4') + '</td>' +
      '<td class="row-actions">' +
      '<button class="small ghost" data-action="apn-connect" data-id="' + attr(p.id) + '">' + esc(t('cell.connect')) + '</button>' +
      '<button class="small ghost" data-action="apn-edit" data-id="' + attr(p.id) + '">' + esc(t('common.edit')) + '</button>' +
      '<button class="small danger" data-action="apn-delete" data-id="' + attr(p.id) + '">' + esc(t('common.delete')) + '</button></td></tr>';
  }).join('');

  var statusBody;
  if (st.flight_mode) {
    statusBody = '<div class="note warn">' + esc(t('cell.flightActive')) + '</div>' +
      '<p class="muted small">' + esc(t('cell.flightHint')) + '</p>' +
      '<div class="btn-row">' + flightButton(true) + '</div>';
  } else if (st.modem_found) {
    statusBody = '<div class="status-line">' + (st.connected ? pill(t('common.connected'), 'ok') : pill(t('common.disconnected'), 'bad')) +
      pill(info.state || t('common.unknown'), '') + '</div>' +
      '<div class="grid two">' +
      kv(t('cell.operator'), esc(info.operator_name || '-')) +
      kv(t('cell.registration'), esc(info.registration || '-')) +
      kv(t('cell.packet'), esc(info.packet_service || '-')) +
      kv(t('cell.tech'), esc((info.access_tech || []).join(', ') || '-')) +
      kv(t('cell.signalQuality'), bar(info.signal_quality || 0, signalKind(info.signal_quality)) + '<span class="small">' + esc(info.signal_quality || 0) + '%</span>') +
      kv('IP', esc(st.ip || '-')) +
      kv(t('cell.imei'), esc(info.imei || '-')) +
      kv(t('cell.firmware'), esc(info.revision || '-')) +
      kv(t('cell.connectionManager'), esc(st.connection_manager || '-')) +
      kv(t('cell.activeProfile'), esc(active || '-')) +
      '</div>' +
      '<div class="btn-row">' + dataSwitch(st.connected) + flightButton(false) +
      '<button class="ghost" data-action="modem-reset">' + esc(t('cell.resetModem')) + '</button></div>';
  } else {
    statusBody = '<div class="note warn">' + esc(t('dash.noModem')) + '</div>' +
      '<div class="btn-row">' + flightButton(false) + '</div>';
  }

  var simBody = st.sim_present
    ? kv('IMSI', esc(get(st, 'sim.imsi', '-') || '-')) + kv('ICCID', esc(get(st, 'sim.iccid', '-') || '-')) +
      kv(t('cell.operator'), esc(get(st, 'sim.operator_name', '-') || '-'))
    : '<div class="note warn">' + esc(t('cell.noSim')) + '</div>';

  var ops = state.cache.operators;
  var opHtml = '';
  if (ops && ops.length) {
    opHtml = '<div class="op-list">' + ops.map(function (o) {
      return '<div class="op"><div><strong>' + esc(o.name || o.code) + '</strong><div class="muted small">' + esc(o.code) + ' · ' + esc((o.tech || []).join(', ')) + ' · ' + esc(o.status || '') + '</div></div>' +
        '<button class="small" data-action="operator-register" data-code="' + attr(o.code) + '">' + esc(t('cell.register')) + '</button></div>';
    }).join('') + '</div>';
  } else {
    opHtml = '<p class="muted">' + esc(t('cell.scanEmpty')) + '</p>';
  }

  return pageHead(t('cell.title'), '', '<button class="ghost" data-action="reload">' + esc(t('common.refresh')) + '</button>') +
    card(t('cell.status'), statusBody) +
    card(t('dash.sim'), simBody) +
    card(t('cell.profiles'), '<div class="btn-row top"><button class="primary small" data-action="apn-new">' + esc(t('cell.addProfile')) + '</button></div>' +
      (rows ? '<div class="table-wrap"><table><thead><tr><th>' + esc(t('common.name')) + '</th><th>APN</th><th>' + esc(t('cell.auth')) + '</th><th>IP</th><th class="row-actions">' + esc(t('common.actions')) + '</th></tr></thead><tbody>' + rows + '</tbody></table></div>' : '<p class="muted">' + esc(t('common.none')) + '</p>') +
      (editing ? '<h3>' + esc(editing === 'new' ? t('cell.addProfile') : t('cell.editProfile')) + '</h3>' + apnForm(editProfile) : ''),
      '<button class="ghost small" data-action="modem-scan">' + esc(t('cell.scanOperators')) + '</button>') +
    card(t('cell.modes'),
      '<form data-form="modes" class="inline-form">' +
      sel('modes', r.allowed_modes || 'auto', [['auto', 'Auto'], ['4g', '4G only'], ['4g3g', '4G/3G'], ['3g', '3G only'], ['2g', '2G only']]) +
      '<button class="primary" type="submit">' + esc(t('common.save')) + '</button></form>' +
      '<div class="muted small">' + esc(t('cell.connectionManager')) + ': ' + esc(r.connection_manager || 'auto') + '</div>') +
    card(t('cell.scanOperators'), opHtml, '<button class="ghost small" data-action="operator-home">' + esc(t('cell.registerHome')) + '</button>') +
    card(t('cell.ussd'),
      '<form data-form="ussd" class="inline-form">' + txt('code', '', '*100#') +
      '<button class="primary" type="submit">' + esc(t('common.send')) + '</button></form>' +
      '<p class="muted small">' + esc(t('cell.ussdHint')) + '</p>' +
      '<form data-form="ussd-respond" class="inline-form">' + txt('code', '', t('cell.replyCode')) +
      '<button class="ghost" type="submit">' + esc(t('common.reply')) + '</button></form>' +
      (state.cache.ussd ? '<pre class="logs">' + esc(state.cache.ussd) + '</pre>' : '')) +
    card(t('cell.simPin'),
      '<form data-form="pin" class="form-grid">' +
      field(t('cell.pin'), '<input type="text" name="pin" inputmode="numeric" pattern="[0-9]{4,8}" required>') +
      field(t('cell.puk'), '<input type="text" name="puk" inputmode="numeric" pattern="[0-9]{8}">') +
      '<div class="btn-row"><button class="primary" type="submit">' + esc(t('cell.unlock')) + '</button></div></form>');
}

/* ---------------------------------------------------------------- SMS */

function smsView(r) {
  var list = r.messages || [];
  var items = list.map(function (m) {
    return '<div class="sms-item' + (m.read ? '' : ' unread') + '">' +
      '<div class="sms-main"><div><strong>' + esc(m.number || '-') + '</strong> ' +
      (!m.read ? pill(t('sms.unread'), 'warn') : '') + '</div>' +
      '<div class="muted small">' + esc(m.timestamp || '') + '</div>' +
      '<div class="sms-preview">' + esc(m.data ? t('sms.dataMessage') : (m.text || '')) + '</div></div>' +
      '<div class="sms-actions"><button class="small" data-action="sms-open" data-id="' + attr(m.id) + '">' + esc(t('common.details')) + '</button>' +
      '<button class="small danger" data-action="sms-delete" data-id="' + attr(m.id) + '">' + esc(t('common.delete')) + '</button></div></div>';
  }).join('');

  return pageHead(t('sms.title'), r.unread ? r.unread + ' ' + t('sms.unread') : '',
    '<button class="ghost" data-action="sms-read-all">' + esc(t('sms.markAll')) + '</button>' +
    '<button class="danger" data-action="sms-delete-all">' + esc(t('sms.deleteAll')) + '</button>') +
    card(t('sms.compose'), '<form data-form="sms" class="stack">' +
      '<div class="form-grid">' +
      field(t('sms.number'), '<input type="text" name="number" inputmode="tel" placeholder="+8613800138000" required>') +
      field(t('sms.text'), '<textarea name="text" rows="4" maxlength="1000" required></textarea>') +
      '</div><button class="primary" type="submit">' + esc(t('sms.send')) + '</button></form>') +
    card(t('sms.list'), items || '<p class="muted">' + esc(t('sms.empty')) + '</p>');
}

/* ---------------------------------------------------------------- WiFi */

function wifiView(r) {
  var st = r.status || {};
  var c = r.config || {};
  var scan = state.cache.wifiScan;
  var scanHtml = '';
  if (scan && scan.length) {
    scanHtml = '<div class="scan-list">' + scan.map(function (n) {
      return '<div class="scan-item"><div><strong>' + esc(n.ssid) + '</strong>' +
        '<div class="muted small">' + esc(n.security) + ' · ' + esc(n.channel || '-') + ' · ' + esc(n.signal) + '%</div></div>' +
        '<div class="btn-row"><button class="small ghost" data-action="wifi-use-ssid" data-ssid="' + attr(n.ssid) + '">' + esc(t('wifi.useSsid')) + '</button></div></div>';
    }).join('') + '</div>';
  } else {
    scanHtml = '<p class="muted">' + esc(t('wifi.scanEmpty')) + '</p>';
  }
  return pageHead(t('wifi.title'), st.ssid || '',
    '<button class="ghost" data-action="wifi-restart">' + esc(t('wifi.restart')) + '</button>' +
    '<button class="danger" data-action="wifi-stop">' + esc(t('wifi.stop')) + '</button>') +
    (st.bridge ? '<div class="note warn">' + esc(t('wifi.bridgeActive')) + '</div>' : '') +
    card(t('wifi.status'),
      '<div class="status-line">' + (st.active ? pill(t('common.enabled'), 'ok') : pill(t('common.disabled'), 'bad')) +
      pill(st.backend || 'auto', '') + '</div>' +
      '<div class="grid two">' +
      kv(t('wifi.ssid'), esc(st.ssid || '-')) + kv(t('common.channel'), esc(st.channel || '-')) +
      kv(t('wifi.clientCount'), esc(st.clients || 0)) + kv(t('wifi.state'), esc(st.state || '-')) +
      kv(t('common.interface'), esc(st.ap_interface || '-')) + kv(t('common.security'), esc(c.security || '-')) +
      '</div>') +
    card(t('wifi.title'), '<form data-form="wifi" class="stack">' +
      '<div class="form-grid">' +
      field(t('wifi.ssid'), txt('ssid', c.ssid || '')) +
      field(t('wifi.password'), '<input type="text" name="password" value="' + attr(c.password || '') + '" autocomplete="new-password">') +
      field(t('wifi.security'), sel('security', c.security || 'wpa2', [['wpa2', t('wifi.wpa2')], ['wpa3', t('wifi.wpa3')], ['open', t('wifi.open')]])) +
      field(t('wifi.band'), '<input type="text" name="band" value="2.4 GHz" disabled>') +
      field(t('wifi.channel'), '<input type="number" name="channel" min="0" max="14" value="' + attr(c.channel || 0) + '">') +
      field(t('wifi.maxClients'), '<input type="number" name="max_clients" min="1" max="64" value="' + attr(c.max_clients || 10) + '">') +
      field(t('wifi.country'), txt('country', c.country || 'CN', 'CN')) +
      field(t('wifi.backend'), sel('backend', c.backend || 'auto', [['auto', 'Auto'], ['nmcli', 'NetworkManager'], ['hostapd', 'hostapd'], ['none', 'None']])) +
      '</div>' +
      check('hidden', c.hidden, t('wifi.hidden')) +
      check('apply_on_start', c.apply_on_start, t('wifi.applyOnStart')) +
      '<p class="muted small">' + esc(t('wifi.note')) + '</p>' +
      '<div class="btn-row"><button class="primary" type="submit">' + esc(t('wifi.apply')) + '</button>' +
      '<button class="ghost" type="button" data-action="wifi-scan">' + esc(t('wifi.scan')) + '</button></div></form>') +
    card(t('wifi.scan'), scanHtml);
}

/* ---------------------------------------------------------------- bridge */

function bridgeView(r) {
  var st = r.status || {};
  var c = r.config || {};
  var cap = st.capability || {};
  var scan = state.cache.bridgeScan;
  var scanHtml = scan && scan.length
    ? '<div class="scan-list">' + scan.map(function (n) {
        return '<div class="scan-item"><div><strong>' + esc(n.ssid) + '</strong><div class="muted small">' + esc(n.security) + ' · ' + esc(n.channel || '-') + ' · ' + esc(n.signal) + '%</div></div>' +
          '<button class="small" data-action="bridge-use" data-ssid="' + attr(n.ssid) + '" data-security="' + attr(n.security || 'wpa2') + '">' + esc(t('bridge.use')) + '</button></div>';
      }).join('') + '</div>'
    : '<p class="muted">' + esc(t('bridge.scanEmpty')) + '</p>';

  var capBody = '<div class="status-line">' +
    (cap.supported ? pill(t('bridge.supported'), 'ok') : pill(t('bridge.unsupported'), 'warn')) + '</div>' +
    '<p class="muted">' + esc(cap.reason || '') + '</p>' +
    (cap.external_ap ? '<p class="small">' + esc(t('bridge.externalHint', { n: cap.external_ap })) + '</p>' : '') +
    (cap.interfaces && cap.interfaces.length ? '<p class="muted small">' + esc(cap.interfaces.join(', ')) + '</p>' : '') +
    check('force', c.force, t('bridge.force')) +
    '<p class="small muted">' + esc(t('bridge.warning')) + '</p>';

  var statusBody = '<div class="status-line">' + (st.active ? pill(t('bridge.active'), 'ok') : pill(t('bridge.inactive'), 'bad')) + '</div>' +
    '<div class="grid two">' +
    kv(t('bridge.upstream'), esc(st.upstream_ssid || c.upstream_ssid || '-')) +
    kv(t('bridge.upstream') + ' IP', esc(st.upstream_ip || '-')) +
    kv(t('bridge.ap'), esc(st.ap_interface || '-')) +
    kv(t('common.channel'), esc(st.channel || '-')) +
    kv(t('bridge.started'), esc(st.started_at ? dateText(st.started_at) : '-')) +
    kv(t('bridge.lastError'), esc(st.last_error || '-')) +
    '</div>';

  return pageHead(t('bridge.title'), t('bridge.desc'),
    '<button class="primary" data-action="bridge-enable">' + esc(t('bridge.enable')) + '</button>' +
    '<button class="danger" data-action="bridge-disable">' + esc(t('bridge.disable')) + '</button>') +
    card(t('bridge.capability'), capBody) +
    card(t('bridge.status'), statusBody) +
    card(t('bridge.title'), '<form data-form="bridge" class="stack">' +
      '<div class="form-grid">' +
      field(t('bridge.upstreamSsid'), txt('upstream_ssid', c.upstream_ssid || '', 'Upstream WiFi')) +
      field(t('bridge.upstreamPassword'), '<input type="text" name="upstream_password" value="' + attr(c.upstream_password || '') + '">') +
      field(t('bridge.upstreamSecurity'), sel('upstream_security', c.upstream_security || 'wpa2', [['wpa2', t('wifi.wpa2')], ['wpa3', t('wifi.wpa3')], ['open', t('wifi.open')], ['auto', 'Auto']])) +
      field(t('bridge.apInterface'), sel('ap_interface', c.ap_interface || 'auto', [['auto', t('bridge.autoIface')], ['uap0', 'uap0']].concat((cap.interfaces || []).filter(function (x) { return x !== 'uap0'; }).map(function (x) { return [x, x]; })))) +
      field(t('bridge.channel'), '<input type="number" name="channel" min="0" max="14" value="' + attr(c.channel || 0) + '">') +
      field(t('bridge.routeMetric'), '<input type="number" name="route_metric" min="0" max="99999" value="' + attr(c.route_metric || 50) + '">') +
      '</div>' +
      check('auto', c.auto, t('bridge.auto')) +
      '<p class="muted small">' + esc(t('bridge.autoNote')) + '</p>' +
      '<div class="btn-row"><button class="primary" type="submit">' + esc(t('common.save')) + '</button>' +
      '<button class="ghost" type="button" data-action="bridge-scan">' + esc(t('bridge.scan')) + '</button></div></form>') +
    card(t('bridge.scan'), scanHtml);
}

/* ---------------------------------------------------------------- clients */

function clientsView(r) {
  var list = r.clients || [];
  var rows = list.map(function (c) {
    return '<tr class="' + (c.online ? 'online' : '') + (c.blocked ? ' blocked' : '') + '">' +
      '<td><strong>' + esc(c.hostname || c.ip || '-') + '</strong>' + (c.blocked ? ' ' + pill(t('clients.blocked'), 'bad') : '') +
      '<div class="muted small">' + esc(c.ip || '-') + '</div></td>' +
      '<td><code>' + esc(c.mac) + '</code></td>' +
      '<td>' + (c.online ? pill(t('clients.online'), 'ok') : pill(t('clients.offline'), '')) + '</td>' +
      '<td>' + esc(c.signal || '-') + ' dBm</td>' +
      '<td>' + esc(bytes(c.rx_bytes || 0)) + ' / ' + esc(bytes(c.tx_bytes || 0)) + '</td>' +
      '<td class="small">' + (c.last_seen ? dateText(c.last_seen) : '-') + '</td>' +
      '<td class="row-actions">' +
      (c.blocked
        ? '<button class="small" data-action="client-unblock" data-mac="' + attr(c.mac) + '">' + esc(t('clients.unblock')) + '</button>'
        : '<button class="small danger" data-action="client-block" data-mac="' + attr(c.mac) + '">' + esc(t('clients.block')) + '</button>') +
      '<button class="small ghost" data-action="client-rename" data-mac="' + attr(c.mac) + '" data-name="' + attr(c.hostname || '') + '">' + esc(t('clients.rename')) + '</button>' +
      '</td></tr>';
  }).join('');
  var blocked = (r.blocked || []).map(function (m) {
    return '<span class="tag">' + esc(m) + ' <button class="link" data-action="client-unblock" data-mac="' + attr(m) + '">×</button></span>';
  }).join(' ');
  return pageHead(t('clients.title'), list.filter(function (c) { return c.online; }).length + ' ' + t('clients.online'),
    '<button class="ghost" data-action="clients-forget">' + esc(t('clients.forget')) + '</button>' +
    '<button class="ghost" data-action="reload">' + esc(t('common.refresh')) + '</button>') +
    card(t('clients.title'), rows
      ? '<div class="table-wrap"><table><thead><tr><th>' + esc(t('clients.hostname')) + '</th><th>MAC</th><th>' + esc(t('common.status')) + '</th><th>' + esc(t('clients.signal')) + '</th><th>RX / TX</th><th>' + esc(t('clients.lastSeen')) + '</th><th class="row-actions">' + esc(t('common.actions')) + '</th></tr></thead><tbody>' + rows + '</tbody></table></div>'
      : '<p class="muted">' + esc(t('clients.empty')) + '</p>') +
    card(t('clients.filter'), '<p class="muted small">' + esc(t('clients.filterHint')) + '</p>' + (blocked || '<p class="muted">' + esc(t('common.none')) + '</p>'));
}

/* ---------------------------------------------------------------- LAN */

function lanView(c) {
  var ip = c.ip || '', mask = c.netmask || '', start = c.dhcp_start || '', end = c.dhcp_end || '';
  var dns = (c.dns || []).join(', ');
  return pageHead(t('lan.title'), ip + ' / ' + mask) +
    '<div class="note warn">' + esc(t('lan.warning')) + '</div>' +
    card(t('lan.title'), '<form data-form="lan" class="stack">' +
      '<div class="form-grid">' +
      field(t('lan.ip'), txt('ip', ip)) +
      field(t('lan.netmask'), txt('netmask', mask)) +
      field(t('lan.dhcpStart'), txt('dhcp_start', start)) +
      field(t('lan.dhcpEnd'), txt('dhcp_end', end)) +
      field(t('lan.dns'), txt('dns', dns)) +
      field(t('lan.lease'), txt('lease_time', c.lease_time || '12h')) +
      '</div><button class="primary" type="submit">' + esc(t('lan.apply')) + '</button></form>');
}

/* ---------------------------------------------------------------- traffic */

function trafficView(st) {
  var quota = Number(st.quota_bytes || 0);
  var quotaMb = quota > 0 ? (quota / 1048576).toFixed(0) : '0';
  var qp = Number(st.quota_percent || 0);

  var monthRows = (st.months || []).map(function (m) {
    return '<tr><td><strong>' + esc(m.period || '-') + '</strong>' + (m.current ? ' ' + pill(t('traffic.current'), 'ok') : '') + '</td>' +
      '<td>' + esc(bytes(m.rx || 0)) + '</td><td>' + esc(bytes(m.tx || 0)) + '</td><td>' + esc(bytes(m.total || 0)) + '</td></tr>';
  }).join('');

  var dayRows = (st.days || []).map(function (d) {
    return '<tr><td>' + esc(d.date || '-') + '</td>' +
      '<td>' + esc(bytes(d.rx || 0)) + '</td><td>' + esc(bytes(d.tx || 0)) + '</td><td>' + esc(bytes(d.total || 0)) + '</td></tr>';
  }).join('');

  return pageHead(t('traffic.title'), st.period || '',
    '<button class="ghost" data-action="traffic-reset">' + esc(t('traffic.reset')) + '</button>') +
    card(t('traffic.title'),
      '<div class="grid two">' +
      kv(t('traffic.total'), '<strong>' + esc(bytes(st.total || 0)) + '</strong>') +
      kv(t('traffic.rx'), esc(bytes(st.rx || 0))) +
      kv(t('traffic.tx'), esc(bytes(st.tx || 0))) +
      kv(t('traffic.rate'), esc(rate(st.rx_rate || 0)) + ' / ' + esc(rate(st.tx_rate || 0))) +
      kv(t('traffic.lifetime'), esc(bytes((st.lifetime_rx || 0) + (st.lifetime_tx || 0)))) +
      kv(t('traffic.period'), esc(st.period || '-')) +
      '</div>' +
      (quota > 0 ? '<div class="quota-box">' + bar(qp, qp >= 90 ? 'bad' : qp >= 70 ? 'warn' : '') +
        '<div class="muted small">' + esc(bytes(st.total || 0)) + ' / ' + esc(bytes(quota)) + ' · ' + esc(pct(qp)) + '</div></div>' : '') +
      '<p class="muted small">' + esc(t('traffic.note')) + '</p>') +
    card(t('traffic.months'), monthRows
      ? '<div class="table-wrap"><table><thead><tr><th>' + esc(t('traffic.month')) + '</th><th>' + esc(t('traffic.rx')) + '</th><th>' + esc(t('traffic.tx')) + '</th><th>' + esc(t('traffic.total')) + '</th></tr></thead><tbody>' + monthRows + '</tbody></table></div>'
      : '<p class="muted">' + esc(t('traffic.noHistory')) + '</p>') +
    card(t('traffic.periodDaily'), dayRows
      ? '<div class="table-wrap"><table><thead><tr><th>' + esc(t('traffic.date')) + '</th><th>' + esc(t('traffic.rx')) + '</th><th>' + esc(t('traffic.tx')) + '</th><th>' + esc(t('traffic.total')) + '</th></tr></thead><tbody>' + dayRows + '</tbody></table></div>' +
        '<p class="muted small">' + esc(t('traffic.dailyNote')) + '</p>'
      : '<p class="muted">' + esc(t('common.none')) + '</p>') +
    card(t('traffic.title'), '<form data-form="traffic" class="stack">' +
      '<div class="form-grid">' +
      field(t('traffic.interface'), txt('interface', st.interface || '')) +
      field(t('traffic.quotaMb'), '<input type="number" name="quota_mb" min="0" value="' + attr(quotaMb) + '">') +
      field(t('traffic.resetDay'), '<input type="number" name="reset_day" min="1" max="28" value="' + attr(st.reset_day || 1) + '">') +
      '</div>' + check('enforce_quota', st.enforce_quota, t('traffic.enforce')) +
      '<div class="btn-row"><button class="primary" type="submit">' + esc(t('traffic.save')) + '</button>' +
      '<button class="danger" type="button" data-action="traffic-lifetime">' + esc(t('traffic.resetLifetime')) + '</button></div></form>');
}

/* ---------------------------------------------------------------- system */

function systemView(sys, leds) {
  var cpu = sys.cpu || {};
  var mem = sys.memory || {};
  var bat = sys.battery || {};
  var info = kv(t('system.hostname'), esc(sys.hostname || '-')) +
    kv(t('system.os'), esc(sys.os || '-')) +
    kv(t('system.kernel'), esc(sys.kernel || '-')) +
    kv(t('system.arch'), esc(sys.arch || '-')) +
    kv(t('system.model'), esc(sys.model || '-')) +
    kv(t('common.uptime'), esc(sys.uptime_text || '-')) +
    kv(t('system.timezone'), esc(sys.timezone || '-')) +
    kv(t('system.time'), esc(sys.time ? dateText(sys.time) : '-')) +
    kv(t('common.temperature'), esc((Number(cpu.temp_c || 0)).toFixed(1)) + ' °C') +
    kv(t('common.memory'), esc(bytes(mem.used || 0)) + ' / ' + esc(bytes(mem.total || 0))) +
    kv(t('common.disk'), esc(bytes(sys.disk_used || 0)) + ' / ' + esc(bytes(sys.disk_total || 0))) +
    (bat.present ? kv(t('system.battery'), esc(bat.capacity || 0) + '% · ' + esc(bat.status || '')) : '');

  var ledRows = (leds || []).map(function (l) {
    return '<tr><td><code>' + esc(l.name) + '</code></td><td>' + esc(l.trigger || '-') + '</td><td>' + esc(l.brightness) + ' / ' + esc(l.max) + '</td>' +
      '<td class="row-actions"><button class="small ghost" data-action="led-edit" data-name="' + attr(l.name) + '">' + esc(t('common.edit')) + '</button></td></tr>';
  }).join('');

  var logs = state.cache.logs || '';
  var logUnit = state.cache.logUnit || '';
  var logLines = state.cache.logLines || 200;
  var frpcLog = state.cache.frpcLog || '';
  var frpcLogLines = state.cache.frpcLogLines || 200;

  return pageHead(t('system.title'), sys.version ? 'v' + esc(sys.version) : '') +
    card(t('system.info'), '<div class="grid two">' + info + '</div>') +
    card(t('system.password'), '<form data-form="password" class="stack">' +
      '<div class="form-grid">' +
      field(t('system.oldPassword'), '<input type="password" name="old" autocomplete="current-password">') +
      field(t('system.newPassword'), '<input type="password" name="new" autocomplete="new-password" minlength="8" required>') +
      field(t('system.confirmPassword'), '<input type="password" name="confirm" autocomplete="new-password" minlength="8" required>') +
      '</div><button class="primary" type="submit">' + esc(t('common.save')) + '</button></form>') +
    card(t('system.account'), '<form data-form="account" class="stack">' +
      '<div class="form-grid">' +
      field(t('system.adminUser'), txt('user', state.session.user || 'admin')) +
      field(t('system.sessionHours'), '<input type="number" name="hours" min="1" max="720" value="12">') +
      '</div><button class="primary" type="submit">' + esc(t('common.save')) + '</button></form>') +
    card(t('system.logs'),
      '<div class="inline-form">' +
      sel('log_unit', logUnit, [['', t('common.all')], ['uz801d', 'uz801d'], ['frpc', 'frpc'], ['NetworkManager', 'NetworkManager'], ['ModemManager', 'ModemManager'], ['hostapd', 'hostapd'], ['dnsmasq', 'dnsmasq']]) +
      '<input type="number" name="log_lines" min="10" max="2000" value="' + attr(logLines) + '">' +
      '<button class="primary" data-action="logs-load">' + esc(t('system.loadLogs')) + '</button></div>' +
      '<pre class="logs">' + esc(logs || t('system.logsEmpty')) + '</pre>') +
    card(t('system.frpcLog'),
      '<form data-form="frpc-log" class="stack">' +
      '<div class="form-grid">' +
      field(t('system.frpcLogPath'), txt('frpc_log_path', sys.frpc_log_path || '', '/var/log/frpc.log')) +
      field(t('system.lines'), '<input type="number" name="frpc_log_lines" min="10" max="2000" value="' + attr(frpcLogLines) + '">') +
      '</div>' +
      '<div class="btn-row"><button class="primary" type="submit">' + esc(t('system.saveAndRead')) + '</button>' +
      '<button class="ghost" type="button" data-action="frpc-log-load">' + esc(t('system.loadLogs')) + '</button></div>' +
      '<p class="muted small">' + esc(t('system.frpcLogHint')) + '</p></form>' +
      '<pre class="logs">' + esc(frpcLog || t('system.frpcLogEmpty')) + '</pre>') +
    card(t('system.leds'), ledRows
      ? '<div class="table-wrap"><table><thead><tr><th>' + esc(t('system.ledName')) + '</th><th>' + esc(t('system.trigger')) + '</th><th>' + esc(t('system.brightness')) + '</th><th class="row-actions">' + esc(t('common.actions')) + '</th></tr></thead><tbody>' + ledRows + '</tbody></table></div>'
      : '<p class="muted">' + esc(t('system.noLeds')) + '</p>') +
    card(t('system.ping'), '<div class="inline-form">' +
      txt('target', '', '223.5.5.5') +
      '<button class="primary" data-action="diag-ping">' + esc(t('system.runPing')) + '</button>' +
      '<button class="ghost" data-action="diag-dns">' + esc(t('system.runDns')) + '</button></div>' +
      '<pre class="logs">' + esc(state.cache.diag || t('net.noOutput')) + '</pre>') +
    card(t('system.danger'), '<div class="btn-row">' +
      '<button class="ghost" data-action="system-reboot">' + esc(t('system.reboot')) + '</button>' +
      '<button class="ghost" data-action="system-poweroff">' + esc(t('system.poweroff')) + '</button>' +
      '<button class="danger" data-action="system-factory">' + esc(t('system.factoryReset')) + '</button></div>');
}
