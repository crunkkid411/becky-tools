# model-heartbeat.ps1 - every 30 min, prove the free models still answer.
#
# WHY: every free route is unreliable - they rate-limit, get marked DEGRADED, and
# expire without notice. Finding that out mid-task costs Jordan a night. This
# checks them on a schedule so a dead route is known BEFORE it is needed, and
# writes a machine-readable status any agent can read in one line.
#
# FREE OR OAUTH ONLY. This never calls a paid model - it filters the catalogue to
# ':free' ids, so it cannot spend money even if the list changes.
#
# Output: X:\AI-2\fleet\model-heartbeat.json  (+ .log, appended)
# Run:    pwsh -NoProfile -File X:\AI-2\fleet\model-heartbeat.ps1

$ErrorActionPreference = 'Continue'
$outJson = 'X:\AI-2\fleet\model-heartbeat.json'
$outLog  = 'X:\AI-2\fleet\model-heartbeat.log'
$stamp   = (Get-Date).ToString('s')

function Log($m) { "$stamp  $m" | Add-Content $outLog -Encoding utf8 }

$key = $env:OPENROUTER_API_KEY
if (-not $key) {
    $keyFile = "$env:USERPROFILE\.claude\openrouter_key.bin"
    if (Test-Path $keyFile) {
        try {
            $sec = Get-Content $keyFile | ConvertTo-SecureString
            $key = [Runtime.InteropServices.Marshal]::PtrToStringAuto(
                [Runtime.InteropServices.Marshal]::SecureStringToBSTR($sec))
        } catch { }
    }
}

$results = @()

if ($key) {
    # Only ever test models the catalogue says are FREE.
    $free = @()
    try {
        $cat = Invoke-RestMethod -Uri 'https://openrouter.ai/api/v1/models' `
                 -Headers @{ Authorization = "Bearer $key" } -TimeoutSec 30
        $free = $cat.data.id | Where-Object { $_ -like '*:free' }
    } catch {
        Log "catalogue unreachable: $($_.Exception.Message)"
    }

    # The ones becky-subtitle actually rotates through, if still offered.
    $want = @('tencent/hy3:free',
              'nvidia/nemotron-3-ultra-550b-a55b:free',
              'google/gemma-4-31b-it:free') | Where-Object { $free -contains $_ }

    foreach ($m in $want) {
        $sw = [Diagnostics.Stopwatch]::StartNew()
        $ok = $false; $err = ''
        try {
            $body = @{
                model      = $m
                messages   = @(@{ role = 'user'; content = 'Reply with exactly: OK' })
                max_tokens = 10
            } | ConvertTo-Json -Depth 5
            $r = Invoke-RestMethod -Uri 'https://openrouter.ai/api/v1/chat/completions' `
                   -Method Post -TimeoutSec 45 `
                   -Headers @{ Authorization = "Bearer $key"; 'Content-Type' = 'application/json' } `
                   -Body $body
            $ok = [bool]$r.choices[0].message.content
        } catch { $err = $_.Exception.Message }
        $results += [pscustomobject]@{
            model = $m; ok = $ok
            seconds = [Math]::Round($sw.Elapsed.TotalSeconds, 1)
            error = $err
        }
        Log ("{0,-42} ok={1} {2}s {3}" -f $m, $ok, [Math]::Round($sw.Elapsed.TotalSeconds,1), $err)
    }
} else {
    Log 'no OPENROUTER_API_KEY - skipped'
}

$healthy = @($results | Where-Object { $_.ok }).Count
[pscustomobject]@{
    checked     = $stamp
    healthy     = $healthy
    total       = $results.Count
    all_down    = ($results.Count -gt 0 -and $healthy -eq 0)
    models      = $results
} | ConvertTo-Json -Depth 6 | Set-Content $outJson -Encoding utf8

Log "healthy $healthy/$($results.Count)"
if ($results.Count -gt 0 -and $healthy -eq 0) { Log 'ALL FREE ROUTES DOWN' }
