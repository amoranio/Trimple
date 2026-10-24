'use strict';
// (6) outside edits / conflicts / deletion, (7) read-only protection for files that cannot round-trip.
const { test, before, after } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
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
    assert.deepEqual(page.dialogs, [], 'native dialogs');
  } finally { await app.stop(); }
}
async function openFile(page, name) {
  await page.click(`#filelist button:has(.fname:text-is("${name}"))`);
  await page.waitForFunction((n) => document.querySelector('#tabs .tab.active .tname')?.textContent === n.split('/').pop(), name);
}
const edValue = (page) => page.evaluate(() => [...document.querySelectorAll('textarea.editor')].find((t) => !t.hidden).value);
const noticeVisible = (page) => page.isVisible('#notice');

test('6a. outside edit to an open CLEAN file reloads in the editor within ~5s', async () => {
  await withApp({}, async (app, page) => {
    await openFile(page, 'Work.txt');
    const orig = app.read('Work.txt');
    await L.activeTa(page).click();
    await page.keyboard.press('Control+End');
    const t0 = Date.now();
    app.write('Work.txt', Buffer.concat([orig, Buffer.from('EXTERNAL LINE\r\n- [ ] ext task !3\r\n')]));
    await page.waitForFunction(() => [...document.querySelectorAll('textarea.editor')].find((t) => !t.hidden).value.includes('EXTERNAL LINE'), null, { timeout: 6000 });
    const dt = Date.now() - t0;
    console.log(`# outside edit visible in editor after ${dt} ms`);
    assert.ok(dt < 5000, 'reload took ' + dt);
    assert.match(await L.status(page), /Reloaded "Work\.txt"/);
    assert.equal(await page.locator('#tabs .dot').count(), 0);
    assert.equal(await noticeVisible(page), false);
    await page.waitForFunction(() => [...document.querySelectorAll('#tasklist .ttext')].some((e) => e.textContent === 'ext task'));
    // and a following save does not trigger the conflict dialog
    await L.activeTa(page).click();
    await page.keyboard.press('Control+End');
    await page.keyboard.type('mine');
    await page.keyboard.press('Control+s');
    await page.waitForFunction(() => document.querySelectorAll('#tabs .dot').length === 0);
    assert.equal(await L.modal(page).count(), 0);
    assert.ok(app.read('Work.txt').toString().endsWith('mine'));
  });
});

test('6b. outside edit to a DIRTY file shows the conflict notice; Keep mine then Save overwrites without prompting', async () => {
  await withApp({}, async (app, page) => {
    await openFile(page, 'Home.txt');
    await L.activeTa(page).click();
    await page.keyboard.press('Control+End');
    await page.keyboard.type('MY-EDIT');
    app.write('Home.txt', 'completely different\n- [ ] other task\n');
    await page.waitForSelector('#notice:not([hidden])', { timeout: 6000 });
    assert.match(await page.textContent('#notice-text'), /"Home\.txt" changed outside Trimple, and you have unsaved edits here/);
    assert.equal(await page.locator('#tabs .tab.conflict').count(), 1);
    assert.ok((await edValue(page)).includes('MY-EDIT'), 'editor keeps my text');
    assert.equal(await page.isDisabled('#notice-reload'), false);
    await page.click('#notice-keep');
    await page.waitForSelector('#notice', { state: 'hidden' });
    await page.keyboard.press('Control+s');
    await page.waitForFunction(() => document.querySelectorAll('#tabs .dot').length === 0);
    assert.equal(await L.modal(page).count(), 0, 'no prompt after Keep mine');
    const now = app.read('Home.txt').toString();
    assert.ok(now.includes('MY-EDIT') && now.includes('Renew parking pass'), 'my version overwrote the outside one');
    assert.ok(!now.includes('completely different'));
    // and the notice does not come back
    await L.sleep(2200);
    assert.equal(await noticeVisible(page), false);
  });
});

test('6c. conflict, Save without choosing: asks "File changed outside Trimple"; Cancel keeps both', async () => {
  await withApp({}, async (app, page) => {
    await openFile(page, 'Home.txt');
    await L.activeTa(page).click();
    await page.keyboard.press('Control+End');
    await page.keyboard.type('MINE');
    app.write('Home.txt', 'theirs\n');
    await page.waitForSelector('#notice:not([hidden])', { timeout: 6000 });
    await page.keyboard.press('Control+s');
    await L.modal(page).waitFor();
    assert.equal(await L.modal(page).locator('h3').textContent(), 'File changed outside Trimple');
    await L.modal(page).locator('button:has-text("Cancel")').click();
    assert.equal(app.read('Home.txt').toString(), 'theirs\n');
    assert.ok((await edValue(page)).includes('MINE'));
    await page.keyboard.press('Control+s');
    await L.modal(page).waitFor();
    await L.modal(page).locator('button:has-text("Overwrite")').click();
    await L.waitFor(() => app.read('Home.txt').toString().includes('MINE'), { what: 'overwritten' });
  });
});

test('6d. "Reload from disk" discards my edits and shows the outside version', async () => {
  await withApp({}, async (app, page) => {
    await openFile(page, 'Work.txt');
    await L.activeTa(page).click();
    await page.keyboard.press('Control+End');
    await page.keyboard.type('DISCARD-ME');
    const outside = 'outside version\r\n- [ ] only task\r\n';
    app.write('Work.txt', outside);
    await page.waitForSelector('#notice:not([hidden])', { timeout: 6000 });
    // discarding unsaved edits asks first; Cancel (Escape) keeps them
    await page.click('#notice-reload');
    await page.locator('.modal').waitFor();
    await page.keyboard.press('Escape');
    await page.locator('.modal-overlay').waitFor({ state: 'detached' });
    assert.ok((await edValue(page)).includes('DISCARD-ME'), 'cancelling must keep my edits');
    await page.click('#notice-reload');
    await page.locator('.modal').waitFor();
    await page.click('.modal button.primary');
    await page.locator('.modal-overlay').waitFor({ state: 'detached' });
    await page.waitForFunction(() => [...document.querySelectorAll('textarea.editor')].find((t) => !t.hidden).value === 'outside version\n- [ ] only task\n');
    assert.equal(await noticeVisible(page), false);
    assert.equal(await page.locator('#tabs .dot').count(), 0);
    assert.equal(app.read('Work.txt').toString(), outside);
    assert.equal(await page.locator('#tabs .tab.conflict').count(), 0);
  });
});

test('6e. outside edit that only touches the timestamp does not disturb a dirty editor', async () => {
  await withApp({}, async (app, page) => {
    await openFile(page, 'Home.txt');
    await L.activeTa(page).click();
    await page.keyboard.press('Control+End');
    await page.keyboard.type('KEEP');
    const t = new Date(Date.now() + 5000);
    fs.utimesSync(app.file('Home.txt'), t, t);
    await L.sleep(3500);
    assert.equal(await noticeVisible(page), false, 'a touch is not a conflict');
    assert.ok((await edValue(page)).includes('KEEP'));
  });
});

test('6f. deleting the open file shows the deleted notice; saving recreates it', async () => {
  await withApp({}, async (app, page) => {
    await openFile(page, 'Home.txt');
    fs.rmSync(app.file('Home.txt'));
    await page.waitForSelector('#notice:not([hidden])', { timeout: 6000 });
    assert.match(await page.textContent('#notice-text'), /"Home\.txt" was deleted or moved outside Trimple/);
    assert.equal(await page.isDisabled('#notice-reload'), true);
    await page.waitForFunction(() => ![...document.querySelectorAll('#filelist .fname')].some((e) => e.textContent === 'Home.txt'));
    assert.equal(await page.locator('#tabs .tab.conflict').count(), 1);
    // my text is still in the editor
    assert.ok((await edValue(page)).includes('Shopping and home'));
    await L.activeTa(page).click();
    await page.keyboard.press('Control+End');
    await page.keyboard.type('!');
    await page.keyboard.press('Control+s');
    await page.waitForFunction(() => document.querySelectorAll('#tabs .dot').length === 0);
    await page.waitForSelector('#notice', { state: 'hidden' });
    assert.ok(fs.existsSync(app.file('Home.txt')));
    assert.ok(app.read('Home.txt').toString().endsWith('!'));
    assert.equal(L.eolCounts(app.read('Home.txt')).crlf, 0, 'LF file recreated with LF');
  });
});

test('6g. file deleted and recreated outside while open and clean: editor follows', async () => {
  await withApp({}, async (app, page) => {
    await openFile(page, 'Home.txt');
    fs.rmSync(app.file('Home.txt'));
    await page.waitForSelector('#notice:not([hidden])', { timeout: 6000 });
    app.write('Home.txt', 'back again\n');
    await page.waitForFunction(() => [...document.querySelectorAll('textarea.editor')].find((t) => !t.hidden).value === 'back again\n', null, { timeout: 6000 })
      .catch(async () => { throw new Error('editor did not pick up the re-created file; notice visible=' + await noticeVisible(page) + ', value=' + JSON.stringify(await edValue(page))); });
    assert.equal(await noticeVisible(page), false);
  });
});

test('6h. a new file created outside appears in the Files list and Tasks within a few seconds', async () => {
  await withApp({}, async (app, page) => {
    app.write('Fresh.txt', '- [ ] freshly added !1\r\n');
    await page.waitForFunction(() => [...document.querySelectorAll('#tasklist .ttext')].some((e) => e.textContent === 'freshly added'), null, { timeout: 6000 });
    assert.ok((await L.fileNames(page)).includes('Fresh.txt'));
    fs.rmSync(app.file('Fresh.txt'));
    await page.waitForFunction(() => ![...document.querySelectorAll('#tasklist .ttext')].some((e) => e.textContent === 'freshly added'), null, { timeout: 6000 });
  });
});

test('6i. ticking in the panel while the same file changed outside: task state wins, no data loss', async () => {
  await withApp({}, async (app, page) => {
    await openFile(page, 'Work.txt');
    const orig = app.read('Work.txt');
    app.write('Work.txt', Buffer.concat([orig, Buffer.from('tail\r\n')]));
    await L.taskRow(page, 'Book offsite venue').locator('input.tick').click();
    await L.waitFor(() => /\[x\] Book offsite venue/.test(app.read('Work.txt').toString()), { what: 'tick applied' });
    assert.ok(app.read('Work.txt').toString().endsWith('tail\r\n'), 'outside edit preserved');
    await page.waitForFunction(() => [...document.querySelectorAll('textarea.editor')].find((t) => !t.hidden).value.includes('[x] Book offsite') && true);
  });
});

// ------------------------------------------------------------------ (7) read-only protection
const BOM_BAD = Buffer.concat([Buffer.from([0xef, 0xbb, 0xbf]), Buffer.from('- [ ] a'), Buffer.from([0xff])]);
const NUL = Buffer.from('header\r\n- [ ] binary task !1\r\nbin\0ary\r\n');

function roSetup(d) {
  fs.writeFileSync(require('node:path').join(d, 'bad.txt'), BOM_BAD);
  fs.writeFileSync(require('node:path').join(d, 'binary.txt'), NUL);
}

test('7a. invalid-byte and NUL files open read-only, Save is refused with an explanation, bytes untouched', async () => {
  await withApp({ setup: roSetup }, async (app, page) => {
    for (const [name, bytes] of [['bad.txt', BOM_BAD], ['binary.txt', NUL]]) {
      await openFile(page, name);
      const st = await L.editorState(page);
      assert.equal(st.readOnly, true, name + ' textarea is readOnly');
      assert.equal(await page.locator('#tabs .tab.active.readonly').count(), 1);
      assert.match(await L.status(page), /opened read-only to protect it/);
      assert.equal(await page.locator('#msg.err').count(), 1);
      // typing does nothing
      await L.activeTa(page).click();
      await page.keyboard.press('Control+End');
      await page.keyboard.type('hello');
      assert.equal((await L.editorState(page)).value, st.value, 'typing into a read-only editor changes nothing');
      assert.equal(await page.locator('#tabs .dot').count(), 0);
      // Ctrl+S: dialog explaining why
      await page.keyboard.press('Control+s');
      await L.modal(page).waitFor();
      assert.equal(await L.modal(page).locator('h3').textContent(), 'Read-only file');
      assert.match(await L.modal(page).textContent(), /will not write it/);
      await page.keyboard.press('Escape');
      await page.locator('.modal-overlay').waitFor({ state: 'detached' });
      // Save button too, and Save As
      await page.click('#btn-save');
      await L.modal(page).waitFor();
      await L.modal(page).locator('button:has-text("OK")').click();
      await page.keyboard.press('Control+Shift+S');
      await L.modal(page).waitFor();
      assert.equal(await L.modal(page).locator('h3').textContent(), 'Read-only file');
      await L.modal(page).locator('button:has-text("OK")').click();
      // key commands refuse too
      await page.keyboard.press('Alt+t');
      await page.keyboard.press('Control+Enter');
      assert.equal((await L.editorState(page)).value, st.value);
      assert.deepEqual(app.read(name), bytes, name + ' bytes untouched');
    }
    // the shown text never contains NUL / raw invalid bytes
    assert.ok(!(await edValue(page)).includes('\0'));
  });
});

test('7b. ticking a task that lives in a read-only file is refused (panel and API), bytes untouched', async () => {
  await withApp({ setup: roSetup }, async (app, page) => {
    const rows = await L.tasksText(page);
    console.log('# read-only files expose tasks in the panel:', JSON.stringify(rows.filter((r) => /binary|a./.test(r) && r.length < 12)));
    for (const [name, text, bytes] of [['binary.txt', 'binary task', NUL], ['bad.txt', 'a', BOM_BAD]]) {
      const row = L.taskRow(page, text).first();
      if (await row.count() === 0) continue;
      await row.locator('input.tick').click();
      await page.waitForFunction(() => /^Could not update the task/.test(document.querySelector('#msg').textContent));
      assert.match(await L.status(page), /cannot be rewritten safely|no longer a task|changed/);
      assert.deepEqual(app.read(name), bytes);
    }
    const r = await app.request({ method: 'POST', path: '/api/task', body: { path: 'binary.txt', line: 2, done: true, raw: '- [ ] binary task !1' } });
    assert.equal(r.status, 409, r.text); assert.equal(r.json.code, 'readonly');
    const put = await app.request({ method: 'PUT', path: '/api/file', body: { path: 'binary.txt', text: 'x', kind: 'utf8', eol: 'crlf', expectedHash: '', create: false, overwrite: true } });
    assert.equal(put.status, 409); assert.equal(put.json.code, 'readonly');
    const put2 = await app.request({ method: 'PUT', path: '/api/file', body: { path: 'bad.txt', text: 'x', kind: 'utf8', eol: 'crlf', create: true, overwrite: true } });
    assert.equal(put2.status, 409, 'even create+overwrite must not clobber an unreadable file: ' + put2.text);
    assert.deepEqual(app.read('binary.txt'), NUL);
    assert.deepEqual(app.read('bad.txt'), BOM_BAD);
    // the file list flags them
    assert.equal(await page.locator('#filelist .badge', { hasText: 'read-only' }).count(), 2);
  });
});

test('7c. a UTF-16 and an ANSI (cp1252) note round-trip: tick changes one byte, edit keeps encoding', async () => {
  const utf16 = Buffer.concat([Buffer.from([0xff, 0xfe]), Buffer.from('- [ ] wide task !1 #w\r\nline2\r\n', 'utf16le')]);
  const ansi = Buffer.concat([Buffer.from('- [ ] caf'), Buffer.from([0xe9]), Buffer.from(' ansi #a\r\n')]);
  await withApp({ setup: (d) => { fs.writeFileSync(require('node:path').join(d, 'u16.txt'), utf16); fs.writeFileSync(require('node:path').join(d, 'ansi.txt'), ansi); } }, async (app, page) => {
    const names = await L.tasksText(page);
    assert.ok(names.includes('wide task'), JSON.stringify(names));
    await L.taskRow(page, 'wide task').locator('input.tick').click();
    await L.waitFor(() => Buffer.compare(app.read('u16.txt'), utf16) !== 0, { what: 'u16 written' });
    const d = L.diffBytes(utf16, app.read('u16.txt'));
    assert.equal(d.length, 1, 'bytes changed in UTF-16 file: ' + d);
    await openFile(page, 'ansi.txt');
    assert.equal(await page.textContent('#enc'), 'ANSI');
    assert.ok((await edValue(page)).includes('café'));
    await L.activeTa(page).click();
    await page.keyboard.press('Control+End');
    await page.keyboard.type('z');
    await page.keyboard.press('Control+s');
    await page.waitForFunction(() => document.querySelectorAll('#tabs .dot').length === 0);
    assert.deepEqual(app.read('ansi.txt'), Buffer.concat([ansi, Buffer.from('z')]));
  });
});
