# Headless UI self-test. Drives the real form through the same functions the event handlers call.
# Run with:  powershell -STA -File Trimple.ps1 -SelfTest      (exit code 0 = pass)
# Keep this file ASCII-only and compatible with Windows PowerShell 5.1.

function Invoke-TrimpleSelfTest {
    param([string]$AppRoot)

    $failures = New-Object System.Collections.Generic.List[string]
    $check = {
        param([bool]$Ok, [string]$Name)
        if ($Ok) { Write-Host "  ok   $Name" } else { Write-Host "  FAIL $Name" -ForegroundColor Red; $failures.Add($Name) }
    }
    $work = Join-Path ([System.IO.Path]::GetTempPath()) ('trimple-selftest-' + [guid]::NewGuid().ToString('N'))
    $notes = Join-Path $work 'notes'
    New-Item -ItemType Directory -Path $notes -Force | Out-Null
    Copy-Item -Path (Join-Path $AppRoot 'tests\sample-notes\*') -Destination $notes -Recurse -Force
    $workFile = Join-Path $notes 'Work.txt'
    $homeFile = Join-Path $notes 'Home.txt'

    try {
        Write-Host 'UI self-test'
        Initialize-TrimpleApp -AppRoot $work -Folder $notes -Headless $true
        $ui = $script:ui
        & $check ($ui.Form.IsHandleCreated) 'form handle is created'
        & $check ($script:Docs.Count -eq 1) 'starts with one untitled tab'

        # --- task panel
        $all = @(Get-TrimpleIndexTasks -Index $script:Index)
        & $check ($all.Count -eq 9) 'indexes 9 tasks from the sample folder'
        & $check ($ui.TaskList.Items.Count -eq 11) 'priority view: 4 headers + 7 open tasks'
        $ui.GroupCombo.SelectedItem = 'Label'
        & $check ($ui.TaskList.Items.Count -ne 11) 'switching to group-by-label rebuilds the list'
        $ui.GroupCombo.SelectedItem = 'Priority'
        $ui.LabelCombo.SelectedItem = 'dev'
        & $check ($ui.TaskList.Items.Count -eq 5) 'label filter dev: 2 headers + 3 tasks'
        $ui.LabelCombo.SelectedIndex = 0
        $ui.Search.Text = 'zzz-no-match'
        & $check ($ui.TaskList.Items.Count -eq 0) 'search with no match empties the list'
        $ui.Search.Text = ''
        $ui.ShowDone.Checked = $true
        & $check ($ui.TaskList.Items.Count -gt 11) 'show completed adds the done tasks'
        $ui.ShowDone.Checked = $false
        & $check ($ui.FileList.Items.Count -eq 3) 'file list shows 3 files'

        # --- open + jump
        $doc = Open-Doc -Path $workFile -Line 3
        & $check ($script:Docs.Count -eq 1 -and $doc.Name -eq 'Work.txt') 'opening a file replaces the empty untitled tab'
        & $check ($doc.Editor.Text.Contains('Write Q3 report')) 'editor shows the file'
        $expect = $doc.Editor.Text.IndexOf('- [ ] Write Q3 report')
        & $check ($doc.Editor.SelectionStart -eq $expect) 'jump-to-line puts the caret on the task'
        & $check ((Open-Doc -Path $workFile) -eq $doc -and $script:Docs.Count -eq 1) 'opening the same file twice reuses the tab'

        # --- tick a task: exactly one byte changes
        $before = [System.IO.File]::ReadAllBytes($workFile)
        $task = @($all | Where-Object { $_.Text -eq 'Write Q3 report' })[0]
        Switch-TaskState -Task $task
        $after = [System.IO.File]::ReadAllBytes($workFile)
        $diff = 0
        if ($after.Length -eq $before.Length) { for ($i = 0; $i -lt $after.Length; $i++) { if ($after[$i] -ne $before[$i]) { $diff++ } } } else { $diff = -1 }
        & $check ($diff -eq 1) 'ticking a task changes exactly one byte on disk'
        & $check ($doc.Editor.Text.Contains('- [x] Write Q3 report')) 'open tab reloads after a tick'
        & $check (-not $doc.Dirty) 'tab is clean after a tick'
        $stillListed = @($ui.TaskList.Items | Where-Object { $_.Kind -eq 'task' -and $_.Task.Text -eq 'Write Q3 report' })
        & $check ($stillListed.Count -eq 0) 'ticked task leaves the open list'

        # --- unsaved edits are protected
        $doc.Editor.AppendText("`r`nscratch")
        & $check ($doc.Dirty) 'typing marks the tab dirty'
        $before = [System.IO.File]::ReadAllBytes($workFile)
        $other = @($all | Where-Object { $_.Text -eq 'Book offsite venue' })[0]
        Switch-TaskState -Task $other
        $after = [System.IO.File]::ReadAllBytes($workFile)
        & $check ($after.Length -eq $before.Length -and (@(Compare-Object $before $after)).Count -eq 0) 'ticking is refused while the tab has unsaved edits'

        # --- external change vs unsaved edits -> conflict, not overwrite
        Start-Sleep -Milliseconds 30
        [System.IO.File]::AppendAllText($workFile, "- [ ] added in notepad`r`n")
        Invoke-Poll
        & $check ($doc.Conflict -eq 'changed') 'external change on a dirty tab raises a conflict'
        & $check ($doc.Editor.Text.Contains('scratch')) 'dirty edits are kept'
        & $check ($ui.ConflictLabel.Text -like '*changed outside*') 'conflict bar explains the problem'
        $tasksNow = @(Get-TrimpleIndexTasks -Index $script:Index | Where-Object { $_.Text -eq 'added in notepad' })
        & $check ($tasksNow.Count -eq 1) 'task panel picks up the Notepad edit'

        # --- save preserves CRLF and clears the conflict path
        $doc.Stamp = Get-FileStamp -Path $workFile
        $doc.SeenStamp = ''; $doc.Conflict = ''
        $saved = Save-Doc -Doc $doc
        & $check ($saved -and -not $doc.Dirty) 'save succeeds and clears dirty'
        $bytes = [System.IO.File]::ReadAllBytes($workFile)
        $bareLf = 0
        for ($i = 0; $i -lt $bytes.Length; $i++) { if ($bytes[$i] -eq 10 -and ($i -eq 0 -or $bytes[$i - 1] -ne 13)) { $bareLf++ } }
        & $check ($bareLf -eq 0) 'saving a CRLF file keeps CRLF line endings'

        # --- clean tab auto-reloads on external change
        Start-Sleep -Milliseconds 30
        [System.IO.File]::AppendAllText($workFile, "- [ ] second external edit`r`n")
        Invoke-Poll
        & $check ($doc.Editor.Text.Contains('second external edit') -and $doc.Conflict -eq '') 'clean tab reloads silently'

        # --- LF file keeps LF
        $d2 = Open-Doc -Path $homeFile
        & $check ($d2.Eol -eq "`n") 'LF file is detected as LF'
        $d2.Editor.AppendText("`r`n- [ ] new home task")
        [void](Save-Doc -Doc $d2)
        $hb = [System.IO.File]::ReadAllBytes($homeFile)
        & $check (-not ($hb -contains 13)) 'saving an LF file does not introduce CR'

        # --- tab switching, untitled, close
        & $check ($script:Docs.Count -eq 2) 'two tabs open'
        Switch-Doc -Step 1
        & $check ($script:ActiveDoc -eq $doc) 'next-tab wraps around'
        $u = New-UntitledDoc
        & $check ($script:Docs.Count -eq 3 -and $u.Name -like 'Untitled*') 'new tab is untitled'
        [void](Close-Doc -Doc $u)
        [void](Close-Doc -Doc $d2)
        [void](Close-Doc -Doc $doc)
        & $check ($script:Docs.Count -eq 1) 'closing the last file leaves one empty tab'

        # --- appearance
        $err = $null
        try { Set-Theme -Name 'light'; Set-Theme -Name 'dark' } catch { $err = $_.Exception.Message }
        & $check ($null -eq $err) ('theme switching works' + $(if ($err) { ": $err" } else { '' }))
        $ui.Form.PerformLayout()
        $script:LastUiError = $null
        foreach ($ctl in @($ui.TaskList, $ui.FileList, $ui.Strip)) {
            if ($ctl.Width -lt 10 -or $ctl.Height -lt 10) { $ctl.Size = New-Object System.Drawing.Size(300, 240) }
            $bmp = New-Object System.Drawing.Bitmap($ctl.Width, $ctl.Height)
            try { $ctl.DrawToBitmap($bmp, (New-Object System.Drawing.Rectangle(0, 0, $ctl.Width, $ctl.Height))) }
            catch { $script:LastUiError = $_.Exception.ToString() }
            finally { $bmp.Dispose() }
        }
        & $check ($null -eq $script:LastUiError) ('owner-drawn controls paint without errors' + $(if ($script:LastUiError) { ': ' + $script:LastUiError } else { '' }))

        $missing = @()
        $wanted = @{ 'Save' = 'Control, S'; 'Next tab' = 'Control, Tab'; 'Task panel' = 'Control, B'; 'Open in Notepad' = 'Control, Shift, N' }
        $items = New-Object System.Collections.Generic.List[object]
        $walk = $null
        $walk = { param($it) $items.Add($it); if ($it -is [System.Windows.Forms.ToolStripMenuItem]) { foreach ($s in $it.DropDownItems) { & $walk $s } } }
        foreach ($top in $ui.Menu.Items) { & $walk $top }
        foreach ($k in $wanted.Keys) {
            $hit = @($items | Where-Object { $_ -is [System.Windows.Forms.ToolStripMenuItem] -and ($_.Text -replace '&', '') -eq $k })
            $want = [System.Enum]::Parse([System.Windows.Forms.Keys], $wanted[$k])
            if ($hit.Count -ne 1 -or $hit[0].ShortcutKeys -ne $want) { $missing += $k }
        }
        & $check ($missing.Count -eq 0) ('menu shortcuts are bound' + $(if ($missing.Count) { ': missing ' + ($missing -join ', ') } else { '' }))

        Save-AppState
        & $check (Test-Path -LiteralPath (Join-Path $work 'Trimple.settings.json')) 'settings are saved'
    }
    catch {
        Write-Host ("  FAIL unexpected error: " + $_.Exception.ToString()) -ForegroundColor Red
        $failures.Add('unexpected error')
    }
    finally {
        try { if ($script:ui.ContainsKey('Form')) { $script:ui.Timer.Dispose(); $script:ui.Form.Dispose() } } catch { }
        Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
    }

    Write-Host ''
    if ($failures.Count -eq 0) { Write-Host 'UI self-test: all checks passed'; return 0 }
    Write-Host ("UI self-test: {0} check(s) failed" -f $failures.Count) -ForegroundColor Red
    return 1
}
