@echo off
REM Copies becky's VEGAS Pro scripts into the VEGAS "Script Menu" folder, which
REM lives under C:\Program Files and therefore needs administrator rights.
REM Double-click this, click Yes on the Windows prompt, done.
REM ASCII ONLY - a stray Unicode char makes PowerShell 5.1 fail to parse this.

setlocal
set "PS=%~dp0install-vegas-scripts.ps1"

echo.
echo   Installing becky's VEGAS scripts...
echo.

powershell -NoProfile -ExecutionPolicy Bypass -Command "Start-Process powershell -Verb RunAs -Wait -ArgumentList '-NoProfile','-ExecutionPolicy','Bypass','-File','\"%PS%\"'"

echo.
echo   Done. Start VEGAS, then: Tools - Scripting - Becky Captions
echo.
pause
