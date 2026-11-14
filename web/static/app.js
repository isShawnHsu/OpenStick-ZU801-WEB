/* MSM8916 Manager - application shell, actions and bootstrap */
var PAGES = ['dashboard', 'network', 'cellular', 'sms', 'wifi', 'bridge', 'clients', 'lan', 'traffic', 'system'];
// The network page pings Baidu automatically so the user has a working target
// without typing anything. A custom target is kept until the page is reloaded.
var DEFAULT_PING_HOST = 'www.baidu.com';

function currentPage() {
  var raw = (location.hash || '').replace(/^#\/?/, '').split('?')[0];
  return PAGES.indexOf(raw) >= 0 ? raw : 'dashboard';
}

function documentLang() { return state.lang === 'zh' ? 'zh-CN' : 'en'; }

async function api(path, opts) {
  opts = opts || {};
  var headers = { 'Accept': 'application/json' };
  if (opts.body !== undefined) headers['Content-Type'] = 'application/json';
  if (state.csrf) headers['X-CSRF-Token'] = state.csrf;
  var res;
  try {
    res = await fetch(path, {
      method: opts.method || 'GET',
      headers: headers,
      body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
      credentials: 'same-origin',
      cache: 'no-store'
    });
  } catch (e) {
    var err = new Error(t('error.network'));
    err.cause = e;
    throw err;
  }
  var data = null;
  try { data = await res.json(); } catch (e) { data = null; }
  if (res.status === 401 && path !== '/api/session' && path !== '/api/login') {
    state.session = { authenticated: false, language: state.lang, password_set: true };
    state.csrf = '';
    render();
    var e401 = new Error((data && data.error) || t('error.unauthorized'));
    e401.status = 401;
    throw e401;
  }
  if (!res.ok || (data && data.ok === false)) {
    var err2 = new Error((data && data.error) || ('HTTP ' + res.status));
    err2.status = res.status;
    throw err2;
  }
  return data ? data.data : null;
}

function toast(message, kind) {
  var box = document.getElementById('toast');
  if (!box) return;
  var el = document.createElement('div');
  el.className = 'toast ' + (kind || '');
  el.textContent = message;
  box.appendChild(el);
  setTimeout(function () { if (el.parentNode) el.parentNode.removeChild(el); }, kind === 'bad' ? 6000 : 3500);
}

function openModal(title, body) {
  var m = document.getElementById('modal');
  if (!m) return;
  m.innerHTML = '<div class="modal-backdrop" data-action="modal-close"></div>' +
    '<div class="modal"><div class="modal-head"><h2>' + esc(title) + '</h2>' +
    '<button class="ghost small" data-action="modal-close" type="button">×</button></div>' +
    '<div class="modal-body">' + body + '</div></div>';
  document.body.classList.add('modal-open');
}

function closeModal() {
  var m = document.getElementById('modal');
  if (m) m.innerHTML = '';
  document.body.classList.remove('modal-open');
}

function formObject(form) {
  var out = {};
  new FormData(form).forEach(function (v, k) { out[k] = typeof v === 'string' ? v : ''; });
  form.querySelectorAll('input[type="checkbox"]').forEach(function (c) { out[c.name] = c.checked; });
  return out;
}

function setValue(name, value) {
  var el = document.querySelector('[name="' + name + '"]');
  if (el) el.value = value;
}

// smsForwardPayload builds the API body from the cached forwarding settings.
function smsForwardPayload(cfg, providers) {
  cfg = cfg || {};
  return {
    forward_enabled: !!cfg.forward_enabled,
    poll_seconds: parseInt(cfg.poll_seconds, 10) || 60,
    providers: providers || []
  };
}

// syncSmsForwardForm shows only the fields and guide that belong to the chosen
// channel: Bark uses the device key/group/sound, DingTalk and Feishu use a
// webhook plus an optional signing secret.
function syncSmsForwardForm(form, type) {
  type = type || 'bark';
  var bark = type === 'bark';
  var url = form.querySelector('input[name="url"]');
  if (url) {
    var prevType = form.dataset.forwardType || type;
    var prevDefault = smsForwardDefaultUrl(prevType);
    if (url.value === '' || url.value === prevDefault) url.value = smsForwardDefaultUrl(type);
    url.placeholder = smsForwardUrlPlaceholder(type);
  }
  form.dataset.forwardType = type;
  ['token', 'group', 'sound'].forEach(function (name) {
    var el = form.querySelector('[name="' + name + '"]');
    var box = el && el.closest('.field');
    if (box) box.hidden = !bark;
  });
  var secret = form.querySelector('[name="secret"]');
  var secretBox = secret && secret.closest('.field');
  if (secretBox) secretBox.hidden = bark;
  var guide = form.querySelector('.forward-guide-wrap');
  if (guide) guide.innerHTML = smsForwardGuide(type);
}

function initSmsForwardForms(root) {
  if (!root) return;
  root.querySelectorAll('form[data-form="sms-forward-provider"], form[data-form="sms-forward-add"]').forEach(function (form) {
    var selEl = form.querySelector('select[name="type"]');
    if (selEl) syncSmsForwardForm(form, selEl.value);
  });
}

function topStatusText() {
  var d = state.last;
  if (!d) return '';
  var bits = [];
  var m = d.modem || {};
  if (m.modem_found) {
    if (m.connected) bits.push('LTE');
    else if (m.sim_present) bits.push('SIM');
    else bits.push('No SIM');
  }
  var w = d.wifi || {};
  if (w.active) bits.push('WiFi ' + (w.ssid || ''));
  if (d.bridge && d.bridge.active) bits.push('BRIDGE');
  if (d.traffic) bits.push(bytes(d.traffic.total || 0));
  return bits.join(' · ');
}

function updateChrome() {
  var el = document.getElementById('top-status');
  if (el) el.textContent = topStatusText();
}

function shell(page) {
  var links = PAGES.map(function (p) {
    return '<a href="#/' + p + '" class="' + (p === page ? 'active' : '') + '">' + esc(t('nav.' + p)) + '</a>';
  }).join('');
  var ver = state.session && state.session.version ? state.session.version : '';
  return '<header class="topbar">' +
    '<div class="brand"><span class="dot ok"></span><span>MSM8916 Manager</span>' + (ver ? '<small class="ver">v' + esc(ver) + '</small>' : '') + '</div>' +
    '<span id="top-status" class="top-status">' + esc(topStatusText()) + '</span>' +
    '<div class="top-actions">' +
    '<button class="ghost small" data-action="toggle-lang" type="button">' + (state.lang === 'zh' ? 'EN' : '中') + '</button>' +
    '<button class="ghost small" data-action="logout" type="button">' + esc(t('common.logout')) + '</button>' +
    '</div></header>' +
    '<nav class="tabs">' + links + '</nav>' +
    '<main id="main"><div class="loading">' + esc(t('common.loading')) + '</div></main>' +
    '<footer class="foot">MSM8916 Manager · OpenStick Debian · ' + esc(ver || '') + '</footer>' +
    '<div id="toast"></div><div id="modal"></div>';
}

function render() {
  var app = document.getElementById('app');
  if (!app) return;
  if (!state.session) {
    app.innerHTML = '<div class="loading">' + esc(t('common.loading')) + '</div>';
    return;
  }
  if (!state.session.authenticated) {
    if (state.timer) { clearTimeout(state.timer); state.timer = null; }
    app.innerHTML = loginView();
    document.documentElement.lang = documentLang();
    return;
  }
  var page = currentPage();
  app.innerHTML = shell(page);
  document.documentElement.lang = documentLang();
  centerActiveTab();
  loadPage(page);
}

// Keep the active tab visible in the horizontally scrollable tab bar.
function centerActiveTab() {
  var nav = document.querySelector('.tabs');
  if (!nav) return;
  var active = nav.querySelector('a.active');
  if (!active) return;
  var navRect = nav.getBoundingClientRect();
  var tabRect = active.getBoundingClientRect();
  var target = nav.scrollLeft + (tabRect.left - navRect.left) - (nav.clientWidth - tabRect.width) / 2;
  var max = nav.scrollWidth - nav.clientWidth;
  nav.scrollLeft = Math.max(0, Math.min(target, max > 0 ? max : 0));
}

function schedule(page) {
  if (state.timer) { clearTimeout(state.timer); state.timer = null; }
  var interval = page === 'dashboard' ? 5000 : (page === 'network' || page === 'clients') ? 10000 : 0;
  if (!interval || document.hidden) return;
  state.timer = setTimeout(function () {
    if (document.hidden) { schedule(page); return; }
    if (currentPage() !== page) return;
    // Do not replace the page while the operator is typing in a form.
    var ae = document.activeElement;
    if (ae && ae.closest && ae.closest('#main form[data-form]')) { schedule(page); return; }
    loadPage(page, true);
  }, interval);
}

async function loadPage(page, silent) {
  // Only auto-ping when the operator actually opens the network page, not on
  // the 10s background refresh (which would ping every few seconds forever).
  var enteredNetwork = page === 'network' && state.page !== 'network';
  var seq = ++state.seq;
  state.page = page;
  try {
    var html;
    if (page === 'dashboard') {
      var d = await api('/api/status');
      state.last = d;
      html = dashboardView(d);
    } else if (page === 'network') {
      html = networkView(await api('/api/network'));
    } else if (page === 'cellular') {
      var c = await api('/api/modem');
      state.cache.modem = c;
      html = cellularView(c);
    } else if (page === 'sms') {
      var smsData = null, smsError = '', fwdError = '';
      try {
        smsData = await api('/api/sms');
        state.cache.sms = smsData.messages || [];
        if (smsData.forward) state.cache.smsForward = smsData.forward;
      } catch (e) {
        if (e.status === 401) throw e;
        smsError = e.message || t('common.error');
      }
      // Forwarding settings do not depend on the modem, so load them even when
      // the message list cannot be read (no modem, no SIM, mmcli failure...).
      try {
        state.cache.smsForward = await api('/api/sms/forward');
      } catch (e) {
        if (e.status === 401) throw e;
        if (!state.cache.smsForward) fwdError = e.message || t('common.error');
      }
      if (!smsData && fwdError) throw new Error(smsError || fwdError);
      html = smsView(smsData || {}, smsError, fwdError);
    } else if (page === 'wifi') {
      var w = await api('/api/wifi');
      state.cache.wifiConfig = w.config || {};
      html = wifiView(w);
    } else if (page === 'bridge') {
      var b = await api('/api/bridge');
      state.cache.bridgeConfig = b.config || {};
      html = bridgeView(b);
    } else if (page === 'clients') {
      var cl = await api('/api/clients');
      state.cache.clients = cl.clients || [];
      html = clientsView(cl);
    } else if (page === 'lan') {
      html = lanView(await api('/api/lan'));
    } else if (page === 'traffic') {
      html = trafficView(await api('/api/traffic'));
    } else if (page === 'system') {
      var results = await Promise.all([api('/api/system'), api('/api/system/leds'), api('/api/system/led-control')]);
      state.cache.system = results[0];
      state.cache.leds = results[1] || [];
      state.cache.ledControl = results[2] || {};
      html = systemView(results[0], results[1], results[2]);
    } else {
      html = '<div class="note bad">' + esc(t('error.noData')) + '</div>';
    }
    if (seq !== state.seq) return;
    var main = document.getElementById('main');
    if (main) {
      main.innerHTML = html;
      if (page === 'sms') initSmsForwardForms(main);
    }
    updateChrome();
    schedule(page);
    if (enteredNetwork) autoPing();
  } catch (e) {
    if (seq !== state.seq || e.status === 401) return;
    var main2 = document.getElementById('main');
    if (main2) {
      main2.innerHTML = pageHead(t('common.error'), '') +
        '<div class="note bad">' + esc(e.message || t('common.error')) + '</div>' +
        '<div class="btn-row"><button data-action="reload">' + esc(t('common.refresh')) + '</button></div>';
    }
  }
}

async function refresh(silent) {
  if (!state.session || !state.session.authenticated) return;
  await loadPage(currentPage(), silent !== false);
}

// Update the diagnostics output immediately, without rebuilding the page.
function setToolOutput(text) {
  state.cache.toolOutput = text;
  var pre = document.querySelector('#main pre.logs');
  if (pre) pre.textContent = text;
}

async function runPing(host) {
  host = (host || '').trim() || DEFAULT_PING_HOST;
  state.cache.pingHost = host;
  setToolOutput(t('common.working'));
  try {
    var res = await api('/api/system/ping', { method: 'POST', body: { host: host, count: 4 } });
    setToolOutput(res.output || t('net.noOutput'));
    await refresh(true);
  } catch (e) {
    setToolOutput(e.message || t('common.error'));
    throw e;
  }
}

// Ping the current target (Baidu by default) when the network page opens.
function autoPing() {
  var host = (state.cache.pingHost || DEFAULT_PING_HOST).trim() || DEFAULT_PING_HOST;
  runPing(host).catch(function (e) {
    if (e.status !== 401) toast(e.message || t('common.error'), 'bad');
  });
}

async function act(btn, fn) {
  if (btn) { btn.disabled = true; btn.classList.add('busy'); }
  try {
    await fn();
  } catch (e) {
    if (e.status !== 401) toast(e.message || t('common.error'), 'bad');
  } finally {
    if (btn && btn.isConnected) { btn.disabled = false; btn.classList.remove('busy'); }
  }
}

function confirmBox(message) { return window.confirm(message); }

/* ---------------------------------------------------------------- actions */

async function handleAction(el) {
  var action = el.dataset.action;
  switch (action) {
    case 'modal-close':
      closeModal();
      return;
    case 'toggle-lang': {
      var next = state.lang === 'zh' ? 'en' : 'zh';
      state.lang = next;
      try { localStorage.setItem('uz801d_lang', next); } catch (e) {}
      if (state.session && state.session.authenticated) {
        try { await api('/api/system/language', { method: 'POST', body: { language: next } }); } catch (e) { toast(e.message, 'bad'); }
        if (state.session) state.session.language = next;
      }
      render();
      return;
    }
    case 'logout':
      await act(el, async function () {
        try { await api('/api/logout', { method: 'POST', body: {} }); } catch (e) {}
        state.session = { authenticated: false, language: state.lang, password_set: true };
        state.csrf = '';
        state.last = null;
        if (location.hash) location.hash = '';
        render();
      });
      return;
    case 'reload':
      await act(el, function () { return refresh(); });
      return;
    case 'modem-connect':
      await act(el, async function () {
        toast(t('common.working'));
        await api('/api/modem/connect', { method: 'POST', body: { profile: '' } });
        toast(t('cell.dataEnabled'));
        await refresh();
      });
      return;
    case 'modem-disconnect':
      await act(el, async function () {
        await api('/api/modem/disconnect', { method: 'POST', body: {} });
        toast(t('cell.dataDisabled'));
        await refresh();
      });
      return;
    case 'flight-on':
      await act(el, async function () {
        toast(t('common.working'));
        try {
          await api('/api/modem/flight', { method: 'POST', body: { enable: true } });
          toast(t('cell.flightActive'));
        } finally {
          await refresh().catch(function () {});
        }
      });
      return;
    case 'flight-off':
      await act(el, async function () {
        toast(t('common.working'));
        try {
          await api('/api/modem/flight', { method: 'POST', body: { enable: false } });
          toast(t('cell.flightOff'));
        } finally {
          await refresh().catch(function () {});
        }
      });
      return;
    case 'modem-reset':
      await act(el, async function () {
        if (!confirmBox(t('cell.resetModem') + '?')) return;
        await api('/api/modem/reset', { method: 'POST', body: {} });
        toast(t('common.saved'));
      });
      return;
    case 'apn-new':
      state.editingAPN = 'new';
      await refresh();
      return;
    case 'apn-cancel':
      state.editingAPN = null;
      await refresh();
      return;
    case 'apn-edit':
      state.editingAPN = el.dataset.id;
      await refresh();
      return;
    case 'apn-connect':
      await act(el, async function () {
        toast(t('common.working'));
        await api('/api/modem/connect', { method: 'POST', body: { profile: el.dataset.id } });
        toast(t('cell.dataEnabled'));
        await refresh();
      });
      return;
    case 'apn-delete':
      await act(el, async function () {
        if (!confirmBox(t('common.delete') + '?')) return;
        await api('/api/modem/apn/' + encodeURIComponent(el.dataset.id), { method: 'DELETE' });
        if (state.editingAPN === el.dataset.id) state.editingAPN = null;
        await refresh();
      });
      return;
    case 'modem-scan':
      await act(el, async function () {
        toast(t('common.working'));
        state.cache.operators = await api('/api/modem/scan', { method: 'POST', body: {} });
        await refresh();
      });
      return;
    case 'operator-register':
      await act(el, async function () {
        toast(t('common.working'));
        await api('/api/modem/register', { method: 'POST', body: { code: el.dataset.code || '' } });
        toast(t('common.saved'));
        await refresh();
      });
      return;
    case 'operator-home':
      await act(el, async function () {
        toast(t('common.working'));
        await api('/api/modem/register', { method: 'POST', body: { code: '' } });
        await refresh();
      });
      return;
    case 'wifi-restart':
      await act(el, async function () {
        toast(t('common.working'));
        await api('/api/wifi/restart', { method: 'POST', body: {} });
        toast(t('common.saved'));
        await refresh();
      });
      return;
    case 'wifi-stop':
      if (el.disabled) return;
      el.disabled = true;
      el.classList.add('busy');
      try {
        await api('/api/wifi/stop', { method: 'POST', body: {} });
        toast(t('wifi.stopped'));
        // Keep the button greyed briefly, then reload every status field.
        // A short delay avoids reading a stale NetworkManager state right at
        // the moment the AP connection goes down.
        el.classList.remove('busy');
        setTimeout(function () { refresh(true).catch(function () {}); }, 1500);
      } catch (e) {
        if (e.status !== 401) toast(e.message || t('common.error'), 'bad');
        if (el.isConnected) {
          el.disabled = false;
          el.classList.remove('busy');
        }
      }
      return;
    case 'wifi-scan':
      await act(el, async function () {
        toast(t('common.working'));
        state.cache.wifiScan = await api('/api/wifi/scan');
        await refresh();
      });
      return;
    case 'wifi-use-ssid':
      setValue('ssid', el.dataset.ssid || '');
      toast(t('common.filled'));
      return;
    case 'bridge-toggle':
      await act(el, async function () {
        var active = state.last && state.last.bridge && state.last.bridge.active;
        if (active) {
          await api('/api/bridge/disable', { method: 'POST', body: {} });
          state.cache.portal = null;
          toast(t('bridge.stopping'));
          setTimeout(function () { refresh(true).catch(function () {}); }, 4000);
        } else {
          toast(t('common.working'));
          await api('/api/bridge/enable', { method: 'POST', body: {} });
          toast(t('common.saved'));
          state.cache.portal = null;
          await refresh();
        }
      });
      return;
    case 'bridge-enable':
      await act(el, async function () {
        if (document.querySelector('form[data-form="bridge"]')) await saveBridgeForm();
        toast(t('common.working'));
        await api('/api/bridge/enable', { method: 'POST', body: {} });
        toast(t('common.saved'));
        state.cache.portal = null;
        await refresh();
      });
      return;
    case 'bridge-disable':
      if (el.disabled) return;
      el.disabled = true;
      el.classList.add('busy');
      try {
        await api('/api/bridge/disable', { method: 'POST', body: {} });
        state.cache.portal = null;
        toast(t('bridge.stopping'));
        // Keep the button greyed until the page reloads after the async stop.
        el.classList.remove('busy');
        setTimeout(function () { refresh(true).catch(function () {}); }, 4000);
      } catch (e) {
        if (e.status !== 401) toast(e.message || t('common.error'), 'bad');
        if (el.isConnected) {
          el.disabled = false;
          el.classList.remove('busy');
        }
      }
      return;
    case 'bridge-portal':
      await act(el, async function () {
        toast(t('common.working'));
        var res = await api('/api/bridge/portal');
        state.cache.portal = res || {};
        if (res && res.online) toast(t('bridge.portalOnline'));
        else if (res && res.portal_url) toast(t('bridge.portalDetected'));
        else toast((res && res.error) || t('bridge.portalError'), 'bad');
        await refresh();
      });
      return;
    case 'bridge-scan':
      await act(el, async function () {
        toast(t('common.working'));
        state.cache.bridgeScan = await api('/api/bridge/scan');
        await refresh();
      });
      return;
    case 'bridge-use':
      setValue('upstream_ssid', el.dataset.ssid || '');
      setValue('upstream_security', el.dataset.security || 'wpa2');
      toast(t('common.filled'));
      return;
    case 'sms-open':
      await act(el, async function () {
        var msg = await api('/api/sms/' + encodeURIComponent(el.dataset.id));
        openModal(t('sms.detail'),
          kv(t('sms.from'), esc(msg.number || '-')) +
          kv(t('common.time'), esc(msg.timestamp || '-')) +
          '<pre class="sms-text">' + esc(msg.data ? t('sms.dataMessage') : (msg.text || '')) + '</pre>' +
          '<div class="btn-row"><button class="primary" data-action="sms-reply" data-number="' + attr(msg.number || '') + '">' + esc(t('common.reply')) + '</button></div>');
        await refresh();
      });
      return;
    case 'sms-reply':
      setValue('number', el.dataset.number || '');
      closeModal();
      var ta = document.querySelector('form[data-form="sms"] textarea');
      if (ta) ta.focus();
      return;
    case 'sms-delete':
      await act(el, async function () {
        if (!confirmBox(t('sms.deleteConfirm'))) return;
        await api('/api/sms/' + encodeURIComponent(el.dataset.id), { method: 'DELETE' });
        await refresh();
      });
      return;
    case 'sms-read-all':
      await act(el, async function () {
        await api('/api/sms/read', { method: 'POST', body: { all: true } });
        await refresh();
      });
      return;
    case 'sms-delete-all':
      await act(el, async function () {
        if (!confirmBox(t('sms.deleteAllConfirm'))) return;
        await api('/api/sms?confirm=yes', { method: 'DELETE' });
        await refresh();
      });
      return;
    case 'sms-forward-test':
      await act(el, async function () {
        var provider = null;
        var form = el.closest('form');
        if (form && form.dataset.form === 'sms-forward-provider') {
          var d = formObject(form);
          provider = {
            name: d.name, type: d.type, enabled: true,
            url: d.url, token: d.token, secret: d.secret, group: d.group, sound: d.sound
          };
        }
        if (!provider) {
          var fwd = state.cache.smsForward || {};
          provider = (fwd.providers || [])[parseInt(el.dataset.index, 10)];
        }
        if (!provider) return;
        toast(t('common.working'));
        await api('/api/sms/forward/test', { method: 'POST', body: { provider: provider } });
        toast(t('sms.forwardTestOk'));
      });
      return;
    case 'sms-forward-delete':
      await act(el, async function () {
        if (!confirmBox(t('sms.forwardDeleteConfirm'))) return;
        var fwd = state.cache.smsForward || {};
        var providers = (fwd.providers || []).slice();
        providers.splice(parseInt(el.dataset.index, 10), 1);
        await api('/api/sms/forward', { method: 'POST', body: smsForwardPayload(fwd, providers) });
        toast(t('common.saved'));
        await refresh();
      });
      return;
    case 'client-block':
      await act(el, async function () {
        if (!confirmBox(t('clients.blockConfirm'))) return;
        await api('/api/clients/block', { method: 'POST', body: { mac: el.dataset.mac } });
        await refresh();
      });
      return;
    case 'client-unblock':
      await act(el, async function () {
        await api('/api/clients/unblock', { method: 'POST', body: { mac: el.dataset.mac } });
        await refresh();
      });
      return;
    case 'client-rename':
      openModal(t('clients.renameTitle'),
        '<form data-form="rename" class="stack">' +
        '<input type="hidden" name="mac" value="' + attr(el.dataset.mac) + '">' +
        field(t('common.name'), '<input name="name" value="' + attr(el.dataset.name || '') + '" maxlength="64" autofocus>') +
        '<div class="btn-row"><button class="primary" type="submit">' + esc(t('common.save')) + '</button>' +
        '<button class="ghost" type="button" data-action="modal-close">' + esc(t('common.cancel')) + '</button></div></form>');
      return;
    case 'clients-forget':
      await act(el, async function () {
        if (!confirmBox(t('clients.forgetConfirm'))) return;
        await api('/api/clients/forget', { method: 'POST', body: {} });
        await refresh();
      });
      return;
    case 'traffic-reset':
      await act(el, async function () {
        if (!confirmBox(t('traffic.resetConfirm'))) return;
        await api('/api/traffic/reset', { method: 'POST', body: {} });
        await refresh();
      });
      return;
    case 'traffic-lifetime':
      await act(el, async function () {
        if (!confirmBox(t('traffic.lifetimeConfirm'))) return;
        await api('/api/traffic/lifetime', { method: 'POST', body: {} });
        await refresh();
      });
      return;
    case 'logs-load':
      await act(el, async function () {
        var unitEl = document.getElementById('main').querySelector('[name="log_unit"]');
        var linesEl = document.getElementById('main').querySelector('[name="log_lines"]');
        var unit = unitEl ? unitEl.value : '';
        var lines = linesEl ? parseInt(linesEl.value, 10) || 200 : 200;
        state.cache.logUnit = unit;
        state.cache.logLines = lines;
        toast(t('common.working'));
        var res = await api('/api/system/logs?unit=' + encodeURIComponent(unit) + '&lines=' + lines);
        state.cache.logs = res.logs || '';
        await refresh();
      });
      return;
    case 'frpc-log-load':
      await act(el, async function () {
        var form = document.querySelector('form[data-form="frpc-log"]');
        var lines = '';
        if (form) {
          var d = formObject(form);
          lines = parseInt(d.frpc_log_lines, 10) || '';
          state.cache.frpcLogLines = lines || 200;
          var typed = (d.frpc_log_path || '').trim();
          var saved = state.cache.system ? (state.cache.system.frpc_log_path || '') : '';
          if (typed !== saved) {
            // Persist whatever is in the field so the log shown matches it.
            await api('/api/system/frpc-log', { method: 'POST', body: { path: typed } });
            state.cache.system = state.cache.system || {};
            state.cache.system.frpc_log_path = typed;
          }
        }
        await readFrpcLog(lines);
        await refresh();
      });
      return;
    case 'led-edit': {
      var leds = state.cache.leds || [];
      var led = null;
      leds.forEach(function (l) { if (l.name === el.dataset.name) led = l; });
      if (!led) return;
      var opts = (led.triggers || []).map(function (tg) { return [tg, tg]; });
      openModal(t('system.ledEdit') + ': ' + led.name,
        '<form data-form="led" class="stack">' +
        '<input type="hidden" name="name" value="' + attr(led.name) + '">' +
        field(t('system.trigger'), sel('trigger', led.trigger, opts)) +
        field(t('system.brightness'), '<input type="range" name="brightness" min="0" max="' + attr(led.max || 255) + '" value="' + attr(led.brightness || 0) + '">') +
        '<div class="btn-row"><button class="primary" type="submit">' + esc(t('common.save')) + '</button>' +
        '<button class="ghost" type="button" data-action="modal-close">' + esc(t('common.cancel')) + '</button></div></form>');
      return;
    }
    case 'diag-ping':
    case 'diag-dns':
      await act(el, async function () {
        var input = document.getElementById('main').querySelector('[name="target"]');
        var host = input ? input.value.trim() : '';
        if (!host) { toast(t('net.target'), 'bad'); return; }
        toast(t('common.working'));
        var res = await api(action === 'diag-ping' ? '/api/system/ping' : '/api/system/dns', { method: 'POST', body: { host: host, count: 4 } });
        state.cache.diag = res.output || ((res.addresses || []).join('\n'));
        await refresh();
      });
      return;
    case 'system-reboot':
      await act(el, async function () {
        if (!confirmBox(t('system.rebootConfirm'))) return;
        await api('/api/system/reboot', { method: 'POST', body: {} });
        toast(t('system.rebootOk'), 'bad');
      });
      return;
    case 'system-poweroff':
      await act(el, async function () {
        if (!confirmBox(t('system.poweroffConfirm'))) return;
        await api('/api/system/poweroff', { method: 'POST', body: {} });
        toast(t('system.poweroffOk'), 'bad');
      });
      return;
    case 'system-factory':
      await act(el, async function () {
        if (!confirmBox(t('system.resetConfirm'))) return;
        toast(t('common.working'));
        await api('/api/system/factory-reset', { method: 'POST', body: { keep_password: true } });
        toast(t('common.saved'));
        setTimeout(function () { location.reload(); }, 1500);
      });
      return;
  }
}

async function saveWifiForm() {
  var form = document.querySelector('form[data-form="wifi"]');
  if (!form) return;
  var d = formObject(form);
  var payload = {
    backend: d.backend,
    ssid: d.ssid,
    password: d.password,
    security: d.security,
    band: 'bg',
    channel: parseInt(d.channel, 10) || 0,
    hidden: !!d.hidden,
    max_clients: parseInt(d.max_clients, 10) || 10,
    country: d.country,
    apply_on_start: !!d.apply_on_start,
    connection: (state.cache.wifiConfig && state.cache.wifiConfig.connection) || '',
    interface: (state.cache.wifiConfig && state.cache.wifiConfig.interface) || 'wlan0'
  };
  await api('/api/wifi', { method: 'POST', body: payload });
}

async function saveBridgeForm() {
  var form = document.querySelector('form[data-form="bridge"]');
  if (!form) return;
  var d = formObject(form);
  var c = state.cache.bridgeConfig || {};
  var payload = {
    mode: 'usb',
    downstream_interface: d.downstream_interface || 'usb0',
    upstream_ssid: d.upstream_ssid,
    upstream_password: d.upstream_password,
    upstream_security: d.upstream_security,
    upstream_hidden: !!c.upstream_hidden,
    upstream_connection: c.upstream_connection || 'uz801d-upstream',
    previous_ap_connection: c.previous_ap_connection || '',
    ap_interface: c.ap_interface || 'auto',
    channel: c.channel || 0,
    route_metric: parseInt(d.route_metric, 10) || 50,
    'auto': !!d.auto,
    force: !!c.force,
    enabled: !!c.enabled
  };
  await api('/api/bridge', { method: 'POST', body: payload });
  state.cache.bridgeConfig = payload;
}

async function readFrpcLog(lines) {
  var q = lines ? '?lines=' + encodeURIComponent(lines) : '';
  var res = await api('/api/system/frpc-log' + q);
  state.cache.frpcLog = res.logs || '';
  if (res.path) {
    state.cache.system = state.cache.system || {};
    state.cache.system.frpc_log_path = res.path;
  }
}

/* ---------------------------------------------------------------- forms */

async function handleSubmit(ev) {
  var form = ev.target.closest('form[data-form]');
  if (!form) return;
  ev.preventDefault();
  var kind = form.dataset.form;
  var d = formObject(form);
  var btn = form.querySelector('button[type="submit"]');
  try {
    if (kind === 'login') {
      var errBox = document.getElementById('login-error');
      if (errBox) { errBox.textContent = ''; errBox.hidden = true; }
      if (btn) { btn.disabled = true; btn.classList.add('busy'); }
      try {
        var sess = await api('/api/login', { method: 'POST', body: { username: d.username, password: d.password } });
        state.session = sess;
        state.csrf = sess.csrf || '';
        state.lang = (function () { try { return localStorage.getItem('uz801d_lang') || sess.language || 'zh'; } catch (e) { return sess.language || 'zh'; } })();
        state.last = null;
        toast(t('common.saved'));
        location.hash = '#/dashboard';
        render();
      } catch (e) {
        var msg = e.status === 401 ? t('login.bad')
          : e.status === 429 ? t('login.tooMany')
          : (e.message || t('login.bad'));
        if (errBox) { errBox.textContent = msg; errBox.hidden = false; }
        toast(msg, 'bad');
        var pw = form.querySelector('input[name="password"]');
        if (pw) { pw.select(); pw.focus(); }
      } finally {
        if (btn && btn.isConnected) { btn.disabled = false; btn.classList.remove('busy'); }
      }
      return;
    }
    if (!state.session || !state.session.authenticated) return;
    if (kind === 'apn') {
      await act(btn, async function () {
        var activate = !!d.activate;
        if (activate) toast(t('common.working'));
        var res = await api('/api/modem/apn', { method: 'POST', body: {
          profile: {
            id: d.id || '', name: d.name, apn: d.apn, user: d.user || '', password: d.password || '',
            auth: d.auth || 'none', ip_type: d.ip_type || 'ipv4'
          },
          activate: activate
        } });
        state.editingAPN = null;
        if (activate && res && res.connect_error) {
          toast(t('cell.savedDialFailed') + res.connect_error, 'bad');
        } else if (activate && res && res.connected) {
          toast(t('cell.savedConnected'));
        } else {
          toast(t('common.saved'));
        }
        await refresh();
      });
      return;
    }
    if (kind === 'modes') {
      await act(btn, async function () {
        toast(t('common.working'));
        await api('/api/modem/modes', { method: 'POST', body: { modes: d.modes } });
        toast(t('common.saved'));
        await refresh();
      });
      return;
    }
    if (kind === 'ping' || kind === 'dns') {
      await act(btn, async function () {
        var host = (d.host || '').trim();
        if (!host) { toast(t('net.target'), 'bad'); return; }
        toast(t('common.working'));
        if (kind === 'ping') {
          await runPing(host);
        } else {
          state.cache.dnsHost = host;
          var dres = await api('/api/system/dns', { method: 'POST', body: { host: host } });
          var addrs = dres.addresses || [];
          setToolOutput(addrs.length ? addrs.join('\n') : t('net.noAddresses'));
          await refresh();
        }
      });
      return;
    }
    if (kind === 'ussd' || kind === 'ussd-respond') {
      await act(btn, async function () {
        toast(t('common.working'));
        var res = await api('/api/modem/ussd', { method: 'POST', body: { action: kind === 'ussd' ? 'initiate' : 'respond', code: d.code } });
        state.cache.ussd = res.output || '';
        toast(t('common.saved'));
        await refresh();
      });
      return;
    }
    if (kind === 'pin') {
      await act(btn, async function () {
        var isPuk = !!d.puk;
        await api('/api/modem/pin', { method: 'POST', body: { kind: isPuk ? 'puk' : 'pin', pin: d.pin, puk: d.puk || '' } });
        toast(t('common.saved'));
        await refresh();
      });
      return;
    }
    if (kind === 'sms') {
      await act(btn, async function () {
        toast(t('common.working'));
        await api('/api/sms/send', { method: 'POST', body: { number: d.number, text: d.text } });
        toast(t('common.saved'));
        form.reset();
        await refresh();
      });
      return;
    }
    if (kind === 'sms-forward') {
      await act(btn, async function () {
        var fwd = state.cache.smsForward || {};
        var payload = smsForwardPayload(fwd, fwd.providers || []);
        payload.forward_enabled = !!d.enabled;
        payload.poll_seconds = parseInt(d.poll_seconds, 10) || 60;
        var res = await api('/api/sms/forward', { method: 'POST', body: payload });
        state.cache.smsForward = res.forward || payload;
        toast(t('common.saved'));
        await refresh();
      });
      return;
    }
    if (kind === 'sms-forward-provider') {
      await act(btn, async function () {
        var fwd = state.cache.smsForward || {};
        var providers = (fwd.providers || []).slice();
        var idx = parseInt(d.index, 10);
        if (!providers[idx]) return;
        providers[idx] = {
          name: d.name, type: d.type, enabled: !!d.enabled,
          url: d.url, token: d.token, secret: d.secret, group: d.group, sound: d.sound
        };
        var res = await api('/api/sms/forward', { method: 'POST', body: smsForwardPayload(fwd, providers) });
        state.cache.smsForward = res.forward || {};
        toast(t('common.saved'));
        await refresh();
      });
      return;
    }
    if (kind === 'sms-forward-add') {
      await act(btn, async function () {
        var fwd = state.cache.smsForward || {};
        var providers = (fwd.providers || []).slice();
        providers.push({
          name: d.name, type: d.type, enabled: true,
          url: d.url, token: d.token, secret: d.secret
        });
        var res = await api('/api/sms/forward', { method: 'POST', body: smsForwardPayload(fwd, providers) });
        state.cache.smsForward = res.forward || {};
        toast(t('common.saved'));
        await refresh();
      });
      return;
    }
    if (kind === 'wifi') {
      await act(btn, async function () {
        toast(t('common.working'));
        await saveWifiForm();
        toast(t('common.saved'));
        await refresh();
      });
      return;
    }
    if (kind === 'bridge') {
      await act(btn, async function () {
        await saveBridgeForm();
        toast(t('common.saved'));
        await refresh();
      });
      return;
    }
    if (kind === 'lan') {
      await act(btn, async function () {
        toast(t('common.working'));
        await api('/api/lan', { method: 'POST', body: {
          ip: d.ip, netmask: d.netmask, dhcp_start: d.dhcp_start, dhcp_end: d.dhcp_end,
          dns: (d.dns || '').split(',').map(function (x) { return x.trim(); }).filter(Boolean),
          lease_time: d.lease_time
        } });
        toast(t('common.saved'));
        await refresh();
      });
      return;
    }
    if (kind === 'traffic') {
      await act(btn, async function () {
        await api('/api/traffic', { method: 'POST', body: {
          interface: d.interface,
          quota_bytes: Math.round((parseFloat(d.quota_mb) || 0) * 1048576),
          reset_day: parseInt(d.reset_day, 10) || 1,
          enforce_quota: !!d.enforce_quota
        } });
        toast(t('common.saved'));
        await refresh();
      });
      return;
    }
    if (kind === 'frpc-log') {
      await act(btn, async function () {
        toast(t('common.working'));
        await api('/api/system/frpc-log', { method: 'POST', body: { path: d.frpc_log_path || '' } });
        state.cache.system = state.cache.system || {};
        state.cache.system.frpc_log_path = d.frpc_log_path || '';
        state.cache.frpcLogLines = parseInt(d.frpc_log_lines, 10) || 200;
        await readFrpcLog(d.frpc_log_lines);
        toast(t('common.saved'));
        await refresh();
      });
      return;
    }
    if (kind === 'password') {
      await act(btn, async function () {
        if (d.new !== d.confirm) { toast(t('system.passwordMismatch'), 'bad'); return; }
        await api('/api/system/password', { method: 'POST', body: { old: d.old, new: d.new } });
        toast(t('common.saved'));
        form.reset();
      });
      return;
    }
    if (kind === 'account') {
      await act(btn, async function () {
        await api('/api/system/admin-user', { method: 'POST', body: { user: d.user } });
        await api('/api/system/session-hours', { method: 'POST', body: { hours: parseInt(d.hours, 10) || 12 } });
        if (state.session) state.session.user = d.user;
        toast(t('common.saved'));
        await refresh();
      });
      return;
    }
    if (kind === 'rename') {
      await act(btn, async function () {
        await api('/api/clients/rename', { method: 'POST', body: { mac: d.mac, name: d.name } });
        closeModal();
        toast(t('common.saved'));
        await refresh();
      });
      return;
    }
    if (kind === 'led') {
      await act(btn, async function () {
        await api('/api/system/led', { method: 'POST', body: { name: d.name, trigger: d.trigger, brightness: parseInt(d.brightness, 10) } });
        closeModal();
        toast(t('common.saved'));
        await refresh();
      });
      return;
    }
    if (kind === 'led-control') {
      await act(btn, async function () {
        await api('/api/system/led-control', { method: 'POST', body: {
          off: !!d.off,
          schedule_enabled: !!d.schedule_enabled,
          off_time: d.off_time || '22:00',
          on_time: d.on_time || '07:00'
        } });
        toast(t('common.saved'));
        await refresh();
      });
      return;
    }
  } catch (e) {
    if (e.status !== 401) toast(e.message || t('common.error'), 'bad');
  }
}

/* ---------------------------------------------------------------- bootstrap */

function bindEvents() {
  document.addEventListener('click', function (ev) {
    var el = ev.target.closest('[data-action]');
    if (!el) return;
    if (el.tagName === 'A') return;
    ev.preventDefault();
    handleAction(el);
  });
  document.addEventListener('submit', handleSubmit);
  document.addEventListener('change', function (ev) {
    var el = ev.target;
    if (!el || el.name !== 'type') return;
    var form = el.closest('form[data-form="sms-forward-provider"], form[data-form="sms-forward-add"]');
    if (!form) return;
    syncSmsForwardForm(form, el.value);
  });
  document.addEventListener('input', function (ev) {
    var el = ev.target;
    if (!el || el.name !== 'host') return;
    var form = el.closest('form[data-form]');
    if (!form) return;
    if (form.dataset.form === 'ping') state.cache.pingHost = el.value;
    else if (form.dataset.form === 'dns') state.cache.dnsHost = el.value;
  });
  window.addEventListener('hashchange', function () {
    if (!state.session || !state.session.authenticated) return;
    // Update only the tab highlight and page body. Rebuilding the whole shell
    // here would reset the tab bar's horizontal scroll position.
    var page = currentPage();
    var nav = document.querySelector('.tabs');
    if (nav) {
      var links = nav.querySelectorAll('a[href^="#/"]');
      for (var i = 0; i < links.length; i++) {
        links[i].classList.toggle('active', links[i].getAttribute('href') === '#/' + page);
      }
    }
    loadPage(page);
  });
  document.addEventListener('visibilitychange', function () {
    if (!document.hidden && state.session && state.session.authenticated) {
      schedule(currentPage());
    }
  });
}

async function init() {
  bindEvents();
  var lang = null;
  try { lang = localStorage.getItem('uz801d_lang'); } catch (e) {}
  try {
    var sess = await api('/api/session');
    state.session = sess;
    state.csrf = sess.csrf || '';
    state.lang = lang || sess.language || 'zh';
  } catch (e) {
    state.session = { authenticated: false, language: lang || 'zh', password_set: true };
    state.lang = lang || 'zh';
  }
  document.documentElement.lang = documentLang();
  render();
}

init();
