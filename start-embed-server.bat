@echo off
REM ============================================================================
REM start-embed-server.bat — resident llama.cpp embedding server for becky-embed
REM and becky-search.
REM
REM Serves Qwen3-Embedding-4B (Q5_K_M GGUF) on http://127.0.0.1:8088. The becky
REM Go tools do NOT launch or supervise this server (a deliberate design
REM constraint: no daemon supervisor inside the tools). Run this script once
REM (or from a machine-startup task); the tools just POST to the URL and FAIL
REM with a clear message when it is unreachable.
REM
REM Model:  Qwen3-Embedding-4B-Q5_K_M.gguf (native 2560 dims, Apache-2.0, MRL).
REM         The client (embed_text.py) MRL-truncates to the first 1024 dims and
REM         L2-normalizes, keeping the existing float[1024] DB schema.
REM Pooling: --pooling last is REQUIRED for Qwen3-Embedding (last-token pooling).
REM         llama-server does NOT normalize this path, so the client normalizes.
REM
REM VRAM: ~2.9 GB weights at -ngl 99 (all layers on GPU). Fits the RTX 3070
REM       Laptop's 8 GB with headroom for h264_nvenc.
REM ============================================================================

setlocal

REM --- Configurable knobs (env overrides honored) -----------------------------
if "%LLAMA_SERVER%"=="" set LLAMA_SERVER=C:\llama.cpp\build\bin\llama-server.exe
if "%EMBED_GGUF%"==""   set EMBED_GGUF=X:\AI-2\becky-tools\models\embeddings\gguf\Qwen3-Embedding-4B-Q5_K_M.gguf
if "%EMBED_HOST%"==""   set EMBED_HOST=127.0.0.1
if "%EMBED_PORT%"==""   set EMBED_PORT=8088

if not exist "%LLAMA_SERVER%" (
  echo ERROR: llama-server not found at "%LLAMA_SERVER%"
  echo Set LLAMA_SERVER to the llama-server.exe path and re-run.
  exit /b 1
)
if not exist "%EMBED_GGUF%" (
  echo ERROR: embedding GGUF not found at "%EMBED_GGUF%"
  echo Download it with:
  echo   huggingface-cli download Qwen/Qwen3-Embedding-4B-GGUF Qwen3-Embedding-4B-Q5_K_M.gguf --local-dir X:\AI-2\becky-tools\models\embeddings\gguf
  exit /b 1
)

echo Starting Qwen3-Embedding-4B server on http://%EMBED_HOST%:%EMBED_PORT% ...
echo   model: %EMBED_GGUF%
echo   (Ctrl+C to stop. becky-embed/search MRL-truncate to 1024 + L2-normalize.)
echo.

"%LLAMA_SERVER%" ^
  -m "%EMBED_GGUF%" ^
  --embedding ^
  --pooling last ^
  -ngl 99 ^
  -c 8192 ^
  -ub 8192 ^
  --host %EMBED_HOST% ^
  --port %EMBED_PORT%

endlocal
