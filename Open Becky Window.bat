@echo off
REM Open Becky Window - builds and launches the native WPF becky window.
REM Double-click this. Needs the .NET 8 SDK installed once (see message below).

where dotnet >nul 2>nul
if errorlevel 1 (
  echo .NET SDK not found.
  echo Install it once by running this in a terminal:  winget install Microsoft.DotNet.SDK.8
  echo Then double-click this file again.
  pause
  exit /b 1
)

echo Building and launching Becky Window...
dotnet run --project "%~dp0gui\BeckyWindow\BeckyWindow.csproj"
if errorlevel 1 (
  echo.
  echo Build or launch failed - see the messages above.
)
pause
