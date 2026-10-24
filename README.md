# Trimple

Notepad with tabs, a dark theme, and a task panel. Your notes stay plain `.txt` files in a folder, so Notepad can still open every one of them.

**One file, nothing to install.** Trimple is a single `Trimple.exe` (about 8 MB). Double-click it: a clean app-style window opens (using the Microsoft Edge that ships with Windows 10/11, or Chrome), and everything runs locally on your PC. Nothing is sent anywhere.

## Get it

Download `Trimple.exe` from the latest [release](../../releases) once one is published, or right now from the **Trimple-windows-amd64** artifact at the bottom of any green run on the [Actions](../../actions) page (GitHub login needed; it arrives zipped). Put the exe anywhere and double-click it.

* Windows may show *"Windows protected your PC"* the first time because the file is not code-signed. Click **More info → Run anyway**.
* On first run pick the folder that holds your notes, or let Trimple create `Documents\Trimple Notes` with a short `Welcome.txt`.
* Closing the window quits Trimple after about a minute (so a quick reopen reuses it). Double-clicking `Trimple.exe` while it is running just opens another window on the same instance. Wait for it to quit before replacing the file with a newer version.

## Writing tasks

Any line shaped like a checkbox becomes a task:

```
- [ ] Write Q3 report !1 #work
- [ ] Review pull requests !2 #work #dev
- [x] Send invoice #admin
```

| Part | Meaning |
|---|---|
| `[ ]` / `[x]` | open / done (`-`, `*` or `+` bullets all work) |
| `!1` `!2` `!3` | priority, 1 is highest (optional) |
| `#word` | label; must start with a letter, so `#123` is not a label (optional) |

Tasks inside ``` code fences are ignored, and a `#` inside a URL is not a label.

## The task panel

* Groups by **priority**, **label** or **file**; filter by label; search; show or hide completed tasks; open counts at the top.
* **Click the box** to tick a task. Only that one character in the file changes; encoding, BOM and line endings are kept byte for byte.
* **Click the text** to open the note with the cursor on that line.
* A tick is refused if the note has unsaved edits in a tab, or if the line moved on disk since the panel last read it.
* Edits made in Notepad (or by a sync client) show up within about 2 seconds. If you have unsaved edits when the file changes underneath you, a bar offers *Reload from disk* / *Keep mine*; neither side is lost silently.
* **Open in Notepad** (toolbar, `Alt+P`) opens the current note in Notepad.
* Unsaved text is also kept as a draft, so closing the window by accident does not lose it.

## Shortcuts

Browsers reserve `Ctrl+N`, `Ctrl+W`, `Ctrl+T` and `Ctrl+Tab` for themselves, so Trimple uses these instead:

| | |
|---|---|
| `Ctrl+S` / `Ctrl+Shift+S` | save / save as |
| `Ctrl+O` | quick-open a note (type to filter) |
| `Alt+N` / `Alt+W` | new tab / close tab |
| `Alt+1`…`9`, `Ctrl+Alt+←/→` | switch tabs |
| `Alt+T` | insert `- [ ] ` at the start of the line |
| `Ctrl+Enter` | tick/untick the task on the current line |
| `Ctrl+B` | show/hide the task panel |
| `Alt+D` | dark / light |
| `Ctrl+=` / `Ctrl+-` / `Ctrl+0` | text size |
| `Alt+P` | open in Notepad |
| `F5` | rescan |

## Safety rules (why you can trust it with your notes)

* Notes are only ever rewritten through a write-to-temp-then-rename step, so a crash cannot leave half a file.
* A file whose bytes could not be reproduced exactly (stray invalid bytes, NUL characters) opens **read-only** and its tasks cannot be ticked; edit those in Notepad.
* Saving never silently changes a file's encoding: an old ANSI note that gains a character it cannot store asks before becoming UTF-8.
* Saving a note that changed on disk since you opened it asks first.
* Saving an untouched note does nothing, so mixed line endings are not normalised behind your back.

## Security

The app is a local web server, so it is locked down: it listens on `127.0.0.1` only, every request needs a random per-run token, requests with a foreign `Host` (DNS rebinding) or `Origin` (cross-site) are refused, there are no CORS headers, the page has a strict Content-Security-Policy, and file paths from the page can never leave the notes folder (no `..`, drive letters, reserved names, alternate streams or symlink escapes).

## Settings, logs and files Trimple creates

* `%AppData%\Trimple\settings.json`: your preferences and open tabs.
* `%LocalAppData%\Trimple\trimple.log` (errors and crashes) and `instance.json` (port and secret token of the running copy, removed on exit).
* A hidden `.trimple-*.tmp` file appears next to a note for a moment while it is being saved.
* Unsaved-text drafts live in the browser profile's local storage for the app window.

Flags (run `Trimple.exe -h` from a terminal): `-folder`, `-config`, `-port`, `-no-browser`, `-keep-alive`, `-version`. Extra arguments are notes to open, so `Trimple.exe todo.txt` works from a shortcut or "Open with".

## Build and test it yourself

```
go build -o Trimple.exe ./cmd/trimple                      # needs Go 1.22+
go build -ldflags "-s -w -H=windowsgui" -o Trimple.exe ./cmd/trimple   # no console window (what releases use)
go test -race ./...                                        # core + server tests
cd e2e && npm ci && npm test                               # drives the real UI in a real browser
```

CI runs all of this on Windows and Linux for every push (Go 1.22 and current), builds `Trimple.exe`, smoke-tests the built exe on a real Windows runner (including a file locked like an editor locks it, and the Notepad hand-off), and drives the UI with Microsoft Edge on Windows and Chrome on Linux.

## Layout

```
cmd/trimple/        entry point, single-instance, window launch
internal/notes/     encoding, task parsing, safe rewrite, folder index (no UI/HTTP)
internal/server/    local HTTP API, security guards, live updates
internal/config/    settings file
web/                the embedded UI (plain HTML/CSS/JS, no build step)
testdata/           sample notes with deliberate CRLF/LF mixes
e2e/                browser tests (Playwright)
legacy-powershell/  the first version (PowerShell + WinForms), kept as a fallback
```

## Known limits

* Windows 10/11 is the target. Open in Notepad is Windows-only; the rest runs anywhere Go does.
* `.txt` only; hidden/system folders, symlinked or junction folders, hidden (dot-prefixed) notes and files over 5 MB are skipped; at most 2000 notes are scanned (pick a smaller folder if you hit it). A whole drive cannot be chosen as the notes folder.
* Paths of 260 characters or more are not supported.
* Notes inside OneDrive or Dropbox work, but keep them "always on this device": a note that is only a cloud placeholder may be slow to open.
* Saving a note rewrites it in one piece: a file with mixed line endings is saved with its most common ending. (Ticking a task from the panel never does this; it changes one character.)
* The ANSI encoding is assumed to be Windows-1252 (Western European).
* Windows Controlled Folder Access can block saves into `Documents` until you allow `Trimple.exe`; Windows Smart App Control blocks unsigned programs outright (turn it off or ask for a signed build).
* No find/replace inside a note yet, no due dates yet (the `!1 #label` format leaves room for `@2026-12-31`).
* The old PowerShell version in `legacy-powershell/` still works but is no longer developed.
