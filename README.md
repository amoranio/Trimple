# Trimple

Notepad with tabs, a dark theme, and a task panel. Your notes stay plain `.txt` files in a folder, so Notepad can still open every one of them.

Nothing to install. Trimple is a PowerShell script that runs on the Windows PowerShell already built into Windows 10 and 11.

## Run it

1. Download or clone this repo anywhere (a USB stick works too).
2. Double-click **`Trimple.bat`**.
3. On first run, pick the folder that holds your notes. Cancel to use `Documents\Trimple Notes`, which gets a short `Welcome.txt`.

`Trimple.bat some-note.txt` opens a file straight into a tab. If something goes wrong, run **`Trimple-Debug.bat`** to keep the console visible, or check `Trimple.log` beside the script.

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
| `#word` | label, as many as you like (optional) |

Tasks inside ``` code fences are ignored, and `#` inside a URL is not a label.

## The task panel

- Groups by **priority**, **label** or **file**; filter by label; search; show or hide completed tasks.
- **Click the box** to tick a task. Only that one character in the file changes; encoding, BOM and line endings are kept as they were.
- **Click the text** to open the file at that line.
- A tick is refused if the file has unsaved edits in a tab, or if the line moved on disk since the panel last read it.
- The folder is rescanned every 2 seconds, so edits made in Notepad show up on their own. If you have unsaved edits in a tab when its file changes, you get a bar with *Reload from disk* / *Keep mine* instead of losing either side.

## Shortcuts

| | |
|---|---|
| Ctrl+N / O / S / W | new tab, open, save, close tab |
| Ctrl+Shift+S / O | save as, open folder |
| Ctrl+Shift+N | open the current file in Notepad |
| Ctrl+Tab / Ctrl+Shift+Tab | next / previous tab |
| Ctrl+Shift+T | insert `- [ ] ` at the start of the line |
| Ctrl+Enter | tick/untick the task on the current line |
| Ctrl+B | show/hide task panel |
| Ctrl+Shift+D | dark / light |
| Ctrl+`+` / Ctrl+`-` / Ctrl+0 | font size |
| F5 | rescan the folder |

## Layout

```
Trimple.ps1          entry point (relaunches itself single-threaded if needed)
Trimple.bat          double-click launcher
src/Trimple.Core.psm1   file I/O, parsing, index, grouping, settings (no UI)
src/Trimple.UI.ps1      WinForms front end
src/Trimple.SelfTest.ps1  headless UI check used by CI
tests/Run-Tests.ps1     core tests (no dependencies)
tests/sample-notes/     fixtures with mixed line endings
```

Settings (folder, theme, font, open tabs, window size) are saved in `Trimple.settings.json` next to the script, or in `%APPDATA%\Trimple` if that folder is read-only.

## Tests

```
powershell -File tests\Run-Tests.ps1        # core logic, also works with pwsh on any OS
powershell -File Trimple.ps1 -SelfTest      # builds the real window headlessly and drives it (Windows only)
```

CI (GitHub Actions) runs on every push:

| | Windows PowerShell 5.1 | PowerShell 7 | Linux (PowerShell 7) |
|---|---|---|---|
| Core + adversarial tests | gating | gating | gating |
| Headless UI self-test | **gating** | informational (may fail) | n/a |

Windows PowerShell 5.1 is the supported runtime for the app itself: it is what `Trimple.bat` starts and it ships with Windows. Running `Trimple.ps1` under PowerShell 7 is untested.

## Known limits (first cut)

- Only `.txt` files; hidden folders, symlinked/junction folders and files over 5 MB are skipped, and at most 2000 files are scanned.
- Files with bytes that are invalid for their encoding (or NUL characters) open **read-only**, and ticking their tasks is refused, so Trimple can never silently alter them. Edit those in Notepad.
- Tasks must start the label with a letter: `#work` is a label, `#123` (an issue number) is not.
- The UI has been exercised headlessly in CI, not by a person clicking through it yet. Expect rough edges on first real use (high-DPI multi-monitor setups in particular); `Trimple.log` records errors.
- No find/replace inside a note yet.
- A file with mixed line endings is saved with its most common one.
- No due dates yet; the `!1 #label` format leaves room to add `@2026-12-31` later.
