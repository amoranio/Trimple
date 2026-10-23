'use strict';
// Adversarial / race / robustness probes beyond the numbered scenarios.
const { test, before, after } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const L = require('../lib');

let browser;
before(async () => { browser = await L.launchBrowser(); });
after(async () => { await browser.close(); });

async function withApp(opts, fn) {
  const app = await L.startApp(opts);
  try {
    const page = await L.openPage(browser, app);
    await fn(app, page);
    assert.deepEqual(page.errors, [], 'console errors / page exceptions');
  } finally { await app.stop(); }
}
async function openFile(page, name) {
  await page.click(`#filelist button:has(.fname:text-is("${name}"))`);
  await page.waitForFunction((n) => document.querySelector('#tabs .tab.active .tname')?.textContent === n.split('/').pop(), name);
}
const dots = (page) => page.locator('#tabs .dot').count();

test('A1. double Ctrl+S does not raise a bogus "changed outside Trimple" prompt', async () => {
  await withApp({}, async (app, page) => {
    let bogus = 0;
    for (let i = 0; i < 8; i++) {
      await openFile(page, 'Home.txt');
      await L.activeTa(page).click();
      await page.keyboard.press('Control+End');
      await page.keyboard.type('x');
      await page.keyboard.press('Control+s');
      await page.keyboard.press('Control+s');
      await L.sleep(250);
      if (await L.modal(page).count()) {
        const t = await L.modal(page).locator('h3').textContent();
        if (/changed outside/i.test(t)) bogus++;
        await page.keyboard.press('Escape');
        await L.sleep(100);
      }
      await L.waitFor(async () => (await dots(page)) === 0 || (await L.modal(page).count()) === 0, { timeout: 2000 }).catch(() => {});
    }
    assert.equal(bogus, 0, `${bogus}/8 double-saves produced a false conflict dialog`);
  });
});

test('A2. text typed while a save is in flight is not silently marked as saved', async () => {
  await withApp({}, async (app, page) => {
    await page.route('**/api/file', async (route) => {
      if (route.request().method() === 'PUT') await new Promise((r) => setTimeout(r, 800));
      await route.continue();
    });
    await openFile(page, 'Home.txt');
    await L.activeTa(page).click();
    await page.keyboard.press('Control+End');
    await page.keyboard.type('AAA');
    await page.keyboard.press('Control+s');
    await page.keyboard.type('BBB'); // typed while the PUT is pending
    await L.sleep(1500);
    const onDisk = app.read('Home.txt').toString();
    assert.ok(onDisk.endsWith('AAA'), 'first text saved');
    const value = (await L.editorState(page)).value;
    assert.ok(value.endsWith('AAABBB'));
    assert.equal(await dots(page), 1, 'tab must still be dirty because BBB is not on disk (disk=' + JSON.stringify(onDisk.slice(-8)) + ')');
  });
});

test('A3. server killed while the page is open: banner appears, save explains, edits are kept', async () => {
  const app = await L.startApp();
  const page = await L.openPage(browser, app);
  try {
    await openFile(page, 'Home.txt');
    await L.activeTa(page).click();
    await page.keyboard.press('Control+End');
    await page.keyboard.type('precious');
    app.proc.kill();
    await app.exited;
    await page.waitForSelector('#banner:not([hidden])', { timeout: 8000 });
    assert.match(await page.textContent('#banner'), /Lost connection to Trimple/);
    await page.keyboard.press('Control+s');
    await page.waitForFunction(() => /Cannot reach Trimple|Could not save/.test(document.querySelector('#msg').textContent));
    assert.equal(await dots(page), 1, 'still dirty');
    assert.ok((await L.editorState(page)).value.endsWith('precious'));
    // ticking a task while offline gives an error, not an exception
    await page.click('#tasklist input.tick >> nth=0');
    await page.waitForFunction(() => /Cannot reach Trimple|^Could not update the task/.test(document.querySelector('#msg').textContent));
    assert.deepEqual(page.errors.filter((e) => !/ERR_CONNECTION|EventSource/.test(e)), []);
  } finally { await page.context().close(); await app.stop(); }
});

test('A4. mixed line endings: panel tick keeps every other byte; editor save normalises (documented)', async () => {
  const mixed = Buffer.from('first\r\nsecond\n- [ ] task one !1\nthird\r\n- [ ] task two\r\nlast');
  await withApp({ setup: (d) => fs.writeFileSync(path.join(d, 'Mixed.txt'), mixed) }, async (app, page) => {
    await L.taskRow(page, 'task two').locator('input.tick').click();
    await L.waitFor(() => Buffer.compare(app.read('Mixed.txt'), mixed) !== 0, { what: 'written' });
    assert.equal(L.diffBytes(mixed, app.read('Mixed.txt')).length, 1);
    await L.taskRow(page, 'task two').locator('input.tick').count(); // may be hidden now
    // editing in the editor and saving rewrites every line ending with the dominant one
    await openFile(page, 'Mixed.txt');
    await L.activeTa(page).click();
    await page.keyboard.press('Control+End');
    await page.keyboard.type('!');
    await page.keyboard.press('Control+s');
    await L.waitFor(async () => (await dots(page)) === 0, { what: 'saved' });
    const after = app.read('Mixed.txt');
    const c = L.eolCounts(after);
    console.log(`# mixed-EOL file after an editor save: ${JSON.stringify(c)} (before: ${JSON.stringify(L.eolCounts(mixed))})`);
    assert.ok(c.bareLF === 0 || c.crlf === 0, 'file is uniform after an editor save');
  });
});

test('A5. 5 MB limit: an oversized note is not listed (and nothing breaks)', async () => {
  await withApp({ setup: (d) => { fs.writeFileSync(path.join(d, 'Huge.txt'), '- [ ] huge\r\n' + 'x'.repeat(5 * 1024 * 1024)); fs.writeFileSync(path.join(d, 'Big.txt'), '- [ ] big one\r\n' + 'y'.repeat(3 * 1024 * 1024) + '\r\n'); } }, async (app, page) => {
    const files = await L.fileNames(page);
    console.log('# files listed with a 5 MB+ and a 3 MB note present:', JSON.stringify(files));
    assert.ok(files.includes('Big.txt'));
    assert.ok(!files.includes('Huge.txt'), 'over-limit file is skipped');
    const t0 = Date.now();
    await openFile(page, 'Big.txt');
    await L.activeTa(page).click();
    await page.keyboard.press('Control+End');
    await page.keyboard.type('z');
    const dt = Date.now() - t0;
    console.log(`# open + type in a 3 MB note: ${dt} ms`);
    await page.keyboard.press('Control+s');
    await L.waitFor(async () => (await dots(page)) === 0, { what: 'saved 3 MB note', timeout: 10000 });
    assert.equal(app.read('Big.txt').length, ('- [ ] big one\r\n'.length) + 3 * 1024 * 1024 + 2 + 1);
  });
});

test('A6. a note that becomes read-only on disk (permissions) reports the error and keeps the edits', { skip: L.WIN && 'chmod semantics differ on Windows', todo: false }, async () => {
  if (process.getuid && process.getuid() === 0) return; // root ignores permission bits
  await withApp({}, async (app, page) => {
    await openFile(page, 'Home.txt');
    fs.chmodSync(app.file('Home.txt'), 0o444);
    fs.chmodSync(app.notes, 0o555);
    try {
      await L.activeTa(page).click();
      await page.keyboard.press('Control+End');
      await page.keyboard.type('x');
      await page.keyboard.press('Control+s');
      await page.waitForFunction(() => /^Could not save/.test(document.querySelector('#msg').textContent), null, { timeout: 15000 });
      assert.equal(await dots(page), 1);
    } finally { fs.chmodSync(app.notes, 0o755); fs.chmodSync(app.file('Home.txt'), 0o644); }
  });
});

test('A7. lots of rapid typing, tab switching and filtering at once stays consistent', async () => {
  await withApp({}, async (app, page) => {
    await openFile(page, 'Home.txt'); await openFile(page, 'Work.txt');
    await L.activeTa(page).click();
    for (let i = 0; i < 15; i++) {
      await page.keyboard.type('ab', { delay: 0 });
      await page.keyboard.press('Control+Alt+ArrowRight');
      await page.fill('#search', 'x'.repeat(i % 3));
      await page.keyboard.press('Control+Alt+ArrowLeft');
    }
    await page.fill('#search', '');
    await L.sleep(500);
    const st = await L.editorState(page);
    assert.ok(st.value.includes('ab'.repeat(5)), 'typed text arrived in the right tab');
    assert.equal(await page.locator('#tasklist li.task').count(), 7);
    await page.keyboard.press('Control+s');
    await L.waitFor(async () => (await dots(page)) === 0, { what: 'saved' });
    assert.equal(L.eolCounts(app.read('Work.txt')).bareLF, 0);
  });
});

test('A8. many open tabs: the active tab is scrolled into view in the tab strip', async () => {
  await withApp({ setup: (d) => { for (let i = 0; i < 25; i++) fs.writeFileSync(path.join(d, `Note number ${i} with a fairly long file name.txt`), `- [ ] t${i}\r\n`); } }, async (app, page) => {
    for (let i = 0; i < 25; i++) {
      await page.keyboard.press('Control+o');
      await page.locator('.modal input').waitFor();
      await page.keyboard.type(`number ${i} `);
      await page.locator(`.modal .pick button[data-path="Note number ${i} with a fairly long file name.txt"]`).waitFor();
      await page.keyboard.press('Enter');
      await page.locator('.modal-overlay').waitFor({ state: 'detached' });
      await L.waitFor(async () => (await L.tabNames(page)).length === i + 1, { what: `tab ${i + 1} opened` });
    }
    assert.equal((await L.tabNames(page)).length, 25);
    const info = await page.$eval('#tabs', (e) => ({ sw: e.scrollWidth, cw: e.clientWidth }));
    assert.ok(info.sw > info.cw, 'tab strip scrolls');
    const active = await page.locator('#tabs .tab.active').boundingBox();
    const strip = await page.locator('#tabs').boundingBox();
    assert.ok(active.x >= strip.x - 1 && active.x + active.width <= strip.x + strip.width + 1, 'active tab is scrolled into view (tab at x=' + Math.round(active.x) + ', strip ' + Math.round(strip.x) + '..' + Math.round(strip.x + strip.width) + ')');
  });
});
