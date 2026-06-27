# clip-sync.ps1 - archive iPhone Chrome history to Obsidian markdown, verified.
#
# Pipeline, ONE PAGE AT A TIME (deterministic; AI only on borderline pages):
#   becky-radar --list   -> every iPhone-synced page in the window (a URL feed)
#   becky-web2md         -> download each NEW page to a single .md
#   becky-clipcheck      -> confirm the .md actually contains the page's content
#
# Idempotent: a manifest (.clip-manifest.json) records what was archived, so a
# page already saved+verified is skipped; only new pages (and prior failures) are
# fetched. Safe to run daily. ASCII-only + PowerShell 5.1 compatible so it runs
# from Task Scheduler without a parse error.
#
# Exit 0 always (a per-page failure is logged, not fatal) so the scheduled task
# never shows a red error for one bad page.

[CmdletBinding()]
param(
    [int]$Days = 30,
    [string]$Target = "C:\Users\only1\Documents\Obsidian\browser_data\iPhone",
    [string]$BinDir = "X:\AI-2\becky-tools\becky-go\bin",
    [switch]$NoAI,
    [int]$Max = 0   # 0 = no limit (process all new pages)
)

$ErrorActionPreference = "Stop"

$radar = Join-Path $BinDir "becky-radar.exe"
$web2md = Join-Path $BinDir "becky-web2md.exe"
$clipcheck = Join-Path $BinDir "becky-clipcheck.exe"
foreach ($exe in @($radar, $web2md, $clipcheck)) {
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

# Canonicalize a URL for dedup: lower host, drop fragment + tracking params, trim
# trailing slash. Two visits to one page (different utm tags) map to one entry.
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

# Load manifest into a hashtable keyed by canonical URL.
$manifest = @{}
if (Test-Path $manifestPath) {
    try {
        $obj = Get-Content $manifestPath -Raw -Encoding utf8 | ConvertFrom-Json
        foreach ($p in $obj.PSObject.Properties) { $manifest[$p.Name] = $p.Value }
    } catch { Log "WARN: could not read manifest ($($_.Exception.Message)); starting fresh" }
}

Log "becky clip-sync starting: window=${Days}d target=$Target ai=$(-not $NoAI)"

# 1) Pull the iPhone-synced URL feed.
$listJson = & $radar --list --days $Days 2>$null
try { $list = $listJson | ConvertFrom-Json } catch { Log "ERROR: radar --list returned no JSON"; exit 0 }
if ($list.degraded) { Log "ERROR: could not read Chrome history: $($list.note)"; exit 0 }
Log "radar: $($list.count) synced pages in window ($($list.filtered_out) junk filtered)"

$done = 0; $newOk = 0; $failed = 0; $skipped = 0; $dl = 0
$failList = @()

foreach ($item in $list.urls) {
    $url = $item.url
    $canon = Canon $url
    $prior = $manifest[$canon]
    if ($prior -and ($prior.verdict -in @('pass', 'thin')) -and (Test-Path (Join-Path $Target $prior.file))) {
        continue  # already archived + verified
    }
    if ($Max -gt 0 -and $done -ge $Max) { break }
    $done++

    $base = Slug $item.title
    if ($base -eq "") { try { $base = Slug ([uri]$url).Host } catch { $base = "web-clip" } }
    $file = "{0}-{1}.md" -f $base, (Hash8 $canon)
    $outPath = Join-Path $Target $file

    # 2) Download to a single .md.
    $null = & $web2md $url --vault $Target --output $file 2>$null
    $wexit = $LASTEXITCODE
    if ($wexit -eq 2) {
        Log "[$done] SKIP (no extractable content): $url"
        $manifest[$canon] = [pscustomobject]@{ file = $file; verdict = 'skipped'; reason = 'no extractable content'; date = $stamp; url = $url }
        $skipped++; continue
    }
    if ($wexit -ne 0 -or -not (Test-Path $outPath)) {
        Log "[$done] DOWNLOAD FAILED: $url"
        $manifest[$canon] = [pscustomobject]@{ file = $file; verdict = 'download-failed'; date = $stamp; url = $url }
        $failed++; $failList += "download-failed  $url"; continue
    }
    $dl++

    # 3) Verify the .md actually contains the page.
    $ccArgs = @($outPath, '--json')
    if ($NoAI) { $ccArgs += '--no-ai' }
    $ccJson = & $clipcheck @ccArgs 2>$null
    try { $cc = $ccJson | ConvertFrom-Json } catch { $cc = $null }
    if ($null -eq $cc) {
        Log "[$done] VERIFY ERROR: $url"
        $manifest[$canon] = [pscustomobject]@{ file = $file; verdict = 'verify-error'; date = $stamp; url = $url }
        $failed++; $failList += "verify-error  $url"; continue
    }

    $manifest[$canon] = [pscustomobject]@{ file = $file; verdict = $cc.verdict; recall = $cc.recall; precision = $cc.precision; date = $stamp; url = $url }
    switch ($cc.verdict) {
        'pass' { Log ("[$done] PASS  recall={0:N2} {1}" -f $cc.recall, $file); $newOk++ }
        'thin' { Log ("[$done] THIN  (little page text) {0}" -f $file); $newOk++ }
        'partial' { Log ("[$done] PARTIAL  recall={0:N2} {1}  <- review" -f $cc.recall, $file); $failed++; $failList += ("partial  recall={0:N2}  {1}" -f $cc.recall, $url) }
        'fail' { Log ("[$done] FAIL  recall={0:N2} {1}  <- content missing" -f $cc.recall, $file); $failed++; $failList += ("fail  recall={0:N2}  {1}" -f $cc.recall, $url) }
        'unverified' { Log ("[$done] UNVERIFIED (could not re-fetch) {0}" -f $file); $failed++; $failList += "unverified  $url" }
        default { Log ("[$done] {0}  {1}" -f $cc.verdict, $file) }
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
$lines += "- This run: $done processed, $newOk verified OK, $skipped skipped (no content), $failed need attention"
$lines += ""
if ($failList.Count -gt 0) {
    $lines += "## Needs attention (this run)"
    foreach ($f in $failList) { $lines += "- $f" }
} else {
    $lines += "All pages this run verified OK."
}
$lines | Set-Content -Path $summaryPath -Encoding utf8

Log "DONE: $done processed | $newOk verified OK | $skipped skipped | $failed need attention | $total total archived"
Log "summary: $summaryPath"
Log "log: $logPath"
exit 0
