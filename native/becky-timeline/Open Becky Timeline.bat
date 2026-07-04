@echo off
REM One-click: opens the native becky-timeline editor (2-layer live composite + ImSequencer).
REM Scrub the timeline cursor, or press Space to play.
set "G=C:\Program Files\gstreamer\1.0\msvc_x86_64"
set "PATH=%G%\bin;%PATH%"
set "GST_PLUGIN_SYSTEM_PATH_1_0=%G%\lib\gstreamer-1.0"
set "GST_PLUGIN_FEATURE_RANK=d3d11h264dec:512,d3d11h265dec:512"
cd /d "%~dp0"
if not exist becky-timeline.exe (
  echo becky-timeline.exe not found - build it first with _build.bat
  pause
  exit /b 1
)
becky-timeline.exe "..\ges-bench\proxyA.mp4" "..\ges-bench\proxyB.mp4"
pause
