'use strict';
// Shared helpers for the Trimple end-to-end tests: build/launch the real binary against a fresh copy
// of the sample notes, drive it with Playwright (playwright-core, no bundled browsers).
const { spawn, execFileSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const http = require('node:http');
const { chromium } = require('playwright-core');

const REPO = path.resolve(__dirname, '..');
const SAMPLE = path.join(REPO, 'testdata', 'sample-notes');
const WIN = process.platform === 'win32';
const TOKEN = 'testtoken';
const RESULTS = path.join(__dirname, 'test-results');

// ---------------------------------------------------------------- binary
let binPath = null;
function binary() {
  if (binPath) return binPath;
  if (process.env.TRIMPLE_BIN) return (binPath = process.env.TRIMPLE_BIN);
  const out = path.join(process.env.TRIMPLE_BUILD_DIR || os.tmpdir(), 'trimple-e2e-bin', WIN ? 'trimple.exe' : 'trimple');
  fs.mkdirSync(path.dirname(out), { recursive: true });
  const env = Object.assign({}, process.env);
  const goBin = WIN ? '' : '/usr/local/go/bin';
  if (goBin && fs.existsSync(goBin)) env.PATH = goBin + path.delimiter + env.PATH;
  execFileSync('go', ['build', '-o', out + '.' + process.pid, './cmd/trimple'], { cwd: REPO, env, stdio: 'inherit' });
  fs.renameSync(out + '.' + process.pid, out);
  return (binPath = out);
}

// ---------------------------------------------------------------- app process
function mkTmp(prefix = 'trimple-e2e-') {
  return fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), prefix)));
}

function copyFixture(dest) {
  fs.cpSync(SAMPLE, dest, { recursive: true }); // bytes copied exactly (CRLF/LF mixes preserved)
}

/**
 * Start a fresh Trimple. opts: { extraArgs, keepAlive (default true), setup(notesDir) , grace, files }
 * Returns { url, port, token, dir, notes, cfg, proc, exited, stop(), file(rel), read(rel), write(rel,data), api() }.
 */
async function startApp(opts = {}) {
  const dir = mkTmp();
  const notes = path.join(dir, 'notes');
  const cfg = path.join(dir, 'settings.json');
  if (opts.empty) fs.mkdirSync(notes); else copyFixture(notes);
  if (opts.setup) opts.setup(notes);
  const args = ['-no-browser'];
  if (opts.keepAlive !== false) args.push('-keep-alive');
  args.push('-port', '0', '-token', TOKEN, '-config', cfg);
  if (!opts.noFolder) args.push('-folder', notes);
  if (opts.grace) args.push('-grace', opts.grace);
  if (opts.extraArgs) args.push(...opts.extraArgs);
  const proc = spawn(binary(), args, { stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true });
  let log = '';
  proc.stderr.on('data', (d) => { log += d; });
  const exited = new Promise((resolve) => proc.once('exit', (code, sig) => resolve({ code, sig })));
  const url = await new Promise((resolve, reject) => {
    let buf = '';
    const timer = setTimeout(() => reject(new Error('Trimple did not print its address; log:\n' + log + buf)), 15000);
    proc.stdout.on('data', (d) => {
      buf += d;
      const m = /Trimple listening on (http:\/\/127\.0\.0\.1:\d+\/\?t=\S+)/.exec(buf);
      if (m) { clearTimeout(timer); resolve(m[1]); }
    });
    proc.once('exit', (c) => { clearTimeout(timer); reject(new Error('Trimple exited early (' + c + '): ' + log)); });
  });
  const port = Number(new URL(url).port);
  const app = {
    url, port, token: TOKEN, dir, notes, cfg, proc, exited,
    log: () => log,
    file: (rel) => path.join(notes, ...rel.split('/')),
    read: (rel) => fs.readFileSync(app.file(rel)),
    write: (rel, data) => { const f = app.file(rel); fs.mkdirSync(path.dirname(f), { recursive: true }); fs.writeFileSync(f, data); },
    settings: () => JSON.parse(fs.readFileSync(cfg, 'utf8')),
    async stop() {
      if (proc.exitCode === null && proc.signalCode === null) {
        proc.kill();
        await Promise.race([exited, new Promise((r) => setTimeout(r, 3000))]);
        if (proc.exitCode === null && proc.signalCode === null) proc.kill('SIGKILL');
      }
      try { fs.rmSync(dir, { recursive: true, force: true, maxRetries: 3 }); } catch (_) { /* best effort */ }
    },
    request: (o) => rawRequest(port, o),
  };
  return app;
}

/** Raw HTTP request to the app (so Host/Origin headers can be forged). */
function rawRequest(port, { method = 'GET', path: p = '/', headers = {}, body, token = TOKEN, host } = {}) {
  return new Promise((resolve, reject) => {
    const h = Object.assign({}, headers);
    if (token) h['X-Trimple-Token'] = token;
    if (host) h.Host = host;
    let payload = body;
    if (body !== undefined && typeof body !== 'string' && !Buffer.isBuffer(body)) {
      payload = JSON.stringify(body);
      h['Content-Type'] = 'application/json';
    }
    if (payload !== undefined) h['Content-Length'] = Buffer.byteLength(payload);
    const req = http.request({ host: '127.0.0.1', port, method, path: p, headers: h }, (res) => {
      const chunks = [];
      res.on('data', (c) => chunks.push(c));
      res.on('end', () => {
        const text = Buffer.concat(chunks).toString('utf8');
        let json = null;
        try { json = JSON.parse(text); } catch (_) { /* not json */ }
        resolve({ status: res.statusCode, headers: res.headers, text, json });
      });
    });
    req.on('error', reject);
    if (payload !== undefined) req.write(payload);
    req.end();
  });
}

// ---------------------------------------------------------------- browser
function findChromium() {
  if (process.env.TRIMPLE_BROWSER_PATH) return { executablePath: process.env.TRIMPLE_BROWSER_PATH };
  const roots = [process.env.PLAYWRIGHT_BROWSERS_PATH, '/opt/pw-browsers'].filter(Boolean);
  for (const root of roots) {
    try {
      for (const d of fs.readdirSync(root).filter((n) => /^chromium-\d+$/.test(n)).sort().reverse()) {
        for (const rel of ['chrome-linux64/chrome', 'chrome-linux/chrome', 'chrome-win64/chrome.exe', 'chrome-mac/Chromium.app/Contents/MacOS/Chromium']) {
          const p = path.join(root, d, rel);
          if (fs.existsSync(p)) return { executablePath: p };
        }
      }
    } catch (_) { /* no such dir */ }
  }
  return { channel: WIN ? 'msedge' : 'chrome' };
}

async function launchBrowser() {
  return chromium.launch(Object.assign({ headless: true, args: ['--no-sandbox'] }, findChromium()));
}

/** Open the app in a fresh context; collects console errors and page exceptions in page.errors. */
async function openPage(browser, app, { viewport = { width: 1280, height: 800 }, colorScheme, wait = true } = {}) {
  const context = await browser.newContext({ viewport, colorScheme, acceptDownloads: false });
  context.setDefaultTimeout(10000);
  const page = await context.newPage();
  page.errors = [];       // uncaught exceptions + console.error that are not plain HTTP-status noise
  page.netNoise = [];     // 'Failed to load resource: 4xx' lines (expected when tests provoke API errors)
  page.dialogs = [];      // native dialogs (the app should not use any besides beforeunload)
  page.on('pageerror', (e) => page.errors.push('pageerror: ' + (e.stack || e.message || e)));
  page.on('console', (m) => {
    if (m.type() !== 'error') return;
    if (/Failed to load resource/.test(m.text())) page.netNoise.push(m.text());
    else page.errors.push('console.error: ' + m.text());
  });
  page.on('dialog', async (d) => { page.dialogs.push(d.type() + ': ' + d.message()); await d.dismiss().catch(() => {}); });
  await page.goto(app.url);
  if (wait) await ready(page);
  return page;
}

async function ready(page) {
  await page.waitForFunction(() => window.__trimpleReady === true, null, { timeout: 15000 });
}

// ---------------------------------------------------------------- page helpers
const tasksText = (page) => page.$$eval('#tasklist li.task', (l) => l.map((li) => li.querySelector('.ttext').textContent));
const groupNames = (page) => page.$$eval('#tasklist li.group', (l) => l.map((li) => li.children[0].textContent + '|' + li.children[1].textContent));
const fileNames = (page) => page.$$eval('#filelist .fname', (l) => l.map((e) => e.textContent));
const tabNames = (page) => page.$$eval('#tabs .tab .tname', (l) => l.map((e) => e.textContent));
const status = (page) => page.$eval('#msg', (e) => e.textContent);
const editorState = (page) => page.evaluate(() => {
  const ta = [...document.querySelectorAll('textarea.editor')].find((t) => !t.hidden);
  return ta && { value: ta.value, start: ta.selectionStart, end: ta.selectionEnd, scrollTop: ta.scrollTop, clientHeight: ta.clientHeight, readOnly: ta.readOnly, wrap: ta.wrap };
});
const activeTa = (page) => page.locator('textarea.editor:not([hidden])');
const taskRow = (page, text) => page.locator('#tasklist li.task', { has: page.locator('.ttext', { hasText: text }) });
const modal = (page) => page.locator('.modal-overlay .modal');

async function waitFor(fn, { timeout = 8000, interval = 100, what = 'condition' } = {}) {
  const end = Date.now() + timeout;
  let last;
  for (;;) {
    try { last = await fn(); if (last) return last; } catch (e) { last = e; }
    if (Date.now() > end) throw new Error('timed out waiting for ' + what + (last instanceof Error ? ': ' + last.message : ''));
    await new Promise((r) => setTimeout(r, interval));
  }
}

/** Offsets of bytes that differ between two equal-length buffers (or ['length'] when sizes differ). */
function diffBytes(a, b) {
  if (a.length !== b.length) return ['length ' + a.length + ' vs ' + b.length];
  const d = [];
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) d.push(i);
  return d;
}

/** Count bare LF (not preceded by CR) and bare CR (not followed by LF). */
function eolCounts(buf) {
  let bareLF = 0, bareCR = 0, crlf = 0;
  for (let i = 0; i < buf.length; i++) {
    if (buf[i] === 0x0a) { if (i > 0 && buf[i - 1] === 0x0d) crlf++; else bareLF++; }
    else if (buf[i] === 0x0d && buf[i + 1] !== 0x0a) bareCR++;
  }
  return { crlf, bareLF, bareCR };
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

module.exports = {
  REPO, SAMPLE, TOKEN, RESULTS, WIN, binary, mkTmp, copyFixture, startApp, rawRequest, launchBrowser, openPage, ready,
  tasksText, groupNames, fileNames, tabNames, status, editorState, activeTa, taskRow, modal, waitFor, diffBytes, eolCounts, sleep,
};
