@echo off
REM One-click: opens the 2-layer live-scrub proof window. Drag in the window to scrub.
set "G=C:\Program Files\gstreamer\1.0\msvc_x86_64"
set "PATH=%G%\bin;%PATH%"
set "GST_PLUGIN_SYSTEM_PATH_1_0=%G%\lib\gstreamer-1.0"
set "GST_PLUGIN_FEATURE_RANK=d3d11h264dec:512,d3d11h265dec:512"
cd /d "%~dp0"
if not exist gst_scrubwin.exe (
  echo gst_scrubwin.exe not found - build it first with _build_scrubwin.bat
  pause
  exit /b 1
)
gst_scrubwin.exe "proxyA.mp4" "proxyB.mp4"
pause
