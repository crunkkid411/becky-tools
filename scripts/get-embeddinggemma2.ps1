# get-embeddinggemma2.ps1 - fetch the model behind becky-mediasearch (search pictures/sound by meaning).
#
# becky-mediasearch puts video frames, short clips and sound into the becky video database as
# EmbeddingGemma 2 vectors (Google, Apache-2.0), so a plain sentence ("the dog barks") or a photo
# can find the matching moment. It needs two files from ggml-org's GGUF conversion: the Q8_0 model
# (310 MB) and its Q8_0 vision+audio projector (555 MB). Pinned revision, sha256-checked. They run
# on the llama-server becky already uses (config llama_server); nothing to build.
#
# No QAT build exists for EmbeddingGemma 2 (Google released QAT only for v1 and for Gemma 4), so
# Q8_0 is used: checked against Google's ONNX reference at cosine 0.9999.
#
# Files land where internal/config looks:
#   X:\AI-2\becky-tools\models\embeddinggemma2\embeddinggemma-2-Q8_0.gguf
#   X:\AI-2\becky-tools\models\embeddinggemma2\mmproj-embeddinggemma-2-Q8_0.gguf
#
# ASCII-only (PowerShell 5.1 safe).
#
# Usage:
#   powershell -ExecutionPolicy Bypass -File scripts\get-embeddinggemma2.ps1
#   powershell ... -File scripts\get-embeddinggemma2.ps1 -Force    # re-download

param(
    [string]$Dir      = "X:\AI-2\becky-tools\models\embeddinggemma2",
    [string]$Repo     = "ggml-org/embeddinggemma-2-GGUF",
    [string]$Revision = "bfcd298762cc34d0357ece5ebdd31791a3a374d8",
    [switch]$Force
)

$ErrorActionPreference = "Stop"
New-Item -ItemType Directory -Force -Path $Dir | Out-Null

$files = @(
    @{ Name = "embeddinggemma-2-Q8_0.gguf";        Sha = "2188ac1deca4b77dffefd603c2776a9d76d9d74ec01841392982ebb840b09135" },
    @{ Name = "mmproj-embeddinggemma-2-Q8_0.gguf"; Sha = "c4a8a52691ecef40618438928bdf9e68379b854e24166f292592353db0aab64f" }
)

foreach ($f in $files) {
    $out  = Join-Path $Dir $f.Name
    $part = "$out.part"
    if ($Force -or -not (Test-Path $out)) {
        $url = "https://huggingface.co/$Repo/resolve/$Revision/$($f.Name)"
        Write-Host "Downloading $($f.Name) (resumable) ..."
        # curl.exe ships with Windows 10+; -C - resumes a broken download, --retry rides out drops.
        & curl.exe -L -C - --retry 20 --retry-delay 5 --speed-limit 50000 --speed-time 60 -o $part $url
        if ($LASTEXITCODE -ne 0) { throw "download failed ($LASTEXITCODE); run the script again to resume" }
        Move-Item -Force $part $out
    }
    $got = (Get-FileHash -Algorithm SHA256 $out).Hash.ToLower()
    if ($got -ne $f.Sha) { throw "checksum mismatch for $out (got $got); delete it and run again" }
    Write-Host "OK: $out"
}
