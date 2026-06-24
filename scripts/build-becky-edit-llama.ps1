# build-becky-edit-llama.ps1 - build becky-edit.exe with the IN-PROCESS Gemma-4 model
# (llama.dll via cgo, -tags llamacgo), installed to becky-go\becky-edit.exe (the path the
# Shotcut Becky dock spawns). The default `go build` / build-all-tools loop produces the
# warm-llama-server build instead, which has NO llama.dll dependency (so CI/cloud + selftest
# stay portable). Run THIS to get the in-process model; launch via "Open Becky Edit.bat"
# (which puts the llama runtime DLLs on PATH so the load-time link resolves).
#
# Requires: Go (cgo), a mingw gcc (Strawberry or msys2), gendef+dlltool, and a llama.cpp
# build with headers at C:\llama.cpp\include + DLLs at C:\llama.cpp\build\bin (the same one
# becky-validate's AVLM uses). ASCII-only.
$ErrorActionPreference = "Stop"
$repo  = Split-Path -Parent $PSScriptRoot
$go    = Join-Path $repo "becky-go"
$pkg   = Join-Path $go "internal\llamacpp"
$llamaBin = "C:\llama.cpp\build\bin"

function Find-Tool($name) {
  $c = Get-Command $name -ErrorAction SilentlyContinue
  if ($c) { return $c.Source }
  foreach ($p in @("C:\Strawberry\c\bin\$name.exe", "C:\msys64\mingw64\bin\$name.exe")) {
    if (Test-Path $p) { return $p }
  }
  throw "$name not found (need gendef/dlltool from Strawberry or msys2)"
}
$gendef  = Find-Tool "gendef"
$dlltool = Find-Tool "dlltool"

Write-Host "Generating mingw import libs from llama.dll + ggml.dll ..."
Push-Location $pkg
try {
  & $gendef  "$llamaBin\llama.dll" | Out-Null
  & $dlltool -d llama.def -D llama.dll -l libllama.dll.a
  & $gendef  "$llamaBin\ggml.dll"  | Out-Null
  & $dlltool -d ggml.def  -D ggml.dll  -l libggml.dll.a
  if (-not (Test-Path "libllama.dll.a") -or -not (Test-Path "libggml.dll.a")) {
    throw "import lib generation failed"
  }
} finally { Pop-Location }

Write-Host "Building becky-edit.exe (-tags llamacgo, CGO) ..."
Push-Location $go
try {
  $env:CGO_ENABLED = "1"
  & go build -tags llamacgo -o becky-edit.exe .\cmd\becky-edit
  if ($LASTEXITCODE -ne 0) { throw "go build failed (exit $LASTEXITCODE)" }
} finally { Pop-Location }

Write-Host ""
Write-Host "OK: becky-go\becky-edit.exe is now the IN-PROCESS Gemma-4 build."
Write-Host "    Launch via 'Open Becky Edit.bat' (it adds $llamaBin to PATH)."
