# Transcribe Videos - batch becky-transcribe for Becky Review.
# Drag video files or folders onto "Transcribe Videos.bat" (or double-click it and pick a folder).
# Each video gets <name>_parakeet_transcription.srt next to it - the same file Becky Review's
# "+" button makes - so Becky Review shows it right away. Videos that already have one are skipped.
# Plain transcription only: no speakers, no forensic check. One video at a time so the
# graphics card is not split between jobs. ASCII-only (PowerShell 5.1 reads this as ANSI).

$exts = @('.mp4', '.mkv', '.mov', '.webm', '.m4v', '.avi', '.flv', '.ts')
$exe = Join-Path $env:USERPROFILE 'bin\becky-transcribe.exe'
if (-not (Test-Path $exe)) {
    Write-Host "becky-transcribe.exe was not found in $env:USERPROFILE\bin. Run build-all-tools.bat first." -ForegroundColor Red
    exit 1
}

$inputs = @($args)
if ($inputs.Count -eq 0) {
    $shell = New-Object -ComObject Shell.Application
    $pick = $shell.BrowseForFolder(0, 'Pick the folder with the videos to transcribe', 0)
    if ($null -eq $pick) { Write-Host 'No folder picked.'; exit 0 }
    $inputs = @($pick.Self.Path)
}

$videos = @()
foreach ($p in $inputs) {
    if (Test-Path $p -PathType Container) {
        $videos += Get-ChildItem -LiteralPath $p -File | Where-Object { $exts -contains $_.Extension.ToLower() } | Sort-Object Name | ForEach-Object { $_.FullName }
    } elseif (Test-Path $p -PathType Leaf) {
        $videos += (Resolve-Path -LiteralPath $p).Path
    }
}
if ($videos.Count -eq 0) { Write-Host 'No videos found.' -ForegroundColor Yellow; exit 0 }

$done = 0; $skipped = 0; $failed = @()
$i = 0
foreach ($v in $videos) {
    $i++
    $dir = Split-Path -Parent $v
    $stem = [System.IO.Path]::GetFileNameWithoutExtension($v)
    $srt = Join-Path $dir ($stem + '_parakeet_transcription.srt')
    if (Test-Path -LiteralPath $srt) {
        Write-Host "[$i of $($videos.Count)] Already transcribed, skipping: $stem" -ForegroundColor DarkGray
        $skipped++
        continue
    }
    Write-Host ''
    Write-Host "[$i of $($videos.Count)] Transcribing: $stem" -ForegroundColor Cyan
    $started = Get-Date
    & $exe $v --format srt --output $srt
    $mins = [math]::Round(((Get-Date) - $started).TotalMinutes, 1)
    if ($LASTEXITCODE -eq 0 -and (Test-Path -LiteralPath $srt)) {
        Write-Host "Done in $mins min: $srt" -ForegroundColor Green
        $done++
    } else {
        Write-Host "FAILED after $mins min: $stem" -ForegroundColor Red
        $failed += $stem
    }
}

Write-Host ''
Write-Host "Finished. Transcribed $done, skipped $skipped (already had one), failed $($failed.Count)." -ForegroundColor Cyan
foreach ($f in $failed) { Write-Host "  failed: $f" -ForegroundColor Red }
