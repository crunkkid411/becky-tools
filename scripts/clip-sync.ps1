# clip-sync.ps1 - archive iPhone Chrome history to Obsidian markdown, verified,
# with automatic Gemma-4 recovery of pages the deterministic path misses.
#
# Pipeline, ONE PAGE AT A TIME (deterministic first; AI only when needed):
#   becky-radar --list   -> every iPhone-synced page in the window (a URL feed)
#   becky-web2md         -> download each NEW page to a single .md (deterministic)
#   becky-clipcheck      -> confirm the .md actually contains the page's content
#   becky-regrab         -> if web2md missed it, the local Gemma-4 model re-grabs
#                           the content from the page text, then it is re-verified
#
# Idempotent: a manifest (.clip-manifest.json) records what was archived, so a
# page already saved+verified is skipped; only new pages (and prior failures) are
# fetched. Run -Retry to re-attempt only the pages that previously needed
# attention (now through the full deterministic + Gemma ladder).
#
# ASCII-only + PowerShell 5.1 compatible so it runs from Task Scheduler without a
# parse error. Exit 0 always (a per-page failure is logged, not fatal).

[CmdletBinding()]
param(
    [int]$Days = 30,
    [string]$Target = "C:\Users\only1\Documents\Obsidian\browser_data\iPhone",
    [string]$BinDir = "X:\AI-2\becky-tools\becky-go\bin",
    [switch]$NoAI,    # skip the borderline clipcheck adjudication (regrab still runs)
    [switch]$Retry,   # re-attempt only the manifest entries that previously failed
    [int]$Max = 0     # 0 = no limit (process all new pages)
)

$ErrorActionPreference = "Stop"

$radar = Join-Path $BinDir "becky-radar.exe"
$web2md = Join-Path $BinDir "becky-web2md.exe"
$clipcheck = Join-Path $BinDir "becky-clipcheck.exe"
$regrab = Join-Path $BinDir "becky-regrab.exe"
foreach ($exe in @($radar, $web2md, $clipcheck, $regrab)) {
    if (-not (Test-Path $exe)) { Write-Output "MISSING TOOL: $exe"; exit 0 }
}

if (-not (Test-Path $Target)) { New-Item -ItemType Directory -Force $Target | Out-Null }
$manifestPath = Join-Path $Target ".clip-manifest.json"
$logDir = Join-Path $Target "_logs"
if (-not (Test-Path $logDir)) { New-Item -ItemType Directory -Force $logDir | Out-Null }
$stamp = Get-Date -Format "yyyy-MM-dd_HHmmss"
$logPath = Join-Path $logDir "clip-sync_$stamp.log"

function Log($msg) {
    $line = "{0}  {1}" -f (Get-Date -Format "HH:mm:ss"), $msg
    Write-Output $line
    Add-Content -Path $logPath -Value $line -Encoding utf8
}

function Canon([string]$u) {
    try { $uri = [uri]$u } catch { return $u.ToLower().TrimEnd('/') }
    $hostName = $uri.Host.ToLower()
    $path = $uri.AbsolutePath.TrimEnd('/')
    $keep = @()
    if ($uri.Query.Length -gt 1) {
        foreach ($kv in $uri.Query.TrimStart('?').Split('&')) {
            $k = $kv.Split('=')[0].ToLower()
            if ($k -like 'utm_*' -or $k -in @('fbclid', 'gclid', 'igshid', 'mc_cid', 'mc_eid', 'ref', 'ref_src', 'spm')) { continue }
            if ($k -ne '') { $keep += $kv }
        }
    }
    $q = ''
    if ($keep.Count -gt 0) { $q = '?' + ($keep -join '&') }
    return ("{0}://{1}{2}{3}" -f $uri.Scheme.ToLower(), $hostName, $path, $q)
}

function Slug([string]$s) {
    if ([string]::IsNullOrWhiteSpace($s)) { return "" }
    $s = ($s -replace '[^A-Za-z0-9 _-]+', ' ') -replace '\s+', ' '
    $s = $s.Trim()
    if ($s.Length -gt 70) { $s = $s.Substring(0, 70).Trim() }
    return $s
}

function Hash8([string]$s) {
    $sha = [System.Security.Cryptography.SHA1]::Create()
    $bytes = $sha.ComputeHash([System.Text.Encoding]::UTF8.GetBytes($s))
    return -join ($bytes[0..3] | ForEach-Object { $_.ToString('x2') })
}

# GrabOne runs the full ladder for one page and returns the final verdict/method.
# Deterministic web2md first; if it misses (skip/fail/partial), Gemma re-grabs.
function GrabOne([string]$url, [string]$file) {
    $outPath = Join-Path $Target $file

    $null = & $web2md $url --vault $Target --output $file 2>$null
    $wexit = $LASTEXITCODE
    $det = $null
    if ($wexit -eq 0 -and (Test-Path $outPath)) {
        $ccArgs = @($outPath, '--json')
        if ($NoAI) { $ccArgs += '--no-ai' }
        try { $det = (& $clipcheck @ccArgs 2>$null) | ConvertFrom-Json } catch { $det = $null }
        if ($det -and ($det.verdict -in @('pass', 'thin'))) {
            return [pscustomobject]@{ verdict = $det.verdict; method = 'web2md'; recall = $det.recall }
        }
    }

    # Deterministic path missed -> Gemma-4 recovery, then re-verify (regrab scores
    # its own output). This is the "gemma is part of the workflow every time" step.
    # VRAM leak fix: kill any existing llama-server.exe before spawning a new one,
    # and wrap the call in try/finally to guarantee cleanup on exit (normal or error).
    $rg = $null
    try {
        Get-Process -Name llama-server -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
        try { $rg = (& $regrab $url --vault $Target --output $file --json 2>$null) | ConvertFrom-Json } catch { $rg = $null }
    }
    finally {
        Get-Process -Name llama-server -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
    }
    if ($rg) {
        return [pscustomobject]@{ verdict = $rg.verdict; method = $rg.method; recall = $rg.recall }
    }
    if ($det) {
        return [pscustomobject]@{ verdict = $det.verdict; method = 'web2md'; recall = $det.recall }
    }
    if ($wexit -eq 2) {
        return [pscustomobject]@{ verdict = 'unrecoverable'; method = 'web2md'; recall = 0 }
    }
    return [pscustomobject]@{ verdict = 'download-failed'; method = 'web2md'; recall = 0 }
}

# Load manifest into a hashtable keyed by canonical URL.
$manifest = @{}
if (Test-Path $manifestPath) {
    try {
        $obj = Get-Content $manifestPath -Raw -Encoding utf8 | ConvertFrom-Json
        foreach ($p in $obj.PSObject.Properties) { $manifest[$p.Name] = $p.Value }
    } catch { Log "WARN: could not read manifest ($($_.Exception.Message)); starting fresh" }
}

# Build the work list: the radar feed (normal) or the failed manifest entries (-Retry).
$work = @()
if ($Retry) {
    Log "becky clip-sync RETRY: re-attempting pages that previously needed attention"
    foreach ($k in $manifest.Keys) {
        $m = $manifest[$k]
        if ($m.verdict -notin @('pass', 'thin')) {
            $u = $m.url; if ([string]::IsNullOrWhiteSpace($u)) { $u = $k }
            $work += [pscustomobject]@{ url = $u; title = '' }
        }
    }
    Log "retry: $($work.Count) pages to re-attempt"
} else {
    Log "becky clip-sync starting: window=${Days}d target=$Target ai=$(-not $NoAI)"
    $listJson = & $radar --list --days $Days 2>$null
    try { $list = $listJson | ConvertFrom-Json } catch { Log "ERROR: radar --list returned no JSON"; exit 0 }
    if ($list.degraded) { Log "ERROR: could not read Chrome history: $($list.note)"; exit 0 }
    Log "radar: $($list.count) synced pages in window ($($list.filtered_out) junk filtered)"
    $work = $list.urls
}

$done = 0; $newOk = 0; $failed = 0; $recovered = 0
$failList = @()

foreach ($item in $work) {
    $url = $item.url
    $canon = Canon $url
    $prior = $manifest[$canon]
    if (-not $Retry -and $prior -and ($prior.verdict -in @('pass', 'thin')) -and (Test-Path (Join-Path $Target $prior.file))) {
        continue  # already archived + verified
    }
    if ($Max -gt 0 -and $done -ge $Max) { break }
    $done++

    $base = Slug $item.title
    if ($base -eq "" -and $prior -and $prior.file) { $base = Slug ([System.IO.Path]::GetFileNameWithoutExtension($prior.file)) }
    if ($base -eq "") { try { $base = Slug ([uri]$url).Host } catch { $base = "web-clip" } }
    $file = "{0}-{1}.md" -f $base, (Hash8 $canon)

    $r = GrabOne $url $file
    $manifest[$canon] = [pscustomobject]@{ file = $file; verdict = $r.verdict; recall = $r.recall; method = $r.method; date = $stamp; url = $url }

    switch ($r.verdict) {
        'pass' {
            if ($r.method -eq 'web2md') { Log ("[$done] PASS  recall={0:N2} {1}" -f $r.recall, $file); $newOk++ }
            else { Log ("[$done] PASS (recovered by {0})  recall={1:N2} {2}" -f $r.method, $r.recall, $file); $newOk++; $recovered++ }
        }
        'thin' { Log ("[$done] THIN  (little page text) {0}" -f $file); $newOk++ }
        'unrecoverable' { Log ("[$done] UNRECOVERABLE (needs a browser; no static text) {0}" -f $url); $failed++; $failList += "unrecoverable  $url" }
        'download-failed' { Log ("[$done] DOWNLOAD FAILED {0}" -f $url); $failed++; $failList += "download-failed  $url" }
        default { Log ("[$done] {0}  recall={1:N2} {2}  <- review" -f $r.verdict.ToUpper(), $r.recall, $url); $failed++; $failList += ("{0}  {1}" -f $r.verdict, $url) }
    }
}

# Save manifest.
$manifest | ConvertTo-Json -Depth 5 | Set-Content -Path $manifestPath -Encoding utf8

# Write a human summary the user can open in Obsidian.
$summaryPath = Join-Path $Target "_SUMMARY.md"
$total = ($manifest.Keys | Measure-Object).Count
$lines = @()
$lines += "# iPhone history archive - summary"
$lines += ""
$lines += "_Last run: $stamp_"
$lines += ""
$lines += "- Pages archived (all-time): **$total**"
$lines += "- This run: $done processed, $newOk verified OK ($recovered recovered by Gemma), $failed need attention"
$lines += ""
if ($failList.Count -gt 0) {
    $lines += "## Needs attention (this run)"
    $lines += "These are bot-blocked or JavaScript-only pages with no static content to save."
    foreach ($f in $failList) { $lines += "- $f" }
} else {
    $lines += "All pages this run verified OK."
}
$lines | Set-Content -Path $summaryPath -Encoding utf8

Log "DONE: $done processed | $newOk verified OK | $recovered Gemma-recovered | $failed need attention | $total total archived"
Log "summary: $summaryPath"
exit 0
