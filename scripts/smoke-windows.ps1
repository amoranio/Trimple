# Smoke test for the built Trimple.exe on a real Windows machine (run by CI).
# Starts the exe without a window, then checks the API, the security guards, tick-to-file,
# a file locked the way an editor locks it, and the Notepad hand-off.
param([string]$Exe = 'dist\Trimple.exe')
$ErrorActionPreference = 'Stop'

function Assert($cond, $msg) { if (-not $cond) { throw "SMOKE FAIL: $msg" } else { Write-Host "  ok   $msg" } }
function Status($scriptBlock) { try { & $scriptBlock | Out-Null; return 200 } catch { return [int]$_.Exception.Response.StatusCode } }

$root = Split-Path -Parent $PSScriptRoot
$tmp = Join-Path ([IO.Path]::GetTempPath()) ('trimple-smoke-' + [guid]::NewGuid().ToString('N'))
$notes = Join-Path $tmp 'notes'
New-Item -ItemType Directory -Path $notes -Force | Out-Null
Copy-Item -Path (Join-Path $root 'testdata\sample-notes\*') -Destination $notes -Recurse
$cfg = Join-Path $tmp 'settings.json'

$exePath = (Resolve-Path (Join-Path $root $Exe)).Path
Write-Host "Starting $exePath"
$proc = Start-Process -FilePath $exePath -PassThru -WindowStyle Hidden -ArgumentList @('-no-browser', '-keep-alive', '-port', '0', '-token', 'smoke', '-config', $cfg, '-folder', ('"' + $notes + '"'))
try {
    $log = Join-Path $tmp 'trimple.log'
    $port = $null
    for ($i = 0; $i -lt 75 -and -not $port; $i++) {
        Start-Sleep -Milliseconds 200
        if (Test-Path $log) { $m = Select-String -Path $log -Pattern 'listening on port (\d+)'; if ($m) { $port = $m.Matches[0].Groups[1].Value } }
    }
    Assert ($null -ne $port) 'the exe started and logged its port'
    $base = "http://127.0.0.1:$port"
    $h = @{ 'X-Trimple-Token' = 'smoke' }

    $st = Invoke-RestMethod "$base/api/state" -Headers $h
    Assert ($st.totalTasks -eq 9 -and $st.counts.total -eq 7) 'indexes the sample notes (9 tasks, 7 open)'
    Assert ((Invoke-WebRequest "$base/" -UseBasicParsing).StatusCode -eq 200) 'serves the embedded UI'
    Assert ((Invoke-WebRequest "$base/app.js" -UseBasicParsing).Content.Length -gt 5000) 'serves app.js'

    Assert ((Status { Invoke-WebRequest "$base/api/state" -UseBasicParsing }) -eq 401) 'API without a token is refused'
    Assert ((Status { Invoke-WebRequest "$base/api/state" -UseBasicParsing -Headers @{ 'X-Trimple-Token' = 'smoke'; Host = 'evil.example' } }) -eq 403) 'a foreign Host header is refused'

    $work = Join-Path $notes 'Work.txt'
    $before = [IO.File]::ReadAllBytes($work)
    $body = @{ path = 'Work.txt'; line = 3; done = $true; raw = '- [ ] Write Q3 report !1 #work' } | ConvertTo-Json
    Invoke-RestMethod "$base/api/task" -Method Post -Headers $h -ContentType 'application/json' -Body $body | Out-Null
    $after = [IO.File]::ReadAllBytes($work)
    $diff = 0; for ($i = 0; $i -lt $after.Length; $i++) { if ($after[$i] -ne $before[$i]) { $diff++ } }
    Assert ($after.Length -eq $before.Length -and $diff -eq 1) 'ticking a task changes exactly one byte on disk'
    Assert (@(Get-ChildItem $notes -Filter '*.trimple-tmp' -Recurse).Count -eq 0) 'no temp files left behind'

    # A file held open the way an editor holds it: the tick must wait it out and succeed.
    $lockJob = Start-Job -ScriptBlock { param($p) $fs = [IO.File]::Open($p, 'Open', 'ReadWrite', 'None'); Start-Sleep -Milliseconds 900; $fs.Dispose() } -ArgumentList $work
    Start-Sleep -Milliseconds 300
    $body2 = @{ path = 'Work.txt'; line = 3; done = $false; raw = '- [x] Write Q3 report !1 #work' } | ConvertTo-Json
    $sw = [Diagnostics.Stopwatch]::StartNew()
    Invoke-RestMethod "$base/api/task" -Method Post -Headers $h -ContentType 'application/json' -Body $body2 | Out-Null
    Wait-Job $lockJob -Timeout 10 | Out-Null; Remove-Job $lockJob -Force
    $restored = [IO.File]::ReadAllBytes($work)
    Assert (($restored.Length -eq $before.Length) -and (-not (Compare-Object $before $restored))) 'a briefly locked file is waited out and the original bytes come back'

    # The Notepad hand-off launches the real notepad.exe.
    $existing = @(Get-Process notepad -ErrorAction SilentlyContinue | ForEach-Object Id)
    $np = Invoke-RestMethod "$base/api/notepad" -Method Post -Headers $h -ContentType 'application/json' -Body (@{ path = 'Home.txt' } | ConvertTo-Json)
    Assert ($np.ok -eq $true) '/api/notepad reports success'
    $started = $false
    for ($i = 0; $i -lt 25 -and -not $started; $i++) { Start-Sleep -Milliseconds 200; $started = @(Get-Process notepad -ErrorAction SilentlyContinue | Where-Object { $existing -notcontains $_.Id }).Count -gt 0 }
    Assert $started 'Notepad actually started'
    Get-Process notepad -ErrorAction SilentlyContinue | Where-Object { $existing -notcontains $_.Id } | Stop-Process -Force -ErrorAction SilentlyContinue

    Assert ((Status { Invoke-WebRequest "$base/api/file?path=..%5CWork.txt" -UseBasicParsing -Headers $h }) -eq 400) 'path traversal with a backslash is refused'
    Assert ((Status { Invoke-WebRequest "$base/api/file?path=CON.txt" -UseBasicParsing -Headers $h }) -eq 400) 'a reserved device name is refused'
    Write-Host 'Windows smoke test passed'
}
finally {
    if ($proc -and -not $proc.HasExited) { Stop-Process -Id $proc.Id -Force }
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
