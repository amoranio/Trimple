'use strict';
(() => {
  // ------------------------------------------------------------------ helpers
  const $ = (sel, root = document) => root.querySelector(sel);

  function h(tag, props, ...kids) {
    const el = document.createElement(tag);
    for (const [k, v] of Object.entries(props || {})) {
      if (v === false || v == null) continue;
      if (k === 'class') el.className = v;
      else if (k === 'text') el.textContent = v;
      else if (k === 'dataset') Object.assign(el.dataset, v);
      else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
      else el.setAttribute(k, v === true ? '' : v);
    }
    for (const kid of kids.flat()) {
      if (kid == null || kid === false) continue;
      el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
    }
    return el;
  }

  const S = {
    token: '', os: '', canNotepad: false,
    settings: null, state: null, folder: '',
    docs: [], active: null, nextId: 1, untitled: 0,
    modal: false, stateSeq: 0, connected: false, refreshing: null, lastOwnWrite: 0, busyTask: false,
  };

  const params = new URLSearchParams(location.search);
  S.token = params.get('t') || sessionStorage.getItem('trimple.token') || '';
  try { sessionStorage.setItem('trimple.token', S.token); } catch (_) { /* private mode */ }
  const openAbs = params.getAll('openabs');
  history.replaceState(null, '', location.pathname);

  async function api(method, url, body) {
    const opts = { method, headers: { 'X-Trimple-Token': S.token }, cache: 'no-store' };
    if (body !== undefined) { opts.headers['Content-Type'] = 'application/json'; opts.body = JSON.stringify(body); }
    let res;
    try { res = await fetch(url, opts); }
    catch (e) { const err = new Error('Cannot reach Trimple. Was it closed?'); err.code = 'offline'; throw err; }
    let data = null;
    if ((res.headers.get('content-type') || '').includes('json')) { try { data = await res.json(); } catch (_) { /* ignore */ } }
    if (!res.ok) {
      const err = new Error((data && data.error) || res.statusText || 'request failed');
      err.status = res.status; err.code = data && data.code; err.data = data;
      throw err;
    }
    return data;
  }

  let sayTimer = 0;
  function say(msg, isErr) {
    const el = $('#msg');
    el.textContent = msg || '';
    el.className = isErr ? 'err' : '';
    clearTimeout(sayTimer);
    if (msg && !isErr) sayTimer = setTimeout(() => { if (el.textContent === msg) el.textContent = ''; }, 8000);
  }

  const debounce = (fn, ms) => { let t = 0; return (...a) => { clearTimeout(t); t = setTimeout(() => fn(...a), ms); }; };
  const baseName = (p) => p.split('/').pop();

  // ------------------------------------------------------------------ modal dialogs
  // Returns a promise for the id of the pressed button. `body` is a DOM node or text.
  function modal({ title, body, buttons, cancelId = 'cancel', focus, onKey, onOpen }) {
    return new Promise((resolve) => {
      S.modal = true;
      const prevFocus = document.activeElement;
      $('#app').inert = true; // nothing behind the dialog can be reached or typed into
      const close = (id) => {
        document.removeEventListener('keydown', keyHandler, true);
        overlay.remove();
        S.modal = false;
        $('#app').inert = false;
        if (prevFocus && prevFocus.focus) prevFocus.focus();
        resolve(id);
      };
      const keyHandler = (e) => {
        if (e.isComposing) return;
        if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(cancelId); }
        else if (onKey) onKey(e, close);
      };
      const bodyEl = h('div', { class: 'mbody' }, typeof body === 'string' ? h('p', { text: body }) : body);
      const btns = h('div', { class: 'mbuttons' }, buttons.map((b) =>
        h('button', { class: b.primary ? 'primary' : '', 'data-id': b.id, onclick: () => close(b.id) }, b.label)));
      const dlg = h('div', { class: 'modal', role: 'dialog', 'aria-modal': 'true', 'aria-label': title }, h('h3', { text: title }), bodyEl, btns);
      const overlay = h('div', { class: 'modal-overlay' }, dlg);
      $('#modal-root').append(overlay);
      document.addEventListener('keydown', keyHandler, true);
      const target = (focus && focus(dlg)) || $('button.primary', dlg) || $('button', dlg);
      if (target) target.focus();
      if (onOpen) onOpen(close);
    });
  }

  const ask = (title, message, buttons) => modal({ title, body: message, buttons });

  // ------------------------------------------------------------------ settings and appearance
  const saveSettingsSoon = debounce(async () => {
    if (!S.settings) return;
    try { await api('PUT', '/api/settings', settingsPayload()); }
    catch (e) { say('Could not save settings: ' + e.message, true); }
  }, 400);

  function settingsPayload() {
    const s = S.settings;
    return {
      folder: s.folder || '', theme: s.theme, fontSize: s.fontSize, wordWrap: s.wordWrap,
      panelVisible: s.panelVisible, panelWidth: s.panelWidth, groupBy: s.groupBy, showDone: s.showDone,
      openFiles: S.docs.filter((d) => d.path).map((d) => d.path),
    };
  }

  function applyAppearance() {
    const s = S.settings;
    document.documentElement.dataset.theme = s.theme;
    document.documentElement.style.setProperty('--side-w', s.panelWidth + 'px');
    document.body.classList.toggle('panel-hidden', !s.panelVisible);
    $('#btn-panel').setAttribute('aria-pressed', String(s.panelVisible));
    $('#btn-wrap').setAttribute('aria-pressed', String(s.wordWrap));
    $('#group').value = s.groupBy;
    $('#showdone').checked = s.showDone;
    for (const d of S.docs) styleEditor(d.ta);
    $('#splitter').setAttribute('aria-valuenow', String(s.panelWidth));
  }

  function styleEditor(ta) {
    ta.style.fontSize = S.settings.fontSize + 'px';
    ta.wrap = S.settings.wordWrap ? 'soft' : 'off';
  }

  function setFont(size) {
    S.settings.fontSize = Math.max(8, Math.min(40, size));
    applyAppearance(); saveSettingsSoon();
  }

  // ------------------------------------------------------------------ task panel
  function refreshState() {
    const seq = ++S.stateSeq;
    const wanted = $('#label').value;
    const q = new URLSearchParams({ group: $('#group').value, label: wanted, q: $('#search').value, done: $('#showdone').checked ? '1' : '0' });
    const p = (async () => {
      let st;
      try { st = await api('GET', '/api/state?' + q); }
      catch (e) { if (seq === S.stateSeq) say(e.message, true); return S.refreshing; }
      if (seq !== S.stateSeq) return S.refreshing; // a newer request is on its way: wait for it
      S.state = st;
      S.folder = st.folder;
      const focusKey = focusedTaskKey();
      renderLabels(); renderTasks(); renderFiles(); renderCounts();
      restoreFocus(focusKey);
      if (wanted && $('#label').value !== wanted) refreshStateSoon(); // that label no longer exists
      return undefined;
    })();
    S.refreshing = p;
    return p;
  }

  function focusedTaskKey() {
    const a = document.activeElement;
    const row = a && a.closest && a.closest('li.task');
    if (!row) return null;
    return { path: row.dataset.path, line: row.dataset.line, kind: a.classList.contains('tick') ? 'tick' : 'text' };
  }
  function restoreFocus(key) {
    if (!key || S.modal) return;
    for (const row of $('#tasklist').querySelectorAll('li.task')) {
      if (row.dataset.path === key.path && row.dataset.line === key.line) {
        const el = row.querySelector(key.kind === 'tick' ? 'input.tick' : 'button.ttext');
        if (el) el.focus();
        return;
      }
    }
  }
  const refreshStateSoon = debounce(refreshState, 120);

  function renderLabels() {
    const sel = $('#label');
    const prev = sel.value;
    sel.replaceChildren(h('option', { value: '' }, 'All labels'), ...S.state.labels.map((l) => h('option', { value: l }, '#' + l)));
    sel.value = S.state.labels.includes(prev) ? prev : '';
  }

  function renderCounts() {
    const c = S.state.counts;
    $('#counts').textContent = `${c.total} open · ${c.high} high · ${c.medium} med · ${c.low} low`;
    $('#folderpath').textContent = S.state.folder;
    $('#folderpath').title = S.state.folder;
    const extra = S.state.truncated ? ' (first 2000 only)' : '';
    $('#filecount').textContent = S.state.files.length + extra;
    const bar = $('#banner');
    if (S.state.truncated) { bar.hidden = false; bar.textContent = 'This folder has more than 2000 notes; only the first 2000 are scanned. Choose a smaller folder.'; }
    else if (S.connected) bar.hidden = true;
  }

  function renderTasks() {
    const ul = $('#tasklist');
    ul.replaceChildren();
    for (const g of S.state.groups) {
      ul.append(h('li', { class: 'group' }, h('span', { text: g.name }), h('span', { text: String(g.open) })));
      for (const t of g.tasks) ul.append(taskRow(t));
    }
    if (!S.state.groups.length) {
      ul.append(h('li', { class: 'empty' }, S.state.totalTasks === 0
        ? 'No tasks yet. Add a line like "- [ ] Write report !1 #work" to any note.'
        : 'No tasks match the filters.'));
    }
  }

  function taskRow(t) {
    const cb = h('input', { type: 'checkbox', class: 'tick', 'aria-label': (t.done ? 'Mark not done: ' : 'Mark done: ') + t.text });
    cb.checked = t.done;
    cb.addEventListener('click', (e) => { e.preventDefault(); toggleTask(t); });
    const chips = t.labels.length ? h('span', { class: 'chips', text: t.labels.map((l) => '#' + l).join(' ') }) : null;
    return h('li', { class: `task p${t.priority}${t.done ? ' done' : ''}`, dataset: { path: t.path, line: String(t.line) } },
      cb,
      h('button', { class: 'ttext', title: `${t.path}, line ${t.line}`, onclick: () => openDoc(t.path, t.line) }, t.text),
      chips);
  }

  function renderFiles() {
    const ul = $('#filelist');
    ul.replaceChildren(...S.state.files.map((f) => h('li', {},
      h('button', { class: 'file' + (S.active && S.active.path === f.path ? ' active' : ''), title: f.problem ? `${f.path} ${f.problem}` : f.path, onclick: () => openDoc(f.path) },
        h('span', { class: 'fname', text: f.path }),
        f.problem ? h('span', { class: 'badge', text: 'read-only' }) : (f.openTasks ? h('span', { class: 'fcount', text: String(f.openTasks) }) : null)))));
    if (!S.state.files.length) ul.append(h('li', { class: 'empty' }, 'No .txt notes in this folder yet. Press New to start one.'));
  }

  async function toggleTask(t) {
    if (S.busyTask) return; // ignore double-clicks while the first one is being applied
    const doc = findDoc(t.path);
    if (doc && doc.dirty) { say(`Save "${doc.name}" first - it has unsaved changes.`, true); return; }
    S.busyTask = true;
    try {
      S.lastOwnWrite = Date.now();
      try { await api('POST', '/api/task', { path: t.path, line: t.line, done: !t.done, raw: t.raw }); }
      catch (e) {
        say(e.code === 'stale' ? 'That task changed on disk, so the list was refreshed. Try again.' : 'Could not update the task: ' + e.message, true);
        await refreshState();
        return;
      }
      await refreshState();
      if (doc && !doc.dirty) await reloadDoc(doc);
    } finally { S.busyTask = false; }
  }

  // ------------------------------------------------------------------ documents
  const findDoc = (path) => (path ? S.docs.find((d) => d.path && d.path.toLowerCase() === path.toLowerCase()) : null);

  function makeDoc(o) {
    const ta = h('textarea', { class: 'editor', spellcheck: 'false', autocapitalize: 'off', autocomplete: 'off', 'aria-label': 'Note text', hidden: true });
    const doc = Object.assign({
      id: S.nextId++, path: '', name: 'Untitled', kind: 'utf8', eol: 'crlf', hash: '', stamp: '', seenStamp: '',
      readOnly: false, problem: '', dirty: false, conflict: '', savedText: '', ta, scroll: 0, saving: false,
    }, o);
    ta.value = doc.savedText;
    ta.setSelectionRange(0, 0); // a note opens at its top, like Notepad (setting value leaves the caret at the end)
    ta.readOnly = doc.readOnly;
    styleEditor(ta);
    ta.addEventListener('input', () => onEdit(doc));
    ta.addEventListener('keydown', (e) => editorKey(e, doc));
    for (const ev of ['keyup', 'click', 'focus', 'select']) ta.addEventListener(ev, updateStatus);
    $('#editors').append(ta);
    S.docs.push(doc);
    return doc;
  }

  function onEdit(doc) {
    const was = doc.dirty;
    doc.dirty = doc.ta.value !== doc.savedText;
    saveDraftSoon(doc);
    if (doc.dirty !== was) renderTabs();
    updateStatus();
  }

  // Replace [start,end) with text in a way the browser can undo (Ctrl+Z); fall back to setRangeText.
  function editText(ta, start, end, text, selectAfter) {
    ta.focus();
    ta.setSelectionRange(start, end);
    let ok = false;
    try { ok = document.execCommand('insertText', false, text); } catch (_) { ok = false; }
    if (!ok) { ta.setRangeText(text, start, end, 'end'); ta.dispatchEvent(new Event('input')); }
    if (selectAfter != null) ta.setSelectionRange(selectAfter, selectAfter);
  }

  function editorKey(e, doc) {
    if (e.isComposing) return;
    // Tab types a tab; Shift+Tab (and Esc then Tab) still move focus so keyboard users are never trapped.
    if (e.key === 'Tab' && !e.shiftKey && !e.ctrlKey && !e.altKey && !e.metaKey && !doc.readOnly) {
      e.preventDefault();
      editText(doc.ta, doc.ta.selectionStart, doc.ta.selectionEnd, '\t');
    }
  }

  function showDoc(doc) {
    if (S.active && S.active !== doc && S.docs.includes(S.active)) S.active.scroll = S.active.ta.scrollTop;
    S.active = doc;
    for (const d of S.docs) d.ta.hidden = d !== doc;
    doc.ta.scrollTop = doc.scroll || 0;
    renderTabs(); renderNotice(); updateStatus(); if (S.state) renderFiles();
    document.title = (doc.dirty ? '* ' : '') + doc.name + ' - Trimple';
    if (!S.modal) doc.ta.focus();
  }

  function newDoc() {
    S.untitled++;
    const doc = makeDoc({ name: S.untitled > 1 ? `Untitled ${S.untitled}` : 'Untitled' });
    showDoc(doc);
    return doc;
  }

  function removePristine(except) {
    for (const d of [...S.docs]) {
      if (d !== except && !d.path && !d.dirty && d.ta.value === '') dropDoc(d);
    }
  }

  function dropDoc(doc) {
    doc.ta.remove();
    S.docs.splice(S.docs.indexOf(doc), 1);
    if (S.active === doc) S.active = null;
    forgetDraft(doc);
  }

  async function openDoc(path, line = 0) {
    let doc = findDoc(path);
    if (!doc) {
      let f;
      try { f = await api('GET', '/api/file?path=' + encodeURIComponent(path)); }
      catch (e) { say(`Could not open ${path}: ${e.message}`, true); return null; }
      doc = findDoc(path); // another open of the same note may have finished while we were waiting
      if (!doc) doc = makeDoc({
        path: f.path, name: baseName(f.path), kind: f.kind, eol: f.eol, hash: f.hash, stamp: f.stamp,
        readOnly: !f.lossless, problem: f.problem, savedText: f.text,
      });
      removePristine(doc);
      if (doc.readOnly) say(`"${doc.name}" ${doc.problem}, so it opened read-only to protect it. Use Notepad to edit it.`, true);
      saveSettingsSoon();
    }
    showDoc(doc);
    if (line > 0) gotoLine(doc, line);
    return doc;
  }

  function lineOffset(text, line) {
    let off = 0;
    for (let i = 1; i < line; i++) {
      const nl = text.indexOf('\n', off);
      if (nl < 0) return text.length;
      off = nl + 1;
    }
    return off;
  }

  function scrollToOffset(ta, off) {
    const cs = getComputedStyle(ta);
    const mirror = h('div', { class: 'mirror' });
    for (const p of ['fontFamily', 'fontSize', 'lineHeight', 'letterSpacing', 'tabSize', 'paddingTop', 'paddingLeft', 'paddingRight', 'paddingBottom', 'whiteSpace', 'overflowWrap', 'wordBreak']) mirror.style[p] = cs[p];
    mirror.style.boxSizing = 'border-box';
    mirror.style.width = ta.clientWidth + 'px';
    mirror.textContent = ta.value.slice(0, off);
    const marker = h('span', { text: '​' });
    mirror.append(marker);
    document.body.append(mirror);
    const y = marker.offsetTop;
    mirror.remove();
    ta.scrollTop = Math.max(0, y - ta.clientHeight / 3);
  }

  function gotoLine(doc, line) {
    const ta = doc.ta;
    const off = lineOffset(ta.value, line);
    ta.focus();
    ta.setSelectionRange(off, off);
    scrollToOffset(ta, off);
    updateStatus();
  }

  async function reloadDoc(doc, force = false) {
    let f;
    try { f = await api('GET', '/api/file?path=' + encodeURIComponent(doc.path)); }
    catch (e) { if (e.code === 'missing') { doc.conflict = 'deleted'; renderTabs(); renderNotice(); } return; }
    if (doc.dirty && !force) return; // the user typed while we were fetching: keep their text
    applyFile(doc, f);
  }

  function applyFile(doc, f) {
    const ta = doc.ta;
    const caret = ta.selectionStart, top = ta.scrollTop;
    ta.value = f.text;
    doc.savedText = f.text; doc.kind = f.kind; doc.eol = f.eol; doc.hash = f.hash; doc.stamp = f.stamp; doc.seenStamp = '';
    doc.readOnly = !f.lossless; doc.problem = f.problem; ta.readOnly = doc.readOnly;
    doc.dirty = false; doc.conflict = '';
    ta.setSelectionRange(Math.min(caret, ta.value.length), Math.min(caret, ta.value.length));
    ta.scrollTop = top;
    forgetDraft(doc);
    renderTabs(); renderNotice(); updateStatus();
  }

  // ---- saving
  function normalizeName(name) {
    let n = name.trim().replace(/\\/g, '/').replace(/^\/+/, '');
    if (!/\.txt$/i.test(n)) n += '.txt';
    return n;
  }
  const hiddenName = (n) => n.split('/').some((part) => part.startsWith('.'));

  async function promptName(initial) {
    const input = h('input', { type: 'text', value: initial, 'aria-label': 'File name', spellcheck: 'false' });
    const body = h('div', {}, h('p', { text: 'Name for this note. It is saved inside your notes folder; add a sub-folder like work/Plan.txt if you like.' }), input);
    const choice = await modal({
      title: 'Save note', body,
      buttons: [{ id: 'cancel', label: 'Cancel' }, { id: 'ok', label: 'Save', primary: true }],
      focus: () => { setTimeout(() => { input.focus(); input.select(); }, 0); return input; },
      onKey: (e, close) => { if (e.key === 'Enter') { e.preventDefault(); close('ok'); } },
    });
    return choice === 'ok' && input.value.trim() ? normalizeName(input.value) : null;
  }

  async function saveDoc(doc, saveAs = false) {
    if (!doc) return false;
    if (doc.readOnly) {
      await ask('Read-only file', `"${doc.name}" ${doc.problem}, so Trimple opened it read-only and will not write it.\n\nUse Notepad to edit this file.`, [{ id: 'ok', label: 'OK', primary: true }]);
      return false;
    }
    if (doc.saving) return false; // a save is already in flight (key repeat, double click)
    if (!saveAs && doc.path && !doc.dirty && doc.conflict !== 'deleted') { say('No changes to save.'); return true; }
    let path = doc.path, create = false;
    if (!path || saveAs) {
      const name = await promptName(doc.path || 'Notes.txt');
      if (!name) return false;
      if (hiddenName(name)) { say('Could not save: note names cannot start with a dot.', true); return false; }
      const other = findDoc(name);
      if (other && other !== doc) { say(`"${name}" is already open in another tab.`, true); return false; }
      path = name; create = true;
    }
    const text = doc.ta.value;
    const body = { path, text, kind: doc.kind, eol: doc.eol, expectedHash: create ? '' : doc.hash, create, overwrite: false, allowUpgrade: false };
    if (!create && !body.expectedHash) body.overwrite = true; // recreating a note that was deleted underneath us
    let res;
    doc.saving = true;
    try {
    for (;;) {
      try { res = await api('PUT', '/api/file', body); break; }
      catch (e) {
        if (e.code === 'exists') {
          if (await ask('Replace note?', `"${path}" already exists. Replace it with this text?`, [{ id: 'no', label: 'Cancel' }, { id: 'yes', label: 'Replace', primary: true }]) !== 'yes') return false;
          body.overwrite = true;
        } else if (e.code === 'changed') {
          if (await ask('File changed outside Trimple', `"${doc.name}" was changed on disk since you opened it.\n\nOverwrite it with your version?`, [{ id: 'no', label: 'Cancel' }, { id: 'yes', label: 'Overwrite', primary: true }]) !== 'yes') return false;
          body.overwrite = true;
        } else if (e.code === 'encoding') {
          if (await ask('Change file encoding?', `"${doc.name}" uses the old ANSI encoding, but now contains characters it cannot store.\n\nSave it as UTF-8 instead? Accented letters already in the file keep looking the same in Notepad.`, [{ id: 'no', label: 'Cancel' }, { id: 'yes', label: 'Save as UTF-8', primary: true }]) !== 'yes') return false;
          body.allowUpgrade = true;
        } else { say('Could not save: ' + e.message, true); return false; }
      }
    }
    } finally { doc.saving = false; }
    S.lastOwnWrite = Date.now();
    if (doc.path && doc.path !== path) forgetDraft(doc); // Save As: the old name's draft is stale
    doc.path = path; doc.name = baseName(path); doc.kind = res.kind; doc.hash = res.hash; doc.stamp = res.stamp; doc.seenStamp = '';
    doc.savedText = text; doc.conflict = '';
    doc.dirty = doc.ta.value !== text; // keys typed while the save was in flight stay unsaved
    if (!doc.dirty) forgetDraft(doc); else saveDraftSoon(doc);
    renderTabs(); renderNotice(); updateStatus();
    say('Saved ' + doc.name);
    saveSettingsSoon(); refreshState();
    return true;
  }

  async function closeDoc(doc) {
    if (doc.dirty) {
      showDoc(doc);
      const r = await ask('Unsaved changes', `Save changes to "${doc.name}"?`, [
        { id: 'cancel', label: 'Cancel' }, { id: 'discard', label: "Don't save" }, { id: 'save', label: 'Save', primary: true }]);
      if (r === 'cancel') return false;
      if (r === 'save' && !(await saveDoc(doc))) return false;
    }
    const idx = S.docs.indexOf(doc);
    const wasActive = S.active === doc;
    dropDoc(doc);
    if (!S.docs.length) newDoc();
    else if (wasActive) showDoc(S.docs[Math.min(idx, S.docs.length - 1)]);
    else renderTabs();
    saveSettingsSoon();
    return true;
  }

  function switchDoc(step) {
    if (S.docs.length < 2) return;
    const i = S.docs.indexOf(S.active);
    showDoc(S.docs[(i + step + S.docs.length) % S.docs.length]);
  }

  // ---- unsaved-draft recovery (survives a crashed window)
  function readDrafts() { try { return JSON.parse(localStorage.getItem('trimple.drafts') || '{}'); } catch (_) { return {}; } }
  function writeDrafts(d) { try { localStorage.setItem('trimple.drafts', JSON.stringify(d)); } catch (_) { /* storage unavailable */ } }
  const draftKey = (doc) => S.folder + '|' + doc.path;
  function saveDraftSoon(doc) {
    clearTimeout(doc.draftTimer); // one timer per tab, so editing tab B never cancels tab A's draft
    doc.draftTimer = setTimeout(() => {
      if (!doc.path || doc.readOnly || !S.docs.includes(doc)) return;
      const d = readDrafts();
      if (doc.dirty) d[draftKey(doc)] = { text: doc.ta.value, hash: doc.hash }; else delete d[draftKey(doc)];
      writeDrafts(d);
    }, 800);
  }
  function forgetDraft(doc) {
    if (!doc.path) return;
    clearTimeout(doc.draftTimer);
    const d = readDrafts();
    if (d[draftKey(doc)]) { delete d[draftKey(doc)]; writeDrafts(d); }
  }
  function restoreDrafts() {
    const d = readDrafts();
    for (const doc of S.docs) {
      const r = doc.path && d[draftKey(doc)];
      if (!r || doc.readOnly || r.text === doc.savedText) continue;
      doc.ta.value = r.text; doc.dirty = true;
      if (r.hash === doc.hash) say(`Restored unsaved changes in "${doc.name}".`);
      else { doc.conflict = 'changed'; say(`"${doc.name}" changed on disk after your unsaved edits; both are kept.`, true); }
    }
    renderTabs(); renderNotice();
  }

  // ---- outside changes and conflicts
  async function checkDocs() {
    const docs = S.docs.filter((d) => d.path);
    if (!docs.length) return;
    let stats;
    try { stats = await api('GET', '/api/stat?' + docs.map((d) => 'path=' + encodeURIComponent(d.path)).join('&')); }
    catch (_) { return; }
    let changed = false;
    for (const doc of docs) {
      const st = stats[doc.path];
      if (!st) continue;
      if (!st.exists) {
        if (doc.conflict !== 'deleted' && doc.hash !== '') { doc.conflict = 'deleted'; changed = true; }
        continue;
      }
      if (doc.conflict === 'deleted') { doc.conflict = ''; changed = true; }
      if (st.stamp === doc.stamp || (doc.dirty && st.stamp === doc.seenStamp)) continue;
      let f;
      try { f = await api('GET', '/api/file?path=' + encodeURIComponent(doc.path)); } catch (_) { continue; }
      if (f.hash === doc.hash) { doc.stamp = f.stamp; continue; } // touched, not changed
      if (!doc.dirty) {
        applyFile(doc, f);
        if (Date.now() - S.lastOwnWrite > 4000) say(`Reloaded "${doc.name}" (changed outside Trimple).`);
      }
      else { doc.conflict = 'changed'; doc.seenStamp = st.stamp; changed = true; }
    }
    if (changed) { renderTabs(); renderNotice(); }
  }

  function renderNotice() {
    const doc = S.active;
    const bar = $('#notice');
    if (!doc || !doc.conflict) { bar.hidden = true; return; }
    bar.hidden = false;
    $('#notice-text').textContent = doc.conflict === 'deleted'
      ? `"${doc.name}" was deleted or moved outside Trimple. Saving will recreate it.`
      : `"${doc.name}" changed outside Trimple, and you have unsaved edits here.`;
    $('#notice-reload').disabled = doc.conflict === 'deleted';
  }

  // ------------------------------------------------------------------ tabs and status
  function renderTabs() {
    const tabs = $('#tabs');
    const frag = [];
    S.docs.forEach((d) => {
      frag.push(h('div', {
        class: 'tab' + (d === S.active ? ' active' : '') + (d.conflict ? ' conflict' : '') + (d.readOnly ? ' readonly' : ''),
        role: 'presentation', title: d.path || d.name, dataset: { id: String(d.id) },
        onauxclick: (e) => { if (e.button === 1) { e.preventDefault(); closeDoc(d); } },
      },
        h('button', { class: 'tname', role: 'tab', 'aria-selected': String(d === S.active), onclick: () => showDoc(d) }, d.name),
        d.dirty ? h('span', { class: 'dot', role: 'img', title: 'Unsaved changes', 'aria-label': 'unsaved changes' }) : null,
        h('button', { class: 'tclose', 'aria-label': 'Close ' + d.name, title: 'Close (Alt+W)', onclick: (e) => { e.stopPropagation(); closeDoc(d); } }, '\u00d7')));
    });
    frag.push(h('button', { class: 'newtab', title: 'New note (Alt+N)', 'aria-label': 'New note', onclick: () => newDoc() }, '+'));
    tabs.replaceChildren(...frag);
    const active = tabs.querySelector('.tab.active');
    if (active && active.scrollIntoView) active.scrollIntoView({ block: 'nearest', inline: 'nearest' });
    if (S.active) document.title = (S.active.dirty ? '* ' : '') + S.active.name + ' - Trimple';
  }

  function updateStatus() {
    const doc = S.active;
    if (!doc) return;
    const ta = doc.ta;
    const pos = ta.selectionStart, v = ta.value;
    let line = 1, lastBreak = -1, i = 0;
    while (i < pos) {
      const nl = v.indexOf('\n', i);
      if (nl < 0 || nl >= pos) break;
      line++; lastBreak = nl; i = nl + 1;
    }
    $('#pos').textContent = `Ln ${line}, Col ${pos - lastBreak}`;
    $('#enc').textContent = { utf8: 'UTF-8', utf8bom: 'UTF-8 BOM', utf16le: 'UTF-16 LE', utf16be: 'UTF-16 BE', ansi: 'ANSI' }[doc.kind] || doc.kind;
    $('#eol').textContent = doc.eol.toUpperCase();
  }

  // ------------------------------------------------------------------ editor commands
  function currentLineBounds(ta) {
    const v = ta.value, pos = ta.selectionStart;
    const start = pos ? v.lastIndexOf('\n', pos - 1) + 1 : 0;
    let end = v.indexOf('\n', pos);
    if (end < 0) end = v.length;
    return [start, end];
  }

  function toggleTaskOnLine() {
    const doc = S.active;
    if (!doc || doc.readOnly) return;
    const ta = doc.ta;
    const [start, end] = currentLineBounds(ta);
    const m = /^([ \t]*[-*+][ \t]+\[)([ xX])(\][ \t]+\S)/.exec(ta.value.slice(start, end));
    if (!m) { say('No task on this line.'); return; }
    const caret = ta.selectionStart;
    const at = start + m[1].length;
    editText(ta, at, at + 1, m[2] === ' ' ? 'x' : ' ', caret);
  }

  function insertTask() {
    const doc = S.active;
    if (!doc || doc.readOnly) return;
    const ta = doc.ta;
    const [start] = currentLineBounds(ta);
    editText(ta, start, start, '- [ ] ');
  }

  async function openInNotepad() {
    const doc = S.active;
    if (!doc) return;
    if (!S.canNotepad) { say('Open in Notepad is only available on Windows.', true); return; }
    if (!doc.path) { say('Save this note first, then it can be opened in Notepad.', true); return; }
    if (doc.dirty) {
      const r = await ask('Unsaved changes', `"${doc.name}" has unsaved changes.\n\nSave before opening in Notepad?`, [
        { id: 'cancel', label: 'Cancel' }, { id: 'no', label: 'Open without saving' }, { id: 'yes', label: 'Save and open', primary: true }]);
      if (r === 'cancel') return;
      if (r === 'yes' && !(await saveDoc(doc))) return;
    }
    try { await api('POST', '/api/notepad', { path: doc.path }); say('Opened in Notepad.'); }
    catch (e) { say('Could not start Notepad: ' + e.message, true); }
  }

  // ------------------------------------------------------------------ dialogs: folder and quick open
  async function chooseFolder(first) {
    let info;
    try { info = await api('GET', '/api/fs?path=' + encodeURIComponent(S.folder || '')); }
    catch (e) { try { info = await api('GET', '/api/fs'); } catch (e2) { say(e2.message, true); return null; } }
    let current = info.path;
    const pathInput = h('input', { type: 'text', 'aria-label': 'Folder path', spellcheck: 'false' });
    const list = h('ul', { class: 'pick', 'aria-label': 'Folders' });
    const intro = h('p', { text: first
      ? 'Welcome to Trimple. Choose the folder that holds your notes (.txt files). Trimple reads tasks from every note in it.'
      : 'Choose the folder that holds your notes (.txt files).' });
    const wrap = h('div', {}, intro, h('div', { class: 'pathrow' }, pathInput, h('button', { onclick: () => go(pathInput.value) }, 'Go')), list);

    async function go(p) {
      let r;
      try { r = await api('GET', '/api/fs?path=' + encodeURIComponent(p)); } catch (e) { say(e.message, true); return; }
      current = r.path; pathInput.value = r.path;
      const rows = [];
      if (r.parent) rows.push(h('li', {}, h('button', { onclick: () => go(r.parent) }, '.. (up one level)')));
      if (!r.path && r.drives && r.drives.length) for (const d of r.drives) rows.push(h('li', {}, h('button', { onclick: () => go(d) }, d)));
      if (r.path && r.drives && r.drives.length && !r.parent) for (const d of r.drives) rows.push(h('li', {}, h('button', { onclick: () => go(d) }, d)));
      const sep = r.path.includes('\\') ? '\\' : '/';
      for (const d of r.dirs) rows.push(h('li', {}, h('button', { onclick: () => go(r.path.replace(/[\\/]+$/, '') + sep + d) }, d)));
      list.replaceChildren(...rows);
    }
    pathInput.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); go(pathInput.value); } });
    const buttons = [];
    if (!first) buttons.push({ id: 'cancel', label: 'Cancel' });
    if (first && info.suggested) buttons.push({ id: 'create', label: 'Create "Trimple Notes" in Documents' });
    buttons.push({ id: 'use', label: 'Use this folder', primary: true });
    go(current || '');
    const r = await modal({ title: first ? 'Choose your notes folder' : 'Change notes folder', body: wrap, buttons, cancelId: first ? 'none' : 'cancel' });
    let path = null, create = false;
    if (r === 'use') path = pathInput.value.trim() || current;
    else if (r === 'create') { path = info.suggested; create = true; }
    if (!path) return null;
    try { const res = await api('POST', '/api/folder', { path, create }); return res.folder; }
    catch (e) { say('Could not use that folder: ' + e.message, true); return null; }
  }

  async function changeFolder(first) {
    const unsaved = S.docs.filter((d) => d.path && d.dirty);
    if (unsaved.length) {
      say(`Save or close "${unsaved[0].name}" before changing the notes folder.`, true);
      showDoc(unsaved[0]);
      return false;
    }
    const folder = await chooseFolder(first);
    if (!folder) return false;
    S.settings.folder = folder;
    // tabs from the old folder no longer belong here
    for (const d of [...S.docs]) if (d.path && !d.dirty) dropDoc(d);
    if (!S.docs.length) newDoc(); else showDoc(S.docs[0]);
    await refreshState();
    say('Notes folder: ' + folder);
    saveSettingsSoon();
    return true;
  }

  async function quickOpen() {
    if (!S.state) return;
    const input = h('input', { type: 'text', placeholder: 'Type to filter notes', 'aria-label': 'Filter notes', spellcheck: 'false' });
    const list = h('ul', { class: 'pick' });
    let sel = 0, shown = [];
    const draw = () => {
      const q = input.value.toLowerCase();
      shown = S.state.files.filter((f) => f.path.toLowerCase().includes(q)).slice(0, 200);
      sel = Math.min(sel, Math.max(0, shown.length - 1));
      list.replaceChildren(...shown.map((f, i) => h('li', {}, h('button', { class: i === sel ? 'sel' : '', dataset: { path: f.path }, onclick: () => pick(f.path) }, f.path))));
      if (!shown.length) list.replaceChildren(h('li', { class: 'empty' }, 'No matching notes.'));
    };
    let closeFn = () => {};
    const pick = (p) => { chosen = p; closeFn('pick'); };
    let chosen = null;
    input.addEventListener('input', () => { sel = 0; draw(); });
    draw();
    const r = await modal({
      title: 'Open note', body: h('div', {}, input, list), buttons: [{ id: 'cancel', label: 'Cancel' }],
      focus: () => input,
      onOpen: (close) => { closeFn = close; },
      onKey: (e, close) => {
        if (e.key === 'ArrowDown') { e.preventDefault(); sel = Math.min(sel + 1, shown.length - 1); draw(); }
        else if (e.key === 'ArrowUp') { e.preventDefault(); sel = Math.max(sel - 1, 0); draw(); }
        else if (e.key === 'Enter' && shown[sel]) { e.preventDefault(); chosen = shown[sel].path; close('pick'); }
      },
    });
    if (r === 'pick' && chosen) openDoc(chosen);
  }

  // ------------------------------------------------------------------ live updates
  function connectEvents() {
    const es = new EventSource('/api/events?t=' + encodeURIComponent(S.token));
    es.addEventListener('version', () => { S.connected = true; $('#banner').hidden = !(S.state && S.state.truncated); refreshState(); checkDocs(); });
    es.onerror = () => {
      S.connected = false;
      const bar = $('#banner');
      bar.hidden = false;
      bar.textContent = 'Lost connection to Trimple. If you closed it, start Trimple again.';
    };
  }

  // ------------------------------------------------------------------ wiring
  function wire() {
    $('#btn-folder').onclick = () => changeFolder(false);
    $('#btn-new').onclick = () => newDoc();
    $('#btn-open').onclick = () => quickOpen();
    $('#btn-save').onclick = () => saveDoc(S.active);
    $('#btn-notepad').onclick = () => openInNotepad();
    $('#btn-panel').onclick = togglePanel;
    $('#btn-wrap').onclick = () => { S.settings.wordWrap = !S.settings.wordWrap; applyAppearance(); saveSettingsSoon(); };
    $('#btn-smaller').onclick = () => setFont(S.settings.fontSize - 1);
    $('#btn-bigger').onclick = () => setFont(S.settings.fontSize + 1);
    $('#btn-theme').onclick = toggleTheme;
    $('#group').onchange = () => { S.settings.groupBy = $('#group').value; saveSettingsSoon(); refreshState(); };
    $('#label').onchange = refreshState;
    $('#search').oninput = refreshStateSoon;
    $('#showdone').onchange = () => { S.settings.showDone = $('#showdone').checked; saveSettingsSoon(); refreshState(); };
    $('#notice-reload').onclick = async () => {
      const doc = S.active;
      if (!doc) return;
      if (doc.dirty && await ask('Discard your edits?', `Reloading "${doc.name}" from disk throws away the unsaved changes in this tab.`, [
        { id: 'no', label: 'Cancel' }, { id: 'yes', label: 'Reload from disk', primary: true }]) !== 'yes') return;
      await reloadDoc(doc, true);
    };
    $('#notice-keep').onclick = async () => {
      const doc = S.active;
      if (!doc) return;
      if (doc.conflict === 'deleted') { doc.hash = ''; doc.stamp = ''; doc.conflict = ''; doc.dirty = true; } // Save recreates it
      else {
        try { const f = await api('GET', '/api/file?path=' + encodeURIComponent(doc.path)); doc.hash = f.hash; doc.stamp = f.stamp; doc.seenStamp = ''; doc.conflict = ''; }
        catch (e) { say(e.message, true); return; }
      }
      renderTabs(); renderNotice();
    };

    const sp = $('#splitter');
    sp.addEventListener('pointerdown', (e) => {
      sp.setPointerCapture(e.pointerId);
      const move = (ev) => { S.settings.panelWidth = Math.max(220, Math.min(900, Math.round(ev.clientX))); document.documentElement.style.setProperty('--side-w', S.settings.panelWidth + 'px'); };
      const up = () => { sp.removeEventListener('pointermove', move); sp.removeEventListener('pointerup', up); saveSettingsSoon(); };
      sp.addEventListener('pointermove', move); sp.addEventListener('pointerup', up);
    });
    sp.addEventListener('keydown', (e) => {
      if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
        S.settings.panelWidth = Math.max(220, Math.min(900, S.settings.panelWidth + (e.key === 'ArrowLeft' ? -20 : 20)));
        applyAppearance(); saveSettingsSoon();
      }
    });

    window.addEventListener('keydown', onKey);
    for (const ev of ['dragover', 'drop']) window.addEventListener(ev, (e) => e.preventDefault()); // a dropped file must not navigate away
    window.addEventListener('focus', () => { if (S.state) checkDocs(); });
    window.addEventListener('beforeunload', (e) => { if (S.docs.some((d) => d.dirty)) { e.preventDefault(); e.returnValue = ''; } });
  }

  function togglePanel() { S.settings.panelVisible = !S.settings.panelVisible; applyAppearance(); saveSettingsSoon(); }
  function toggleTheme() { S.settings.theme = S.settings.theme === 'dark' ? 'light' : 'dark'; applyAppearance(); saveSettingsSoon(); }

  function onKey(e) {
    if (e.isComposing) return;
    const ctrl = e.ctrlKey || e.metaKey;
    const k = (e.key || '').toLowerCase();
    // keep the browser's own Save Page / Open File / reload from firing behind a dialog
    if (S.modal) {
      if ((ctrl && !e.altKey && (k === 's' || k === 'o' || k === 'b')) || e.key === 'F5') e.preventDefault();
      return;
    }
    if (e.repeat && (ctrl || e.altKey)) { // holding a shortcut must not repeat it (save twice, close every tab ...)
      if (/^(s|o|b|n|w|t|p|d|enter)$/.test(k)) e.preventDefault();
      return;
    }
    if (ctrl && !e.altKey && k === 's') { e.preventDefault(); saveDoc(S.active, e.shiftKey); }
    else if (ctrl && !e.altKey && !e.shiftKey && k === 'o') { e.preventDefault(); quickOpen(); }
    else if (ctrl && !e.altKey && !e.shiftKey && k === 'b') { e.preventDefault(); togglePanel(); }
    else if (ctrl && !e.altKey && e.key === 'Enter') { e.preventDefault(); toggleTaskOnLine(); }
    else if (ctrl && !e.altKey && (e.key === '=' || e.key === '+')) { e.preventDefault(); setFont(S.settings.fontSize + 1); }
    else if (ctrl && !e.altKey && e.key === '-') { e.preventDefault(); setFont(S.settings.fontSize - 1); }
    else if (ctrl && !e.altKey && e.key === '0') { e.preventDefault(); setFont(14); }
    else if (ctrl && e.altKey && e.key === 'ArrowRight') { e.preventDefault(); switchDoc(1); }
    else if (ctrl && e.altKey && e.key === 'ArrowLeft') { e.preventDefault(); switchDoc(-1); }
    else if (e.altKey && !ctrl && !e.shiftKey && k === 'n') { e.preventDefault(); newDoc(); }
    else if (e.altKey && !ctrl && !e.shiftKey && k === 'w') { e.preventDefault(); if (S.active) closeDoc(S.active); }
    else if (e.altKey && !ctrl && !e.shiftKey && k === 't') { e.preventDefault(); insertTask(); }
    else if (e.altKey && !ctrl && !e.shiftKey && k === 'p') { e.preventDefault(); openInNotepad(); }
    else if (e.altKey && !ctrl && !e.shiftKey && k === 'd') { e.preventDefault(); toggleTheme(); }
    else if (e.altKey && !ctrl && /^Digit[1-9]$/.test(e.code || '')) { const d = S.docs[Number(e.code.slice(5)) - 1]; if (d) { e.preventDefault(); showDoc(d); } }
    else if (e.key === 'F5') { e.preventDefault(); refreshState(); checkDocs(); }
  }

  // ------------------------------------------------------------------ start
  async function start() {
    wire();
    let boot;
    try { boot = await api('GET', '/api/bootstrap'); }
    catch (e) {
      document.body.classList.remove('loading');
      const bar = $('#banner'); bar.hidden = false;
      bar.textContent = e.status === 401 ? 'This window is not connected to Trimple. Start Trimple again.' : e.message;
      return;
    }
    S.os = boot.os; S.canNotepad = boot.notepad; S.settings = boot.settings; S.folder = boot.folder;
    if (!S.canNotepad) $('#btn-notepad').hidden = true;
    applyAppearance();
    document.body.classList.remove('loading');
    $('#folderpath').textContent = boot.folder;
    newDoc();
    S.connected = true;
    if (!boot.folder) { if (!(await changeFolder(true))) say('Choose a notes folder to see your tasks.', true); }
    await refreshState();

    // restore the previous session's tabs, then anything asked for on the command line
    for (const p of S.settings.openFiles || []) {
      if (!S.state.files.some((f) => f.path === p)) continue;
      await openDoc(p);
    }
    restoreDrafts();
    for (const abs of openAbs) {
      try {
        const r = await api('GET', '/api/resolve?abs=' + encodeURIComponent(abs));
        if (r.inside) await openDoc(r.path); else say(`"${abs}" is outside the notes folder. Use Folder to switch to it.`, true);
      } catch (e) { say(e.message, true); }
    }
    if (S.docs.length) showDoc(S.docs[S.docs.length - 1]);
    connectEvents(); // only now, so a live update can never race the startup sequence above
    window.__trimpleReady = true;
  }

  start();
})();
