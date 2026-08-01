let csrf = null;

async function api(method, path, body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
    if (csrf) opts.headers['X-CSRF-Token'] = csrf;
  }
  const res = await fetch(path, opts);
  if (!res.ok) throw new Error((await res.text()) || res.status);
  const ct = res.headers.get('content-type') || '';
  return ct.includes('json') ? res.json() : res.text();
}

function el(tag, text) { const e = document.createElement(tag); if (text != null) e.textContent = text; return e; }

async function boot() {
  try {
    await api('GET', '/api/servers');   // 200 only if a session exists
    showList();
  } catch {
    showGate();
  }
}

function showGate() {
  const g = document.getElementById('gate');
  g.replaceChildren();
  const wrap = el('div');
  const info = el('p', 'Enter master password to unlock. First run? Use the setup token printed in the terminal.');
  const pw = el('input'); pw.type = 'password'; pw.placeholder = 'master password'; pw.autocomplete = 'current-password';
  const boot = el('input'); boot.placeholder = 'setup token (first run only)';
  const btn = el('button', 'Unlock / Set up');
  btn.onclick = async () => {
    try {
      if (boot.value) {
        const r = await api('POST', '/api/first-run', { bootstrapToken: boot.value, masterPassword: pw.value });
        csrf = r.csrf;
      } else {
        const r = await api('POST', '/api/unlock', { masterPassword: pw.value });
        csrf = r.csrf;
      }
      showList();
    } catch (e) { alert('' + e.message); }
  };
  wrap.append(info, pw, boot, btn);
  g.replaceChildren(wrap);
  document.getElementById('list').hidden = true;
  g.hidden = false;
}

async function showList() {
  document.getElementById('gate').hidden = true;
  document.getElementById('form').hidden = true;
  document.getElementById('importui').hidden = true;
  const list = document.getElementById('list');
  list.hidden = false;
  const rows = document.getElementById('rows');
  rows.replaceChildren();
  const servers = await api('GET', '/api/servers');
  (servers || []).forEach(s => {
    const tr = el('tr');
    tr.append(el('td', s.name), el('td', s.host + ':' + s.port), el('td', s.user), el('td', s.auth));
    const secrets = [s.hasPassword && 'pw', s.hasSuPassword && 'su', s.hasSudoPassword && 'sudo'].filter(Boolean).join(',') || '—';
    tr.append(el('td', secrets));
    const td = el('td');
    const edit = el('button', 'Edit'); edit.onclick = () => showForm(s);
    const del = el('button', 'Delete'); del.onclick = async () => { await api('DELETE', '/api/servers/' + encodeURIComponent(s.name)); showList(); };
    td.append(edit, del); tr.append(td);
    rows.append(tr);
  });
  document.getElementById('add').onclick = () => showForm(null);
  document.getElementById('export').onclick = doExport;
  document.getElementById('import').onclick = showImport;
}

function field(label, value, type) {
  const l = el('label', label); const i = el('input'); i.value = value || ''; if (type) i.type = type;
  if (type === 'password') i.autocomplete = 'off';
  l.append(i); return { l, i };
}

function showForm(s) {
  const f = document.getElementById('form');
  f.replaceChildren();
  const name = field('Name', s && s.name, 'text');
  if (s) name.i.disabled = true;
  const host = field('Host', s && s.host, 'text');
  const port = field('Port', s ? s.port : 22, 'number');
  const user = field('User', s && s.user, 'text');
  const auth = field('Auth (password/key/agent)', (s && s.auth) || 'password', 'text');
  const keyPath = field('Key path', s && s.keyPath, 'text');
  const pw = field('Password (blank = keep)', '', 'password');
  const su = field('su password (blank = keep)', '', 'password');
  const sudo = field('sudo password (blank = keep)', '', 'password');
  const save = el('button', 'Save');
  save.onclick = async () => {
    const dto = { name: name.i.value, host: host.i.value, port: +port.i.value, user: user.i.value, auth: auth.i.value, keyPath: keyPath.i.value };
    if (pw.i.value) dto.password = pw.i.value;
    if (su.i.value) dto.suPassword = su.i.value;
    if (sudo.i.value) dto.sudoPassword = sudo.i.value;
    try {
      if (s) await api('PUT', '/api/servers/' + encodeURIComponent(s.name), dto);
      else await api('POST', '/api/servers', dto);
      showList();
    } catch (e) { alert('' + e.message); }
  };
  const cancel = el('button', 'Cancel'); cancel.onclick = showList;
  [name, host, port, user, auth, keyPath, pw, su, sudo].forEach(x => f.append(x.l));
  f.append(save, cancel);
  document.getElementById('list').hidden = true;
  f.hidden = false;
}

function showImport() {
  const u = document.getElementById('importui');
  u.replaceChildren();
  const ta = el('textarea'); ta.placeholder = 'paste ~/.ssh/config or exported JSON';
  const src = el('select'); ['ssh_config', 'json'].forEach(o => { const opt = el('option', o); opt.value = o; src.append(opt); });
  const prev = el('button', 'Preview');
  const out = el('div');
  prev.onclick = async () => {
    const r = await api('POST', '/api/import/preview', { source: src.value, payload: ta.value });
    out.replaceChildren();
    (r.notes || []).forEach(n => out.append(el('p', n)));
    const apply = el('button', 'Import ' + (r.entries || []).length + ' (skips ' + (r.conflicts || []).length + ' conflicts)');
    apply.onclick = async () => { await api('POST', '/api/import/apply', { entries: r.entries }); showList(); };
    (r.entries || []).forEach(e => out.append(el('div', e.name + ' → ' + e.host + ':' + e.port + ' ' + e.user)));
    out.append(apply);
  };
  const cancel = el('button', 'Cancel'); cancel.onclick = showList;
  u.append(src, ta, prev, out, cancel);
  document.getElementById('list').hidden = true;
  u.hidden = false;
}

async function doExport() {
  const withSecrets = confirm('Include secrets? OK = yes (needs export passphrase), Cancel = names only');
  const body = { secrets: withSecrets };
  if (withSecrets) { body.exportPassphrase = prompt('Export passphrase:') || ''; if (!body.exportPassphrase) return; }
  const res = await fetch('/api/export', { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf }, body: JSON.stringify(body) });
  if (!res.ok) { alert(await res.text()); return; }
  const blob = await res.blob();
  const a = document.createElement('a'); a.href = URL.createObjectURL(blob); a.download = 'ssh-mcp-export.json'; a.click();
}

boot();
