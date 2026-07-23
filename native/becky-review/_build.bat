@echo off
REM Build becky-review.exe - the full-native single-window Becky Review.
REM Step 6 (2026-07-23): mpv DELETED; video is the in-process engine
REM (engine.cpp, libavcodec/D3D11VA + WASAPI). FFmpeg comes from the MSYS2
REM mingw64 install: headers are STAGED into ffinc\ (never expose the whole
REM mingw include root to MSVC - its libc headers poison the build), import
REM libs are the .dll.a files (MSVC links them fine, proven 2026-07-23).
REM ASCII-only. Builds from THIS script's folder (worktree-safe).
cd /d "%~dp0"
taskkill /IM becky-review.exe /F >nul 2>nul
if not exist ffinc (
  mkdir ffinc
  xcopy /E /I /Q C:\msys64\mingw64\include\libavcodec ffinc\libavcodec >nul
  xcopy /E /I /Q C:\msys64\mingw64\include\libavformat ffinc\libavformat >nul
  xcopy /E /I /Q C:\msys64\mingw64\include\libavutil ffinc\libavutil >nul
  xcopy /E /I /Q C:\msys64\mingw64\include\libswresample ffinc\libswresample >nul
  xcopy /E /I /Q C:\msys64\mingw64\include\libswscale ffinc\libswscale >nul
)
call "C:\Program Files (x86)\Microsoft Visual Studio\2022\BuildTools\VC\Auxiliary\Build\vcvars64.bat" >nul
cl /nologo /O2 /MD /EHsc /std:c++17 /I"X:\AI-2\becky-tools\native\timeline-bench\third_party\imgui" /I"X:\AI-2\becky-tools\native\timeline-bench\third_party\imgui\backends" /I"X:\AI-2\becky-tools\native\timeline-bench\third_party\nlohmann" /Iffinc /I"C:\Program Files\gstreamer\1.0\msvc_x86_64\include\gstreamer-1.0" /I"C:\Program Files\gstreamer\1.0\msvc_x86_64\include\glib-2.0" /I"C:\Program Files\gstreamer\1.0\msvc_x86_64\lib\glib-2.0\include" main.cpp engine.cpp "X:\AI-2\becky-tools\native\timeline-bench\third_party\imgui\imgui.cpp" "X:\AI-2\becky-tools\native\timeline-bench\third_party\imgui\imgui_draw.cpp" "X:\AI-2\becky-tools\native\timeline-bench\third_party\imgui\imgui_tables.cpp" "X:\AI-2\becky-tools\native\timeline-bench\third_party\imgui\imgui_widgets.cpp" "X:\AI-2\becky-tools\native\timeline-bench\third_party\imgui\backends\imgui_impl_win32.cpp" "X:\AI-2\becky-tools\native\timeline-bench\third_party\imgui\backends\imgui_impl_dx11.cpp" /Fe:becky-review.exe /link /MAP:becky-review.map /SUBSYSTEM:WINDOWS /ENTRY:mainCRTStartup /LIBPATH:"C:\Program Files\gstreamer\1.0\msvc_x86_64\lib" /LIBPATH:"C:\msys64\mingw64\lib" gstreamer-1.0.lib gstapp-1.0.lib gstvideo-1.0.lib gobject-2.0.lib glib-2.0.lib libavformat.dll.a libavcodec.dll.a libavutil.dll.a libswresample.dll.a d3d11.lib dxgi.lib d3dcompiler.lib gdi32.lib user32.lib shell32.lib comdlg32.lib windowscodecs.lib ole32.lib
