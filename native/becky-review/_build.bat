@echo off
REM Build becky-review.exe - the full-native single-window Becky Review.
REM Grown from native/becky-timeline (same D3D11 + Dear ImGui + GStreamer D3D11 decode).
REM ASCII-only (no em-dash / smart quotes) - PowerShell 5.1 parses this under 5.1 codepage.
cd /d "X:\AI-2\becky-tools\native\becky-review"
REM Kill any running instance so the linker can overwrite becky-review.exe (prevents
REM the transient LNK1104 'cannot open file' failure when a prior/verify launch is still up).
taskkill /IM becky-review.exe /F >nul 2>nul
call "C:\Program Files (x86)\Microsoft Visual Studio\2022\BuildTools\VC\Auxiliary\Build\vcvars64.bat" >nul
cl /nologo /O2 /MD /EHsc /std:c++17 /I"X:\AI-2\becky-tools\native\timeline-bench\third_party\imgui" /I"X:\AI-2\becky-tools\native\timeline-bench\third_party\imgui\backends" /I"X:\AI-2\becky-tools\native\timeline-bench\third_party\nlohmann" /I"C:\Program Files\gstreamer\1.0\msvc_x86_64\include\gstreamer-1.0" /I"C:\Program Files\gstreamer\1.0\msvc_x86_64\include\glib-2.0" /I"C:\Program Files\gstreamer\1.0\msvc_x86_64\lib\glib-2.0\include" main.cpp "X:\AI-2\becky-tools\native\timeline-bench\third_party\imgui\imgui.cpp" "X:\AI-2\becky-tools\native\timeline-bench\third_party\imgui\imgui_draw.cpp" "X:\AI-2\becky-tools\native\timeline-bench\third_party\imgui\imgui_tables.cpp" "X:\AI-2\becky-tools\native\timeline-bench\third_party\imgui\imgui_widgets.cpp" "X:\AI-2\becky-tools\native\timeline-bench\third_party\imgui\backends\imgui_impl_win32.cpp" "X:\AI-2\becky-tools\native\timeline-bench\third_party\imgui\backends\imgui_impl_dx11.cpp" /Fe:becky-review.exe /link /MAP:becky-review.map /SUBSYSTEM:WINDOWS /ENTRY:mainCRTStartup /LIBPATH:"C:\Program Files\gstreamer\1.0\msvc_x86_64\lib" gstreamer-1.0.lib gstapp-1.0.lib gstvideo-1.0.lib gobject-2.0.lib glib-2.0.lib d3d11.lib dxgi.lib gdi32.lib user32.lib shell32.lib comdlg32.lib windowscodecs.lib ole32.lib
