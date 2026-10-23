'use strict';
// (3) ticking a task changes exactly one byte; (4) clicking task text opens the note at that line.
const { test, before, after } = require('node:test');
const assert = require('node:assert/strict');
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
const openCount = (page, n) => page.waitForFunction((n) => document.querySelectorAll('#tasklist li.task').length === n, n);

test('3a. ticking a task flips exactly one byte on disk (CRLF file) and unticking restores the bytes', async () => {
  await withApp({}, async (app, page) => {
    const orig = app.read('Work.txt');
    await L.taskRow(page, 'Write Q3 report').locator('input.tick').click();
    await openCount(page, 6);
    const after1 = app.read('Work.txt');
    const d = L.diffBytes(orig, after1);
    assert.equal(d.length, 1, 'bytes changed: ' + d);
    assert.equal(orig[d[0]], 0x20); assert.equal(after1[d[0]], 0x78); // ' ' -> 'x'
    assert.match(after1.toString(), /- \[x\] Write Q3 report !1 #work\r\n/);
    assert.equal(await page.textContent('#counts'), '6 open · 1 high · 3 med · 1 low');
    // other files untouched
    assert.deepEqual(app.read('Home.txt'), require('node:fs').readFileSync(L.SAMPLE + '/Home.txt'));
    // show completed, untick
    await page.check('#showdone');
    await openCount(page, 9);
    const row = L.taskRow(page, 'Write Q3 report');
    assert.equal(await row.locator('input.tick').isChecked(), true);
    await row.locator('input.tick').click();
    await page.waitForFunction(() => !document.querySelector('#tasklist li.task.done .ttext')?.textContent.includes('Q3'));
    await L.waitFor(() => Buffer.compare(app.read('Work.txt'), orig) === 0, { what: 'bytes restored' });
    await L.waitFor(async () => (await page.textContent('#counts')) === '7 open · 2 high · 3 med · 1 low', { what: 'counts restored' });
  });
});

test('3b. LF file with "* [x]" bullet: tick and untick keep every other byte', async () => {
  await withApp({}, async (app, page) => {
    const orig = app.read('Home.txt');
    await L.taskRow(page, 'Buy printer ink').locator('input.tick').first().click();
    await openCount(page, 6);
    const a = app.read('Home.txt');
    assert.equal(L.diffBytes(orig, a).length, 1);
    await page.check('#showdone');
    await openCount(page, 9);
    // untick the pre-existing done task: "* [x] Call plumber" -> "* [ ]"
    const before = app.read('Home.txt');
    await L.taskRow(page, 'Call plumber').locator('input.tick').click();
    await L.waitFor(() => Buffer.compare(app.read('Home.txt'), before) !== 0, { what: 'file written' });
    const b = app.read('Home.txt');
    assert.equal(L.diffBytes(before, b).length, 1);
    assert.match(b.toString(), /\* \[ \] Call plumber !1 #home\n/);
    assert.equal(L.eolCounts(b).crlf, 0, 'LF file stayed LF');
    // restore both
    await L.taskRow(page, 'Call plumber').locator('input.tick').click();
    await L.taskRow(page, 'Buy printer ink').locator('input.tick').first().click();
    await L.waitFor(() => Buffer.compare(app.read('Home.txt'), orig) === 0, { what: 'original bytes restored' });
  });
});

test('3c. ticking a task that is open in a clean editor tab updates the editor text', async () => {
  await withApp({}, async (app, page) => {
    await page.click('#filelist button:has-text("Work.txt")');
    await page.waitForFunction(() => document.querySelector('textarea.editor:not([hidden])')?.value.includes('Write Q3'));
    await L.taskRow(page, 'Review pull requests').locator('input.tick').click();
    await page.waitForFunction(() => document.querySelector('textarea.editor:not([hidden])').value.includes('[x] Review pull requests'));
    assert.equal(await page.locator('#tabs .dot').count(), 0, 'no dirty dot after a panel tick');
  });
});

test('3d. ticking a task whose file has unsaved edits in the editor is refused', async () => {
  await withApp({}, async (app, page) => {
    const orig = app.read('Work.txt');
    await page.click('#filelist button:has-text("Work.txt")');
    await page.waitForFunction(() => document.querySelector('textarea.editor:not([hidden])')?.value.includes('Write Q3'));
    await L.activeTa(page).click();
    await page.keyboard.press('Control+End');
    await page.keyboard.type('xyz');
    await L.taskRow(page, 'Write Q3 report').locator('input.tick').click();
    await page.waitForFunction(() => /Save "Work.txt" first/.test(document.querySelector('#msg').textContent));
    assert.deepEqual(app.read('Work.txt'), orig);
    assert.equal(await page.locator('#msg.err').count(), 1);
  });
});

test('3e. rapid repeated clicking never corrupts the file', async () => {
  await withApp({}, async (app, page) => {
    const orig = app.read('Work.txt');
    const cb = L.taskRow(page, 'Write Q3 report').locator('input.tick');
    const handle = await cb.elementHandle();
    for (let i = 0; i < 12; i++) await handle.evaluate((e) => e.click()).catch(() => {});
    await L.sleep(1500);
    const now = app.read('Work.txt');
    const d = L.diffBytes(orig, now);
    assert.ok(d.length <= 1, 'at most the single checkbox byte differs: ' + d);
    assert.equal(L.eolCounts(now).bareLF, 0);
    // UI and disk agree
    await page.check('#showdone');
    await L.sleep(500);
    const ticked = /\[x\] Write Q3/.test(now.toString());
    const rowDone = await L.taskRow(page, 'Write Q3 report').evaluate((li) => li.classList.contains('done'));
    assert.equal(rowDone, ticked, 'panel agrees with disk');
  });
});

test('3f. ticking while the file changes underneath is refused, never ticks the wrong line', async () => {
  await withApp({}, async (app, page) => {
    // API level: stale line/raw
    const fs = require('node:fs');
    const orig = app.read('Work.txt');
    fs.writeFileSync(app.file('Work.txt'), Buffer.concat([Buffer.from('new first line\r\n'), orig]));
    const afterEdit = app.read('Work.txt');
    const r = await app.request({ method: 'POST', path: '/api/task', body: { path: 'Work.txt', line: 3, done: true, raw: '- [ ] Write Q3 report !1 #work' } });
    assert.equal(r.status, 409, r.text);
    assert.deepEqual(app.read('Work.txt'), afterEdit, 'file untouched on stale tick');
    // UI level: write, then click before the panel has refreshed
    fs.writeFileSync(app.file('Work.txt'), orig);
    await L.sleep(2200);
    await openCount(page, 7);
    const row = L.taskRow(page, 'Review pull requests').locator('input.tick');
    fs.writeFileSync(app.file('Work.txt'), Buffer.concat([Buffer.from('a\r\nb\r\n'), orig]));
    const edited = app.read('Work.txt');
    await row.click();
    await L.sleep(2500);
    const now = app.read('Work.txt').toString();
    const lines = now.split('\r\n');
    const ticked = lines.filter((l) => /\[x\]/.test(l));
    assert.ok(ticked.every((l) => /Send invoice|Review pull requests/.test(l)), 'only intended lines are ticked: ' + ticked);
    assert.ok(L.diffBytes(edited, app.read('Work.txt')).length <= 1);
    // the panel converged to the new line numbers
    const tl = await page.getAttribute('#tasklist li.task >> nth=0', 'data-line');
    assert.ok(Number(tl) >= 1);
  });
});

test('3g. unicode, emoji and very long task text', async () => {
  const long = 'L'.repeat(3000) + ' endmarker';
  await withApp({
    setup: (dir) => {
      require('node:fs').writeFileSync(require('node:path').join(dir, 'Uni.txt'), Buffer.from([
        '- [ ] Café meeting !1 #café #日本語',
        '- [ ] Launch 🚀 rocket #🚀 #space',
        '- [ ] ' + long + ' !2 #long #' + 'x'.repeat(80),
        '- [ ] ‮RTL override trick <b>bold</b> <img src=x onerror=alert(1)> #html',
        '',
      ].join('\n'), 'utf8'));
    },
  }, async (app, page) => {
    const names = await L.tasksText(page);
    assert.ok(names.includes('Café meeting'));
    assert.ok(names.some((n) => n.startsWith('Launch')));
    assert.deepEqual(await page.$$eval('#label option', (o) => o.map((x) => x.textContent)).then((a) => a.filter((x) => /caf|日/.test(x))), ['#café', '#日本語']);
    await page.selectOption('#label', '日本語');
    await openCount(page, 1);
    assert.deepEqual(await L.tasksText(page), ['Café meeting']);
    await page.selectOption('#label', '');
    await openCount(page, 11);
    // html in text is rendered as text, not as markup
    assert.equal(await page.locator('#tasklist img, #tasklist b').count(), 0);
    assert.equal(await page.locator('#tasklist .ttext', { hasText: '<b>bold</b>' }).count(), 1);
    // layout: no horizontal overflow in the list; the long row is clipped with an ellipsis, chips still visible
    const m = await page.$eval('#tasklist', (e) => ({ sw: e.scrollWidth, cw: e.clientWidth }));
    assert.ok(m.sw <= m.cw + 1, `task list overflows horizontally ${m.sw} > ${m.cw}`);
    const row = L.taskRow(page, 'LLLLLLLL');
    const rb = await row.boundingBox();
    const cb = await row.locator('.chips').boundingBox();
    assert.ok(cb.x + cb.width <= rb.x + rb.width + 1, 'chips are inside the row');
    assert.ok(rb.height < 60, 'row height stays small: ' + rb.height);
    // tick the emoji / unicode task: exactly one byte
    const orig = app.read('Uni.txt');
    await L.taskRow(page, 'Café meeting').locator('input.tick').click();
    await L.waitFor(() => Buffer.compare(app.read('Uni.txt'), orig) !== 0, { what: 'file written' });
    assert.equal(L.diffBytes(orig, app.read('Uni.txt')).length, 1);
    assert.equal(await page.evaluate(() => window.__alerted === true), false);
    // the long task opens in the editor
    await page.click('#tasklist .ttext:has-text("LLLLLLL")');
    await page.waitForFunction(() => document.querySelector('textarea.editor:not([hidden])').value.includes('endmarker'));
  });
});

test('4a. clicking task text opens the note in a tab with the caret on that line', async () => {
  await withApp({}, async (app, page) => {
    await page.click('#tasklist .ttext:has-text("Draft API spec")');
    await page.waitForSelector('#tabs .tab.active .tname:text("Project.txt")');
    const st = await L.editorState(page);
    const expected = st.value.split('\n').slice(0, 2).join('\n').length + 1; // start of line 3
    assert.equal(st.start, expected);
    assert.equal(st.end, expected);
    assert.equal(await page.evaluate(() => document.activeElement.classList.contains('editor')), true, 'editor has focus');
    assert.equal(await page.textContent('#pos'), 'Ln 3, Col 1');
    // untitled scratch tab was replaced
    assert.deepEqual(await L.tabNames(page), ['Project.txt']);
    // another task in the same file moves the caret, no second tab
    await page.click('#tasklist .ttext:has-text("Fix flaky build")');
    await page.waitForFunction(() => document.querySelector('#pos').textContent === 'Ln 4, Col 1');
    assert.deepEqual(await L.tabNames(page), ['Project.txt']);
    // and a task in a CRLF file in another tab
    await page.click('#tasklist .ttext:has-text("Review pull requests")');
    await page.waitForSelector('#tabs .tab.active .tname:text("Work.txt")');
    await page.waitForFunction(() => document.querySelector('#pos').textContent === 'Ln 4, Col 1');
    const st2 = await L.editorState(page);
    assert.ok(st2.value.slice(0, st2.start).endsWith('\n'));
    assert.ok(st2.value.slice(st2.start).startsWith('- [ ] Review pull requests'));
    assert.deepEqual(await L.tabNames(page), ['Project.txt', 'Work.txt']);
  });
});

function longNote(n, wrapEvery) {
  const lines = [];
  for (let i = 1; i <= n; i++) {
    if (i === 350) lines.push('- [ ] Far away task !1 #far');
    else lines.push(wrapEvery ? `line ${i} ` + 'lorem ipsum dolor sit amet '.repeat(wrapEvery) : `line ${i} filler text`);
  }
  return lines.join('\r\n') + '\r\n';
}

// y of the caret line inside a textarea, computed with a hidden mirror (independent of the app's own code)
async function lineVisibility(page) {
  return page.evaluate(() => {
    const ta = [...document.querySelectorAll('textarea.editor')].find((t) => !t.hidden);
    const cs = getComputedStyle(ta);
    const m = document.createElement('div');
    for (const p of ['fontFamily', 'fontSize', 'lineHeight', 'letterSpacing', 'tabSize', 'paddingTop', 'paddingLeft', 'paddingRight', 'paddingBottom', 'whiteSpace', 'overflowWrap', 'wordBreak']) m.style[p] = cs[p];
    m.style.cssText += ';position:absolute;visibility:hidden;left:-9999px;box-sizing:border-box;width:' + ta.clientWidth + 'px';
    m.textContent = ta.value.slice(0, ta.selectionStart);
    const s = document.createElement('span'); s.textContent = '​'; m.append(s);
    document.body.append(m);
    const top = s.offsetTop, lh = parseFloat(cs.lineHeight);
    m.remove();
    return { top, lh, scrollTop: ta.scrollTop, h: ta.clientHeight, visible: top >= ta.scrollTop && top + lh <= ta.scrollTop + ta.clientHeight };
  });
}

test('4b. long (400-line) note: caret on the task line and textarea scrolled so it is visible', async () => {
  await withApp({ setup: (d) => require('node:fs').writeFileSync(require('node:path').join(d, 'Long.txt'), longNote(400, 0)) }, async (app, page) => {
    await page.click('#tasklist .ttext:has-text("Far away task")');
    await page.waitForSelector('#tabs .tab.active .tname:text("Long.txt")');
    const st = await L.editorState(page);
    const lines = st.value.split('\n');
    const expected = lines.slice(0, 349).join('\n').length + 1;
    assert.equal(st.start, expected, 'caret at start of line 350');
    assert.ok(st.value.slice(st.start).startsWith('- [ ] Far away task'));
    assert.ok(st.scrollTop > 1000, 'scrolled: ' + st.scrollTop);
    const v = await lineVisibility(page);
    assert.ok(v.visible, 'line is visible ' + JSON.stringify(v));
    // and the status bar agrees
    assert.equal(await page.textContent('#pos'), 'Ln 350, Col 1');
  });
});

test('4c. long note with soft-wrapped lines: task line still scrolled into view', async () => {
  await withApp({ setup: (d) => require('node:fs').writeFileSync(require('node:path').join(d, 'Wrapped.txt'), longNote(400, 8)) }, async (app, page) => {
    await page.click('#tasklist .ttext:has-text("Far away task")');
    await page.waitForSelector('#tabs .tab.active .tname:text("Wrapped.txt")');
    const v = await lineVisibility(page);
    assert.ok(v.visible, 'line is visible ' + JSON.stringify(v));
    // narrow window too
    await page.setViewportSize({ width: 700, height: 500 });
    await page.click('#tasklist .ttext:has-text("Far away task")');
    await L.sleep(200);
    const v2 = await lineVisibility(page);
    assert.ok(v2.visible, 'still visible after resize/re-click ' + JSON.stringify(v2));
  });
});

test('4d. already-open long note scrolled elsewhere: clicking the task re-scrolls to it', async () => {
  await withApp({ setup: (d) => require('node:fs').writeFileSync(require('node:path').join(d, 'Long.txt'), longNote(400, 0)) }, async (app, page) => {
    await page.click('#filelist button:has-text("Long.txt")');
    await page.waitForFunction(() => document.querySelector('textarea.editor:not([hidden])').value.startsWith('line 1'));
    await L.activeTa(page).click();
    await page.keyboard.press('Control+Home');
    assert.ok((await L.editorState(page)).scrollTop < 30);
    await page.click('#tasklist .ttext:has-text("Far away task")');
    await L.sleep(200);
    const v = await lineVisibility(page);
    assert.ok(v.visible, JSON.stringify(v));
  });
});

test('4e. 2000-task note: panel loads, ticks and filters stay responsive', async () => {
  const lines = [];
  for (let i = 1; i <= 2000; i++) lines.push(`- [ ] Task number ${i} !${(i % 3) + 1} #lab${i % 20}`);
  const t0 = Date.now();
  await withApp({ setup: (d) => require('node:fs').writeFileSync(require('node:path').join(d, 'Big.txt'), lines.join('\r\n') + '\r\n') }, async (app, page) => {
    const load = Date.now() - t0;
    await page.waitForFunction(() => document.querySelectorAll('#tasklist li.task').length >= 2000);
    assert.equal(await page.locator('#tasklist li.task').count(), 2007);
    console.log(`# 2000 tasks: startup to ready incl. process start ${load} ms`);
    let t = Date.now();
    await page.fill('#search', 'number 1999');
    await page.waitForFunction(() => document.querySelectorAll('#tasklist li.task').length === 1);
    console.log(`# 2000 tasks: search round trip ${Date.now() - t} ms`);
    assert.ok(Date.now() - t < 3000, 'search is slow');
    await page.fill('#search', '');
    await page.waitForFunction(() => document.querySelectorAll('#tasklist li.task').length === 2007);
    const orig = app.read('Big.txt');
    t = Date.now();
    await L.taskRow(page, 'Task number 1000').locator('input.tick').click();
    await page.waitForFunction(() => document.querySelectorAll('#tasklist li.task').length === 2006);
    const tick = Date.now() - t;
    console.log(`# 2000 tasks: tick round trip ${tick} ms`);
    assert.ok(tick < 3000, 'tick is slow: ' + tick);
    assert.equal(L.diffBytes(orig, app.read('Big.txt')).length, 1);
    // open the big note and jump to the last task
    t = Date.now();
    await page.fill('#search', 'number 2000');
    await page.waitForFunction(() => document.querySelectorAll('#tasklist li.task').length === 1);
    await page.click('#tasklist .ttext');
    await page.waitForFunction(() => document.querySelector('#pos').textContent === 'Ln 2000, Col 1');
    console.log(`# 2000 tasks: open note at last task ${Date.now() - t} ms`);
  });
});

test('4f. more than 2000 notes shows the truncation banner', async () => {
  await withApp({
    setup: (d) => { for (let i = 0; i < 2010; i++) require('node:fs').writeFileSync(require('node:path').join(d, `n${String(i).padStart(4, '0')}.txt`), `- [ ] t${i}\r\n`); },
  }, async (app, page) => {
    assert.equal(await page.isVisible('#banner'), true);
    assert.match(await page.textContent('#banner'), /more than 2000 notes/);
    assert.match(await page.textContent('#filecount'), /2000 \(first 2000 only\)/);
  });
});

test('4g. opening a note from the Files list starts at the top (like Notepad), not at the end', { todo: 'makeDoc sets ta.value, which leaves the caret at the END and focus() scrolls there (web/app.js makeDoc/openDoc)' }, async () => {
  await withApp({ setup: (d) => require('node:fs').writeFileSync(require('node:path').join(d, 'Long.txt'), longNote(400, 0)) }, async (app, page) => {
    await page.click('#filelist button:has-text("Long.txt")');
    await page.waitForSelector('#tabs .tab.active .tname:text("Long.txt")');
    await L.sleep(200);
    const st = await L.editorState(page);
    assert.equal(st.start, 0, 'caret offset after opening from the Files list');
    assert.ok(st.scrollTop < 30, 'scrollTop after opening: ' + st.scrollTop);
  });
});
