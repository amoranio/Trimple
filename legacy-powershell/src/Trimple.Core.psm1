# Trimple core: file I/O, task parsing, indexing, grouping and settings.
# No UI code lives here so it can be unit tested without WinForms.
# Keep this file ASCII-only and compatible with Windows PowerShell 5.1.

Set-StrictMode -Version 2.0

try { [System.Text.Encoding]::RegisterProvider([System.Text.CodePagesEncodingProvider]::Instance) } catch { }

$script:EolRegex  = '(\r\n|\n|\r)'
$script:TaskRegex = [regex]'^(?<pre>[ \t]*[-*+][ \t]+\[)(?<mark>[ xX])(?<post>\][ \t]+)(?<body>.*)$'
$script:PriorityRegex = [regex]'(?<!\S)!([1-3])(?!\S)'
$script:LabelRegex    = [regex]'(?<!\S)#((?:\p{L}|[\uD800-\uDBFF][\uDC00-\uDFFF])(?:[\p{L}\p{N}\p{M}_-]|[\uD800-\uDBFF][\uDC00-\uDFFF])*)(?=$|[\s,;.!?)])'
$script:FenceRegex    = [regex]'^\s*(?<f>`{3,}|~{3,})(?<rest>.*)$'

# ---------------------------------------------------------------- encoding

function Get-TrimpleAnsiEncoding {
    $cp = 0
    try { $cp = [System.Globalization.CultureInfo]::CurrentCulture.TextInfo.ANSICodePage } catch { }
    if ($cp -le 0 -or $cp -eq 65001) { $cp = 1252 }
    try { return [System.Text.Encoding]::GetEncoding($cp) } catch { }
    try { return [System.Text.Encoding]::GetEncoding(1252) } catch { }
    return [System.Text.Encoding]::GetEncoding(28591)
}

# .NET exceptions raised through New-Object / method calls arrive wrapped; look at the real one.
function Get-TrimpleRootException {
    param($ErrorRecord)
    $ex = $ErrorRecord.Exception
    while ($null -ne $ex.InnerException) { $ex = $ex.InnerException }
    return $ex
}

function Read-TrimpleBytes {
    param([Parameter(Mandatory)][string]$Path, [int]$Retries = 6)
    $Path = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Path)
    $share = [System.IO.FileShare]::ReadWrite -bor [System.IO.FileShare]::Delete
    for ($attempt = 1; $attempt -le $Retries; $attempt++) {
        try {
            $fs = New-Object System.IO.FileStream($Path, [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read, $share)
            try {
                $ms = New-Object System.IO.MemoryStream
                $fs.CopyTo($ms)
                return ,$ms.ToArray()
            }
            finally { $fs.Dispose() }
        }
        catch {
            $root = Get-TrimpleRootException -ErrorRecord $_
            $transient = ($root -is [System.IO.IOException]) -and -not ($root -is [System.IO.FileNotFoundException]) -and -not ($root -is [System.IO.DirectoryNotFoundException])
            if (-not $transient -or $attempt -eq $Retries) { throw }
            Start-Sleep -Milliseconds (100 * $attempt)      # e.g. Notepad or a sync client holds the file for a moment
        }
    }
}

# Returns @{ Text; Kind } where Kind is utf8 | utf8bom | utf16le | utf16be | ansi.
function ConvertFrom-TrimpleBytes {
    param([Parameter(Mandatory)][AllowEmptyCollection()][byte[]]$Bytes)
    $n = $Bytes.Length
    if ($n -ge 3 -and $Bytes[0] -eq 0xEF -and $Bytes[1] -eq 0xBB -and $Bytes[2] -eq 0xBF) {
        $enc = New-Object System.Text.UTF8Encoding($true)
        return [pscustomobject]@{ Text = $enc.GetString($Bytes, 3, $n - 3); Kind = 'utf8bom' }
    }
    if ($n -ge 2 -and $Bytes[0] -eq 0xFF -and $Bytes[1] -eq 0xFE) {
        $enc = New-Object System.Text.UnicodeEncoding($false, $true)
        return [pscustomobject]@{ Text = $enc.GetString($Bytes, 2, $n - 2); Kind = 'utf16le' }
    }
    if ($n -ge 2 -and $Bytes[0] -eq 0xFE -and $Bytes[1] -eq 0xFF) {
        $enc = New-Object System.Text.UnicodeEncoding($true, $true)
        return [pscustomobject]@{ Text = $enc.GetString($Bytes, 2, $n - 2); Kind = 'utf16be' }
    }
    try {
        $strict = New-Object System.Text.UTF8Encoding($false, $true)
        return [pscustomobject]@{ Text = $strict.GetString($Bytes); Kind = 'utf8' }
    }
    catch {
        return [pscustomobject]@{ Text = (Get-TrimpleAnsiEncoding).GetString($Bytes); Kind = 'ansi' }
    }
}

# Returns @{ Bytes; Kind }. Kind can change: ansi text that cannot be represented is upgraded to utf8.
function ConvertTo-TrimpleBytes {
    param(
        [Parameter(Mandatory)][AllowEmptyString()][string]$Text,
        [Parameter(Mandatory)][string]$Kind
    )
    switch ($Kind) {
        'utf8bom' {
            $enc = New-Object System.Text.UTF8Encoding($false)
            $body = $enc.GetBytes($Text)
            $out = New-Object byte[] ($body.Length + 3)
            $out[0] = 0xEF; $out[1] = 0xBB; $out[2] = 0xBF
            [Array]::Copy($body, 0, $out, 3, $body.Length)
            return [pscustomobject]@{ Bytes = $out; Kind = $Kind }
        }
        'utf16le' {
            $enc = New-Object System.Text.UnicodeEncoding($false, $false)
            $body = $enc.GetBytes($Text)
            $out = New-Object byte[] ($body.Length + 2)
            $out[0] = 0xFF; $out[1] = 0xFE
            [Array]::Copy($body, 0, $out, 2, $body.Length)
            return [pscustomobject]@{ Bytes = $out; Kind = $Kind }
        }
        'utf16be' {
            $enc = New-Object System.Text.UnicodeEncoding($true, $false)
            $body = $enc.GetBytes($Text)
            $out = New-Object byte[] ($body.Length + 2)
            $out[0] = 0xFE; $out[1] = 0xFF
            [Array]::Copy($body, 0, $out, 2, $body.Length)
            return [pscustomobject]@{ Bytes = $out; Kind = $Kind }
        }
        'ansi' {
            $ansi = Get-TrimpleAnsiEncoding
            $bytes = $ansi.GetBytes($Text)
            if ($ansi.GetString($bytes) -ceq $Text) {
                return [pscustomobject]@{ Bytes = $bytes; Kind = 'ansi' }
            }
            # Typed characters the old code page cannot hold: upgrade rather than write '?'.
            $enc = New-Object System.Text.UTF8Encoding($false)
            return [pscustomobject]@{ Bytes = $enc.GetBytes($Text); Kind = 'utf8' }
        }
        default {
            $enc = New-Object System.Text.UTF8Encoding($false)
            return [pscustomobject]@{ Bytes = $enc.GetBytes($Text); Kind = 'utf8' }
        }
    }
}

function Read-TrimpleFile {
    param([Parameter(Mandatory)][string]$Path)
    $bytes = Read-TrimpleBytes -Path $Path
    $dec = ConvertFrom-TrimpleBytes -Bytes $bytes
    # Lossless = writing the text back gives exactly the bytes we read. When it does not (stray
    # invalid bytes, NULs) the file must not be rewritten, or those bytes would be silently changed.
    $back = ConvertTo-TrimpleBytes -Text $dec.Text -Kind $dec.Kind
    $same = ($back.Kind -eq $dec.Kind -and $back.Bytes.Length -eq $bytes.Length)
    if ($same) {
        for ($i = 0; $i -lt $bytes.Length; $i++) { if ($back.Bytes[$i] -ne $bytes[$i]) { $same = $false; break } }
    }
    $hasNul = ($dec.Text.IndexOf([char]0) -ge 0)
    return [pscustomobject]@{
        Path     = $Path
        Text     = $dec.Text
        Kind     = $dec.Kind
        Eol      = (Get-TrimpleDominantEol -Text $dec.Text)
        Lossless = ($same -and -not $hasNul)
        Problem  = $(if ($hasNul) { 'contains NUL characters' } elseif (-not $same) { 'contains bytes that are not valid for its encoding' } else { '' })
    }
}

# Most common line ending in the text. Ties and no-newline files default to CRLF.
function Get-TrimpleDominantEol {
    param([AllowEmptyString()][string]$Text)
    $crlf = [regex]::Matches($Text, '\r\n').Count
    $lf   = [regex]::Matches($Text, '(?<!\r)\n').Count
    $cr   = [regex]::Matches($Text, '\r(?!\n)').Count
    if ($lf -gt $crlf -and $lf -ge $cr) { return "`n" }
    if ($cr -gt $crlf -and $cr -gt $lf) { return "`r" }
    return "`r`n"
}

function Write-TrimpleBytes {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][byte[]]$Bytes,
        [int]$Retries = 6
    )
    $Path = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Path)
    try {
        $item = Get-Item -LiteralPath $Path -Force -ErrorAction Stop
        if ($item.LinkType -eq 'SymbolicLink' -and $item.Target) {
            $target = [string]@($item.Target)[0]
            if (-not [System.IO.Path]::IsPathRooted($target)) { $target = Join-Path (Split-Path -Parent $Path) $target }
            $Path = [System.IO.Path]::GetFullPath($target)      # write through the link instead of replacing it
        }
    }
    catch { }
    $tmp = $Path + '.trimple-tmp'
    for ($attempt = 1; $attempt -le $Retries; $attempt++) {
        try {
            $fs = New-Object System.IO.FileStream($tmp, [System.IO.FileMode]::Create, [System.IO.FileAccess]::Write, [System.IO.FileShare]::None)
            try { $fs.Write($Bytes, 0, $Bytes.Length); $fs.Flush($true) } finally { $fs.Dispose() }
            if (Test-Path -LiteralPath $Path) {
                [System.IO.File]::Replace($tmp, $Path, [NullString]::Value)
            }
            else {
                [System.IO.File]::Move($tmp, $Path)
            }
            return
        }
        catch {
            $root = Get-TrimpleRootException -ErrorRecord $_
            # File.Replace can fail after it already removed the original: never delete the only copy.
            if (-not (Test-Path -LiteralPath $Path) -and (Test-Path -LiteralPath $tmp)) {
                [System.IO.File]::Move($tmp, $Path)
                return
            }
            if (Test-Path -LiteralPath $tmp) { Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue }
            $transient = ($root -is [System.IO.IOException]) -and -not ($root -is [System.IO.DirectoryNotFoundException])
            if (-not $transient -or $attempt -eq $Retries) { throw }
            Start-Sleep -Milliseconds (120 * $attempt)
        }
    }
}

# ---------------------------------------------------------------- task parsing

function ConvertFrom-TrimpleTaskBody {
    param([AllowEmptyString()][string]$Body)
    $priority = 0
    $pm = $script:PriorityRegex.Match($Body)
    if ($pm.Success) { $priority = [int]$pm.Groups[1].Value }

    $labels = New-Object System.Collections.Generic.List[string]
    foreach ($lm in $script:LabelRegex.Matches($Body)) {
        $name = $lm.Groups[1].Value.ToLowerInvariant()
        if (-not $labels.Contains($name)) { $labels.Add($name) }
    }

    $text = $script:PriorityRegex.Replace($Body, '')
    $text = $script:LabelRegex.Replace($text, '')
    $text = ([regex]::Replace($text, '\s{2,}', ' ')).Trim()
    if ($text.Length -eq 0) { $text = $Body.Trim() }

    return [pscustomobject]@{ Priority = $priority; Labels = $labels.ToArray(); Text = $text }
}

function ConvertFrom-TrimpleTaskLine {
    param(
        [AllowEmptyString()][string]$Line,
        [string]$Path = '',
        [string]$RelPath = '',
        [int]$LineNumber = 0
    )
    $m = $script:TaskRegex.Match($Line)
    if (-not $m.Success) { return $null }
    $bodyText = $m.Groups['body'].Value
    if ($bodyText.Trim().Length -eq 0) { return $null }
    if ($bodyText.IndexOf('!') -ge 0 -or $bodyText.IndexOf('#') -ge 0) {
        $body = ConvertFrom-TrimpleTaskBody -Body $bodyText
    }
    else {
        $body = [pscustomobject]@{ Priority = 0; Labels = @(); Text = ([regex]::Replace($bodyText, '\s{2,}', ' ')).Trim() }
    }
    return [pscustomobject]@{
        File     = $Path
        RelPath  = $RelPath
        Line     = $LineNumber
        Done     = ($m.Groups['mark'].Value -ne ' ')
        Priority = $body.Priority
        Labels   = $body.Labels
        Text     = $body.Text
        Raw      = $Line
    }
}

function Get-TrimpleTasksFromText {
    param(
        [AllowEmptyString()][string]$Text,
        [string]$Path = '',
        [string]$RelPath = ''
    )
    $tasks = New-Object System.Collections.Generic.List[object]
    $parts = [regex]::Split($Text, $script:EolRegex)
    $fence = ''
    $lineNo = 0
    for ($i = 0; $i -lt $parts.Length; $i += 2) {
        $lineNo++
        $line = $parts[$i]
        $hasBracket = ($line.IndexOf('[') -ge 0)
        if ($fence -ne '' -or $line.IndexOf('`') -ge 0 -or $line.IndexOf('~') -ge 0) {
            $fm = $script:FenceRegex.Match($line)
            if ($fm.Success) {
                $f = $fm.Groups['f'].Value
                if ($fence -eq '') { $fence = $f; continue }
                if ($f[0] -eq $fence[0] -and $f.Length -ge $fence.Length -and $fm.Groups['rest'].Value.Trim().Length -eq 0) { $fence = ''; continue }
            }
            if ($fence -ne '') { continue }
        }
        if (-not $hasBracket) { continue }
        $t = ConvertFrom-TrimpleTaskLine -Line $line -Path $Path -RelPath $RelPath -LineNumber $lineNo
        if ($null -ne $t) { $tasks.Add($t) }
    }
    return $tasks.ToArray()
}

# Flip the checkbox on one line. Only that single character changes; everything else
# (encoding, BOM, line endings, other lines) is written back as it was read.
function Set-TrimpleTaskState {
    param(
        [Parameter(Mandatory)][string]$Path,
        [Parameter(Mandatory)][int]$Line,
        [Parameter(Mandatory)][bool]$Done,
        [string]$ExpectedRaw
    )
    $file = Read-TrimpleFile -Path $Path
    if (-not $file.Lossless) { throw "'$Path' $($file.Problem), so Trimple will not rewrite it. Edit it in Notepad instead." }
    $parts = [regex]::Split($file.Text, $script:EolRegex)
    $idx = 2 * ($Line - 1)
    if ($Line -lt 1 -or $idx -ge $parts.Length) { throw "Line $Line no longer exists in '$Path'. Refresh and try again." }
    $raw = $parts[$idx]
    if ($PSBoundParameters.ContainsKey('ExpectedRaw') -and $raw -cne $ExpectedRaw) {
        throw "The task on line $Line changed on disk. Refresh and try again."
    }
    $m = $script:TaskRegex.Match($raw)
    if (-not $m.Success) { throw "Line $Line in '$Path' is no longer a task." }
    $g = $m.Groups['mark']
    $mark = ' '
    if ($Done) { $mark = 'x' }
    $parts[$idx] = $raw.Substring(0, $g.Index) + $mark + $raw.Substring($g.Index + 1)
    $enc = ConvertTo-TrimpleBytes -Text ($parts -join '') -Kind $file.Kind
    Write-TrimpleBytes -Path $Path -Bytes $enc.Bytes
}

# ---------------------------------------------------------------- folder index

function Get-TrimpleFileList {
    param(
        [Parameter(Mandatory)][string]$Root,
        [long]$MaxBytes = 5MB,
        [int]$MaxFiles = 2000
    )
    $Root = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Root)
    if (-not (Test-Path -LiteralPath $Root -PathType Container)) { return @() }
    $rootFull = [System.IO.Path]::GetFullPath($Root).TrimEnd('\', '/')
    $found = New-Object System.Collections.Generic.List[object]
    $stack = New-Object System.Collections.Generic.Stack[string]
    $stack.Push($rootFull + [System.IO.Path]::DirectorySeparatorChar)
    while ($stack.Count -gt 0 -and $found.Count -lt $MaxFiles) {
        $dir = $stack.Pop()
        try {
            $di = New-Object System.IO.DirectoryInfo($dir)
            $files = $di.GetFiles()                    # filtered by extension below (case-insensitive, no '.txtx' look-alikes)
            $subs = $di.GetDirectories()
        }
        catch { continue }
        foreach ($f in $files) {
            if ($f.Extension -ine '.txt' -or $f.Name.StartsWith('.')) { continue }
            if ($f.Length -gt $MaxBytes) { continue }
            if ($found.Count -ge $MaxFiles) { break }
            $rel = $f.FullName.Substring([Math]::Min($rootFull.Length, $f.FullName.Length)).TrimStart('\', '/')
            $found.Add([pscustomobject]@{
                Path    = $f.FullName
                RelPath = $rel
                Stamp   = ('{0}:{1}' -f $f.LastWriteTimeUtc.Ticks, $f.Length)
            })
        }
        foreach ($d in $subs) {
            if ($d.Name.StartsWith('.')) { continue }                                   # .git and friends
            if (($d.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) { continue }   # junction/symlink loops
            $stack.Push($d.FullName)
        }
    }
    return @($found.ToArray() | Sort-Object { $_.RelPath })
}

# Index: hashtable keyed by lower-case full path -> @{ Path; RelPath; Stamp; Tasks }.
# Re-parses only new or changed files. Returns what changed so callers can react.
function Update-TrimpleTaskIndex {
    param(
        [Parameter(Mandatory)][hashtable]$Index,
        [Parameter(Mandatory)][string]$Root
    )
    $changed = New-Object System.Collections.Generic.List[string]
    $removed = New-Object System.Collections.Generic.List[string]
    $errors  = New-Object System.Collections.Generic.List[string]
    $seen = @{}
    $list = @(Get-TrimpleFileList -Root $Root)
    foreach ($f in $list) {
        $key = $f.Path.ToLowerInvariant()
        $seen[$key] = $true
        $entry = $null
        if ($Index.ContainsKey($key)) { $entry = $Index[$key] }
        if ($null -ne $entry -and $entry.Stamp -eq $f.Stamp) { continue }
        try {
            $file = Read-TrimpleFile -Path $f.Path
            $tasks = @(Get-TrimpleTasksFromText -Text $file.Text -Path $f.Path -RelPath $f.RelPath)
            $Index[$key] = @{ Path = $f.Path; RelPath = $f.RelPath; Stamp = $f.Stamp; Tasks = $tasks }
            $changed.Add($f.Path)
        }
        catch {
            $errors.Add(('{0}: {1}' -f $f.RelPath, $_.Exception.Message))
        }
    }
    foreach ($key in @($Index.Keys)) {
        if (-not $seen.ContainsKey($key)) {
            $removed.Add($Index[$key].Path)
            $Index.Remove($key)
        }
    }
    return [pscustomobject]@{ Changed = $changed.ToArray(); Removed = $removed.ToArray(); Errors = $errors.ToArray(); FileCount = $list.Count; Truncated = ($list.Count -ge 2000) }
}

function Get-TrimpleIndexTasks {
    param([Parameter(Mandatory)][hashtable]$Index)
    $all = New-Object System.Collections.Generic.List[object]
    foreach ($entry in $Index.Values) { foreach ($t in $entry.Tasks) { $all.Add($t) } }
    return $all.ToArray()
}

function Get-TrimpleIndexFiles {
    param([Parameter(Mandatory)][hashtable]$Index)
    return @($Index.Values | Sort-Object { $_.RelPath } | ForEach-Object { [pscustomobject]@{ Path = $_.Path; RelPath = $_.RelPath; OpenTasks = @($_.Tasks | Where-Object { -not $_.Done }).Count } })
}

# ---------------------------------------------------------------- filter / group

function Get-TrimpleLabels {
    param([object[]]$Tasks)
    $set = New-Object System.Collections.Generic.HashSet[string]
    foreach ($t in @($Tasks)) { if ($null -eq $t) { continue }; foreach ($l in $t.Labels) { [void]$set.Add($l) } }
    return @($set | Sort-Object)
}

function Select-TrimpleTasks {
    param(
        [object[]]$Tasks,
        [string]$Label = '',
        [string]$Search = '',
        [bool]$IncludeDone = $false
    )
    $out = New-Object System.Collections.Generic.List[object]
    foreach ($t in @($Tasks)) {
        if ($null -eq $t) { continue }
        if (-not $IncludeDone -and $t.Done) { continue }
        if ($Label -and ($t.Labels -notcontains $Label.ToLowerInvariant())) { continue }
        if ($Search) {
            $hay = ($t.Text + ' ' + ($t.Labels -join ' ') + ' ' + $t.RelPath)
            if ($hay.IndexOf($Search, [System.StringComparison]::OrdinalIgnoreCase) -lt 0) { continue }
        }
        $out.Add($t)
    }
    return $out.ToArray()
}

function Sort-TrimpleTasks {
    param([object[]]$Tasks)
    return @(@($Tasks) | Sort-Object `
        @{ Expression = { [int]$_.Done } }, `
        @{ Expression = { if ($_.Priority -eq 0) { 4 } else { $_.Priority } } }, `
        @{ Expression = { $_.RelPath } }, `
        @{ Expression = { $_.Line } })
}

# Returns an ordered array of @{ Name; Tasks; OpenCount }. Empty groups are omitted.
function Group-TrimpleTasks {
    param(
        [object[]]$Tasks,
        [ValidateSet('Priority', 'Label', 'File')][string]$By = 'Priority'
    )
    $clean = New-Object System.Collections.Generic.List[object]
    foreach ($t in @($Tasks)) { if ($null -ne $t) { $clean.Add($t) } }

    $buckets = @{}
    $order = New-Object System.Collections.Generic.List[string]
    $names = @{}
    $put = {
        param($key, $name, $task)
        if (-not $buckets.ContainsKey($key)) {
            $buckets[$key] = New-Object System.Collections.Generic.List[object]
            $names[$key] = $name
            $order.Add($key)
        }
        $buckets[$key].Add($task)
    }
    foreach ($t in $clean) {
        switch ($By) {
            'Priority' { & $put ([string]$t.Priority) '' $t }
            'File'     { & $put $t.RelPath $t.RelPath $t }
            'Label'    {
                if (@($t.Labels).Count -eq 0) { & $put '~none' '(no label)' $t }
                else { foreach ($l in $t.Labels) { & $put ('#' + $l) ('#' + $l) $t } }
            }
        }
    }
    $keys = @()
    if ($By -eq 'Priority') {
        $fixed = @(@('1', 'High (!1)'), @('2', 'Medium (!2)'), @('3', 'Low (!3)'), @('0', 'No priority'))
        foreach ($p in $fixed) { if ($buckets.ContainsKey($p[0])) { $names[$p[0]] = $p[1]; $keys += $p[0] } }
    }
    elseif ($By -eq 'Label') {
        $keys = @($order | Where-Object { $_ -ne '~none' } | Sort-Object)
        if ($buckets.ContainsKey('~none')) { $keys += '~none' }
    }
    else { $keys = @($order | Sort-Object) }

    $groups = New-Object System.Collections.Generic.List[object]
    foreach ($k in $keys) {
        $items = @(Sort-TrimpleTasks -Tasks $buckets[$k].ToArray())
        $open = 0
        foreach ($i in $items) { if (-not $i.Done) { $open++ } }
        $groups.Add([pscustomobject]@{ Name = $names[$k]; Tasks = $items; OpenCount = $open })
    }
    return $groups.ToArray()
}

function Get-TrimpleOpenCounts {
    param([object[]]$Tasks)
    $open = @(@($Tasks) | Where-Object { $null -ne $_ -and -not $_.Done })
    return [pscustomobject]@{
        Total  = $open.Count
        High   = @($open | Where-Object { $_.Priority -eq 1 }).Count
        Medium = @($open | Where-Object { $_.Priority -eq 2 }).Count
        Low    = @($open | Where-Object { $_.Priority -eq 3 }).Count
        None   = @($open | Where-Object { $_.Priority -eq 0 }).Count
    }
}

# ---------------------------------------------------------------- settings

function Get-TrimpleDefaultSettings {
    return @{
        Folder           = ''
        Theme            = 'dark'
        FontSize         = 11
        WordWrap         = $true
        TaskPanelVisible = $true
        GroupBy          = 'Priority'
        ShowDone         = $false
        OpenFiles        = @()
        Window           = @{ X = -1; Y = -1; Width = 1100; Height = 700; Maximized = $false }
        PanelWidth       = 340
    }
}

# Settings come from a file anyone can edit: coerce every value to a safe type and range.
function Repair-TrimpleSettings {
    param([Parameter(Mandatory)][hashtable]$Settings)
    $d = Get-TrimpleDefaultSettings
    $toInt = {
        param($v, $min, $max, $fallback)
        $n = 0
        if ($null -ne $v -and [int]::TryParse([string]$v, [ref]$n) -and $n -ge $min -and $n -le $max) { return $n }
        return $fallback
    }
    $toBool = {
        param($v, $fallback)
        if ($v -is [bool]) { return $v }
        $b = $false
        if ($null -ne $v -and [bool]::TryParse([string]$v, [ref]$b)) { return $b }
        return $fallback
    }
    $Settings.FontSize = & $toInt $Settings.FontSize 8 32 $d.FontSize
    $Settings.PanelWidth = & $toInt $Settings.PanelWidth 150 900 $d.PanelWidth
    $Settings.WordWrap = & $toBool $Settings.WordWrap $d.WordWrap
    $Settings.TaskPanelVisible = & $toBool $Settings.TaskPanelVisible $d.TaskPanelVisible
    $Settings.ShowDone = & $toBool $Settings.ShowDone $d.ShowDone
    if (@('dark', 'light') -notcontains [string]$Settings.Theme) { $Settings.Theme = $d.Theme }
    if (@('Priority', 'Label', 'File') -notcontains [string]$Settings.GroupBy) { $Settings.GroupBy = $d.GroupBy }
    if ($null -eq $Settings.Folder -or $Settings.Folder -isnot [string]) { $Settings.Folder = '' }
    $files = New-Object System.Collections.Generic.List[string]
    foreach ($f in @($Settings.OpenFiles)) {
        if ($f -is [string] -and $f.Length -gt 0 -and -not $f.StartsWith('\\')) { $files.Add($f) }   # no UNC: would trigger network access at startup
    }
    $Settings.OpenFiles = $files.ToArray()
    if ($Settings.Window -isnot [hashtable]) { $Settings.Window = $d.Window }
    $w = $Settings.Window
    $w.X = & $toInt $w.X -32000 32000 -1
    $w.Y = & $toInt $w.Y -32000 32000 -1
    $w.Width = & $toInt $w.Width 400 10000 $d.Window.Width
    $w.Height = & $toInt $w.Height 300 10000 $d.Window.Height
    $w.Maximized = & $toBool $w.Maximized $false
    return $Settings
}

function Get-TrimpleSettingsPaths {
    param([string]$AppRoot)
    $paths = @()
    if ($AppRoot) { $paths += (Join-Path $AppRoot 'Trimple.settings.json') }
    if ($env:APPDATA) { $paths += (Join-Path (Join-Path $env:APPDATA 'Trimple') 'settings.json') }
    return $paths
}

function Import-TrimpleSettings {
    param([string[]]$Paths)
    $s = Get-TrimpleDefaultSettings
    foreach ($p in $Paths) {
        if (-not (Test-Path -LiteralPath $p -PathType Leaf)) { continue }
        try {
            $raw = Get-Content -LiteralPath $p -Raw -Encoding UTF8
            $obj = $raw | ConvertFrom-Json
            foreach ($prop in $obj.PSObject.Properties) {
                if ($prop.Name -eq 'Window') {
                    if ($prop.Value -is [System.Management.Automation.PSCustomObject]) {
                        foreach ($wp in $prop.Value.PSObject.Properties) { if ($s.Window.ContainsKey($wp.Name)) { $s.Window[$wp.Name] = $wp.Value } }
                    }
                }
                elseif ($prop.Name -eq 'OpenFiles') { $s.OpenFiles = @($prop.Value) }
                elseif ($s.ContainsKey($prop.Name)) { $s[$prop.Name] = $prop.Value }
            }
            return (Repair-TrimpleSettings -Settings $s)
        }
        catch { continue }   # corrupt settings must never stop the app starting
    }
    return $s
}

# Writes to the first location that works (next to the app for a portable setup, then %APPDATA%).
function Export-TrimpleSettings {
    param([Parameter(Mandatory)][hashtable]$Settings, [string[]]$Paths)
    $json = $Settings | ConvertTo-Json -Depth 5
    foreach ($p in $Paths) {
        try {
            $dir = Split-Path -Parent $p
            if (-not (Test-Path -LiteralPath $dir)) { New-Item -ItemType Directory -Path $dir -Force | Out-Null }
            $enc = New-Object System.Text.UTF8Encoding($false)
            $tmp = $p + '.tmp'
            [System.IO.File]::WriteAllText($tmp, $json, $enc)
            if (Test-Path -LiteralPath $p) { [System.IO.File]::Replace($tmp, $p, [NullString]::Value) } else { [System.IO.File]::Move($tmp, $p) }
            return $p
        }
        catch { continue }
    }
    return $null
}

Export-ModuleMember -Function `
    Get-TrimpleAnsiEncoding, Read-TrimpleBytes, ConvertFrom-TrimpleBytes, ConvertTo-TrimpleBytes, `
    Read-TrimpleFile, Get-TrimpleDominantEol, Write-TrimpleBytes, `
    ConvertFrom-TrimpleTaskBody, ConvertFrom-TrimpleTaskLine, Get-TrimpleTasksFromText, Set-TrimpleTaskState, `
    Get-TrimpleFileList, Update-TrimpleTaskIndex, Get-TrimpleIndexTasks, Get-TrimpleIndexFiles, `
    Get-TrimpleLabels, Select-TrimpleTasks, Sort-TrimpleTasks, Group-TrimpleTasks, Get-TrimpleOpenCounts, `
    Get-TrimpleDefaultSettings, Repair-TrimpleSettings, Get-TrimpleSettingsPaths, Import-TrimpleSettings, Export-TrimpleSettings
