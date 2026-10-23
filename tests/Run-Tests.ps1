# Dependency-free test runner for Trimple.Core. Works on Windows PowerShell 5.1 and PowerShell 7+.
# Usage: pwsh -File tests/Run-Tests.ps1      (exit code 0 = all passed)
param([string]$Filter = '*')

$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
Import-Module (Join-Path $repo 'src\Trimple.Core.psm1') -Force -DisableNameChecking

$script:Pass = 0
$script:Fail = 0
$script:Failures = New-Object System.Collections.Generic.List[string]
$script:TempRoots = New-Object System.Collections.Generic.List[string]

function New-TempDir {
    $d = Join-Path ([System.IO.Path]::GetTempPath()) ('trimple-test-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $d | Out-Null
    $script:TempRoots.Add($d)
    return $d
}

function Assert-Equal($Actual, $Expected, [string]$Message = '') {
    $a = if ($Actual -is [array]) { '[' + ($Actual -join ',') + ']' } else { "$Actual" }
    $e = if ($Expected -is [array]) { '[' + ($Expected -join ',') + ']' } else { "$Expected" }
    if ($a -cne $e) { throw "Expected <$e> but got <$a>. $Message" }
}
function Assert-True($Value, [string]$Message = '') { if (-not $Value) { throw "Expected true. $Message" } }
function Assert-Bytes([byte[]]$Actual, [byte[]]$Expected, [string]$Message = '') {
    if ($Actual.Length -ne $Expected.Length) { throw "Byte length $($Actual.Length) != $($Expected.Length). $Message" }
    for ($i = 0; $i -lt $Actual.Length; $i++) { if ($Actual[$i] -ne $Expected[$i]) { throw "Byte $i differs ($($Actual[$i]) != $($Expected[$i])). $Message" } }
}

function Test([string]$Name, [scriptblock]$Body) {
    if ($Name -notlike $Filter) { return }
    try { & $Body; $script:Pass++; Write-Host "  ok   $Name" }
    catch { $script:Fail++; $script:Failures.Add("$Name :: $($_.Exception.Message)"); Write-Host "  FAIL $Name :: $($_.Exception.Message)" -ForegroundColor Red }
}

function Write-Raw([string]$Path, [byte[]]$Bytes) { [System.IO.File]::WriteAllBytes($Path, $Bytes) }
function Utf8([string]$s) { (New-Object System.Text.UTF8Encoding($false)).GetBytes($s) }

Write-Host 'Task line parsing'
Test 'parses an open task with priority and labels' {
    $t = ConvertFrom-TrimpleTaskLine -Line '- [ ] Write report !1 #work #Q3' -LineNumber 4
    Assert-True ($null -ne $t)
    Assert-Equal $t.Done $false
    Assert-Equal $t.Priority 1
    Assert-Equal $t.Labels @('work', 'q3')
    Assert-Equal $t.Text 'Write report'
    Assert-Equal $t.Line 4
}
Test 'parses done tasks with x and X' {
    Assert-Equal (ConvertFrom-TrimpleTaskLine -Line '- [x] done').Done $true
    Assert-Equal (ConvertFrom-TrimpleTaskLine -Line '- [X] done').Done $true
}
Test 'accepts star and plus bullets and indentation' {
    Assert-True ($null -ne (ConvertFrom-TrimpleTaskLine -Line '* [ ] a'))
    Assert-True ($null -ne (ConvertFrom-TrimpleTaskLine -Line '+ [ ] a'))
    Assert-True ($null -ne (ConvertFrom-TrimpleTaskLine -Line "`t  - [ ] nested"))
}
Test 'rejects non-task lines' {
    foreach ($l in @('', 'just text', '# heading #tag', '- not a task', '[ ] no bullet', '-[ ] no space', '- [] empty box', '- [ ]', '- [ ]   ')) {
        Assert-True ($null -eq (ConvertFrom-TrimpleTaskLine -Line $l)) "line: '$l'"
    }
}
Test 'priority only counts as a standalone token' {
    Assert-Equal (ConvertFrom-TrimpleTaskLine -Line '- [ ] wow!1 ok').Priority 0
    Assert-Equal (ConvertFrom-TrimpleTaskLine -Line '- [ ] go !4 now').Priority 0
    Assert-Equal (ConvertFrom-TrimpleTaskLine -Line '- [ ] go !3').Priority 3
    Assert-Equal (ConvertFrom-TrimpleTaskLine -Line '- [ ] !2 first').Priority 2
}
Test 'url fragments are not labels' {
    $t = ConvertFrom-TrimpleTaskLine -Line '- [ ] read https://x.com/a#frag #real'
    Assert-Equal $t.Labels @('real')
}
Test 'labels tolerate trailing punctuation, hyphen and unicode' {
    $t = ConvertFrom-TrimpleTaskLine -Line ("- [ ] a #q3-plan, #caf" + [char]0xE9) -LineNumber 1
    Assert-Equal $t.Labels @('q3-plan', ('caf' + [char]0xE9))
}
Test 'duplicate labels collapse' {
    Assert-Equal (ConvertFrom-TrimpleTaskLine -Line '- [ ] a #x #X #x').Labels @('x')
}
Test 'text that is only tokens keeps something to show' {
    $t = ConvertFrom-TrimpleTaskLine -Line '- [ ] !1 #work'
    Assert-True ($t.Text.Length -gt 0)
}

Write-Host 'Text scanning'
Test 'ignores tasks inside code fences and counts line numbers across CRLF/LF/CR' {
    $fence = '```'
    $text = "a`r`n- [ ] one`n$fence`r`n- [ ] hidden`r`n$fence`r- [ ] two"
    $tasks = Get-TrimpleTasksFromText -Text $text
    Assert-Equal @($tasks).Count 2
    Assert-Equal $tasks[0].Line 2
    Assert-Equal $tasks[1].Line 6
}
Test 'empty text yields no tasks' { Assert-Equal @(Get-TrimpleTasksFromText -Text '').Count 0 }

Write-Host 'Encoding'
Test 'detects utf8, utf8 bom, utf16 le/be and ansi' {
    Assert-Equal (ConvertFrom-TrimpleBytes -Bytes (Utf8 'caf')).Kind 'utf8'
    Assert-Equal (ConvertFrom-TrimpleBytes -Bytes ([byte[]](0xEF, 0xBB, 0xBF, 0x61))).Kind 'utf8bom'
    Assert-Equal (ConvertFrom-TrimpleBytes -Bytes ([byte[]](0xFF, 0xFE, 0x61, 0x00))).Kind 'utf16le'
    Assert-Equal (ConvertFrom-TrimpleBytes -Bytes ([byte[]](0xFE, 0xFF, 0x00, 0x61))).Kind 'utf16be'
    $ansi = ConvertFrom-TrimpleBytes -Bytes ([byte[]](0x63, 0x61, 0x66, 0xE9))
    Assert-Equal $ansi.Kind 'ansi'
    Assert-Equal $ansi.Text ('caf' + [char]0xE9)
}
Test 'empty file reads as empty utf8' {
    $d = ConvertFrom-TrimpleBytes -Bytes ([byte[]]@())
    Assert-Equal $d.Text ''
    Assert-Equal $d.Kind 'utf8'
}
Test 'round trips every kind byte for byte' {
    $cases = @(
        @{ K = 'utf8';    B = (Utf8 "h$([char]0xE9)llo`r`n") },
        @{ K = 'utf8bom'; B = ([byte[]](0xEF, 0xBB, 0xBF) + (Utf8 "x`n")) },
        @{ K = 'utf16le'; B = ([byte[]](0xFF, 0xFE) + [System.Text.Encoding]::Unicode.GetBytes("x`r`n")) },
        @{ K = 'utf16be'; B = ([byte[]](0xFE, 0xFF) + [System.Text.Encoding]::BigEndianUnicode.GetBytes("x`r`n")) },
        @{ K = 'ansi';    B = ([byte[]](0x63, 0x61, 0x66, 0xE9, 0x0D, 0x0A)) }
    )
    foreach ($c in $cases) {
        $dec = ConvertFrom-TrimpleBytes -Bytes $c.B
        Assert-Equal $dec.Kind $c.K
        $enc = ConvertTo-TrimpleBytes -Text $dec.Text -Kind $dec.Kind
        Assert-Equal $enc.Kind $c.K
        Assert-Bytes $enc.Bytes $c.B $c.K
    }
}
Test 'ansi text with characters outside the code page upgrades to utf8 instead of writing ?' {
    $enc = ConvertTo-TrimpleBytes -Text ("caf$([char]0xE9) $([char]0x2713)") -Kind 'ansi'
    Assert-Equal $enc.Kind 'utf8'
    Assert-Equal (ConvertFrom-TrimpleBytes -Bytes $enc.Bytes).Text ("caf$([char]0xE9) $([char]0x2713)")
}
Test 'dominant EOL' {
    Assert-Equal ((Get-TrimpleDominantEol "a`r`nb`r`nc`n") -replace "`r", 'R' -replace "`n", 'N') 'RN'
    Assert-Equal ((Get-TrimpleDominantEol "a`nb`nc`r`n") -replace "`r", 'R' -replace "`n", 'N') 'N'
    Assert-Equal ((Get-TrimpleDominantEol "no newline") -replace "`r", 'R' -replace "`n", 'N') 'RN'
    Assert-Equal ((Get-TrimpleDominantEol "a`rb`rc") -replace "`r", 'R' -replace "`n", 'N') 'R'
}

Write-Host 'Writing a task state'
Test 'ticking changes exactly one byte and nothing else (CRLF, utf8)' {
    $d = New-TempDir; $p = Join-Path $d 'a.txt'
    $orig = Utf8 "title`r`n- [ ] one !1 #x`r`n- [ ] two`r`ntail without newline"
    Write-Raw $p $orig
    Set-TrimpleTaskState -Path $p -Line 2 -Done $true -ExpectedRaw '- [ ] one !1 #x'
    $new = [System.IO.File]::ReadAllBytes($p)
    Assert-Equal $new.Length $orig.Length
    $diff = 0; for ($i = 0; $i -lt $new.Length; $i++) { if ($new[$i] -ne $orig[$i]) { $diff++ } }
    Assert-Equal $diff 1
    Assert-Equal @(Get-TrimpleTasksFromText -Text ([System.IO.File]::ReadAllText($p)))[0].Done $true
}
Test 'ticking preserves BOM, ansi bytes, LF endings and utf16' {
    $d = New-TempDir
    $files = @{
        'bom.txt'   = ([byte[]](0xEF, 0xBB, 0xBF) + (Utf8 "- [ ] a`n"))
        'ansi.txt'  = ([byte[]](0x2D, 0x20, 0x5B, 0x20, 0x5D, 0x20, 0x63, 0x61, 0x66, 0xE9, 0x0A))
        'u16.txt'   = ([byte[]](0xFF, 0xFE) + [System.Text.Encoding]::Unicode.GetBytes("- [ ] a`r`n"))
    }
    foreach ($name in $files.Keys) {
        $p = Join-Path $d $name; Write-Raw $p $files[$name]
        Set-TrimpleTaskState -Path $p -Line 1 -Done $true
        $new = [System.IO.File]::ReadAllBytes($p); $old = $files[$name]
        Assert-Equal $new.Length $old.Length $name
        $diff = 0; for ($i = 0; $i -lt $new.Length; $i++) { if ($new[$i] -ne $old[$i]) { $diff++ } }
        Assert-Equal $diff 1 $name
    }
}
Test 'unticking restores the original bytes' {
    $d = New-TempDir; $p = Join-Path $d 'a.txt'
    $orig = Utf8 "- [ ] one`r`n"
    Write-Raw $p $orig
    Set-TrimpleTaskState -Path $p -Line 1 -Done $true
    Set-TrimpleTaskState -Path $p -Line 1 -Done $false
    Assert-Bytes ([System.IO.File]::ReadAllBytes($p)) $orig
}
Test 'refuses when the line moved or changed on disk' {
    $d = New-TempDir; $p = Join-Path $d 'a.txt'
    Write-Raw $p (Utf8 "- [ ] one`r`n")
    $threw = $false
    try { Set-TrimpleTaskState -Path $p -Line 1 -Done $true -ExpectedRaw '- [ ] something else' } catch { $threw = $true }
    Assert-True $threw
    $threw = $false
    try { Set-TrimpleTaskState -Path $p -Line 9 -Done $true } catch { $threw = $true }
    Assert-True $threw
    Assert-Bytes ([System.IO.File]::ReadAllBytes($p)) (Utf8 "- [ ] one`r`n")
}
Test 'leaves no temp files behind' {
    $d = New-TempDir; $p = Join-Path $d 'a.txt'
    Write-Raw $p (Utf8 "- [ ] one`r`n")
    Set-TrimpleTaskState -Path $p -Line 1 -Done $true
    Assert-Equal @(Get-ChildItem -LiteralPath $d).Count 1
}

Write-Host 'Folder index'
Test 'indexes the sample folder recursively and skips code fences' {
    $idx = @{}
    $r = Update-TrimpleTaskIndex -Index $idx -Root (Join-Path $PSScriptRoot 'sample-notes')
    Assert-Equal @($r.Changed).Count 3
    $tasks = @(Get-TrimpleIndexTasks -Index $idx)
    Assert-Equal $tasks.Count 9
    Assert-Equal @($tasks | Where-Object { $_.RelPath -like '*Project.txt' }).Count 2
}
Test 'only changed files are re-parsed; removals are reported' {
    $d = New-TempDir
    Write-Raw (Join-Path $d 'a.txt') (Utf8 "- [ ] a`n")
    Write-Raw (Join-Path $d 'b.txt') (Utf8 "- [ ] b`n")
    $idx = @{}
    $r1 = Update-TrimpleTaskIndex -Index $idx -Root $d
    Assert-Equal @($r1.Changed).Count 2
    $r2 = Update-TrimpleTaskIndex -Index $idx -Root $d
    Assert-Equal @($r2.Changed).Count 0
    Start-Sleep -Milliseconds 30
    Write-Raw (Join-Path $d 'a.txt') (Utf8 "- [ ] a`n- [ ] a2`n")
    $r3 = Update-TrimpleTaskIndex -Index $idx -Root $d
    Assert-Equal @($r3.Changed).Count 1
    Remove-Item (Join-Path $d 'b.txt')
    $r4 = Update-TrimpleTaskIndex -Index $idx -Root $d
    Assert-Equal @($r4.Removed).Count 1
    Assert-Equal @(Get-TrimpleIndexTasks -Index $idx).Count 2
}
Test 'ignores non-txt, look-alike extensions, hidden folders and oversized files' {
    $d = New-TempDir
    New-Item -ItemType Directory -Path (Join-Path $d '.git') | Out-Null
    Write-Raw (Join-Path $d 'ok.txt') (Utf8 "- [ ] ok`n")
    Write-Raw (Join-Path $d 'no.md') (Utf8 "- [ ] md`n")
    Write-Raw (Join-Path $d 'no.txtx') (Utf8 "- [ ] txtx`n")
    Write-Raw (Join-Path $d '.git\hidden.txt') (Utf8 "- [ ] hidden`n")
    $list = @(Get-TrimpleFileList -Root $d)
    Assert-Equal $list.Count 1
    Assert-Equal $list[0].RelPath 'ok.txt'
    $big = Join-Path $d 'big.txt'
    Write-Raw $big (New-Object byte[] 1024)
    Assert-Equal @(Get-TrimpleFileList -Root $d -MaxBytes 512).Count 1
}
Test 'a missing folder yields an empty index, not an error' {
    $idx = @{}
    $r = Update-TrimpleTaskIndex -Index $idx -Root (Join-Path (New-TempDir) 'nope')
    Assert-Equal @($r.Changed).Count 0
}

Write-Host 'Filter and group'
function Sample-Tasks {
    $idx = @{}; [void](Update-TrimpleTaskIndex -Index $idx -Root (Join-Path $PSScriptRoot 'sample-notes'))
    return @(Get-TrimpleIndexTasks -Index $idx)
}
Test 'hides done tasks by default and shows them on request' {
    $all = Sample-Tasks
    Assert-Equal @(Select-TrimpleTasks -Tasks $all).Count 7
    Assert-Equal @(Select-TrimpleTasks -Tasks $all -IncludeDone $true).Count 9
}
Test 'label filter is case-insensitive and search covers text, labels and file' {
    $all = Sample-Tasks
    Assert-Equal @(Select-TrimpleTasks -Tasks $all -Label 'DEV').Count 3
    Assert-Equal @(Select-TrimpleTasks -Tasks $all -Search 'report').Count 1
    Assert-Equal @(Select-TrimpleTasks -Tasks $all -Search 'project.txt').Count 2
}
Test 'groups by priority in fixed order' {
    $g = @(Group-TrimpleTasks -Tasks (Select-TrimpleTasks -Tasks (Sample-Tasks)) -By Priority)
    Assert-Equal @($g | ForEach-Object { $_.Name }) @('High (!1)', 'Medium (!2)', 'Low (!3)', 'No priority')
    Assert-Equal $g[0].OpenCount 2
}
Test 'groups by label with multi-label tasks in each and unlabeled last' {
    $g = @(Group-TrimpleTasks -Tasks (Select-TrimpleTasks -Tasks (Sample-Tasks)) -By Label)
    Assert-Equal $g[0].Name '#admin'
    $dev = $g | Where-Object { $_.Name -eq '#dev' }
    Assert-Equal @($dev.Tasks).Count 3
}
Test 'groups by file' {
    $g = @(Group-TrimpleTasks -Tasks (Sample-Tasks) -By File)
    Assert-Equal $g.Count 3
}
Test 'done tasks sort after open tasks within a group' {
    $g = @(Group-TrimpleTasks -Tasks (Sample-Tasks) -By Priority)
    $high = $g[0].Tasks
    Assert-Equal $high[$high.Count - 1].Done $true
}
Test 'open counts' {
    $c = Get-TrimpleOpenCounts -Tasks (Sample-Tasks)
    Assert-Equal $c.Total 7
    Assert-Equal ($c.High + $c.Medium + $c.Low + $c.None) 7
}
Test 'empty input is safe everywhere' {
    Assert-Equal @(Group-TrimpleTasks -Tasks @() -By Label).Count 0
    Assert-Equal @(Select-TrimpleTasks -Tasks @()).Count 0
    Assert-Equal (Get-TrimpleOpenCounts -Tasks @()).Total 0
    Assert-Equal @(Get-TrimpleLabels -Tasks @()).Count 0
}

Write-Host 'Settings'
Test 'defaults when no file exists, and a corrupt file is ignored' {
    $d = New-TempDir
    $s = Import-TrimpleSettings -Paths @((Join-Path $d 'missing.json'))
    Assert-Equal $s.Theme 'dark'
    $bad = Join-Path $d 'bad.json'; Write-Raw $bad (Utf8 '{ not json')
    Assert-Equal (Import-TrimpleSettings -Paths @($bad)).Theme 'dark'
}
Test 'round trips, merges unknown keys away and falls back to a second location' {
    $d = New-TempDir
    $s = Get-TrimpleDefaultSettings
    $s.Theme = 'light'; $s.Folder = 'C:\Notes'; $s.OpenFiles = @('C:\Notes\a.txt', 'C:\Notes\b.txt'); $s.Window.Width = 800
    $unwritable = Join-Path (Join-Path $d 'missing-file-as-dir\x') 'settings.json'
    New-Item -ItemType File -Path (Join-Path $d 'missing-file-as-dir') | Out-Null     # makes the first path impossible
    $good = Join-Path $d 'ok.json'
    $written = Export-TrimpleSettings -Settings $s -Paths @($unwritable, $good)
    Assert-Equal $written $good
    $back = Import-TrimpleSettings -Paths @($good)
    Assert-Equal $back.Theme 'light'
    Assert-Equal $back.Folder 'C:\Notes'
    Assert-Equal @($back.OpenFiles).Count 2
    Assert-Equal $back.Window.Width 800
    Assert-Equal $back.Window.Height 700
}

Write-Host 'Repository hygiene'
Test 'all PowerShell sources are ASCII-only (5.1 reads BOM-less files as ANSI)' {
    $files = Get-ChildItem -LiteralPath $repo -Recurse -Include *.ps1, *.psm1, *.bat -File | Where-Object { $_.FullName -notmatch '[\\/]\.git[\\/]' }
    foreach ($f in $files) {
        $bytes = [System.IO.File]::ReadAllBytes($f.FullName)
        foreach ($b in $bytes) { if ($b -gt 127) { throw "Non-ASCII byte in $($f.Name)" } }
    }
}

foreach ($d in $script:TempRoots) { Remove-Item -LiteralPath $d -Recurse -Force -ErrorAction SilentlyContinue }
Write-Host ''
Write-Host ("{0} passed, {1} failed" -f $script:Pass, $script:Fail)
if ($script:Fail -gt 0) { $script:Failures | ForEach-Object { Write-Host " - $_" -ForegroundColor Red }; exit 1 }
exit 0
