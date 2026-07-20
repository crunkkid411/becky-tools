# visual-critic.ps1 - the REAL 2-hourly adversarial VISUAL critic Jordan asked for
# (twice) and was lied about twice.
#
# What it does, every run: take a SCREENSHOT of Becky Review 3, send that image
# PLUS a reference image of the target layout to a FREE vision-model rotation
# (cohere -> ollama -> openrouter llama-vision:free -> gemini-flash, Claude Max
# OAuth as last resort - all free, never a paid API), and append the model's
# blunt "what does it still get wrong" answer to a dated log with a timestamp.
#
# This is DIFFERENT from BeckyBullshitCheck, which only read docs/code TEXT and
# never took a screenshot. Vision is cheap/fast and catches in seconds what an
# agent reading code convinces itself is fine.
#
#   Register (every 2h, forever):  powershell -File visual-critic.ps1 -Register
#   Run once now:                  powershell -File visual-critic.ps1
#
# Proof it ran: X:\AI-2\becky-tools\.visual-critic\visual-critic-log.md grows a
# timestamped entry, and verify-shot's receipts.jsonl gets a line, every run.

param(
    [string]$Reel      = 'X:/Videos/2025/11_November/Rendered/post_constantly.reel.json',
    [string]$Reference = 'X:\AI-2\becky-tools\.visual-critic\reference.png',
    [string]$LogDir    = 'X:\AI-2\becky-tools\.visual-critic',
    [switch]$Register
)

$ErrorActionPreference = 'Continue'
$exe       = 'X:\AI-2\becky-tools\native\becky-review\becky-review.exe'
$verifyShot = 'X:\AI-2\fleet\verify-shot.ps1'

if ($Register) {
    $self = $PSCommandPath
    schtasks /Create /TN 'BeckyVisualCritic' `
        /TR "powershell -NoProfile -ExecutionPolicy Bypass -File `"$self`"" `
        /SC HOURLY /MO 2 /F | Out-Null
    if ($LASTEXITCODE -eq 0) {
        Write-Host 'Registered BeckyVisualCritic: every 2 hours, indefinitely.'
    } else {
        Write-Host "schtasks /Create FAILED (exit $LASTEXITCODE)"
    }
    exit $LASTEXITCODE
}

if (-not (Test-Path $LogDir)) { New-Item -ItemType Directory -Path $LogDir -Force | Out-Null }
if (-not (Test-Path $Reference)) {
    Write-Host "No reference image at $Reference - run with a saved reference first."
    exit 2
}

# Use an already-open Review 3 window if there is one (non-destructive: we only
# screenshot). Only launch our OWN instance if none is open, and clean it up.
$launched = $false
$p = Get-Process -Name 'becky-review' -ErrorAction SilentlyContinue | Where-Object { $_.MainWindowTitle -ne '' } | Select-Object -First 1
if (-not $p) {
    $env:BECKY_REVIEW_REEL = $Reel
    Start-Process -FilePath $exe -WorkingDirectory (Split-Path $exe)
    Start-Sleep -Seconds 10
    $launched = $true
}

$stamp = Get-Date -Format 'yyyy-MM-dd_HH-mm-ss'
$shot  = Join-Path $LogDir "current-$stamp.png"
$ans   = Join-Path $LogDir 'last-answer.txt'

$question = 'Both are video-editor GUIs. Image 1 is the current app; image 2 is the target design. ' +
            'List the 3 most important things image 1 still gets WRONG versus image 2 - visual polish, ' +
            'font quality, layout, alignment, wasted space. For each, give ONE concrete fix. Be blunt.'

powershell -NoProfile -ExecutionPolicy Bypass -File $verifyShot `
    -Window 'Becky Review' -Reference $Reference -ShotPath $shot -OutFile $ans `
    -Question $question | Out-Null

$answer = if (Test-Path $ans) { Get-Content $ans -Raw } else { '(no answer file written)' }

$entry = @"

## $stamp
shot: $shot
$($answer.Trim())
"@
Add-Content -Path (Join-Path $LogDir 'visual-critic-log.md') -Value $entry -Encoding UTF8

Write-Host "Visual critic ran. Answer:"
Write-Host $answer.Trim()

if ($launched) {
    Get-Process -Name 'becky-review','mpv' -ErrorAction SilentlyContinue | ForEach-Object { try { $_.Kill() } catch {} }
}
