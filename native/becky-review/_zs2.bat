@echo off
call "C:\Program Files (x86)\Microsoft Visual Studio\2022\BuildTools\VC\Auxiliary\Build\vcvars64.bat" >nul 2>&1
cd /d "X:\AI-2\becky-tools\native\becky-review"
cl /nologo /Zs /std:c++17 /I"X:\AI-2\becky-tools\native\timeline-bench\third_party\imgui" /I"X:\AI-2\becky-tools\native\timeline-bench\third_party\imgui\backends" /I"X:\AI-2\becky-tools\native\timeline-bench\third_party\nlohmann" /I"C:\Program Files\gstreamer\1.0\msvc_x86_64\include\gstreamer-1.0" /I"C:\Program Files\gstreamer\1.0\msvc_x86_64\include\glib-2.0" /I"C:\Program Files\gstreamer\1.0\msvc_x86_64\lib\glib-2.0\include" main.cpp > zs2.txt 2>&1
echo ZS_EXIT=%ERRORLEVEL% >> zs2.txt
