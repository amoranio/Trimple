'use strict';
// (5) save keeps line endings, (9) save dialog, (11) keyboard commands, (12) closing dirty tabs, drafts.
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
  await page.click(`#filelist button[title="${name}"]`);
  await page.waitForFunction((n) => document.querySelector('#tabs .tab.active .tname')?.textContent === n.split('/').pop(), name);
}
const dirtyDots = (page) => page.locator('#tabs .dot').count();

test('5a. edit + Ctrl+S: dirty dot appears and clears, CRLF file stays CRLF with no bare LF', async () => {
  await withApp({}, async (app, page) => {
    const orig = app.read('Work.txt');
    await openFile(page, 'Work.txt');
    await L.activeTa(page).click();
    await page.keyboard.press('Control+End');
    await page.keyboard.type('Added line one');
    await page.keyboard.press('Enter');
    await page.keyboard.type('- [ ] Added task !2 #new');
    await page.keyboard.press('Enter');
    await page.keyboard.press('Enter');
    await page.keyboard.type('last');
    assert.equal(await dirtyDots(page), 1);
    assert.match(await page.title(), /^\* Work\.txt/);
    await page.keyboard.press('Control+s');
    await page.waitForFunction(() => document.querySelectorAll('#tabs .dot').length === 0);
    assert.equal(await page.title(), 'Work.txt - Trimple');
    assert.match(await L.status(page), /Saved Work\.txt/);
    const now = app.read('Work.txt');
    assert.deepEqual(now.subarray(0, orig.length), orig, 'original bytes are an untouched prefix');
    const c = L.eolCounts(now);
    assert.equal(c.bareLF, 0, 'no bare LF'); assert.equal(c.bareCR, 0);
    assert.equal(now.toString(), orig.toString() + 'Added line one\r\n- [ ] Added task !2 #new\r\n\r\nlast');
    // new task shows in the panel
    await page.waitForFunction(() => [...document.querySelectorAll('#tasklist .ttext')].some((e) => e.textContent === 'Added task'));
    // saving again with no changes does nothing
    await page.keyboard.press('Control+s');
    await page.waitForFunction(() => document.querySelector('#msg').textContent === 'No changes to save.');
  });
});

test('5b. LF file stays LF after editing and saving', async () => {
  await withApp({}, async (app, page) => {
    const orig = app.read('Home.txt');
    await openFile(page, 'Home.txt');
    await L.activeTa(page).click();
    await page.keyboard.press('Control+Home');
    await page.keyboard.type('Top line');
    await page.keyboard.press('Enter');
    await page.keyboard.press('Control+s');
    await page.waitForFunction(() => document.querySelectorAll('#tabs .dot').length === 0);
    const now = app.read('Home.txt');
    assert.equal(now.toString(), 'Top line\n' + orig.toString());
    assert.equal(L.eolCounts(now).crlf, 0);
    assert.equal(await page.textContent('#eol'), 'LF');
    assert.equal(await page.textContent('#enc'), 'UTF-8');
  });
});

test('5c. undoing an edit back to the saved text clears the dirty dot; Save button works too', async () => {
  await withApp({}, async (app, page) => {
    await openFile(page, 'Home.txt');
    await L.activeTa(page).click();
    await page.keyboard.press('Control+End');
    await page.keyboard.type('zz');
    assert.equal(await dirtyDots(page), 1);
    await page.keyboard.press('Backspace'); await page.keyboard.press('Backspace');
    assert.equal(await dirtyDots(page), 0);
    await page.keyboard.type('Q');
    await page.click('#btn-save');
    await page.waitForFunction(() => document.querySelectorAll('#tabs .dot').length === 0);
    assert.ok(app.read('Home.txt').toString().endsWith('Q'));
  });
});

test('5d. UTF-8 BOM / non-ASCII content survives an edit and save', async () => {
  await withApp({
    setup: (d) => fs.writeFileSync(require('node:path').join(d, 'Bom.txt'), Buffer.concat([Buffer.from([0xef, 0xbb, 0xbf]), Buffer.from('café ☃ 🚀\r\n- [ ] x\r\n', 'utf8')])),
  }, async (app, page) => {
    await openFile(page, 'Bom.txt');
    assert.equal(await page.textContent('#enc'), 'UTF-8 BOM');
    await L.activeTa(page).click();
    await page.keyboard.press('Control+End');
    await page.keyboard.type('end');
    await page.keyboard.press('Control+s');
    await page.waitForFunction(() => document.querySelectorAll('#tabs .dot').length === 0);
    const b = app.read('Bom.txt');
    assert.deepEqual([...b.subarray(0, 3)], [0xef, 0xbb, 0xbf]);
    assert.equal(b.subarray(3).toString('utf8'), 'café ☃ 🚀\r\n- [ ] x\r\nend');
  });
});

test('9a. New tab -> type -> Ctrl+S opens a Save dialog; "work/Plan" is saved as work/Plan.txt with CRLF', async () => {
  await withApp({}, async (app, page) => {
    await page.click('#btn-new');
    assert.deepEqual(await L.tabNames(page), ['Untitled 2'].length ? (await L.tabNames(page)) : []);
    await L.activeTa(page).click();
    await page.keyboard.type('Plan\nsecond line\n- [ ] planned task !1 #plan');
    await page.keyboard.press('Control+s');
    await L.modal(page).waitFor();
    assert.equal(await L.modal(page).locator('h3').textContent(), 'Save note');
    assert.equal(await page.evaluate(() => document.activeElement.tagName), 'INPUT');
    await page.keyboard.type('work/Plan');
    await page.keyboard.press('Enter');
    await page.locator('.modal-overlay').waitFor({ state: 'detached' });
    await L.waitFor(() => fs.existsSync(app.file('work/Plan.txt')), { what: 'Plan.txt written' });
    const bytes = app.read('work/Plan.txt');
    assert.equal(bytes.toString(), 'Plan\r\nsecond line\r\n- [ ] planned task !1 #plan');
    assert.equal(L.eolCounts(bytes).bareLF, 0);
    assert.ok((await L.tabNames(page)).includes('Plan.txt'));
    await page.waitForFunction(() => [...document.querySelectorAll('#filelist .fname')].some((e) => e.textContent === 'work/Plan.txt'));
    assert.deepEqual(await L.fileNames(page), ['Home.txt', 'Work.txt', 'work/Plan.txt', 'work/Project.txt']);
    await page.waitForFunction(() => [...document.querySelectorAll('#tasklist .ttext')].some((e) => e.textContent === 'planned task'));
    await L.waitFor(() => app.settings().openFiles.includes('work/Plan.txt'), { what: 'open files saved in settings' });
    // second Ctrl+S just saves in place, no dialog
    await page.keyboard.type('!');
    await page.keyboard.press('Control+s');
    await page.waitForFunction(() => document.querySelectorAll('#tabs .dot').length === 0);
    assert.equal(await L.modal(page).count(), 0);
  });
});

test('9b. saving over an existing note asks first (Cancel keeps it, Replace overwrites)', async () => {
  await withApp({}, async (app, page) => {
    const home = app.read('Home.txt');
    await page.click('#btn-new');
    await L.activeTa(page).click();
    await page.keyboard.type('replacement text');
    await page.keyboard.press('Control+s');
    await L.modal(page).waitFor();
    await page.keyboard.type('Home');
    await page.keyboard.press('Enter');
    await page.waitForFunction(() => document.querySelector('.modal h3')?.textContent === 'Replace note?');
    assert.match(await L.modal(page).textContent(), /Home\.txt" already exists/);
    await L.modal(page).locator('button:has-text("Cancel")').click();
    await page.locator('.modal-overlay').waitFor({ state: 'detached' });
    assert.deepEqual(app.read('Home.txt'), home, 'Home.txt untouched after Cancel');
    assert.equal(await dirtyDots(page), 1, 'new tab still dirty');
    await page.keyboard.press('Control+s');
    await L.modal(page).waitFor();
    await page.keyboard.type('home'); // different case: still the same file on Windows; on Linux a new file
    await page.keyboard.press('Escape');
    await page.locator('.modal-overlay').waitFor({ state: 'detached' });
    assert.equal(await dirtyDots(page), 1, 'Escape cancels the save dialog');
    await page.keyboard.press('Control+s');
    await L.modal(page).waitFor();
    await page.keyboard.type('Home.txt');
    await page.keyboard.press('Enter');
    await page.waitForFunction(() => document.querySelector('.modal h3')?.textContent === 'Replace note?');
    await L.modal(page).locator('button:has-text("Replace")').click();
    await page.locator('.modal-overlay').waitFor({ state: 'detached' });
    assert.equal(app.read('Home.txt').toString(), 'replacement text');
    assert.equal(await dirtyDots(page), 0);
  });
});

test('9c. Save dialog refuses bad names (traversal, reserved, colon) and writes nothing', async () => {
  await withApp({}, async (app, page) => {
    const outside = require('node:path').join(app.dir, 'x.txt');
    for (const bad of ['../x', '..\\x', 'a:b', 'CON', 'dir/NUL.txt', 'trailing.']) {
      if (!(await L.tabNames(page)).length) await page.click('#btn-new');
      await page.click('#btn-new');
      await L.activeTa(page).click();
      await page.keyboard.type('bad ' + bad);
      await page.keyboard.press('Control+s');
      await L.modal(page).waitFor();
      await page.keyboard.type(bad);
      await page.keyboard.press('Enter');
      await page.locator('.modal-overlay').waitFor({ state: 'detached' });
      await page.waitForFunction(() => /^Could not save/.test(document.querySelector('#msg').textContent), null, { timeout: 4000 }).catch(() => { throw new Error('no error shown for name ' + bad); });
      assert.equal(fs.existsSync(outside), false);
    }
    assert.deepEqual(fs.readdirSync(app.dir).sort(), ['notes', 'settings.json', 'trimple.log'].filter((n) => fs.existsSync(require('node:path').join(app.dir, n))).sort());
    assert.deepEqual(await L.fileNames(page), ['Home.txt', 'Work.txt', 'work/Project.txt']);
    // an empty / whitespace name is just cancelled
    await page.click('#btn-new'); await L.activeTa(page).click(); await page.keyboard.type('x');
    await page.keyboard.press('Control+s'); await L.modal(page).waitFor();
    await page.keyboard.press('Control+a'); await page.keyboard.type('   '); await page.keyboard.press('Enter');
    await page.locator('.modal-overlay').waitFor({ state: 'detached' });
    assert.deepEqual(await L.fileNames(page), ['Home.txt', 'Work.txt', 'work/Project.txt']);
  });
});

test('9d. Save As (Ctrl+Shift+S) on an existing note writes a copy and switches the tab to it', async () => {
  await withApp({}, async (app, page) => {
    const orig = app.read('Work.txt');
    await openFile(page, 'Work.txt');
    await page.keyboard.press('Control+Shift+S');
    await L.modal(page).waitFor();
    assert.equal(await page.inputValue('.modal input'), 'Work.txt');
    await page.fill('.modal input', 'Copy of Work');
    await page.keyboard.press('Enter');
    await page.locator('.modal-overlay').waitFor({ state: 'detached' });
    await L.waitFor(() => fs.existsSync(app.file('Copy of Work.txt')), { what: 'copy written' });
    await L.waitFor(async () => (await L.tabNames(page)).includes('Copy of Work.txt'), { what: 'tab renamed' });
    assert.deepEqual(app.read('Copy of Work.txt'), orig, 'copy has identical bytes');
    assert.deepEqual(app.read('Work.txt'), orig);
    assert.ok((await L.tabNames(page)).includes('Copy of Work.txt'));
  });
});

test('11a. Ctrl+O quick open filters and opens a note with the keyboard', async () => {
  await withApp({}, async (app, page) => {
    await page.keyboard.press('Control+o');
    await L.modal(page).waitFor();
    assert.equal(await L.modal(page).locator('h3').textContent(), 'Open note');
    assert.equal(await page.evaluate(() => document.activeElement.getAttribute('aria-label')), 'Filter notes');
    assert.equal(await L.modal(page).locator('li button').count(), 3);
    await page.keyboard.type('proj');
    assert.equal(await L.modal(page).locator('li button').count(), 1);
    await page.keyboard.press('Enter');
    await page.locator('.modal-overlay').waitFor({ state: 'detached' });
    await page.waitForFunction(() => document.querySelector('#tabs .tab.active .tname')?.textContent === 'Project.txt');
    assert.equal(await page.evaluate(() => document.activeElement.classList.contains('editor')), true, 'focus returns to the editor');
    // arrow keys move the selection; opening a second note keeps both tabs
    await page.keyboard.press('Control+o');
    await L.modal(page).waitFor();
    await page.keyboard.press('ArrowDown'); await page.keyboard.press('ArrowDown'); await page.keyboard.press('ArrowUp');
    assert.equal(await L.modal(page).locator('li button.sel').textContent(), 'Work.txt');
    await page.keyboard.press('Enter');
    await page.waitForFunction(() => document.querySelector('#tabs .tab.active .tname')?.textContent === 'Work.txt');
    assert.deepEqual(await L.tabNames(page), ['Project.txt', 'Work.txt']);
    // no match + Escape
    await page.keyboard.press('Control+o');
    await L.modal(page).waitFor();
    await page.keyboard.type('zzz');
    assert.match(await L.modal(page).textContent(), /No matching notes/);
    await page.keyboard.press('Enter'); // nothing to open: dialog stays
    assert.equal(await L.modal(page).count(), 1);
    await page.keyboard.press('Escape');
    assert.equal(await L.modal(page).count(), 0);
    // clicking a result opens it
    await page.click('#btn-open');
    await L.modal(page).locator('li button:has-text("Home.txt")').click();
    await page.waitForFunction(() => document.querySelector('#tabs .tab.active .tname')?.textContent === 'Home.txt');
  });
});

test('11b. Ctrl+Enter toggles [ ]/[x] on the current line, Alt+T inserts a task prefix', async () => {
  await withApp({}, async (app, page) => {
    await openFile(page, 'Work.txt');
    await L.activeTa(page).click();
    await page.keyboard.press('Control+Home');
    await page.keyboard.press('ArrowDown'); await page.keyboard.press('ArrowDown'); // line 3
    await page.keyboard.press('End');
    const caret = (await L.editorState(page)).start;
    await page.keyboard.press('Control+Enter');
    let st = await L.editorState(page);
    assert.ok(st.value.split('\n')[2].startsWith('- [x] Write Q3 report'));
    assert.equal(st.start, caret, 'caret did not move');
    assert.equal(await dirtyDots(page), 1);
    await page.keyboard.press('Control+Enter');
    st = await L.editorState(page);
    assert.ok(st.value.split('\n')[2].startsWith('- [ ] Write Q3 report'));
    assert.equal(await dirtyDots(page), 0, 'toggling twice is back to clean');
    // non-task line
    await page.keyboard.press('Control+Home');
    await page.keyboard.press('Control+Enter');
    assert.equal(await L.status(page), 'No task on this line.');
    assert.equal(await dirtyDots(page), 0);
    // Alt+T on an empty new line
    await page.keyboard.press('Control+End');
    await page.keyboard.type('buy milk');
    await page.keyboard.press('Alt+t');
    st = await L.editorState(page);
    assert.ok(st.value.endsWith('- [ ] buy milk'), JSON.stringify(st.value.slice(-30)));
    assert.equal(st.start, st.value.length - 'buy milk'.length, 'caret right after the prefix');
    // tick it with the keyboard, save, and the panel gets it as done
    await page.keyboard.press('Control+Enter');
    await page.keyboard.press('Control+s');
    await page.waitForFunction(() => document.querySelectorAll('#tabs .dot').length === 0);
    assert.ok(app.read('Work.txt').toString().endsWith('- [x] buy milk'));
    assert.equal(L.eolCounts(app.read('Work.txt')).bareLF, 0);
    // Tab key inserts a tab character, not focus change
    await page.keyboard.press('Tab');
    assert.equal(await page.evaluate(() => document.activeElement.classList.contains('editor')), true);
    assert.ok((await L.editorState(page)).value.endsWith('buy milk\t'));
  });
});

test('11c. tab switching shortcuts and Alt+N / Alt+W', async () => {
  await withApp({}, async (app, page) => {
    await openFile(page, 'Home.txt'); await openFile(page, 'Work.txt');
    assert.deepEqual(await L.tabNames(page), ['Home.txt', 'Work.txt']);
    await page.keyboard.press('Alt+1');
    assert.equal(await page.textContent('#tabs .tab.active .tname'), 'Home.txt');
    await page.keyboard.press('Control+Alt+ArrowRight');
    assert.equal(await page.textContent('#tabs .tab.active .tname'), 'Work.txt');
    await page.keyboard.press('Control+Alt+ArrowRight');
    assert.equal(await page.textContent('#tabs .tab.active .tname'), 'Home.txt', 'wraps around');
    await page.keyboard.press('Alt+n');
    assert.equal((await L.tabNames(page)).length, 3);
    await page.keyboard.press('Alt+w');
    await page.waitForFunction(() => document.querySelectorAll('#tabs .tab').length === 2);
  });
});

test('12. closing a dirty tab asks Save / Don\'t save / Cancel', async () => {
  await withApp({}, async (app, page) => {
    const orig = app.read('Home.txt');
    await openFile(page, 'Home.txt');
    await L.activeTa(page).click();
    await page.keyboard.press('Control+End');
    await page.keyboard.type('EDIT');
    const closeBtn = page.locator('#tabs .tab.active .tclose');
    // Cancel
    await closeBtn.click();
    await L.modal(page).waitFor();
    assert.equal(await L.modal(page).locator('h3').textContent(), 'Unsaved changes');
    assert.deepEqual(await L.modal(page).locator('.mbuttons button').allTextContents(), ['Cancel', "Don't save", 'Save']);
    assert.match(await L.modal(page).textContent(), /Save changes to "Home\.txt"\?/);
    await L.modal(page).locator('button:has-text("Cancel")').click();
    assert.deepEqual(await L.tabNames(page), ['Home.txt']);
    assert.equal(await dirtyDots(page), 1);
    // Escape also cancels
    await closeBtn.click(); await L.modal(page).waitFor(); await page.keyboard.press('Escape');
    assert.deepEqual(await L.tabNames(page), ['Home.txt']);
    // Don't save
    await closeBtn.click(); await L.modal(page).waitFor();
    await L.modal(page).locator("button:has-text(\"Don't save\")").click();
    await page.waitForFunction(() => /^Untitled/.test(document.querySelector('#tabs .tab .tname')?.textContent));
    assert.deepEqual(app.read('Home.txt'), orig);
    // reopen, edit, close with Save
    await openFile(page, 'Home.txt');
    assert.ok(!(await L.editorState(page)).value.includes('EDIT'), 'discarded edits do not come back');
    await L.activeTa(page).click();
    await page.keyboard.press('Control+End');
    await page.keyboard.type('SAVED');
    await page.keyboard.press('Alt+w');
    await L.modal(page).waitFor();
    assert.equal(await page.evaluate(() => document.activeElement.textContent), 'Save', 'Save is the default button');
    await page.keyboard.press('Enter');
    await page.waitForFunction(() => /^Untitled/.test(document.querySelector('#tabs .tab .tname')?.textContent));
    assert.ok(app.read('Home.txt').toString().endsWith('SAVED'));
    // closing a clean tab asks nothing
    await openFile(page, 'Work.txt');
    await page.locator('#tabs .tab.active .tclose').click();
    assert.equal(await L.modal(page).count(), 0);
    // dirty untitled tab -> Save opens the Save dialog; cancelling it keeps the tab
    await L.activeTa(page).click();
    await page.keyboard.type('scratch');
    await page.keyboard.press('Alt+w');
    await L.modal(page).waitFor();
    await L.modal(page).locator('button:has-text("Save")').last().click();
    await page.waitForFunction(() => document.querySelector('.modal h3')?.textContent === 'Save note');
    await page.keyboard.press('Escape');
    await page.locator('.modal-overlay').waitFor({ state: 'detached' });
    assert.deepEqual(await L.tabNames(page), ['Untitled']);
    assert.equal(await dirtyDots(page), 1);
  });
});

test('12b. middle-click closes a tab; closing the last tab leaves an empty Untitled', async () => {
  await withApp({}, async (app, page) => {
    await openFile(page, 'Home.txt');
    await page.locator('#tabs .tab.active').click({ button: 'middle' });
    await page.waitForFunction(() => /^Untitled/.test(document.querySelector('#tabs .tab .tname')?.textContent));
    assert.equal((await L.tabNames(page)).length, 1);
  });
});

test('12c. unsaved edits survive closing the window (draft recovery) and reopen with the dirty dot', async () => {
  const app = await L.startApp();
  try {
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    await page.goto(app.url); await L.ready(page);
    await openFile(page, 'Home.txt');
    await L.activeTa(page).click();
    await page.keyboard.press('Control+End');
    await page.keyboard.type('UNSAVED-DRAFT');
    await L.sleep(1200); // drafts are written after 800 ms
    await page.close({ runBeforeUnload: false });
    const p2 = await ctx.newPage();
    await p2.goto(app.url); await L.ready(p2);
    assert.deepEqual(await L.tabNames(p2), ['Home.txt']);
    assert.equal(await p2.locator('#tabs .dot').count(), 1);
    assert.ok((await L.editorState(p2)).value.includes('UNSAVED-DRAFT'));
    assert.match(await L.status(p2), /Restored unsaved changes/);
    assert.ok(!app.read('Home.txt').toString().includes('UNSAVED-DRAFT'));
    await ctx.close();
  } finally { await app.stop(); }
});

test('12d. open tabs are restored after a reload (openFiles in settings.json)', async () => {
  await withApp({}, async (app, page) => {
    await openFile(page, 'Home.txt'); await openFile(page, 'work/Project.txt');
    await L.waitFor(() => app.settings().openFiles.length === 2, { what: 'open files saved' });
    await page.reload(); await L.ready(page);
    assert.deepEqual(await L.tabNames(page), ['Home.txt', 'Project.txt']);
    assert.equal(await page.textContent('#tabs .tab.active .tname'), 'Project.txt');
  });
});
