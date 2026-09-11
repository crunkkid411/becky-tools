# Proves BeckyCut.cs's JSON reader against a REAL becky-cut --dry-run answer,
# without opening VEGAS.
#
# Why this exists: BeckyCut used to read becky's answer with
# JavaScriptSerializer, which VEGAS cannot load (System.Web.Extensions.dll is
# not in the tiny assembly set VEGAS compiles scripts against). Replacing it
# with a regex reader is the fix - and a regex reader is exactly the kind of
# thing that quietly reads 0 decisions, or reads them off by one key, and then
# cuts the wrong part of a clip. So it gets a check.
#
# It compiles the ACTUAL BeckyCut.cs and calls its ACTUAL private
# ParseCutReport by reflection - no copy of the parser to drift.
#
#   powershell -ExecutionPolicy Bypass -File vegas\test-beckycut-parser.ps1
#   powershell -ExecutionPolicy Bypass -File vegas\test-beckycut-parser.ps1 -Media "X:\clip.mp4"
#
# ASCII ONLY (see CLAUDE.md - PowerShell 5.1 parses this as the ANSI codepage).

param([string]$Media = "X:\AI-2\becky-tools\test.mp4")

$ErrorActionPreference = 'Stop'
$script = Join-Path $PSScriptRoot 'BeckyCut.cs'

# 1. compile the real script the same way VEGAS does
& (Join-Path $PSScriptRoot 'check-vegas-script.ps1') -Script $script | Out-Null
if ($LASTEXITCODE -ne 0) { Write-Host '  FAIL: BeckyCut.cs does not compile' -ForegroundColor Red; exit 1 }
$dll = Join-Path $env:TEMP 'becky-vegas-scriptcheck.dll'

# 2. get a real answer out of becky-cut
$becky = $env:BECKY_CUT
if (-not $becky) { $becky = Join-Path $PSScriptRoot '..\becky-go\bin\becky-cut.exe' }
if (-not (Test-Path $becky)) { Write-Host "  SKIP: no becky-cut.exe at $becky" -ForegroundColor Yellow; exit 0 }
if (-not (Test-Path $Media)) { Write-Host "  SKIP: no media at $Media" -ForegroundColor Yellow; exit 0 }

$json = [string]((& $becky $Media --dry-run 2>$null) -join "`n")
if (-not $json.Trim()) { Write-Host '  FAIL: becky-cut printed nothing' -ForegroundColor Red; exit 1 }

# 3. The answer key, counted out of the raw text by a DIFFERENT mechanism than
# the one under test, so agreement means something.
#
# Note "keep_segments" is NOT the answer: it is the count BEFORE the Silero VAD
# post-pass, and "removed_by_vad" of those are flipped to cut in the decision
# list. (Measured on test.mp4: 14 - 3 = 11 keeps in decisions[].) Checking
# against the wrong field is how you "verify" a parser that is fine.
$expectAll  = [int]([regex]::Match($json, '"total_chunks"\s*:\s*(\d+)').Groups[1].Value)
$expectKeep = ([regex]::Matches($json, '"status"\s*:\s*"keep"')).Count
$expectCut  = ([regex]::Matches($json, '"status"\s*:\s*"cut"')).Count
if (($expectKeep + $expectCut) -ne $expectAll) {
    Write-Host "  FAIL: becky-cut's own JSON is inconsistent ($expectKeep + $expectCut != $expectAll)" -ForegroundColor Red
    exit 1
}

# 4. run the script's own parser over it
foreach ($root in @($env:ProgramFiles, ${env:ProgramFiles(x86)})) {
    if (-not $root) { continue }
    $vr = Join-Path $root 'VEGAS'
    if (-not (Test-Path $vr)) { continue }
    Get-ChildItem $vr -Directory -EA SilentlyContinue | Sort-Object Name -Descending | ForEach-Object {
        $d = Join-Path $_.FullName 'ScriptPortal.Vegas.dll'
        if ((Test-Path $d) -and -not $script:vdll) { $script:vdll = $d }
    }
}
[void][Reflection.Assembly]::LoadFrom($script:vdll)
$asm = [Reflection.Assembly]::LoadFrom($dll)
$flags = [Reflection.BindingFlags]'NonPublic,Static'
$parse = $asm.GetType('EntryPoint').GetMethod('ParseCutReport', $flags)
if (-not $parse) { Write-Host '  FAIL: ParseCutReport not found' -ForegroundColor Red; exit 1 }

$report = $parse.Invoke($null, [object[]]@([string]$json))
$rt = $report.GetType()
$fps  = $rt.GetField('fps').GetValue($report)
$thr  = $rt.GetField('threshold').GetValue($report)
$decs = $rt.GetField('decisions').GetValue($report)

$dt = $null; $keep = 0; $cut = 0
foreach ($d in $decs) {
    if (-not $dt) { $dt = $d.GetType() }
    $s = $dt.GetField('status').GetValue($d)
    if ($s -eq 'keep') { $keep++ } elseif ($s -eq 'cut') { $cut++ }
}

# 5. assert VALUES, not truthiness
$fail = $false
function Check($name, $got, $want) {
    if ($got -eq $want) { Write-Host ("  ok    {0,-16} {1}" -f $name, $got) -ForegroundColor Green }
    else { Write-Host ("  FAIL  {0,-16} got {1}, expected {2}" -f $name, $got, $want) -ForegroundColor Red; $script:fail = $true }
}
Write-Host ''
Write-Host "  BeckyCut.cs parser vs becky-cut on $(Split-Path $Media -Leaf)" -ForegroundColor Cyan
Check 'decisions'  $decs.Count  $expectAll
Check 'keep'       $keep        $expectKeep
Check 'cut'        $cut         $expectCut

if ($fps -le 0)        { Write-Host "  FAIL  fps              got $fps" -ForegroundColor Red; $fail = $true }
else                   { Write-Host ("  ok    {0,-16} {1}" -f 'fps', $fps) -ForegroundColor Green }
if (-not $thr)         { Write-Host "  FAIL  threshold        empty" -ForegroundColor Red; $fail = $true }
else                   { Write-Host ("  ok    {0,-16} {1}" -f 'threshold', $thr) -ForegroundColor Green }

# a cut span with end <= start would delete nothing or delete backwards
foreach ($d in $decs) {
    if ([double]$dt.GetField('end').GetValue($d) -le [double]$dt.GetField('start').GetValue($d)) {
        Write-Host '  FAIL  a decision ends before it starts' -ForegroundColor Red; $fail = $true; break
    }
}

Write-Host ''
if ($fail) { Write-Host '  PARSER TEST FAILED' -ForegroundColor Red; exit 1 }
Write-Host '  PARSER TEST PASSED' -ForegroundColor Green
exit 0
