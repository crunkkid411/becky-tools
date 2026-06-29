@echo off
setlocal
REM One-time setup for WHORETANA's voice (FastRTC + sounddevice + Gemini).
REM Builds an isolated Python venv at models\voice\venv. Run once.

where python >nul 2>nul
if errorlevel 1 goto NOPY

echo Building the voice runtime (FastRTC + sounddevice + google-genai)...
python -m venv "%~dp0models\voice\venv"
set "VPY=%~dp0models\voice\venv\Scripts\python.exe"

REM clear the machine-wide pip redirect so packages land IN the venv
set "PIP_TARGET="
set "PYTHONUSERBASE="
"%VPY%" -m pip install --no-user "fastrtc[vad]" google-genai sounddevice soundfile numpy
if errorlevel 1 goto FAIL

echo.
echo Voice runtime ready. For the Gemini realtime brain, put GEMINI_API_KEY in .env
echo (copy .env.example to .env). The local brain works with no key.
pause
exit /b 0

:FAIL
echo.
echo Install failed - see the messages above.
pause
exit /b 1

:NOPY
echo Python 3.10+ not found. Install it, then run this again.
pause
exit /b 1
