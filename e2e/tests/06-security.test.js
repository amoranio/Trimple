'use strict';
// (13) HTTP-level security checks driven from node, (14) process lifetime.
const { test, before, after } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const L = require('../lib');

let app;
before(async () => { app = await L.startApp(); });
after(async () => { await app.stop(); });
const q = (p) => encodeURIComponent(p);

test('13a. no token / wrong token -> 401 on every API endpoint; the static UI needs no token', async () => {
  for (const [method, p] of [['GET', '/api/state'], ['GET', '/api/bootstrap'], ['GET', '/api/file?path=Home.txt'], ['GET', '/api/stat?path=Home.txt'],
    ['GET', '/api/fs'], ['GET', '/api/settings'], ['PUT', '/api/settings'], ['POST', '/api/task'], ['POST', '/api/folder'], ['POST', '/api/quit'],
    ['PUT', '/api/file'], ['GET', '/api/ping'], ['GET', '/api/resolve?abs=/x'], ['POST', '/api/notepad']]) {
    const none = await app.request({ method, path: p, token: null, body: method === 'GET' ? undefined : {} });
    assert.equal(none.status, 401, `${method} ${p} without token -> ${none.status}`);
    const bad = await app.request({ method, path: p, token: 'wrong', body: method === 'GET' ? undefined : {} });
    assert.equal(bad.status, 401, `${method} ${p} wrong token -> ${bad.status}`);
    const sameLen = await app.request({ method, path: p, token: 'testtokex', body: method === 'GET' ? undefined : {} });
    assert.equal(sameLen.status, 401);
  }
  // token in the query string is only honoured for the event stream, not for ordinary calls
  const viaQuery = await app.request({ path: '/api/state?t=testtoken', token: null });
  assert.equal(viaQuery.status, 401);
  const idx = await app.request({ path: '/', token: null });
  assert.equal(idx.status, 200);
  assert.match(idx.text, /<title>Trimple<\/title>/);
  const ok = await app.request({ path: '/api/ping' });
  assert.equal(ok.status, 200);
  // security headers on everything
  assert.match(idx.headers['content-security-policy'], /default-src 'self'/);
  assert.match(idx.headers['content-security-policy'], /frame-ancestors 'none'/);
  assert.equal(idx.headers['x-content-type-options'], 'nosniff');
  assert.equal(idx.headers['x-frame-options'], 'DENY');
  assert.equal(idx.headers['cache-control'], 'no-store');
});

test('13b. wrong Host header -> 403 (DNS rebinding), including for the static UI and a missing port', async () => {
  for (const host of ['evil.example.com', 'evil.example.com:' + app.port, '127.0.0.1', 'localhost', '127.0.0.1:1', `127.0.0.1.evil.com:${app.port}`, `[::1]:${app.port}`, '0.0.0.0:' + app.port, '10.0.0.5:' + app.port]) {
    for (const p of ['/api/state', '/', '/app.js']) {
      const r = await app.request({ path: p, host });
      assert.equal(r.status, 403, `Host ${host} ${p} -> ${r.status}`);
    }
  }
  for (const host of [`127.0.0.1:${app.port}`, `localhost:${app.port}`, `LOCALHOST:${app.port}`]) {
    const r = await app.request({ path: '/api/state', host });
    assert.equal(r.status, 200, `Host ${host} should be accepted`);
  }
});

test('13c. POST/PUT with a foreign or null Origin -> 403; same-origin and no Origin are accepted', async () => {
  const body = { theme: 'dark', fontSize: 14, wordWrap: true, panelVisible: true, panelWidth: 360, groupBy: 'priority', showDone: false, openFiles: [], folder: '' };
  for (const origin of ['http://evil.example.com', 'http://127.0.0.1:1', 'null', 'http://localhost.evil.com:' + app.port, 'file://']) {
    for (const [method, p, b] of [['PUT', '/api/settings', body], ['POST', '/api/task', { path: 'Home.txt', line: 3, done: true }], ['POST', '/api/quit', {}], ['POST', '/api/folder', { path: app.dir }], ['PUT', '/api/file', { path: 'x.txt', text: 'x', create: true }]]) {
      const r = await app.request({ method, path: p, body: b, headers: { Origin: origin } });
      assert.equal(r.status, 403, `${method} ${p} Origin ${origin} -> ${r.status} ${r.text}`);
    }
  }
  assert.equal(fs.existsSync(app.file('x.txt')), false);
  const same = await app.request({ method: 'PUT', path: '/api/settings', body, headers: { Origin: `http://127.0.0.1:${app.port}` } });
  assert.equal(same.status, 200);
  const none = await app.request({ method: 'PUT', path: '/api/settings', body });
  assert.equal(none.status, 200);
  // process still alive after all the refused /api/quit calls
  assert.equal((await app.request({ path: '/api/ping' })).status, 200);
});

test('13d. path traversal, absolute paths, backslashes, reserved names, colons -> 400 on read, write, stat and task', async () => {
  const secret = path.join(app.dir, 'secret.txt');
  fs.writeFileSync(secret, 'top secret\n');
  const bad = ['../secret.txt', '..\\secret.txt', '../x', '..', 'work/../../secret.txt', 'work\\..\\..\\secret.txt',
    secret, '/etc/passwd', '\\\\server\\share\\x.txt', 'C:\\Windows\\win.ini', 'C:win.ini', 'CON.txt', 'con', 'nul.txt', 'work/PRN.txt', 'AUX', 'COM1.txt', 'lpt9.txt',
    'a:b.txt', 'file.txt:stream', 'Home.txt:Zone.Identifier', 'a<b.txt', 'a|b.txt', 'a?b.txt', 'a*b.txt', 'a"b.txt', 'trailing.txt.', 'space.txt ', 'ctl\u0001.txt', '', '.', './Home.txt/..', 'x'.repeat(600) + '.txt'];
  for (const p of bad) {
    const g = await app.request({ path: '/api/file?path=' + q(p) });
    assert.equal(g.status, 400, `GET /api/file?path=${JSON.stringify(p)} -> ${g.status} ${g.text.slice(0, 80)}`);
    const put = await app.request({ method: 'PUT', path: '/api/file', body: { path: p, text: 'pwned', kind: 'utf8', eol: 'crlf', create: true, overwrite: true } });
    assert.equal(put.status, 400, `PUT ${JSON.stringify(p)} -> ${put.status} ${put.text.slice(0, 80)}`);
    const t = await app.request({ method: 'POST', path: '/api/task', body: { path: p, line: 1, done: true } });
    assert.equal(t.status, 400, `POST task ${JSON.stringify(p)} -> ${t.status}`);
    const s = await app.request({ path: '/api/stat?path=' + q(p) });
    assert.equal(s.status, 200);
    assert.equal(Object.values(s.json)[0].exists, false, 'stat of a bad path never reports existence: ' + JSON.stringify(p));
  }
  assert.equal(fs.readFileSync(secret, 'utf8'), 'top secret\n');
  assert.equal(fs.existsSync(path.join(app.dir, 'x')), false);
  // raw URL tricks that bypass client-side encoding
  for (const raw of ['/api/file?path=../secret.txt', '/api/file?path=%2e%2e%2fsecret.txt', '/api/file?path=..%5Csecret.txt', '/../api/state', '/api/file/../../secret.txt']) {
    const r = await app.request({ path: raw });
    assert.ok([400, 404, 200, 301, 307, 308].includes(r.status), raw + ' -> ' + r.status); // 3xx = Go cleaning the path; the target still needs the token
    assert.ok(!r.text.includes('top secret'), raw + ' leaked the secret');
  }
  // the static handler does not serve anything outside the embedded web files
  for (const raw of ['/..%2f..%2fetc/passwd', '/%2e%2e/%2e%2e/etc/passwd', '/../../../../etc/passwd', '/main.go', '/server.go']) {
    const r = await app.request({ path: raw, token: null });
    assert.ok(!/root:|package /.test(r.text), raw + ' leaked file contents');
  }
});

test('13e. PUT of a non-.txt path -> 400; other write abuses are refused', async () => {
  for (const p of ['evil.exe', 'evil.bat', 'evil.txt.exe', 'evil', 'work/evil.html', '.txt/x', 'Home.TXT.bak', 'a.txtx']) {
    const r = await app.request({ method: 'PUT', path: '/api/file', body: { path: p, text: 'x', kind: 'utf8', eol: 'crlf', create: true } });
    assert.equal(r.status, 400, `${p} -> ${r.status} ${r.text}`);
  }
  assert.deepEqual(fs.readdirSync(app.notes).sort(), ['Home.txt', 'Work.txt', 'work']);
  const nul = await app.request({ method: 'PUT', path: '/api/file', body: { path: 'nul-in-text.txt', text: 'a\u0000b', create: true } });
  assert.equal(nul.status, 400);
  const unknown = await app.request({ method: 'PUT', path: '/api/file', body: { path: 'u.txt', text: 'x', create: true, extra: 1 } });
  assert.equal(unknown.status, 400);
  const trailing = await app.request({ method: 'PUT', path: '/api/file', body: '{"path":"t.txt","text":"x","create":true}{"a":1}' , headers: { 'Content-Type': 'application/json' } });
  assert.equal(trailing.status, 400);
  const garbage = await app.request({ method: 'PUT', path: '/api/file', body: 'not json' });
  assert.equal(garbage.status, 400);
  const badMethod = await app.request({ method: 'DELETE', path: '/api/file?path=Home.txt' });
  assert.equal(badMethod.status, 405);
  assert.equal(fs.existsSync(app.file('Home.txt')), true);
  // creating a .txt in a new sub-folder is fine; case-insensitive extension accepted
  const ok = await app.request({ method: 'PUT', path: '/api/file', body: { path: 'newdir/ok.TXT', text: 'fine', kind: 'utf8', eol: 'lf', create: true } });
  assert.equal(ok.status, 200, ok.text);
  assert.equal(app.read('newdir/ok.TXT').toString(), 'fine');
  // "create" on an existing name without overwrite is a 409, expectedHash mismatch is a 409
  const exists = await app.request({ method: 'PUT', path: '/api/file', body: { path: 'Home.txt', text: 'x', create: true } });
  assert.equal(exists.status, 409); assert.equal(exists.json.code, 'exists');
  const changed = await app.request({ method: 'PUT', path: '/api/file', body: { path: 'Home.txt', text: 'x', expectedHash: '0000000000000000' } });
  assert.equal(changed.status, 409); assert.equal(changed.json.code, 'changed');
  // an oversized body is refused
  const big = await app.request({ method: 'PUT', path: '/api/file', body: JSON.stringify({ path: 'big.txt', text: 'x'.repeat(25 << 20), create: true }) , headers: { 'Content-Type': 'application/json' } });
  assert.equal(big.status, 400);
  assert.equal(fs.existsSync(app.file('big.txt')), false);
});

test('13f. a symlink inside the notes folder cannot be used to escape it', { skip: L.WIN && 'symlinks need privileges on Windows' }, async () => {
  const outside = path.join(app.dir, 'outside');
  fs.mkdirSync(outside, { recursive: true });
  fs.writeFileSync(path.join(outside, 'leak.txt'), 'leaked\n');
  fs.symlinkSync(outside, app.file('linkdir'));
  fs.symlinkSync(path.join(outside, 'leak.txt'), app.file('linkfile.txt'));
  for (const p of ['linkdir/leak.txt', 'linkfile.txt', 'linkdir/new.txt']) {
    const g = await app.request({ path: '/api/file?path=' + q(p) });
    assert.equal(g.status, 400, `GET ${p} -> ${g.status} ${g.text.slice(0, 100)}`);
    const w = await app.request({ method: 'PUT', path: '/api/file', body: { path: p, text: 'pwn', create: true, overwrite: true } });
    assert.equal(w.status, 400, `PUT ${p} -> ${w.status}`);
    const t = await app.request({ method: 'POST', path: '/api/task', body: { path: p, line: 1, done: true } });
    assert.equal(t.status, 400, `task ${p} -> ${t.status}`);
  }
  assert.equal(fs.readFileSync(path.join(outside, 'leak.txt'), 'utf8'), 'leaked\n');
  assert.equal(fs.existsSync(path.join(outside, 'new.txt')), false);
  // and the index does not list or read tasks through the links
  const st = await app.request({ path: '/api/state' });
  const names = st.json.files.map((f) => f.path);
  assert.ok(!names.some((n) => n.startsWith('linkdir/')), 'symlinked dir listed: ' + names);
  fs.rmSync(app.file('linkdir')); fs.rmSync(app.file('linkfile.txt'));
});

test('13g. /api/fs and /api/folder validate their input', async () => {
  const r1 = await app.request({ path: '/api/fs?path=' + q('relative/dir') });
  assert.equal(r1.status, 200); assert.equal(r1.json.path, '');
  const r2 = await app.request({ path: '/api/fs?path=' + q(path.join(app.dir, 'missing')) });
  assert.equal(r2.status, 400);
  const r3 = await app.request({ path: '/api/fs?path=' + q(app.notes) });
  assert.ok(r3.json.dirs.includes('work'));
  const f1 = await app.request({ method: 'POST', path: '/api/folder', body: { path: 'relative' } });
  assert.equal(f1.status, 400);
  const f2 = await app.request({ method: 'POST', path: '/api/folder', body: { path: path.join(app.dir, 'nope') } });
  assert.equal(f2.status, 400);
  assert.equal(fs.existsSync(path.join(app.dir, 'nope')), false);
  const f3 = await app.request({ method: 'POST', path: '/api/folder', body: { path: app.notes + '\u0000x' } });
  assert.equal(f3.status, 400);
  const s = await app.request({ path: '/api/settings' });
  assert.equal(s.json.folder, app.notes, 'folder unchanged by refused calls');
  // settings sanitising
  const put = await app.request({ method: 'PUT', path: '/api/settings', body: { folder: '/etc', theme: 'neon', fontSize: 999, wordWrap: true, panelVisible: true, panelWidth: 5, groupBy: 'x', showDone: false, openFiles: ['../../etc/passwd', '', 'ok.txt'] } });
  assert.equal(put.status, 200);
  assert.equal(put.json.theme, 'dark'); assert.equal(put.json.fontSize, 14); assert.equal(put.json.panelWidth, 360); assert.equal(put.json.groupBy, 'priority');
  assert.equal((await app.request({ path: '/api/settings' })).json.folder, app.notes, 'settings PUT cannot change the folder');
});

test('13h. /api/stat and /api/state survive hostile / huge parameters', async () => {
  const many = Array.from({ length: 500 }, (_, i) => 'path=' + q('f' + i + '.txt')).join('&');
  const r = await app.request({ path: '/api/stat?' + many });
  assert.equal(r.status, 200);
  assert.ok(Object.keys(r.json).length <= 60, 'stat is capped');
  const s = await app.request({ path: '/api/state?group=' + q('<script>') + '&label=' + q('\u0000') + '&q=' + 'a'.repeat(100000) });
  assert.equal(s.status, 200);
  assert.deepEqual(s.json.groups, []);
});

// ------------------------------------------------------------------ (14) lifetime
test('14a. without -keep-alive the process exits by itself a few seconds after the page is closed (-grace 2s)', async () => {
  const a = await L.startApp({ keepAlive: false, grace: '2s' });
  const browser = await L.launchBrowser();
  try {
    const page = await L.openPage(browser, a);
    // stays alive while the page is open, longer than the grace period
    const early = await Promise.race([a.exited.then(() => 'exited'), L.sleep(4500).then(() => 'alive')]);
    assert.equal(early, 'alive', 'process must stay alive while a window is open');
    // a reload (window briefly disconnects) must not kill it
    await page.reload();
    await L.ready(page);
    const t0 = Date.now();
    await page.context().close();
    const res = await Promise.race([a.exited, L.sleep(12000).then(() => 'timeout')]);
    const dt = Date.now() - t0;
    console.log(`# process exited ${dt} ms after the window closed (grace 2s)`);
    assert.notEqual(res, 'timeout', 'process did not exit after the window was closed');
    assert.equal(res.code, 0, 'clean exit code');
    assert.ok(dt >= 1500, 'respects the grace period: ' + dt);
    assert.ok(dt < 6000, 'exits promptly: ' + dt);
  } finally { await browser.close(); await a.stop(); }
});

test('14b. with -keep-alive the process survives the page closing', async () => {
  const a = await L.startApp({ grace: '1s' });
  const browser = await L.launchBrowser();
  try {
    const page = await L.openPage(browser, a);
    await page.context().close();
    const res = await Promise.race([a.exited.then(() => 'exited'), L.sleep(4000).then(() => 'alive')]);
    assert.equal(res, 'alive');
    assert.equal((await a.request({ path: '/api/ping' })).status, 200);
  } finally { await browser.close(); await a.stop(); }
});

test('14c. POST /api/quit (the only way for the UI to stop the app) ends the process cleanly', async () => {
  const a = await L.startApp();
  try {
    const r = await a.request({ method: 'POST', path: '/api/quit', body: {} });
    assert.equal(r.status, 200);
    const res = await Promise.race([a.exited, L.sleep(5000).then(() => 'timeout')]);
    assert.notEqual(res, 'timeout');
    assert.equal(res.code, 0);
  } finally { await a.stop(); }
});

test('14d. a second window on the same server counts: closing one keeps the process alive', async () => {
  const a = await L.startApp({ keepAlive: false, grace: '2s' });
  const browser = await L.launchBrowser();
  try {
    const p1 = await L.openPage(browser, a);
    const p2 = await L.openPage(browser, a);
    await p1.context().close();
    const early = await Promise.race([a.exited.then(() => 'exited'), L.sleep(4500).then(() => 'alive')]);
    assert.equal(early, 'alive');
    await p2.context().close();
    const res = await Promise.race([a.exited, L.sleep(10000).then(() => 'timeout')]);
    assert.notEqual(res, 'timeout');
  } finally { await browser.close(); await a.stop(); }
});
