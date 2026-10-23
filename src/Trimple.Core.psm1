# Trimple core: file I/O, task parsing, indexing, grouping and settings.
# No UI code lives here so it can be unit tested without WinForms.
# Keep this file ASCII-only and compatible with Windows PowerShell 5.1.

Set-StrictMode -Version 2.0

try { [System.Text.Encoding]::RegisterProvider([System.Text.CodePagesEncodingProvider]::Instance) } catch { }

$script:EolRegex  = '(\r\n|\n|\r)'
$script:TaskRegex = [regex]'^(?<pre>[ \t]*[-*+][ \t]+\[)(?<mark>[ xX])(?<post>\][ \t]+)(?<body>.*)$'
$script:PriorityRegex = [regex]'(?<!\S)!([1-3])(?!\S)'
$script:LabelRegex    = [regex]'(?<!\S)#([\p{L}\p{N}_-]+)(?=$|[\s,;.!?)])'

# ---------------------------------------------------------------- encoding

function Get-TrimpleAnsiEncoding {
    try { return [System.Text.Encoding]::GetEncoding(1252) }
    catch { return [System.Text.Encoding]::GetEncoding(28591) }
}

function Read-TrimpleBytes {
    param([Parameter(Mandatory)][string]$Path)
    $share = [System.IO.FileShare]::ReadWrite -bor [System.IO.FileShare]::Delete
    $fs = New-Object System.IO.FileStream($Path, [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read, $share)
    try {
        $ms = New-Object System.IO.MemoryStream
        $fs.CopyTo($ms)
        return ,$ms.ToArray()
    }
    finally { $fs.Dispose() }
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
    return [pscustomobject]@{
        Path = $Path
        Text = $dec.Text
        Kind = $dec.Kind
        Eol  = (Get-TrimpleDominantEol -Text $dec.Text)
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
        [int]$Retries = 4
    )
    $tmp = $Path + '.trimple-tmp'
    for ($attempt = 1; $attempt -le $Retries; $attempt++) {
        try {
            [System.IO.File]::WriteAllBytes($tmp, $Bytes)
            if (Test-Path -LiteralPath $Path) {
                [System.IO.File]::Replace($tmp, $Path, [NullString]::Value)
            }
            else {
                [System.IO.File]::Move($tmp, $Path)
            }
            return
        }
        catch [System.IO.IOException] {
            if (Test-Path -LiteralPath $tmp) { Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue }
            if ($attempt -eq $Retries) { throw }
            Start-Sleep -Milliseconds (120 * $attempt)
        }
        catch {
            if (Test-Path -LiteralPath $tmp) { Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue }
            throw
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
    if ($m.Groups['body'].Value.Trim().Length -eq 0) { return $null }
    $body = ConvertFrom-TrimpleTaskBody -Body $m.Groups['body'].Value
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
    $inFence = $false
    $lineNo = 0
    for ($i = 0; $i -lt $parts.Length; $i += 2) {
        $lineNo++
        $line = $parts[$i]
        if ($line -match '^\s*(```|~~~)') { $inFence = -not $inFence; continue }
        if ($inFence) { continue }
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
    $rootFull = [System.IO.Path]::GetFullPath($Root).TrimEnd('\', '/')
    if (-not (Test-Path -LiteralPath $Root -PathType Container)) { return @() }
    $found = New-Object System.Collections.Generic.List[object]
    $items = Get-ChildItem -LiteralPath $Root -Filter '*.txt' -Recurse -File -ErrorAction SilentlyContinue
    foreach ($f in $items) {
        if ($f.Extension -ine '.txt') { continue }          # '*.txt' also matches '.txtx' style names
        if ($f.Length -gt $MaxBytes) { continue }
        $rel = $f.FullName.Substring([Math]::Min($rootFull.Length, $f.FullName.Length)).TrimStart('\', '/')
        if ($rel -match '(^|[\\/])\.') { continue }          # hidden folders/files such as .git
        $found.Add([pscustomobject]@{
            Path    = $f.FullName
            RelPath = $rel
            Stamp   = ('{0}:{1}' -f $f.LastWriteTimeUtc.Ticks, $f.Length)
        })
        if ($found.Count -ge $MaxFiles) { break }
    }
    return $found.ToArray()
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
    foreach ($f in (Get-TrimpleFileList -Root $Root)) {
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
    return [pscustomobject]@{ Changed = $changed.ToArray(); Removed = $removed.ToArray(); Errors = $errors.ToArray() }
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
    $set = @{}
    foreach ($t in @($Tasks)) { foreach ($l in $t.Labels) { $set[$l] = $true } }
    return @($set.Keys | Sort-Object)
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
    $tasks = @($Tasks)
    $groups = New-Object System.Collections.Generic.List[object]
    $add = {
        param($name, $items)
        $items = @(Sort-TrimpleTasks -Tasks $items)
        if ($items.Count -gt 0) {
            $groups.Add([pscustomobject]@{
                Name = $name
                Tasks = $items
                OpenCount = @($items | Where-Object { -not $_.Done }).Count
            })
        }
    }
    switch ($By) {
        'Priority' {
            & $add 'High (!1)'    @($tasks | Where-Object { $_.Priority -eq 1 })
            & $add 'Medium (!2)'  @($tasks | Where-Object { $_.Priority -eq 2 })
            & $add 'Low (!3)'     @($tasks | Where-Object { $_.Priority -eq 3 })
            & $add 'No priority'  @($tasks | Where-Object { $_.Priority -eq 0 })
        }
        'Label' {
            foreach ($l in (Get-TrimpleLabels -Tasks $tasks)) {
                & $add ('#' + $l) @($tasks | Where-Object { $_.Labels -contains $l })
            }
            & $add '(no label)' @($tasks | Where-Object { @($_.Labels).Count -eq 0 })
        }
        'File' {
            foreach ($f in @($tasks | ForEach-Object { $_.RelPath } | Sort-Object -Unique)) {
                & $add $f @($tasks | Where-Object { $_.RelPath -eq $f })
            }
        }
    }
    return $groups.ToArray()
}

function Get-TrimpleOpenCounts {
    param([object[]]$Tasks)
    $open = @(@($Tasks) | Where-Object { -not $_.Done })
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
                if ($prop.Name -eq 'Window' -and $null -ne $prop.Value) {
                    foreach ($wp in $prop.Value.PSObject.Properties) { $s.Window[$wp.Name] = $wp.Value }
                }
                elseif ($prop.Name -eq 'OpenFiles') { $s.OpenFiles = @($prop.Value) }
                elseif ($s.ContainsKey($prop.Name)) { $s[$prop.Name] = $prop.Value }
            }
            return $s
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
            [System.IO.File]::WriteAllText($p, $json, $enc)
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
    Get-TrimpleDefaultSettings, Get-TrimpleSettingsPaths, Import-TrimpleSettings, Export-TrimpleSettings
