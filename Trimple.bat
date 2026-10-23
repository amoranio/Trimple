@echo off
rem Starts Trimple with no console window left open. Extra arguments are files to open.
start "" /min "%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -ExecutionPolicy Bypass -STA -WindowStyle Hidden -File "%~dp0Trimple.ps1" %*
