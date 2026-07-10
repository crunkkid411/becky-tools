# vision-smoke-gate.ps1 - the becky-AI-Agent-review-1.md Section 5 regression
# gate, callable on demand (or from a scheduled task / build-all-tools.bat)
# instead of typing the raw `go test` invocation by hand.
#
# Runs becky-go\cmd\vision\smoke_test.go (build tag: llm) against every
# fixture in becky-go\testdata\vision. FAST (default, every build): rung 0
# (450M) + mandatory OCR corroboration only, ~20s. FULL (weekly): the real,
# uncapped no-flags escalation ladder - the actual acceptance-criterion-2
# gate - ~1-2 minutes (spins up Gemma-4 E4B/12B via llama-server).
#
# Usage:
#   powershell -File scripts\vision-smoke-gate.ps1            (fast, every build)
#   powershell -File scripts\vision-smoke-gate.ps1 -Full       (weekly, full ladder)
#
# Exit code is go test's: 0 = every fixture's assertions passed, nonzero =
# at least one assertion missed (or the model/binary was missing and every
# case skipped - go test itself still exits 0 for an all-skip run, so a
# "not installed on this machine" box never blocks a caller that piped this
# into a build).

param(
    [switch]$Full
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot   # becky-go\

Push-Location $repoRoot
try {
    if ($Full) {
        Write-Host "vision-smoke-gate: FULL ladder mode (weekly) - this spins up Gemma-4 E4B/12B, budget 1-2 min per fixture."
        $env:BECKY_VISION_SMOKE_FULL = "1"
        $timeout = "20m"
    } else {
        Write-Host "vision-smoke-gate: FAST mode (every build) - rung 0 (450M) + mandatory OCR only."
        Remove-Item Env:\BECKY_VISION_SMOKE_FULL -ErrorAction SilentlyContinue
        $timeout = "3m"
    }

    go test -tags=llm -timeout $timeout -run TestVisionSmoke -v ./cmd/vision/...
    $goExit = $LASTEXITCODE

    if ($goExit -eq 0) {
        Write-Host "vision-smoke-gate: PASS"
    } else {
        Write-Host "vision-smoke-gate: FAIL (exit $goExit) - see the assertion output above."
    }
    exit $goExit
} finally {
    Remove-Item Env:\BECKY_VISION_SMOKE_FULL -ErrorAction SilentlyContinue
    Pop-Location
}
