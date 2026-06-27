@echo off
setlocal
REM Open Becky Review - the one-window forensic video reviewer.
REM LEFT: your clip/search list (WebView2). RIGHT: a smooth, frame-exact video player (mpv).
REM First run builds the app + the index tool and fetches the video runtime; after that it
REM opens instantly. The Desktop "Becky Review" shortcut runs this with no console.

set "ROOT=%~dp0"
set "PROJ=%ROOT%gui\BeckyReview"
set "EXE=%PROJ%\bin\Release\net8.0-windows\BeckyReview.exe"
if not exist "%EXE%" set "EXE=%PROJ%\bin\Debug\net8.0-windows\BeckyReview.exe"

REM 1) The search/index tool the app shells out to.
if not exist "%ROOT%becky-go\bin\becky-review-index.exe" (
  where go >nul 2>nul
  if not errorlevel 1 (
    echo First run: building the folder index tool...
    pushd "%ROOT%becky-go"
    go build -o bin\becky-review-index.exe .\cmd\review-index
    popd
  )
)

REM 1b) The persistent engine (headless becky-clip bridge: warm search + all verbs).
if not exist "%ROOT%becky-go\bin\becky-review-engine.exe" (
  where go >nul 2>nul
  if not errorlevel 1 (
    echo First run: building the review engine...
    pushd "%ROOT%becky-go"
    go build -o bin\becky-review-engine.exe .\cmd\clip
    popd
  )
)

REM 2) The mpv video runtime (large; downloaded once).
if not exist "%PROJ%\runtime\mpv\mpv.exe" (
  echo First run: fetching the mpv video runtime ^(one time, ~60 MB^)...
  powershell -NoProfile -ExecutionPolicy Bypass -File "%PROJ%\fetch-mpv.ps1"
)

REM 3) The app itself.
if exist "%EXE%" goto LAUNCH
where dotnet >nul 2>nul
if errorlevel 1 goto NODOTNET
echo First run: building Becky Review. Please wait...
dotnet build -c Release "%PROJ%\BeckyReview.csproj"
set "EXE=%PROJ%\bin\Release\net8.0-windows\BeckyReview.exe"
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
