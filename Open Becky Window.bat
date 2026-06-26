@echo off
setlocal
REM Open Becky Window - opens the native becky window.
REM Tip: the Desktop "Becky Window" shortcut opens it instantly with no console.
REM This .bat is the fallback / first-time builder. The program finds its own tools,
REM so no PATH setup is needed here.

set "EXE=%~dp0gui\BeckyWindow\bin\Release\net8.0-windows\BeckyWindow.exe"
if not exist "%EXE%" set "EXE=%~dp0gui\BeckyWindow\bin\Debug\net8.0-windows\BeckyWindow.exe"

if exist "%EXE%" goto LAUNCH

REM Not built yet - build it once. Needs the .NET SDK.
where dotnet >nul 2>nul
if errorlevel 1 goto NODOTNET
echo First run: building the becky window. Please wait...
dotnet build -c Release "%~dp0gui\BeckyWindow\BeckyWindow.csproj"
set "EXE=%~dp0gui\BeckyWindow\bin\Release\net8.0-windows\BeckyWindow.exe"
if not exist "%EXE%" goto BUILDFAIL

:LAUNCH
start "" "%EXE%"
exit /b 0

:BUILDFAIL
echo.
echo Build failed - see the messages above.
pause
exit /b 1

:NODOTNET
echo .NET SDK not found. Install it once:  winget install Microsoft.DotNet.SDK.8
echo Then double-click this again.
pause
exit /b 1
