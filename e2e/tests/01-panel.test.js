'use strict';
// (1) startup, (2) grouping/filtering/search/show completed, (8) theme / wrap / font persistence.
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
    assert.deepEqual(page.dialogs, [], 'native dialogs');
  } finally { await app.stop(); }
}

test('1. startup shows 7 open tasks in 4 priority groups, dark theme, 3 files', async () => {
  await withApp({}, async (app, page) => {
    assert.equal(await page.getAttribute('html', 'data-theme'), 'dark');
    assert.equal(await page.title(), 'Untitled - Trimple');
    assert.equal(await page.textContent('#counts'), '7 open · 2 high · 3 med · 1 low');
    assert.deepEqual(await L.groupNames(page), ['High (!1)|2', 'Medium (!2)|3', 'Low (!3)|1', 'No priority|1']);
    assert.deepEqual(await L.tasksText(page), [
      'Write Q3 report', 'Draft API spec',
      'Renew parking pass', 'Review pull requests', 'Fix flaky build',
      'Book offsite venue', 'Buy printer ink']);
    assert.deepEqual(await L.fileNames(page), ['Home.txt', 'Work.txt', 'work/Project.txt']);
    assert.equal(await page.textContent('#filecount'), '3');
    assert.equal(await page.textContent('#folderpath'), app.notes);
    // the fenced-code "task" and the done tasks are not listed
    assert.equal(await page.locator('#tasklist', { hasText: 'code fence' }).count(), 0);
    assert.equal(await page.locator('#tasklist', { hasText: 'plumber' }).count(), 0);
    // one empty scratch tab, tasks panel visible, a colour contrast sanity check on body text
    assert.deepEqual(await L.tabNames(page), ['Untitled']);
    const bg = await page.$eval('body', (b) => getComputedStyle(b).backgroundColor);
    assert.notEqual(bg, 'rgba(0, 0, 0, 0)');
    // priority badges per file in the Files list
    assert.deepEqual(await page.$$eval('#filelist li', (l) => l.map((x) => x.textContent)), ['Home.txt2', 'Work.txt3', 'work/Project.txt2']);
  });
});

test('2a. group by label / file, label filter', async () => {
  await withApp({}, async (app, page) => {
    await page.selectOption('#group', 'label');
    await page.waitForFunction(() => document.querySelector('#tasklist .group span').textContent === '#admin');
    assert.deepEqual(await L.groupNames(page), ['#admin|1', '#dev|3', '#home|2', '#work|3']);
    // a task with two labels shows under both
    assert.equal(await page.locator('#tasklist .ttext', { hasText: 'Buy printer ink' }).count(), 2);

    await page.selectOption('#group', 'file');
    await page.waitForFunction(() => document.querySelector('#tasklist .group span').textContent === 'Home.txt');
    assert.deepEqual(await L.groupNames(page), ['Home.txt|2', 'Work.txt|3', 'work/Project.txt|2']);

    await page.selectOption('#group', 'priority');
    await page.selectOption('#label', 'dev');
    await page.waitForFunction(() => document.querySelectorAll('#tasklist li.task').length === 3);
    assert.deepEqual(await L.tasksText(page), ['Write Q3 report', 'Draft API spec', 'Review pull requests', 'Fix flaky build'].filter((t) => t !== 'Write Q3 report'));
    assert.deepEqual(await page.$$eval('#label option', (o) => o.map((x) => x.value)), ['', 'admin', 'dev', 'home', 'work']);
    await page.selectOption('#label', '');
    await page.waitForFunction(() => document.querySelectorAll('#tasklist li.task').length === 7);
    // the grouping choice is persisted in settings.json
    await L.waitFor(() => app.settings().groupBy === 'priority', { what: 'groupBy saved' });
  });
});

test('2b. search box and Show completed', async () => {
  await withApp({}, async (app, page) => {
    await page.fill('#search', 'report');
    await page.waitForFunction(() => document.querySelectorAll('#tasklist li.task').length === 1);
    assert.deepEqual(await L.tasksText(page), ['Write Q3 report']);
    // search also matches labels
    await page.fill('#search', '#dev');
    await page.waitForFunction(() => document.querySelectorAll('#tasklist li.task').length >= 2);
    const dev = await L.tasksText(page);
    assert.ok(dev.includes('Draft API spec'), 'label search: ' + dev);
    await page.fill('#search', 'zzzz-nothing');
    await page.waitForSelector('#tasklist li.empty');
    assert.match(await page.textContent('#tasklist li.empty'), /No tasks match/);
    await page.fill('#search', '');
    await page.waitForFunction(() => document.querySelectorAll('#tasklist li.task').length === 7);

    await page.check('#showdone');
    await page.waitForFunction(() => document.querySelectorAll('#tasklist li.task').length === 9);
    assert.equal(await page.locator('#tasklist li.task.done').count(), 2);
    assert.equal(await page.locator('#tasklist li.task.done input.tick:checked').count(), 2);
    // done tasks do not change the open counters
    assert.equal(await page.textContent('#counts'), '7 open · 2 high · 3 med · 1 low');
    await L.waitFor(() => app.settings().showDone === true, { what: 'showDone saved' });
    await page.uncheck('#showdone');
    await page.waitForFunction(() => document.querySelectorAll('#tasklist li.task').length === 7);
    // an empty folder says so
  });
});

test('2c. an empty notes folder shows helpful empty states', async () => {
  await withApp({ empty: true }, async (app, page) => {
    assert.match(await page.textContent('#tasklist li.empty'), /No tasks yet/);
    assert.match(await page.textContent('#filelist li.empty'), /No \.txt notes/);
    assert.equal(await page.textContent('#counts'), '0 open · 0 high · 0 med · 0 low');
  });
});

test('8a. theme button toggles data-theme and persists across reload via settings.json', async () => {
  await withApp({}, async (app, page) => {
    await page.click('#btn-theme');
    assert.equal(await page.getAttribute('html', 'data-theme'), 'light');
    await L.waitFor(() => app.settings().theme === 'light', { what: 'theme saved in settings.json' });
    const lightBg = await page.$eval('#work', (e) => getComputedStyle(e).backgroundColor);
    assert.equal(lightBg, 'rgb(255, 255, 255)');
    await page.reload();
    await L.ready(page);
    assert.equal(await page.getAttribute('html', 'data-theme'), 'light');
    await page.keyboard.press('Alt+d');
    assert.equal(await page.getAttribute('html', 'data-theme'), 'dark');
    await L.waitFor(() => app.settings().theme === 'dark', { what: 'theme saved again' });
    // the stored file is valid JSON with the sanitized fields
    assert.equal(app.settings().fontSize, 14);
  });
});

test('8b. word wrap and font size buttons work and persist', async () => {
  await withApp({}, async (app, page) => {
    const ta = () => L.editorState(page);
    assert.equal((await ta()).wrap, 'soft');
    await page.click('#btn-wrap');
    assert.equal((await ta()).wrap, 'off');
    assert.equal(await page.getAttribute('#btn-wrap', 'aria-pressed'), 'false');
    const fs0 = await page.$eval('textarea.editor', (e) => parseFloat(getComputedStyle(e).fontSize));
    assert.equal(fs0, 14);
    await page.click('#btn-bigger'); await page.click('#btn-bigger');
    assert.equal(await page.$eval('textarea.editor', (e) => parseFloat(getComputedStyle(e).fontSize)), 16);
    await page.click('#btn-smaller');
    assert.equal(await page.$eval('textarea.editor', (e) => parseFloat(getComputedStyle(e).fontSize)), 15);
    await page.keyboard.press('Control+=');
    await page.keyboard.press('Control+-');
    await page.keyboard.press('Control+-');
    assert.equal(await page.$eval('textarea.editor', (e) => parseFloat(getComputedStyle(e).fontSize)), 14);
    await page.keyboard.press('Control+0');
    for (let i = 0; i < 40; i++) await page.click('#btn-bigger');
    assert.equal(await page.$eval('textarea.editor', (e) => parseFloat(getComputedStyle(e).fontSize)), 40, 'font size is clamped at 40');
    for (let i = 0; i < 40; i++) await page.click('#btn-smaller');
    assert.equal(await page.$eval('textarea.editor', (e) => parseFloat(getComputedStyle(e).fontSize)), 8, 'font size is clamped at 8');
    await page.click('#btn-bigger'); // 9
    await L.waitFor(() => { const s = app.settings(); return s.fontSize === 9 && s.wordWrap === false; }, { what: 'font + wrap saved' });
    await page.reload(); await L.ready(page);
    assert.equal((await ta()).wrap, 'off');
    assert.equal(await page.$eval('textarea.editor', (e) => parseFloat(getComputedStyle(e).fontSize)), 9);
    // panel toggle (Ctrl+B) and button
    await page.click('#btn-panel');
    assert.equal(await page.isVisible('#side'), false);
    await page.keyboard.press('Control+b');
    assert.equal(await page.isVisible('#side'), true);
  });
});

test('8c. splitter resizes the panel (keyboard and mouse) and is clamped', async () => {
  await withApp({}, async (app, page) => {
    const w = () => page.$eval('#side', (e) => Math.round(e.getBoundingClientRect().width));
    assert.equal(await w(), 360);
    await page.focus('#splitter');
    await page.keyboard.press('ArrowRight'); await page.keyboard.press('ArrowRight');
    assert.equal(await w(), 400);
    const box = await page.locator('#splitter').boundingBox();
    await page.mouse.move(box.x + 2, box.y + 200);
    await page.mouse.down(); await page.mouse.move(500, box.y + 200, { steps: 5 }); await page.mouse.up();
    assert.ok(Math.abs((await w()) - 500) <= 3, 'dragged width ' + await w());
    await page.mouse.move(500, 300);
    await page.mouse.down(); await page.mouse.move(5, 300, { steps: 5 }); await page.mouse.up();
    assert.ok((await w()) >= 220, 'min width honoured: ' + await w());
    await L.waitFor(() => app.settings().panelWidth >= 220, { what: 'panel width saved' });
  });
});
