@echo off
rem Same as Trimple.bat but keeps the console open so errors are visible.
powershell.exe -NoProfile -ExecutionPolicy Bypass -STA -File "%~dp0Trimple.ps1" %*
pause
