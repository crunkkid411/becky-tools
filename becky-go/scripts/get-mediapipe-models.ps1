# get-mediapipe-models.ps1 - fetch the MediaPipe model bundles becky-short needs.
#
# MediaPipe Tasks ships NO weights inside the pip package, so the .task bundles
# have to be downloaded once. They go beside becky's other models so config.Load
# finds them with no configuration.
#
# ASCII only, on purpose: a double-clicked .bat runs Windows PowerShell 5.1, which
# reads a BOM-less .ps1 as the system ANSI codepage, and one stray Unicode char
# makes the whole script fail to parse with no visible error.

$ErrorActionPreference = 'Stop'

$dest = Join-Path $PSScriptRoot '..\..\models\mediapipe'
$dest = [System.IO.Path]::GetFullPath($dest)
if (-not (Test-Path $dest)) { New-Item -ItemType Directory -Force -Path $dest | Out-Null }

$base = 'https://storage.googleapis.com/mediapipe-models'
$models = @(
    @{ Name = 'pose_landmarker_heavy.task'; Path = 'pose_landmarker/pose_landmarker_heavy/float16/1/pose_landmarker_heavy.task' },
    @{ Name = 'pose_landmarker_full.task';  Path = 'pose_landmarker/pose_landmarker_full/float16/1/pose_landmarker_full.task' }
)

Write-Host "Downloading MediaPipe models to $dest"
foreach ($m in $models) {
    $out = Join-Path $dest $m.Name
    if (Test-Path $out) {
        $mb = [math]::Round((Get-Item $out).Length / 1MB, 1)
        Write-Host ("  already have {0} ({1} MB)" -f $m.Name, $mb)
        continue
    }
    Write-Host ("  fetching {0} ..." -f $m.Name)
    Invoke-WebRequest -Uri "$base/$($m.Path)" -OutFile $out -UseBasicParsing
    $mb = [math]::Round((Get-Item $out).Length / 1MB, 1)
    Write-Host ("  got {0} ({1} MB)" -f $m.Name, $mb)
}

Write-Host ""
Write-Host "Done. becky-short will now frame on the subject instead of centre-cropping."
Write-Host "Check it with:  bin\becky-short.exe --selftest"
