<#
.SYNOPSIS
    Trimple - Notepad-style tabs with a task panel built from a folder of .txt notes.
.PARAMETER Folder
    Notes folder to use for this run (otherwise the saved one, or a first-run prompt).
.PARAMETER SelfTest
    Build the UI headlessly, exercise it, print results and exit (used by CI).
.PARAMETER Open
    Files to open in tabs, e.g.  Trimple.bat notes.txt
#>
param(
    [string]$Folder = '',
    [switch]$SelfTest,
    [Parameter(ValueFromRemainingArguments = $true)][string[]]$Open
)

$ErrorActionPreference = 'Stop'

# WinForms dialogs need a single-threaded apartment. Windows PowerShell 5.1 starts multi-threaded.
if ([System.Threading.Thread]::CurrentThread.GetApartmentState() -ne [System.Threading.ApartmentState]::STA) {
    $exe = (Get-Process -Id $PID).Path
    $argList = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-STA', '-File', ('"' + $PSCommandPath + '"'))
    if ($Folder) { $argList += @('-Folder', ('"' + $Folder + '"')) }
    if ($SelfTest) { $argList += '-SelfTest' }
    foreach ($o in @($Open)) { if ($o) { $argList += ('"' + $o + '"') } }
    $p = Start-Process -FilePath $exe -ArgumentList $argList -Wait -PassThru -NoNewWindow
    exit $p.ExitCode
}

$root = Split-Path -Parent $PSCommandPath
Import-Module (Join-Path $root 'src\Trimple.Core.psm1') -Force -DisableNameChecking
. (Join-Path $root 'src\Trimple.UI.ps1')

if ($SelfTest) {
    . (Join-Path $root 'src\Trimple.SelfTest.ps1')
    exit (Invoke-TrimpleSelfTest -AppRoot $root)
}

try {
    Start-TrimpleApp -AppRoot $root -Folder $Folder -Open $Open
}
catch {
    Write-TrimpleLog ("fatal: " + $_.Exception.ToString())
    [void][System.Windows.Forms.MessageBox]::Show(
        "Trimple hit an error and has to close.`n`n$($_.Exception.Message)`n`nDetails were written to Trimple.log.",
        'Trimple', 'OK', 'Error')
    exit 1
}
