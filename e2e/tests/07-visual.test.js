'use strict';
// Screenshots (e2e/test-results/*.png) plus automatic layout sanity checks, and (15) a whole-session
// check that nothing logged a console error or threw an uncaught exception.
const { test, before, after } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const L = require('../lib');

let browser;
before(async () => { browser = await L.launchBrowser(); fs.mkdirSync(L.RESULTS, { recursive: true }); });
after(async () => { await browser.close(); });

const shot = (page, name) => page.screenshot({ path: path.join(L.RESULTS, name + '.png') });

// Elements whose box sticks out of their scroll container / the viewport are layout bugs.
async function layoutProblems(page) {
  return page.evaluate(() => {
    const out = [];
    const vw = document.documentElement.clientWidth, vh = document.documentElement.clientHeight;
    if (document.documentElement.scrollWidth > vw + 1) out.push(`page scrolls horizontally: ${document.documentElement.scrollWidth} > ${vw}`);
    if (document.documentElement.scrollHeight > vh + 1) out.push(`page scrolls vertically: ${document.documentElement.scrollHeight} > ${vh}`);
    for (const sel of ['#toolbar', '#side', '#work', '#status', '#tabs']) {
      const e = document.querySelector(sel);
      if (!e || e.hidden) continue;
      const r = e.getBoundingClientRect();
      if (r.right > vw + 1 || r.bottom > vh + 1) out.push(`${sel} exceeds the viewport (${Math.round(r.right)}x${Math.round(r.bottom)} vs ${vw}x${vh})`);
    }
    const tb = document.querySelector('#toolbar');
    if (tb.scrollWidth > tb.clientWidth + 1) out.push(`toolbar content overflows: ${tb.scrollWidth} > ${tb.clientWidth}`);
    const btns = [...document.querySelectorAll('#toolbar button')].filter((b) => !b.hidden);
    for (const b of btns) { const r = b.getBoundingClientRect(); if (r.right > vw + 1) out.push(`toolbar button "${b.textContent}" off-screen`); if (b.scrollWidth > b.clientWidth + 1) out.push(`button "${b.textContent}" text clipped`); }
    const rows = [...document.querySelectorAll('#tasklist li.task, #filelist li')];
    for (const li of rows) { if (li.scrollWidth > li.clientWidth + 1) out.push('row overflows: ' + li.textContent.slice(0, 40)); }
    return out;
  });
}

// contrast ratio of a computed colour pair
async function contrast(page, fgSel, bgSel) {
  return page.evaluate(([fgSel, bgSel]) => {
    const parse = (c) => { const m = c.match(/[\d.]+/g).map(Number); return m.slice(0, 3); };
    const lum = ([r, g, b]) => { const f = (v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; }; return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b); };
    if (!document.querySelector(fgSel) || !document.querySelector(bgSel)) return 99;
    const fg = parse(getComputedStyle(document.querySelector(fgSel)).color);
    let el = document.querySelector(bgSel), bg;
    while (el) { const c = getComputedStyle(el).backgroundColor; if (!/rgba\(0, 0, 0, 0\)|transparent/.test(c)) { bg = parse(c); break; } el = el.parentElement; }
    const a = lum(fg), b = lum(bg);
    return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
  }, [fgSel, bgSel]);
}

test('screenshots: dark and light, panel + open note, plus layout/contrast checks', async () => {
  const app = await L.startApp({
    setup: (d) => {
      fs.writeFileSync(path.join(d, 'Long title note.txt'), '# A long note\r\n\r\n' + Array.from({ length: 40 }, (_, i) => `- [ ] Item ${i} with some words to wrap around the pane ${'blah '.repeat(i % 6)}!${(i % 3) + 1} #l${i % 4}`).join('\r\n') + '\r\n');
      fs.writeFileSync(path.join(d, 'Edge.txt'), '- [ ] ' + 'VeryLongUnbrokenWord'.repeat(12) + ' !1 #verylonglabelname #another #third\r\n- [x] finished thing #done\r\n');
      fs.writeFileSync(path.join(d, 'binary.txt'), Buffer.from('- [ ] a\u0000b\r\n'));
    },
  });
  const issues = [];
  try {
    const page = await L.openPage(browser, app);
    await shot(page, '01-dark-startup');
    issues.push(...(await layoutProblems(page)).map((x) => 'dark startup: ' + x));
    await page.click('#tasklist .ttext:has-text("Item 3 ")');
    await page.waitForSelector('#tabs .tab.active .tname:text("Long title note.txt")');
    await L.sleep(150);
    await shot(page, '02-dark-note-open');
    issues.push(...(await layoutProblems(page)).map((x) => 'dark note: ' + x));
    await page.check('#showdone');
    await page.selectOption('#group', 'label');
    await L.sleep(500);
    await shot(page, '03-dark-by-label-done');
    await page.click('#btn-theme');
    await L.sleep(200);
    await page.selectOption('#group', 'priority');
    await L.sleep(400);
    await shot(page, '04-light-note-open');
    issues.push(...(await layoutProblems(page)).map((x) => 'light note: ' + x));
    await page.click('#filelist button:has(.fname:text-is("Edge.txt"))');
    await L.sleep(150);
    // contrast checks on key text in both themes
    for (const theme of ['light', 'dark']) {
      if ((await page.getAttribute('html', 'data-theme')) !== theme) { await page.click('#btn-theme'); await L.sleep(100); }
      for (const [fg, bg, min, what] of [['textarea.editor:not([hidden])', 'textarea.editor:not([hidden])', 4.5, 'editor text'], ['.task .ttext', '#tasklist', 4.5, 'task text'], ['h2', '#side', 4.5, 'section headings'],
        ['.muted', '#side', 4.5, 'muted text'], ['#status', '#status', 4.5, 'status bar'], ['#tabs .tab:not(.active)', '#tabs .tab:not(.active)', 4.5, 'inactive tab'], ['.chips', '#tasklist', 4.5, 'label chips'], ['#btn-save', '#btn-save', 4.5, 'toolbar button']]) {
        const c = await contrast(page, fg, bg);
        if (c < min) issues.push(`${theme}: ${what} contrast ${c.toFixed(2)} < ${min}`);
      }
    }
    // done tasks (strike-through grey) and priority colours
    await page.click('#btn-theme'); await L.sleep(100);
    const doneC = await page.evaluate(() => { const e = document.querySelector('.task.done .ttext'); return e ? getComputedStyle(e).color : null; });
    console.log('# done-task colour (' + (await page.getAttribute('html', 'data-theme')) + '): ' + doneC);
    // read-only file in light and dark
    await page.click('#filelist button:has(.fname:text-is("binary.txt"))');
    await L.sleep(200);
    await shot(page, '05-readonly-note');
    // modal screenshot
    await page.keyboard.press('Control+o');
    await L.modal(page).waitFor();
    await shot(page, '06-quick-open-modal');
    await page.keyboard.press('Escape');
    // narrow window
    await page.setViewportSize({ width: 760, height: 520 });
    await L.sleep(200);
    await shot(page, '07-narrow');
    issues.push(...(await layoutProblems(page)).map((x) => 'narrow 760x520: ' + x));
    await page.setViewportSize({ width: 560, height: 420 });
    await L.sleep(200);
    await shot(page, '08-very-narrow');
    issues.push(...(await layoutProblems(page)).map((x) => 'very narrow 560x420: ' + x));
    await page.setViewportSize({ width: 1920, height: 1080 });
    await page.click('#btn-new');
    await L.sleep(150);
    await shot(page, '09-wide-new-note');
    // zoomed fonts
    for (let i = 0; i < 12; i++) await page.click('#btn-bigger');
    await shot(page, '10-large-font');
    assert.deepEqual(page.errors, [], 'console errors / page exceptions');
  } finally { await app.stop(); }
  if (issues.length) console.log('# LAYOUT/CONTRAST FINDINGS:\n# ' + issues.join('\n# '));
  fs.writeFileSync(path.join(L.RESULTS, 'layout-findings.txt'), issues.join('\n') + '\n');
});

test('15. a long mixed session raises no console errors, page exceptions or native dialogs', async () => {
  const app = await L.startApp();
  try {
    const page = await L.openPage(browser, app);
    // lots of ordinary use
    await page.click('#tasklist .ttext >> nth=0');
    await page.keyboard.type(' typed');
    await page.keyboard.press('Control+s');
    await L.sleep(300);
    await page.click('#btn-theme'); await page.click('#btn-wrap'); await page.click('#btn-bigger');
    await page.selectOption('#group', 'file'); await page.fill('#search', 'a'); await page.check('#showdone');
    await L.sleep(400);
    await page.click('#btn-new'); await page.keyboard.type('abc'); await page.keyboard.press('Alt+w');
    await L.modal(page).locator('button:has-text("Don\'t save")').click();
    await page.keyboard.press('Control+o'); await page.keyboard.press('Escape');
    await page.click('#btn-folder'); await page.keyboard.press('Escape');
    app.write('Home.txt', 'changed outside\n');
    await L.sleep(2500);
    await page.keyboard.press('F5');
    await page.reload({ waitUntil: 'domcontentloaded' });
    await L.sleep(1500);
    assert.deepEqual(page.errors, [], 'console errors / page exceptions');
    assert.deepEqual(page.dialogs, [], 'native dialogs');
    console.log('# network-level 4xx logged by the browser during the tour: ' + JSON.stringify(page.netNoise));
  } finally { await app.stop(); }
});
