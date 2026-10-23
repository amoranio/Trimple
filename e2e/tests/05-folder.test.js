'use strict';
// (10) folder chooser.
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
const listItems = (page) => L.modal(page).locator('ul.pick li button').allTextContents();

function makeOther(app) {
  const other = path.join(app.dir, 'other');
  fs.mkdirSync(path.join(other, 'sub', 'deep'), { recursive: true });
  fs.mkdirSync(path.join(other, '.hidden'));
  fs.writeFileSync(path.join(other, 'Other.txt'), '- [ ] other folder task !1 #o\r\n- [ ] second other #o\r\n');
  fs.writeFileSync(path.join(other, 'sub', 'Deep.txt'), '- [x] done one\n');
  return other;
}

test('10a. Folder button opens a chooser that lists directories; switching folders updates tasks', async () => {
  await withApp({}, async (app, page) => {
    const other = makeOther(app);
    await page.click('#btn-folder');
    await L.modal(page).waitFor();
    assert.equal(await L.modal(page).locator('h3').textContent(), 'Change notes folder');
    assert.equal(await L.modal(page).locator('input[aria-label="Folder path"]').inputValue(), app.notes);
    await page.waitForFunction(() => document.querySelectorAll('.modal ul.pick li').length > 0);
    const items = await listItems(page);
    assert.deepEqual(items, ['.. (up one level)', 'work']);
    // navigate with the mouse: up one level, then into "other" and "sub"
    await L.modal(page).locator('li button', { hasText: '.. (up one level)' }).click();
    await page.waitForFunction((d) => document.querySelector('.modal input').value === d, app.dir);
    assert.deepEqual((await listItems(page)).sort(), ['.. (up one level)', 'notes', 'other']);
    await L.modal(page).locator('li button', { hasText: /^other$/ }).click();
    await page.waitForFunction((d) => document.querySelector('.modal input').value === d, other);
    assert.deepEqual(await listItems(page), ['.. (up one level)', 'sub'], 'hidden folders are not listed');
    await L.modal(page).locator('li button', { hasText: /^sub$/ }).click();
    await page.waitForFunction((d) => document.querySelector('.modal input').value === d, path.join(other, 'sub'));
    await L.modal(page).locator('button', { hasText: '.. (up one level)' }).click();
    await page.waitForFunction((d) => document.querySelector('.modal input').value === d, other);
    await L.modal(page).locator('button:has-text("Use this folder")').click();
    await page.locator('.modal-overlay').waitFor({ state: 'detached' });
    await page.waitForFunction(() => [...document.querySelectorAll('#tasklist .ttext')].some((e) => e.textContent === 'other folder task'));
    assert.deepEqual(await L.tasksText(page), ['other folder task', 'second other']);
    assert.deepEqual(await L.fileNames(page), ['Other.txt', 'sub/Deep.txt']);
    assert.equal(await page.textContent('#folderpath'), other);
    assert.equal(await page.textContent('#counts'), '2 open · 1 high · 0 med · 0 low');
    assert.match(await L.status(page), /Notes folder: /);
    await L.waitFor(() => app.settings().folder === other, { what: 'folder saved in settings.json' });
    // typing a path + Enter works too, and going back
    await page.click('#btn-folder');
    await L.modal(page).waitFor();
    await page.fill('.modal input[aria-label="Folder path"]', app.notes);
    await page.keyboard.press('Enter');
    await page.waitForFunction(() => [...document.querySelectorAll('.modal ul.pick li button')].some((b) => b.textContent === 'work'));
    await L.modal(page).locator('button:has-text("Use this folder")').click();
    await page.waitForFunction(() => document.querySelectorAll('#tasklist li.task').length === 7);
    assert.deepEqual(await L.fileNames(page), ['Home.txt', 'Work.txt', 'work/Project.txt']);
  });
});

test('10b. an unsaved tab blocks the folder switch; Cancel and Escape change nothing', async () => {
  await withApp({}, async (app, page) => {
    makeOther(app);
    await page.click('#filelist button[title="Home.txt"]');
    await page.waitForSelector('#tabs .tab.active .tname:text("Home.txt")');
    await L.activeTa(page).click();
    await page.keyboard.press('Control+End');
    await page.keyboard.type('x');
    await page.click('#btn-folder');
    await page.waitForFunction(() => /before changing the notes folder/.test(document.querySelector('#msg').textContent));
    assert.match(await L.status(page), /Save or close "Home\.txt"/);
    assert.equal(await L.modal(page).count(), 0, 'no chooser opened');
    assert.equal(await page.textContent('#folderpath'), app.notes);
    // save, then it works; Escape from the chooser changes nothing
    await page.keyboard.press('Control+s');
    await page.waitForFunction(() => document.querySelectorAll('#tabs .dot').length === 0);
    await page.click('#btn-folder');
    await L.modal(page).waitFor();
    await page.keyboard.press('Escape');
    assert.equal(await L.modal(page).count(), 0);
    assert.equal(await page.textContent('#folderpath'), app.notes);
    assert.deepEqual(await L.tabNames(page), ['Home.txt']);
  });
});

test('10c. an unsaved *Untitled* tab survives a folder switch; clean tabs from the old folder are closed', async () => {
  await withApp({}, async (app, page) => {
    const other = makeOther(app);
    await page.click('#filelist button[title="Home.txt"]');
    await page.waitForSelector('#tabs .tab.active .tname:text("Home.txt")');
    await page.click('#btn-new');
    await L.activeTa(page).click();
    await page.keyboard.type('scratch text');
    await page.click('#btn-folder');
    await L.modal(page).waitFor();
    await page.fill('.modal input[aria-label="Folder path"]', other);
    await page.keyboard.press('Enter');
    await page.waitForFunction(() => [...document.querySelectorAll('.modal ul.pick li button')].some((b) => b.textContent === 'sub'));
    await L.modal(page).locator('button:has-text("Use this folder")').click();
    await page.waitForFunction(() => [...document.querySelectorAll('#tasklist .ttext')].some((e) => e.textContent === 'second other'));
    const tabs = await L.tabNames(page);
    assert.ok(!tabs.includes('Home.txt'), 'old-folder tab closed: ' + tabs);
    const values = await page.$$eval('textarea.editor', (l) => l.map((t) => t.value));
    assert.ok(values.includes('scratch text'), 'untitled draft kept');
    // saving the scratch tab now lands in the NEW folder
    await page.locator('#tabs .tab', { hasText: 'Untitled' }).filter({ has: page.locator('.dot') }).click();
    await page.keyboard.press('Control+s');
    await L.modal(page).waitFor();
    await page.keyboard.type('scratch');
    await page.keyboard.press('Enter');
    await L.waitFor(() => fs.existsSync(path.join(other, 'scratch.txt')), { what: 'scratch.txt in the new folder' });
    assert.equal(fs.existsSync(app.file('scratch.txt')), false);
  });
});

test('10d. bad folder paths are rejected with a message and nothing changes', async () => {
  await withApp({}, async (app, page) => {
    await page.click('#btn-folder');
    await L.modal(page).waitFor();
    await page.fill('.modal input[aria-label="Folder path"]', path.join(app.dir, 'does-not-exist'));
    await L.modal(page).locator('button:has-text("Use this folder")').click();
    await page.waitForFunction(() => /^Could not use that folder/.test(document.querySelector('#msg').textContent));
    assert.equal(await page.textContent('#folderpath'), app.notes);
    assert.equal(await page.locator('#tasklist li.task').count(), 7);
    await page.click('#btn-folder');
    await L.modal(page).waitFor();
    await page.fill('.modal input[aria-label="Folder path"]', 'relative/path');
    await page.keyboard.press('Enter');
    await L.sleep(300);
    await page.keyboard.press('Escape');
    // "Go" to a nonexistent folder: shows the error in the status bar, dialog stays usable
    await page.click('#btn-folder');
    await L.modal(page).waitFor();
    await page.fill('.modal input[aria-label="Folder path"]', path.join(app.dir, 'nope'));
    await L.modal(page).locator('button', { hasText: /^Go$/ }).click();
    await page.waitForFunction(() => /cannot open that folder/.test(document.querySelector('#msg').textContent));
    await page.keyboard.press('Escape');
    assert.equal(await L.modal(page).count(), 0);
  });
});

test('10e. starting with no folder (no -folder, empty settings) forces the "Choose your notes folder" dialog', async () => {
  const app = await L.startApp({ noFolder: true });
  try {
    const page = await L.openPage(browser, app, { wait: false });
    await L.modal(page).waitFor();
    assert.equal(await L.modal(page).locator('h3').textContent(), 'Choose your notes folder');
    assert.deepEqual(await L.modal(page).locator('.mbuttons button').allTextContents().then((a) => a.map((x) => x.replace(/".*"/, '"X"'))), ['Create "X" in Documents', 'Use this folder']);
    // Escape must not dismiss the first-run dialog into a broken state
    await page.keyboard.press('Escape');
    await L.sleep(300);
    // choose a folder
    const dir = path.join(app.dir, 'chosen'); fs.mkdirSync(dir);
    fs.writeFileSync(path.join(dir, 'A.txt'), '- [ ] chosen task\r\n');
    if (await L.modal(page).count() === 0) {
      console.log('# first-run dialog was dismissable with Escape; reopening via Folder button');
      await page.click('#btn-folder');
      await L.modal(page).waitFor();
    }
    await page.fill('.modal input[aria-label="Folder path"]', dir);
    await L.modal(page).locator('button:has-text("Use this folder")').click();
    await page.waitForFunction(() => [...document.querySelectorAll('#tasklist .ttext')].some((e) => e.textContent === 'chosen task'));
    await L.ready(page);
    assert.deepEqual(page.errors, []);
  } finally { await app.stop(); }
});
