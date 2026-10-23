@echo off
rem Same as Trimple.bat but keeps the console open so errors are visible.
"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -ExecutionPolicy Bypass -STA -File "%~dp0Trimple.ps1" %*
pause
