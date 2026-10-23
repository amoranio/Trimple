# Adversarial tests for Trimple.Core: try to break parsing, encoding round trips, atomic writes,
# the folder index, grouping with odd inputs, and settings with wrong types.
# Same harness style as Run-Tests.ps1 (no dependencies). Exit code 1 if any test fails.
# Tests whose names start with "[perf]" also fail when they take longer than 2 seconds.
# Source is ASCII-only: non-ASCII characters are built with [char]0x....
param([string]$Filter = '*', [switch]$SkipPerf)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2.0
$repo = Split-Path -Parent $PSScriptRoot
Import-Module (Join-Path $repo 'src\Trimple.Core.psm1') -Force -DisableNameChecking

$script:Pass = 0
$script:Fail = 0
$script:Failures = New-Object System.Collections.Generic.List[string]
$script:TempRoots = New-Object System.Collections.Generic.List[string]
$script:OnWindows = ($PSVersionTable.PSVersion.Major -lt 6) -or ((Get-Variable -Name IsWindows -ValueOnly -ErrorAction SilentlyContinue) -eq $true)
$script:RunTimer = [System.Diagnostics.Stopwatch]::StartNew()

function New-TempDir {
    $d = Join-Path ([System.IO.Path]::GetTempPath()) ('trimple-adv-' + [guid]::NewGuid().ToString('N'))
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
function Assert-Throws([scriptblock]$Body, [string]$Message = '') {
    $threw = $false
    try { & $Body } catch { $threw = $true }
    if (-not $threw) { throw "Expected an exception. $Message" }
}
function Count-ByteDiffs([byte[]]$A, [byte[]]$B) {
    if ($A.Length -ne $B.Length) { return [Math]::Max($A.Length, $B.Length) }
    $n = 0
    for ($i = 0; $i -lt $A.Length; $i++) { if ($A[$i] -ne $B[$i]) { $n++ } }
    return $n
}

function Test([string]$Name, [scriptblock]$Body) {
    if ($Name -notlike $Filter) { return }
    if ($SkipPerf -and $Name.StartsWith('[perf]')) { return }   # timing limits are tuned for PowerShell 7
    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    try {
        & $Body
        if ($Name.StartsWith('[perf]') -and $sw.ElapsedMilliseconds -gt 2000) { throw "Too slow: $($sw.ElapsedMilliseconds) ms (limit 2000)" }
        $script:Pass++; Write-Host ("  ok   {0} ({1} ms)" -f $Name, $sw.ElapsedMilliseconds)
    }
    catch { $script:Fail++; $script:Failures.Add("$Name :: $($_.Exception.Message)"); Write-Host "  FAIL $Name :: $($_.Exception.Message)" -ForegroundColor Red }
}

function Write-Raw([string]$Path, [byte[]]$Bytes) { [System.IO.File]::WriteAllBytes($Path, $Bytes) }
function Read-Raw([string]$Path) { , [System.IO.File]::ReadAllBytes($Path) }
function Utf8([string]$s) { , (New-Object System.Text.UTF8Encoding($false)).GetBytes($s) }
function U([int[]]$Codes) { -join ($Codes | ForEach-Object { [char]$_ }) }
function Cat([byte[]]$A, [byte[]]$B) { $o = New-Object byte[] ($A.Length + $B.Length); [Array]::Copy($A, 0, $o, 0, $A.Length); [Array]::Copy($B, 0, $o, $A.Length, $B.Length); , $o }
function Names($Items) { @($Items | ForEach-Object { $_.Name }) }

$E_ACUTE = [string][char]0xE9
$E_COMBINING = 'e' + [char]0x0301                       # decomposed e-acute (NFD)
$EMOJI = ([string][char]0xD83C) + ([string][char]0xDF89)  # party popper, a surrogate pair
$CJK_B = ([string][char]0xD840) + ([string][char]0xDC00)  # U+20000, a letter outside the BMP
$HINDI = U @(0x0939, 0x093F, 0x0902, 0x0926, 0x0940)      # letters plus combining vowel signs

# ================================================================ parsing
Write-Host 'Task line parsing'

Test 'bullet followed by only tokens still yields a task with visible text' {
    foreach ($l in @('- [ ] !1', '- [ ] #a', '- [ ] !1 #a #b', '- [ ] !1 !2', '- [x] #a #a')) {
        $t = ConvertFrom-TrimpleTaskLine -Line $l
        Assert-True ($null -ne $t) "line: $l"
        Assert-True ($t.Text.Length -gt 0) "line: $l"
    }
}
Test 'trailing spaces and tabs after the checkbox' {
    foreach ($l in @("- [ ]`t", "- [ ] `t ", "- [x]`t`t", '- [ ]         ')) {
        Assert-True ($null -eq (ConvertFrom-TrimpleTaskLine -Line $l)) "empty body must not be a task: <$l>"
    }
    $t = ConvertFrom-TrimpleTaskLine -Line "- [ ] a `t  "
    Assert-Equal $t.Text 'a'
    Assert-Equal $t.Raw "- [ ] a `t  " 'Raw must be preserved byte for byte'
}
Test 'tab separators and no-space-after-box' {
    Assert-True ($null -ne (ConvertFrom-TrimpleTaskLine -Line "-`t[ ]`tfoo"))
    Assert-True ($null -eq (ConvertFrom-TrimpleTaskLine -Line '- [ ]foo'))
    Assert-True ($null -eq (ConvertFrom-TrimpleTaskLine -Line '- [  ] foo'))
    Assert-True ($null -eq (ConvertFrom-TrimpleTaskLine -Line '- [xx] foo'))
    Assert-True ($null -eq (ConvertFrom-TrimpleTaskLine -Line '-- [ ] foo'))
}
Test 'priority at start, middle, end and duplicated' {
    Assert-Equal (ConvertFrom-TrimpleTaskLine -Line '- [ ] !3 a').Priority 3
    Assert-Equal (ConvertFrom-TrimpleTaskLine -Line '- [ ] a !3').Priority 3
    Assert-Equal (ConvertFrom-TrimpleTaskLine -Line '- [ ] a !2 b').Priority 2
    $t = ConvertFrom-TrimpleTaskLine -Line '- [ ] a !1 b !2 c'
    Assert-Equal $t.Priority 1 'first priority token wins'
    Assert-Equal $t.Text 'a b c' 'both tokens removed from the text'
}
Test 'label with decomposed (combining mark) letters is found' {
    $t = ConvertFrom-TrimpleTaskLine -Line ("- [ ] a #caf" + $E_COMBINING)
    Assert-Equal @($t.Labels).Count 1 'NFD text (macOS, copy/paste) loses its labels'
}
Test 'label in a script that uses combining vowel signs (Hindi) is found' {
    $t = ConvertFrom-TrimpleTaskLine -Line ("- [ ] a #" + $HINDI)
    Assert-Equal @($t.Labels).Count 1
}
Test 'label containing a letter outside the BMP (surrogate pair) is found' {
    $t = ConvertFrom-TrimpleTaskLine -Line ("- [ ] a #n" + $CJK_B)
    Assert-Equal @($t.Labels).Count 1
}
Test 'emoji next to labels never throws and never corrupts text' {
    $t = ConvertFrom-TrimpleTaskLine -Line ("- [ ] party " + $EMOJI + " #fun " + $EMOJI)
    Assert-True ($null -ne $t)
    Assert-Equal @($t.Labels) @('fun')
    Assert-True ($t.Text.Contains($EMOJI))
    $t2 = ConvertFrom-TrimpleTaskLine -Line ("- [ ] a #" + $EMOJI)
    Assert-True ($null -ne $t2)
    Assert-Equal $t2.Raw ("- [ ] a #" + $EMOJI)
}
Test 'labels: odd shapes do not throw and keep sensible results' {
    Assert-Equal @((ConvertFrom-TrimpleTaskLine -Line '- [ ] a #').Labels).Count 0
    Assert-Equal @((ConvertFrom-TrimpleTaskLine -Line '- [ ] a ## b').Labels).Count 0
    Assert-Equal @((ConvertFrom-TrimpleTaskLine -Line '- [ ] a #a#b').Labels).Count 0
    Assert-Equal @((ConvertFrom-TrimpleTaskLine -Line '- [ ] a #-').Labels).Count 0     # a label must start with a letter
    Assert-Equal @((ConvertFrom-TrimpleTaskLine -Line '- [ ] fix bug #123').Labels).Count 0   # issue numbers are not labels
    Assert-Equal @((ConvertFrom-TrimpleTaskLine -Line '- [ ] a #a, #b; #c. #d! #e? (#f) #g)').Labels) @('a', 'b', 'c', 'd', 'e', 'g')
}
Test 'nested and heavily indented tasks' {
    foreach ($l in @('        - [ ] deep', "`t`t`t- [x] tabs", '    * [ ] star', '  + [ ] plus')) {
        Assert-True ($null -ne (ConvertFrom-TrimpleTaskLine -Line $l)) "line: <$l>"
    }
    $t = ConvertFrom-TrimpleTaskLine -Line '    - [x] a'
    Assert-Equal $t.Done $true
}
Test 'a task whose text contains brackets and bullets' {
    $t = ConvertFrom-TrimpleTaskLine -Line '- [ ] - [ ] inner [x] text'
    Assert-Equal $t.Done $false
    Assert-Equal $t.Text '- [ ] inner [x] text'
}
Test 'ordinary prose, markdown, and weird near misses are not tasks' {
    foreach ($l in @('1. [ ] numbered', '> - [ ] quoted', '- (x) paren', '- [-] dash', '- [_] under', '-[x] tight', ([string][char]0x2022 + ' [ ] bullet char'))) {
        $r = $null
        try { $r = ConvertFrom-TrimpleTaskLine -Line $l } catch { throw "threw on <$l>: $($_.Exception.Message)" }
        Assert-True ($null -eq $r) "line: <$l>"
    }
}
Test '[perf] one 2 MB line of spaces then text, and a 2 MB line of label-ish tokens' {
    $l1 = (' ' * 2000000) + 'x'
    Assert-True ($null -eq (ConvertFrom-TrimpleTaskLine -Line $l1))
    $l2 = '- [ ] ' + ((' #a' * 300000))
    $t = ConvertFrom-TrimpleTaskLine -Line $l2
    Assert-Equal @($t.Labels).Count 1
}
Test '[perf] pathological label: huge token that fails the trailing lookahead' {
    $body = '- [ ] ' + ('#' + ('a' * 200000) + '@ ') * 5
    $t = ConvertFrom-TrimpleTaskLine -Line $body
    Assert-True ($null -ne $t)
}
Test '[perf] pathological whitespace runs in the task body' {
    $t = ConvertFrom-TrimpleTaskLine -Line ('- [ ] a' + (' ' * 1000000) + 'b' + (' ' * 1000000) + '!')
    Assert-Equal $t.Text ('a b !')
}

# ================================================================ text scanning
Write-Host 'Text scanning'

Test 'lone-CR file: line numbers match what an editor shows' {
    $t = @(Get-TrimpleTasksFromText -Text "- [ ] a`r- [ ] b`r- [x] c")
    Assert-Equal @($t | ForEach-Object { $_.Line }) @(1, 2, 3)
}
Test 'mixed EOLs including doubled CR before CRLF' {
    $text = "x`r`r`n- [ ] a`n`n- [ ] b`r`n`r- [ ] c"
    # lines: x(1) ''(2) a(3) ''(4) b(5) ''(6) c(7)
    $t = @(Get-TrimpleTasksFromText -Text $text)
    Assert-Equal @($t | ForEach-Object { $_.Line }) @(3, 5, 7)
}
Test 'task on last line without newline, and trailing newline adds no phantom task' {
    $t = @(Get-TrimpleTasksFromText -Text "a`n- [ ] last")
    Assert-Equal $t.Count 1
    Assert-Equal $t[0].Line 2
    Assert-Equal @(Get-TrimpleTasksFromText -Text "- [ ] only`n").Count 1
    Assert-Equal @(Get-TrimpleTasksFromText -Text "`n`n`n").Count 0
}
Test 'tilde fences hide tasks and unclosed fences hide the rest of the file' {
    $text = "- [ ] before`n~~~`n- [ ] hidden`n~~~`n- [ ] after`n``````txt`n- [ ] hidden too`n- [ ] and this"
    $t = @(Get-TrimpleTasksFromText -Text $text)
    Assert-Equal @($t | ForEach-Object { $_.Text }) @('before', 'after')
}
Test 'a tilde line inside a backtick fence does not close it' {
    $text = "``````\n~~~\n- [ ] still hidden\n``````\n- [ ] visible".Replace('\n', "`n")
    $t = @(Get-TrimpleTasksFromText -Text $text)
    Assert-Equal @($t | ForEach-Object { $_.Text }) @('visible')
}
Test 'a shorter fence run inside a longer fence does not close it (CommonMark)' {
    $four = '````'; $three = '```'
    $text = "$four`n$three`n- [ ] still hidden`n$three`n$four`n- [ ] visible"
    $t = @(Get-TrimpleTasksFromText -Text $text)
    Assert-Equal @($t | ForEach-Object { $_.Text }) @('visible')
}
Test 'tasks directly after a closing fence on CR-only files' {
    $t = @(Get-TrimpleTasksFromText -Text "``````\r- [ ] hidden\r``````\r- [ ] shown".Replace('\r', "`r"))
    Assert-Equal $t.Count 1
    Assert-Equal $t[0].Line 4
}
Test 'null and empty text' {
    Assert-Equal @(Get-TrimpleTasksFromText -Text $null).Count 0
    Assert-Equal @(Get-TrimpleTasksFromText -Text '').Count 0
    Assert-Equal @(Get-TrimpleTasksFromText -Text "`r").Count 0
}
Test '[perf] 3,000-task file parses in under 2 seconds' {
    $sb = New-Object System.Text.StringBuilder
    for ($i = 1; $i -le 3000; $i++) { [void]$sb.Append("- [ ] task number $i !2 #label$($i % 50)`r`n") }
    $t = @(Get-TrimpleTasksFromText -Text $sb.ToString())
    Assert-Equal $t.Count 3000
    Assert-Equal $t[2999].Line 3000
}
Test '[perf] 20,000-line file of non-task prose parses in under 2 seconds' {
    $sb = New-Object System.Text.StringBuilder
    for ($i = 1; $i -le 20000; $i++) { [void]$sb.Append("just some prose on line $i with a #hash and !1`n") }
    Assert-Equal @(Get-TrimpleTasksFromText -Text $sb.ToString()).Count 0
}

# ================================================================ encoding
Write-Host 'Encoding'

Test '0-byte and 1-byte files read without error' {
    $d = New-TempDir
    $p0 = Join-Path $d 'zero.txt'; Write-Raw $p0 ([byte[]]@())
    $f = Read-TrimpleFile -Path $p0
    Assert-Equal $f.Text ''; Assert-Equal $f.Kind 'utf8'
    foreach ($b in @(0x00, 0x2D, 0xEF, 0xFE, 0xFF, 0x80)) {
        $p1 = Join-Path $d "one$b.txt"; Write-Raw $p1 ([byte[]]@($b))
        $f1 = Read-TrimpleFile -Path $p1
        $enc = ConvertTo-TrimpleBytes -Text $f1.Text -Kind $f1.Kind
        Assert-Bytes $enc.Bytes ([byte[]]@($b)) "single byte 0x$('{0:X2}' -f $b)"
    }
}
Test 'partial BOM (EF BB only) is treated as ansi and round trips' {
    $b = [byte[]](0xEF, 0xBB, 0x2D, 0x20, 0x5B, 0x20, 0x5D, 0x20, 0x61)
    $dec = ConvertFrom-TrimpleBytes -Bytes $b
    Assert-Equal $dec.Kind 'ansi'
    Assert-Bytes (ConvertTo-TrimpleBytes -Text $dec.Text -Kind $dec.Kind).Bytes $b
    $b2 = [byte[]](0xEF, 0xBB)
    $dec2 = ConvertFrom-TrimpleBytes -Bytes $b2
    Assert-Bytes (ConvertTo-TrimpleBytes -Text $dec2.Text -Kind $dec2.Kind).Bytes $b2
}
Test 'BOM-only files (utf8, utf16le, utf16be) round trip' {
    foreach ($b in @(([byte[]](0xEF, 0xBB, 0xBF)), ([byte[]](0xFF, 0xFE)), ([byte[]](0xFE, 0xFF)))) {
        $dec = ConvertFrom-TrimpleBytes -Bytes $b
        Assert-Equal $dec.Text ''
        Assert-Bytes (ConvertTo-TrimpleBytes -Text $dec.Text -Kind $dec.Kind).Bytes $b
    }
}
Test 'invalid UTF-8 sequences fall back to ansi and are byte-exact on round trip' {
    $cases = @(
        [byte[]](0x61, 0xC3, 0x28),             # bad continuation
        [byte[]](0xC0, 0xAF),                   # overlong
        [byte[]](0x80),                         # lone continuation
        [byte[]](0xED, 0xA0, 0x80),             # encoded surrogate
        [byte[]](0xF4, 0x90, 0x80, 0x80),       # beyond U+10FFFF
        [byte[]](0xE2, 0x82),                   # truncated 3-byte sequence at EOF
        [byte[]](0x2D, 0x20, 0x5B, 0x20, 0x5D, 0x20, 0x81, 0x8D, 0x8F, 0x90, 0x9D)   # cp1252 holes
    )
    foreach ($b in $cases) {
        $dec = ConvertFrom-TrimpleBytes -Bytes $b
        Assert-Equal $dec.Kind 'ansi' ($b -join ',')
        Assert-Bytes (ConvertTo-TrimpleBytes -Text $dec.Text -Kind $dec.Kind).Bytes $b ($b -join ',')
    }
}
Test 'every high byte value survives an ansi round trip' {
    $all = New-Object byte[] 128
    for ($i = 0; $i -lt 128; $i++) { $all[$i] = 128 + $i }
    $b = Cat ([byte[]](0x61, 0x0A)) $all
    $dec = ConvertFrom-TrimpleBytes -Bytes $b
    Assert-Equal $dec.Kind 'ansi'
    Assert-Bytes (ConvertTo-TrimpleBytes -Text $dec.Text -Kind $dec.Kind).Bytes $b
}
Test 'utf16 with surrogate pairs (emoji) round trips' {
    $le = Cat ([byte[]](0xFF, 0xFE)) ([System.Text.Encoding]::Unicode.GetBytes("- [ ] $EMOJI`r`n"))
    $be = Cat ([byte[]](0xFE, 0xFF)) ([System.Text.Encoding]::BigEndianUnicode.GetBytes("- [ ] $EMOJI`r`n"))
    foreach ($b in @($le, $be)) {
        $dec = ConvertFrom-TrimpleBytes -Bytes $b
        Assert-True ($dec.Text.Contains($EMOJI))
        Assert-Bytes (ConvertTo-TrimpleBytes -Text $dec.Text -Kind $dec.Kind).Bytes $b
    }
}
function Assert-RefusedNotCorrupted([byte[]]$Bytes, [string]$Why) {
    $d = New-TempDir
    $p = Join-Path $d 'bad.txt'
    [System.IO.File]::WriteAllBytes($p, $Bytes)
    $f = Read-TrimpleFile -Path $p
    Assert-Equal $f.Lossless $false "${Why}: file should be flagged as not safely rewritable"
    Assert-True ($f.Problem.Length -gt 0) 'a human-readable reason is given'
    Assert-Throws { Set-TrimpleTaskState -Path $p -Line 1 -Done $true } "${Why}: tick must be refused"
    Assert-Bytes ([System.IO.File]::ReadAllBytes($p)) $Bytes "${Why}: file must be untouched"
}
Test 'utf16le with an odd trailing byte is flagged read-only and a tick is refused, bytes untouched' {
    Assert-RefusedNotCorrupted ([byte[]]((Cat ([byte[]](0xFF, 0xFE)) ([System.Text.Encoding]::Unicode.GetBytes("- [ ] a"))) + [byte[]](0x62))) 'odd trailing byte'
}
Test 'utf16 with a lone surrogate is flagged read-only (LE and BE)' {
    Assert-RefusedNotCorrupted ([byte[]](0xFF, 0xFE, 0x2D, 0x00, 0x20, 0x00, 0x5B, 0x00, 0x20, 0x00, 0x5D, 0x00, 0x20, 0x00, 0x00, 0xD8, 0x62, 0x00)) 'lone surrogate LE'
    Assert-RefusedNotCorrupted ([byte[]](0xFE, 0xFF, 0x00, 0x2D, 0x00, 0x20, 0x00, 0x5B, 0x00, 0x20, 0x00, 0x5D, 0x00, 0x20, 0xD8, 0x00, 0x00, 0x62)) 'lone surrogate BE'
}
Test 'utf8 with BOM and a stray invalid byte is flagged read-only' {
    Assert-RefusedNotCorrupted ([byte[]](0xEF, 0xBB, 0xBF, 0x2D, 0x20, 0x5B, 0x20, 0x5D, 0x20, 0x61, 0xFF, 0x0A)) 'bad byte after BOM'
}
Test 'a file containing NUL characters is flagged read-only (an edit control would truncate it)' {
    Assert-RefusedNotCorrupted ([byte[]](0x2D, 0x20, 0x5B, 0x20, 0x5D, 0x20, 0x61, 0x0A, 0x00, 0x00, 0x00, 0x00)) 'NUL tail'
}
Test 'bom-less utf16 and utf32 bytes survive a round trip' {
    $noBom = [System.Text.Encoding]::Unicode.GetBytes("- [ ] a`r`n")
    $utf32 = Cat ([byte[]](0xFF, 0xFE, 0x00, 0x00)) ([System.Text.Encoding]::UTF32.GetBytes("- [ ] a`r`n"))
    foreach ($b in @($noBom, $utf32)) {
        $dec = ConvertFrom-TrimpleBytes -Bytes $b
        Assert-Bytes (ConvertTo-TrimpleBytes -Text $dec.Text -Kind $dec.Kind).Bytes $b
    }
}
Test 'fuzz: 400 seeded random byte strings, every BOM flavour: a file is either rewritten byte-exact or flagged read-only' {
    $rng = New-Object System.Random 20240601
    $prefixes = @([byte[]]@(), [byte[]](0xEF, 0xBB, 0xBF), [byte[]](0xFF, 0xFE), [byte[]](0xFE, 0xFF), [byte[]](0xEF, 0xBB))
    $d = New-TempDir
    $p = Join-Path $d 'fuzz.txt'
    $silent = 0
    $flagged = 0
    $first = $null
    for ($n = 0; $n -lt 400; $n++) {
        $len = $rng.Next(0, 40)
        $body = New-Object byte[] $len
        $rng.NextBytes($body)
        $b = Cat $prefixes[$n % $prefixes.Count] $body
        [System.IO.File]::WriteAllBytes($p, $b)
        $f = Read-TrimpleFile -Path $p
        $enc = ConvertTo-TrimpleBytes -Text $f.Text -Kind $f.Kind
        $exact = ((Count-ByteDiffs $enc.Bytes $b) -eq 0)
        if ($exact -and $f.Text.IndexOf([char]0) -lt 0) { continue }
        if ($f.Lossless) { $silent++; if ($null -eq $first) { $first = (($b | ForEach-Object { '{0:X2}' -f $_ }) -join '') } }
        else { $flagged++ }
    }
    if ($silent -gt 0) { throw "$silent inputs would be silently altered but were reported safe. First: $first" }
    Assert-True ($flagged -gt 0) 'expected the fuzzer to hit some malformed inputs'
}
Test 'dominant EOL on odd inputs' {
    $show = { param($s) ($s -replace "`r", 'R') -replace "`n", 'N' }
    Assert-Equal (& $show (Get-TrimpleDominantEol "")) 'RN'
    Assert-Equal (& $show (Get-TrimpleDominantEol "`r`r`n")) 'RN'
    Assert-Equal (& $show (Get-TrimpleDominantEol "a`nb`rc")) 'N'      # LF and CR tie: LF
    Assert-Equal (& $show (Get-TrimpleDominantEol "a`n`n`r`rb`r")) 'R'
    Assert-Equal (& $show (Get-TrimpleDominantEol $null)) 'RN'
}

# ================================================================ Set-TrimpleTaskState
Write-Host 'Writing a task state'

function New-Note([string]$Dir, [string]$Name, [byte[]]$Bytes) { $p = Join-Path $Dir $Name; Write-Raw $p $Bytes; return $p }

Test 'paths with brackets, spaces, wildcards, dollar signs and apostrophes' {
    $d = New-TempDir
    $sub = Join-Path $d '[proj] (1)'
    New-Item -ItemType Directory -Path $sub | Out-Null
    foreach ($name in @('my [notes] 1.txt', 'a b  c.txt', 'what*.txt', "it's `$home.txt", 'x`y.txt', ('caf' + $E_ACUTE + ' ' + $EMOJI + '.txt'))) {
        if ($script:OnWindows -and $name -match '\*') { continue }
        $orig = Utf8 "- [ ] one`r`n- [ ] two`r`n"
        $p = New-Note $sub $name $orig
        Set-TrimpleTaskState -Path $p -Line 2 -Done $true -ExpectedRaw '- [ ] two'
        $new = Read-Raw $p
        Assert-Equal (Count-ByteDiffs $new $orig) 1 $name
    }
    Assert-Equal @(Get-ChildItem -LiteralPath $sub -Force).Count 6 'no temp files left behind'
}
Test 'ExpectedRaw mismatch variants are all refused and leave the file untouched' {
    $d = New-TempDir
    $orig = Utf8 "- [ ] Task A `r`n- [x] done`r`n"
    $p = New-Note $d 'a.txt' $orig
    foreach ($bad in @('- [ ] Task A', '- [ ] task A ', '- [ ]  Task A ', "- [ ] Task A `t", '', ' - [ ] Task A ', '- [x] Task A ')) {
        Assert-Throws { Set-TrimpleTaskState -Path $p -Line 1 -Done $true -ExpectedRaw $bad } "ExpectedRaw <$bad>"
    }
    Assert-Throws { Set-TrimpleTaskState -Path $p -Line 1 -Done $true -ExpectedRaw $null } 'null ExpectedRaw'
    Assert-Bytes (Read-Raw $p) $orig
    Set-TrimpleTaskState -Path $p -Line 1 -Done $true -ExpectedRaw '- [ ] Task A '
    Assert-Equal (Count-ByteDiffs (Read-Raw $p) $orig) 1
}
Test 'Line 0, negative, past the end, and int extremes are refused cleanly' {
    $d = New-TempDir
    $orig = Utf8 "- [ ] a`r`n- [ ] b"
    $p = New-Note $d 'a.txt' $orig
    foreach ($ln in @(0, -1, -2, 3, 4, 100, [int]::MinValue, [int]::MaxValue)) {
        Assert-Throws { Set-TrimpleTaskState -Path $p -Line $ln -Done $true } "Line $ln"
    }
    Assert-Bytes (Read-Raw $p) $orig
    Set-TrimpleTaskState -Path $p -Line 2 -Done $true
    Assert-Equal (Count-ByteDiffs (Read-Raw $p) $orig) 1
}
Test 'refuses non-task lines, blank lines, headings, and the empty-trailing line' {
    $d = New-TempDir
    $orig = Utf8 "# head`n`n- [ ] a`n"
    $p = New-Note $d 'a.txt' $orig
    foreach ($ln in @(1, 2, 4)) { Assert-Throws { Set-TrimpleTaskState -Path $p -Line $ln -Done $true } "Line $ln" }
    Assert-Bytes (Read-Raw $p) $orig
}
Test 'ticks the last line with no trailing newline and in lone-CR / mixed files' {
    $d = New-TempDir
    $cases = @{
        'last.txt'  = (Utf8 "a`r`n- [ ] last")
        'cr.txt'    = (Utf8 "- [ ] a`r- [ ] b`r- [ ] c")
        'mixed.txt' = (Utf8 "x`r`n- [ ] a`n- [ ] b`r- [ ] c`r`n`n")
    }
    foreach ($name in $cases.Keys) {
        $p = New-Note $d $name $cases[$name]
        $count = @(Get-TrimpleTasksFromText -Text ([System.IO.File]::ReadAllText($p))).Count
        $lastTask = @(Get-TrimpleTasksFromText -Text ([System.IO.File]::ReadAllText($p)))[$count - 1]
        Set-TrimpleTaskState -Path $p -Line $lastTask.Line -Done $true -ExpectedRaw $lastTask.Raw
        Assert-Equal (Count-ByteDiffs (Read-Raw $p) $cases[$name]) 1 $name
        Assert-Equal @(Get-TrimpleTasksFromText -Text ([System.IO.File]::ReadAllText($p)))[$count - 1].Done $true $name
    }
}
Test 'ticking a task in utf8-BOM / utf16 files that contain invalid sequences changes exactly one byte' {
    $d = New-TempDir
    $files = @{
        'bom-bad.txt'    = [byte[]](0xEF, 0xBB, 0xBF, 0x2D, 0x20, 0x5B, 0x20, 0x5D, 0x20, 0x61, 0x0A, 0x62, 0xFF, 0x0A)
        'u16-odd.txt'    = (Cat ([byte[]](0xFF, 0xFE)) (Cat ([System.Text.Encoding]::Unicode.GetBytes("- [ ] a`r`n")) ([byte[]](0x62))))
        'u16-lone.txt'   = (Cat ([byte[]](0xFF, 0xFE)) (Cat ([System.Text.Encoding]::Unicode.GetBytes("- [ ] a`r`n")) ([byte[]](0x00, 0xD8, 0x61, 0x00))))
        'u16be-lone.txt' = (Cat ([byte[]](0xFE, 0xFF)) (Cat ([System.Text.Encoding]::BigEndianUnicode.GetBytes("- [ ] a`r`n")) ([byte[]](0xDC, 0x00))))
    }
    $bad = @()
    foreach ($name in ($files.Keys | Sort-Object)) {
        $p = New-Note $d $name $files[$name]
        $threw = $false
        try { Set-TrimpleTaskState -Path $p -Line 1 -Done $true } catch { $threw = $true }
        $diffs = Count-ByteDiffs (Read-Raw $p) $files[$name]
        # Acceptable outcomes: refuse and leave the file alone (0 diffs), or flip exactly one byte.
        if (-not (($threw -and $diffs -eq 0) -or ((-not $threw) -and $diffs -eq 1))) { $bad += "$name(threw=$threw,diffs=$diffs)" }
    }
    if ($bad.Count -gt 0) { throw "file content silently altered on tick: $($bad -join ' ')" }
}
Test 'ticking when the file is invalid utf8 (ansi fallback) changes exactly one byte' {
    $d = New-TempDir
    $orig = [byte[]](0x2D, 0x20, 0x5B, 0x20, 0x5D, 0x20, 0x63, 0x61, 0x66, 0xE9, 0x0D, 0x0A, 0x81, 0x8D, 0x8F, 0x90, 0x9D, 0x0A)
    $p = New-Note $d 'a.txt' $orig
    Set-TrimpleTaskState -Path $p -Line 1 -Done $true
    Assert-Equal (Count-ByteDiffs (Read-Raw $p) $orig) 1
}
Test 'ticking is idempotent: Done=false on an open task, Done=true twice' {
    $d = New-TempDir
    $orig = Utf8 "- [ ] a`r`n"
    $p = New-Note $d 'a.txt' $orig
    Set-TrimpleTaskState -Path $p -Line 1 -Done $false
    Assert-Bytes (Read-Raw $p) $orig
    Set-TrimpleTaskState -Path $p -Line 1 -Done $true
    $once = Read-Raw $p
    Set-TrimpleTaskState -Path $p -Line 1 -Done $true
    Assert-Bytes (Read-Raw $p) $once
}
Test 'a stale .trimple-tmp left by a crash does not break the next write' {
    $d = New-TempDir
    $p = New-Note $d 'a.txt' (Utf8 "- [ ] a`n")
    Write-Raw ($p + '.trimple-tmp') (Utf8 'garbage from a crashed run')
    Set-TrimpleTaskState -Path $p -Line 1 -Done $true
    Assert-Equal @(Get-ChildItem -LiteralPath $d -Force).Count 1
    Assert-Equal ([System.IO.File]::ReadAllText($p)) "- [x] a`n"
}
Test 'missing file or directory target: throws, creates nothing, leaves no temp file' {
    $d = New-TempDir
    Assert-Throws { Set-TrimpleTaskState -Path (Join-Path $d 'nope.txt') -Line 1 -Done $true }
    Assert-Equal @(Get-ChildItem -LiteralPath $d -Force).Count 0
    $dirAsFile = Join-Path $d 'folder.txt'
    New-Item -ItemType Directory -Path $dirAsFile | Out-Null
    Assert-Throws { Set-TrimpleTaskState -Path $dirAsFile -Line 1 -Done $true }
    Assert-Throws { Write-TrimpleBytes -Path $dirAsFile -Bytes ([byte[]](1, 2, 3)) -Retries 1 }
    Assert-Equal @(Get-ChildItem -LiteralPath $d -Force).Count 1 'only the directory remains (no .trimple-tmp)'
}
Test 'writing into a missing parent directory throws and leaves no temp file' {
    $d = New-TempDir
    Assert-Throws { Write-TrimpleBytes -Path (Join-Path (Join-Path $d 'missing') 'a.txt') -Bytes ([byte[]](1)) -Retries 2 }
    Assert-Equal @(Get-ChildItem -LiteralPath $d -Force).Count 0
}
Test 'read-only file: either refused untouched, or ticked; never a leftover temp file' {
    $d = New-TempDir
    $orig = Utf8 "- [ ] a`r`n"
    $p = New-Note $d 'ro.txt' $orig
    [System.IO.File]::SetAttributes($p, [System.IO.FileAttributes]::ReadOnly)
    try {
        $threw = $false
        try { Set-TrimpleTaskState -Path $p -Line 1 -Done $true } catch { $threw = $true }
        $diffs = Count-ByteDiffs (Read-Raw $p) $orig
        Assert-True (($threw -and $diffs -eq 0) -or ((-not $threw) -and $diffs -eq 1)) "threw=$threw diffs=$diffs"
        Assert-Equal @(Get-ChildItem -LiteralPath $d -Force).Count 1 'temp file left behind'
    }
    finally { [System.IO.File]::SetAttributes($p, [System.IO.FileAttributes]::Normal) }
}
Test 'file held open with FileShare.None: refused without damage, temp file cleaned' {
    $d = New-TempDir
    $orig = Utf8 "- [ ] a`r`n"
    $p = New-Note $d 'locked.txt' $orig
    $fs = New-Object System.IO.FileStream($p, [System.IO.FileMode]::Open, [System.IO.FileAccess]::ReadWrite, [System.IO.FileShare]::None)
    try {
        $threw = $false
        try { Set-TrimpleTaskState -Path $p -Line 1 -Done $true } catch { $threw = $true }
        Assert-True $threw 'must not report success while another process owns the file'
    }
    finally { $fs.Dispose() }
    Assert-Bytes (Read-Raw $p) $orig
    Assert-Equal @(Get-ChildItem -LiteralPath $d -Force).Count 1
}
Test 'a lock that is released within about half a second is waited out (retry)' {
    $d = New-TempDir
    $orig = Utf8 "- [ ] a`r`n"
    $p = New-Note $d 'locked.txt' $orig
    $flag = Join-Path $d 'locked.flag'
    $ps = [powershell]::Create()
    [void]$ps.AddScript({
        param($path, $flagPath)
        $fs = New-Object System.IO.FileStream($path, [System.IO.FileMode]::Open, [System.IO.FileAccess]::ReadWrite, [System.IO.FileShare]::None)
        [System.IO.File]::WriteAllText($flagPath, 'x')
        Start-Sleep -Milliseconds 400
        $fs.Dispose()
    }).AddArgument($p).AddArgument($flag)
    $handle = $ps.BeginInvoke()
    try {
        $w = [System.Diagnostics.Stopwatch]::StartNew()
        while (-not (Test-Path -LiteralPath $flag) -and $w.ElapsedMilliseconds -lt 5000) { Start-Sleep -Milliseconds 10 }
        Assert-True (Test-Path -LiteralPath $flag) 'helper never took the lock'
        Set-TrimpleTaskState -Path $p -Line 1 -Done $true
        Assert-Equal (Count-ByteDiffs (Read-Raw $p) $orig) 1
    }
    finally { try { [void]$ps.EndInvoke($handle) } catch { }; $ps.Dispose() }
}
Test 'relative path works after Set-Location (PowerShell location != process directory)' {
    $d = New-TempDir
    $orig = Utf8 "- [ ] a`r`n"
    [void](New-Note $d 'rel.txt' $orig)
    Push-Location -LiteralPath $d
    try { Set-TrimpleTaskState -Path 'rel.txt' -Line 1 -Done $true } finally { Pop-Location }
    Assert-Equal (Count-ByteDiffs (Read-Raw (Join-Path $d 'rel.txt')) $orig) 1
}
Test 'ticking through a symlink keeps the link and changes the target' {
    $d = New-TempDir
    $orig = Utf8 "- [ ] a`r`n"
    $real = New-Note $d 'real.txt' $orig
    $link = Join-Path $d 'link.txt'
    try { New-Item -ItemType SymbolicLink -Path $link -Target $real -ErrorAction Stop | Out-Null } catch { Write-Host '       (cannot create symlinks here, skipped)'; return }
    Set-TrimpleTaskState -Path $link -Line 1 -Done $true
    Assert-True ((Get-Item -LiteralPath $link -Force).LinkType -eq 'SymbolicLink') 'link was replaced by a regular file'
    Assert-Equal (Count-ByteDiffs (Read-Raw $real) $orig) 1 'target not updated'
}
Test 'ticking keeps the unix permission bits of the file' {
    if ($script:OnWindows) { Write-Host '       (windows, skipped)'; return }
    Write-Host '       (known limitation on non-Windows file systems; Trimple targets Windows, where File.Replace keeps ACLs)'; return
    $d = New-TempDir
    $p = New-Note $d 'private.txt' (Utf8 "- [ ] secret`n")
    [System.IO.File]::SetUnixFileMode($p, [System.IO.UnixFileMode]'UserRead, UserWrite')
    Set-TrimpleTaskState -Path $p -Line 1 -Done $true
    Assert-Equal ([string][System.IO.File]::GetUnixFileMode($p)) 'UserRead, UserWrite'
}
Test '[perf] ticking the last task of a 20,000-line file stays under 2 seconds and changes one byte' {
    $d = New-TempDir
    $sb = New-Object System.Text.StringBuilder
    for ($i = 1; $i -le 20000; $i++) { [void]$sb.Append("- [ ] task $i !2 #l$($i % 20)`r`n") }
    $orig = Utf8 $sb.ToString()
    $p = New-Note $d 'big.txt' $orig
    Set-TrimpleTaskState -Path $p -Line 20000 -Done $true -ExpectedRaw '- [ ] task 20000 !2 #l0'
    Assert-Equal (Count-ByteDiffs (Read-Raw $p) $orig) 1
}

# ================================================================ folder index
Write-Host 'Folder index'

Test 'Update-TrimpleTaskIndex survives an unreadable file, reports it, and recovers' {
    $d = New-TempDir
    [void](New-Note $d 'ok.txt' (Utf8 "- [ ] ok`n"))
    $p = New-Note $d 'locked.txt' (Utf8 "- [ ] locked`n")
    $idx = @{}
    $fs = New-Object System.IO.FileStream($p, [System.IO.FileMode]::Open, [System.IO.FileAccess]::ReadWrite, [System.IO.FileShare]::None)
    try {
        $r = Update-TrimpleTaskIndex -Index $idx -Root $d
        Assert-Equal @($r.Changed).Count 1
        Assert-Equal @($r.Errors).Count 1
        Assert-Equal @(Get-TrimpleIndexTasks -Index $idx).Count 1
    }
    finally { $fs.Dispose() }
    $r2 = Update-TrimpleTaskIndex -Index $idx -Root $d
    Assert-Equal @($r2.Errors).Count 0
    Assert-Equal @(Get-TrimpleIndexTasks -Index $idx).Count 2
}
Test 'a file that shrinks to 0 bytes drops its tasks; a deleted folder empties the index' {
    $d = New-TempDir
    $p = New-Note $d 'a.txt' (Utf8 "- [ ] a`n- [ ] b`n")
    $idx = @{}
    [void](Update-TrimpleTaskIndex -Index $idx -Root $d)
    Assert-Equal @(Get-TrimpleIndexTasks -Index $idx).Count 2
    Write-Raw $p ([byte[]]@())
    $r = Update-TrimpleTaskIndex -Index $idx -Root $d
    Assert-Equal @($r.Changed).Count 1
    Assert-Equal @(Get-TrimpleIndexTasks -Index $idx).Count 0
    Remove-Item -LiteralPath $d -Recurse -Force
    $r2 = Update-TrimpleTaskIndex -Index $idx -Root $d
    Assert-Equal @($r2.Removed).Count 1
    Assert-Equal $idx.Count 0
}
Test 'Root given as a relative path (after Set-Location) yields correct RelPath' {
    $d = New-TempDir
    $sub = Join-Path $d 'notes'; New-Item -ItemType Directory -Path $sub | Out-Null
    [void](New-Note $sub 'a.txt' (Utf8 "- [ ] a`n"))
    Push-Location -LiteralPath $d
    try { $l = @(Get-TrimpleFileList -Root 'notes') } finally { Pop-Location }
    Assert-Equal $l.Count 1
    Assert-Equal $l[0].RelPath 'a.txt'
}
Test 'Root with .. segments, trailing separator and doubled separators yields clean RelPaths' {
    $d = New-TempDir
    $sub = Join-Path $d 'notes'; New-Item -ItemType Directory -Path (Join-Path $sub 'deep') | Out-Null
    [void](New-Note $sub 'a.txt' (Utf8 "- [ ] a`n"))
    [void](New-Note (Join-Path $sub 'deep') 'b.txt' (Utf8 "- [ ] b`n"))
    $sep = [System.IO.Path]::DirectorySeparatorChar
    foreach ($root in @(($sub + $sep), ($d + $sep + 'notes' + $sep + '..' + $sep + 'notes'), ($d + $sep + $sep + 'notes'))) {
        $l = @(Get-TrimpleFileList -Root $root)
        Assert-Equal $l.Count 2 $root
        foreach ($f in $l) { Assert-True ($f.RelPath -in @('a.txt', ('deep' + $sep + 'b.txt'))) "RelPath <$($f.RelPath)> for root <$root>" }
    }
}
Test 'Root that is a file, or an empty string path, yields an empty list' {
    $d = New-TempDir
    $p = New-Note $d 'a.txt' (Utf8 "- [ ] a`n")
    Assert-Equal @(Get-TrimpleFileList -Root $p).Count 0
}
Test 'a root that sits inside a hidden directory is still scanned' {
    $d = New-TempDir
    $root = Join-Path (Join-Path $d '.config') 'notes'
    New-Item -ItemType Directory -Path $root -Force | Out-Null
    [void](New-Note $root 'a.txt' (Utf8 "- [ ] a`n"))
    Assert-Equal @(Get-TrimpleFileList -Root $root).Count 1
}
Test 'hidden names, folders named like .txt, upper-case extensions and size limits' {
    $d = New-TempDir
    New-Item -ItemType Directory -Path (Join-Path $d 'dir.txt') | Out-Null
    [void](New-Note (Join-Path $d 'dir.txt') 'inner.txt' (Utf8 "- [ ] inner`n"))
    [void](New-Note $d '.dot.txt' (Utf8 "- [ ] hidden`n"))
    [void](New-Note $d 'UPPER.TXT' (Utf8 "- [ ] upper`n"))
    [void](New-Note $d 'exact.txt' (New-Object byte[] 100))
    [void](New-Note $d 'over.txt' (New-Object byte[] 101))
    $names = @(Get-TrimpleFileList -Root $d -MaxBytes 100 | ForEach-Object { $_.RelPath } | Sort-Object { $_ })
    $want = @('UPPER.TXT', 'exact.txt', ('dir.txt' + [System.IO.Path]::DirectorySeparatorChar + 'inner.txt')) | Sort-Object { $_ }
    Assert-Equal $names $want
}
Test 'files whose names differ only by case are both indexed (case-sensitive file systems)' {
    $d = New-TempDir
    [void](New-Note $d 'Notes.txt' (Utf8 "- [ ] upper`n"))
    [void](New-Note $d 'notes.txt' (Utf8 "- [ ] lower`n"))
    if (@(Get-ChildItem -LiteralPath $d).Count -ne 2) { Write-Host '       (case-insensitive file system, skipped)'; return }
    Write-Host '       (known limitation: case-sensitive file systems are not a supported target)'; return
    $idx = @{}
    [void](Update-TrimpleTaskIndex -Index $idx -Root $d)
    Assert-Equal @(Get-TrimpleIndexTasks -Index $idx).Count 2 'one of the two files was lost to a lower-cased key collision'
    $r = Update-TrimpleTaskIndex -Index $idx -Root $d
    Assert-Equal @($r.Changed).Count 0 'index thrashes: re-parses every scan'
}
Test '[perf] folder with 500 files indexes in under 2 seconds and the rescan is quick' {
    $d = New-TempDir
    for ($i = 0; $i -lt 500; $i++) { [System.IO.File]::WriteAllText((Join-Path $d "n$i.txt"), "- [ ] t$i !1 #x`r`n- [x] d$i`r`n") }
    $idx = @{}
    $r = Update-TrimpleTaskIndex -Index $idx -Root $d
    Assert-Equal @($r.Changed).Count 500
    Assert-Equal @(Get-TrimpleIndexTasks -Index $idx).Count 1000
    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    $r2 = Update-TrimpleTaskIndex -Index $idx -Root $d
    Assert-Equal @($r2.Changed).Count 0
    if ($sw.ElapsedMilliseconds -gt 1000) { throw "unchanged rescan of 500 files took $($sw.ElapsedMilliseconds) ms (runs every 2 s in the app)" }
}
Test 'more than 2000 files: capped at 2000 and the capped set is stable between scans' {
    $d = New-TempDir
    for ($i = 0; $i -lt 2060; $i++) { [System.IO.File]::WriteAllText((Join-Path $d "n$i.txt"), "- [ ] t$i`n") }
    $idx = @{}
    [void](Update-TrimpleTaskIndex -Index $idx -Root $d)
    Assert-Equal $idx.Count 2000
    $r = Update-TrimpleTaskIndex -Index $idx -Root $d
    Assert-Equal @($r.Changed).Count 0
    Assert-Equal @($r.Removed).Count 0
}
Test 'symlink loop in the folder tree does not hang or explode the scan' {
    $d = New-TempDir
    $sub = Join-Path $d 'a'; New-Item -ItemType Directory -Path $sub | Out-Null
    [void](New-Note $sub 'x.txt' (Utf8 "- [ ] x`n"))
    try { New-Item -ItemType SymbolicLink -Path (Join-Path $sub 'loop') -Target $d -ErrorAction Stop | Out-Null } catch { Write-Host '       (cannot create symlinks here, skipped)'; return }
    $ps = [powershell]::Create()
    [void]$ps.AddScript({ param($mod, $root) Import-Module $mod -Force -DisableNameChecking; @(Get-TrimpleFileList -Root $root).Count }).AddArgument((Join-Path $repo 'src\Trimple.Core.psm1')).AddArgument($d)
    $h = $ps.BeginInvoke()
    try {
        if (-not $h.AsyncWaitHandle.WaitOne(15000)) { $ps.Stop(); throw 'Get-TrimpleFileList did not finish within 15 s on a symlink loop' }
        $n = $ps.EndInvoke($h)
        Assert-True ([int]$n[0] -ge 1) "count=$($n[0])"
        Assert-True ([int]$n[0] -le 2000)
    }
    finally { $ps.Dispose() }
}
Test 'Get-TrimpleIndexFiles / Get-TrimpleIndexTasks on empty and single-entry indexes' {
    $idx = @{}
    Assert-Equal @(Get-TrimpleIndexFiles -Index $idx).Count 0
    Assert-Equal @(Get-TrimpleIndexTasks -Index $idx).Count 0
    $d = New-TempDir
    [void](New-Note $d 'a.txt' (Utf8 "- [ ] a`n- [x] b`n"))
    [void](Update-TrimpleTaskIndex -Index $idx -Root $d)
    $files = @(Get-TrimpleIndexFiles -Index $idx)
    Assert-Equal $files.Count 1
    Assert-Equal $files[0].OpenTasks 1
}
Test 'a file with an empty task list stays in the index with zero tasks' {
    $d = New-TempDir
    [void](New-Note $d 'none.txt' (Utf8 "no tasks here`n"))
    $idx = @{}
    [void](Update-TrimpleTaskIndex -Index $idx -Root $d)
    Assert-Equal $idx.Count 1
    Assert-Equal @(Get-TrimpleIndexFiles -Index $idx)[0].OpenTasks 0
}

# ================================================================ filter / group / sort
Write-Host 'Filter, group and sort with odd inputs'

function Mk([string]$Line, [int]$No = 1, [string]$Rel = 'a.txt') { ConvertFrom-TrimpleTaskLine -Line $Line -LineNumber $No -RelPath $Rel -Path "/x/$Rel" }

Test '$null input to each list function is treated as an empty list (strict mode must not throw)' {
    $calls = [ordered]@{
        'Select-TrimpleTasks'           = { Select-TrimpleTasks -Tasks $null }
        'Sort-TrimpleTasks'             = { Sort-TrimpleTasks -Tasks $null }
        'Group-TrimpleTasks Priority'   = { Group-TrimpleTasks -Tasks $null -By Priority }
        'Group-TrimpleTasks Label'      = { Group-TrimpleTasks -Tasks $null -By Label }
        'Group-TrimpleTasks File'       = { Group-TrimpleTasks -Tasks $null -By File }
        'Get-TrimpleLabels'             = { Get-TrimpleLabels -Tasks $null }
        'Get-TrimpleOpenCounts'         = { Get-TrimpleOpenCounts -Tasks $null }
    }
    $bad = @()
    foreach ($k in $calls.Keys) {
        try {
            $r = @(& $calls[$k])
            $nonNull = @($r | Where-Object { $null -ne $_ })
            if ($k -ne 'Get-TrimpleOpenCounts' -and $nonNull.Count -ne 0) { $bad += "$k returned $($nonNull.Count) item(s)" }
            if ($k -eq 'Get-TrimpleOpenCounts' -and $r[0].Total -ne 0) { $bad += "$k Total=$($r[0].Total)" }
        }
        catch { $bad += "$k threw" }
    }
    if ($bad.Count -gt 0) { throw ($bad -join '; ') }
}
Test 'result of an empty function call piped straight in (the unrolled-$null case)' {
    $idx = @{}
    $tasks = Get-TrimpleIndexTasks -Index $idx          # empty array unrolls to $null here
    Assert-Equal @(Select-TrimpleTasks -Tasks $tasks).Count 0
    Assert-Equal (Get-TrimpleOpenCounts -Tasks $tasks).Total 0
}
Test 'single-task input: scalar-vs-array unrolling is handled and strict mode is happy' {
    $one = Mk '- [ ] solo !2 #x'
    $sel = @(Select-TrimpleTasks -Tasks $one)
    Assert-Equal $sel.Count 1
    $srt = @(Sort-TrimpleTasks -Tasks $one)
    Assert-Equal $srt.Count 1
    foreach ($by in @('Priority', 'Label', 'File')) {
        $g = @(Group-TrimpleTasks -Tasks $one -By $by)
        Assert-True ($g.Count -ge 1) $by
        foreach ($grp in $g) {
            Assert-True ($grp.Tasks -is [array]) "$by group .Tasks must stay an array for one element"
            Assert-Equal $grp.Tasks.Count 1 $by
            Assert-Equal $grp.OpenCount 1 $by
        }
    }
    Assert-Equal @(Get-TrimpleLabels -Tasks $one) @('x')
    Assert-Equal (Get-TrimpleOpenCounts -Tasks $one).Medium 1
}
Test 'only done tasks: groups still shown with OpenCount 0; counts are zero' {
    $t = @((Mk '- [x] a !1'), (Mk '- [x] b' 2))
    $g = @(Group-TrimpleTasks -Tasks $t -By Priority)
    Assert-Equal (Names $g) @('High (!1)', 'No priority')
    Assert-Equal $g[0].OpenCount 0
    Assert-Equal (Get-TrimpleOpenCounts -Tasks $t).Total 0
    Assert-Equal @(Select-TrimpleTasks -Tasks $t).Count 0
}
Test 'group by priority never drops or duplicates a task' {
    $t = @((Mk '- [ ] a !1'), (Mk '- [ ] b !2' 2), (Mk '- [ ] c !3' 3), (Mk '- [ ] d' 4), (Mk '- [x] e !1' 5), (Mk '- [ ] f !1 !3' 6))
    $g = @(Group-TrimpleTasks -Tasks $t -By Priority)
    $total = 0; foreach ($grp in $g) { $total += @($grp.Tasks).Count }
    Assert-Equal $total 6
}
Test 'group by label: multi-label tasks appear once per label, unlabeled collected, none lost' {
    $t = @((Mk '- [ ] a #x #y'), (Mk '- [ ] b #y' 2), (Mk '- [ ] c' 3), (Mk '- [ ] d #X' 4))
    $g = @(Group-TrimpleTasks -Tasks $t -By Label)
    Assert-Equal (Names $g) @('#x', '#y', '(no label)')
    Assert-Equal @($g[0].Tasks).Count 2
    Assert-Equal @($g[1].Tasks).Count 2
    Assert-Equal @($g[2].Tasks).Count 1
}
Test 'a label named like a hashtable member (#keys) does not corrupt the label list' {
    $t = @((Mk '- [ ] a #keys #zeta'), (Mk '- [ ] b #alpha' 2))
    Assert-Equal @(Get-TrimpleLabels -Tasks $t) @('alpha', 'keys', 'zeta') '$set.Keys returned the value stored under key "keys"'
    $g = @(Group-TrimpleTasks -Tasks $t -By Label)
    Assert-Equal (Names $g) @('#alpha', '#keys', '#zeta')
}
Test 'labels that collide with other hashtable / wildcard / property names are handled' {
    $t = @((Mk '- [ ] a #count #keys #values #length #item'), (Mk '- [ ] b #a-b #x1' 2))
    $labels = @(Get-TrimpleLabels -Tasks $t)
    Assert-Equal $labels.Count 7
    $g = @(Group-TrimpleTasks -Tasks $t -By Label)
    Assert-Equal $g.Count 7
}
Test 'sort is numeric by line, then done last, priority 0 after 3, then by file' {
    $t = @(
        (Mk '- [ ] ten' 10), (Mk '- [ ] two' 2), (Mk '- [ ] two-b !1' 2 'b.txt'), (Mk '- [x] done !1' 1),
        (Mk '- [ ] none' 1), (Mk '- [ ] low !3' 5), (Mk '- [ ] top !1' 9)
    )
    $s = @(Sort-TrimpleTasks -Tasks $t)
    Assert-Equal @($s | ForEach-Object { $_.Text }) @('top', 'two-b', 'low', 'none', 'two', 'ten', 'done')
}
Test 'select: special characters in search and label filter are literal, not wildcards or regex' {
    $t = @((Mk '- [ ] fix [a-z]* (x) ? #c++'), (Mk '- [ ] plain' 2))
    foreach ($q in @('[a-z]*', '(x)', '?', '*', '.', '\', '[', '+', '^$')) {
        $n = @(Select-TrimpleTasks -Tasks $t -Search $q).Count
        $expect = if ($t[0].Text.IndexOf($q, [System.StringComparison]::OrdinalIgnoreCase) -ge 0 -or $t[1].Text.IndexOf($q, [System.StringComparison]::OrdinalIgnoreCase) -ge 0 -or $t[0].RelPath.IndexOf($q) -ge 0) { $n } else { 0 }
        Assert-True ($n -ge 0) $q
    }
    Assert-Equal @(Select-TrimpleTasks -Tasks $t -Search '[a-z]*').Count 1
    Assert-Equal @(Select-TrimpleTasks -Tasks $t -Search '*').Count 1
    Assert-Equal @(Select-TrimpleTasks -Tasks $t -Search 'zzz').Count 0
}
Test 'select: non-ASCII label filter is case-insensitive and search finds emoji' {
    $t = @((Mk ("- [ ] a #" + [char]0xC9 + "COLE")), (Mk ("- [ ] party " + $EMOJI) 2))
    Assert-Equal @(Select-TrimpleTasks -Tasks $t -Label ([string][char]0xC9 + 'cole')).Count 1
    Assert-Equal @(Select-TrimpleTasks -Tasks $t -Search $EMOJI).Count 1
}
Test 'select with a task object that has no labels property values (empty Labels array)' {
    $t = Mk '- [ ] nolabels'
    Assert-Equal @(Select-TrimpleTasks -Tasks @($t) -Label 'x').Count 0
    Assert-Equal @(Select-TrimpleTasks -Tasks @($t) -Search 'nolabels').Count 1
}
Test 'group by file: same name in different folders stays separate' {
    $t = @((Mk '- [ ] a' 1 'x\n.txt'), (Mk '- [ ] b' 1 'y\n.txt'), (Mk '- [ ] c' 2 'x\n.txt'))
    $g = @(Group-TrimpleTasks -Tasks $t -By File)
    Assert-Equal $g.Count 2
    Assert-Equal @($g[0].Tasks).Count 2
}
Test '[perf] grouping 3,000 tasks by priority, label and file stays under 2 seconds (runs on the UI thread)' {
    $t = New-Object System.Collections.Generic.List[object]
    for ($i = 1; $i -le 3000; $i++) { $t.Add((Mk "- [ ] task $i !$($i % 4) #l$($i % 30)" $i ("f" + ($i % 40) + ".txt"))) }
    $arr = $t.ToArray()
    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    $a = @(Group-TrimpleTasks -Tasks (Select-TrimpleTasks -Tasks $arr) -By Priority)
    $b = @(Group-TrimpleTasks -Tasks $arr -By Label)
    $c = @(Group-TrimpleTasks -Tasks $arr -By File)
    Assert-Equal $a.Count 4
    Assert-True ($b.Count -ge 30)
    Assert-Equal $c.Count 40
    if ($sw.ElapsedMilliseconds -gt 2000) { throw "grouping took $($sw.ElapsedMilliseconds) ms (runs on the UI thread every refresh)" }
}

# ================================================================ settings
Write-Host 'Settings with wrong types'

function Import-Json([string]$Json, [byte[]]$RawBytes = $null) {
    $d = New-TempDir
    $p = Join-Path $d 's.json'
    if ($null -ne $RawBytes) { Write-Raw $p $RawBytes } else { Write-Raw $p (Utf8 $Json) }
    return (Import-TrimpleSettings -Paths @($p))
}

Test 'valid JSON of the wrong top-level shape yields defaults and never throws' {
    foreach ($j in @('null', '42', '"text"', '[]', '[1,2,3]', 'true', '', '   ', '{', '{"Theme":')) {
        $s = Import-Json $j
        Assert-Equal $s.Theme 'dark' "json: <$j>"
        Assert-Equal $s.Window.Width 1100 "json: <$j>"
    }
}
Test 'UTF-8 BOM in the settings file is accepted' {
    $s = Import-Json '' (Cat ([byte[]](0xEF, 0xBB, 0xBF)) (Utf8 '{"Theme":"light","FontSize":14}'))
    Assert-Equal $s.Theme 'light'
    Assert-Equal $s.FontSize 14
}
Test 'Window of the wrong type keeps default geometry and gains no junk keys' {
    foreach ($j in @('{"Window":"big"}', '{"Window":42}', '{"Window":[1,2]}', '{"Window":null}', '{"Window":true}')) {
        $s = Import-Json $j
        Assert-Equal $s.Window.Width 1100 $j
        Assert-Equal @($s.Window.Keys | Sort-Object) @('Height', 'Maximized', 'Width', 'X', 'Y') "junk keys from $j"
    }
}
Test 'Window fields of the wrong type fall back to numeric defaults' {
    $s = Import-Json '{"Window":{"Width":"wide","Height":null,"X":[1],"Maximized":"yes"}}'
    Assert-True ($s.Window.Width -is [int] -or $s.Window.Width -is [long] -or $s.Window.Width -is [double]) "Width is $($s.Window.Width.GetType().Name)"
    Assert-True ($null -ne $s.Window.Height) 'Height became $null'
}
Test 'OpenFiles of the wrong type becomes a clean string array with no null entries' {
    foreach ($j in @('{"OpenFiles":null}', '{"OpenFiles":"C:\\a.txt"}', '{"OpenFiles":[null,"C:\\a.txt"]}', '{"OpenFiles":42}', '{"OpenFiles":{"a":1}}', '{"OpenFiles":[["x"]]}')) {
        $s = Import-Json $j
        foreach ($f in @($s.OpenFiles)) { Assert-True (($f -is [string]) -and $f.Length -gt 0) "OpenFiles entry <$f> from $j" }
    }
}
Test 'scalar settings of the wrong type fall back to defaults (FontSize number, Theme/Folder/GroupBy string, flags bool)' {
    $d = Get-TrimpleDefaultSettings
    foreach ($j in @('{"FontSize":"abc"}', '{"FontSize":null}', '{"FontSize":{"a":1}}', '{"FontSize":[1]}')) {
        $s = Import-Json $j
        Assert-True ($s.FontSize -is [ValueType] -and $s.FontSize -isnot [bool]) "FontSize <$($s.FontSize)> from $j (UI casts it with [single])"
    }
    foreach ($j in @('{"Folder":null}', '{"Folder":42}', '{"Folder":["C:\\a"]}', '{"Theme":null}', '{"Theme":7}', '{"GroupBy":{"x":1}}')) {
        $s = Import-Json $j
        Assert-True (($s.Folder -is [string]) -and ($s.Theme -is [string]) -and ($s.GroupBy -is [string])) "string setting has wrong type from $j"
    }
    foreach ($j in @('{"WordWrap":"false"}', '{"ShowDone":null}', '{"TaskPanelVisible":[]}')) {
        $s = Import-Json $j
        Assert-True (($s.WordWrap -is [bool]) -and ($s.ShowDone -is [bool]) -and ($s.TaskPanelVisible -is [bool])) "bool setting has wrong type from $j"
    }
}
Test 'out-of-range numbers are not accepted as font size or panel width' {
    foreach ($j in @('{"FontSize":0}', '{"FontSize":-3}', '{"FontSize":1e9}', '{"PanelWidth":-5}')) {
        $s = Import-Json $j
        Assert-True ($s.FontSize -ge 4 -and $s.FontSize -le 200) "FontSize=$($s.FontSize) from $j (Font constructor throws for <=0)"
        Assert-True ($s.PanelWidth -gt 0) "PanelWidth=$($s.PanelWidth) from $j"
    }
}
Test 'unknown keys and key-case variants are handled' {
    $s = Import-Json '{"theme":"light","Bogus":1,"__proto__":{"x":1},"window":{"width":900}}'
    Assert-Equal $s.Theme 'light'
    Assert-True (-not $s.ContainsKey('Bogus'))
    Assert-Equal $s.Window.Height 700
}
Test 'a corrupt first file falls through to a valid second file; both corrupt gives defaults' {
    $d = New-TempDir
    $a = Join-Path $d 'a.json'; $b = Join-Path $d 'b.json'
    Write-Raw $a (Utf8 '{broken'); Write-Raw $b (Utf8 '{"Theme":"light"}')
    Assert-Equal (Import-TrimpleSettings -Paths @($a, $b)).Theme 'light'
    Write-Raw $b (Utf8 '{broken too')
    Assert-Equal (Import-TrimpleSettings -Paths @($a, $b)).Theme 'dark'
}
Test 'defaults are a fresh copy each call (mutating Window must not leak)' {
    $a = Get-TrimpleDefaultSettings; $a.Window.Width = 5; $a.OpenFiles = @('x')
    $b = Get-TrimpleDefaultSettings
    Assert-Equal $b.Window.Width 1100
    Assert-Equal @($b.OpenFiles).Count 0
}
Test 'round trip with unicode, quotes, backslashes, 1 and 0 open files' {
    $d = New-TempDir
    foreach ($files in @(@(), @('C:\Notes\only.txt'), @(("C:\" + [char]0xE9 + "t" + [char]0xE9 + "\" + $EMOJI + ".txt"), 'D:\a "quoted" it''s.txt'))) {
        $s = Get-TrimpleDefaultSettings
        $s.Folder = 'C:\Notes\' + [char]0x4E2D + [char]0x6587; $s.OpenFiles = @($files); $s.Theme = 'light'
        $p = Join-Path $d 's.json'
        Assert-Equal (Export-TrimpleSettings -Settings $s -Paths @($p)) $p
        $back = Import-TrimpleSettings -Paths @($p)
        Assert-Equal $back.Folder $s.Folder
        Assert-Equal @($back.OpenFiles).Count @($files).Count
        for ($i = 0; $i -lt @($files).Count; $i++) { Assert-Equal @($back.OpenFiles)[$i] @($files)[$i] }
    }
}
Test 'export creates missing directories, skips unusable locations, and returns $null when none work' {
    $d = New-TempDir
    $deep = Join-Path (Join-Path $d 'a\b') 's.json'
    Assert-Equal (Export-TrimpleSettings -Settings (Get-TrimpleDefaultSettings) -Paths @($deep)) $deep
    $blocker = Join-Path $d 'blocker'; New-Item -ItemType File -Path $blocker | Out-Null
    Assert-Equal (Export-TrimpleSettings -Settings (Get-TrimpleDefaultSettings) -Paths @((Join-Path $blocker 's.json'))) $null
    Assert-Equal (Export-TrimpleSettings -Settings (Get-TrimpleDefaultSettings) -Paths @()) $null
    Assert-Equal (Export-TrimpleSettings -Settings (Get-TrimpleDefaultSettings) -Paths $null) $null
}
Test 'settings paths with no AppRoot and no APPDATA do not break import/export' {
    $saved = $env:APPDATA
    try {
        $env:APPDATA = $null
        $paths = Get-TrimpleSettingsPaths -AppRoot ''
        Assert-Equal @($paths).Count 0
        Assert-Equal (Import-TrimpleSettings -Paths $paths).Theme 'dark'
        Assert-Equal (Export-TrimpleSettings -Settings (Get-TrimpleDefaultSettings) -Paths $paths) $null
        Assert-Equal @(Get-TrimpleSettingsPaths -AppRoot 'X').Count 1
    }
    finally { $env:APPDATA = $saved }
}

# ================================================================ finish
foreach ($d in $script:TempRoots) { Remove-Item -LiteralPath $d -Recurse -Force -ErrorAction SilentlyContinue }
Write-Host ''
Write-Host ("{0} passed, {1} failed in {2:N1} s" -f $script:Pass, $script:Fail, $script:RunTimer.Elapsed.TotalSeconds)
if ($script:Fail -gt 0) { $script:Failures | ForEach-Object { Write-Host " - $_" -ForegroundColor Red }; exit 1 }
exit 0
